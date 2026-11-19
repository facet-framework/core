package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const conventionsRecord = "schema_version: \"0.1\"\nid: billing/invoice/list\nlifecycle: active\nintent:\n  summary: |\n    Lists a customer's invoices, newest first.\nlineage:\n  owner:\n    team: billing\n  created_at: \"2026-01-01\"\n"

// The conventions document has to reach the agent, not only the resolver: a
// path computed correctly and never handed to the session passes every resolver
// test and still does nothing. Both moments an agent starts without context are
// asserted, and the path is the effective one, with where it came from.
func TestTheConventionsDocumentIsNamedAtSessionStartAndBeforeCompaction(t *testing.T) {
	root := adoptedRepo(t, "billing/invoice/list")
	write(t, filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml"), conventionsRecord)
	write(t, filepath.Join(root, "FACET-CONVENTIONS.md"), "# Conventions\n")
	ctx := newHookContext(root)

	for name, run := range map[string]func(){
		"SessionStart": func() { hookSessionStart(ctx) },
		"PreCompact":   func() { hookPreCompact(ctx, "session-conv") },
	} {
		out := captureHook(t, run)
		if !strings.Contains(out, "FACET-CONVENTIONS.md (found at the repository root)") {
			t.Errorf("%s does not name the effective conventions document:\n%s", name, out)
		}
		// The boundary travels with the path, so an agent never reads the
		// document as permission to skip review.
		if !strings.Contains(out, "cannot grant permissions") {
			t.Errorf("%s names the document without its limits:\n%s", name, out)
		}
	}
}

// Absence is unchanged behaviour: no line about conventions at all.
func TestNoConventionsDocumentLeavesTheSessionContextUnchanged(t *testing.T) {
	root := adoptedRepo(t, "billing/invoice/list")
	write(t, filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml"), conventionsRecord)
	ctx := newHookContext(root)

	for name, run := range map[string]func(){
		"SessionStart": func() { hookSessionStart(ctx) },
		"PreCompact":   func() { hookPreCompact(ctx, "session-noconv") },
	} {
		out := captureHook(t, run)
		// Premise: the hook said something, so silence about conventions is a
		// finding and not an empty run.
		if !strings.Contains(out, "FACET") {
			t.Fatalf("%s produced no context; this case would be vacuous:\n%s", name, out)
		}
		if strings.Contains(strings.ToLower(out), "conventions") {
			t.Errorf("%s: a project without conventions must not hear about them:\n%s", name, out)
		}
	}
}

// The shell fallback names the same document in the same words as the binary,
// for both resolution steps it can read. Two implementations of one paragraph
// drift silently, and the machines without the binary are the ones nobody
// watches.
func TestTheShellFallbackNamesTheSameConventionsAsTheBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fallback is a POSIX shell script")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell here")
	}
	plugin, err := filepath.Abs(cliModuleRoot(t))
	if err != nil {
		t.Fatal(err)
	}

	for name, setup := range map[string]func(root string){
		"root file": func(root string) {
			write(t, filepath.Join(root, "FACET-CONVENTIONS.md"), "# c\n")
		},
		"declared": func(root string) {
			write(t, filepath.Join(root, ".facetrc.yaml"), "conventions: docs/CONVENTIONS.md\n")
			write(t, filepath.Join(root, "docs/CONVENTIONS.md"), "# c\n")
		},
		"declared, missing": func(root string) {
			write(t, filepath.Join(root, ".facetrc.yaml"), "conventions: docs/CONVENTIONS.md\n")
		},
	} {
		root := t.TempDir()
		write(t, filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml"), conventionsRecord)
		setup(root)

		cmd := exec.Command("sh", "-c", ". \""+plugin+"/scripts/hook-lib.sh\"; facet_conventions_block \""+root+"\"")
		cmd.Env = append(cmd.Environ(), "CLAUDE_PLUGIN_ROOT="+plugin, "FACET_NO_BINARY=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: the fallback failed: %v\n%s", name, err, out)
		}

		var b strings.Builder
		appendConventions(&b, newHookContext(root))
		// Premise: the binary produced a paragraph, so equality is not two
		// empty strings agreeing.
		if b.Len() == 0 {
			t.Fatalf("%s: the binary named no conventions; this case would be vacuous", name)
		}
		if got, want := strings.TrimSpace(string(out)), strings.TrimSpace(b.String()); got != want {
			t.Errorf("%s: the fallback and the binary disagree.\nshell:\n%s\n\nbinary:\n%s", name, got, want)
		}

		// And the paragraph reaches the hooks that ship, not only the helper:
		// a helper nobody calls would pass the comparison above.

		for _, hook := range []string{"session-start.sh", "pre-compact.sh"} {
			run := exec.Command("sh", filepath.Join(plugin, "scripts", hook))
			run.Env = append(run.Environ(), "CLAUDE_PLUGIN_ROOT="+plugin, "FACET_NO_BINARY=1", "CLAUDE_PROJECT_DIR="+root)
			run.Stdin = strings.NewReader(`{"session_id":"conv"}`)
			got, err := run.Output()
			if err != nil {
				t.Fatalf("%s: %s failed: %v\n%s", name, hook, err, got)
			}
			var payload hookOutput
			if err := json.Unmarshal(got, &payload); err != nil {
				t.Fatalf("%s: %s did not emit JSON: %v\n%s", name, hook, err, got)
			}
			ctx := payload.HookSpecificOutput.AdditionalContext
			if !strings.Contains(ctx, strings.TrimSpace(b.String())) {
				t.Errorf("%s: %s does not carry the conventions paragraph:\n%s", name, hook, ctx)
			}
		}
	}
}

func TestTheConventionsCommandReportsTheEffectiveDocumentPerRegistry(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "facet")
	if out, err := exec.Command("go", "build", "-o", bin, cliCommandPackage(t)).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	root := t.TempDir()
	write(t, filepath.Join(root, ".facetrc.yaml"), `conventions: CONVENTIONS.md
registries:
  storefront:
    records: apps/storefront/docs/facets
    conventions: apps/storefront/CONVENTIONS.md
  admin:
    records: apps/admin/docs/facets
`)
	write(t, filepath.Join(root, "CONVENTIONS.md"), "# shared\n")
	write(t, filepath.Join(root, "apps/storefront/CONVENTIONS.md"), "# storefront\n")

	cmd := exec.Command(bin, "conventions", "--json", "--root", root)
	cmd.Env = append(os.Environ(), "FACET_RECORDS_DIR=")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("facet conventions: %v\n%s", err, out)
	}
	var got []conventionsRow
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	want := map[string]conventionsRow{
		"admin":      {Registry: "admin", Path: "CONVENTIONS.md", Source: "project"},
		"storefront": {Registry: "storefront", Path: "apps/storefront/CONVENTIONS.md", Source: "registry"},
	}
	if len(got) != len(want) {
		t.Fatalf("want %d rows, got %+v", len(want), got)
	}
	for _, row := range got {
		w := want[row.Registry]
		if row.Path != w.Path || row.Source != w.Source || !row.Exists {
			t.Errorf("registry %q: got %+v, want path %q from %q, present", row.Registry, row, w.Path, w.Source)
		}
	}
}
