package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type DebuggerSettings struct {
	Configurations []DebugConfiguration `json:"configurations,omitempty"`
}

// DebugConfiguration selects a plugin-contributed adapter by Type. Args are
// the adapter's own launch/attach arguments, passed through after variable
// substitution.
type DebugConfiguration struct {
	Name    string          `json:"name"`
	Type    string          `json:"type"`
	Request string          `json:"request"`
	Args    json.RawMessage `json:"args,omitempty"`
}

func (c DebugConfiguration) Validate() error {
	switch {
	case c.Name == "":
		return errors.New("debug configuration needs a name")
	case c.Type == "":
		return fmt.Errorf("debug configuration %q needs a type", c.Name)
	case c.Request != "launch" && c.Request != "attach":
		return fmt.Errorf("debug configuration %q: request must be \"launch\" or \"attach\"", c.Name)
	}
	return nil
}

// DebugVars are the ${name} values substituted into configuration args.
type DebugVars struct {
	WorkspaceFolder string
	File            string
	FileDirname     string
}

// ResolvedArgs substitutes ${workspaceFolder}, ${file} and ${fileDirname} in
// every string of Args, keys included. Unknown ${...} references are left
// as written so adapter-specific syntax passes through.
func (c DebugConfiguration) ResolvedArgs(vars DebugVars) (json.RawMessage, error) {
	if len(c.Args) == 0 {
		return json.RawMessage("{}"), nil
	}
	var v any
	if err := json.Unmarshal(c.Args, &v); err != nil {
		return nil, fmt.Errorf("debug configuration %q args: %w", c.Name, err)
	}
	r := strings.NewReplacer(
		"${workspaceFolder}", vars.WorkspaceFolder,
		"${fileDirname}", vars.FileDirname,
		"${file}", vars.File,
	)
	return json.Marshal(substitute(v, r))
}

func substitute(v any, r *strings.Replacer) any {
	switch t := v.(type) {
	case string:
		return r.Replace(t)
	case []any:
		for i := range t {
			t[i] = substitute(t[i], r)
		}
		return t
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[r.Replace(k)] = substitute(val, r)
		}
		return out
	}
	return v
}

func (d DebuggerSettings) Configuration(name string) (DebugConfiguration, bool) {
	for _, c := range d.Configurations {
		if c.Name == name {
			return c, true
		}
	}
	return DebugConfiguration{}, false
}
