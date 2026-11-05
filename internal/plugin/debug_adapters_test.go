package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, dir, data string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.ttt.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadManifestDebuggersOnly(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{"name":"go-debug","permissions":{"system.exec":["dlv"]},
		"debuggers":[{"type":"go","label":"Delve","languages":["Go"],"command":"dlv","args":["dap","--listen","127.0.0.1:${port}"],"transport":"tcp"}]}`)
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Debuggers) != 1 {
		t.Fatalf("debuggers = %+v", m.Debuggers)
	}
	d := m.Debuggers[0]
	if d.Title() != "Delve" || !d.HandlesLanguage("go") || !d.HandlesLanguage("GO") {
		t.Fatalf("adapter = %+v", d)
	}
}

func TestLoadManifestRejectsInvalidDebuggers(t *testing.T) {
	for name, tc := range map[string]struct{ debuggers, want string }{
		"no type":          {`[{"command":"dlv"}]`, "type"},
		"no command":       {`[{"type":"go"}]`, "command"},
		"not in exec":      {`[{"type":"go","command":"gdb"}]`, "system.exec"},
		"duplicate type":   {`[{"type":"go","command":"dlv"},{"type":"go","command":"dlv"}]`, "duplicate"},
		"bad transport":    {`[{"type":"go","command":"dlv","transport":"pipe"}]`, "transport"},
		"tcp without port": {`[{"type":"go","command":"dlv","transport":"tcp","args":["dap"]}]`, "${port}"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeManifest(t, dir, `{"name":"p","permissions":{"system.exec":["dlv"]},"debuggers":`+tc.debuggers+`}`)
			_, err := LoadManifest(dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func loadDebuggerPlugins(t *testing.T, plugins map[string]string, registry string) *Manager {
	t.Helper()
	pluginsDir := t.TempDir()
	regPath := filepath.Join(t.TempDir(), "registry.json")
	for name, manifest := range plugins {
		writeManifest(t, filepath.Join(pluginsDir, name), manifest)
	}
	if err := os.WriteFile(regPath, []byte(registry), 0644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(pluginsDir, regPath)
	m.LoadAll()
	t.Cleanup(m.Shutdown)
	return m
}

func TestDebugAdaptersFromEnabledPlugins(t *testing.T) {
	m := loadDebuggerPlugins(t, map[string]string{
		"go-debug": `{"name":"go-debug","permissions":{"system.exec":["dlv"]},"debuggers":[{"type":"go","languages":["go"],"command":"dlv","args":["dap"]}]}`,
		"py-debug": `{"name":"py-debug","permissions":{"system.exec":["python3"]},"debuggers":[{"type":"python","languages":["python"],"command":"python3"}]}`,
	}, `[{"name":"go-debug","enabled":true,"permissions":{"system.exec":["dlv"]}},
	    {"name":"py-debug","enabled":false,"permissions":{"system.exec":["python3"]}}]`)

	got := m.DebugAdapters()
	if len(got) != 1 || got[0].Type != "go" || got[0].Plugin != "go-debug" {
		t.Fatalf("adapters = %+v, want only the enabled go adapter", got)
	}
	if _, ok := m.DebugAdapter("python"); ok {
		t.Error("disabled plugin's adapter is listed")
	}
	if !m.HasDebugAdapterForLanguage("go") || m.HasDebugAdapterForLanguage("python") || m.HasDebugAdapterForLanguage("") {
		t.Error("HasDebugAdapterForLanguage disagrees with the enabled adapters")
	}
}

func TestDebugAdapterNeedsGrantedExec(t *testing.T) {
	m := loadDebuggerPlugins(t, map[string]string{
		"go-debug": `{"name":"go-debug","permissions":{"system.exec":["dlv"]},"debuggers":[{"type":"go","command":"dlv"}]}`,
	}, `[{"name":"go-debug","enabled":true,"permissions":{"system.exec":["dlv"]}}]`)
	p := m.FindPlugin("go-debug")
	if p == nil {
		t.Fatal("plugin not loaded")
	}
	p.Granted.SystemExec = nil
	if len(m.DebugAdapters()) != 0 {
		t.Fatal("adapter listed after its command's exec grant was removed")
	}
}

func TestDebugAdapterRequiresApproval(t *testing.T) {
	m := loadDebuggerPlugins(t, map[string]string{
		"go-debug": `{"name":"go-debug","permissions":{"system.exec":["dlv"]},"debuggers":[{"type":"go","command":"dlv"}]}`,
	}, `[]`)
	if len(m.DebugAdapters()) != 0 {
		t.Fatal("an unapproved plugin's adapter is listed")
	}
}
