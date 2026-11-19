// @facet-ignore-file
package cli

import (
	"bytes"
	"encoding/json"
	"github.com/facet-framework/core/internal/sourceview"
	"github.com/facet-framework/core/registry"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const gateViewConfig = `sources: [src]
source_views:
  repositories:
    code: {}
  default_view: selected
  views:
    selected:
      sources:
        code: {binding: here}
`

func gateViewRepository(t *testing.T) string {
	t.Helper()
	root := brokenRegistry(t)
	write(t, filepath.Join(root, ".facetrc.yaml"), gateViewConfig)
	write(t, filepath.Join(root, ".gitignore"), ".facet/sources.local.yaml\n")
	write(t, filepath.Join(root, ".facet/sources.local.yaml"), "bindings:\n  here:\n    repository: code\n    checkout: .\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "baseline")
	return root
}
func TestCommitGateSourceViewSwitchAndAdoptionWithoutLocalBindings(t *testing.T) {
	root := gateViewRepository(t)
	write(t, filepath.Join(root, ".facetrc.yaml"), gateViewConfig+"hooks: {commit_gate: off}\n")
	git(t, root, "add", ".facetrc.yaml")
	if commitGateOffAt(root, "HEAD:") {
		t.Fatal("HEAD unexpectedly disabled")
	}
	if !commitGateOffAt(root, ":") {
		t.Fatal("staged off was hidden by source-view resolution")
	}
	if _, err := commitLint(root, "git commit -m next"); err == nil || !strings.Contains(err.Error(), "switching the gate off") {
		t.Fatalf("staged off not refused: %v", err)
	}
	git(t, root, "commit", "-qm", "off")
	if !commitGateOffAt(root, "HEAD:") {
		t.Fatal("committed off ignored")
	}
	if !commitRevisionAdopted(root) {
		t.Fatal("source-view adoption lost")
	}
}
func TestStagedSourceViewCannotUseUnstagedRepair(t *testing.T) {
	root := gateViewRepository(t)
	if _, err := stagedLint(root); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "src/new.ts"), "// @facet: billing/invoice/new\n// @facet-role: primary\nexport const n=1;\n")
	git(t, root, "add", "src/new.ts")
	write(t, filepath.Join(root, "src/new.ts"), "export const n=1;\n")
	report, err := stagedLint(root)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range report.Findings {
		if strings.Contains(f.Message, "billing/invoice/new") || f.ID == "billing/invoice/new" {
			found = true
		}
	}
	if !found || !report.Failed() {
		t.Fatalf("staged failure repaired by working tree: %+v", report)
	}
}
func TestStagedSourceViewMissingBindingFailsClosed(t *testing.T) {
	root := gateViewRepository(t)
	write(t, filepath.Join(root, ".facet/sources.local.yaml"), "bindings: {}\n")
	if _, err := stagedLint(root); err == nil {
		t.Fatal("missing binding passed")
	}
}

func TestStagedSourceViewExternalCommitIsFrozenAndDirtyFilesIgnored(t *testing.T) {
	root := gateViewRepository(t)
	external := t.TempDir()
	git(t, external, "init", "-q")
	git(t, external, "config", "user.name", "Test")
	git(t, external, "config", "user.email", "test@example.com")
	write(t, filepath.Join(external, "source.go"), "committed input\n")
	git(t, external, "add", ".")
	git(t, external, "commit", "-qm", "external")
	write(t, filepath.Join(external, "source.go"), "unstaged repair\n")
	write(t, filepath.Join(external, "untracked.go"), "untracked repair\n")
	write(t, filepath.Join(root, ".facet/sources.local.yaml"), "bindings:\n  here: {repository: code, checkout: '"+external+"'}\n")
	tree, err := commitGit(root, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	tree = strings.TrimSpace(tree)
	cfg, err := facet.LoadConfigUnresolved(root)
	if err != nil {
		t.Fatal(err)
	}
	context := &commitViewContext{root: root, external: map[string]string{}}
	defer context.close()
	resolve := func() *sourceview.ResolvedView {
		t.Helper()
		scratch := t.TempDir()
		if err := materializeCommitTree(root, tree, scratch); err != nil {
			t.Fatal(err)
		}
		view, err := context.resolve(scratch, tree, *cfg.SourceViews)
		if err != nil {
			t.Fatal(err)
		}
		return view
	}
	first := resolve()
	source, _ := first.Source("code")
	raw, err := source.Read("source.go")
	if err != nil || string(raw) != "committed input\n" {
		t.Fatalf("dirty external content used: %q %v", raw, err)
	}
	files, err := source.Files()
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file == "untracked.go" {
			t.Fatal("untracked external content captured")
		}
	}
	git(t, external, "add", ".")
	git(t, external, "commit", "-qm", "external moved")
	second := resolve()
	source, _ = second.Source("code")
	raw, err = source.Read("source.go")
	if err != nil || string(raw) != "committed input\n" {
		t.Fatalf("external baseline drifted: %q %v", raw, err)
	}
}

func TestStagedSourceViewExplicitSelectionAndRefMismatch(t *testing.T) {
	root := gateViewRepository(t)
	config := strings.Replace(gateViewConfig, "default_view: selected", "default_view: unavailable", 1) + "    unavailable:\n      sources:\n        code: {binding: absent}\n"
	write(t, filepath.Join(root, ".facetrc.yaml"), config)
	git(t, root, "add", ".facetrc.yaml")
	git(t, root, "commit", "-qm", "two views")
	if _, err := stagedLintWithSourceView(root, "selected"); err != nil {
		t.Fatalf("explicit nondefault view ignored: %v", err)
	}
	if _, err := stagedLint(root); err == nil {
		t.Fatal("unavailable default binding passed")
	}
	config = strings.Replace(config, "code: {binding: here}", "code: {binding: here, ref: nonexistent}", 1)
	write(t, filepath.Join(root, ".facetrc.yaml"), config)
	git(t, root, "add", ".facetrc.yaml")
	if _, err := stagedLintWithSourceView(root, "selected"); err == nil {
		t.Fatal("unavailable optional ref passed")
	}
	config = strings.Replace(config, "ref: nonexistent", "ref: HEAD~1", 1)
	write(t, filepath.Join(root, ".facetrc.yaml"), config)
	git(t, root, "add", ".facetrc.yaml")
	if _, err := stagedLintWithSourceView(root, "selected"); err == nil || !strings.Contains(err.Error(), "not checked out") {
		t.Fatalf("mismatched available ref passed: %v", err)
	}
}

func TestStagedSourceViewUnbornIndex(t *testing.T) {
	root := gateViewRepository(t)
	git(t, root, "update-ref", "-d", "HEAD")
	if _, err := stagedLint(root); err != nil {
		t.Fatalf("unborn root binding failed: %v", err)
	}
}

func TestStagedSourceViewDoesNotRunCleanFilters(t *testing.T) {
	root := gateViewRepository(t)
	write(t, filepath.Join(root, ".gitattributes"), "*.ts filter=poison\n")
	git(t, root, "add", ".gitattributes")
	tree, err := commitGit(root, "write-tree")
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "global.gitconfig")
	write(t, config, "[filter \"poison\"]\n clean = false\n required = true\n")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	if _, err := lintCommitTree(root, strings.TrimSpace(tree)); err != nil {
		t.Fatalf("immutable snapshot invoked clean filter: %v", err)
	}
}

func TestStagedSourceViewTrackedLocalBindingsRefused(t *testing.T) {
	root := gateViewRepository(t)
	git(t, root, "add", "--force", ".facet/sources.local.yaml")
	if _, err := stagedLint(root); err == nil || !strings.Contains(err.Error(), "not tracked") {
		t.Fatalf("tracked bindings passed: %v", err)
	}
}

func TestStagedSourceViewCLIUsesRevisionConfigAndExplicitSelection(t *testing.T) {
	root := gateViewRepository(t)
	config := strings.Replace(gateViewConfig, "default_view: selected", "default_view: unavailable", 1) + "    unavailable:\n      sources:\n        code: {binding: absent}\n"
	write(t, filepath.Join(root, ".facetrc.yaml"), config)
	git(t, root, "add", ".facetrc.yaml")
	git(t, root, "commit", "-qm", "two views")
	write(t, filepath.Join(root, ".facetrc.yaml"), "source_views: [invalid unstaged configuration\n")
	command := NewCommand(Options{})
	command.SetArgs([]string{"--root", root, "--source-view", "selected", "lint", "--staged", "--json"})
	if err := command.Execute(); err != nil {
		t.Fatalf("staged CLI consulted working config or lost selection: %v", err)
	}
}

func TestStagedSourceViewSnapshotDoesNotRunReferenceHooks(t *testing.T) {
	root := gateViewRepository(t)
	hooks := t.TempDir()
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hook := filepath.Join(hooks, "reference-transaction")
	write(t, hook, "#!/bin/sh\ntouch '"+marker+"'\n")
	if err := os.Chmod(hook, 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "global.gitconfig")
	write(t, config, "[core]\n hooksPath = "+hooks+"\n")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	if _, err := stagedLint(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("snapshot ran reference hook: %v", err)
	}
}

func TestStagedSourceViewHookIngressKeepsExplicitSelection(t *testing.T) {
	root := gateViewRepository(t)
	config := strings.Replace(gateViewConfig, "default_view: selected", "default_view: unavailable", 1) + "    unavailable:\n      sources:\n        code: {binding: absent}\n"
	write(t, filepath.Join(root, ".facetrc.yaml"), config)
	git(t, root, "add", ".facetrc.yaml")
	git(t, root, "commit", "-qm", "two views")
	payload, _ := json.Marshal(map[string]any{"cwd": root, "session_id": "explicit-view", "tool_name": "Bash", "tool_input": map[string]string{"command": "git commit -m next"}})
	command := NewCommand(Options{})
	var output bytes.Buffer
	command.SetArgs([]string{"--root", root, "--source-view", "selected", "hook", "pre-tool-use"})
	command.SetIn(bytes.NewReader(payload))
	command.SetOut(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "deny") {
		t.Fatalf("hook lost explicit source selection: %s", output.String())
	}
	write(t, filepath.Join(root, "src/ingress.ts"), "// @facet: billing/invoice/ingress-defect\n// @facet-role: primary\nexport const defect=1;\n")
	git(t, root, "add", "src/ingress.ts")
	write(t, filepath.Join(root, "src/ingress.ts"), "export const defect=1;\n")
	command = NewCommand(Options{})
	output.Reset()
	command.SetArgs([]string{"--root", root, "--source-view", "selected", "hook", "pre-tool-use"})
	command.SetIn(bytes.NewReader(payload))
	command.SetOut(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"permissionDecision":"deny"`) || !strings.Contains(output.String(), "billing/invoice/ingress-defect") {
		t.Fatalf("hook failed to judge indexed defect: %s", output.String())
	}
}

func TestStagedSourceViewCannotUseUnstagedRecordRepair(t *testing.T) {
	root := gateViewRepository(t)
	path := filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	write(t, path, "schema_version: [invalid staged yaml\n")
	git(t, root, "add", "docs/facets/features/billing/invoice/list.yaml")
	write(t, path, string(original))
	report, err := stagedLint(root)
	if err == nil && !report.Failed() {
		t.Fatal("unstaged record repair hid malformed staged record")
	}
	if err != nil && !strings.Contains(err.Error(), "invalid staged yaml") {
		t.Fatalf("unexpected staged verification failure: %v", err)
	}
}
