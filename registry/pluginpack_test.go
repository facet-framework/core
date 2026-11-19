package facet_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/facet-framework/core/registry"
)

// buildAuthorFolder writes a plugin folder the way an AUTHOR would leave it —
// no SHA256SUMS at all, because that file is exactly what Pack must produce.
func buildAuthorFolder(t *testing.T, manifest map[string]any, files map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestPackThenAddRoundTrips(t *testing.T) {
	src := buildAuthorFolder(t, validManifest("acme-notes"), map[string][]byte{
		"plugin.js": []byte("export default function(){}"),
	})

	out := filepath.Join(t.TempDir(), "acme-notes-1.0.0.facetplugin")
	packed, err := facet.Pack(src, out)
	if err != nil {
		t.Fatalf("packing a folder that satisfies every rule but SHA256SUMS was refused: %v", err)
	}
	if packed.ID != "acme-notes" {
		t.Fatalf("got id %q", packed.ID)
	}

	work := t.TempDir() + "-stage"
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := facet.LoadPackage(out, work, facet.LoadOptions{}); err != nil {
		t.Fatalf("loading pack's own output was refused: %v", err)
	}
	// Mutation: skip plugin.json in measureSums (the bug this fixture was
	// written against) and this line fails with "plugin.json is in the
	// package but not in SHA256SUMS" — pack's own output would not survive
	// the add it exists to feed.
	if _, err := facet.VerifyPackage(work, facet.VerifyOptions{}); err != nil {
		t.Fatalf("a package pack wrote could not verify itself: %v", err)
	}
}

// TestPackMeasuresRatherThanTakesTheAuthorsWord proves the sums Pack writes
// are its own, not copied from a stale value already on disk — the same
// "measured, not typed" promise VerifyPackage's checkSums enforces on the
// reading side.
func TestPackMeasuresRatherThanTakesTheAuthorsWord(t *testing.T) {
	src := buildAuthorFolder(t, validManifest("acme-notes"), map[string][]byte{
		"plugin.js": []byte("export default function(){}"),
	})
	out := filepath.Join(t.TempDir(), "pkg.facetplugin")
	if _, err := facet.Pack(src, out); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	if _, err := facet.LoadPackage(out, work, facet.LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	sums, err := os.ReadFile(filepath.Join(work, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) == 0 {
		t.Fatal("Pack wrote an empty SHA256SUMS")
	}
}

// A folder with a sidecar whose sidecar.sha256 is still empty — the state an
// author leaves it in, because filling that map is what pack does — packs, and
// the package carries the binary and its measured sum.
func TestPackFillsAnEmptySidecarSumFromTheBinary(t *testing.T) {
	platform := runtime.GOOS + "-" + runtime.GOARCH
	bin := "probe-" + platform
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	m := validManifest("acme-lint")
	m["sidecar"] = map[string]any{"bin": "probe", "sha256": map[string]any{}}
	binary := []byte("not really an executable, but bytes to measure")
	src := buildAuthorFolder(t, m, map[string][]byte{
		"plugin.js": []byte("export default function(){}"),
		bin:         binary,
	})

	out := filepath.Join(t.TempDir(), "acme-lint-1.0.0.facetplugin")
	packed, err := facet.Pack(src, out)
	if err != nil {
		t.Fatalf("a folder with a sidecar and no sums yet could not be packed: %v", err)
	}
	want := sha256.Sum256(binary)
	if got := packed.Sidecar.SHA256[platform]; got != hex.EncodeToString(want[:]) {
		t.Fatalf("the manifest's sidecar sum is %q, not the binary's", got)
	}

	work := t.TempDir()
	if _, err := facet.LoadPackage(out, work, facet.LoadOptions{}); err != nil {
		t.Fatalf("loading pack's own output was refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, bin)); err != nil {
		t.Fatalf("the packed archive does not carry the sidecar binary: %v", err)
	}
	if _, err := facet.VerifyPackage(work, facet.VerifyOptions{}); err != nil {
		t.Fatalf("a package with a sidecar that pack wrote could not verify itself: %v", err)
	}
}

// The loosening is for the manifest's own sidecar on a platform the schema
// knows — nothing else at the root gets in through it.
func TestPackStillRefusesAFileThatIsNotTheSidecar(t *testing.T) {
	m := validManifest("acme-lint")
	m["sidecar"] = map[string]any{"bin": "probe", "sha256": map[string]any{}}
	src := buildAuthorFolder(t, m, map[string][]byte{
		"plugin.js":           []byte("export default function(){}"),
		"probe-freebsd-amd64": []byte("a platform the schema does not know"),
	})
	out := filepath.Join(t.TempDir(), "pkg.facetplugin")
	if _, err := facet.Pack(src, out); err == nil {
		t.Fatal("a file that is not a known sidecar build was packed")
	}
}
