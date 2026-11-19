package facet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigUnresolvedValidatesWithoutLocalBindings(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FACET_SOURCE_VIEW", "unavailable")
	data := `hooks: {commit_gate: off}
source_views:
  repositories: {code: {}}
  views:
    selected:
      sources:
        code: {binding: local}
`
	if err := os.WriteFile(filepath.Join(root, ConfigName), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfigUnresolved(root)
	if err != nil || !cfg.CommitGateOff() {
		t.Fatalf("static policy unavailable: %v", err)
	}
	if _, err := LoadConfig(root); err == nil {
		t.Fatal("ordinary reader silently skipped local bindings")
	}
	if err := os.WriteFile(filepath.Join(root, ConfigName), []byte("source_views: {repositories: {}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfigUnresolved(root); err == nil {
		t.Fatal("invalid static source-view declaration accepted")
	}
}
