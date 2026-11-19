package facet

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/facet-framework/core/internal/sourceview"
)

func sourceConfigFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "product")
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", source}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	run("init", "-q")
	run("config", "user.name", "Fixture")
	run("config", "user.email", "fixture@example.invalid")
	writeFile(t, filepath.Join(source, "source.go"), "package source\n")
	run("add", ".")
	run("commit", "-qm", "source")
	writeFile(t, ConfigPath(root), `records: docs/facets
sources: [src]
source_views:
  registry_repository: records
  repositories:
    records: {description: Source of the feature contracts}
    product: {description: Product source}
  default_view: current
  views:
    current:
      sources:
        product: {binding: product}
    alternate:
      sources:
        product: {binding: product}
`)
	writeFile(t, filepath.Join(root, sourceview.LocalName), "bindings:\n  product:\n    repository: product\n    checkout: product\n")
	return root, source
}
func TestSourceViewConfigKeepsRecordsRootAndProjectsOneCoherentSelection(t *testing.T) {
	root, source := sourceConfigFixture(t)
	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	reg := cfg.All()[0]
	if reg.Root != root || reg.RecordsDir != filepath.Join(root, "docs/facets") {
		t.Fatal("source checkout replaced record ownership")
	}
	if len(reg.Sources) != 1 || reg.Sources[0] != "src" {
		t.Fatal("legacy source filters were rewritten")
	}
	if reg.SourceView == nil || reg.SourceView.Name != "current" || reg.SourceView.RegistryRepository != "records" {
		t.Fatal("selected view identity missing")
	}
	selected, err := reg.SourceView.Source("product")
	if err != nil || selected.Root != source {
		t.Fatalf("wrong checkout selected: %+v %v", selected, err)
	}
	if reg.ScanOptions().View != reg.SourceView {
		t.Fatal("scanner did not receive the invocation's same view")
	}
	t.Setenv(sourceview.EnvName, "alternate")
	other, err := LoadConfig(root)
	if err != nil || other.All()[0].SourceView.Name != "alternate" {
		t.Fatal("environment selection was ignored")
	}
	explicit, err := LoadConfigWithSourceView(root, "current")
	if err != nil || explicit.All()[0].SourceView.Name != "current" {
		t.Fatal("explicit invocation selection did not take precedence")
	}
}
func TestSourceViewConfigurationErrorsNeverFallBackToLegacyScanning(t *testing.T) {
	root, _ := sourceConfigFixture(t)
	if err := os.Remove(filepath.Join(root, sourceview.LocalName)); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenAll(root); err == nil {
		t.Fatal("unavailable selected binding silently accepted")
	}
	for _, reg := range []Registry{Open(root), OpenSet(root).Primary()} {
		if reg.SourceView == nil || reg.ValidateSourceView() == nil {
			t.Fatal("legacy accessor discarded source-view failure")
		}
		if _, err := ScanAnchors(reg.Root, reg.ScanOptions()); err == nil {
			t.Fatal("failed source-view scan reverted to registry workspace")
		}
	}
}
func TestInvalidSourceViewBlockDoesNotHideBehindConfigFallback(t *testing.T) {
	root, _ := sourceConfigFixture(t)
	raw, err := os.ReadFile(ConfigPath(root))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, ConfigPath(root), strings.Replace(string(raw), "default_view: current", "checkout: /personal/location\n  default_view: current", 1))
	if _, err := LoadConfig(root); err == nil {
		t.Fatal("personal checkout location entered shared source_views")
	}
	reg := Open(root)
	if reg.SourceView == nil || reg.ValidateSourceView() == nil {
		t.Fatal("source-view decode error became a valid legacy registry")
	}
}
func TestSourceViewOptionsPassTheShippedConfigurationSchema(t *testing.T) {
	root, _ := sourceConfigFixture(t)
	copySchemas(t, root)
	compiled, err := compileSchema(filepath.Join(root, "docs/facets/schema/facetrc.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	data := map[string]any{"source_views": map[string]any{"repositories": map[string]any{"backend": map[string]any{}}, "default_view": "current", "views": map[string]any{"current": map[string]any{"sources": map[string]any{"backend": map[string]any{"binding": "local", "ref": "main"}}}}}, "runners": map[string]any{"unit": map[string]any{"repository": "backend", "command": "go test ./..."}}}
	if err := compiled.Validate(data); err != nil {
		encoded, _ := json.Marshal(data)
		t.Fatalf("shipped schema refused source model %s: %v", encoded, err)
	}
	data["source_views"].(map[string]any)["checkout"] = "/personal/location"
	if err := compiled.Validate(data); err == nil {
		t.Fatal("schema allowed personal bindings in shared config")
	}
}

func TestFrozenSourceViewDoesNotReresolveChangedLocalBindings(t *testing.T) {
	root, _ := sourceConfigFixture(t)
	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	view := cfg.All()[0].SourceView
	writeFile(t, filepath.Join(root, sourceview.LocalName), "bindings: {}\n")
	frozen, err := LoadConfigWithResolvedSourceView(root, view)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.All()[0].SourceView != view {
		t.Fatal("invocation selected another source context")
	}
	if _, err := LoadConfig(root); err == nil {
		t.Fatal("a fresh invocation did not observe changed bindings")
	}
	raw, err := os.ReadFile(ConfigPath(root))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, ConfigPath(root), strings.Replace(string(raw), "binding: product", "binding: other", 1))
	if _, err := LoadConfigWithResolvedSourceView(root, view); err == nil {
		t.Fatal("frozen view silently accepted changed shared selection")
	}
}
