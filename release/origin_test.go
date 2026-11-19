package release

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestClassifyInstall(t *testing.T) {
	type want struct{ origin, distribution, version, note string }
	for _, tc := range []struct {
		name     string
		dir      string // relative to the temp root
		manifest string // empty: no install.json is written
		unread   bool   // install.json exists but is a directory
		want     want
	}{
		{name: "dispatcher install", dir: "dist/core/public/linux-amd64/0.1.0", manifest: `{"product":"core","distribution":"public","platform":"linux-amd64","version":"0.1.0"}`, want: want{origin: OriginFacetCache, distribution: "public", version: "0.1.0"}},
		{name: "npm package", dir: "proj/node_modules/@facet-tools/linux-x64/bin", want: want{origin: OriginNPM}},
		{name: "npm wins over install.json", dir: "proj/node_modules/x/bin", manifest: `{"product":"core","version":"0.1.0"}`, want: want{origin: OriginNPM}},
		{name: "plain directory", dir: "usr/local/bin", want: want{origin: OriginUnmanaged}},
		{name: "element merely containing node_modules", dir: "my_node_modules/bin", want: want{origin: OriginUnmanaged}},
		{name: "invalid install.json", dir: "broken", manifest: `{"product":`, want: want{origin: OriginUnmanaged, note: "not valid JSON"}},
		{name: "other product", dir: "atlas", manifest: `{"product":"atlas","version":"1.0.0"}`, want: want{origin: OriginUnmanaged, note: `names product "atlas"`}},
		{name: "unreadable install.json", dir: "unreadable", unread: true, want: want{origin: OriginUnmanaged, note: "cannot be read"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), filepath.FromSlash(tc.dir))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			exe := filepath.Join(dir, "facet")
			if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
				t.Fatal(err)
			}
			manifest := filepath.Join(dir, InstallManifest)
			switch {
			case tc.unread:
				if err := os.Mkdir(manifest, 0o755); err != nil {
					t.Fatal(err)
				}
			case tc.manifest != "":
				if err := os.WriteFile(manifest, []byte(tc.manifest), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			// The premise of every case: install.json is present exactly when the case says so.
			if _, err := os.Stat(manifest); (err == nil) != (tc.manifest != "" || tc.unread) {
				t.Fatalf("fixture premise broken: stat %s = %v", manifest, err)
			}
			got := ClassifyInstall(exe)
			if got.Origin != tc.want.origin || got.Path != exe || got.Distribution != tc.want.distribution || got.Version != tc.want.version {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			if tc.want.note == "" && got.Note != "" || !strings.Contains(got.Note, tc.want.note) {
				t.Fatalf("note %q, want it to contain %q", got.Note, tc.want.note)
			}
		})
	}
}

func TestClassifyInstallReadsWindowsSeparators(t *testing.T) {
	if got := ClassifyInstall(`C:\proj\node_modules\@facet-tools\win32-x64\bin\facet.exe`); got.Origin != OriginNPM {
		t.Fatalf("%+v", got)
	}
}

func TestCurrentInstallResolvesTheRunningExecutable(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip("os.Executable is unavailable on " + runtime.GOOS)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	if got := CurrentInstall(); got.Path != resolved || got.Origin == "" {
		t.Fatalf("%+v, want path %s", got, resolved)
	}
}

// A link on PATH that points into node_modules is classified by its target.
func TestASymlinkIsClassifiedByWhatItPointsTo(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "node_modules", "x", "bin", "facet")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "bin", "facet")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks are unavailable here: " + err.Error())
	}
	// Premise: the link's own path says nothing about npm.
	if got := ClassifyInstall(link); got.Origin != OriginUnmanaged {
		t.Fatalf("premise: the unresolved link already reads as %q", got.Origin)
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := installAt(link); got.Origin != OriginNPM || got.Path != resolved {
		t.Fatalf("%+v, want npm at %s", got, resolved)
	}
}
