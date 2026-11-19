// @facet-ignore-file

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/facet-framework/core/corpus"
	"github.com/spf13/cobra"
)

type sourceViewIntegrationFixture struct {
	registry, backend, alternate, frontend string
	revisions                              map[string]string
}

func sourceViewIntegrationWrite(t *testing.T, root, path, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if e := os.MkdirAll(filepath.Dir(full), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(full, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
}
func sourceViewIntegrationGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v %s", args, e, out)
	}
	return strings.TrimSpace(string(out))
}
func sourceViewIntegrationRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	sourceViewIntegrationGit(t, root, "init", "-q")
	sourceViewIntegrationGit(t, root, "config", "user.email", "fixture@example.invalid")
	sourceViewIntegrationGit(t, root, "config", "user.name", "Fixture")
	for path, body := range files {
		sourceViewIntegrationWrite(t, root, path, body)
	}
	sourceViewIntegrationGit(t, root, "add", "-A")
	sourceViewIntegrationGit(t, root, "commit", "-qm", "fixture")
	return root
}
func sourceViewIntegrationSetup(t *testing.T) sourceViewIntegrationFixture {
	t.Helper()
	backend := sourceViewIntegrationRepo(t, map[string]string{"src/main.go": "package backend\n// @facet: sample/core/backend-old\nfunc Old(){}\n"})
	alternate := filepath.Join(t.TempDir(), "alternate")
	sourceViewIntegrationGit(t, backend, "worktree", "add", "-q", "-b", "selected-backend", alternate, "HEAD")
	sourceViewIntegrationWrite(t, alternate, "src/main.go", "package backend\n// @facet: sample/core/backend-new\nfunc New(){}\n")
	sourceViewIntegrationGit(t, alternate, "add", "src/main.go")
	sourceViewIntegrationGit(t, alternate, "commit", "-qm", "new backend")
	frontend := sourceViewIntegrationRepo(t, map[string]string{"src/main.go": "package frontend\n// @facet: sample/core/frontend\nfunc Frontend(){}\n"})
	records := map[string]string{".gitignore": ".facet/\n"}
	for _, name := range []string{"backend-old", "backend-new", "frontend"} {
		records["docs/facets/features/sample/core/"+name+".yaml"] = fmt.Sprintf("schema_version: '0.1'\nid: sample/core/%s\ntitle: %s\nlifecycle: active\n", name, name)
	}
	records[".facetrc.yaml"] = "source_views:\n  repositories:\n    backend: {}\n    frontend: {}\n    records: {}\n  registry_repository: records\n  default_view: original\n  views:\n    original:\n      sources:\n        backend: {binding: backend-old}\n        frontend: {binding: frontend}\n    selected:\n      sources:\n        backend: {binding: backend-new}\n        frontend: {binding: frontend}\n"
	registry := sourceViewIntegrationRepo(t, records)
	local := fmt.Sprintf("bindings:\n  backend-old:\n    repository: backend\n    checkout: %q\n  backend-new:\n    repository: backend\n    checkout: %q\n  frontend:\n    repository: frontend\n    checkout: %q\n", backend, alternate, frontend)
	sourceViewIntegrationWrite(t, registry, ".facet/sources.local.yaml", local)
	return sourceViewIntegrationFixture{registry: registry, backend: backend, alternate: alternate, frontend: frontend, revisions: map[string]string{"backend": sourceViewIntegrationGit(t, alternate, "rev-parse", "HEAD"), "frontend": sourceViewIntegrationGit(t, frontend, "rev-parse", "HEAD"), "records": sourceViewIntegrationGit(t, registry, "rev-parse", "HEAD")}}
}

func TestSourceViewInvocationAgreesAcrossCorpusMCPAndSnapshot(t *testing.T) {
	f := sourceViewIntegrationSetup(t)
	before, beforeSet := os.LookupEnv("FACET_SOURCE_VIEW")
	var measured *corpus.Corpus
	var described sourceViewOutput
	called := false
	probe := &cobra.Command{Use: "inspect-selected", RunE: func(cmd *cobra.Command, _ []string) error {
		called = true
		cfg, e := ConfigForCommand(cmd)
		if e != nil {
			return e
		}
		set := RegistrySetForCommand(cmd)
		if cfg.All()[0].SourceView != set.Primary().SourceView {
			t.Fatal("command consumers received different view snapshots")
		}
		measured, e = corpus.Build(set.Primary(), nil)
		if e != nil {
			return e
		}
		described, e = describeSourceView(set, sourceViewInput{})
		if e != nil {
			return e
		}
		for path, want := range map[string]string{"backend:src/main.go": "sample/core/backend-new", "frontend:src/main.go": "sample/core/frontend"} {
			out, e := anchorResolve(set, resolveInput{Path: path})
			if e != nil {
				return e
			}
			if !reflect.DeepEqual(out.Features, []string{want}) {
				t.Fatalf("MCP resolution %s: %+v", path, out)
			}
		}
		if _, e := anchorResolve(set, resolveInput{Path: "src/main.go"}); e == nil {
			t.Fatal("ambiguous cross-repository path resolved without repository identity")
		}
		for id, want := range map[string]string{"sample/core/backend-new": "backend:src/main.go", "sample/core/frontend": "frontend:src/main.go"} {
			out, e := anchorList(set, anchorListInput{ID: id})
			if e != nil {
				return e
			}
			if len(out.Anchors) != 1 || out.Anchors[0].Path != want {
				t.Fatalf("MCP anchors %s: %+v", id, out)
			}
		}
		old, e := anchorList(set, anchorListInput{ID: "sample/core/backend-old"})
		if e != nil {
			return e
		}
		if len(old.Anchors) != 0 {
			t.Fatalf("alternate backend versions merged: %+v", old)
		}
		return nil
	}}
	command := NewCommand(Options{Commands: []*cobra.Command{probe}})
	command.SetArgs([]string{"--root", f.registry, "--source-view", "selected", "inspect-selected"})
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	if e := command.Execute(); e != nil {
		t.Fatal(e)
	}
	if !called {
		t.Fatal("command not executed")
	}
	if after, set := os.LookupEnv("FACET_SOURCE_VIEW"); after != before || set != beforeSet {
		t.Fatal("source-view flag mutated process environment")
	}
	if measured.SourceView == nil || !described.Configured || !reflect.DeepEqual(measured.SourceView, described.View) {
		t.Fatalf("Corpus/MCP view metadata disagree: %+v %+v", measured.SourceView, described)
	}
	if measured.SourceView.Name != "selected" || measured.SourceView.RegistryRepository != "records" || measured.SourceView.RegistryRevision != f.revisions["records"] {
		t.Fatalf("wrong independent registry metadata: %+v", measured.SourceView)
	}
	if len(measured.SourceView.Sources) != 2 {
		t.Fatalf("registry or alternate checkout scanned as source: %+v", measured.SourceView)
	}
	if measured.Scan.Files != 2 {
		t.Fatalf("source-file denominator includes another checkout or registry: %+v", measured.Scan)
	}

	for _, source := range measured.SourceView.Sources {
		if source.Revision != f.revisions[source.Repository] {
			t.Fatalf("wrong source revision: %+v", source)
		}
	}
	for id, path := range map[string]string{"sample/core/backend-new": "backend:src/main.go", "sample/core/frontend": "frontend:src/main.go"} {
		anchors := measured.Scan.Anchors[id]
		if len(anchors) != 1 || anchors[0].Path != path {
			t.Fatalf("Corpus anchors %s: %+v", id, anchors)
		}
	}
	if len(measured.Scan.Anchors["sample/core/backend-old"]) != 0 {
		t.Fatal("Corpus merged old backend checkout")
	}
	output := filepath.Join(t.TempDir(), "snapshot.json")
	snapshot := NewCommand(Options{})
	snapshot.SetArgs([]string{"--root", f.registry, "--source-view", "selected", "snapshot", "--out", output})
	snapshot.SetOut(&bytes.Buffer{})
	snapshot.SetErr(&bytes.Buffer{})
	if e := snapshot.Execute(); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(output)
	if e != nil {
		t.Fatal(e)
	}
	var exported snapshotEnvelope
	if e := json.Unmarshal(raw, &exported); e != nil {
		t.Fatal(e)
	}
	if exported.Corpus == nil || !reflect.DeepEqual(exported.Corpus.SourceView, measured.SourceView) || !reflect.DeepEqual(exported.Corpus.Scan.Anchors, measured.Scan.Anchors) {
		t.Fatal("snapshot did not preserve selected Corpus view and anchors")
	}
	for _, privatePath := range []string{f.registry, f.backend, f.alternate, f.frontend} {
		if bytes.Contains(raw, []byte(privatePath)) {
			t.Fatalf("snapshot exposed checkout path %q", privatePath)
		}
	}
}

func TestUnknownSourceViewFailsBeforeCommandRuns(t *testing.T) {
	f := sourceViewIntegrationSetup(t)
	called := false
	probe := &cobra.Command{Use: "probe-unknown", RunE: func(*cobra.Command, []string) error { called = true; return nil }}
	command := NewCommand(Options{Commands: []*cobra.Command{probe}})
	command.SetArgs([]string{"--root", f.registry, "--source-view", "missing", "probe-unknown"})
	if e := command.Execute(); e == nil {
		t.Fatal("unknown view accepted")
	}
	if called {
		t.Fatal("command ran before selection validation")
	}
}

func TestChangedExecutionInputsPreserveActualRunnerExit(t *testing.T) {
	for _, strict := range []bool{false, true} {
		t.Run(fmt.Sprintf("strict-%t", strict), func(t *testing.T) {
			f := sourceViewIntegrationSetup(t)
			path := filepath.Join(f.registry, ".facetrc.yaml")
			raw, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			raw = append(raw, []byte("runners:\n  backend:\n    repository: backend\n    list: \"printf 'src/main.go\\\\n'\"\n    command: \"printf '\\\\n// changed during execution\\\\n' >> src/main.go; exit 7\"\n")...)
			if e := os.WriteFile(path, raw, 0600); e != nil {
				t.Fatal(e)
			}
			cmd := NewCommand(Options{})
			args := []string{"--root", f.registry, "--source-view", "selected", "test", "--json"}
			if strict {
				args = append(args, "--fail-unmeasured")
			}
			cmd.SetArgs(args)
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			if code := Run(cmd); code != 7 {
				t.Fatalf("changed-input rejection replaced runner exit7 with %d", code)
			}
		})
	}
}
