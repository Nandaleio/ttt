package plugin

import (
	"fmt"
	"slices"
	"strings"
)

const debugPortPlaceholder = "${port}"

// DebugAdapter is a Debug Adapter Protocol server a plugin contributes. The
// editor spawns Command itself, so it must be one of the plugin's system.exec
// entries: declaring an adapter grants no execution the user did not approve.
type DebugAdapter struct {
	Type      string   `json:"type"`
	Label     string   `json:"label,omitempty"`
	Languages []string `json:"languages,omitempty"`
	Command   string   `json:"command"`
	Args      []string `json:"args,omitempty"`
	// Transport is "stdio" (the default) or "tcp"; a tcp adapter must take
	// ${port} in Args so the editor knows where to connect.
	Transport string `json:"transport,omitempty"`
}

func (d DebugAdapter) Title() string {
	if d.Label != "" {
		return d.Label
	}
	return d.Type
}

func (d DebugAdapter) HandlesLanguage(lang string) bool {
	return slices.Contains(d.Languages, strings.ToLower(lang))
}

func validateDebugAdapters(adapters []DebugAdapter, perms PermissionSet) error {
	seen := make(map[string]bool, len(adapters))
	for i := range adapters {
		d := &adapters[i]
		if d.Type == "" {
			return fmt.Errorf("debugger missing required field: type")
		}
		if seen[d.Type] {
			return fmt.Errorf("duplicate debugger type %q", d.Type)
		}
		seen[d.Type] = true
		if d.Command == "" {
			return fmt.Errorf("debugger %q missing required field: command", d.Type)
		}
		if err := perms.CheckExec(d.Command); err != nil {
			return fmt.Errorf("debugger %q: command %q must be listed in system.exec", d.Type, d.Command)
		}
		switch d.Transport {
		case "", "stdio":
		case "tcp":
			if !slices.ContainsFunc(d.Args, func(a string) bool { return strings.Contains(a, debugPortPlaceholder) }) {
				return fmt.Errorf("debugger %q: tcp transport needs %s in args", d.Type, debugPortPlaceholder)
			}
		default:
			return fmt.Errorf("debugger %q: unknown transport %q", d.Type, d.Transport)
		}
		for j, lang := range d.Languages {
			d.Languages[j] = strings.ToLower(lang)
		}
	}
	return nil
}

type RegisteredDebugAdapter struct {
	Plugin string
	DebugAdapter
}

// DebugAdapters lists the adapters of enabled plugins whose command is still
// granted. When two plugins declare the same type, the first loaded wins.
func (m *Manager) DebugAdapters() []RegisteredDebugAdapter {
	var out []RegisteredDebugAdapter
	seen := map[string]bool{}
	for _, p := range m.plugins {
		if !p.Enabled {
			continue
		}
		for _, d := range p.Manifest.Debuggers {
			if seen[d.Type] || p.Granted.CheckExec(d.Command) != nil {
				continue
			}
			seen[d.Type] = true
			out = append(out, RegisteredDebugAdapter{Plugin: p.Name, DebugAdapter: d})
		}
	}
	return out
}

func (m *Manager) DebugAdapter(typ string) (RegisteredDebugAdapter, bool) {
	for _, d := range m.DebugAdapters() {
		if d.Type == typ {
			return d, true
		}
	}
	return RegisteredDebugAdapter{}, false
}

func (m *Manager) HasDebugAdapterForLanguage(lang string) bool {
	if lang == "" {
		return false
	}
	return slices.ContainsFunc(m.DebugAdapters(), func(d RegisteredDebugAdapter) bool {
		return d.HandlesLanguage(lang)
	})
}
