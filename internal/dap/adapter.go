package dap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	TransportStdio = "stdio"
	TransportTCP   = "tcp"

	portPlaceholder = "${port}"
	dialTimeout     = 10 * time.Second
	stopGrace       = 2 * time.Second
)

type AdapterSpec struct {
	Command string
	Args    []string
	// Transport is TransportStdio (default) or TransportTCP. For TCP a free
	// port replaces ${port} in Args and the adapter is dialed on loopback.
	Transport string
	Cwd       string
	Env       []string
	// Stderr receives the adapter's stderr, and its stdout under TCP.
	Stderr io.Writer
}

// Adapter is a running debug adapter process and the stream to talk to it.
type Adapter struct {
	io.Reader
	io.Writer
	cmd       *exec.Cmd
	conn      io.Closer
	exited    chan struct{}
	closeOnce sync.Once
}

func StartAdapter(ctx context.Context, spec AdapterSpec) (*Adapter, error) {
	if spec.Command == "" {
		return nil, errors.New("debug adapter has no command")
	}
	switch spec.Transport {
	case "", TransportStdio:
		return startStdio(spec)
	case TransportTCP:
		return startTCP(ctx, spec)
	default:
		return nil, fmt.Errorf("unknown debug adapter transport %q", spec.Transport)
	}
}

func newCmd(spec AdapterSpec, args []string) *exec.Cmd {
	cmd := exec.Command(spec.Command, args...)
	cmd.Dir = spec.Cwd
	if len(spec.Env) > 0 {
		cmd.Env = append(os.Environ(), spec.Env...)
	}
	cmd.Stderr = spec.Stderr
	return cmd
}

func (a *Adapter) wait() {
	a.exited = make(chan struct{})
	go func() {
		_ = a.cmd.Wait()
		close(a.exited)
	}()
}

func startStdio(spec AdapterSpec) (*Adapter, error) {
	cmd := newCmd(spec, spec.Args)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	a := &Adapter{Reader: stdout, Writer: stdin, cmd: cmd, conn: stdin}
	a.wait()
	return a, nil
}

func startTCP(ctx context.Context, spec AdapterSpec) (*Adapter, error) {
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	args := make([]string, len(spec.Args))
	for i, arg := range spec.Args {
		args[i] = strings.ReplaceAll(arg, portPlaceholder, strconv.Itoa(port))
	}
	cmd := newCmd(spec, args)
	cmd.Stdout = spec.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	a := &Adapter{cmd: cmd}
	a.wait()

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.Now().Add(dialTimeout)
	for {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			a.Reader, a.Writer, a.conn = conn, conn, conn
			return a, nil
		}
		select {
		case <-a.exited:
			return nil, fmt.Errorf("debug adapter exited before listening on %s", addr)
		case <-ctx.Done():
			a.kill()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			a.kill()
			return nil, fmt.Errorf("debug adapter did not listen on %s: %w", addr, err)
		}
	}
}

// freePort can race with another process taking the port before the adapter
// binds it; adapters that accept a port argument leave no better option.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func (a *Adapter) Exited() <-chan struct{} { return a.exited }

func (a *Adapter) kill() {
	if a.cmd.Process != nil {
		_ = a.cmd.Process.Kill()
	}
	<-a.exited
}

// Close closes the stream, gives the adapter a moment to exit on its own,
// then kills it.
func (a *Adapter) Close() error {
	a.closeOnce.Do(func() {
		if a.conn != nil {
			_ = a.conn.Close()
		}
		select {
		case <-a.exited:
		case <-time.After(stopGrace):
			a.kill()
		}
	})
	return nil
}
