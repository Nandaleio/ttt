package dap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

type SessionOptions struct {
	// OnEvent receives every event except initialized, which the session
	// consumes for the handshake. It runs on the read loop.
	OnEvent          func(Event)
	OnReverseRequest func(command string, args json.RawMessage) (any, error)
	OnClose          func(err error)
	MaxMessageSize   int
}

type LaunchConfig struct {
	AdapterID string
	// Request is "launch" or "attach".
	Request string
	// Arguments is passed through as the launch/attach arguments untouched;
	// their shape belongs to the adapter.
	Arguments   json.RawMessage
	Breakpoints map[string][]SourceBreakpoint
}

type Session struct {
	client      *Client
	caps        Capabilities
	initialized chan struct{}
	initOnce    sync.Once
}

func NewSession(r io.Reader, w io.Writer, opts SessionOptions) *Session {
	s := &Session{initialized: make(chan struct{})}
	s.client = NewClient(r, w, Options{
		OnEvent: func(ev Event) {
			if ev.Name == "initialized" {
				s.initOnce.Do(func() { close(s.initialized) })
				return
			}
			if opts.OnEvent != nil {
				opts.OnEvent(ev)
			}
		},
		OnReverseRequest: opts.OnReverseRequest,
		OnClose:          opts.OnClose,
		MaxMessageSize:   opts.MaxMessageSize,
	})
	return s
}

func (s *Session) Done() <-chan struct{}      { return s.client.Done() }
func (s *Session) Capabilities() Capabilities { return s.caps }

// Start runs the handshake in the order adapters expect: launch is sent
// without waiting for its response, because many adapters (debugpy, js-debug)
// only answer it after configurationDone; breakpoints go in between, once
// the adapter signals initialized. It returns the verified breakpoints per
// path.
func (s *Session) Start(ctx context.Context, cfg LaunchConfig) (map[string][]Breakpoint, error) {
	if cfg.Request != "launch" && cfg.Request != "attach" {
		return nil, fmt.Errorf("debug request must be launch or attach, not %q", cfg.Request)
	}
	err := s.client.call(ctx, "initialize", InitializeArguments{
		ClientID:             "ttt",
		ClientName:           "ttt",
		AdapterID:            cfg.AdapterID,
		LinesStartAt1:        true,
		ColumnsStartAt1:      true,
		PathFormat:           "path",
		SupportsVariableType: true,
	}, &s.caps)
	if err != nil {
		return nil, err
	}

	args := cfg.Arguments
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	launched := make(chan error, 1)
	go func() {
		_, err := s.client.Request(ctx, cfg.Request, args)
		launched <- err
	}()

	select {
	case <-s.initialized:
	case err := <-launched:
		if err != nil {
			return nil, err
		}
		// Launch answered first; initialized must still precede configuration.
		select {
		case <-s.initialized:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.Done():
			return nil, ErrClosed
		}
		launched <- nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.Done():
		return nil, ErrClosed
	}

	verified := make(map[string][]Breakpoint, len(cfg.Breakpoints))
	for path, lines := range cfg.Breakpoints {
		bps, err := s.SetBreakpoints(ctx, path, lines)
		if err != nil {
			return nil, err
		}
		verified[path] = bps
	}
	if s.caps.SupportsConfigurationDoneRequest {
		if err := s.client.call(ctx, "configurationDone", nil, nil); err != nil {
			return nil, err
		}
	}
	select {
	case err := <-launched:
		if err != nil {
			return nil, err
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return verified, nil
}

func (s *Session) SetBreakpoints(ctx context.Context, path string, lines []SourceBreakpoint) ([]Breakpoint, error) {
	if lines == nil {
		lines = []SourceBreakpoint{}
	}
	var out struct {
		Breakpoints []Breakpoint `json:"breakpoints"`
	}
	err := s.client.call(ctx, "setBreakpoints", SetBreakpointsArguments{
		Source:      Source{Path: path},
		Breakpoints: lines,
	}, &out)
	return out.Breakpoints, err
}

func (s *Session) Threads(ctx context.Context) ([]Thread, error) {
	var out struct {
		Threads []Thread `json:"threads"`
	}
	err := s.client.call(ctx, "threads", nil, &out)
	return out.Threads, err
}

func (s *Session) StackTrace(ctx context.Context, threadID int) ([]StackFrame, error) {
	var out struct {
		StackFrames []StackFrame `json:"stackFrames"`
	}
	err := s.client.call(ctx, "stackTrace", map[string]int{"threadId": threadID}, &out)
	return out.StackFrames, err
}

func (s *Session) Scopes(ctx context.Context, frameID int) ([]Scope, error) {
	var out struct {
		Scopes []Scope `json:"scopes"`
	}
	err := s.client.call(ctx, "scopes", map[string]int{"frameId": frameID}, &out)
	return out.Scopes, err
}

func (s *Session) Variables(ctx context.Context, ref int) ([]Variable, error) {
	var out struct {
		Variables []Variable `json:"variables"`
	}
	err := s.client.call(ctx, "variables", map[string]int{"variablesReference": ref}, &out)
	return out.Variables, err
}

func (s *Session) threadCommand(ctx context.Context, command string, threadID int) error {
	return s.client.call(ctx, command, map[string]int{"threadId": threadID}, nil)
}

func (s *Session) Continue(ctx context.Context, threadID int) error {
	return s.threadCommand(ctx, "continue", threadID)
}

func (s *Session) Next(ctx context.Context, threadID int) error {
	return s.threadCommand(ctx, "next", threadID)
}

func (s *Session) StepIn(ctx context.Context, threadID int) error {
	return s.threadCommand(ctx, "stepIn", threadID)
}

func (s *Session) StepOut(ctx context.Context, threadID int) error {
	return s.threadCommand(ctx, "stepOut", threadID)
}

func (s *Session) Pause(ctx context.Context, threadID int) error {
	return s.threadCommand(ctx, "pause", threadID)
}

// Stop ends the debuggee: terminate when the adapter supports it, so the
// program can clean up, otherwise disconnect, which kills a launched
// debuggee.
func (s *Session) Stop(ctx context.Context) error {
	if s.caps.SupportsTerminateRequest {
		if err := s.client.call(ctx, "terminate", nil, nil); err == nil {
			return nil
		}
	}
	return s.client.call(ctx, "disconnect", map[string]bool{"terminateDebuggee": true}, nil)
}
