package dap

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"testing"

	"github.com/eugenioenko/ttt/internal/jsonrpc"
)

// fakeAdapter speaks DAP on the far side of a pipe. handle answers each
// request; returning errDefer leaves it unanswered until respond is called.
type fakeAdapter struct {
	codec  *jsonrpc.Codec
	mu     sync.Mutex
	seq    int
	log    []string
	handle func(f *fakeAdapter, req message) (any, error)
	closer io.Closer
}

var errDefer = errors.New("deferred")

func newFakePair(t *testing.T, handle func(f *fakeAdapter, req message) (any, error)) (*fakeAdapter, io.Reader, io.Writer) {
	t.Helper()
	clientR, adapterW := io.Pipe()
	adapterR, clientW := io.Pipe()
	f := &fakeAdapter{codec: jsonrpc.NewCodec(adapterR, adapterW), handle: handle, closer: adapterW}
	t.Cleanup(func() { adapterW.Close(); clientW.Close() })
	go f.serve()
	return f, clientR, clientW
}

func (f *fakeAdapter) serve() {
	for {
		body, err := f.codec.ReceiveFrame()
		if err != nil {
			return
		}
		var req message
		if json.Unmarshal(body, &req) != nil {
			continue
		}
		f.mu.Lock()
		f.log = append(f.log, req.Type+":"+req.Command)
		f.mu.Unlock()
		if req.Type != "request" {
			continue
		}
		result, err := f.handle(f, req)
		if errors.Is(err, errDefer) {
			continue
		}
		f.respond(req, result, err)
	}
}

func (f *fakeAdapter) next() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	return f.seq
}

func (f *fakeAdapter) respond(req message, body any, err error) {
	resp := response{Seq: f.next(), Type: "response", RequestSeq: req.Seq, Success: err == nil, Command: req.Command, Body: body}
	if err != nil {
		resp.Message = "failed"
		resp.Body = map[string]any{"error": map[string]any{"id": 1, "format": err.Error()}}
	}
	_ = f.codec.Send(resp)
}

func (f *fakeAdapter) event(name string, body any) {
	_ = f.codec.Send(map[string]any{"seq": f.next(), "type": "event", "event": name, "body": body})
}

func (f *fakeAdapter) reverse(command string) {
	_ = f.codec.Send(map[string]any{"seq": f.next(), "type": "request", "command": command, "arguments": map[string]any{}})
}

func (f *fakeAdapter) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log...)
}

// TestMain lets the spawn tests run this binary as a stdio or TCP adapter.
func TestMain(m *testing.M) {
	switch os.Getenv("TTT_FAKE_DAP") {
	case "stdio":
		serveEcho(os.Stdin, os.Stdout)
		os.Exit(0)
	case "tcp":
		l, err := net.Listen("tcp", "127.0.0.1:"+os.Args[len(os.Args)-1])
		if err != nil {
			os.Exit(2)
		}
		conn, err := l.Accept()
		if err != nil {
			os.Exit(2)
		}
		serveEcho(conn, conn)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// serveEcho answers every request successfully with its own command name.
func serveEcho(r io.Reader, w io.Writer) {
	codec := jsonrpc.NewCodec(r, w)
	for seq := 1; ; seq++ {
		body, err := codec.ReceiveFrame()
		if err != nil {
			return
		}
		var req message
		if json.Unmarshal(body, &req) != nil {
			continue
		}
		_ = codec.Send(response{Seq: seq, Type: "response", RequestSeq: req.Seq, Success: true, Command: req.Command, Body: map[string]string{"echo": req.Command}})
	}
}
