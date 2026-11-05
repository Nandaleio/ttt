package dap

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"testing"
)

// deferredLaunch behaves like debugpy: launch is answered only after
// configurationDone, and initialized follows the initialize response.
func deferredLaunch(t *testing.T) (*fakeAdapter, *Session) {
	var launch message
	f, r, w := newFakePair(t, func(f *fakeAdapter, req message) (any, error) {
		switch req.Command {
		case "initialize":
			f.respond(req, Capabilities{SupportsConfigurationDoneRequest: true}, nil)
			f.event("initialized", nil)
			return nil, errDefer
		case "launch":
			launch = req
			return nil, errDefer
		case "setBreakpoints":
			var args SetBreakpointsArguments
			_ = json.Unmarshal(req.Arguments, &args)
			bps := make([]Breakpoint, len(args.Breakpoints))
			for i, sb := range args.Breakpoints {
				bps[i] = Breakpoint{ID: i + 1, Verified: true, Line: sb.Line}
			}
			return map[string]any{"breakpoints": bps}, nil
		case "configurationDone":
			f.respond(req, nil, nil)
			f.respond(launch, nil, nil)
			return nil, errDefer
		}
		return nil, nil
	})
	return f, NewSession(r, w, SessionOptions{})
}

func TestStartOrdersHandshakeForDeferredLaunch(t *testing.T) {
	f, s := deferredLaunch(t)
	verified, err := s.Start(testCtx(t), LaunchConfig{
		AdapterID:   "fake",
		Request:     "launch",
		Arguments:   json.RawMessage(`{"program":"main.go"}`),
		Breakpoints: map[string][]SourceBreakpoint{"/src/main.go": {{Line: 3}, {Line: 7}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"request:initialize", "request:launch", "request:setBreakpoints", "request:configurationDone"}
	if got := f.commands(); !slices.Equal(got, want) {
		t.Fatalf("commands = %v, want %v", got, want)
	}
	if bps := verified["/src/main.go"]; len(bps) != 2 || !bps[1].Verified || bps[1].Line != 7 {
		t.Fatalf("verified breakpoints = %+v", bps)
	}
}

// Delve answers launch at once and only then sends initialized.
func TestStartWhenLaunchAnswersBeforeInitialized(t *testing.T) {
	f, r, w := newFakePair(t, func(f *fakeAdapter, req message) (any, error) {
		switch req.Command {
		case "initialize":
			return Capabilities{SupportsConfigurationDoneRequest: true}, nil
		case "launch":
			f.respond(req, nil, nil)
			f.event("initialized", nil)
			return nil, errDefer
		}
		return nil, nil
	})
	s := NewSession(r, w, SessionOptions{})
	if _, err := s.Start(testCtx(t), LaunchConfig{Request: "launch"}); err != nil {
		t.Fatal(err)
	}
	if got := f.commands(); got[len(got)-1] != "request:configurationDone" {
		t.Fatalf("configurationDone not sent last: %v", got)
	}
}

func TestStartReportsLaunchFailure(t *testing.T) {
	_, r, w := newFakePair(t, func(_ *fakeAdapter, req message) (any, error) {
		if req.Command == "launch" {
			return nil, errors.New("program not found")
		}
		return nil, nil
	})
	s := NewSession(r, w, SessionOptions{})
	_, err := s.Start(testCtx(t), LaunchConfig{Request: "launch"})
	var re *ResponseError
	if !errors.As(err, &re) || re.Message != "program not found" {
		t.Fatalf("err = %v, want the launch failure", err)
	}
}

func TestStartRejectsUnknownRequest(t *testing.T) {
	_, r, w := newFakePair(t, func(_ *fakeAdapter, _ message) (any, error) { return nil, nil })
	s := NewSession(r, w, SessionOptions{})
	if _, err := s.Start(testCtx(t), LaunchConfig{Request: "run"}); err == nil {
		t.Fatal("want an error for request \"run\"")
	}
}

func TestInitializedIsNotForwarded(t *testing.T) {
	var events []string
	_, r, w := newFakePair(t, func(f *fakeAdapter, req message) (any, error) {
		if req.Command == "initialize" {
			f.respond(req, Capabilities{}, nil)
			f.event("initialized", nil)
			f.event("stopped", StoppedEvent{Reason: "entry", ThreadID: 1})
			return nil, errDefer
		}
		return nil, nil
	})
	s := NewSession(r, w, SessionOptions{OnEvent: func(ev Event) { events = append(events, ev.Name) }})
	if _, err := s.Start(testCtx(t), LaunchConfig{Request: "attach"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(events, []string{"stopped"}) {
		t.Fatalf("forwarded events = %v", events)
	}
}

func TestStartAdapterTransports(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		transport string
		args      []string
	}{
		{TransportStdio, nil},
		{TransportTCP, []string{"${port}"}},
	} {
		t.Run(tc.transport, func(t *testing.T) {
			a, err := StartAdapter(testCtx(t), AdapterSpec{
				Command:   exe,
				Args:      tc.args,
				Transport: tc.transport,
				Env:       []string{"TTT_FAKE_DAP=" + tc.transport},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			c := NewClient(a, a, Options{})
			body, err := c.Request(context.Background(), "threads", nil)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != `{"echo":"threads"}` {
				t.Fatalf("body = %s", body)
			}
			a.Close()
			<-a.Exited()
		})
	}
}
