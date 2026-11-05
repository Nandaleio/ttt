package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDebugConfigurationValidate(t *testing.T) {
	ok := DebugConfiguration{Name: "run", Type: "go", Request: "launch"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []DebugConfiguration{
		{Type: "go", Request: "launch"},
		{Name: "run", Request: "launch"},
		{Name: "run", Type: "go", Request: "run"},
	} {
		if c.Validate() == nil {
			t.Errorf("%+v: want a validation error", c)
		}
	}
}

func TestResolvedArgsSubstitutesVariables(t *testing.T) {
	c := DebugConfiguration{Name: "run", Args: json.RawMessage(`{
		"program": "${workspaceFolder}/cmd",
		"args": ["${file}", 3, true],
		"env": {"DIR": "${fileDirname}", "KEEP": "${env:HOME}"}
	}`)}
	raw, err := c.ResolvedArgs(DebugVars{WorkspaceFolder: "/ws", File: "/ws/a/main.go", FileDirname: "/ws/a"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["program"] != "/ws/cmd" {
		t.Errorf("program = %v", got["program"])
	}
	if args := got["args"].([]any); args[0] != "/ws/a/main.go" || args[1] != 3.0 || args[2] != true {
		t.Errorf("args = %v", args)
	}
	env := got["env"].(map[string]any)
	if env["DIR"] != "/ws/a" || env["KEEP"] != "${env:HOME}" {
		t.Errorf("env = %v", env)
	}
}

func TestResolvedArgsDefaultsAndErrors(t *testing.T) {
	raw, err := DebugConfiguration{Name: "run"}.ResolvedArgs(DebugVars{})
	if err != nil || string(raw) != "{}" {
		t.Fatalf("empty args = %s, %v; want {}", raw, err)
	}
	_, err = DebugConfiguration{Name: "run", Args: json.RawMessage(`{`)}.ResolvedArgs(DebugVars{})
	if err == nil || !strings.Contains(err.Error(), "run") {
		t.Fatalf("err = %v, want a parse error naming the configuration", err)
	}
}

func TestDebuggerSettingsRoundTrip(t *testing.T) {
	raw := []byte(`{"version":1,"debugger":{"configurations":[{"name":"run","type":"go","request":"launch","args":{"program":"."}}]}}`)
	var s Settings
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	c, ok := s.Debugger.Configuration("run")
	if !ok || c.Type != "go" || string(c.Args) != `{"program":"."}` {
		t.Fatalf("configuration = %+v, %v", c, ok)
	}
	if _, ok := s.Extra["debugger"]; ok {
		t.Fatal("debugger was treated as a plugin key")
	}
	out, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"configurations":[{"name":"run"`) {
		t.Fatalf("debugger section lost on save: %s", out)
	}

	empty, _ := json.Marshal(DefaultSettings())
	if strings.Contains(string(empty), `"debugger"`) {
		t.Fatal("an empty debugger section should be omitted")
	}
}
