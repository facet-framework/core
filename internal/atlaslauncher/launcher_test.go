package atlaslauncher

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/facet-framework/core/registry"
)

func TestEmbeddedPinDescribesAllReleaseArchives(t *testing.T) {
	p := embeddedPin
	if len(p.Platforms) != 6 {
		t.Fatalf("expected one version and six platforms, got %+v", p)
	}
	for _, version := range []string{p.Version, p.Core} {
		root := t.TempDir()
		if err := os.WriteFile(facet.ConfigPath(root), fmt.Appendf(nil, "requires: {core: %q}\n", version), 0o600); err != nil {
			t.Fatal(err)
		}
		if pin, err := facet.ReadCorePin(root); err != nil || pin == nil || pin.Core != version {
			t.Fatalf("expected canonical version, got %q (%+v, %v)", version, pin, err)
		}
	}
	for _, osName := range []string{"darwin", "linux", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			platform := osName + "/" + arch
			a := p.Platforms[platform]
			ext := "tar.gz"
			if osName == "windows" {
				ext = "zip"
			}
			want := fmt.Sprintf("facet-atlas_%s_%s_%s.%s", p.Version, osName, arch, ext)
			if digest, err := hex.DecodeString(a.SHA256); err != nil || len(digest) != 32 || a.Size <= 0 || a.Archive != want {
				t.Errorf("invalid pin for %s: %+v (want %s)", platform, a, want)
			}
		}
	}
}

type testEntry struct {
	name    string
	data    string
	mode    int64
	tarType byte
}

func tarArchive(t *testing.T, entries ...testEntry) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tarball := tar.NewWriter(gz)
	for _, e := range entries {
		header := &tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.data)), Typeflag: e.tarType}
		if e.tarType == tar.TypeLink || e.tarType == tar.TypeSymlink {
			header.Linkname = "facet"
		}
		if err := tarball.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tarball.Write([]byte(e.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarball.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func zipArchive(t *testing.T, entries ...testEntry) []byte {
	t.Helper()
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for _, e := range entries {
		header := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		header.SetMode(os.FileMode(e.mode))
		file, err := z.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(e.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

var shellFixture = testEntry{
	name: "facet",
	data: "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$ATLAS_TEST_ARGS\"\n",
	mode: 0o755,
}

type fixture struct {
	launcher Launcher
	server   *httptest.Server
	requests *atomic.Int32
	target   installation
	url      string
}

func newFixture(t *testing.T, body []byte, archive string) fixture {
	t.Helper()
	const version = "1.2.3-test"
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v"+version+"/"+archive || r.UserAgent() != "facet/test-core" {
			http.Error(w, fmt.Sprintf("unexpected request: %s %s (%s)", r.Method, r.URL.Path, r.UserAgent()), http.StatusBadRequest)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	t.Setenv("FACET_ATLAS_CACHE", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("FACET_ATLAS_BASE_URL", server.URL)
	platform := "linux/amd64"
	if strings.HasSuffix(archive, ".zip") {
		platform = "windows/amd64"
	}
	l := Launcher{
		pin: pin{Version: version, Core: "1.2.4", Platforms: map[string]asset{
			platform: {Archive: archive, SHA256: digestOf(body), Size: int64(len(body))},
		}},
		coreVersion: "test-core",
		platform:    platform,
		isTerminal:  func() bool { return true },
	}
	target, err := l.target()
	if err != nil {
		t.Fatal(err)
	}
	return fixture{l, server, &requests, target, server.URL + "/v" + version + "/" + archive}
}

func (f fixture) run(stdin string, args ...string) (stdout, stderr string, err error) {
	var out, diag bytes.Buffer
	err = f.launcher.Run(context.Background(), args, strings.NewReader(stdin), &out, &diag)
	return out.String(), diag.String(), err
}

func digestOf(body []byte) string { return fmt.Sprintf("%x", sha256.Sum256(body)) }

func mustRead(t *testing.T, filename string) string {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func requireAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be absent, stat error: %v", path, err)
	}
}

func TestApprovedArchiveStartsAtlasAndRunsAgainOffline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executes a Unix shell script")
	}
	archive := tarArchive(t, shellFixture, testEntry{name: "dmn", data: shellFixture.data, mode: 0o755})
	f := newFixture(t, archive, "atlas.tar.gz")
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("ATLAS_TEST_ARGS", argsFile)
	requireAbsent(t, f.target.dir)

	_, stderr, err := f.run("YeS\n", "--no-open", "--port", "1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FACET Atlas " + f.launcher.Version(), f.target.Archive,
		fmt.Sprintf("%.1f MB", float64(len(archive))/1e6), f.url, f.target.dir, "AGPL-3.0", "Download and start? [y/N]"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("prompt lacks %q: %s", want, stderr)
		}
	}
	if f.requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", f.requests.Load())
	}
	if got := mustRead(t, filepath.Join(f.target.dir, markerName)); got != digestOf(archive) {
		t.Fatalf("cache marker = %q", got)
	}
	if info, err := os.Stat(filepath.Join(f.target.dir, "dmn")); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("sidecar was not extracted beside facet with its executable bits: %v (%v)", info, err)
	}
	if got := mustRead(t, argsFile); got != "ui\n--no-open\n--port\n1\n" {
		t.Fatalf("child argv = %q", got)
	}

	f.server.Close()
	f.launcher.isTerminal = func() bool { return false }
	if err := os.Remove(argsFile); err != nil {
		t.Fatal(err)
	}
	_, stderr, err = f.run("", "--no-open", "--root", "--yes", "--registry", "other", "--", "--remove")
	if err != nil || stderr != "" {
		t.Fatalf("offline launch: %v, stderr %q", err, stderr)
	}
	if got := mustRead(t, argsFile); got != "ui\n--no-open\n--root\n--yes\n--registry\nother\n--\n--remove\n" {
		t.Fatalf("offline child argv = %q", got)
	}
}

func TestLauncherFlagsMustPrecedeAtlasFlags(t *testing.T) {
	f := newFixture(t, tarArchive(t, shellFixture), "atlas.tar.gz")
	for _, flag := range []string{"--yes", "--atlas-version", "--remove", "--update", "--no-update-check"} {
		t.Run(flag, func(t *testing.T) {
			_, _, err := f.run("", "--no-open", flag, "--port", "1")
			var exit interface{ ExitCode() int }
			want := "launcher flag " + flag + " must come before Atlas flags: facet ui " + flag + " --no-open --port 1"
			if !errors.As(err, &exit) || exit.ExitCode() != 2 || err.Error() != want || f.requests.Load() != 0 {
				t.Fatalf("misplaced flag: %v, requests %d", err, f.requests.Load())
			}
		})
	}
}

func TestDecliningThePromptMakesNoRequest(t *testing.T) {
	f := newFixture(t, tarArchive(t, shellFixture), "atlas.tar.gz")
	_, stderr, err := f.run("n\n")
	if err == nil || !strings.Contains(err.Error(), "declined") || !strings.Contains(stderr, "Download and start? [y/N]") || f.requests.Load() != 0 {
		t.Fatalf("decline got err %v, stderr %q, requests %d", err, stderr, f.requests.Load())
	}
	requireAbsent(t, f.target.dir)
}

func TestNonTerminalNeedsExplicitConsent(t *testing.T) {
	f := newFixture(t, tarArchive(t, shellFixture), "atlas.tar.gz")
	f.launcher.isTerminal = func() bool { return false }
	_, stderr, err := f.run("yes\n")
	if err == nil || !strings.Contains(err.Error(), "facet ui --yes") || stderr != "" || f.requests.Load() != 0 {
		t.Fatalf("no terminal got err %v, stderr %q, requests %d", err, stderr, f.requests.Load())
	}
	requireAbsent(t, f.target.dir)
}

func TestDigestMismatchLeavesNoInstallation(t *testing.T) {
	body := tarArchive(t, shellFixture)
	f := newFixture(t, body, "atlas.tar.gz")
	// Pin a different archive and ensure the response would otherwise be runnable.
	expected := f.launcher.pin.Platforms[f.launcher.platform]
	expected.SHA256 = digestOf(tarArchive(t, testEntry{name: "facet", data: "#!/bin/sh\nexit 7\n", mode: 0o755}))
	f.launcher.pin.Platforms[f.launcher.platform] = expected
	_, stderr, err := f.run("", "--yes")
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch: expected "+expected.SHA256+", got "+digestOf(body)) ||
		f.requests.Load() != 1 || stderr != "" {
		t.Fatalf("mismatch got err %v, stderr %q, requests %d", err, stderr, f.requests.Load())
	}
	requireAbsent(t, filepath.Join(f.target.dir, markerName))
	requireAbsent(t, filepath.Join(f.target.dir, f.target.binary))
	entries, err := os.ReadDir(filepath.Dir(f.target.dir))
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(f.target.lockPath()) {
		t.Fatalf("temporary download was left behind: %v, %v", entries, err)
	}
}

func TestArchiveTraversalIsRefusedBeforePublishing(t *testing.T) {
	for _, format := range []string{"tar.gz", "zip"} {
		t.Run(format, func(t *testing.T) {
			archive := tarArchive
			if format == "zip" {
				archive = zipArchive
			}
			f := newFixture(t, archive(t, shellFixture, testEntry{name: "../escaped", data: "outside", mode: 0o644}), "atlas."+format)
			_, _, err := f.run("", "--yes")
			if err == nil || !strings.Contains(err.Error(), "extract Atlas archive") || f.requests.Load() != 1 {
				t.Fatalf("traversal got err %v, requests %d", err, f.requests.Load())
			}
			requireAbsent(t, f.target.dir)
			requireAbsent(t, filepath.Join(filepath.Dir(f.target.dir), "escaped"))
		})
	}
}

func TestArchiveLinksAndAbsoluteNamesAreRefusedBeforePublishing(t *testing.T) {
	for _, tc := range []struct {
		name, format string
		archive      func(*testing.T, ...testEntry) []byte
		entry        testEntry
	}{
		{"tar-symlink", "tar.gz", tarArchive, testEntry{name: "symlink", tarType: tar.TypeSymlink}},
		{"tar-hardlink", "tar.gz", tarArchive, testEntry{name: "hardlink", tarType: tar.TypeLink}},
		{"absolute-tar-path", "tar.gz", tarArchive, testEntry{name: "/absolute", data: "outside", mode: 0o644}},
		{"zip-symlink", "zip", zipArchive, testEntry{name: "symlink", data: "facet", mode: int64(os.ModeSymlink | 0o777)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.archive(t, shellFixture, tc.entry), "atlas."+tc.format)
			_, _, err := f.run("", "--yes")
			if err == nil || !strings.Contains(err.Error(), "extract Atlas archive") || f.requests.Load() != 1 {
				t.Fatalf("unsafe entry: %v, requests %d", err, f.requests.Load())
			}
			requireAbsent(t, f.target.dir)
		})
	}
}

func TestDownloadAbortsWhenResponseBodyStopsMakingProgress(t *testing.T) {
	f := newFixture(t, tarArchive(t, shellFixture), "atlas.tar.gz")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("first bytes"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	t.Setenv("FACET_ATLAS_BASE_URL", server.URL)
	f.launcher.idleTimeout = 50 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := f.launcher.Run(ctx, []string{"--yes"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, errIdleDownload) || ctx.Err() != nil {
		t.Fatalf("stalled body: %v (outer context: %v)", err, ctx.Err())
	}
	requireAbsent(t, f.target.dir)
}

func TestVersionAndRemoveUseTheLocalZipCache(t *testing.T) {
	f := newFixture(t, zipArchive(t, testEntry{name: "facet.exe", data: "fixture executable", mode: 0o755},
		testEntry{name: "dmn.exe", data: "fixture sidecar", mode: 0o755},
		testEntry{name: "THIRD_PARTY_LICENSES/example.txt", data: "license", mode: 0o644}), "atlas.zip")
	release := "FACET Atlas " + f.launcher.Version() + " (Core " + f.launcher.AtlasCoreVersion() + ")"
	versions := fmt.Sprintf("Embedded: %s\nActive: %s (Core %s)\n", f.launcher.Version(), f.launcher.Version(), f.launcher.AtlasCoreVersion())
	out, _, err := f.run("", "--atlas-version")
	if err != nil || out != release+" is not installed\n"+versions || f.requests.Load() != 0 {
		t.Fatalf("uncached version: %q, %v, requests %d", out, err, f.requests.Load())
	}
	if err := f.target.install(context.Background(), f.url, f.launcher.coreVersion, 0); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(f.target.dir, "dmn.exe")); got != "fixture sidecar" {
		t.Fatalf("sidecar = %q", got)
	}
	if got := mustRead(t, filepath.Join(f.target.dir, "THIRD_PARTY_LICENSES", "example.txt")); got != "license" {
		t.Fatalf("license = %q", got)
	}
	out, _, err = f.run("", "--atlas-version")
	if err != nil || out != release+" is installed at "+f.target.dir+"\n"+versions || f.requests.Load() != 1 {
		t.Fatalf("cached version: %q, %v, requests %d", out, err, f.requests.Load())
	}
	if out, _, err := f.run("", "--remove"); err != nil || out != "Removed FACET Atlas "+f.launcher.Version()+" cache at "+f.target.dir+"\n" {
		t.Fatalf("remove: %q, %v", out, err)
	}
	requireAbsent(t, f.target.dir)
	requireAbsent(t, f.target.lockPath())
	if out, _, err := f.run("", "--remove"); err != nil || out != "FACET Atlas "+f.launcher.Version()+" is not installed\n" {
		t.Fatalf("remove empty cache: %q, %v", out, err)
	}
	requireAbsent(t, f.target.lockPath())
	if f.requests.Load() != 1 {
		t.Fatalf("remove made an HTTP request: total = %d", f.requests.Load())
	}
}

func TestInstallSweepsOnlyStaleAtlasTemporaryEntries(t *testing.T) {
	f := newFixture(t, zipArchive(t, testEntry{name: "facet.exe", data: "fixture executable", mode: 0o755}), "atlas.zip")
	parent := filepath.Dir(f.target.dir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	download, extract := filepath.Join(parent, ".atlas-download-old"), filepath.Join(parent, ".atlas-extract-old")
	fresh, unrelated := filepath.Join(parent, ".atlas-download-fresh"), filepath.Join(parent, "unrelated")
	if err := os.Mkdir(extract, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{download, fresh, unrelated} {
		if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	for _, path := range []string{download, extract, unrelated} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.target.install(context.Background(), f.url, f.launcher.coreVersion, 0); err != nil {
		t.Fatal(err)
	}
	requireAbsent(t, download)
	requireAbsent(t, extract)
	for _, path := range []string{fresh, unrelated} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConcurrentInstallersUseOneVerifiedCache(t *testing.T) {
	f := newFixture(t, zipArchive(t, testEntry{name: "facet.exe", data: "fixture executable", mode: 0o755}), "atlas.zip")
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			results <- f.target.install(context.Background(), f.url, f.launcher.coreVersion, 0)
		}()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if !f.target.installed() || f.requests.Load() != 1 {
		t.Fatalf("installed = %v with %d requests", f.target.installed(), f.requests.Load())
	}
}

func TestChildStreamsAndExitCodeArePreserved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executes a Unix shell script")
	}
	f := newFixture(t, tarArchive(t, testEntry{name: "facet", data: "#!/bin/sh\nread -r line\nprintf '%s\\n' \"$line\"\necho diagnostic >&2\nexit 23\n", mode: 0o755}), "atlas.tar.gz")
	out, stderr, err := f.run("yes\ninput for Atlas\n")
	exit, ok := err.(interface{ ExitCode() int })
	if !ok || exit.ExitCode() != 23 || out != "input for Atlas\n" || !strings.HasSuffix(stderr, "diagnostic\n") {
		t.Fatalf("child got stdout %q, stderr %q, err %v", out, stderr, err)
	}
}

func TestUnsupportedPlatformNamesTheSupportedPlatforms(t *testing.T) {
	l := New("test")
	l.platform = "plan9/arm"
	_, _, err := l.Status()
	if err == nil || !strings.Contains(err.Error(), l.platform) {
		t.Fatalf("unsupported platform: %v", err)
	}
	for platform := range l.pin.Platforms {
		if !strings.Contains(err.Error(), platform) {
			t.Errorf("unsupported platform error lacks %s: %v", platform, err)
		}
	}
}
