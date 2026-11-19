package facet_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/facet-framework/core/registry"
)

// A plugin directory, built from what a real one would hold.
func pluginDir(t *testing.T, tools []string, grant map[string]any) string {
	t.Helper()
	return pluginDirNamed(t, "confluence", tools, grant)
}

// pluginDirNamed is pluginDir with the manifest's binary name in the caller's
// hands, because one case is about what that name may be.
func pluginDirNamed(t *testing.T, bin string, tools []string, grant map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	id := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(id, 0o755); err != nil {
		t.Fatal(err)
	}

	platform := runtime.GOOS + "-" + runtime.GOARCH
	src, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(id, "confluence-"+platform), src, 0o755); err != nil {
		t.Fatal(err)
	}

	manifest := map[string]any{
		"id":    "wiki",
		"tools": tools,
		"sidecar": map[string]any{
			"bin":    bin,
			"env":    []string{helperEnv, markerEnv},
			"sha256": map[string]string{platform: selfSum(t)},
		},
	}
	writeJSON(t, filepath.Join(id, "plugin.json"), manifest)
	if grant != nil {
		writeJSON(t, filepath.Join(id, "grants.json"), grant)
	}
	return dir
}

// granted is the shape the host writes: what the user answered, tools and
// variables both.
func granted(tools ...string) map[string]any {
	return map[string]any{"tools": tools, "env": []string{helperEnv, markerEnv}}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAToolTheManifestNeverDeclaredIsRefused(t *testing.T) {
	t.Setenv(helperEnv, "echo")
	b := facet.NewPluginBroker(pluginDir(t, []string{"confluence.pull"}, granted("confluence.pull")))

	// The precondition: the declared tool does go through, so the refusal
	// below is about the declaration and not about a broken directory.
	if err := b.Allowed("wiki", "confluence.pull"); err != nil {
		t.Fatalf("the declared and granted tool was refused: %v", err)
	}

	err := b.Allowed("wiki", "repo.commit")

	if !errors.Is(err, facet.ErrToolNotDeclared) {
		t.Fatalf("an undeclared tool was allowed: %v", err)
	}
}

func TestADeclaredButUngrantedToolNeverReachesTheBinary(t *testing.T) {
	t.Setenv(helperEnv, "echo")
	marker := filepath.Join(t.TempDir(), "ran")
	t.Setenv(markerEnv, marker)
	// Declared, granted nothing — but the variables ARE granted, so a binary
	// that did run would run in echo mode and write the marker. Without that
	// the case would be green for the wrong reason: a child with no
	// environment does not answer either.
	b := facet.NewPluginBroker(pluginDir(t, []string{"confluence.pull"},
		map[string]any{"tools": []string{}, "env": []string{helperEnv, markerEnv}}))
	defer b.StopAll()

	_, err := b.Call(context.Background(), "wiki", "confluence.pull", nil)

	if !errors.Is(err, facet.ErrToolNotGranted) {
		t.Fatalf("an ungranted tool was called: %v", err)
	}

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the binary ran before the grant was checked")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestAGrantedToolReachesTheBinaryAndComesBack(t *testing.T) {
	t.Setenv(helperEnv, "echo")
	b := facet.NewPluginBroker(pluginDir(t, []string{"confluence.pull"}, granted("confluence.pull")))
	defer b.StopAll()

	reply, err := b.Call(context.Background(), "wiki", "confluence.pull", nil)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Tool string `json:"tool"`
	}
	if err := json.Unmarshal(reply.Result, &got); err != nil {
		t.Fatalf("the reply is not what the sidecar sent: %v", err)
	}
	if got.Tool != "confluence.pull" {
		t.Errorf("the tool name did not survive the broker: %q", got.Tool)
	}
}

func TestAPluginIdCannotWalkOutOfItsDirectory(t *testing.T) {
	b := facet.NewPluginBroker(pluginDir(t, []string{"confluence.pull"}, granted("confluence.pull")))

	// The id arrives from the browser. Without validPluginID this reads a
	// manifest from wherever the path lands.
	for _, id := range []string{"../wiki", "..", "wiki/../../etc", "Wiki", "wiki\x00"} {
		if _, err := b.Manifest(id); err == nil {
			t.Errorf("the id %q was accepted", id)
		}
	}
}

func TestAManifestCannotClaimAnotherPluginsName(t *testing.T) {
	dir := pluginDir(t, []string{"confluence.pull"}, granted("confluence.pull"))
	writeJSON(t, filepath.Join(dir, "wiki", "plugin.json"), map[string]any{
		"id":    "concepts",
		"tools": []string{"confluence.pull"},
	})

	_, err := facet.NewPluginBroker(dir).Manifest("wiki")

	// The id is the directory's, not the file's. A manifest that could name
	// itself could claim another plugin's grants.
	if err == nil || !strings.Contains(err.Error(), "concepts") {
		t.Fatalf("a manifest claiming another name was accepted: %v", err)
	}
}

func TestAPluginWithoutABinaryIsSaidPlainly(t *testing.T) {
	dir := pluginDir(t, []string{"confluence.pull"}, granted("confluence.pull"))
	writeJSON(t, filepath.Join(dir, "wiki", "plugin.json"), map[string]any{
		"id":    "wiki",
		"tools": []string{"confluence.pull"},
	})

	_, err := facet.NewPluginBroker(dir).Call(context.Background(), "wiki", "confluence.pull", nil)

	if !errors.Is(err, facet.ErrNoSidecar) {
		t.Fatalf("a plugin with no binary failed for the wrong reason: %v", err)
	}
}

func TestOnlyTheVariablesTheReaderGrantedReachTheSidecar(t *testing.T) {
	t.Setenv("CONFLUENCE_TOKEN", "secret")
	t.Setenv(helperEnv, "env")
	dir := t.TempDir()
	id := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(id, 0o755); err != nil {
		t.Fatal(err)
	}
	platform := runtime.GOOS + "-" + runtime.GOARCH
	src, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(id, "confluence-"+platform), src, 0o755); err != nil {
		t.Fatal(err)
	}
	// The manifest asks for a secret alongside what it needs to run.
	writeJSON(t, filepath.Join(id, "plugin.json"), map[string]any{
		"id":    "wiki",
		"tools": []string{"confluence.pull"},
		"sidecar": map[string]any{
			"bin":    "confluence",
			"env":    []string{helperEnv, "CONFLUENCE_TOKEN"},
			"sha256": map[string]string{platform: selfSum(t)},
		},
	})
	// The grant answers for the tool and for one of the two variables.
	writeJSON(t, filepath.Join(id, "grants.json"), map[string]any{
		"tools": []string{"confluence.pull"},
		"env":   []string{helperEnv},
	})

	b := facet.NewPluginBroker(dir)
	defer b.StopAll()
	reply, err := b.Call(context.Background(), "wiki", "confluence.pull", nil)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Env []string `json:"env"`
	}
	if err := json.Unmarshal(reply.Result, &got); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got.Env, "\n")

	// The precondition: the granted variable did arrive. Without it the
	// assertion below would pass for a child that got no environment at all.
	if !strings.Contains(joined, helperEnv+"=env") {
		t.Fatalf("the granted variable did not arrive: %v", got.Env)
	}
	// The promise. Pass m.Sidecar.Env straight through instead of the
	// intersection and this goes red: granting one tool would hand the plugin
	// every variable its own manifest named, and the manifest is the file the
	// plugin ships.
	if strings.Contains(joined, "CONFLUENCE_TOKEN") {
		t.Errorf("a variable the grant never named reached the sidecar: %v", got.Env)
	}
}

func TestAToolNameThatOnlyFoldsOntoAGrantIsRefused(t *testing.T) {
	t.Setenv(helperEnv, "echo")
	marker := filepath.Join(t.TempDir(), "ran")
	t.Setenv(markerEnv, marker)
	// Both spellings declared, one of them granted. Declaring both is what
	// isolates the grant check: with only the lowercase one in the manifest
	// the call would be refused one question earlier, and the case would pass
	// without ever reaching the comparison it is about.
	b := facet.NewPluginBroker(pluginDir(t,
		[]string{"confluence.pull", "CONFLUENCE.PULL"}, granted("confluence.pull")))
	defer b.StopAll()

	// The precondition: the grant's own spelling does go through.
	if err := b.Allowed("wiki", "confluence.pull"); err != nil {
		t.Fatalf("the granted spelling was refused: %v", err)
	}

	if _, err := b.Call(context.Background(), "wiki", "confluence.pull", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("allowed fixture did not execute: %v", err)
	}
	b.StopAll()
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	_, callErr := b.Call(context.Background(), "wiki", "CONFLUENCE.PULL", nil)
	if !errors.Is(callErr, facet.ErrToolNotGranted) {
		t.Fatalf("folded call was not refused at grant check: %v", callErr)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("folded call executed the binary before refusal: %v", err)
	}
	err := b.Allowed("wiki", "CONFLUENCE.PULL")

	if !errors.Is(err, facet.ErrToolNotGranted) {
		t.Fatalf("a folded spelling was allowed against the grant: %v", err)
	}
}

func TestASidecarBinaryCannotWalkOutOfThePluginDirectory(t *testing.T) {
	t.Setenv(helperEnv, "echo")
	marker := filepath.Join(t.TempDir(), "ran")
	t.Setenv(markerEnv, marker)
	for _, bin := range []string{"../confluence", "../../confluence", "/bin/sh", "sub/confluence", "con.fluence"} {
		dir := pluginDirNamed(t, bin, []string{"confluence.pull"}, granted("confluence.pull"))
		if bin == "../confluence" {
			bytes, err := os.ReadFile(os.Args[0])
			if err != nil {
				t.Fatal(err)
			}
			candidate := filepath.Join(dir, "confluence-"+runtime.GOOS+"-"+runtime.GOARCH)
			if err := os.WriteFile(candidate, bytes, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		b := facet.NewPluginBroker(dir)
		_, err := b.Call(context.Background(), "wiki", "confluence.pull", nil)
		b.StopAll()
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("invalid binary name executed before refusal: %v", err)
		}

		if err == nil {
			t.Errorf("the binary name %q was accepted", bin)
			continue
		}
		if !strings.Contains(err.Error(), "which is not a name") {
			t.Errorf("the binary name %q was refused for the wrong reason: %v", bin, err)
		}
	}
}

func TestABundledPluginsDeclaredToolIsGrantedWithoutInstallingIt(t *testing.T) {
	b := facet.NewPluginBroker(t.TempDir())

	if err := b.Allowed("wiki", "repo.write"); err != nil {
		t.Fatalf("a bundled plugin's own tool was refused: %v", err)
	}
}

// The wiki's page history is a host tool of the bundled manifest, so it is
// granted like the write tools and no further.
func TestTheBundledWikiMayReadPageHistory(t *testing.T) {
	b := facet.NewPluginBroker(t.TempDir())

	if err := b.Allowed("wiki", "repo.log"); err != nil {
		t.Fatalf("the wiki's repo.log was refused: %v", err)
	}
	if err := b.Allowed("adr", "repo.log"); !errors.Is(err, facet.ErrToolNotDeclared) {
		t.Fatalf("a plugin that never declared repo.log was allowed it: %v", err)
	}
}

// The same bundled plugin's tools stop at the table: a name the embedded
// manifest does not list is still refused as undeclared, not silently
// granted because the id happens to be bundled.
func TestABundledPluginsToolNotInItsTableIsStillRefused(t *testing.T) {
	b := facet.NewPluginBroker(t.TempDir())

	defer b.StopAll()
	_, callErr := b.Call(context.Background(), "wiki", "confluence.delete", nil)
	if !errors.Is(callErr, facet.ErrToolNotDeclared) {
		t.Fatalf("undeclared bundled call was routed: %v", callErr)
	}
	err := b.Allowed("wiki", "confluence.delete")

	if !errors.Is(err, facet.ErrToolNotDeclared) {
		t.Fatalf("a tool absent from the bundled table was allowed: %v", err)
	}
}

// A plugin that is not on the bundled table gets no free pass: an id nobody
// bundled and nobody installed is refused as not installed, and one that IS
// installed but carries no grants.json is refused as not granted — the
// ordinary rule, unaffected by the bundled exception.
func TestANonBundledPluginStillRequiresAnInstalledGrant(t *testing.T) {
	dir := t.TempDir()

	// Nobody bundled it and nobody installed it.
	if err := facet.NewPluginBroker(dir).Allowed("margin-note", "repo.write"); !errors.Is(err, facet.ErrPluginNotInstalled) {
		t.Fatalf("an id nobody shipped was not reported as not installed: %v", err)
	}

	// Installed, with a real plugin.json declaring the tool, but no
	// grants.json — the ordinary default-deny, which the bundled exception
	// must not widen for a plugin that has its own directory.
	id := filepath.Join(dir, "notes")
	if err := os.MkdirAll(id, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(id, "plugin.json"), map[string]any{
		"id":    "notes",
		"tools": []string{"repo.write"},
	})

	err := facet.NewPluginBroker(dir).Allowed("notes", "repo.write")
	if !errors.Is(err, facet.ErrToolNotGranted) {
		t.Fatalf("an installed plugin with no grants.json was allowed anyway: %v", err)
	}
}

func TestAWindowsSidecarIsSoughtWithTheExeSuffix(t *testing.T) {
	t.Setenv(helperEnv, "echo")
	const platform = "windows-amd64"
	dir := t.TempDir()
	id := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(id, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(id, "confluence-"+platform+".exe"), src, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(id, "plugin.json"), map[string]any{
		"id":    "wiki",
		"tools": []string{"confluence.pull"},
		"sidecar": map[string]any{
			"bin":    "confluence",
			"env":    []string{helperEnv, markerEnv},
			"sha256": map[string]string{platform: selfSum(t)},
		},
	})
	writeJSON(t, filepath.Join(id, "grants.json"), granted("confluence.pull"))

	b := facet.NewPluginBrokerForPlatform(dir, platform)
	defer b.StopAll()

	reply, err := b.Call(context.Background(), "wiki", "confluence.pull", nil)

	// Drop the ".exe" case from sidecarFileName and this fails: the broker
	// looks for "confluence-windows-amd64" without the suffix, which is not
	// the file on disk, so the call errors instead of reaching the binary.
	if err != nil {
		t.Fatalf("a windows sidecar named with its real .exe filename was not found: %v", err)
	}
	var got struct {
		Tool string `json:"tool"`
	}
	if err := json.Unmarshal(reply.Result, &got); err != nil {
		t.Fatalf("the reply is not what the sidecar sent: %v", err)
	}
	if got.Tool != "confluence.pull" {
		t.Errorf("the tool name did not survive the broker: %q", got.Tool)
	}
}

// The ADR plugin is scoped to docs/adr and nothing wider — a plugin
// bundled without an installed grants.json is granted every tool its own
// manifest declares, so a "reads" list wider than intended would be a real
// widening of what the plugin can read, not just a paper claim.
func TestTheAdrPluginIsScopedToDocsAdrOnly(t *testing.T) {
	m, err := facet.NewPluginBroker(t.TempDir()).Manifest("adr")
	if err != nil {
		t.Fatalf("the bundled adr manifest was refused: %v", err)
	}
	if len(m.Reads) != 1 || m.Reads[0].Dir != "docs/adr" || len(m.Reads[0].Exts()) != 1 || m.Reads[0].Exts()[0] != ".md" {
		t.Fatalf("bundledManifests[\"adr\"].Reads = %v, want exactly [\"docs/adr\"]", m.Reads)
	}
}

// The bundled ADR manifest grants its declared tools without grants.json.
// Declaring a write tool would silently widen its read-only access.
func TestTheAdrPluginDeclaresNoWriteTool(t *testing.T) {
	m, err := facet.NewPluginBroker(t.TempDir()).Manifest("adr")
	if err != nil {
		t.Fatalf("the bundled adr manifest was refused: %v", err)
	}
	want := map[string]bool{"repo.list": true, "repo.read": true}
	if len(m.Tools) != len(want) {
		t.Fatalf("bundledManifests[\"adr\"].Tools = %v, want exactly repo.list and repo.read", m.Tools)
	}
	for _, tool := range m.Tools {
		if !want[tool] {
			t.Errorf("bundledManifests[\"adr\"].Tools declares %q, which is not repo.list or repo.read", tool)
		}
	}
}

// The bundled ADR entry has the same tools and read scope as its versioned
// producer contract. Widening either side alone fails this comparison. Full
// release qualification compares the snapshot to the actual plugin manifest.
func TestTheBundledAdrTableAgreesWithTheRealManifest(t *testing.T) {
	raw := facet.BundledPluginContract(t, "adr")
	var real struct {
		Tools []string         `json:"tools"`
		Reads []facet.ReadDecl `json:"reads"`
	}
	if err := json.Unmarshal(raw, &real); err != nil {
		t.Fatalf("the versioned plugin contract is not JSON: %v", err)
	}
	if len(real.Tools) == 0 || len(real.Reads) == 0 {
		t.Fatalf("fixture precondition: the real manifest names tools %v and reads %v", real.Tools, real.Reads)
	}
	bundled, err := facet.NewPluginBroker(t.TempDir()).Manifest("adr")
	if err != nil {
		t.Fatalf("the bundled adr manifest was refused: %v", err)
	}
	if strings.Join(bundled.Tools, ",") != strings.Join(real.Tools, ",") {
		t.Errorf("bundledManifests[\"adr\"].Tools = %v, plugin.json says %v", bundled.Tools, real.Tools)
	}
	bundledReads, _ := json.Marshal(bundled.Reads)
	realReads, _ := json.Marshal(real.Reads)
	if string(bundledReads) != string(realReads) {
		t.Errorf("bundledManifests[\"adr\"].Reads = %v, plugin.json says %v", bundled.Reads, real.Reads)
	}
}
