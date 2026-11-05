package dap

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"

	"github.com/eugenioenko/ttt/internal/jsonrpc"
)

var ErrClosed = errors.New("debug adapter connection closed")

// Options are passed to NewClient because the read loop starts inside it: an
// adapter that writes immediately must not be handled before they are set.
type Options struct {
	// OnEvent runs on the read loop, so events arrive in order; it must not
	// block on a Request to the same adapter.
	OnEvent func(Event)
	// OnReverseRequest answers adapter-initiated requests such as
	// runInTerminal. It runs on its own goroutine. Nil rejects them all.
	OnReverseRequest func(command string, args json.RawMessage) (any, error)
	// OnClose runs once when the read loop ends, after pending Requests are
	// released and before Done is closed.
	OnClose func(err error)
	// MaxMessageSize bounds incoming frames in bytes; 0 means no limit.
	MaxMessageSize int
}

type Client struct {
	codec   *jsonrpc.Codec
	opts    Options
	mu      sync.Mutex
	seq     int
	pending map[int]chan message
	closed  bool
	done    chan struct{}
}

func NewClient(r io.Reader, w io.Writer, opts Options) *Client {
	codec := jsonrpc.NewCodec(r, w)
	codec.SetMaxContentLength(opts.MaxMessageSize)
	c := &Client{
		codec:   codec,
		opts:    opts,
		pending: make(map[int]chan message),
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c
}

func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) nextSeq() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	return c.seq
}

func (c *Client) readLoop() {
	var readErr error
	defer func() {
		c.mu.Lock()
		c.closed = true
		for _, ch := range c.pending {
			close(ch)
		}
		c.pending = nil
		c.mu.Unlock()
		if c.opts.OnClose != nil {
			c.opts.OnClose(readErr)
		}
		close(c.done)
	}()
	for {
		body, err := c.codec.ReceiveFrame()
		if err != nil {
			readErr = err
			slog.Debug("dap read loop exit", "err", err)
			return
		}
		var msg message
		if err := json.Unmarshal(body, &msg); err != nil {
			slog.Debug("dap: discarding malformed message", "err", err)
			continue
		}
		switch msg.Type {
		case "response":
			c.mu.Lock()
			ch, ok := c.pending[msg.RequestSeq]
			delete(c.pending, msg.RequestSeq)
			c.mu.Unlock()
			if ok {
				ch <- msg
			}
		case "event":
			if c.opts.OnEvent != nil {
				c.opts.OnEvent(Event{Name: msg.Event, Body: msg.Body})
			}
		case "request":
			go c.answer(msg)
		}
	}
}

func (c *Client) answer(req message) {
	var result any
	err := errors.New("not supported")
	if c.opts.OnReverseRequest != nil {
		result, err = c.opts.OnReverseRequest(req.Command, req.Arguments)
	}
	resp := response{
		Seq:        c.nextSeq(),
		Type:       "response",
		RequestSeq: req.Seq,
		Success:    err == nil,
		Command:    req.Command,
		Body:       result,
	}
	if err != nil {
		resp.Message = err.Error()
	}
	if sendErr := c.codec.Send(resp); sendErr != nil {
		slog.Debug("dap: reply to reverse request failed", "command", req.Command, "err", sendErr)
	}
}

// Request sends command and waits for its response body. A failed response
// is returned as *ResponseError.
func (c *Client) Request(ctx context.Context, command string, args any) (json.RawMessage, error) {
	ch := make(chan message, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	c.seq++
	seq := c.seq
	c.pending[seq] = ch
	c.mu.Unlock()

	if err := c.codec.Send(request{Seq: seq, Type: "request", Command: command, Arguments: args}); err != nil {
		c.forget(seq)
		return nil, err
	}
	select {
	case msg, ok := <-ch:
		if !ok {
			return nil, ErrClosed
		}
		if !msg.Success {
			return nil, &ResponseError{Command: command, Message: errorMessage(msg)}
		}
		return msg.Body, nil
	case <-ctx.Done():
		c.forget(seq)
		return nil, ctx.Err()
	}
}

func (c *Client) forget(seq int) {
	c.mu.Lock()
	delete(c.pending, seq)
	c.mu.Unlock()
}

// call decodes the response body into out when out is non-nil.
func (c *Client) call(ctx context.Context, command string, args any, out any) error {
	body, err := c.Request(ctx, command, args)
	if err != nil || out == nil || len(body) == 0 {
		return err
	}
	return json.Unmarshal(body, out)
}
