package facet_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/facet-framework/core/registry"
)

// stageFor builds a verified package the way runPluginAdd's own pipeline
// would — a folder that already passed VerifyPackage — so Install is tested
// against the same shape it is always called with, not a hand-built shortcut.
func stageFor(t *testing.T, id string) (staged string, manifest facet.PackageManifest) {
	t.Helper()
	dir := buildPluginDir(t, validManifest(id), pluginFile{name: "plugin.js", body: []byte("x")})
	m, err := facet.VerifyPackage(dir, facet.VerifyOptions{})
	if err != nil {
		t.Fatalf("fixture precondition failed: the staged package does not verify: %v", err)
	}
	return dir, m
}

func TestInstallRefusesADuplicateIdWithoutReplace(t *testing.T) {
	pluginsDir := t.TempDir()
	staged, manifest := stageFor(t, "acme-notes")
	if err := facet.Install(pluginsDir, "acme-notes", staged, manifest, nil, facet.InstallOptions{}); err != nil {
		t.Fatalf("the first install was refused: %v", err)
	}

	snapshot := func() map[string]string {
		t.Helper()
		tree := map[string]string{}
		err := filepath.WalkDir(filepath.Join(pluginsDir, "acme-notes"), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(pluginsDir, path)
			if err != nil {
				return err
			}
			if entry.IsDir() {
				tree[rel] = "directory"
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			tree[rel] = "file:" + string(body)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return tree
	}
	before := snapshot()
	staged2 := buildPluginDir(t, validManifest("acme-notes"), pluginFile{name: "plugin.js", body: []byte("different replacement bytes")})
	manifest2, verifyErr := facet.VerifyPackage(staged2, facet.VerifyOptions{})
	if verifyErr != nil {
		t.Fatal(verifyErr)
	}
	err := facet.Install(pluginsDir, "acme-notes", staged2, manifest2, &manifest, facet.InstallOptions{})

	// A duplicate ID without Replace must be refused and leave the installed
	// bytes and directory entries unchanged.
	if !errors.Is(err, facet.ErrPluginAlreadyInstalled) {
		t.Fatalf("a second install of the same id without --replace was not refused: %v", err)
	}
	if !reflect.DeepEqual(before, snapshot()) {
		t.Fatal("a refused duplicate changed installed bytes or entries")
	}
}

func TestReplaceKeepsGrantsWhenNothingNewIsAsked(t *testing.T) {
	pluginsDir := t.TempDir()
	staged, manifest := stageFor(t, "acme-notes")
	if err := facet.Install(pluginsDir, "acme-notes", staged, manifest, nil, facet.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	grantsPath := filepath.Join(pluginsDir, "acme-notes", "grants.json")
	if err := os.WriteFile(grantsPath, []byte(`{"tools":["notes.read"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Replace with an IDENTICAL manifest — no new tool, env, or ai_tool.
	staged2, manifest2 := stageFor(t, "acme-notes")
	if err := facet.Install(pluginsDir, "acme-notes", staged2, manifest2, &manifest, facet.InstallOptions{Replace: true}); err != nil {
		t.Fatalf("a same-rights replace was refused: %v", err)
	}

	// Mutation: drop the `keep := previous != nil && !previous.AddedRights(...)`
	// guard in Install (always keep, or never keep) and one of this test and
	// its sibling below goes green for the wrong reason instead of red.
	got, err := os.ReadFile(grantsPath)
	if err != nil {
		t.Fatalf("grants.json did not survive a replace that added no rights: %v", err)
	}
	if string(got) != `{"tools":["notes.read"]}` {
		t.Fatalf("grants.json was rewritten even though nothing new was asked: %s", got)
	}
}

func TestReplaceDropsGrantsWhenANewToolIsAdded(t *testing.T) {
	pluginsDir := t.TempDir()
	staged, manifest := stageFor(t, "acme-notes")
	if err := facet.Install(pluginsDir, "acme-notes", staged, manifest, nil, facet.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	grantsPath := filepath.Join(pluginsDir, "acme-notes", "grants.json")
	if err := os.WriteFile(grantsPath, []byte(`{"tools":["notes.read"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Replace with a manifest that now declares a tool the original never did.
	newManifest := manifest
	newManifest.Tools = []string{"notes.write"}
	dir2 := buildPluginDir(t, map[string]any{
		"id": "acme-notes", "name": "Test Plugin", "version": "1.0.0",
		"api": 0, "entry": "plugin.js", "tools": []string{"notes.write"},
	}, pluginFile{name: "plugin.js", body: []byte("x")})
	staged2, err := facet.VerifyPackage(dir2, facet.VerifyOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if err := facet.Install(pluginsDir, "acme-notes", dir2, staged2, &manifest, facet.InstallOptions{Replace: true}); err != nil {
		t.Fatalf("a replace adding a new tool was refused outright: %v", err)
	}

	if _, err := os.Stat(grantsPath); err == nil {
		t.Fatal("grants.json survived a replace that added a new tool")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// An installed directory includes host-written .source.json, which a raw
// package cannot carry. Assert State as well as VerifyError so a successful
// install cannot appear invalid or pass with an empty state.
func TestInstalledPluginsReportsAFreshInstallAsOK(t *testing.T) {
	pluginsDir := t.TempDir()
	staged, manifest := stageFor(t, "acme-notes")
	if err := facet.Install(pluginsDir, "acme-notes", staged, manifest, nil, facet.InstallOptions{}); err != nil {
		t.Fatal(err)
	}

	got, err := facet.InstalledPlugins(pluginsDir)
	if err != nil {
		t.Fatalf("InstalledPlugins failed outright: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly one installed plugin, got %d", len(got))
	}
	if got[0].State != "ok" {
		t.Fatalf("a freshly installed plugin was not reported ok: %s", got[0].State)
	}
	if got[0].VerifyError != nil {
		t.Fatalf("a freshly installed plugin carried a VerifyError: %v", got[0].VerifyError)
	}
}

// TestInstalledPluginsStillRejectsAStrayExtraFile proves the widened
// allow-list is exactly two names wide, not "anything goes" — a file neither
// the package nor Install itself put there must still fail.
func TestInstalledPluginsStillRejectsAStrayExtraFile(t *testing.T) {
	pluginsDir := t.TempDir()
	staged, manifest := stageFor(t, "acme-notes")
	if err := facet.Install(pluginsDir, "acme-notes", staged, manifest, nil, facet.InstallOptions{}); err != nil {
		t.Fatal(err)
	}

	stray := filepath.Join(pluginsDir, "acme-notes", "unexpected.txt")
	if err := os.WriteFile(stray, []byte("not part of the package"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := facet.InstalledPlugins(pluginsDir)
	if err != nil {
		t.Fatalf("InstalledPlugins failed outright: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly one installed plugin, got %d", len(got))
	}
	if got[0].State == "ok" {
		t.Fatal("a stray file outside the allow-list was reported ok")
	}
}

// TestInstalledPluginsRejectsADirectoryNameNotMatchingTheManifest proves a
// plugin cannot be listed loadable under a directory name that is not what
// its own manifest declares — the same risk plugin_broker.go's grant read
// already refuses for a different reason (a manifest naming another
// plugin's id could read that plugin's grants). Here it would let a
// directory answer to one id while a host loads it as another.
func TestInstalledPluginsRejectsADirectoryNameNotMatchingTheManifest(t *testing.T) {
	pluginsDir := t.TempDir()
	staged, manifest := stageFor(t, "acme-notes")
	if err := facet.Install(pluginsDir, "acme-notes", staged, manifest, nil, facet.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	// Rename the installed directory so its name no longer agrees with the
	// manifest it carries — the layout a copy-pasted or hand-renamed
	// directory would produce.
	if err := os.Rename(
		filepath.Join(pluginsDir, "acme-notes"),
		filepath.Join(pluginsDir, "mismatched-dir-name"),
	); err != nil {
		t.Fatal(err)
	}

	got, err := facet.InstalledPlugins(pluginsDir)
	if err != nil {
		t.Fatalf("InstalledPlugins failed outright: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly one listed row, got %d", len(got))
	}
	if got[0].State == "ok" {
		t.Fatal("a directory name disagreeing with its own manifest's id was reported ok")
	}
}

func TestInstalledPluginsVerifiesADevInstallThroughItsSymlink(t *testing.T) {
	pluginsDir := t.TempDir()
	work := t.TempDir()
	manifest := `{"id":"local-dev","name":"Local Dev","version":"0.1.0","api":0,"entry":"entry.js"}`
	if err := os.WriteFile(filepath.Join(work, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write plugin.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, "entry.js"), []byte("export default function(){}"), 0o644); err != nil {
		t.Fatalf("write entry.js: %v", err)
	}
	m, verr := facet.VerifyPackage(work, facet.VerifyOptions{SkipSums: true})
	if verr != nil {
		t.Fatalf("fixture precondition failed: the working copy does not verify: %v", verr)
	}

	if err := facet.Install(pluginsDir, "local-dev", "", m, nil, facet.InstallOptions{
		DevLinkTarget: work,
		Source:        facet.SourceInfo{Dev: true},
	}); err != nil {
		t.Fatalf("dev install: %v", err)
	}

	got, err := facet.InstalledPlugins(pluginsDir)
	if err != nil {
		t.Fatalf("InstalledPlugins failed outright: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly one listed row, got %d", len(got))
	}
	if got[0].State != "ok" {
		t.Fatalf("a --dev install must verify ok through its symlink, got %q", got[0].State)
	}
}

// TestInstalledPluginsStillOKWithGrants proves grants.json — the second
// host-written file, present only once a right has actually been granted —
// does not itself make an installed plugin look invalid.
func TestInstalledPluginsStillOKWithGrants(t *testing.T) {
	pluginsDir := t.TempDir()
	staged, manifest := stageFor(t, "acme-notes")
	if err := facet.Install(pluginsDir, "acme-notes", staged, manifest, nil, facet.InstallOptions{}); err != nil {
		t.Fatal(err)
	}

	grantsPath := filepath.Join(pluginsDir, "acme-notes", "grants.json")
	if err := os.WriteFile(grantsPath, []byte(`{"tools":["notes.read"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := facet.InstalledPlugins(pluginsDir)
	if err != nil {
		t.Fatalf("InstalledPlugins failed outright: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly one installed plugin, got %d", len(got))
	}
	if got[0].State != "ok" {
		t.Fatalf("an installed plugin with grants.json was not reported ok: %s", got[0].State)
	}
}

// Widening reads drops the old grant. Parse the real manifest to check the
// field as well as the rights comparison; empty parsed lists must not hide it.
func TestReplaceDropsGrantsWhenReadsIsWidened(t *testing.T) {
	pluginsDir := t.TempDir()
	base := validManifest("acme-notes")
	base["tools"] = []string{"repo.read"}
	base["reads"] = []string{"docs/adr"}
	dir1 := buildPluginDir(t, base, pluginFile{name: "plugin.js", body: []byte("x")})
	first, err := facet.VerifyPackage(dir1, facet.VerifyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Reads) != 1 {
		t.Fatalf("fixture precondition: the parsed manifest carries reads %v, want [docs/adr]", first.Reads)
	}
	if err := facet.Install(pluginsDir, "acme-notes", dir1, first, nil, facet.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	grantsPath := filepath.Join(pluginsDir, "acme-notes", "grants.json")
	if err := os.WriteFile(grantsPath, []byte(`{"tools":["repo.read"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	widened := validManifest("acme-notes")
	widened["tools"] = []string{"repo.read"}
	widened["reads"] = []string{"docs/adr", "docs"}
	dir2 := buildPluginDir(t, widened, pluginFile{name: "plugin.js", body: []byte("x")})
	second, err := facet.VerifyPackage(dir2, facet.VerifyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := facet.Install(pluginsDir, "acme-notes", dir2, second, &first, facet.InstallOptions{Replace: true}); err != nil {
		t.Fatalf("a replace widening reads was refused outright: %v", err)
	}
	if _, err := os.Stat(grantsPath); err == nil {
		t.Fatal("grants.json survived a replace that widened reads")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
