package dap

import (
	"encoding/json"
	"fmt"
)

// message is any frame read from the adapter. DAP is not JSON-RPC: requests,
// responses and events share one shape distinguished by Type.
type message struct {
	Seq        int             `json:"seq"`
	Type       string          `json:"type"`
	Command    string          `json:"command,omitempty"`
	Arguments  json.RawMessage `json:"arguments,omitempty"`
	RequestSeq int             `json:"request_seq,omitempty"`
	Success    bool            `json:"success,omitempty"`
	Message    string          `json:"message,omitempty"`
	Body       json.RawMessage `json:"body,omitempty"`
	Event      string          `json:"event,omitempty"`
}

type request struct {
	Seq       int    `json:"seq"`
	Type      string `json:"type"`
	Command   string `json:"command"`
	Arguments any    `json:"arguments,omitempty"`
}

// response always carries success, which the protocol requires even when false.
type response struct {
	Seq        int    `json:"seq"`
	Type       string `json:"type"`
	RequestSeq int    `json:"request_seq"`
	Success    bool   `json:"success"`
	Command    string `json:"command"`
	Message    string `json:"message,omitempty"`
	Body       any    `json:"body,omitempty"`
}

type Event struct {
	Name string
	Body json.RawMessage
}

type ResponseError struct {
	Command string
	Message string
}

func (e *ResponseError) Error() string {
	return fmt.Sprintf("dap %s failed: %s", e.Command, e.Message)
}

// errorMessage prefers the human-readable error.format from the body, which
// adapters fill while leaving message as a short code.
func errorMessage(m message) string {
	var body struct {
		Error *struct {
			Format string `json:"format"`
		} `json:"error"`
	}
	if len(m.Body) > 0 && json.Unmarshal(m.Body, &body) == nil && body.Error != nil && body.Error.Format != "" {
		return body.Error.Format
	}
	if m.Message != "" {
		return m.Message
	}
	return "unknown error"
}

type InitializeArguments struct {
	ClientID                     string `json:"clientID"`
	ClientName                   string `json:"clientName"`
	AdapterID                    string `json:"adapterID"`
	Locale                       string `json:"locale,omitempty"`
	LinesStartAt1                bool   `json:"linesStartAt1"`
	ColumnsStartAt1              bool   `json:"columnsStartAt1"`
	PathFormat                   string `json:"pathFormat"`
	SupportsVariableType         bool   `json:"supportsVariableType"`
	SupportsRunInTerminalRequest bool   `json:"supportsRunInTerminalRequest"`
}

type Capabilities struct {
	SupportsConfigurationDoneRequest bool `json:"supportsConfigurationDoneRequest"`
	SupportsTerminateRequest         bool `json:"supportsTerminateRequest"`
	SupportsRestartRequest           bool `json:"supportsRestartRequest"`
}

type Source struct {
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
}

type SourceBreakpoint struct {
	Line int `json:"line"`
}

type SetBreakpointsArguments struct {
	Source      Source             `json:"source"`
	Breakpoints []SourceBreakpoint `json:"breakpoints"`
}

type Breakpoint struct {
	ID       int    `json:"id,omitempty"`
	Verified bool   `json:"verified"`
	Message  string `json:"message,omitempty"`
	Line     int    `json:"line,omitempty"`
	Source   Source `json:"source,omitzero"`
}

type Thread struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type StackFrame struct {
	ID     int     `json:"id"`
	Name   string  `json:"name"`
	Source *Source `json:"source,omitempty"`
	Line   int     `json:"line"`
	Column int     `json:"column"`
}

type Scope struct {
	Name               string `json:"name"`
	VariablesReference int    `json:"variablesReference"`
	Expensive          bool   `json:"expensive"`
}

type Variable struct {
	Name               string `json:"name"`
	Value              string `json:"value"`
	Type               string `json:"type,omitempty"`
	VariablesReference int    `json:"variablesReference"`
}

type StoppedEvent struct {
	Reason            string `json:"reason"`
	Description       string `json:"description,omitempty"`
	ThreadID          int    `json:"threadId,omitempty"`
	AllThreadsStopped bool   `json:"allThreadsStopped,omitempty"`
	Text              string `json:"text,omitempty"`
}

type ContinuedEvent struct {
	ThreadID            int  `json:"threadId"`
	AllThreadsContinued bool `json:"allThreadsContinued,omitempty"`
}

type OutputEvent struct {
	Category string `json:"category,omitempty"`
	Output   string `json:"output"`
}

type ExitedEvent struct {
	ExitCode int `json:"exitCode"`
}

type BreakpointEvent struct {
	Reason     string     `json:"reason"`
	Breakpoint Breakpoint `json:"breakpoint"`
}
