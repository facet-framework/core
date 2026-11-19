package cli

import (
	"bytes"
	"encoding/json"
	"github.com/facet-framework/core/internal/docsite"
	"os"
	"path/filepath"
	"testing"
)

func TestDocsSiteRootPaths(t *testing.T) {
	root := t.TempDir()
	if e := os.MkdirAll(filepath.Join(root, "docs"), 0755); e != nil {
		t.Fatal(e)
	}
	c := docsite.Config{Version: 1, Title: "Guide", Audiences: []docsite.Audience{{ID: "new", Title: "New colleagues", Kind: "creator"}}}
	b, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, "docs/site.json"), b, 0644); e != nil {
		t.Fatal(e)
	}
	cmd := newDocsSiteCmd()
	cmd.PersistentFlags().String("root", root, "")
	cmd.SetArgs([]string{"build", "--output", "site"})
	cmd.SetOut(&bytes.Buffer{})
	if e = cmd.Execute(); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(root, "site/index.html")); e != nil {
		t.Fatal(e)
	}
	cmd = newDocsSiteCmd()
	cmd.PersistentFlags().String("root", root, "")
	cmd.SetArgs([]string{"check", "--output", "site"})
	cmd.SetOut(&bytes.Buffer{})
	if e = cmd.Execute(); e != nil {
		t.Fatal(e)
	}
}
