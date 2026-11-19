package facet_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/facet-framework/core/registry"
)

// pluginFile is one entry a test wants inside a built package, before
// SHA256SUMS is computed for it.
type pluginFile struct {
	name string
	body []byte
	mode os.FileMode // zero means the zip's ordinary file mode
}

func validManifest(id string) map[string]any {
	return map[string]any{
		"id":      id,
		"name":    "Test Plugin",
		"version": "1.0.0",
		"api":     0,
		"entry":   "plugin.js",
	}
}

// buildPluginDir writes a fresh temporary plugin directory from the supplied
// manifest and files. It computes SHA256SUMS for every file, including
// plugin.json, except SHA256SUMS itself.
func buildPluginDir(t *testing.T, manifest map[string]any, extra ...pluginFile) string {
	t.Helper()
	dir := t.TempDir()
	writeManifest(t, dir, manifest)
	for _, f := range extra {
		full := filepath.Join(dir, filepath.FromSlash(f.name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := f.mode
		if mode == 0 {
			mode = 0o644
		}
		if err := os.WriteFile(full, f.body, mode); err != nil {
			t.Fatal(err)
		}
	}
	writeSHA256SUMS(t, dir)
	return dir
}

func writeManifest(t *testing.T, dir string, manifest map[string]any) {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeSHA256SUMS measures every file in dir except SHA256SUMS itself, and
// writes the sums file from those measurements — the same thing facet.Pack
// does, reimplemented here deliberately so a fixture bug in Pack cannot also
// hide a bug in the verifier that checks it.
func writeSHA256SUMS(t *testing.T, dir string) {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "SHA256SUMS" {
			return nil
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(sum[:]), rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// --- §5 rule 4: manifest -----------------------------------------------

func TestValidPackageVerifies(t *testing.T) {
	dir := buildPluginDir(t, validManifest("acme-notes"), pluginFile{name: "plugin.js", body: []byte("export default function(){}")})
	m, err := facet.VerifyPackage(dir, facet.VerifyOptions{})
	if err != nil {
		t.Fatalf("a package built to satisfy every rule was refused: %v", err)
	}
	if m.ID != "acme-notes" {
		t.Fatalf("got id %q", m.ID)
	}
}

// VerifyPackage rejects uppercase letters and spaces in a plugin id.
func TestUppercaseIDIsRefused(t *testing.T) {
	m := validManifest("Acme Notes")
	dir := buildPluginDir(t, m, pluginFile{name: "plugin.js", body: []byte("x")})
	if _, err := facet.VerifyPackage(dir, facet.VerifyOptions{}); err == nil {
		t.Fatal("an id with spaces and capitals was accepted")
	}
}

func TestBundledIDIsRefused(t *testing.T) {
	dir := buildPluginDir(t, validManifest("wiki"), pluginFile{name: "plugin.js", body: []byte("x")})
	_, err := facet.VerifyPackage(dir, facet.VerifyOptions{})
	if !errors.Is(err, facet.ErrPluginIsBundled) {
		t.Fatalf("a package naming the bundled id 'wiki' was not refused as bundled: %v", err)
	}
}

func TestSchemaRejectsAdditionalTopLevelField(t *testing.T) {
	m := validManifest("acme-notes")
	m["css"] = "body{}" // additionalProperties: false in the schema
	dir := buildPluginDir(t, m, pluginFile{name: "plugin.js", body: []byte("x")})
	if _, err := facet.VerifyPackage(dir, facet.VerifyOptions{}); err == nil {
		t.Fatal("a manifest field the schema does not declare was accepted")
	}
}

// --- §5 rule 3: allow-list ------------------------------------------------

func TestCSSFileIsRefused(t *testing.T) {
	dir := buildPluginDir(t, validManifest("acme-notes"),
		pluginFile{name: "plugin.js", body: []byte("x")},
		pluginFile{name: "plugin.css", body: []byte("body{}")},
	)
	_, err := facet.VerifyPackage(dir, facet.VerifyOptions{})
	if !errors.Is(err, facet.ErrPluginEntryOutsideAllowList) {
		t.Fatalf("a .css file in the package was not refused: %v", err)
	}
}

func TestAssetsFolderIsAllowed(t *testing.T) {
	dir := buildPluginDir(t, validManifest("acme-notes"),
		pluginFile{name: "plugin.js", body: []byte("x")},
		pluginFile{name: "assets/icon.png", body: []byte("fakepng")},
	)
	if _, err := facet.VerifyPackage(dir, facet.VerifyOptions{}); err != nil {
		t.Fatalf("a file under assets/ was refused: %v", err)
	}
}

// --- §5 rule 5: checksums ---------------------------------------------

func TestSHA256SUMSMissingAFileIsRefused(t *testing.T) {
	dir := buildPluginDir(t, validManifest("acme-notes"), pluginFile{name: "plugin.js", body: []byte("x")})
	// Add a file AFTER SHA256SUMS was written, so it is present in the
	// package but not named in the sums file.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := facet.VerifyPackage(dir, facet.VerifyOptions{})
	if !errors.Is(err, facet.ErrPluginChecksumMismatch) {
		t.Fatalf("a file present but not named in SHA256SUMS was not refused: %v", err)
	}
}

func TestSHA256SUMSNamingAMissingFileIsRefused(t *testing.T) {
	dir := buildPluginDir(t, validManifest("acme-notes"), pluginFile{name: "plugin.js", body: []byte("x")})
	sums, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	extra := "0000000000000000000000000000000000000000000000000000000000000000  ghost.js\n"
	// The missing-file fixture uses a 64-digit hexadecimal SHA-256 value.
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), append(sums, []byte(extra)...), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = facet.VerifyPackage(dir, facet.VerifyOptions{})
	if !errors.Is(err, facet.ErrPluginChecksumMismatch) {
		t.Fatalf("SHA256SUMS naming a file that is not in the package was not refused: %v", err)
	}
}

func TestSidecarSumDisagreeingWithManifestIsRefused(t *testing.T) {
	platform := runtime.GOOS + "-" + runtime.GOARCH
	m := validManifest("acme-notes")
	m["sidecar"] = map[string]any{
		"bin":    "acmed",
		"sha256": map[string]string{platform: strings.Repeat("a", 64)}, // wrong on purpose
	}
	dir := buildPluginDir(t, m,
		pluginFile{name: "plugin.js", body: []byte("x")},
		pluginFile{name: "acmed-" + platform, body: []byte("binary-bytes"), mode: 0o755},
	)
	_, err := facet.VerifyPackage(dir, facet.VerifyOptions{})
	if !errors.Is(err, facet.ErrPluginChecksumMismatch) {
		t.Fatalf("a sidecar whose sha256 disagrees with plugin.json's own sidecar.sha256 was not refused: %v", err)
	}
}

func TestSidecarSumMatchingManifestPasses(t *testing.T) {
	platform := runtime.GOOS + "-" + runtime.GOARCH
	binBytes := []byte("binary-bytes")
	m := validManifest("acme-notes")
	m["sidecar"] = map[string]any{
		"bin":    "acmed",
		"sha256": map[string]string{platform: hashBytes(binBytes)},
	}
	dir := buildPluginDir(t, m,
		pluginFile{name: "plugin.js", body: []byte("x")},
		pluginFile{name: "acmed-" + platform, body: binBytes, mode: 0o644}, // deliberately not executable yet
	)
	manifest, err := facet.VerifyPackage(dir, facet.VerifyOptions{})
	if err != nil {
		t.Fatalf("a correctly matching sidecar sum was refused: %v", err)
	}
	if manifest.Sidecar == nil || manifest.Sidecar.Bin != "acmed" {
		t.Fatal("the parsed manifest lost its sidecar")
	}
	info, err := os.Stat(filepath.Join(dir, "acmed-"+platform))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatal("VerifyPackage did not set the execute bit on the sidecar (§5 rule 7)")
	}
}

// --- §5 rule 6: API -----------------------------------------------------

func TestUnofferedAPIIsRefused(t *testing.T) {
	m := validManifest("acme-notes")
	m["api"] = 7
	dir := buildPluginDir(t, m, pluginFile{name: "plugin.js", body: []byte("x")})
	_, err := facet.VerifyPackage(dir, facet.VerifyOptions{})
	if !errors.Is(err, facet.ErrPluginAPIMismatch) {
		t.Fatalf("a manifest asking for an API this reader does not offer was not refused: %v", err)
	}
	if !strings.Contains(err.Error(), "package asks for 7") || !strings.Contains(err.Error(), fmt.Sprintf("this reader offers %d", facet.HostPluginAPI)) {
		t.Fatalf("API refusal must name both actual versions: %v", err)
	}
}

// --- path safety, exercised through the zip-staging path ------------------

func writeZip(t *testing.T, entries map[string][]byte, modes map[string]os.FileMode) string {
	t.Helper()
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "pkg.facetplugin")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range entries {
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		if mode, ok := modes[name]; ok {
			hdr.SetMode(mode)
		} else {
			hdr.SetMode(0o644)
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return zipPath
}

func manifestBytes(t *testing.T, id string) []byte {
	t.Helper()
	raw, err := json.Marshal(validManifest(id))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func sumsBytes(entries map[string][]byte, skip ...string) []byte {
	skipSet := map[string]bool{}
	for _, s := range skip {
		skipSet[s] = true
	}
	var b strings.Builder
	for name, body := range entries {
		if skipSet[name] {
			continue
		}
		sum := sha256.Sum256(body)
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	return []byte(b.String())
}

func loadAndVerify(t *testing.T, zipPath string) (facet.PackageManifest, string, error) {
	t.Helper()
	work := t.TempDir() + "-stage"
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	sum, err := facet.LoadPackage(zipPath, work, facet.LoadOptions{})
	if err != nil {
		return facet.PackageManifest{}, sum, err
	}
	m, err := facet.VerifyPackage(work, facet.VerifyOptions{})
	return m, sum, err
}

func TestPathTraversalEntryIsRefused(t *testing.T) {
	entries := map[string][]byte{
		"plugin.json": manifestBytes(t, "acme-notes"),
		"plugin.js":   []byte("x"),
		"../evil.txt": []byte("escaped"),
	}
	entries["SHA256SUMS"] = sumsBytes(entries, "SHA256SUMS")
	zipPath := writeZip(t, entries, nil)

	work := t.TempDir()
	sibling := filepath.Join(filepath.Dir(work), "evil.txt")
	os.Remove(sibling)
	_, _, err := loadAndVerify(t, zipPath)
	if err == nil {
		t.Fatal("a zip entry naming ../evil.txt was accepted")
	}
	if _, statErr := os.Stat(sibling); statErr == nil {
		t.Fatal("a path-traversal entry wrote outside the staging directory")
	}
}

func TestAbsolutePathEntryIsRefused(t *testing.T) {
	entries := map[string][]byte{
		"plugin.json":  manifestBytes(t, "acme-notes"),
		"plugin.js":    []byte("x"),
		"/etc/evil.js": []byte("escaped"),
	}
	entries["SHA256SUMS"] = sumsBytes(entries, "SHA256SUMS")
	zipPath := writeZip(t, entries, nil)
	if _, _, err := loadAndVerify(t, zipPath); err == nil {
		t.Fatal("an absolute-path zip entry was accepted")
	}
}

func TestBackslashEntryIsRefused(t *testing.T) {
	entries := map[string][]byte{
		"plugin.json":     manifestBytes(t, "acme-notes"),
		"plugin.js":       []byte("x"),
		`assets\icon.png`: []byte("x"),
	}
	entries["SHA256SUMS"] = sumsBytes(entries, "SHA256SUMS")
	zipPath := writeZip(t, entries, nil)
	if _, _, err := loadAndVerify(t, zipPath); err == nil {
		t.Fatal("a zip entry carrying a backslash was accepted")
	}
}

func TestSymlinkEntryIsRefused(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "pkg.facetplugin")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)

	entries := map[string][]byte{
		"plugin.json": manifestBytes(t, "acme-notes"),
		"plugin.js":   []byte("x"),
	}
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(body)
	}
	// A symlink entry: mode carries ModeSymlink, and the "content" is the
	// link target text, the way zip actually stores one.
	hdr := &zip.FileHeader{Name: "evil-link", Method: zip.Store}
	hdr.SetMode(os.ModeSymlink | 0o777)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("/etc/passwd"))

	sums := sumsBytes(entries, "SHA256SUMS")
	w, err = zw.Create("SHA256SUMS")
	if err != nil {
		t.Fatal(err)
	}
	w.Write(sums)

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if _, _, err := loadAndVerify(t, zipPath); err == nil {
		t.Fatal("a symlink entry inside the zip was accepted")
	}
}

func TestCaseInsensitiveDuplicateIsRefused(t *testing.T) {
	entries := map[string][]byte{
		"plugin.json":     manifestBytes(t, "acme-notes"),
		"plugin.js":       []byte("x"),
		"assets/Icon.png": []byte("a"),
		"assets/icon.png": []byte("b"),
	}
	entries["SHA256SUMS"] = sumsBytes(entries, "SHA256SUMS")
	zipPath := writeZip(t, entries, nil)
	if _, _, err := loadAndVerify(t, zipPath); err == nil {
		t.Fatal("two entries differing only by case were accepted")
	}
}

// TestHighlyCompressedFileExceedsUnpackedLimit checks a regular ZIP whose
// compressed size is small but whose unpacked file exceeds the size limit.
// The headers are produced by zip.Writer without manipulation.
func TestHighlyCompressedFileExceedsUnpackedLimit(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "pkg.facetplugin")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)

	entries := map[string][]byte{
		"plugin.json": manifestBytes(t, "acme-notes"),
	}
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(body)
	}

	// Zero bytes compress strongly, making a small archive whose unpacked
	// plugin.js exceeds the per-file limit.
	big := bytes.Repeat([]byte{0}, 70<<20) // 70 MiB > the 64 MiB per-file limit
	w, err := zw.Create("plugin.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(big); err != nil {
		t.Fatal(err)
	}

	sums := sumsBytes(map[string][]byte{"plugin.js": big})
	w, err = zw.Create("SHA256SUMS")
	if err != nil {
		t.Fatal(err)
	}
	w.Write(sums)

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	// Assert that the archive is small on disk before checking its much larger
	// unpacked size against the per-file limit.
	info, err := os.Stat(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 5<<20 {
		t.Fatalf("fixture precondition failed: the archive is %d bytes, expected it compressed far below the 64 MiB unpacked limit", info.Size())
	}

	if _, _, err := loadAndVerify(t, zipPath); err == nil {
		t.Fatal("a file whose decompressed size exceeds the per-file limit was accepted despite its small compressed size")
	}
}

// --- §4: single top-level folder -----------------------------------------

func TestSingleTopFolderZipIsAccepted(t *testing.T) {
	entries := map[string][]byte{
		"acme-notes/plugin.json": manifestBytes(t, "acme-notes"),
		"acme-notes/plugin.js":   []byte("x"),
	}
	entries["acme-notes/SHA256SUMS"] = prefixedSums(entries, "acme-notes/")
	zipPath := writeZip(t, entries, nil)
	m, _, err := loadAndVerify(t, zipPath)
	if err != nil {
		t.Fatalf("a zip with one top-level folder containing plugin.json was refused: %v", err)
	}
	if m.ID != "acme-notes" {
		t.Fatalf("got id %q", m.ID)
	}
}

func prefixedSums(entries map[string][]byte, prefix string) []byte {
	var b strings.Builder
	for name, body := range entries {
		if name == prefix+"SHA256SUMS" {
			continue
		}
		rel := strings.TrimPrefix(name, prefix)
		sum := sha256.Sum256(body)
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(sum[:]), rel)
	}
	return []byte(b.String())
}

func TestTwoLevelZipIsRefused(t *testing.T) {
	entries := map[string][]byte{
		"outer/inner/plugin.json": manifestBytes(t, "acme-notes"),
		"outer/inner/plugin.js":   []byte("x"),
	}
	entries["outer/inner/SHA256SUMS"] = prefixedSums(entries, "outer/inner/")
	zipPath := writeZip(t, entries, nil)
	if _, _, err := loadAndVerify(t, zipPath); err == nil {
		t.Fatal("a zip with plugin.json two levels deep was accepted")
	}
}

// --- https / github sources -------------------------------------------

func TestHTTPSWithoutSha256IsRefused(t *testing.T) {
	work := t.TempDir()
	_, err := facet.LoadPackage("https://example.invalid/pkg.facetplugin", work, facet.LoadOptions{})
	if !errors.Is(err, facet.ErrPluginHTTPRequiresChecksum) {
		t.Fatalf("an https source with no --sha256 was not refused: %v", err)
	}
}

func TestHTTPSChecksumMismatchLeavesNothingOnDisk(t *testing.T) {
	entries := map[string][]byte{
		"plugin.json": manifestBytes(t, "acme-notes"),
		"plugin.js":   []byte("x"),
	}
	entries["SHA256SUMS"] = sumsBytes(entries, "SHA256SUMS")
	zipPath := writeZip(t, entries, nil)
	zipBytes, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	srv := newStaticHTTPServer(t, map[string][]byte{"/pkg.facetplugin": zipBytes})
	defer srv.Close()

	work := t.TempDir()
	sub := filepath.Join(work, "stage")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = facet.LoadPackage(srv.URL+"/pkg.facetplugin", sub, facet.LoadOptions{SHA256: strings.Repeat("f", 64), HTTPClient: srv.Client()})
	if !errors.Is(err, facet.ErrPluginChecksumMismatch) {
		t.Fatalf("a wrong --sha256 did not produce a checksum-mismatch error: %v", err)
	}
	entriesOnDisk, err := os.ReadDir(sub)
	if err != nil {
		t.Fatal(err)
	}
	if len(entriesOnDisk) != 0 {
		t.Fatalf("a checksum mismatch left %d entries staged on disk, expected none", len(entriesOnDisk))
	}
}

func TestHTTPSCorrectChecksumSucceeds(t *testing.T) {
	entries := map[string][]byte{
		"plugin.json": manifestBytes(t, "acme-notes"),
		"plugin.js":   []byte("x"),
	}
	entries["SHA256SUMS"] = sumsBytes(entries, "SHA256SUMS")
	zipPath := writeZip(t, entries, nil)
	zipBytes, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	srv := newStaticHTTPServer(t, map[string][]byte{"/pkg.facetplugin": zipBytes})
	defer srv.Close()

	work := t.TempDir()
	sum := hashBytes(zipBytes)
	if _, err := facet.LoadPackage(srv.URL+"/pkg.facetplugin", work, facet.LoadOptions{SHA256: sum, HTTPClient: srv.Client()}); err != nil {
		t.Fatalf("a correctly matching https download was refused: %v", err)
	}
	if _, err := facet.VerifyPackage(work, facet.VerifyOptions{}); err != nil {
		t.Fatalf("staged https package failed verification: %v", err)
	}
}

func TestGitHubSourceWithMismatchingChecksumsIsRefused(t *testing.T) {
	entries := map[string][]byte{
		"plugin.json": manifestBytes(t, "acme-notes"),
		"plugin.js":   []byte("x"),
	}
	entries["SHA256SUMS"] = sumsBytes(entries, "SHA256SUMS")
	zipPath := writeZip(t, entries, nil)
	zipBytes, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	// Pretty-printed across lines, the way GitHub's own API answers — and the
	// shape parseReleaseAssets's line scanner (mirroring bin/facet's
	// jq-free parsing) actually depends on.
	releaseJSON := "{\n  \"assets\": [\n    {\n      \"name\": \"acme-notes-1.0.0.facetplugin\",\n      \"browser_download_url\": \"__DOWNLOAD__/acme-notes-1.0.0.facetplugin\"\n    }\n  ]\n}\n"
	// A checksums.txt naming the WRONG hash for the asset.
	wrongChecksums := strings.Repeat("a", 64) + "  acme-notes-1.0.0.facetplugin\n"

	mux := map[string][]byte{}
	srv := newRoutedHTTPServer(t, mux)
	releaseJSON = strings.ReplaceAll(releaseJSON, "__DOWNLOAD__", srv.URL)
	mux["/repos/acme/facet-notes/releases/tags/v1.0.0"] = []byte(releaseJSON)
	mux["/acme-notes-1.0.0.facetplugin"] = zipBytes
	mux["/acme/facet-notes/releases/download/v1.0.0/checksums.txt"] = []byte(wrongChecksums)

	work := t.TempDir()
	_, err = facet.LoadPackage("github:acme/facet-notes@v1.0.0", work, facet.LoadOptions{
		GitHub: &facet.GitHubSource{APIBase: srv.URL, DownloadBase: srv.URL, HTTPClient: srv.Client()},
	})
	if !errors.Is(err, facet.ErrPluginChecksumMismatch) {
		t.Fatalf("a github source whose checksums.txt names the wrong hash was not refused: %v", err)
	}
}

func TestGitHubSourceWithMatchingChecksumsSucceeds(t *testing.T) {
	entries := map[string][]byte{
		"plugin.json": manifestBytes(t, "acme-notes"),
		"plugin.js":   []byte("x"),
	}
	entries["SHA256SUMS"] = sumsBytes(entries, "SHA256SUMS")
	zipPath := writeZip(t, entries, nil)
	zipBytes, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	correctSum := hashBytes(zipBytes)
	releaseJSONTemplate := "{\n  \"assets\": [\n    {\n      \"name\": \"acme-notes-1.0.0.facetplugin\",\n      \"browser_download_url\": \"__DOWNLOAD__/acme-notes-1.0.0.facetplugin\"\n    }\n  ]\n}\n"
	checksums := correctSum + "  acme-notes-1.0.0.facetplugin\n"

	mux := map[string][]byte{}
	srv := newRoutedHTTPServer(t, mux)
	mux["/repos/acme/facet-notes/releases/tags/v1.0.0"] = []byte(strings.ReplaceAll(releaseJSONTemplate, "__DOWNLOAD__", srv.URL))
	mux["/acme-notes-1.0.0.facetplugin"] = zipBytes
	mux["/acme/facet-notes/releases/download/v1.0.0/checksums.txt"] = []byte(checksums)

	work := t.TempDir()
	_, err = facet.LoadPackage("github:acme/facet-notes@v1.0.0", work, facet.LoadOptions{
		GitHub: &facet.GitHubSource{APIBase: srv.URL, DownloadBase: srv.URL, HTTPClient: srv.Client()},
	})
	if err != nil {
		t.Fatalf("a github source whose checksums.txt matches was refused: %v", err)
	}
	if _, err := facet.VerifyPackage(work, facet.VerifyOptions{}); err != nil {
		t.Fatalf("staged github package failed verification: %v", err)
	}
}

// --- AddedRights (§6 replace-keeps-grants rule) --------------------------

func TestAddedRightsIsFalseWhenNothingNew(t *testing.T) {
	a := facet.PackageManifest{Tools: []string{"repo.write"}}
	b := facet.PackageManifest{Tools: []string{"repo.write"}}
	if a.AddedRights(b) {
		t.Fatal("identical tool lists were reported as adding a right")
	}
}

func TestAddedRightsIsTrueWhenANewToolAppears(t *testing.T) {
	a := facet.PackageManifest{Tools: []string{"repo.write"}}
	b := facet.PackageManifest{Tools: []string{"repo.write", "repo.commit"}}
	if !a.AddedRights(b) {
		t.Fatal("a new tool was not reported as adding a right")
	}
}

func TestAddedRightsIsTrueWhenANewEnvVarAppears(t *testing.T) {
	a := facet.PackageManifest{Sidecar: &facet.ManifestSidecar{Env: []string{"A"}}}
	b := facet.PackageManifest{Sidecar: &facet.ManifestSidecar{Env: []string{"A", "B"}}}
	if !a.AddedRights(b) {
		t.Fatal("a new environment variable was not reported as adding a right")
	}
}

// A grant for docs/adr does not authorize another directory added by an update.
func TestAddedRightsIsTrueWhenANewReadDirectoryAppears(t *testing.T) {
	a := facet.PackageManifest{Tools: []string{"repo.read"}, Reads: []facet.ReadDecl{{Dir: "docs/adr"}}}
	b := facet.PackageManifest{Tools: []string{"repo.read"}, Reads: []facet.ReadDecl{{Dir: "docs/adr"}, {Dir: "docs"}}}
	if !a.AddedRights(b) {
		t.Fatal("a widened reads list was not reported as adding a right")
	}
}

// --- "reads": schema and runtime grammar agree ------------------

// readDirCases is one table for both gates — the schema at install and
// facet.ValidReadDir at every call — so the two cannot drift apart without
// one of the two tests below going red.
var readDirCases = []struct {
	dir string
	ok  bool
}{
	{"docs/adr", true},
	{"docs/decisions_2026", true},
	{"docs/v1.2", true},
	{".", false},
	{"..", false},
	{"./docs", false},
	{"docs/..", false},
	{"docs/../..", false},
	{".git", false},
	{"docs/.hidden", false},
	{"/etc", false},
	{"docs//adr", false},
	{"docs/adr/", false},
	{`docs\adr`, false},
	{"", false},
}

// Mutation: widen the "reads" item pattern in both schema copies back to
// `^(?!/)(?!.*\.\.)[A-Za-z0-9_./-]+$` → red on ".", "./docs", ".git".
func TestSchemaRefusesAReadsDirectoryOutsideTheGrammar(t *testing.T) {
	for _, c := range readDirCases {
		m := validManifest("acme-notes")
		m["reads"] = []string{c.dir}
		dir := buildPluginDir(t, m, pluginFile{name: "plugin.js", body: []byte("x")})
		_, err := facet.VerifyPackage(dir, facet.VerifyOptions{})
		if c.ok && err != nil {
			t.Errorf("reads %q was refused: %v", c.dir, err)
		}
		if !c.ok && err == nil {
			t.Errorf("reads %q was accepted", c.dir)
		}
	}
}

// Mutation: make ValidReadDir return true → red on every refused case.
func TestValidReadDirAgreesWithTheSchema(t *testing.T) {
	for _, c := range readDirCases {
		if got := facet.ValidReadDir(c.dir); got != c.ok {
			t.Errorf("ValidReadDir(%q) = %v, want %v", c.dir, got, c.ok)
		}
	}
}

// --- "reads" with extensions (#dmn) ----------------------------------------

// readsExtCases is one table for both gates of an extension in the object
// form — the schema at install and ValidReadExt at every call.
var readsExtCases = []struct {
	ext string
	ok  bool
}{
	{".dmn", true},
	{".md", true},
	{".json", true},
	{".a1", true},
	{".abcdefghij", true},
	{".abcdefghijk", false}, // eleven characters
	{"dmn", false},          // no dot
	{".", false},
	{"..", false},
	{".DMN", false}, // uppercase
	{".dm.n", false},
	{".d-n", false},
	{"", false},
	{".env", false}, // conventionally holds secrets
	{".pem", false},
	{".key", false},
	{".p12", false},
	{".pfx", false},
	{".kdbx", false},
	{".htpasswd", false},
}

// Mutation: widen the "ext" item pattern in both schema copies to
// `^\\.[a-zA-Z0-9.]{1,10}$` → red on ".DMN" and ".dm.n".
func TestSchemaRefusesAReadsExtensionOutsideTheGrammar(t *testing.T) {
	for _, c := range readsExtCases {
		m := validManifest("acme-rules")
		m["reads"] = []any{map[string]any{"dir": "rules", "ext": []string{c.ext}}}
		dir := buildPluginDir(t, m, pluginFile{name: "plugin.js", body: []byte("x")})
		_, err := facet.VerifyPackage(dir, facet.VerifyOptions{})
		if c.ok && err != nil {
			t.Errorf("reads ext %q was refused: %v", c.ext, err)
		}
		if !c.ok && err == nil {
			t.Errorf("reads ext %q was accepted", c.ext)
		}
	}
}

// Mutation: make ValidReadExt return true → red on every refused case.
func TestValidReadExtAgreesWithTheSchema(t *testing.T) {
	for _, c := range readsExtCases {
		if got := facet.ValidReadExt(c.ext); got != c.ok {
			t.Errorf("ValidReadExt(%q) = %v, want %v", c.ext, got, c.ok)
		}
	}
}

// The package check validates the whole object, not only its extensions.
// Mutation: set "additionalProperties": true on the object form of "reads"
// in both schema copies → red on the unknown key; remove "required": ["dir",
// "ext"] → red on the missing ext and the empty list.
func TestSchemaRefusesAMalformedReadsObject(t *testing.T) {
	cases := map[string]any{
		"unknown key":      map[string]any{"dir": "rules", "ext": []string{".dmn"}, "write": true},
		"missing ext":      map[string]any{"dir": "rules"},
		"empty ext list":   map[string]any{"dir": "rules", "ext": []string{}},
		"duplicate ext":    map[string]any{"dir": "rules", "ext": []string{".dmn", ".dmn"}},
		"bad dir":          map[string]any{"dir": "../rules", "ext": []string{".dmn"}},
		"missing dir":      map[string]any{"ext": []string{".dmn"}},
		"a number entry":   7,
		"hidden dir":       map[string]any{"dir": "rules/.git", "ext": []string{".dmn"}},
		"ext not an array": map[string]any{"dir": "rules", "ext": ".dmn"},
	}
	for name, entry := range cases {
		m := validManifest("acme-rules")
		m["reads"] = []any{entry}
		dir := buildPluginDir(t, m, pluginFile{name: "plugin.js", body: []byte("x")})
		if _, err := facet.VerifyPackage(dir, facet.VerifyOptions{}); err == nil {
			t.Errorf("%s: reads entry %v was accepted", name, entry)
		}
	}
	// Precondition: the well-formed object and the string form do verify,
	// so the refusals above are about the entries and not the fixture.
	for _, ok := range []any{
		map[string]any{"dir": "rules", "ext": []string{".dmn"}},
		"docs/adr",
	} {
		m := validManifest("acme-rules")
		m["reads"] = []any{ok}
		dir := buildPluginDir(t, m, pluginFile{name: "plugin.js", body: []byte("x")})
		if _, err := facet.VerifyPackage(dir, facet.VerifyOptions{}); err != nil {
			t.Fatalf("fixture precondition: reads entry %v was refused: %v", ok, err)
		}
	}
}

// Widening the extensions of an already-declared directory is a new right;
// the string form and the same directory with [".md"] are the same right.
// Mutation: make readRights return only d.Dir for each entry → red on the
// widened extension.
func TestAddedRightsIsTrueWhenAReadExtensionIsAdded(t *testing.T) {
	obj := func(exts ...string) facet.PackageManifest {
		raw := fmt.Sprintf(`{"reads":[{"dir":"rules","ext":%s}]}`, mustJSON(t, exts))
		var m facet.PackageManifest
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	a, b := obj(".dmn"), obj(".dmn", ".xml")
	if len(a.Reads) != 1 || len(a.Reads[0].Exts()) != 1 {
		t.Fatalf("fixture precondition: parsed reads = %+v", a.Reads)
	}
	if !a.AddedRights(b) {
		t.Fatal("an added extension was not reported as adding a right")
	}
	if b.AddedRights(a) {
		t.Fatal("dropping an extension was reported as adding a right")
	}
	str := facet.PackageManifest{Reads: []facet.ReadDecl{{Dir: "rules"}}}
	md := obj(".md")
	if str.AddedRights(md) || md.AddedRights(str) {
		t.Fatal("the string form and the object form with [\".md\"] are the same right")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Both forms parse, and each is written back in the form it came in.
// Mutation: make UnmarshalJSON ignore the object branch (always parse a
// string) → red (the object form fails to parse).
func TestReadsParsesBothFormsAndKeepsThem(t *testing.T) {
	var m facet.PluginManifest
	raw := `{"id":"x","reads":["docs/adr",{"dir":"rules","ext":[".dmn"]}]}`
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("manifest with both forms did not parse: %v", err)
	}
	if len(m.Reads) != 2 {
		t.Fatalf("fixture precondition: parsed %d entries, want 2", len(m.Reads))
	}
	if got := m.Reads[0].Exts(); len(got) != 1 || got[0] != ".md" {
		t.Errorf("string form Exts = %v, want [.md]", got)
	}
	if got := m.Reads[1].Exts(); len(got) != 1 || got[0] != ".dmn" {
		t.Errorf("object form Exts = %v, want [.dmn]", got)
	}
	out, err := json.Marshal(m.Reads)
	if err != nil || string(out) != `["docs/adr",{"dir":"rules","ext":[".dmn"]}]` {
		t.Errorf("re-marshalled reads = %s (%v)", out, err)
	}
	var bad facet.PluginManifest
	if err := json.Unmarshal([]byte(`{"reads":[{"dir":"a","ext":[".x"],"more":1}]}`), &bad); err == nil {
		t.Error("an object with an unknown key parsed")
	}
}

// ai_env is a right of its own: an update that offers the agent a variable
// the old manifest gave only the surface widens what a granted ai_env lets
// through. Mutation: delete the manifestAIEnv branch in AddedRights → red.
func TestAddedRightsIsTrueWhenANewAIEnvVarAppears(t *testing.T) {
	a := facet.PackageManifest{Sidecar: &facet.ManifestSidecar{Env: []string{"A", "B"}, AIEnv: []string{"A"}}}
	b := facet.PackageManifest{Sidecar: &facet.ManifestSidecar{Env: []string{"A", "B"}, AIEnv: []string{"A", "B"}}}
	if !a.AddedRights(b) {
		t.Fatal("a variable newly offered to the agent was not reported as adding a right")
	}
}

func TestGenericFilesAndForgedHostMetadataAreRefusedInRawPackages(t *testing.T) {
	for _, name := range []string{"notes.txt", "source.ts", "secret.env", "grants.json", ".source.json"} {
		t.Run(name, func(t *testing.T) {
			dir := buildPluginDir(t, validManifest("acme-notes"), pluginFile{name: "plugin.js", body: []byte("x")}, pluginFile{name: name, body: []byte("{}")})
			_, err := facet.VerifyPackage(dir, facet.VerifyOptions{})
			if !errors.Is(err, facet.ErrPluginEntryOutsideAllowList) {
				t.Fatalf("raw package accepted %q or refused for wrong reason: %v", name, err)
			}
		})
	}
}
func TestMixedTopLevelZipShapesAreRefused(t *testing.T) {
	entries := map[string][]byte{"one/plugin.json": manifestBytes(t, "acme-notes"), "one/plugin.js": []byte("x"), "two/README.md": []byte("other root")}
	entries["one/SHA256SUMS"] = prefixedSums(entries, "one/")
	if _, _, err := loadAndVerify(t, writeZip(t, entries, nil)); err == nil || !strings.Contains(err.Error(), "single top-level folder") {
		t.Fatalf("different top-level folders not explicitly refused: %v", err)
	}
	root := map[string][]byte{"plugin.json": manifestBytes(t, "acme-notes"), "plugin.js": []byte("x"), "extra/plugin.json": manifestBytes(t, "acme-other")}
	root["SHA256SUMS"] = prefixedSums(root, "")
	if _, _, err := loadAndVerify(t, writeZip(t, root, nil)); !errors.Is(err, facet.ErrPluginEntryOutsideAllowList) {
		t.Fatalf("root manifest mixed with another package not refused: %v", err)
	}
}
