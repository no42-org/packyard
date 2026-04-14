package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "packyard-*.yml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write config: %v", err)
	}
	f.Close()
	return f.Name()
}

func TestLoad_DefaultVisibilityIsPrivate(t *testing.T) {
	path := writeConfig(t, "components:\n  - name: core\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	vis := cfg.ComponentVisibility()
	if got := vis["core"]; got != "private" {
		t.Errorf("expected visibility \"private\" for core, got %q", got)
	}
}

func TestLoad_InvalidVisibilityRejected(t *testing.T) {
	path := writeConfig(t, "components:\n  - name: core\n    visibility: restricted\n")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid visibility, got nil")
	}
}

func TestPublicComponents_ReturnsOnlyPublic(t *testing.T) {
	path := writeConfig(t, `components:
  - name: core
    visibility: public
  - name: minion
    visibility: private
  - name: sentinel
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	pub := cfg.PublicComponents()
	if !pub["core"] {
		t.Error("expected core in PublicComponents")
	}
	if pub["minion"] {
		t.Error("expected minion NOT in PublicComponents")
	}
	if pub["sentinel"] {
		t.Error("expected sentinel NOT in PublicComponents (no visibility = private)")
	}
}

func TestComponentVisibility_AllValues(t *testing.T) {
	path := writeConfig(t, `components:
  - name: core
    visibility: public
  - name: minion
    visibility: private
  - name: sentinel
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	vis := cfg.ComponentVisibility()
	cases := map[string]string{
		"core":     "public",
		"minion":   "private",
		"sentinel": "private",
	}
	for comp, want := range cases {
		if got := vis[comp]; got != want {
			t.Errorf("component %q: expected visibility %q, got %q", comp, want, got)
		}
	}
}

func TestLoad_NoComponents_Error(t *testing.T) {
	path := writeConfig(t, "components:\n")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty components, got nil")
	}
}

func TestLoad_WhitespaceOnlyNameSkipped(t *testing.T) {
	path := writeConfig(t, "components:\n  - name: \"  \"\n  - name: core\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.ComponentVisibility()["  "]; ok {
		t.Error("whitespace-only name should not appear in ComponentVisibility")
	}
	if _, ok := cfg.ComponentVisibility()["core"]; !ok {
		t.Error("expected core in ComponentVisibility")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nonexistent.yml"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}
