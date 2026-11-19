// Exercises the shell dispatcher in bin/facet rather than implementing a
// feature of its own.
// @facet-ignore-file
package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// The distribution directory of a release from the default upstream.
const upstreamDist = "github-facet-framework-core"

// The platform every case here pretends to be, through the uname shim.
const testPlatform = "linux_amd64"

// A PATH the dispatcher can work in but cannot find `facet` on.
func sandboxPath(t *testing.T) string {
	t.Helper()
	const path = "/usr/bin:/bin"
	for _, dir := range []string{"/usr/bin", "/bin"} {
		if _, err := os.Stat(filepath.Join(dir, "facet")); err == nil {
			t.Skip("a system-wide facet on " + dir + " would be asked before the cache")
		}
	}
	return path
}

func dispatcherScript(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join(cliModuleRoot(t), "bin/facet"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// dispatcherCmd prepares a run of bin/facet. Every setting a developer's own
// shell might carry is cleared first and the case's own env applied after, so
// nothing leaks into a fixture; for a duplicated key the last value wins.
func dispatcherCmd(t *testing.T, env []string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sh", append([]string{dispatcherScript(t)}, args...)...)
	cmd.Env = append(cmd.Environ(),
		"FACET_REPO=", "FACET_REPOS=", "FACET_BINARY=", "FACET_DEV_BUILD=",
		"FACET_VERSION=", "FACET_RELEASE_TAG=", "GH_TOKEN=", "GITHUB_TOKEN=",
		"FACET_TEST_UNAME_S=Linux", "FACET_TEST_UNAME_M=x86_64",
	)
	cmd.Env = append(cmd.Env, env...)
	return cmd
}

func runFacetDispatcher(t *testing.T, env []string, args ...string) (string, error) {
	t.Helper()
	out, err := dispatcherCmd(t, env, args...).CombinedOutput()
	return string(out), err
}

// offlineEnv is a machine with no release anywhere: the shims answer uname
// and fail every download, and the fixture directory is empty.
func offlineEnv(t *testing.T, data string) []string {
	t.Helper()
	shims := archiveShims(t)
	return []string{
		"CLAUDE_PLUGIN_DATA=" + data,
		"FACET_TEST_FIXTURES=" + t.TempDir(),
		"PATH=" + shims + ":" + sandboxPath(t),
	}
}

// stubScript is a stand-in binary that prints a marker when it runs.
func stubScript(marker string) []byte {
	return []byte("#!/bin/sh\necho " + marker + "\n")
}

// installFixture lays out one install the way the dispatcher leaves it. With
// complete false it omits install.json, the marker of a finished install.
func installFixture(t *testing.T, data, dist, version, marker string, complete bool) string {
	t.Helper()
	dir := filepath.Join(data, "dist", "core", dist, testPlatform, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "facet"), stubScript(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if complete {
		body := `{"product": "core", "distribution": "` + dist + `", "platform": "` + testPlatform + `", "version": "` + version + `"}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, "install.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// premiseRuns proves a stub would have printed its marker had it been chosen,
// so its absence from the dispatcher's output means it was not chosen.
func premiseRuns(t *testing.T, path, marker string) {
	t.Helper()
	out, err := exec.Command(path).CombinedOutput()
	if err != nil || !strings.Contains(string(out), marker) {
		t.Fatalf("the fixture %s does not run and print %q (%v: %s), so its absence proves nothing", path, marker, err, out)
	}
}

func skipUnlessShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the dispatcher is a POSIX shell script")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell here")
	}
}

// installsUnder lists the complete installs (directories holding install.json)
// below data/dist/core, relative to it.
func installsUnder(t *testing.T, data string) []string {
	t.Helper()
	var found []string
	root := filepath.Join(data, "dist", "core")
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.Name() == "install.json" {
			rel, _ := filepath.Rel(root, filepath.Dir(path))
			found = append(found, rel)
		}
		return nil
	})
	return found
}

// stagingLeftovers lists whatever is left in the staging directory.
func stagingLeftovers(t *testing.T, data string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(data, "dist", "core", ".staging"))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// A cached install is selected through both version-free pointers, and both
// are relative so the cache stays movable. The durable path has to run, not
// merely exist.
func TestTheDispatcherPointsSelectedAtTheCachedInstall(t *testing.T) {
	skipUnlessShell(t)
	const version = "9.9.9"
	data := t.TempDir()
	installFixture(t, data, upstreamDist, version, "cached-stub-ran", true)

	out, err := runFacetDispatcher(t, append(offlineEnv(t, data), "FACET_VERSION="+version), "--version")
	if err != nil || !strings.Contains(out, "cached-stub-ran") {
		t.Fatalf("the cached install did not run: %v\n%s", err, out)
	}

	current := filepath.Join(data, "dist", "core", upstreamDist, testPlatform, "current")
	if target, err := os.Readlink(current); err != nil || target != version {
		t.Errorf("current = %q (%v), want the relative %q", target, err, version)
	}
	selected := filepath.Join(data, "dist", "core", "selected")
	target, err := os.Readlink(selected)
	if err != nil {
		t.Fatalf("no selected pointer: %v", err)
	}
	if want := upstreamDist + "/" + testPlatform + "/current"; target != want {
		t.Errorf("selected = %q, want the relative %q", target, want)
	}
	through, err := exec.Command(filepath.Join(selected, "facet")).CombinedOutput()
	if err != nil || !strings.Contains(string(through), "cached-stub-ran") {
		t.Fatalf("the binary is not reachable through selected: %v\n%s", err, through)
	}
}

// A second version arriving must move the pointer; a stale one is worse than
// none, because it looks maintained.
func TestTheSelectedPointerFollowsANewerVersion(t *testing.T) {
	skipUnlessShell(t)
	data := t.TempDir()
	installFixture(t, data, upstreamDist, "1.0.0", "one-ran", true)
	installFixture(t, data, upstreamDist, "1.1.0", "two-ran", true)
	for _, v := range []string{"1.0.0", "1.1.0"} {
		if out, err := runFacetDispatcher(t, append(offlineEnv(t, data), "FACET_VERSION="+v), "--version"); err != nil {
			t.Fatalf("the dispatcher failed for %s: %v\n%s", v, err, out)
		}
	}
	out, err := exec.Command(filepath.Join(data, "dist", "core", "selected", "facet")).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "two-ran") {
		t.Errorf("selected still runs %q after 1.1.0 resolved (%v)", out, err)
	}
}

// The 1.x cache is never consulted: an old binary there with its own current
// pointer is not run, and the run fails instead of quietly using it.
func TestAnOldBinCacheIsNeverExecuted(t *testing.T) {
	skipUnlessShell(t)
	data := t.TempDir()
	old := filepath.Join(data, "bin", "1.20.1", "facet")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, stubScript("old-1x-cache-ran"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("1.20.1", filepath.Join(data, "bin", "current")); err != nil {
		t.Fatal(err)
	}
	premiseRuns(t, old, "old-1x-cache-ran")
	premiseRuns(t, filepath.Join(data, "bin", "current", "facet"), "old-1x-cache-ran")

	for _, env := range [][]string{offlineEnv(t, data), append(offlineEnv(t, data), "FACET_VERSION=1.20.1")} {
		out, err := runFacetDispatcher(t, env, "--version")
		if err == nil {
			t.Fatalf("the dispatcher succeeded with only an old cache present:\n%s", out)
		}
		if strings.Contains(out, "old-1x-cache-ran") {
			t.Fatalf("the old bin/ cache was executed:\n%s", out)
		}
		if !strings.Contains(out, "could not obtain facet core") {
			t.Errorf("the failure does not say what it could not obtain:\n%s", out)
		}
	}
	if _, err := os.Lstat(filepath.Join(data, "dist", "core", "selected")); err == nil {
		t.Error("a selected pointer was created although nothing was resolved")
	}
}

// A version directory without install.json is an unfinished install and is
// never selected, even though its binary would run.
func TestAVersionDirectoryWithoutInstallJSONIsNotSelected(t *testing.T) {
	skipUnlessShell(t)
	const version = "9.9.2"
	data := t.TempDir()
	dir := installFixture(t, data, upstreamDist, version, "incomplete-ran", false)
	premiseRuns(t, filepath.Join(dir, "facet"), "incomplete-ran")
	if _, err := os.Stat(filepath.Join(dir, "install.json")); err == nil {
		t.Fatal("the fixture has an install.json, so this case is not about its absence")
	}

	out, err := runFacetDispatcher(t, append(offlineEnv(t, data), "FACET_VERSION="+version), "--version")
	if err == nil || strings.Contains(out, "incomplete-ran") {
		t.Fatalf("an install without install.json was selected (err %v):\n%s", err, out)
	}
}

// The same version from two sources lives in two directories, and the order of
// FACET_REPOS decides which one runs.
func TestTheSameVersionFromTwoReposDoesNotCollide(t *testing.T) {
	skipUnlessShell(t)
	const version = "9.9.1"
	data := t.TempDir()
	installFixture(t, data, "github-mirror-one-facet", version, "from-mirror-one", true)
	installFixture(t, data, "github-mirror.two-facet", version, "from-mirror-two", true)

	for _, c := range []struct{ repos, want, not string }{
		{"Mirror-One/facet mirror.two/facet", "from-mirror-one", "from-mirror-two"},
		{"mirror.two/facet Mirror-One/facet", "from-mirror-two", "from-mirror-one"},
	} {
		out, err := runFacetDispatcher(t, append(offlineEnv(t, data), "FACET_VERSION="+version, "FACET_REPOS="+c.repos), "--version")
		if err != nil || !strings.Contains(out, c.want) || strings.Contains(out, c.not) {
			t.Errorf("FACET_REPOS=%q ran %q (err %v), want %s", c.repos, out, err, c.want)
		}
	}

	// A source that is not listed is not consulted, even with the version cached.
	out, err := runFacetDispatcher(t, append(offlineEnv(t, data), "FACET_VERSION="+version, "FACET_REPOS=someone/else"), "--version")
	if err == nil || strings.Contains(out, "from-mirror") {
		t.Errorf("an unlisted source's cache was used (err %v):\n%s", err, out)
	}
}

// A pinned version that is already installed needs no network tool at all.
func TestAPinnedInstalledVersionRunsWithoutNetworkTools(t *testing.T) {
	skipUnlessShell(t)
	const version = "9.9.0"
	data := t.TempDir()
	installFixture(t, data, upstreamDist, version, "offline-ran", true)

	narrow := t.TempDir()
	for _, tool := range []string{"sh", "dirname", "basename", "head", "ln", "mkdir", "rm", "tr", "sed", "cat"} {
		src, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("no %s here", tool)
		}
		if err := os.Symlink(src, filepath.Join(narrow, tool)); err != nil {
			t.Fatal(err)
		}
	}
	shims := t.TempDir()
	if err := os.WriteFile(filepath.Join(shims, "uname"), []byte("#!/bin/sh\ncase \"$1\" in -m) echo x86_64 ;; *) echo Linux ;; esac\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := shims + ":" + narrow
	for _, tool := range []string{"curl", "wget", "gh", "go"} {
		for _, dir := range strings.Split(path, ":") {
			if _, err := os.Stat(filepath.Join(dir, tool)); err == nil {
				t.Fatalf("%s is on the narrow PATH, so this case is not offline", tool)
			}
		}
	}

	out, err := runFacetDispatcher(t, []string{"CLAUDE_PLUGIN_DATA=" + data, "FACET_VERSION=" + version, "PATH=" + path}, "--version")
	if err != nil || !strings.Contains(out, "offline-ran") {
		t.Fatalf("the installed version did not run offline: %v\n%s", err, out)
	}
}

// FACET_BINARY naming something that cannot run is a mistake to report, not a
// hint to look elsewhere — even when the cache could have answered.
func TestANonExecutableFacetBinaryFailsLoudly(t *testing.T) {
	skipUnlessShell(t)
	const version = "9.8.9"
	data := t.TempDir()
	dir := installFixture(t, data, upstreamDist, version, "cache-would-have-run", true)
	premiseRuns(t, filepath.Join(dir, "facet"), "cache-would-have-run")

	notExec := filepath.Join(t.TempDir(), "facet")
	if err := os.WriteFile(notExec, stubScript("override-ran"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{notExec, filepath.Join(t.TempDir(), "missing")} {
		out, err := runFacetDispatcher(t, append(offlineEnv(t, data), "FACET_VERSION="+version, "FACET_BINARY="+target), "--version")
		if err == nil {
			t.Fatalf("FACET_BINARY=%s was accepted:\n%s", target, out)
		}
		if !strings.Contains(out, target) {
			t.Errorf("the failure does not name %s:\n%s", target, out)
		}
		if strings.Contains(out, "cache-would-have-run") || strings.Contains(out, "override-ran") {
			t.Errorf("something ran despite the broken override:\n%s", out)
		}
	}
}

// pathStandIn writes an executable `facet` without a `#!` line, which is what
// passes the dispatcher's compiled-binary filter; the shell still runs it.
func pathStandIn(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "facet")
	if err := os.WriteFile(p, []byte(": compiled stand-in, no shebang\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// An old 1.x binary on PATH has no `version` command. It is passed over, named
// in the failure, and never handed the caller's arguments.
func TestAPathBinaryThatIsNotCoreIsSkippedAndNamed(t *testing.T) {
	skipUnlessShell(t)
	data := t.TempDir()
	old := pathStandIn(t, `[ "$1" = version ] && { echo "unknown command" >&2; exit 1; }
echo old-path-binary-ran
`)
	// The premise: it runs, and it would have printed its marker.
	if out, err := exec.Command("sh", "-c", filepath.Join(old, "facet")+" --version").CombinedOutput(); err != nil || !strings.Contains(string(out), "old-path-binary-ran") {
		t.Fatalf("the stand-in does not run (%v): %s", err, out)
	}
	env := offlineEnv(t, data)
	env[len(env)-1] = "PATH=" + old + ":" + strings.TrimPrefix(env[len(env)-1], "PATH=")

	out, err := runFacetDispatcher(t, env, "--version")
	if err == nil || strings.Contains(out, "old-path-binary-ran") {
		t.Fatalf("the 1.x stand-in was used (err %v):\n%s", err, out)
	}
	if !strings.Contains(out, filepath.Join(old, "facet")) || !strings.Contains(out, "version --json") {
		t.Errorf("the failure does not name the rejected candidate and why:\n%s", out)
	}
}

// One that answers as FACET Core is used; with an explicit FACET_VERSION only
// the exact version is.
func TestAPathBinaryThatIsCoreIsUsedAndAPinIsHonoured(t *testing.T) {
	skipUnlessShell(t)
	core := func(version, marker string) string {
		return pathStandIn(t, `if [ "$1" = version ]; then echo '{"product":"core","version":"`+version+`","distribution":"source"}'; exit 0; fi
echo `+marker+`
`)
	}
	devBuild := core("0.1.0-dev.1+gabc", "dev-path-binary-ran")
	pinned := core("9.7.0", "pinned-path-binary-ran")

	run := func(env []string, pathDirs ...string) (string, error) {
		data := t.TempDir()
		e := offlineEnv(t, data)
		e[len(e)-1] = "PATH=" + strings.Join(pathDirs, ":") + ":" + strings.TrimPrefix(e[len(e)-1], "PATH=")
		return runFacetDispatcher(t, append(e, env...), "--version")
	}

	if out, err := run(nil, devBuild); err != nil || !strings.Contains(out, "dev-path-binary-ran") {
		t.Errorf("a core binary on PATH was not used (err %v):\n%s", err, out)
	}

	out, err := run([]string{"FACET_VERSION=9.7.0"}, devBuild)
	if err == nil || strings.Contains(out, "dev-path-binary-ran") {
		t.Errorf("a core binary of another version was used for an explicit pin (err %v):\n%s", err, out)
	}
	if !strings.Contains(out, filepath.Join(devBuild, "facet")) || !strings.Contains(out, "9.7.0") {
		t.Errorf("the failure does not name the mismatching candidate:\n%s", out)
	}

	out, err = run([]string{"FACET_VERSION=9.7.0"}, devBuild, pinned)
	if err != nil || !strings.Contains(out, "pinned-path-binary-ran") || strings.Contains(out, "dev-path-binary-ran") {
		t.Errorf("the exactly pinned binary later on PATH was not the one used (err %v):\n%s", err, out)
	}

	// A release build reports <version>+g<commit>; the pin names the version.
	release := core("9.7.0+g8ff6ff5d1234", "release-path-binary-ran")
	out, err = run([]string{"FACET_VERSION=9.7.0"}, devBuild, release)
	if err != nil || !strings.Contains(out, "release-path-binary-ran") {
		t.Errorf("a release build of the pinned version was passed over for its build metadata (err %v):\n%s", err, out)
	}
	// The next version must not ride in on a shared prefix.
	longer := core("9.7.01", "longer-path-binary-ran")
	out, err = run([]string{"FACET_VERSION=9.7.0"}, longer)
	if err == nil || strings.Contains(out, "longer-path-binary-ran") {
		t.Errorf("a binary whose version only starts with the pin was used (err %v):\n%s", err, out)
	}
}

// Building from source is opt-in: with Go and the source right there, a plain
// run still never builds.
func TestNoSourceBuildWithoutFacetDevBuild(t *testing.T) {
	skipUnlessShell(t)
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go here, so leaving it unused proves nothing")
	}
	root, err := filepath.Abs(cliModuleRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "cmd", "facet")); err != nil {
		t.Fatalf("no source at %s, so this case is not about a build being declined: %v", root, err)
	}
	data := t.TempDir()
	env := offlineEnv(t, data)
	env[len(env)-1] += ":" + filepath.Dir(goBin)
	env = append(env, "CLAUDE_PLUGIN_ROOT="+root)

	out, err := runFacetDispatcher(t, env, "--version")
	if err == nil {
		t.Fatalf("the dispatcher resolved something with no release and no FACET_DEV_BUILD:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(data, "dist", "core", "source")); err == nil {
		t.Error("a source build was made without FACET_DEV_BUILD=1")
	}
	if !strings.Contains(out, "FACET_DEV_BUILD=1") {
		t.Errorf("the failure does not offer the explicit development build:\n%s", out)
	}
}

// FACET_DEV_BUILD=1 without Go stops and says so, rather than downloading or
// using a cached install instead.
func TestFacetDevBuildWithoutGoFailsInsteadOfFallingBack(t *testing.T) {
	skipUnlessShell(t)
	data := t.TempDir()
	dir := installFixture(t, data, upstreamDist, "0.1.0", "cached-instead-ran", true)
	premiseRuns(t, filepath.Join(dir, "facet"), "cached-instead-ran")
	env := offlineEnv(t, data)
	for _, d := range strings.Split(strings.TrimPrefix(env[len(env)-1], "PATH="), ":") {
		if _, err := os.Stat(filepath.Join(d, "go")); err == nil {
			t.Skip("a go on " + d + " is on the sandbox PATH")
		}
	}
	root, _ := filepath.Abs(cliModuleRoot(t))
	out, err := runFacetDispatcher(t, append(env, "FACET_DEV_BUILD=1", "CLAUDE_PLUGIN_ROOT="+root), "--version")
	if err == nil || strings.Contains(out, "cached-instead-ran") {
		t.Fatalf("FACET_DEV_BUILD=1 without Go fell back (err %v):\n%s", err, out)
	}
	if !strings.Contains(out, "Go") {
		t.Errorf("the failure does not name the missing Go:\n%s", out)
	}
}

// A download that breaks off mid-way leaves nothing that a later run selects,
// and nothing in staging.
func TestAFailedDownloadLeavesNothingSelectable(t *testing.T) {
	skipUnlessShell(t)
	const version = "9.6.0"
	shims := archiveShims(t)
	fixtures := t.TempDir()
	data := t.TempDir()
	archive := "facet_" + version + "_" + testPlatform + ".tar.gz"
	body := makeTarGz(t, "facet")
	writeFixture(t, filepath.Join(fixtures, archive), body)
	writeFixture(t, filepath.Join(fixtures, "checksums.txt"), []byte(sha256Hex(body)+"  "+archive+"\n"))
	witness := filepath.Join(t.TempDir(), "partial")

	out, err := runFacetDispatcher(t, []string{
		"CLAUDE_PLUGIN_DATA=" + data, "FACET_VERSION=" + version,
		"FACET_TEST_FIXTURES=" + fixtures, "FACET_TEST_CURL_PARTIAL=" + witness,
		"PATH=" + shims + ":" + sandboxPath(t),
	}, "--version")
	// The premise: the stub really did write bytes before it failed.
	if st, statErr := os.Stat(witness); statErr != nil || st.Size() == 0 {
		t.Fatalf("the stub curl wrote no partial bytes, so nothing was interrupted: %v", statErr)
	}
	if err == nil || strings.Contains(out, archiveStubMarker) {
		t.Fatalf("a broken download resolved (err %v):\n%s", err, out)
	}
	if got := installsUnder(t, data); len(got) != 0 {
		t.Errorf("a broken download left installs behind: %v", got)
	}
	if got := stagingLeftovers(t, data); len(got) != 0 {
		t.Errorf("a broken download left staging behind: %v", got)
	}
}

// A run interrupted while it downloads removes its staging directory.
func TestAnInterruptedDownloadLeavesNoStaging(t *testing.T) {
	skipUnlessShell(t)
	const version = "9.6.1"
	shims := archiveShims(t)
	fixtures := t.TempDir()
	data := t.TempDir()
	archive := "facet_" + version + "_" + testPlatform + ".tar.gz"
	body := makeTarGz(t, "facet")
	writeFixture(t, filepath.Join(fixtures, archive), body)
	writeFixture(t, filepath.Join(fixtures, "checksums.txt"), []byte(sha256Hex(body)+"  "+archive+"\n"))

	cmd := dispatcherCmd(t, []string{
		"CLAUDE_PLUGIN_DATA=" + data, "FACET_VERSION=" + version,
		"FACET_TEST_FIXTURES=" + fixtures, "FACET_TEST_CURL_DELAY=1",
		"PATH=" + shims + ":" + sandboxPath(t),
	}, "--version")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Wait until the download is under way: a partial archive in staging.
	deadline := time.Now().Add(10 * time.Second)
	for {
		matches, _ := filepath.Glob(filepath.Join(data, "dist", "core", ".staging", "*", "dl", archive))
		if len(matches) > 0 {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatal("the download never started, so nothing was interrupted")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("the interrupted run exited 0")
	}
	if got := installsUnder(t, data); len(got) != 0 {
		t.Errorf("an interrupted download left installs behind: %v", got)
	}
	if got := stagingLeftovers(t, data); len(got) != 0 {
		t.Errorf("an interrupted download left staging behind: %v", got)
	}
}

// Two dispatchers racing for the same release both run it, and exactly one
// complete install remains.
func TestTwoConcurrentDispatchersEndWithOneInstall(t *testing.T) {
	skipUnlessShell(t)
	const version = "9.5.0"
	shims := archiveShims(t)
	fixtures := t.TempDir()
	data := t.TempDir()
	barrier := t.TempDir()
	nested := filepath.Join(t.TempDir(), "nested")
	archive := "facet_" + version + "_" + testPlatform + ".tar.gz"
	body := makeTarGz(t, "facet")
	writeFixture(t, filepath.Join(fixtures, archive), body)
	writeFixture(t, filepath.Join(fixtures, "checksums.txt"), []byte(sha256Hex(body)+"  "+archive+"\n"))

	mv := mvShim(t, `case "${src##*/}" in
  install-*)
    if mkdir "$FACET_TEST_BARRIER/first" 2>/dev/null; then role=first; else role=second; fi
    : > "$FACET_TEST_BARRIER/arrived-$$"
    i=0
    while [ "$(ls "$FACET_TEST_BARRIER" | grep -c arrived-)" -lt 2 ] && [ "$i" -lt 400 ]; do sleep 0.05; i=$((i + 1)); done
    [ "$role" = first ] || sleep 0.5
    "$REALMV" "$@"; rc=$?
    [ ! -d "$dst/${src##*/}" ] || : > "$FACET_TEST_NESTED"
    exit "$rc" ;;
esac`)
	env := []string{
		"CLAUDE_PLUGIN_DATA=" + data, "FACET_VERSION=" + version,
		"FACET_TEST_FIXTURES=" + fixtures, "FACET_TEST_BARRIER=" + barrier, "FACET_TEST_NESTED=" + nested,
		"PATH=" + mv + ":" + shims + ":" + sandboxPath(t),
	}
	var wg sync.WaitGroup
	outs := make([]string, 2)
	errs := make([]error, 2)
	for i := range outs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b, err := dispatcherCmd(t, env, "--version").CombinedOutput()
			outs[i], errs[i] = string(b), err
		}(i)
	}
	wg.Wait()
	for i := range outs {
		if errs[i] != nil || !strings.Contains(outs[i], archiveStubMarker) {
			t.Errorf("run %d did not run the release (err %v):\n%s", i, errs[i], outs[i])
		}
	}
	// The premise: both runs reached the rename together, and the loser's copy
	// really did land inside the winner's install.
	arrived, _ := filepath.Glob(filepath.Join(barrier, "arrived-*"))
	if len(arrived) != 2 {
		t.Fatalf("%d run(s) reached the final rename, so there was no race", len(arrived))
	}
	if _, err := os.Stat(nested); err != nil {
		t.Fatal("the second rename did not land inside the first install, so the stray-copy cleanup was not exercised")
	}
	want := filepath.Join(upstreamDist, testPlatform, version)
	if got := installsUnder(t, data); len(got) != 1 || got[0] != want {
		t.Errorf("installs after the race = %v, want exactly [%s]", got, want)
	}
	if got := stagingLeftovers(t, data); len(got) != 0 {
		t.Errorf("the race left staging behind: %v", got)
	}
	entries, _ := os.ReadDir(filepath.Join(data, "dist", "core", want))
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("the install holds a stray directory %s, a losing copy moved inside it", e.Name())
		}
	}
}

// The trap removes only this run's staging directory. Under a BASE with a
// space in it, a space-split list of staging paths once handed `rm -rf` the
// part before the space — here, a sibling directory holding a sentinel.
func TestABaseWithASpaceLosesNothingOutsideStaging(t *testing.T) {
	skipUnlessShell(t)
	parent := t.TempDir()
	sentinel := filepath.Join(parent, "my", "sentinel")
	writeFixture(t, sentinel, []byte("keep me\n"))
	data := filepath.Join(parent, "my data", "facet")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	env := offlineEnv(t, data)
	env[0] = "CLAUDE_PLUGIN_DATA=" + data

	out, err := runFacetDispatcher(t, append(env, "FACET_VERSION=9.4.0"), "--version")
	if err == nil {
		t.Fatalf("the offline run resolved something:\n%s", out)
	}
	// The premise: the run got as far as creating a staging directory.
	if _, statErr := os.Stat(filepath.Join(data, "dist", "core", ".staging")); statErr != nil {
		t.Fatalf("no staging directory was ever made, so nothing was cleaned up: %v", statErr)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("a file outside the cache was deleted: %v\n%s", err, out)
	}
	if got := stagingLeftovers(t, data); len(got) != 0 {
		t.Errorf("staging was left behind: %v", got)
	}
}

// A directory with install.json but no runnable binary is not a complete
// install, and must not block that version for good.
func TestAnInstallWithoutABinaryDoesNotBlockItsVersion(t *testing.T) {
	skipUnlessShell(t)
	const version = "9.3.0"
	shims := archiveShims(t)
	fixtures := t.TempDir()
	data := t.TempDir()
	broken := filepath.Join(data, "dist", "core", upstreamDist, testPlatform, version)
	writeFixture(t, filepath.Join(broken, "install.json"), []byte(`{"product": "core"}`+"\n"))
	if _, err := os.Stat(filepath.Join(broken, "facet")); err == nil {
		t.Fatal("the broken fixture has a binary, so it is not broken")
	}
	archive := "facet_" + version + "_" + testPlatform + ".tar.gz"
	body := makeTarGz(t, "facet")
	writeFixture(t, filepath.Join(fixtures, archive), body)
	writeFixture(t, filepath.Join(fixtures, "checksums.txt"), []byte(sha256Hex(body)+"  "+archive+"\n"))

	out, err := runFacetDispatcher(t, []string{
		"CLAUDE_PLUGIN_DATA=" + data, "FACET_VERSION=" + version,
		"FACET_TEST_FIXTURES=" + fixtures, "PATH=" + shims + ":" + sandboxPath(t),
	}, "--version")
	if err != nil || !strings.Contains(out, archiveStubMarker) {
		t.Fatalf("a leftover without a binary blocked the install (err %v):\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(broken, "facet")); err != nil {
		t.Errorf("the version directory still holds no binary: %v", err)
	}
}

// The PATH probe runs with no stdin. The dispatcher serves MCP and LSP on
// stdio, so a probed binary that reads its input must not take the client's
// first message from the binary that finally runs.
func TestThePathProbeDoesNotConsumeStdin(t *testing.T) {
	skipUnlessShell(t)
	dir := pathStandIn(t, `if [ "$1" = version ]; then cat >/dev/null; echo '{"product":"core","version":"0.1.0-dev.1"}'; exit 0; fi
cat
`)
	data := t.TempDir()
	env := offlineEnv(t, data)
	env[len(env)-1] = "PATH=" + dir + ":" + strings.TrimPrefix(env[len(env)-1], "PATH=")
	cmd := dispatcherCmd(t, env, "mcp")
	cmd.Stdin = strings.NewReader("FIRST-CLIENT-MESSAGE\n")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "FIRST-CLIENT-MESSAGE") {
		t.Fatalf("the client's input did not reach the binary that ran (err %v):\n%s", err, out)
	}
}

// A FACET_VERSION that is not a version never reaches a path or a URL.
func TestAnUnsafeFacetVersionIsRefused(t *testing.T) {
	skipUnlessShell(t)
	data := t.TempDir()
	for _, v := range []string{"../../escape", "1.0.0/x", "1.0 0"} {
		out, err := runFacetDispatcher(t, append(offlineEnv(t, data), "FACET_VERSION="+v), "--version")
		if err == nil || !strings.Contains(out, "is not a version") || !strings.Contains(out, v) {
			t.Errorf("FACET_VERSION=%q was not refused by name (err %v):\n%s", v, err, out)
		}
	}
	if _, err := os.Stat(filepath.Join(data, "dist")); err == nil {
		t.Error("something was written to the cache for a refused version")
	}
}
