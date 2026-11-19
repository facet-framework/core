// Fixtures here contain example @facet: tags; this file drives the session-start
// hook of the real binary rather than implementing a feature.
// @facet-ignore-file

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hintRun is one session start, delivered the way Claude Code delivers it: real
// hook JSON on stdin to the real binary.
type hintRun struct {
	bin  string
	data string // CLAUDE_PLUGIN_DATA, shared between runs so "once" can be tested
	home string
}

func newHintRun(t *testing.T) hintRun {
	t.Helper()
	skipOnWindows(t)
	return hintRun{bin: facetBinary(t), data: t.TempDir(), home: t.TempDir()}
}

func (h hintRun) sessionStart(t *testing.T, dir, session string) string {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"cwd": dir, "session_id": session, "hook_event_name": "SessionStart"})
	env := baseEnv(h.home, "CLAUDE_PLUGIN_DATA="+h.data)
	stdout, stderr, exit := runBin(t, h.bin, dir, env, string(payload), "hook", "session-start")
	if exit != 0 || stderr != "" {
		t.Fatalf("hook session-start: exit %d, stderr %q", exit, stderr)
	}
	return stdout
}

// hint extracts the additionalContext from a hook reply, "" when there is none.
func hint(t *testing.T, stdout string) string {
	t.Helper()
	if strings.TrimSpace(stdout) == "" {
		return ""
	}
	var out hookOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("hook output is not JSON: %v\n%s", err, stdout)
	}
	return out.HookSpecificOutput.AdditionalContext
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	git(t, dir, "init", "-q")
}

// The hint is two lines and names the command.
func TestSessionStartInAnUnadoptedGitRepoNamesSetup(t *testing.T) {
	h := newHintRun(t)
	repo := t.TempDir()
	gitInit(t, repo)
	got := hint(t, h.sessionStart(t, repo, "s1"))
	if !strings.Contains(got, "facet setup status") {
		t.Fatalf("no hint naming facet setup status:\n%q", got)
	}
	if n := len(strings.Split(strings.TrimSpace(got), "\n")); n != 2 {
		t.Fatalf("the hint is %d lines, want 2:\n%s", n, got)
	}
}

// An adopted repository has done what the hint advertises.
func TestSessionStartInAnAdoptedRepoDoesNotAdvertiseSetup(t *testing.T) {
	h := newHintRun(t)
	for name, files := range map[string]map[string]string{
		"records": {"docs/facets/features/demo/thing/widget.yaml": "schema_version: \"0.1\"\nid: demo/thing/widget\nlifecycle: active\n"},
		"config":  {".facetrc.yaml": "schema_version: \"0.1\"\n"},
		"empty":   {"docs/facets/.gitkeep": ""},
	} {
		repo := t.TempDir()
		gitInit(t, repo)
		for rel, body := range files {
			write(t, filepath.Join(repo, rel), body)
		}
		got := hint(t, h.sessionStart(t, repo, "adopted-"+name))
		if strings.Contains(got, "facet setup status") {
			t.Errorf("%s: an adopted repository was told to run setup:\n%s", name, got)
		}
	}
}

// Outside git the hint would speak in $HOME and in scratch directories.
func TestSessionStartOutsideGitSaysNothing(t *testing.T) {
	h := newHintRun(t)
	plain := t.TempDir()
	// Precondition: the directory really is outside any repository.
	if top := gitToplevel(plain); top != "" {
		t.Skipf("the temp directory sits inside a git repository: %s", top)
	}
	if got := h.sessionStart(t, plain, "plain"); got != "" {
		t.Fatalf("output outside git: %s", got)
	}
}

// A dotfiles repository at $HOME makes every directory below it "inside git".
func TestSessionStartInAHomeRepositorySaysNothing(t *testing.T) {
	h := newHintRun(t)
	gitInit(t, h.home)
	sub := filepath.Join(h.home, "notes")
	mustMkdir(t, sub)
	if got := h.sessionStart(t, sub, "home"); got != "" {
		t.Fatalf("output in a repository rooted at $HOME: %s", got)
	}
}

// Once per repository, across sessions — not once per session.
func TestTheSetupHintIsShownOncePerRepository(t *testing.T) {
	h := newHintRun(t)
	repo := t.TempDir()
	gitInit(t, repo)
	if !strings.Contains(hint(t, h.sessionStart(t, repo, "first")), "facet setup status") {
		t.Fatal("precondition: the first session got no hint")
	}
	if got := h.sessionStart(t, repo, "second"); got != "" {
		t.Fatalf("a later session repeated the hint: %s", got)
	}
	if got := h.sessionStart(t, repo, "first"); got != "" {
		t.Fatalf("the same session repeated the hint: %s", got)
	}
	other := t.TempDir()
	gitInit(t, other)
	if !strings.Contains(hint(t, h.sessionStart(t, other, "third")), "facet setup status") {
		t.Fatal("a second repository did not get its own hint")
	}
}

// The hint belongs to session start and to no other event.
func TestOtherHookEventsNeverCarryTheSetupHint(t *testing.T) {
	h := newHintRun(t)
	repo := t.TempDir()
	gitInit(t, repo)
	payload, _ := json.Marshal(map[string]any{"cwd": repo, "session_id": "ev"})
	env := baseEnv(h.home, "CLAUDE_PLUGIN_DATA="+h.data)
	for _, event := range []string{"pre-compact", "stop", "post-tool-use", "pre-tool-use"} {
		stdout, _, exit := runBin(t, h.bin, repo, env, string(payload), "hook", event)
		if exit != 0 || stdout != "" {
			t.Errorf("%s: exit %d, output %q", event, exit, stdout)
		}
	}
	// The hint is still available afterwards: those events did not use it up.
	if !strings.Contains(hint(t, h.sessionStart(t, repo, "after")), "facet setup status") {
		t.Fatal("another event consumed the once-only hint")
	}
}

// Without a plugin data directory the marker must not sit in a directory every
// user of the machine can write: another user could create it first and silence
// the hint, or plant a link there.
func TestTheSetupHintMarkerWithoutPluginDataIsPerUser(t *testing.T) {
	skipOnWindows(t)
	tmp := t.TempDir()
	repo := t.TempDir()
	gitInit(t, repo)
	payload, _ := json.Marshal(map[string]any{"cwd": repo, "session_id": "s", "hook_event_name": "SessionStart"})
	env := baseEnv(t.TempDir(), "TMPDIR="+tmp)
	stdout, _, exit := runBin(t, facetBinary(t), repo, env, string(payload), "hook", "session-start")
	if exit != 0 || !strings.Contains(hint(t, stdout), "facet setup status") {
		t.Fatalf("precondition: the hint must have been given, exit %d:\n%s", exit, stdout)
	}
	if _, err := os.Stat(filepath.Join(tmp, "notices")); err == nil {
		t.Fatal("the marker was written to the shared temp directory")
	}
	dir := filepath.Join(tmp, fmt.Sprintf("facet-%d", os.Getuid()), "notices")
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("no per-user marker directory: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("marker directory mode = %v, want 0700", info.Mode().Perm())
	}
}
