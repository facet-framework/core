package sourceview

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

func command(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
func write(t *testing.T, root, path, text string) {
	t.Helper()
	name := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}
func repository(t *testing.T, root string) string {
	t.Helper()
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	command(t, root, "init", "-q")
	command(t, root, "config", "user.name", "Fixture")
	command(t, root, "config", "user.email", "fixture@example.invalid")
	write(t, root, ".gitignore", "ignored/\n")
	write(t, root, "shared.txt", "common old version\n")
	command(t, root, "add", ".")
	command(t, root, "commit", "-qm", "first")
	return command(t, root, "rev-parse", "HEAD")
}
func fixture(t *testing.T) (string, Config, LocalConfig, string, string) {
	t.Helper()
	base := t.TempDir()
	registry := filepath.Join(base, "registry")
	if err := os.MkdirAll(registry, 0755); err != nil {
		t.Fatal(err)
	}
	backend := filepath.Join(base, "backend")
	old := repository(t, backend)
	write(t, backend, "shared.txt", "common new version\n")
	write(t, backend, "new-only.txt", "new behavior\n")
	command(t, backend, "add", ".")
	command(t, backend, "commit", "-qm", "new behavior")
	newRevision := command(t, backend, "rev-parse", "HEAD")
	oldTree := filepath.Join(base, "backend-old")
	command(t, backend, "worktree", "add", "--detach", oldTree, old)
	frontend := filepath.Join(base, "frontend")
	repository(t, frontend)
	cfg := Config{Repositories: map[string]Repository{"records": {}, "backend": {}, "frontend": {}}, RegistryRepository: "records", DefaultView: "current", Views: map[string]View{"current": {Sources: map[string]Selection{"backend": {Binding: "backend-current", Ref: newRevision}, "frontend": {Binding: "frontend"}}}, "historical": {Sources: map[string]Selection{"backend": {Binding: "backend-old", Ref: old}, "frontend": {Binding: "frontend"}}}}}
	local := LocalConfig{Bindings: map[string]Binding{"backend-current": {Repository: "backend", Checkout: backend}, "backend-old": {Repository: "backend", Checkout: oldTree}, "frontend": {Repository: "frontend", Checkout: frontend}}}
	return registry, cfg, local, old, newRevision
}
func TestNamedViewsResolveOneCheckoutWithoutChangingReferences(t *testing.T) {
	root, cfg, local, old, newRevision := fixture(t)
	before, _ := json.Marshal(cfg)
	current, err := ResolveWithBindings(root, cfg, local, "")
	if err != nil {
		t.Fatal(err)
	}
	historical, err := ResolveWithBindings(root, cfg, local, "historical")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		view           *ResolvedView
		revision, text string
	}{{current, newRevision, "common new version\n"}, {historical, old, "common old version\n"}} {
		source, relative, err := item.view.ResolveReference("backend:shared.txt")
		if err != nil {
			t.Fatal(err)
		}
		if source.Revision != item.revision || relative != "shared.txt" {
			t.Fatal("view mixed repository versions")
		}
		raw, err := source.Read(relative)
		if err != nil || string(raw) != item.text {
			t.Fatalf("selected content: %q %v", raw, err)
		}
		ref, err := item.view.Qualify("backend", "shared.txt::case")
		if err != nil || ref != "backend:shared.txt::case" {
			t.Fatal("qualified reference changed")
		}
	}
	if _, _, err := current.ResolveReference("shared.txt"); err == nil {
		t.Fatal("ambiguous unqualified reference was guessed")
	}
	source, _ := historical.Source("backend")
	if _, err := source.Read("new-only.txt"); err == nil {
		t.Fatal("historical view borrowed a newer file")
	}
	after, _ := json.Marshal(cfg)
	if string(before) != string(after) {
		t.Fatal("resolving a view changed shared configuration")
	}
}
func TestLocalRelocationPreservesIdentityAndSnapshot(t *testing.T) {
	root, cfg, local, _, _ := fixture(t)
	first, err := ResolveWithBindings(root, cfg, local, "")
	if err != nil {
		t.Fatal(err)
	}
	original := local.Bindings["frontend"].Checkout
	moved := original + "-moved"
	if err := os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	binding := local.Bindings["frontend"]
	binding.Checkout = moved
	local.Bindings["frontend"] = binding
	second, err := ResolveWithBindings(root, cfg, local, "")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := first.Source("frontend")
	b, _ := second.Source("frontend")
	if a.Repository != b.Repository || a.ContentHash != b.ContentHash || a.Revision != b.Revision {
		t.Fatal("relocation changed portable identity/evidence")
	}
	raw, _ := json.Marshal(second)
	if strings.Contains(string(raw), moved) || strings.Contains(string(raw), root) {
		t.Fatal("serialized view leaked personal checkout locations")
	}
}
func TestDirtyContentAndChangesAfterResolutionAreDistinguished(t *testing.T) {
	root, cfg, local, _, _ := fixture(t)
	clean, err := ResolveWithBindings(root, cfg, local, "")
	if err != nil {
		t.Fatal(err)
	}
	source, _ := clean.Source("backend")
	write(t, source.Root, "ignored/log.txt", "not source evidence")
	if err := source.ValidateWorkingTree(); err != nil {
		t.Fatal("ignored artifact invalidated source view")
	}
	write(t, source.Root, "shared.txt", "dirty implementation\n")
	if err := source.ValidateWorkingTree(); err == nil {
		t.Fatal("changed content accepted as prior evidence")
	}
	if _, err := source.Read("shared.txt"); err == nil {
		t.Fatal("Read returned content from another snapshot")
	}
	dirty, err := ResolveWithBindings(root, cfg, local, "")
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := dirty.Source("backend")
	if !updated.Dirty || updated.Revision != source.Revision || updated.ContentHash == source.ContentHash {
		t.Fatal("dirty content did not retain independent revision/hash")
	}
	write(t, source.Root, "untracked.txt", "new source")
	if err := updated.ValidateWorkingTree(); err == nil {
		t.Fatal("new untracked source was not detected")
	}
}
func TestUnavailableAmbiguousOrWrongVersionBindingsFail(t *testing.T) {
	root, cfg, local, old, _ := fixture(t)
	bad := cfg
	bad.DefaultView = "missing"
	if _, err := ResolveWithBindings(root, bad, local, ""); err == nil {
		t.Fatal("unknown default selected")
	}
	bad = cfg
	bad.Views = map[string]View{"bad": {Sources: map[string]Selection{"backend": {Binding: "backend-current"}, "frontend": {Binding: "backend-current"}}}}
	bad.DefaultView = "bad"
	if _, err := ResolveWithBindings(root, bad, local, ""); err == nil {
		t.Fatal("duplicate binding selected")
	}
	for _, ref := range []string{old, "not-an-available-ref"} {
		bad = cfg
		bad.Views = map[string]View{"current": {Sources: map[string]Selection{"backend": {Binding: "backend-current", Ref: ref}}}}
		if _, err := ResolveWithBindings(root, bad, local, ""); err == nil {
			t.Fatalf("ref %q did not match checkout but passed", ref)
		}
	}
	delete(local.Bindings, "backend-current")
	if _, err := ResolveWithBindings(root, cfg, local, ""); err == nil {
		t.Fatal("missing local binding fell back to another worktree")
	}
}
func TestSharedAndLocalBindingsRejectUnknownConfigurationFields(t *testing.T) {
	for _, input := range []string{"repositories: {backend: {checkout: /personal/path}}\nviews: {}\n", "repositories: {}\nviews: {main: {sources: {}, checkout: /personal/path}}\n", "repositories: {}\nviews: {main: {sources: {backend: {binding: here, checkout: /personal/path}}}}\n"} {
		var cfg Config
		if err := yaml.Unmarshal([]byte(input), &cfg); err == nil {
			t.Fatalf("shared config accepted local/unknown field: %s", input)
		}
	}
	var local LocalConfig
	if err := yaml.Unmarshal([]byte("bindings: {here: {repository: backend, checkout: ., token: secret}}"), &local); err == nil {
		t.Fatal("local config accepted undeclared secret field")
	}
}
func TestSourceReferencesCannotEscapeRepository(t *testing.T) {
	root, cfg, local, _, _ := fixture(t)
	view, err := ResolveWithBindings(root, cfg, local, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"backend:../outside", "backend:/absolute", "backend:src/../file", "backend:C:\\private", "unknown:shared.txt"} {
		if _, _, err := view.ResolveReference(ref); err == nil {
			t.Fatalf("invalid reference accepted: %q", ref)
		}
	}
	source, _ := view.Source("backend")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(source.Root, "link.txt")); err != nil {
		t.Skip(err)
	}
	view, err = ResolveWithBindings(root, cfg, local, "")
	if err != nil {
		t.Fatal(err)
	}
	source, _ = view.Source("backend")
	if _, err := source.Read("link.txt"); err == nil {
		t.Fatal("source read followed an external symlink")
	}
}

func TestTwoWorktreesCannotMasqueradeAsIndependentRepositories(t *testing.T) {
	root, cfg, local, _, _ := fixture(t)
	previous := local.Bindings["backend-old"]
	previous.Repository = "frontend"
	local.Bindings["frontend"] = previous
	if _, err := ResolveWithBindings(root, cfg, local, ""); err == nil {
		t.Fatal("same repository aggregated through two logical identities")
	}
}
func TestTrackedPersonalBindingsAreRefused(t *testing.T) {
	root, cfg, local, _, _ := fixture(t)
	repository(t, root)
	raw, err := yaml.Marshal(local)
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, LocalName, string(raw))
	command(t, root, "add", LocalName)
	command(t, root, "commit", "-qm", "mistakenly tracked local bindings")
	if _, err := Resolve(root, cfg, ""); err == nil {
		t.Fatal("personal bindings were read from tracked configuration")
	}
}

func TestDuplicateYAMLRepositoryOrBindingKeysAreRefused(t *testing.T) {
	for _, input := range []string{"repositories: {backend: {}, backend: {}}\nviews: {}\n", "repositories: {backend: {}}\nviews: {current: {sources: {backend: {binding: first}, backend: {binding: second}}}}\n"} {
		var cfg Config
		if err := yaml.Unmarshal([]byte(input), &cfg); err == nil {
			t.Fatal("duplicate repository key accepted")
		}
	}
	var local LocalConfig
	if err := yaml.Unmarshal([]byte("bindings: {here: {repository: backend, checkout: a}, here: {repository: backend, checkout: b}}"), &local); err == nil {
		t.Fatal("duplicate local binding accepted")
	}
}

func TestUnselectedSubmoduleCheckoutDoesNotChangeParentSourceIdentity(t *testing.T) {
	root, cfg, local, _, _ := fixture(t)
	backend := local.Bindings["backend-current"].Checkout
	frontend := local.Bindings["frontend"].Checkout
	command(t, backend, "-c", "protocol.file.allow=always", "submodule", "add", frontend, "module")
	command(t, backend, "commit", "-qm", "declare dependency")
	selected := cfg.Views["current"]
	selection := selected.Sources["backend"]
	selection.Ref = command(t, backend, "rev-parse", "HEAD")
	selected.Sources["backend"] = selection
	initialized, err := ResolveWithBindings(root, cfg, local, "")
	if err != nil {
		t.Fatal(err)
	}
	command(t, backend, "submodule", "deinit", "--force", "--", "module")
	absent, err := ResolveWithBindings(root, cfg, local, "")
	if err != nil {
		t.Fatal(err)
	}
	first, _ := initialized.Source("backend")
	second, _ := absent.Source("backend")
	if first.ContentHash != second.ContentHash || first.Dirty != second.Dirty {
		t.Fatal("parent source aggregated an unselected submodule checkout")
	}
	if _, err := second.Read("module/shared.txt"); err == nil {
		t.Fatal("parent source borrowed files from another repository")
	}
}
