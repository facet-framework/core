// @facet-ignore-file

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// devFixture is a git repository with go.mod, a release package carrying the
// variables the dispatcher stamps, a cmd/facet that answers `version --json`
// with what it was stamped with, and a copy of bin/facet. extra adds files
// before the first commit.
func devFixture(t *testing.T, extra map[string]string) string {
	t.Helper()
	skipUnlessShell(t)
	for _, tool := range []string{"go", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s here", tool)
		}
	}
	root := filepath.Join(t.TempDir(), "plugin")
	dispatcher, err := os.ReadFile(dispatcherScript(t))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":                "module example.com/devfixture\n\ngo 1.21\n",
		"release/release.go":    "package release\n\nvar Distribution, SourceCommit, Dirty, BuildTime string\n",
		"tools/facet-lint/keep": "",
		"cmd/facet/main.go": `package main

import (
	"fmt"
	"os"

	"example.com/devfixture/release"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("{\"product\":\"core\",\"version\":\"0.1.0-dev.fixture\",\"distribution\":%q,\"source_commit\":%q,\"dirty\":%q,\"built\":%q}\n",
			release.Distribution, release.SourceCommit, release.Dirty, release.BuildTime)
		return
	}
	fmt.Println("fixture-facet-ran")
}
`,
	}
	for k, v := range extra {
		files[k] = v
	}
	for name, body := range files {
		writeFixture(t, filepath.Join(root, name), []byte(body))
	}
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "facet"), dispatcher, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("add", ".")
	git("commit", "-qm", "fixture")
	return root
}

// runDevBuild runs the fixture's own dispatcher with FACET_DEV_BUILD=1.
func runDevBuild(t *testing.T, root, data string, extraEnv ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", filepath.Join(root, "bin", "facet"), "run")
	cmd.Dir = root
	cmd.Env = append(cmd.Environ(),
		"CLAUDE_PLUGIN_ROOT="+root, "CLAUDE_PLUGIN_DATA="+data, "FACET_DEV_BUILD=1",
		"FACET_BINARY=", "FACET_VERSION=", "GOWORK=off", "GOFLAGS=",
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func devDir(data string) string {
	return filepath.Join(data, "dist", "core", "source", runtime.GOOS+"_"+runtime.GOARCH, "dev")
}

// mvShim puts an `mv` ahead of the real one that runs body first; body sees
// the source as $src, the destination as $dst and the real mv as $REALMV.
func mvShim(t *testing.T, body string) string {
	t.Helper()
	real, err := exec.LookPath("mv")
	if err != nil {
		t.Skip("no mv here")
	}
	dir := t.TempDir()
	script := `#!/bin/sh
REALMV='` + real + `'
src=; dst=
for a in "$@"; do
  case "$a" in
    --) ;;
    *) if [ -z "$src" ]; then src=$a; else dst=$a; fi ;;
  esac
done
` + body + `
exec "$REALMV" "$@"
`
	if err := os.WriteFile(filepath.Join(dir, "mv"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A failing optional sidecar is reported and skipped, including when it is the
// last entry. The Core development build must still complete and run.
func TestAFailingLastSidecarDoesNotEndTheDevBuild(t *testing.T) {
	// The sidecar build is this tree's development-build hook, which bin/facet
	// sources when the plugin root carries it.
	hook, err := os.ReadFile(filepath.Join(cliModuleRoot(t), "scripts", "dev-build-hook.sh"))
	if os.IsNotExist(err) {
		// A tree without the hook builds facet alone; there is no sidecar
		// step whose failure this case could exercise.
		t.Skip("this tree carries no development-build hook")
	}
	if err != nil {
		t.Fatal(err)
	}
	root := devFixture(t, map[string]string{
		"scripts/dev-build-hook.sh": string(hook),
		"scripts/sidecars.list":     "# fixture\ngood sidecar/good\nbroken sidecar/broken\n",
		"sidecar/good/main.go":      "package main\n\nfunc main() {}\n",
		"sidecar/broken/main.go":    "package main\n\nfunc main() { this does not compile }\n",
		"sidecar/broken/README.md":  "",
	})
	data := t.TempDir()
	out, err := runDevBuild(t, root, data)
	if err != nil || !strings.Contains(out, "fixture-facet-ran") {
		t.Fatalf("a failing last sidecar ended the development build (err %v):\n%s", err, out)
	}
	if !strings.Contains(out, "broken sidecar") || !strings.Contains(out, "did not build") {
		t.Errorf("the failed sidecar was not named:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(devDir(data), "good")); err != nil {
		t.Errorf("the sidecar that builds is missing beside facet: %v", err)
	}
}

// When the old development build cannot be moved aside, it is still complete
// and in place; the dispatcher must notice that the install is not its own
// and stop, rather than run the stale binary.
func TestADevRebuildThatCannotReplaceTheOldBuildStops(t *testing.T) {
	root := devFixture(t, nil)
	data := t.TempDir()
	old := devDir(data)
	writeFixture(t, filepath.Join(old, "install.json"), []byte(`{"product": "core", "binary_sha256": "0000"}`+"\n"))
	if err := os.WriteFile(filepath.Join(old, "facet"), stubScript("stale-dev-build-ran"), 0o755); err != nil {
		t.Fatal(err)
	}
	premiseRuns(t, filepath.Join(old, "facet"), "stale-dev-build-ran")
	// Refuse exactly the move that sets the old build aside.
	shim := mvShim(t, `case "$src" in */dev) exit 1 ;; esac`)

	out, err := runDevBuild(t, root, data, "PATH="+shim+":"+os.Getenv("PATH"))
	if err == nil || strings.Contains(out, "stale-dev-build-ran") {
		t.Fatalf("the stale development build ran in place of the new one (err %v):\n%s", err, out)
	}
	if !strings.Contains(out, "does not hold the build just made") {
		t.Errorf("the failure does not say why:\n%s", out)
	}
}
