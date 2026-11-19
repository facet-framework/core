// Fixtures here contain example @facet: tags; this file drives the setup
// commands against throwaway repositories rather than implementing a feature.
// @facet-ignore-file
package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// harnessVersion is the version the versioned test binary reports. Status
// checks that compare against the running version need a binary that has one;
// the plain `go build` in facetBinary derives a development identity.
const harnessVersion = "9.9.9"

var (
	versionedOnce sync.Once
	versionedPath string
	versionedErr  error
)

// versionedFacetBinary builds the real binary once with a version stamped in,
// the way the dispatcher's own build does (-X main.version=...). It builds
// ./ with `go build`; it never runs the test binary.
func versionedFacetBinary(t *testing.T) string {
	t.Helper()
	versionedOnce.Do(func() {
		dir, err := os.MkdirTemp("", "facet-setup-bin-")
		if err != nil {
			versionedErr = err
			return
		}
		versionedPath = filepath.Join(dir, "facet")
		build := exec.Command("go", "build", "-ldflags", "-X main.version="+harnessVersion, "-o", versionedPath, cliCommandPackage(t))
		if out, err := build.CombinedOutput(); err != nil {
			versionedErr = err
			t.Logf("go build: %s", out)
		}
	})
	if versionedErr != nil {
		t.Skipf("cannot build the facet binary here: %v", versionedErr)
	}
	return versionedPath
}

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shells and symlinks are assumed here")
	}
}

// fakeLoginShell writes an executable that behaves like `sh -lc <script>`
// without sourcing any profile: the profile of the machine running the tests
// must not decide the outcome, and a distribution's /etc/profile resets PATH.
func fakeLoginShell(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	body := "#!/bin/sh\n# stands in for `<shell> -lc <script>`; the profile is not read\nshift\nexec /bin/sh -c \"$1\"\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// runBin runs the real binary in dir with exactly the environment given, so a
// variable of the machine running the tests cannot leak into the answer.
func runBin(t *testing.T, bin, dir string, env []string, stdin string, args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	if stdin != "" {
		cmd.Stdin = bytes.NewBufferString(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		exit = exitErr.ExitCode()
	default:
		t.Fatalf("%s %v: %v", bin, args, err)
	}
	return out.String(), errb.String(), exit
}

// baseEnv is the minimal environment a run needs: a PATH that finds git and sh,
// and a HOME that is not the real one.
func baseEnv(home string, extra ...string) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "LANG=C"}
	return append(env, extra...)
}

// mustMkdir creates a directory for a fixture.
func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}
