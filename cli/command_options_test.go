package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/facet-framework/core/internal/atlaslauncher"
	"github.com/facet-framework/core/registry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func TestCoreCommandTreeIncludesTheAtlasLauncher(t *testing.T) {
	cmd := NewCommand(Options{})
	for _, name := range []string{"lint", "snapshot", "setup", "mcp", "lsp"} {
		found, _, err := cmd.Find([]string{name})
		if err != nil || found.Name() != name {
			t.Fatalf("Core command %q missing: %v", name, err)
		}
	}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"ui", "--help"})
	if err := cmd.Execute(); err != nil || !strings.Contains(out.String(), "--atlas-version") ||
		!strings.Contains(out.String(), "The remaining arguments go to Atlas.") {
		t.Fatalf("Core launcher help: %v, output %q", err, out.String())
	}
	t.Setenv("FACET_ATLAS_CACHE", t.TempDir())
	launcher := atlaslauncher.New("test")
	version := launcher.Version()
	root := pinnedProject(t, "9.9.9")
	code, stdout, stderr := runTree(t, "1.2.3", nil, "ui", "--atlas-version", "--root", root)
	if code != 0 || strings.TrimSpace(stdout) != "FACET Atlas "+version+" (Core "+launcher.AtlasCoreVersion()+") is not installed\nEmbedded: "+version+"\nActive: "+version+" (Core "+launcher.AtlasCoreVersion()+")" || stderr != "" {
		t.Fatalf("Atlas version in a pinned project: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	got := checkUI(newStatusEnv(t.TempDir()))
	if got.State != stateNotApplicable || got.Finding != "FACET Atlas "+version+" is downloaded on the first `facet ui`" {
		t.Fatalf("ui row before the Atlas download: %+v", got)
	}
}

func TestApplicationCommandsAndFrontEndAssetsAreExplicit(t *testing.T) {
	const invoked = "application UI"
	extra := &cobra.Command{Use: "ui", Run: func(cmd *cobra.Command, _ []string) {
		cmd.Println(invoked)
	}}
	cmd := NewCommand(Options{Commands: []*cobra.Command{extra}})
	found, _, err := cmd.Find([]string{"ui"})
	if err != nil || found != extra {
		t.Fatalf("application command not registered: %v", err)
	}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"ui", "--root", t.TempDir()})
	if err := cmd.Execute(); err != nil || strings.TrimSpace(out.String()) != invoked {
		t.Fatalf("application command run: %v, output %q", err, out.String())
	}
	for _, tc := range []struct {
		assets fstest.MapFS
		want   checkState
	}{
		{fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html>")}}, stateOK},
		{fstest.MapFS{}, stateRed},
	} {
		root := t.TempDir()
		t.Setenv("HOME", t.TempDir())
		t.Setenv("SHELL", filepath.Join(root, "no-shell"))
		t.Setenv("CLAUDE_PLUGIN_DATA", filepath.Join(root, "plugin-data"))
		t.Setenv("FACET_PLUGINS_DIR", filepath.Join(root, "plugins"))
		selected := NewCommand(Options{ReaderAssets: tc.assets})
		var out bytes.Buffer
		selected.SetOut(&out)
		selected.SetArgs([]string{"setup", "status", "--root", root, "--json"})
		if err := selected.Execute(); err != nil {
			t.Fatal(err)
		}
		var rows []statusRow
		if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range rows {
			if row.ID == "ui" {
				found = true
				if row.State != tc.want {
					t.Fatalf("ui row with selected assets: %+v, want %s", row, tc.want)
				}
			}
		}
		if !found {
			t.Fatal("setup omitted the UI row")
		}
	}
}

func TestCommandVersionsRemainLocalToEachTree(t *testing.T) {
	first := NewCommand(Options{Version: "first"})
	second := NewCommand(Options{Version: "second"})
	if first.Version != "first" || second.Version != "second" {
		t.Fatal("version shared between command trees")
	}
	root := snapshotRepo(t)
	out := filepath.Join(t.TempDir(), "snapshot.json")
	first.SetArgs([]string{"snapshot", "--root", root, "--out", out})
	if err := first.Execute(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot snapshotEnvelope
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Origin.FacetVersion != "first" {
		t.Fatalf("snapshot version = %q", snapshot.Origin.FacetVersion)
	}
	for _, version := range []string{"first", "second"} {
		server := newLSPServer(facet.OpenSet(root), version)
		result := server.initialize(nil).(map[string]any)
		info := result["serverInfo"].(map[string]any)
		if info["version"] != version {
			t.Fatalf("LSP version = %v, want %s", info["version"], version)
		}
	}
}

// coreCommandModulePackages names, by their last path element, every package
// of this module the Core command may link. The Core command ships without a
// browser application, embedded front-end bundle or sidecar checksums; a
// package outside this set reaching its dependency closure is how such a
// dependency would arrive, so it fails here first and is admitted by editing
// the list on purpose. The match is by last path element, so an entry that
// names no package of this tree admits nothing. "scaffold" is the payload
// 'facet init' writes, which carries Core files only.
var coreCommandModulePackages = map[string]bool{
	"atlaslauncher": true, "cli": true, "corpus": true, "docsite": true, "facet": true, "filelock": true,
	"integrations": true, "productplugin": true, "rag": true, "registry": true,
	"release": true, "remote": true, "scaffold": true, "schema": true, "sourceview": true, "spec": true,
}

func TestCoreExecutableLinksOnlyReviewedPackages(t *testing.T) {
	format := "{{if not .Standard}}{{.Name}} {{.ImportPath}} {{with .Module}}{{.Main}}{{end}}{{end}}"
	out, err := exec.Command("go", "list", "-deps", "-f", format, cliCommandPackage(t)).CombinedOutput()
	if err != nil {
		t.Fatalf("go list Core command: %v\n%s", err, out)
	}
	inModule := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		name, importPath, mainModule := fields[0], fields[1], fields[2] == "true"
		// Atlas, a separate product, is never a dependency of Core.
		if strings.Contains(importPath, "facet-framework/atlas") {
			t.Errorf("Core executable imports application dependency %s", importPath)
		}
		if !mainModule || name == "main" {
			continue
		}
		inModule++
		if !coreCommandModulePackages[path.Base(importPath)] {
			t.Errorf("Core executable links module package %s, which is not in the reviewed Core set", importPath)
		}
	}
	// The closure must have been read at all: the CLI package itself is in it.
	if inModule == 0 {
		t.Fatalf("go list reported no module package for the Core command:\n%s", out)
	}
}

func TestMCPServerVersionsRemainLocal(t *testing.T) {
	set := facet.OpenSet(t.TempDir())
	servers := []struct {
		server  *mcp.Server
		version string
	}{
		{buildMCPServer(set, "first"), "first"},
		{buildMCPServer(set, "second"), "second"},
	}
	for _, item := range servers {
		t.Run(item.version, func(t *testing.T) {
			ctx := context.Background()
			clientT, serverT := mcp.NewInMemoryTransports()
			serverSession, err := item.server.Connect(ctx, serverT, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer serverSession.Close()
			client := mcp.NewClient(&mcp.Implementation{Name: "version-test", Version: "1"}, nil)
			session, err := client.Connect(ctx, clientT, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			if got := session.InitializeResult().ServerInfo.Version; got != item.version {
				t.Fatalf("MCP version = %q, want %q", got, item.version)
			}
		})
	}
}
