// Exercises the release-archive half of the shell dispatcher in bin/facet
// rather than implementing a feature of its own.
// @facet-ignore-file

package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// What the stub binary prints, so a test can tell "it ran" from "it exists".
const archiveStubMarker = "stub-from-the-archive-ran"

func archiveStub() []byte {
	return []byte("#!/bin/sh\necho " + archiveStubMarker + "\n")
}

// A machine the dispatcher can be driven across without a network or a release.
func archiveShims(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("uname", `#!/bin/sh
case "$1" in
  -m) printf '%s\n' "$FACET_TEST_UNAME_M" ;;
  *)  printf '%s\n' "$FACET_TEST_UNAME_S" ;;
esac
`)
	// Only the flags bin/facet actually passes: -fsL, -o <path>, -H <header>.
	write("curl", `#!/bin/sh
out=
url=
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -H) shift 2 ;;
    -*) shift ;;
    *)  url="$1"; shift ;;
  esac
done
[ -n "$url" ] || exit 1
[ -n "$out" ] || exit 1
src="$FACET_TEST_FIXTURES/${url##*/}"
[ -f "$src" ] || exit 22
# A transfer that breaks off: some bytes written, then curl's partial-file exit.
if [ -n "$FACET_TEST_CURL_PARTIAL" ]; then
  head -c 16 "$src" > "$out"
  cp "$out" "$FACET_TEST_CURL_PARTIAL"
  exit 18
fi
# A slow transfer: the partial file exists while the delay runs.
if [ -n "$FACET_TEST_CURL_DELAY" ]; then
  head -c 16 "$src" > "$out"
  sleep "$FACET_TEST_CURL_DELAY"
fi
cp "$src" "$out"
`)
	write("gh", "#!/bin/sh\nexit 1\n")
	return dir
}

// The shims ahead of a PATH on which no facet can be found. A `go` there does
// not matter: the dispatcher builds from source only under FACET_DEV_BUILD=1,
// which runDispatcher clears.
func archivePath(t *testing.T, shims string) string {
	t.Helper()
	return shims + ":" + sandboxPath(t)
}

// cachedPath is where a release from the default upstream lands for platform.
func cachedPath(cache, platform, version, name string) string {
	return filepath.Join(cache, "dist", "core", upstreamDist, platform, version, name)
}

func writeFixture(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A Windows release ZIP contains the binary under its .exe name alongside
// the release companion files.
func makeZip(t *testing.T, member string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, mode os.FileMode, body []byte) {
		t.Helper()
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	add("README.md", 0o644, []byte("readme\n"))
	add(member, 0o755, archiveStub())
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	// The premise: this archive really does carry that member, executable.
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("the fixture zip is not readable, so no case on it measures anything: %v", err)
	}
	found := false
	for _, f := range zr.File {
		if f.Name == member {
			found = true
			if f.Mode()&0o111 == 0 {
				t.Fatalf("the fixture stored %s without an executable bit, so the dispatcher would reject it for the wrong reason", member)
			}
		}
	}
	if !found {
		t.Fatalf("the fixture zip has no %s; this case would measure the fallback, not the member", member)
	}
	return buf.Bytes()
}

// A tar.gz shaped like the one published for every other platform.
func makeTarGz(t *testing.T, member string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	add := func(name string, mode int64, body []byte) {
		t.Helper()
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	add("README.md", 0o644, []byte("readme\n"))
	add(member, 0o755, archiveStub())
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}

	// The premise, read back rather than assumed.
	gr, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("the fixture tarball is not readable: %v", err)
	}
	tr := tar.NewReader(gr)
	for {
		h, err := tr.Next()
		if err != nil {
			t.Fatalf("the fixture tarball has no %s; this case would measure the fallback, not the member", member)
		}
		if h.Name == member {
			return buf.Bytes()
		}
	}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Runs the dispatcher against one fixture directory and returns its combined
// output plus the error, because half these cases are about the failure.
func runDispatcher(t *testing.T, cache, fixtures, path, unameS, unameM, version string) (string, error) {
	t.Helper()
	dispatcher, err := filepath.Abs(filepath.Join(cliModuleRoot(t), "bin/facet"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", dispatcher, "--version")
	cmd.Env = append(cmd.Environ(),
		"CLAUDE_PLUGIN_DATA="+cache,
		"FACET_VERSION="+version,
		"FACET_TEST_FIXTURES="+fixtures,
		"FACET_TEST_UNAME_S="+unameS,
		"FACET_TEST_UNAME_M="+unameM,
		"PATH="+path,
		// Otherwise a developer's own settings leak into the fixture.
		"FACET_REPO=", "FACET_REPOS=", "FACET_BINARY=", "FACET_DEV_BUILD=",
		"FACET_RELEASE_TAG=", "GH_TOKEN=", "GITHUB_TOKEN=",
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func skipUnlessPOSIX(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the dispatcher is a POSIX shell script")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell here")
	}
}

// Windows uses a zip archive and an .exe member; other platforms use tar.gz.
func TestTheWindowsArchiveIsAZipAndItsBinaryCarriesExe(t *testing.T) {
	skipUnlessPOSIX(t)
	if _, err := exec.LookPath("unzip"); err != nil {
		t.Skip("no unzip here; the zip path cannot be exercised")
	}

	const version = "9.9.9"
	shims := archiveShims(t)
	path := archivePath(t, shims)
	fixtures := t.TempDir()
	cache := t.TempDir()

	archive := "facet_" + version + "_windows_amd64.zip"
	body := makeZip(t, "facet.exe")
	writeFixture(t, filepath.Join(fixtures, archive), body)
	writeFixture(t, filepath.Join(fixtures, "checksums.txt"),
		[]byte(fmt.Sprintf("%s  %s\n", sha256Hex(body), archive)))

	// Only the ZIP is available, so choosing tar.gz cannot accidentally succeed
	// against a second fixture archive.
	if _, err := os.Stat(filepath.Join(fixtures, "facet_"+version+"_windows_amd64.tar.gz")); err == nil {
		t.Fatal("a tar.gz is present too, so this case cannot tell the two names apart")
	}

	out, err := runDispatcher(t, cache, fixtures, path, "MINGW64_NT-10.0-22631", "x86_64", version)
	if err != nil {
		t.Fatalf("the dispatcher could not resolve the Windows release: %v\n%s", err, out)
	}
	if !strings.Contains(out, archiveStubMarker) {
		t.Errorf("the binary from the archive did not run; got:\n%s", out)
	}

	// Unpacked under the name the archive actually uses. Checking for `facet`
	// here is the mistake that survives a correct download.
	if _, err := os.Stat(cachedPath(cache, "windows_amd64", version, "facet.exe")); err != nil {
		t.Errorf("no facet.exe in the cache after a Windows resolve: %v", err)
	}
}

// Linux resolves a tar.gz archive, complementing the Windows ZIP case to
// check both branches of platform archive selection.
func TestTheLinuxArchiveIsStillATarball(t *testing.T) {
	skipUnlessPOSIX(t)

	const version = "9.9.8"
	shims := archiveShims(t)
	path := archivePath(t, shims)
	fixtures := t.TempDir()
	cache := t.TempDir()

	archive := "facet_" + version + "_linux_amd64.tar.gz"
	body := makeTarGz(t, "facet")
	writeFixture(t, filepath.Join(fixtures, archive), body)
	writeFixture(t, filepath.Join(fixtures, "checksums.txt"),
		[]byte(fmt.Sprintf("%s  %s\n", sha256Hex(body), archive)))

	if _, err := os.Stat(filepath.Join(fixtures, "facet_"+version+"_linux_amd64.zip")); err == nil {
		t.Fatal("a zip is present too, so this case cannot tell the two names apart")
	}

	out, err := runDispatcher(t, cache, fixtures, path, "Linux", "x86_64", version)
	if err != nil {
		t.Fatalf("the dispatcher could not resolve the Linux release: %v\n%s", err, out)
	}
	if !strings.Contains(out, archiveStubMarker) {
		t.Errorf("the binary from the archive did not run; got:\n%s", out)
	}
	if _, err := os.Stat(cachedPath(cache, "linux_amd64", version, "facet")); err != nil {
		t.Errorf("no facet in the cache after a Linux resolve: %v", err)
	}
	// The install records where it came from, written last as its marker.
	meta, err := os.ReadFile(cachedPath(cache, "linux_amd64", version, "install.json"))
	if err != nil {
		t.Fatalf("no install.json beside the binary: %v", err)
	}
	for _, want := range []string{
		`"product": "core"`, `"distribution": "` + upstreamDist + `"`, `"platform": "linux_amd64"`,
		`"version": "` + version + `"`, `"archive_sha256": "` + sha256Hex(body) + `"`,
		`"source_repo": "facet-framework/core"`, `"release_tag": "v` + version + `"`, `"binary_sha256": "` + sha256Hex(archiveStub()) + `"`,
	} {
		if !strings.Contains(string(meta), want) {
			t.Errorf("install.json lacks %s:\n%s", want, meta)
		}
	}
	// A fresh download moves both version-free pointers too, not only a
	// cache hit, and the durable path runs the new install.
	if target, err := os.Readlink(filepath.Join(cache, "dist", "core", upstreamDist, "linux_amd64", "current")); err != nil || target != version {
		t.Errorf("current after a download = %q (%v), want %q", target, err, version)
	}
	if target, err := os.Readlink(filepath.Join(cache, "dist", "core", "selected")); err != nil || target != upstreamDist+"/linux_amd64/current" {
		t.Errorf("selected after a download = %q (%v)", target, err)
	}
	if got, err := exec.Command(filepath.Join(cache, "dist", "core", "selected", "facet")).CombinedOutput(); err != nil || !strings.Contains(string(got), archiveStubMarker) {
		t.Errorf("the download is not reachable through selected (%v): %s", err, got)
	}
}

// The checksum must come from the exact archive filename. A similar filename
// with an incorrect hash appears first to distinguish exact matching from
// pattern or substring matching.
func TestTheChecksumLineIsMatchedByExactName(t *testing.T) {
	skipUnlessPOSIX(t)
	const version = "9.9.3"
	shims := archiveShims(t)
	path := archivePath(t, shims)
	fixtures := t.TempDir()
	cache := t.TempDir()
	archive := "facet_" + version + "_linux_amd64.tar.gz"
	lookalike := "facet_" + version + "_linux_amd64Xtar.gz"
	body := makeTarGz(t, "facet")
	writeFixture(t, filepath.Join(fixtures, archive), body)
	const wrong = "1111111111111111111111111111111111111111111111111111111111111111"
	writeFixture(t, filepath.Join(fixtures, "checksums.txt"),
		[]byte(fmt.Sprintf("%s  %s\n%s  %s\n", wrong, lookalike, sha256Hex(body), archive)))

	out, err := runDispatcher(t, cache, fixtures, path, "Linux", "x86_64", version)
	if err != nil || !strings.Contains(out, archiveStubMarker) {
		t.Fatalf("the right checksum line was not the one used (err %v):\n%s", err, out)
	}
}

// binary-verification promises a mismatch stops everything and names the
// source. The zip path is a second way into that comparison, and a promise
// that holds on one platform is not the promise.
func TestAMismatchedZipStopsEverything(t *testing.T) {
	skipUnlessPOSIX(t)
	if _, err := exec.LookPath("unzip"); err != nil {
		t.Skip("no unzip here; the zip path cannot be exercised")
	}

	const version = "9.9.7"
	shims := archiveShims(t)
	path := archivePath(t, shims)
	fixtures := t.TempDir()
	cache := t.TempDir()

	archive := "facet_" + version + "_windows_amd64.zip"
	body := makeZip(t, "facet.exe")
	writeFixture(t, filepath.Join(fixtures, archive), body)

	const wrong = "0000000000000000000000000000000000000000000000000000000000000000"
	// The premise: the list really does disagree with the bytes. A fixture that
	// accidentally stated the right hash would make this case pass while
	// proving the opposite.
	if wrong == sha256Hex(body) {
		t.Fatal("the fixture's stated hash matches the archive, so nothing is being rejected")
	}
	writeFixture(t, filepath.Join(fixtures, "checksums.txt"),
		[]byte(fmt.Sprintf("%s  %s\n", wrong, archive)))

	out, err := runDispatcher(t, cache, fixtures, path, "MINGW64_NT-10.0-22631", "x86_64", version)
	if err == nil {
		t.Fatalf("a mismatched archive was accepted:\n%s", out)
	}
	if !strings.Contains(out, "checksum mismatch") {
		t.Errorf("the failure does not name the mismatch, so it reads as a missing release:\n%s", out)
	}
	// Nothing executed, and nothing left behind to be executed next time.
	if strings.Contains(out, archiveStubMarker) {
		t.Error("the binary ran despite the mismatch")
	}
	if _, err := os.Stat(cachedPath(cache, "windows_amd64", version, "facet.exe")); err == nil {
		t.Error("the rejected binary was cached, so the next run would trust it")
	}
	if got := installsUnder(t, cache); len(got) != 0 {
		t.Errorf("the rejected archive left an install behind: %v", got)
	}
	if got := stagingLeftovers(t, cache); len(got) != 0 {
		t.Errorf("the rejected archive left staging behind: %v", got)
	}
}

// A zip needs an unzip, and Git Bash — the shell this dispatcher runs in on
// Windows — does not ship one. Without a word about it the run ends at the
// closing `die`, whose advice is entirely about credentials and forks.
func TestAMissingUnzipIsSaidBeforeTheDownload(t *testing.T) {
	skipUnlessPOSIX(t)

	const version = "9.9.5"
	shims := archiveShims(t)
	fixtures := t.TempDir()
	cache := t.TempDir()

	// A PATH holding what the dispatcher needs and no unzip. Built by naming
	// the tools rather than by subtracting one, because a PATH that quietly
	// lost something else would fail this case for a reason it does not test.
	narrow := t.TempDir()
	for _, tool := range []string{
		"sh", "dirname", "basename", "pwd", "head", "ln", "mkdir", "rm",
		"grep", "awk", "sed", "cat", "chmod", "tar", "sha256sum", "cp", "env", "tr",
	} {
		src, err := exec.LookPath(tool)
		if err != nil {
			continue
		}
		if err := os.Symlink(src, filepath.Join(narrow, tool)); err != nil {
			t.Fatal(err)
		}
	}
	path := shims + ":" + narrow

	// The premise, measured twice: unzip is unreachable on this PATH and
	// reachable on a normal one. A probe that finds nothing because it is
	// broken looks exactly like a probe that finds nothing because it works.
	if _, err := exec.LookPath("unzip"); err != nil {
		t.Skip("no unzip on this machine either, so hiding it proves nothing")
	}
	if _, err := os.Stat(filepath.Join(narrow, "unzip")); err == nil {
		t.Fatal("unzip is on the narrow PATH, so this case is not about its absence")
	}
	// The dispatcher must still be able to run at all under this PATH.
	if _, err := os.Stat(filepath.Join(narrow, "sh")); err != nil {
		t.Fatalf("no sh on the narrow PATH; the dispatcher would fail before reaching the check: %v", err)
	}

	archive := "facet_" + version + "_windows_amd64.zip"
	body := makeZip(t, "facet.exe")
	writeFixture(t, filepath.Join(fixtures, archive), body)
	writeFixture(t, filepath.Join(fixtures, "checksums.txt"),
		[]byte(fmt.Sprintf("%s  %s\n", sha256Hex(body), archive)))

	out, err := runDispatcher(t, cache, fixtures, path, "MINGW64_NT-10.0-22631", "x86_64", version)
	if err == nil {
		t.Fatalf("the dispatcher claimed success with no way to open the archive:\n%s", out)
	}
	if !strings.Contains(out, "unzip") {
		t.Errorf("the failure never mentions unzip, so it reads as a credentials problem:\n%s", out)
	}
}

// A missing-asset error names the requested asset so that an absent file can
// be distinguished from a credential failure.
func TestAMissingAssetNamesWhatWasLookedFor(t *testing.T) {
	skipUnlessPOSIX(t)

	const version = "9.9.6"
	shims := archiveShims(t)
	path := archivePath(t, shims)
	fixtures := t.TempDir()
	cache := t.TempDir()

	// The premise: the fixture directory is empty, so every fetch fails.
	entries, err := os.ReadDir(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the fixture directory holds %d files; this case is about none", len(entries))
	}

	out, err := runDispatcher(t, cache, fixtures, path, "MINGW64_NT-10.0-22631", "x86_64", version)
	if err == nil {
		t.Fatalf("the dispatcher claimed success with nothing to download:\n%s", out)
	}
	want := "facet_" + version + "_windows_amd64.zip"
	if !strings.Contains(out, want) {
		t.Errorf("the failure never names %s, so it cannot be told from a credentials problem:\n%s", want, out)
	}
	// And the sources tried and every way out, because no release of the
	// version asked for may exist yet.
	for _, way := range []string{"Tried: facet-framework/core", "FACET_VERSION=", "FACET_BINARY=", "FACET_DEV_BUILD=1", "gh auth login"} {
		if !strings.Contains(out, way) {
			t.Errorf("the failure does not mention %q:\n%s", way, out)
		}
	}
}

// A synthetic tar.gz carries facet and companion executables. Multiple members
// distinguish whole-archive extraction from extraction of only the requested
// member; the companion is not a Core release component.
func makeTarGzWithSidecar(t *testing.T, facetMember string, sidecarMembers ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	add := func(name string, mode int64, body []byte) {
		t.Helper()
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	add("README.md", 0o644, []byte("readme\n"))
	add(facetMember, 0o755, archiveStub())
	for _, m := range sidecarMembers {
		add(m, 0o755, archiveStub())
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// An executable an archive carries beside facet must land beside it after an
// archive install, not just after a direct `go build` in a source checkout: a
// build that bundles plugin sidecars resolves each one beside os.Executable(),
// and a person who only ever ran this dispatcher never built anything
// themselves.
//
// Every companion does, not only the first: the fixture archive carries two,
// and the dispatcher names none of them — archive_companions in bin/facet takes
// every executable regular file at the archive root other than facet itself.
//
// Named mutation: delete the
// `tar -xzf "$_dl/$archive" -C "$_pkg" "$_side" 2>/dev/null || true`
// line from the archive branch in bin/facet — this test goes red: facet still
// resolves and runs (the archive's primary member is unaffected), but no
// companion binary appears in the cache beside it.
func TestTheSidecarLandsBesideFacetAfterAnArchiveInstall(t *testing.T) {
	skipUnlessPOSIX(t)

	const version = "9.9.4"
	shims := archiveShims(t)
	path := archivePath(t, shims)
	fixtures := t.TempDir()
	cache := t.TempDir()

	archive := "facet_" + version + "_linux_amd64.tar.gz"
	body := makeTarGzWithSidecar(t, "facet", "sidecar-a", "sidecar-b")
	writeFixture(t, filepath.Join(fixtures, archive), body)
	writeFixture(t, filepath.Join(fixtures, "checksums.txt"),
		[]byte(fmt.Sprintf("%s  %s\n", sha256Hex(body), archive)))

	out, err := runDispatcher(t, cache, fixtures, path, "Linux", "x86_64", version)
	if err != nil {
		t.Fatalf("the dispatcher could not resolve the Linux release: %v\n%s", err, out)
	}
	if !strings.Contains(out, archiveStubMarker) {
		t.Errorf("the binary from the archive did not run; got:\n%s", out)
	}

	facetPath := cachedPath(cache, "linux_amd64", version, "facet")
	if _, err := os.Stat(facetPath); err != nil {
		t.Fatalf("no facet in the cache after a Linux resolve: %v", err)
	}
	for _, name := range []string{"sidecar-a", "sidecar-b"} {
		sidecarPath := cachedPath(cache, "linux_amd64", version, name)
		info, err := os.Stat(sidecarPath)
		if err != nil {
			t.Fatalf("no %s sidecar beside facet in the cache after an archive install: %v", name, err)
		}
		if info.Mode()&0o111 == 0 {
			t.Errorf("%s was extracted without an executable bit", name)
		}
	}
}
