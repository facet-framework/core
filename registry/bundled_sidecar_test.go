// White-box tests for the bundled wiki plugin's sidecar resolution:
// where the binary is found, what checksum it is
// checked against, and what happens when neither is trustworthy.

package facet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These two string literals must match helperEnv and markerEnv in
// sidecar_test.go exactly. They cannot be imported: that file is package
// facet_test, which this in-package test cannot import.
const (
	bundledTestHelperEnv = "FACET_SIDECAR_HELPER"
	bundledTestMarkerEnv = "FACET_SIDECAR_MARKER"
)

// installBundledFixture copies this test binary to <dir>/<name>, so a
// "bundled" resolution has a real, hashable, runnable executable waiting
// where bundledSidecarExecutable's directory says to look.
func installBundledFixture(t *testing.T, dir, name string) (path, sum string) {
	t.Helper()
	path = filepath.Join(dir, name)
	src, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatalf("could not read this test binary to use as a fixture: %v", err)
	}
	if err := os.WriteFile(path, src, 0o755); err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	if _, err := h.Write(src); err != nil {
		t.Fatal(err)
	}
	sum = hex.EncodeToString(h.Sum(nil))
	return path, sum
}

// withEchoingBundledWiki points the bundled wiki manifest's sidecar env at
// the two variables sidecar_test.go's TestMain re-execs on, so a bundled
// call reaches a real answering process. Restores bundledRegistry.manifests["wiki"]
// afterward.
func withEchoingBundledWiki(t *testing.T) {
	t.Helper()
	original := bundledRegistry.manifests["wiki"]
	widened := original
	env := make([]string, len(original.Sidecar.Env))
	copy(env, original.Sidecar.Env)
	widened.Sidecar = &ManifestSidecar{
		Bin: original.Sidecar.Bin,
		Env: append(env, bundledTestHelperEnv, bundledTestMarkerEnv),
	}
	bundledRegistry.manifests["wiki"] = widened
	t.Cleanup(func() { bundledRegistry.manifests["wiki"] = original })
	t.Setenv(bundledTestHelperEnv, "echo")
}

// A bundled plugin's sidecar is resolved beside the facet executable, not
// under any plugins directory — and an empty plugins directory is the
// broker's own proof that no directory-based path was consulted, since
// nothing else there could have answered.
func TestABundledSidecarIsResolvedBesideTheFacetExecutable(t *testing.T) {
	fixtureDir := t.TempDir()
	_, sum := installBundledFixture(t, fixtureDir, "confluence")
	fakeExe := filepath.Join(fixtureDir, "facet")
	if err := os.WriteFile(fakeExe, []byte("not a real facet binary, just a path"), 0o755); err != nil {
		t.Fatal(err)
	}

	restoreExe, restoreSum := bundledSidecarExecutable, bundledSidecarSum
	bundledSidecarExecutable = func() (string, error) { return fakeExe, nil }
	bundledSidecarSum = func(string) string { return sum }
	t.Cleanup(func() { bundledSidecarExecutable, bundledSidecarSum = restoreExe, restoreSum })
	withEchoingBundledWiki(t)

	pluginsDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(pluginsDir, "wiki"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"confluence", filepath.Join("wiki", "confluence-"+runtime.GOOS+"-"+runtime.GOARCH)} {
		if err := os.WriteFile(filepath.Join(pluginsDir, name), []byte("decoy outside the executable directory"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	b := NewPluginBroker(pluginsDir)
	defer b.StopAll()

	if _, err := b.Call(context.Background(), "wiki", "confluence.state", nil); err != nil {
		t.Fatalf("a bundled sidecar placed beside the fake executable was not found: %v", err)
	}
}

// The checksum a bundled sidecar is checked against is the one this build
// measured and embedded (bundledSidecarSum) — never the manifest's own
// sha256 map, which stays empty for a bundled plugin (see bundledManifests'
// comment). A binary that does not match that measured sum is refused
// before it is ever started.
func TestATamperedBundledSidecarIsRefusedBeforeItRuns(t *testing.T) {
	fixtureDir := t.TempDir()
	actualPath, actualSum := installBundledFixture(t, fixtureDir, "confluence")
	fakeExe := filepath.Join(fixtureDir, "facet")
	if err := os.WriteFile(fakeExe, []byte("not a real facet binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	restoreExe, restoreSum := bundledSidecarExecutable, bundledSidecarSum
	bundledSidecarExecutable = func() (string, error) { return fakeExe, nil }
	// A wrong sum on purpose: the fixture at fixtureDir/confluence (a copy of
	// this test binary) does not hash to this.
	bundledSidecarSum = func(string) string {
		return "0000000000000000000000000000000000000000000000000000000000000000"
	}
	t.Cleanup(func() { bundledSidecarExecutable, bundledSidecarSum = restoreExe, restoreSum })

	withEchoingBundledWiki(t)
	marker := filepath.Join(t.TempDir(), "ran")
	t.Setenv(bundledTestMarkerEnv, marker)
	bundledSidecarSum = func(string) string { return actualSum }
	b := NewPluginBroker(t.TempDir())
	if _, err := b.Call(context.Background(), "wiki", "confluence.state", nil); err != nil {
		t.Fatalf("valid fixture %s cannot execute: %v", actualPath, err)
	}
	b.StopAll()
	if _, err := os.Stat(marker); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	bundledSidecarSum = func(string) string { return strings.Repeat("0", 64) }
	defer b.StopAll()

	_, err := b.Call(context.Background(), "wiki", "confluence.state", nil)
	if err == nil {
		t.Fatal("a bundled sidecar whose checksum does not match was allowed to run")
	}
	if !errors.Is(err, ErrSidecarChecksum) {
		t.Fatalf("the refusal was not the checksum error: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("tampered bundled fixture executed: %v", err)
	}
}

// A registered companion with no embedded checksum must be refused rather than
// executed without verification. The synthetic fixture exercises registration
// and checksum lookup; the diagnostic also retains the development build
// repair command used by distributions that provide it.
func TestABuildWithNoEmbeddedSumRefusesRatherThanInventingOne(t *testing.T) {
	fixtureDir := t.TempDir()
	installBundledFixture(t, fixtureDir, "confluence")
	fakeExe := filepath.Join(fixtureDir, "facet")
	if err := os.WriteFile(fakeExe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}

	restoreExe, restoreSum := bundledSidecarExecutable, bundledSidecarSum
	bundledSidecarExecutable = func() (string, error) { return fakeExe, nil }
	bundledSidecarSum = func(string) string { return "" }
	t.Cleanup(func() { bundledSidecarExecutable, bundledSidecarSum = restoreExe, restoreSum })

	b := NewPluginBroker(t.TempDir())
	defer b.StopAll()

	_, err := b.Call(context.Background(), "wiki", "confluence.state", nil)
	if err == nil {
		t.Fatal("a build with no embedded checksum ran the bundled sidecar anyway")
	}
	if !strings.Contains(err.Error(), "build-sidecars.sh") {
		t.Fatalf("the refusal did not name the fix: %v", err)
	}
	// Not a checksum mismatch either — a build with nothing measured must not
	// be told "the checksum is wrong", which would send a person looking for
	// a tampered file that does not exist.
	if errors.Is(err, ErrSidecarChecksum) {
		t.Fatal("an unmeasured build was refused as a checksum mismatch instead of naming the real cause")
	}
}
