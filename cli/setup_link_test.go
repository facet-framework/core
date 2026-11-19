// Fixtures here contain example @facet: tags; this file drives
// `facet setup link` against throwaway directories rather than implementing a
// feature.
// @facet-ignore-file
package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// linkFixture is a machine that has run the dispatcher: a plugin data
// directory holding two versions of one distribution, its `current` pointer and
// the `selected` pointer to that, a home directory, and a fake login shell
// whose PATH is the one given.
type linkFixture struct {
	home    string
	data    string
	bin     string // the directory the link goes into, on the shell's PATH
	env     []string
	pointer string // data/dist/core/<distribution>/<platform>/current
}

// linkFixtureDist is the distribution/platform directory the fixture installs into.
var linkFixtureDist = filepath.Join("dist", "core", "github-facet-framework-core", "linux_amd64")

// installDistFixture lays out what the dispatcher leaves behind: versions under
// one distribution, `current` at the first of them, and `selected` at current.
func installDistFixture(t *testing.T, data string, versions ...string) string {
	t.Helper()
	for _, v := range versions {
		p := filepath.Join(data, linkFixtureDist, v, "facet")
		write(t, p, "#!/bin/sh\necho facet-"+v+"\n")
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(data, linkFixtureDist, v, "install.json"), `{"product": "core", "version": "`+v+`"}`)
	}
	pointer := filepath.Join(data, linkFixtureDist, "current")
	if err := os.Symlink(versions[0], pointer); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("github-facet-framework-core", "linux_amd64", "current"), filepath.Join(data, "dist", "core", "selected")); err != nil {
		t.Fatal(err)
	}
	return pointer
}

// Each fake "facet" prints its own version, so what a link resolves to can be
// asked by running it.
func newLinkFixture(t *testing.T, onPath bool) linkFixture {
	t.Helper()
	skipOnWindows(t)
	home := t.TempDir()
	data := filepath.Join(home, "plugin-data")
	pointer := installDistFixture(t, data, "1.0.0", "2.0.0")
	bin := filepath.Join(home, ".local", "bin")
	mustMkdir(t, bin)
	path := "/usr/bin:/bin"
	if onPath {
		path = bin + ":" + path
	}
	return linkFixture{
		home: home, data: data, bin: bin, pointer: pointer,
		env: baseEnv(home, "PATH="+path, "CLAUDE_PLUGIN_DATA="+data, "SHELL="+fakeLoginShell(t, "zsh")),
	}
}

func (f linkFixture) run(t *testing.T, args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	return runBin(t, facetBinary(t), f.home, f.env, "", append([]string{"setup", "link"}, args...)...)
}

// runLinked executes the installed link and returns what the binary behind it
// says — the only way to know where a link really goes.
func runLinked(t *testing.T, link string) string {
	t.Helper()
	out, _, exit := runBin(t, link, t.TempDir(), baseEnv(t.TempDir()), "")
	if exit != 0 {
		t.Fatalf("%s exited %d", link, exit)
	}
	return strings.TrimSpace(out)
}

func swapCurrent(t *testing.T, f linkFixture, version string) {
	t.Helper()
	if err := os.Remove(f.pointer); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(version, f.pointer); err != nil {
		t.Fatal(err)
	}
}

// The link must follow `selected`, not the version it held on the day it was
// made, or it stops being true at the next update.
func TestLinkSurvivesAnUpdateOfTheCurrentPointer(t *testing.T) {
	f := newLinkFixture(t, true)
	stdout, stderr, exit := f.run(t, "--dir", f.bin)
	if exit != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	link := filepath.Join(f.bin, "facet")
	if got := runLinked(t, link); got != "facet-1.0.0" {
		t.Fatalf("before the update the link runs %q, want facet-1.0.0", got)
	}
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(f.data, "dist", "core", "selected", "facet"); target != want {
		t.Fatalf("link target = %q, want the version-free %q", target, want)
	}
	swapCurrent(t, f, "2.0.0")
	if got := runLinked(t, link); got != "facet-2.0.0" {
		t.Fatalf("after the update the link runs %q, want facet-2.0.0", got)
	}
}

// Mutation: drop the onPath check in linkInto — the link is then created.
func TestLinkRefusesADirectoryOffThePathAndNamesTheLineToAdd(t *testing.T) {
	f := newLinkFixture(t, false)
	stdout, stderr, exit := f.run(t, "--dir", f.bin)
	if exit == 0 {
		t.Fatalf("a directory off the login shell's PATH was accepted:\n%s", stdout)
	}
	if _, err := os.Lstat(filepath.Join(f.bin, "facet")); err == nil {
		t.Fatal("a link was made in a directory that is not on the PATH")
	}
	for _, want := range []string{"~/.zprofile", `export PATH="$HOME/.local/bin:$PATH"`, "--force"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	// Nothing edits a startup file, ever.
	for _, rc := range []string{".zshrc", ".bashrc", ".profile", ".zprofile", ".config/fish/config.fish"} {
		if _, err := os.Stat(filepath.Join(f.home, rc)); err == nil {
			t.Errorf("%s was created", rc)
		}
	}
}

// --force links anyway and says what to add, still without touching a file.
func TestLinkForceLinksAndStillPrintsTheRCLine(t *testing.T) {
	f := newLinkFixture(t, false)
	stdout, stderr, exit := f.run(t, "--dir", f.bin, "--force")
	if exit != 0 {
		t.Fatalf("exit %d: %s", exit, stderr)
	}
	if got := runLinked(t, filepath.Join(f.bin, "facet")); got != "facet-1.0.0" {
		t.Fatalf("link runs %q", got)
	}
	if !strings.Contains(stdout, `export PATH="$HOME/.local/bin:$PATH"`) || !strings.Contains(stdout, "~/.zprofile") {
		t.Fatalf("the rc line was not printed:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(f.home, ".zshrc")); err == nil {
		t.Fatal(".zshrc was written")
	}
}

// A facet that is somebody's own binary is never replaced.
func TestLinkLeavesARegularFileAlone(t *testing.T) {
	f := newLinkFixture(t, true)
	existing := filepath.Join(f.bin, "facet")
	write(t, existing, "#!/bin/sh\necho mine\n")
	if err := os.Chmod(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	_, stderr, exit := f.run(t, "--dir", f.bin)
	if exit == 0 || !strings.Contains(stderr, "not a symlink") {
		t.Fatalf("a regular file was not refused (exit %d): %s", exit, stderr)
	}
	if got := runLinked(t, existing); got != "mine" {
		t.Fatalf("the file was replaced; it now runs %q", got)
	}
}

// Only a link into a facet cache may be replaced.
func TestLinkReplacesOnlyALinkIntoTheFacetCache(t *testing.T) {
	f := newLinkFixture(t, true)
	link := filepath.Join(f.bin, "facet")

	foreign := filepath.Join(t.TempDir(), "other-tool")
	write(t, foreign, "#!/bin/sh\necho foreign\n")
	if err := os.Chmod(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, link); err != nil {
		t.Fatal(err)
	}
	_, stderr, exit := f.run(t, "--dir", f.bin)
	if exit == 0 || !strings.Contains(stderr, "not a facet cache") {
		t.Fatalf("a foreign symlink was not refused (exit %d): %s", exit, stderr)
	}
	if got := runLinked(t, link); got != "foreign" {
		t.Fatalf("the foreign link was replaced; it now runs %q", got)
	}

	// A stale link into the cache (an older, versioned one) is replaced.
	os.Remove(link)
	if err := os.Symlink(filepath.Join(f.data, linkFixtureDist, "1.0.0", "facet"), link); err != nil {
		t.Fatal(err)
	}
	swapCurrent(t, f, "2.0.0")
	if stdout, stderr, exit := f.run(t, "--dir", f.bin); exit != 0 {
		t.Fatalf("a link into the cache was not replaced (exit %d): %s %s", exit, stdout, stderr)
	}
	if got := runLinked(t, link); got != "facet-2.0.0" {
		t.Fatalf("the replaced link runs %q, want facet-2.0.0", got)
	}
	if target, _ := os.Readlink(link); strings.Contains(target, "/1.0.0/") {
		t.Fatalf("the replaced link still names a versioned path: %s", target)
	}

	// A link this command made against the retired bin/<version> cache is
	// recognised and replaced too, even in a cache that no longer holds it.
	os.Remove(link)
	elsewhere := filepath.Join(t.TempDir(), "facet", "bin", "1.20.1", "facet")
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Fatal(err)
	}
	if stdout, stderr, exit := f.run(t, "--dir", f.bin); exit != 0 {
		t.Fatalf("a link into the retired cache layout was not replaced (exit %d): %s %s", exit, stdout, stderr)
	}
	if got := runLinked(t, link); got != "facet-2.0.0" {
		t.Fatalf("the replaced link runs %q, want facet-2.0.0", got)
	}
}

// Mutation: skip the filter `underHome` (offer /usr/bin), or skip the
// writable probe (offer the read-only directory).
func TestLinkCandidatesAreWritableDirectoriesUnderHomeOnTheLoginShellPath(t *testing.T) {
	f := newLinkFixture(t, true)
	missing := filepath.Join(f.home, "missing")
	readOnly := filepath.Join(f.home, "readonly")
	mustMkdir(t, readOnly)
	if err := os.Chmod(readOnly, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(readOnly, 0o755)
	offPath := filepath.Join(f.home, "bin")
	mustMkdir(t, offPath)
	// Precondition: the directory really is not writable here (not root).
	if dirWritable(readOnly) {
		t.Skip("running as a user who can write a 0555 directory")
	}
	f.env = append(f.env, "PATH="+strings.Join([]string{f.bin, missing, readOnly, "/usr/bin", "/bin"}, ":"))

	stdout, stderr, exit := f.run(t, "--candidates")
	if exit != 0 {
		t.Fatalf("exit %d: %s", exit, stderr)
	}
	if !strings.Contains(stdout, "shell: zsh") {
		t.Errorf("the detected shell is not named:\n%s", stdout)
	}
	if !strings.Contains(stdout, "\n  "+f.bin+"\n") {
		t.Errorf("the writable directory on the PATH is not offered:\n%s", stdout)
	}
	for name, dir := range map[string]string{"missing": missing, "read-only": readOnly, "off the PATH": offPath, "system": "/usr/bin"} {
		if strings.Contains(stdout, "  "+dir+"\n") {
			t.Errorf("the %s directory %s was offered:\n%s", name, dir, stdout)
		}
	}
}

// --candidates and --dir are exclusive, and one of them is required.
func TestLinkNeedsExactlyOneMode(t *testing.T) {
	f := newLinkFixture(t, true)
	if _, _, exit := f.run(t); exit == 0 {
		t.Error("no mode was accepted")
	}
	if _, _, exit := f.run(t, "--candidates", "--dir", f.bin); exit == 0 {
		t.Error("both modes were accepted")
	}
}

// Without a binary behind `selected` nothing is linked: a link to a pointer that
// resolves to nothing would be found by the shell and fail when run.
func TestLinkRefusesWhenCurrentHoldsNoBinary(t *testing.T) {
	f := newLinkFixture(t, true)
	if err := os.Remove(f.pointer); err != nil {
		t.Fatal(err)
	}
	_, stderr, exit := f.run(t, "--dir", f.bin)
	if exit == 0 || !strings.Contains(stderr, "no binary behind") {
		t.Fatalf("exit %d: %s", exit, stderr)
	}
	if _, err := os.Lstat(filepath.Join(f.bin, "facet")); err == nil {
		t.Fatal("a link was made to nothing")
	}
}

// Windows gets a wrapper, not a symlink — checked in-process, because the
// platform under test is a variable.
func TestLinkWritesACmdWrapperOnWindowsAndKeepsAForeignOne(t *testing.T) {
	skipOnWindows(t)
	old := setupGOOS
	setupGOOS = "windows"
	defer func() { setupGOOS = old }()

	home := t.TempDir()
	data := filepath.Join(home, "data")
	installDistFixture(t, data, "1.0.0")
	bin := filepath.Join(home, "tools")
	mustMkdir(t, bin)
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_PLUGIN_DATA", data)
	t.Setenv("PATH", bin)
	t.Setenv("SHELL", "")

	run := func() (string, error) {
		cmd := newSetupLinkCmd()
		var out strings.Builder
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs([]string{"--dir", bin})
		err := cmd.Execute()
		return out.String(), err
	}
	out, err := run()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	wrapper := filepath.Join(bin, "facet.cmd")
	body, err := os.ReadFile(wrapper)
	if err != nil {
		t.Fatalf("no wrapper written: %v", err)
	}
	if !strings.Contains(string(body), filepath.Join(data, "dist", "core", "selected", "facet")) || !strings.Contains(string(body), "%*") {
		t.Fatalf("the wrapper does not call the selected pointer with its arguments:\n%s", body)
	}
	if _, err := os.Lstat(filepath.Join(bin, "facet")); err == nil {
		t.Fatal("a symlink was made on Windows")
	}
	if _, err := run(); err != nil {
		t.Fatalf("a wrapper this command wrote could not be replaced: %v", err)
	}

	write(t, wrapper, "@echo off\r\necho mine\r\n")
	if _, err := run(); err == nil {
		t.Fatal("a facet.cmd somebody else wrote was replaced")
	}
	if got, _ := os.ReadFile(wrapper); !strings.Contains(string(got), "mine") {
		t.Fatalf("the foreign wrapper was overwritten: %s", got)
	}
}

// The file the instruction names must be one the measuring shell actually
// reads. The PATH is read with `<shell> -lc`, so a line in the file an
// interactive terminal reads (.zshrc, .bashrc) would never be seen.
func TestTheNamedStartupFileIsTheOneALoginShellReads(t *testing.T) {
	skipOnWindows(t)
	for _, name := range []string{"zsh", "bash"} {
		t.Run(name, func(t *testing.T) {
			sh, err := exec.LookPath(name)
			if err != nil {
				t.Skipf("%s is not installed here, so what its login shell reads cannot be measured", name)
			}
			home := t.TempDir()
			marker := filepath.Join(home, "marker-bin")
			file, line := rcInstruction(loginShell{Path: sh, Name: name}, marker, home)
			rc := filepath.Join(home, strings.TrimPrefix(file, "~/"))
			if !strings.HasPrefix(file, "~/") {
				t.Fatalf("unexpected instruction file %q", file)
			}
			// $HOME is the fixture home inside the child, so the printed line works as is.
			write(t, rc, line+"\n")
			cmd := exec.Command(sh, "-lc", "echo $PATH")
			cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "ZDOTDIR=" + home}
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("%s -lc: %v", name, err)
			}
			if !strings.Contains(string(out), marker) {
				t.Fatalf("%s -lc does not read %s, yet the instruction names it; PATH = %s", name, file, out)
			}
		})
	}
}
