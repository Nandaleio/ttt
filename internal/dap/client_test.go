package dap

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"
)

func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestRequestMatchesResponseBySeq(t *testing.T) {
	_, r, w := newFakePair(t, func(_ *fakeAdapter, req message) (any, error) {
		return map[string]string{"command": req.Command}, nil
	})
	c := NewClient(r, w, Options{})
	for _, cmd := range []string{"threads", "scopes"} {
		body, err := c.Request(testCtx(t), cmd, nil)
		if err != nil {
			t.Fatal(err)
		}
		var got struct{ Command string }
		_ = json.Unmarshal(body, &got)
		if got.Command != cmd {
			t.Fatalf("response for %q carried %q", cmd, got.Command)
		}
	}
}

func TestFailedResponseUsesErrorFormat(t *testing.T) {
	_, r, w := newFakePair(t, func(_ *fakeAdapter, _ message) (any, error) {
		return nil, errors.New("no such thread")
	})
	c := NewClient(r, w, Options{})
	_, err := c.Request(testCtx(t), "continue", nil)
	var re *ResponseError
	if !errors.As(err, &re) || re.Message != "no such thread" {
		t.Fatalf("err = %v, want ResponseError with the body's error.format", err)
	}
}

func TestEventsArriveInOrder(t *testing.T) {
	_, r, w := newFakePair(t, func(f *fakeAdapter, _ message) (any, error) {
		for _, out := range []string{"a", "b", "c"} {
			f.event("output", OutputEvent{Output: out})
		}
		return nil, nil
	})
	var got []string
	c := NewClient(r, w, Options{OnEvent: func(ev Event) {
		var out OutputEvent
		_ = json.Unmarshal(ev.Body, &out)
		got = append(got, out.Output)
	}})
	if _, err := c.Request(testCtx(t), "threads", nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("events = %v", got)
	}
}

func TestReverseRequestIsRejectedByDefault(t *testing.T) {
	f, r, w := newFakePair(t, func(_ *fakeAdapter, _ message) (any, error) {
		return nil, nil
	})
	NewClient(r, w, Options{})
	f.reverse("runInTerminal")
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Contains(f.commands(), "response:runInTerminal") {
		if time.Now().After(deadline) {
			t.Fatalf("no reply to the reverse request; saw %v", f.commands())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCloseReleasesPendingRequests(t *testing.T) {
	f, r, w := newFakePair(t, func(_ *fakeAdapter, _ message) (any, error) {
		return nil, errDefer
	})
	closed := make(chan error, 1)
	c := NewClient(r, w, Options{OnClose: func(err error) { closed <- err }})
	result := make(chan error, 1)
	go func() {
		_, err := c.Request(testCtx(t), "continue", nil)
		result <- err
	}()
	for !slices.Contains(f.commands(), "request:continue") {
		time.Sleep(10 * time.Millisecond)
	}
	f.closer.Close()
	if err := <-result; !errors.Is(err, ErrClosed) {
		t.Fatalf("pending request err = %v, want ErrClosed", err)
	}
	<-closed
	if _, err := c.Request(testCtx(t), "threads", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("request after close err = %v, want ErrClosed", err)
	}
}

func TestRequestHonorsContext(t *testing.T) {
	_, r, w := newFakePair(t, func(_ *fakeAdapter, _ message) (any, error) {
		return nil, errDefer
	})
	c := NewClient(r, w, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.Request(ctx, "pause", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}
