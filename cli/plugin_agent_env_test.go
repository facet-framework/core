package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/facet-framework/core/registry"
)

// buildPackedAgentPlugin writes and packs a plugin whose sidecar declares
// env and ai_env, and which offers the agent one tool — the shape a grant for
// the agent's environment is a question about at all. The sidecar binary is
// bytes standing in for a build; nothing here runs it.
func buildPackedAgentPlugin(t *testing.T, env, aiEnv []string) string {
	t.Helper()
	platform := runtime.GOOS + "-" + runtime.GOARCH
	bin := "probe-" + platform
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	src := t.TempDir()
	manifest := map[string]any{
		"id": "acme-notes", "name": "Acme Notes", "version": "1.0.0",
		"api": 0, "entry": "plugin.js", "tools": []string{"notes.read"},
		"ai_tools": []map[string]any{{
			"tool": "notes.read", "description": "reads a note",
			"input": map[string]any{"type": "object"},
		}},
		"sidecar": map[string]any{
			"bin": "probe", "env": env, "ai_env": aiEnv,
			"sha256": map[string]any{},
		},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{
		"plugin.json": raw,
		"plugin.js":   []byte("export default function(){}"),
		bin:           []byte("bytes standing in for a build"),
	} {
		if err := os.WriteFile(filepath.Join(src, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(t.TempDir(), "acme-notes-1.0.0.facetplugin")
	if _, err := facet.Pack(src, out); err != nil {
		t.Fatalf("fixture precondition failed: packing did not succeed: %v", err)
	}
	return out
}

func readGrant(t *testing.T, dir string) facet.Grant {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "acme-notes", "grants.json"))
	if err != nil {
		t.Fatalf("no grants.json was written: %v", err)
	}
	var g facet.Grant
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("grants.json is not a grant: %v\n%s", err, raw)
	}
	return g
}

// --grant ai-env=X writes X under ai_env, and nothing under env: the two are
// separate answers, the way ai=… and tools=… are.
func TestGrantAIEnvWritesTheAgentsEnvironmentSeparately(t *testing.T) {
	source := buildPackedAgentPlugin(t, []string{"NOTES_TOKEN"}, []string{"NOTES_TOKEN"})

	dir, output, err := runPluginAddCLI(t, "", source, "--grant", "ai-env=NOTES_TOKEN")
	if err != nil {
		t.Fatalf("--grant ai-env naming a declared variable was refused: %v\n%s", err, output)
	}

	g := readGrant(t, dir)
	if len(g.AIEnv) != 1 || g.AIEnv[0] != "NOTES_TOKEN" {
		t.Fatalf("grants.json ai_env = %v, want [NOTES_TOKEN]", g.AIEnv)
	}
	if len(g.Env) != 0 {
		t.Fatalf("--grant ai-env also granted the surface: env = %v", g.Env)
	}
}

// A variable the manifest did not offer the agent cannot be granted to it.
func TestGrantAIEnvNamingAnUnofferedVariableIsRefused(t *testing.T) {
	t.Run("absent from ai_env", func(t *testing.T) {
		source := buildPackedAgentPlugin(t, []string{"NOTES_TOKEN", "NOTES_URL"}, []string{"NOTES_URL"})

		_, _, err := runPluginAddCLI(t, "", source, "--grant", "ai-env=NOTES_TOKEN")

		if err == nil {
			t.Fatal("a --grant ai-env naming a variable the manifest offers only the surface was not refused")
		}
		if !strings.Contains(err.Error(), "NOTES_TOKEN") {
			t.Fatalf("the refusal does not name the variable: %v", err)
		}
	})
	t.Run("in ai_env but not in env", func(t *testing.T) {
		// Packing itself refuses this manifest? No: the schema holds each
		// list's grammar, not the subset relation, so the install path has
		// to. The precondition below says the package exists at all.
		source := buildPackedAgentPlugin(t, []string{"NOTES_URL"}, []string{"NOTES_TOKEN"})

		_, _, err := runPluginAddCLI(t, "", source, "--grant", "ai-env=NOTES_TOKEN")

		if err == nil {
			t.Fatal("a --grant ai-env naming a variable the manifest never declared under env was not refused")
		}
		if !strings.Contains(err.Error(), "NOTES_TOKEN") {
			t.Fatalf("the refusal does not name the variable: %v", err)
		}
	})
}

// A --replace that names one group keeps the other three.
func TestReplaceWithOneGrantKeepsTheOtherGroups(t *testing.T) {
	source := buildPackedAgentPlugin(t, []string{"NOTES_TOKEN"}, []string{"NOTES_TOKEN"})
	dir := t.TempDir()
	t.Setenv("FACET_PLUGINS_DIR", dir)

	run := func(args ...string) {
		t.Helper()
		cmd := newPluginAddCmd()
		var out strings.Builder
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetIn(strings.NewReader(""))
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%v failed: %v\n%s", args, err, out.String())
		}
	}
	run(source, "--grant", "tools=notes.read", "--grant", "env=NOTES_TOKEN", "--grant", "ai=notes.read")
	// The precondition: all three were written, so a null below is a loss
	// and not a grant that never existed.
	before := readGrant(t, dir)
	if len(before.Tools) != 1 || len(before.Env) != 1 || len(before.AITools) != 1 {
		t.Fatalf("the first install did not write all three groups: %+v", before)
	}

	run(source, "--replace", "--grant", "ai-env=NOTES_TOKEN")

	after := readGrant(t, dir)
	if len(after.Tools) != 1 || after.Tools[0] != "notes.read" {
		t.Errorf("--replace --grant ai-env dropped tools: %v", after.Tools)
	}
	if len(after.Env) != 1 || after.Env[0] != "NOTES_TOKEN" {
		t.Errorf("--replace --grant ai-env dropped env: %v", after.Env)
	}
	if len(after.AITools) != 1 || after.AITools[0] != "notes.read" {
		t.Errorf("--replace --grant ai-env dropped ai_tools: %v", after.AITools)
	}
	if len(after.AIEnv) != 1 || after.AIEnv[0] != "NOTES_TOKEN" {
		t.Errorf("--replace --grant ai-env did not write ai_env: %v", after.AIEnv)
	}
}

// A --replace without any --grant and without a terminal leaves the kept
// grant exactly as it was — the groups nobody mentioned keep their answer,
// and the "nothing granted" notice does not print, because something is.
func TestReplaceWithoutGrantsKeepsTheKeptGrantUntouched(t *testing.T) {
	source := buildPackedAgentPlugin(t, []string{"NOTES_TOKEN"}, []string{"NOTES_TOKEN"})
	dir := t.TempDir()
	t.Setenv("FACET_PLUGINS_DIR", dir)

	run := func(args ...string) string {
		t.Helper()
		cmd := newPluginAddCmd()
		var out strings.Builder
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetIn(strings.NewReader(""))
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%v failed: %v\n%s", args, err, out.String())
		}
		return out.String()
	}
	run(source, "--grant", "tools=notes.read", "--grant", "env=NOTES_TOKEN")
	grantPath := filepath.Join(dir, "acme-notes", "grants.json")
	original, readErr := os.ReadFile(grantPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	before := append(append([]byte("\n  "), original...), '\n')
	before = bytes.Replace(before, []byte("{"), []byte("{\"future_metadata\": {\"keep\": true},"), 1)
	if err := os.WriteFile(grantPath, before, 0o644); err != nil {
		t.Fatal(err)
	}

	output := run(source, "--replace")

	exact, readErr := os.ReadFile(grantPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(before, exact) {
		t.Fatal("plain replace rewrote grant bytes")
	}
	after := readGrant(t, dir)
	if len(after.Tools) != 1 || len(after.Env) != 1 {
		t.Fatalf("a plain --replace lost the kept grant: %+v", after)
	}
	if strings.Contains(output, "nothing granted") {
		t.Fatalf("a replace that kept a grant claimed nothing was granted:\n%s", output)
	}
}

// Without a terminal, the agent's environment is not granted either, and the
// notice names the group a script would use.
func TestNoTTYGrantsNoAgentEnvironment(t *testing.T) {
	source := buildPackedAgentPlugin(t, []string{"NOTES_TOKEN"}, []string{"NOTES_TOKEN"})

	dir, output, err := runPluginAddCLI(t, "", source)
	if err != nil {
		t.Fatalf("a plain install was refused: %v\n%s", err, output)
	}

	if _, err := os.Stat(filepath.Join(dir, "acme-notes", "grants.json")); !os.IsNotExist(err) {
		t.Fatalf("a non-interactive install granted something nobody asked for: err=%v", err)
	}
	if !strings.Contains(output, "ai-env=") {
		t.Fatalf("the notice does not tell a script how to grant the agent's environment:\n%s", output)
	}
}

func TestReplacingAPluginWithNewAgentEnvironmentDropsTheOldGrant(t *testing.T) {
	first := buildPackedAgentPlugin(t, []string{"A", "B"}, []string{"A"})
	update := buildPackedAgentPlugin(t, []string{"A", "B"}, []string{"A", "B"})
	dir := t.TempDir()
	t.Setenv("FACET_PLUGINS_DIR", dir)
	run := func(args ...string) {
		t.Helper()
		cmd := newPluginAddCmd()
		var out strings.Builder
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetIn(strings.NewReader(""))
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("install failed: %v: %s", err, out.String())
		}
	}
	run(first, "--grant", "tools=notes.read", "--grant", "env=A,B", "--grant", "ai-env=A")
	if grant := readGrant(t, dir); len(grant.AIEnv) != 1 || grant.AIEnv[0] != "A" {
		t.Fatalf("old grant precondition missing: %+v", grant)
	}
	run(update, "--replace")
	if _, err := os.Stat(filepath.Join(dir, "acme-notes", "grants.json")); !os.IsNotExist(err) {
		t.Fatalf("update widened ai_env but kept old grant: %v", err)
	}
}
