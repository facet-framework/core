package atlaslauncher

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/facet-framework/core/registry"
)

const updateVersion = "1.2.4"
const updateCore = "2.3.4"
const updateArchive = "atlas.tar.gz"
const notice = "FACET Atlas 1.2.4 is available. Run 'facet ui --update' to install it.\n"

type releaseFixture struct {
	t            *testing.T
	mu           sync.Mutex
	routes       map[string][]byte
	apiCalls     atomic.Int32
	archiveCalls atomic.Int32
	key          ed25519.PublicKey
	private      ed25519.PrivateKey
}

func fakeReleases(t *testing.T) *releaseFixture {
	t.Helper()
	key, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &releaseFixture{t: t, key: key, private: private, routes: make(map[string][]byte)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases" {
			f.apiCalls.Add(1)
		}
		if strings.HasSuffix(r.URL.Path, "/"+updateArchive) {
			f.archiveCalls.Add(1)
		}
		f.mu.Lock()
		data, ok := f.routes[r.URL.Path]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	t.Cleanup(server.Close)
	t.Setenv("FACET_ATLAS_API_URL", server.URL+"/releases?per_page=30")
	t.Setenv("FACET_ATLAS_BASE_URL", server.URL)
	t.Setenv("FACET_ATLAS_UPDATES", "")
	return f
}

func (f *releaseFixture) route(path, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[path] = []byte(body)
}

func (f *releaseFixture) unroute(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.routes, path)
}

func (f *releaseFixture) sign(data string, key ed25519.PrivateKey, id string) string {
	f.t.Helper()
	sig, err := json.Marshal(signature{id, "ed25519", base64.StdEncoding.EncodeToString(ed25519.Sign(key, []byte(data)))})
	if err != nil {
		f.t.Fatal(err)
	}
	return string(sig)
}

func updateManifest(version, platform, digest string, size int) string {
	return fmt.Sprintf(`{"manifest_version":1,"product":"atlas","version":%q,"core_version":"v%s","channel":"stable",
		"platforms":[{"platform":%q,"archive":%q,"sha256":%q,"size":%d}],"extra":"ignored"}`,
		version, updateCore, platform, updateArchive, digest, size)
}

func (f *releaseFixture) release(version, data string, body []byte) {
	prefix := "/v" + version + "/"
	f.route(prefix+manifestName, data)
	f.route(prefix+signatureName, f.sign(data, f.private, keyID(f.key)))
	f.route(prefix+updateArchive, string(body))
}

func (f *releaseFixture) list(tags ...string) {
	f.t.Helper()
	var releases []map[string]string
	for _, tag := range tags {
		releases = append(releases, map[string]string{"tag_name": "v" + tag})
	}
	data, err := json.Marshal(releases)
	if err != nil {
		f.t.Fatal(err)
	}
	f.route("/releases", string(data))
}

func setupUpdate(t *testing.T, pinnedScript string) (fixture, *releaseFixture) {
	t.Helper()
	f := newFixture(t, tarArchive(t, testEntry{"facet", pinnedScript, 0o755, 0}), "pinned.tar.gz")
	if err := f.target.install(context.Background(), f.url, f.launcher.coreVersion, 0); err != nil {
		t.Fatal(err)
	}
	releases := fakeReleases(t)
	f.launcher.keys = map[string]ed25519.PublicKey{keyID(releases.key): releases.key}
	return f, releases
}

func TestRejectsUntrustedAtlasUpdateMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, failure string
	}{
		{"missing", ""},
		{"malformed", "invalid Atlas signature envelope"},
		{"wrong-key", "invalid Atlas manifest signature"},
		{"unknown-key-id", "unknown Atlas signing key"},
		{"tampered", "invalid Atlas manifest signature"},
		{"wrong-version", "invalid Atlas manifest"},
		{"wrong-product", "invalid Atlas manifest"},
		{"wrong-core", ""},
		{"missing-platform", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, releases := setupUpdate(t, "#!/bin/sh\nexit 0\n")
			releases.list(updateVersion)
			data := updateManifest(updateVersion, "linux_amd64", strings.Repeat("a", 64), 42)
			switch tc.name {
			case "wrong-version":
				data = strings.Replace(data, updateVersion, "1.2.5", 1)
			case "wrong-product":
				data = strings.Replace(data, `"product":"atlas"`, `"product":"core"`, 1)
			case "wrong-core":
				f.launcher.projectPin = &facet.CorePin{Core: "2.3.5"}
			case "missing-platform":
				data = strings.Replace(data, "linux_amd64", "darwin_amd64", 1)
			}
			releases.release(updateVersion, data, []byte("unused"))
			prefix := "/v" + updateVersion + "/"
			switch tc.name {
			case "missing":
				releases.unroute(prefix + signatureName)
			case "malformed":
				releases.route(prefix+signatureName, "{")
			case "wrong-key", "unknown-key-id":
				key, wrong, err := ed25519.GenerateKey(nil)
				if err != nil {
					t.Fatal(err)
				}
				id := keyID(releases.key)
				if tc.name == "unknown-key-id" {
					id = keyID(key)
				}
				releases.route(prefix+signatureName, releases.sign(data, wrong, id))
			case "tampered":
				releases.route(prefix+manifestName, data+"\n")
			}
			out, _, err := f.run("", "--update")
			if tc.failure == "" {
				if err != nil || out != "No FACET Atlas update available.\n" {
					t.Fatalf("unexpected update result: %q, %v", out, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.failure) {
				t.Fatalf("expected %q, got %v", tc.failure, err)
			}
			if releases.apiCalls.Load() != 1 || releases.archiveCalls.Load() != 0 {
				t.Fatalf("release list/archive requests: %d/%d", releases.apiCalls.Load(), releases.archiveCalls.Load())
			}
			requireAbsent(t, filepath.Join(filepath.Dir(f.target.dir), updateVersion))
		})
	}
}

func TestRejectsAtlasArchiveWithWrongSizeOrDigest(t *testing.T) {
	for _, field := range []string{"size", "SHA-256"} {
		t.Run(field, func(t *testing.T) {
			f, releases := setupUpdate(t, "#!/bin/sh\nexit 0\n")
			body := []byte("update archive")
			size, digest := len(body), digestOf(body)
			if field == "size" {
				size++
			} else {
				digest = strings.Repeat("a", 64)
			}
			releases.list(updateVersion)
			releases.release(updateVersion, updateManifest(updateVersion, "linux_amd64", digest, size), body)
			if _, _, err := f.run("", "--update"); err == nil || !strings.Contains(err.Error(), field+" mismatch") {
				t.Fatalf("expected %s mismatch, got %v", field, err)
			}
			if releases.archiveCalls.Load() != 1 || f.target.state().Active != "" {
				t.Fatalf("unexpected state or archive request count: %+v, %d", f.target.state(), releases.archiveCalls.Load())
			}
			requireAbsent(t, filepath.Join(filepath.Dir(f.target.dir), updateVersion))
		})
	}
}

func TestAtlasReleaseOrderingAndFloor(t *testing.T) {
	f, releases := setupUpdate(t, "#!/bin/sh\nexit 0\n")
	for _, version := range []string{"1.2.2", "1.2.4-rc.2", "1.2.4-rc.10", "1.2.4", "1.2.5"} {
		body := []byte(version)
		releases.release(version, updateManifest(version, "linux_amd64", digestOf(body), len(body)), body)
	}
	for _, tc := range []struct{ list, want string }{
		{`[{"tag_name":"v1.2.2"}]`, ""},
		{`[{"tag_name":"v1.2.4-rc.2"},{"tag_name":"v1.2.4-rc.10","prerelease":true},{"tag_name":"v1.2.5","draft":true}]`, "1.2.4-rc.10"},
		{`[{"tag_name":"v1.2.6-rc.1"},{"tag_name":"v1.2.4"}]`, "1.2.4"},
		{`[{"tag_name":"v1.2.6-rc.1"},{"tag_name":"v1.2.2"}]`, ""},
	} {
		releases.route("/releases", tc.list)
		got, err := f.launcher.discover(context.Background(), f.launcher.Version())
		if err != nil || got.Version != tc.want {
			t.Fatalf("release list %s: got %q, %v, want %q", tc.list, got.Version, err, tc.want)
		}
	}
	releases.list("1.2.2")
	if out, _, err := f.run("", "--update"); err != nil || out != "No FACET Atlas update available.\n" || releases.archiveCalls.Load() != 0 {
		t.Fatalf("downgrade attempt: %q, %v, archives %d", out, err, releases.archiveCalls.Load())
	}
}

func TestAtlasUpdateActivationFallbackAndCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell executables")
	}
	f, releases := setupUpdate(t, "#!/bin/sh\nprintf embedded\n")
	parent := filepath.Dir(f.target.dir)
	for _, version := range []string{updateVersion, "1.2.5"} {
		body := tarArchive(t, testEntry{"facet", "#!/bin/sh\nprintf " + version + "\n", 0o755, 0})
		releases.release(version, updateManifest(version, "linux_amd64", digestOf(body), len(body)), body)
		releases.list(version)
		out, _, err := f.run("", "--update")
		if err != nil || out != fmt.Sprintf("Installed FACET Atlas %s. Run 'facet ui' to start it.\n", version) {
			t.Fatalf("update did not install without launching: %q, %v", out, err)
		}
		dir := filepath.Join(parent, version)
		for _, file := range []string{manifestName, signatureName, markerName} {
			if mustRead(t, filepath.Join(dir, file)) == "" {
				t.Fatalf("empty %s", file)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(parent, updateVersion, markerName)); err != nil {
		t.Fatalf("previous install removed before first successful launch: %v", err)
	}
	out, _, err := f.run("", "--no-update-check")
	if err != nil || out != "1.2.5" {
		t.Fatalf("active Atlas: %q, %v", out, err)
	}
	requireAbsent(t, filepath.Join(parent, updateVersion))
	if !f.target.installed() {
		t.Fatal("embedded fallback was removed")
	}
	dir := filepath.Join(parent, "1.2.5")
	sig := mustRead(t, filepath.Join(dir, signatureName))
	writeFixtureFile(t, filepath.Join(dir, signatureName), "broken")
	out, _, err = f.run("", "--no-update-check")
	if err != nil || out != "embedded" {
		t.Fatalf("invalid signature fallback: %q, %v", out, err)
	}
	writeFixtureFile(t, filepath.Join(dir, signatureName), sig)
	if err := os.Remove(filepath.Join(dir, markerName)); err != nil {
		t.Fatal(err)
	}
	out, _, err = f.run("", "--no-update-check")
	if err != nil || out != "embedded" {
		t.Fatalf("incomplete installation fallback: %q, %v", out, err)
	}
}

func writeFixtureFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

type signalWriter struct {
	io.Writer
	once    sync.Once
	written chan struct{}
}

func (w *signalWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	w.once.Do(func() { close(w.written) })
	return n, err
}

func TestAtlasBackgroundCheckNotifiesAndCaches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell executables")
	}
	f, releases := setupUpdate(t, "#!/bin/sh\nprintf started\nread finish\n")
	f.launcher.isTerminal = func() bool { return true }
	releases.list(updateVersion)
	body := []byte("archive")
	releases.release(updateVersion, updateManifest(updateVersion, "linux_amd64", digestOf(body), len(body)), body)
	for i := 0; i < 2; i++ {
		stdin, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		started := &signalWriter{Writer: &stdout, written: make(chan struct{})}
		notified := &signalWriter{Writer: &stderr, written: make(chan struct{})}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		go func() {
			defer writer.Close()
			for _, signal := range []chan struct{}{started.written, notified.written} {
				select {
				case <-signal:
				case <-ctx.Done():
					return
				}
			}
			fmt.Fprintln(writer, "exit")
		}()
		err = f.launcher.Run(ctx, nil, stdin, started, notified)
		cancel()
		stdin.Close()
		if err != nil || stdout.String() != "started" || stderr.String() != notice || releases.apiCalls.Load() != 1 {
			t.Fatalf("run %d: stdout %q, stderr %q, err %v, requests %d", i, &stdout, &stderr, err, releases.apiCalls.Load())
		}
	}
	out, _, err := f.run("", "--atlas-version")
	if err != nil || !strings.Contains(out, "Available: "+updateVersion+"\n") {
		t.Fatalf("cached available version: %q, %v", out, err)
	}
	if _, _, err := f.run("", "--update"); err == nil || releases.apiCalls.Load() != 2 || releases.archiveCalls.Load() != 1 {
		t.Fatalf("explicit update did not bypass the window: %v, list/archive %d/%d", err, releases.apiCalls.Load(), releases.archiveCalls.Load())
	}
}

func TestAtlasBackgroundCheckCanBeDisabled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell executables")
	}
	for _, mode := range []string{"flag", "env", "empty-keys"} {
		t.Run(mode, func(t *testing.T) {
			f, releases := setupUpdate(t, "#!/bin/sh\nprintf started\n")
			f.launcher.isTerminal = func() bool { return true }
			releases.list(updateVersion)
			var args []string
			switch mode {
			case "flag":
				args = []string{"--no-update-check"}
			case "env":
				t.Setenv("FACET_ATLAS_UPDATES", "off")
			case "empty-keys":
				f.launcher.keys = nil
			}
			out, stderr, err := f.run("", args...)
			if err != nil || out != "started" || stderr != "" || releases.apiCalls.Load() != 0 {
				t.Fatalf("disabled check: %q / %q, %v, requests %d", out, stderr, err, releases.apiCalls.Load())
			}
			requireAbsent(t, filepath.Join(filepath.Dir(f.target.dir), updateStateName))
		})
	}
}

func TestAtlasStartsWhileReleaseCheckIsOfflineOrTimesOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell executables")
	}
	for _, mode := range []string{"offline", "timeout", "nonterminal"} {
		t.Run(mode, func(t *testing.T) {
			f, releases := setupUpdate(t, "#!/bin/sh\nprintf started\nread finish\n")
			f.launcher.isTerminal = func() bool { return mode != "nonterminal" }
			started := &signalWriter{Writer: &bytes.Buffer{}, written: make(chan struct{})}
			var stderr bytes.Buffer
			checked := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Observe the child output before the network check can complete.
				select {
				case <-started.written:
				case <-r.Context().Done():
					return
				}
				switch mode {
				case "timeout":
					<-r.Context().Done()
				case "offline":
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
					} else {
						conn.Close()
					}
				case "nonterminal":
					body := []byte("archive")
					releases.release(updateVersion, updateManifest(updateVersion, "linux_amd64", digestOf(body), len(body)), body)
					fmt.Fprintf(w, `[{"tag_name":"v%s"}]`, updateVersion)
				}
				close(checked)
			}))
			defer server.Close()
			t.Setenv("FACET_ATLAS_API_URL", server.URL)
			stdin, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			released := make(chan bool, 1)
			go func() {
				defer writer.Close()
				select {
				case <-checked:
					if mode == "nonterminal" {
						ticker := time.NewTicker(10 * time.Millisecond)
						defer ticker.Stop()
						for f.target.state().Available != updateVersion {
							select {
							case <-ticker.C:
							case <-ctx.Done():
								released <- false
								return
							}
						}
					}
					_, err := fmt.Fprintln(writer, "exit")
					released <- err == nil
				case <-ctx.Done():
					released <- false
				}
			}()
			err = f.launcher.Run(ctx, nil, stdin, started, &stderr)
			cancel()
			if err != nil || !<-released || stderr.String() != "" || f.target.state().Checked.IsZero() {
				t.Fatalf("launch during %s: %v, stderr %q, state %+v", mode, err, &stderr, f.target.state())
			}
		})
	}
}

func TestFacetBinaryInstallsAndLaunchesSignedAtlas(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell executable fixture")
	}
	releases := fakeReleases(t)
	temp := t.TempDir()
	t.Setenv("FACET_ATLAS_CACHE", filepath.Join(temp, "cache"))
	t.Setenv("FACET_PRODUCTS_DIR", filepath.Join(temp, "products"))
	root := filepath.Join(temp, "project")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, facet.ConfigPath(root), "requires: {core: "+updateCore+"}\n")
	// Inject the fixture key into the real launcher's constructor in this build.
	// The production key file is shared by neither test setup nor the server.
	launcherSource, err := filepath.Abs("launcher.go")
	if err != nil {
		t.Fatal(err)
	}
	source := mustRead(t, launcherSource)
	const original = "keys: trustedKeys"
	if strings.Count(source, original) != 1 {
		t.Fatal("expected one trusted-key assignment in the launcher constructor")
	}
	injected := fmt.Sprintf("keys: map[string]ed25519.PublicKey{%q: %#v}", keyID(releases.key), []byte(releases.key))
	replacement := filepath.Join(temp, "launcher.go")
	writeFixtureFile(t, replacement, strings.Replace(source, original, injected, 1))
	overlay := filepath.Join(temp, "overlay.json")
	writeFixtureFile(t, overlay, fmt.Sprintf(`{"Replace":{%q:%q}}`, launcherSource, replacement))
	binary := filepath.Join(temp, "facet")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-overlay", overlay, "-o", binary, coreCommand(t))
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build facet: %v\n%s", err, out)
	}
	body := tarArchive(t, testEntry{"facet", "#!/bin/sh\nprintf '%s\\n' \"$@\"\n", 0o755, 0})
	releases.list(updateVersion)
	releases.release(updateVersion, updateManifest(updateVersion, runtime.GOOS+"_"+runtime.GOARCH, digestOf(body), len(body)), body)
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, append([]string{"ui"}, args...)...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("facet %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	if out := run("--update"); out != "Installed FACET Atlas 1.2.4. Run 'facet ui' to start it.\n" {
		t.Fatalf("update output: %q", out)
	}
	if out := run("--atlas-version"); !strings.Contains(out, "Active: 1.2.4 (Core "+updateCore+")\n") {
		t.Fatalf("version output: %q", out)
	}
	if out := run("--no-update-check", "--no-open"); out != "ui\n--no-open\n" {
		t.Fatalf("Atlas process output: %q", out)
	}
	if releases.apiCalls.Load() != 1 || releases.archiveCalls.Load() != 1 {
		t.Fatalf("network requests: list %d, archive %d", releases.apiCalls.Load(), releases.archiveCalls.Load())
	}
}

func TestAtlasDiscoveryPassesOverReleasesWithoutSignature(t *testing.T) {
	f, releases := setupUpdate(t, "#!/bin/sh\nexit 0\n")
	body := []byte("1.2.4")
	releases.release("1.2.4", updateManifest("1.2.4", "linux_amd64", digestOf(body), len(body)), body)
	unsignedBody := []byte("1.2.5")
	releases.route("/v1.2.5/"+manifestName, updateManifest("1.2.5", "linux_amd64", digestOf(unsignedBody), len(unsignedBody)))
	releases.list("1.2.5", "1.2.4")
	got, err := f.launcher.discover(context.Background(), f.launcher.Version())
	if err != nil || got.Version != "1.2.4" {
		t.Fatalf("an unsigned newer release must be passed over: got %q, %v", got.Version, err)
	}
	releases.unroute("/v1.2.5/" + manifestName)
	got, err = f.launcher.discover(context.Background(), f.launcher.Version())
	if err != nil || got.Version != "1.2.4" {
		t.Fatalf("a release without manifest must be passed over: got %q, %v", got.Version, err)
	}
	releases.route("/v1.2.5/"+manifestName, "{")
	releases.route("/v1.2.5/"+signatureName, "{")
	if _, err := f.launcher.discover(context.Background(), f.launcher.Version()); err == nil {
		t.Fatal("a malformed signature must stay an error, not a skip")
	}
}

// coreCommand returns the import path of the facet command: the main package
// under cmd/ that imports the module's cli package with the fewest
// dependencies.
func coreCommand(t *testing.T) string {
	t.Helper()
	module, err := exec.Command("go", "list", "-m").Output()
	if err != nil {
		t.Fatalf("go list -m: %v", err)
	}
	prefix := strings.TrimSpace(string(module)) + "/"
	list := exec.Command("go", "list", "-f", "{{.ImportPath}} {{.Name}} {{len .Deps}} {{join .Imports \" \"}}", "./cmd/...")
	list.Dir = "../.."
	out, err := list.Output()
	if err != nil {
		t.Fatalf("go list ./cmd/...: %v", err)
	}
	best, fewest := "", -1
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[1] != "main" {
			continue
		}
		var deps int
		if _, err := fmt.Sscan(fields[2], &deps); err != nil {
			t.Fatalf("dependency count in %q: %v", line, err)
		}
		imports := false
		for _, imported := range fields[3:] {
			imports = imports || (strings.HasPrefix(imported, prefix) && strings.HasSuffix(imported, "/cli"))
		}
		if imports && (fewest < 0 || deps < fewest) {
			best, fewest = fields[0], deps
		}
	}
	if best == "" {
		t.Fatal("no main package under cmd/ imports the module's CLI")
	}
	return best
}
