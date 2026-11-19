// @facet-ignore-file
package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/facet-framework/core/registry"
)

func TestFilterStatusUsesSelectedRepositoryWithoutRebindingOtherWorktrees(t *testing.T) {
	f := sourceViewIntegrationSetup(t)
	sourceViewIntegrationWrite(t, f.backend, "src/only-old.go", "package backend\nfunc Exclusive(){}\n")
	sourceViewIntegrationGit(t, f.backend, "add", "src/only-old.go")
	sourceViewIntegrationGit(t, f.backend, "commit", "-qm", "version-exclusive file")
	common, e := facet.GitCommonDir(f.backend)
	if e != nil {
		t.Fatal(e)
	}
	origin, e := facet.PrivateAnchorWorktree(f.backend)
	if e != nil {
		t.Fatal(e)
	}
	revision := sourceViewIntegrationGit(t, f.backend, "rev-parse", "HEAD")
	originals := map[string][]byte{}
	for path, before := range map[string]string{"src/main.go": "func Old(){}", "src/only-old.go": "func Exclusive(){}"} {
		pa := facet.PrivateAnchors{Path: path, Blocks: []facet.AnchorBlock{{Repository: "backend", OriginRevision: revision, OriginWorktree: origin, Lines: []string{"// @facet: sample/core/private-action"}, Before: before, Occurrence: 1}}}
		if e := facet.SavePrivateAnchors(common, pa); e != nil {
			t.Fatal(e)
		}
		raw, e := os.ReadFile(facet.SidecarPath(common, path))
		if e != nil {
			t.Fatal(e)
		}
		originals[path] = raw
	}
	status := func() string {
		t.Helper()
		var output bytes.Buffer
		cmd := NewCommand(Options{})
		cmd.SetOut(&output)
		cmd.SetErr(&output)
		cmd.SetArgs([]string{"--root", f.registry, "--source-view", "selected", "filter", "--repository", "backend", "status"})
		if e := cmd.Execute(); e != nil {
			t.Fatal(e)
		}
		return output.String()
	}
	output := status()
	if !strings.Contains(output, "repository backend; revision "+f.revisions["backend"]+"; snapshot ") || strings.Count(output, "(absent here;") != 2 {
		t.Fatalf("missing selected/version-exclusive diagnostics: %s", output)
	}
	for path, before := range originals {
		after, e := os.ReadFile(facet.SidecarPath(common, path))
		if e != nil || !bytes.Equal(before, after) {
			t.Fatalf("status changed common sidecar %s: %v", path, e)
		}
	}
	// Relocating the chosen checkout changes its local binding, never its logical
	// repository or the stored association's identity/revision/placement.
	moved := filepath.Join(t.TempDir(), "moved-backend")
	sourceViewIntegrationGit(t, f.backend, "worktree", "move", f.alternate, moved)
	local := filepath.Join(f.registry, ".facet/sources.local.yaml")
	raw, e := os.ReadFile(local)
	if e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(local, bytes.ReplaceAll(raw, []byte(f.alternate), []byte(moved)), 0600); e != nil {
		t.Fatal(e)
	}
	output = status()
	if !strings.Contains(output, "repository backend; revision "+f.revisions["backend"]) || strings.Count(output, "(absent here;") != 2 {
		t.Fatalf("relocation changed association identity: %s", output)
	}
	for path, before := range originals {
		after, e := os.ReadFile(facet.SidecarPath(common, path))
		if e != nil || !bytes.Equal(before, after) {
			t.Fatalf("relocation rewrote sidecar %s: %v", path, e)
		}
	}
}

func TestAutomaticPrivateFiltersIgnoreInheritedCentralViewSelection(t *testing.T) {
	root := sourceViewIntegrationRepo(t, map[string]string{"src/main.go": "package backend\nfunc Source(){}\n"})
	sourceViewIntegrationGit(t, root, "config", "facet.privateRepository", "backend")
	bin := facetBinary(t)
	env := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "FACET_SOURCE_VIEW=") {
			env = append(env, entry)
		}
	}
	env = append(env, "FACET_SOURCE_VIEW=central-dev")
	annotated := "package backend\n// @facet: sample/core/private-action\nfunc Source(){}\n"
	run := func(action, input string) string {
		t.Helper()
		cmd := exec.Command(bin, "filter", action, "src/main.go")
		cmd.Dir = root
		cmd.Env = env
		cmd.Stdin = strings.NewReader(input)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, e := cmd.Output()
		if e != nil {
			t.Fatalf("automatic %s rejected inherited central view: %v %s", action, e, stderr.String())
		}
		return string(out)
	}
	cleaned := run("clean", annotated)
	if cleaned != "package backend\nfunc Source(){}\n" {
		t.Fatalf("clean failed: %q", cleaned)
	}
	if restored := run("smudge", cleaned); restored != annotated {
		t.Fatalf("smudge failed: %q", restored)
	}
	common, e := facet.GitCommonDir(root)
	if e != nil {
		t.Fatal(e)
	}
	pa, e := facet.LoadPrivateAnchors(common, "src/main.go")
	if e != nil {
		t.Fatal(e)
	}
	if len(pa.Blocks) != 1 || pa.Blocks[0].Repository != "backend" || pa.Blocks[0].OriginRevision == "" {
		t.Fatalf("automatic filter lost logical provenance: %+v", pa)
	}
}
