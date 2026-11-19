// @facet-ignore-file

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// Independent command trees must not collect each other's patch responses or
// rewrite another invocation's event while reconstructing compacted context.
func TestHookCommandOutputsStayWithTheirInvocation(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	type invocation struct {
		id, event string
		cmd       *cobra.Command
		output    bytes.Buffer
	}
	var calls []*invocation
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("demo/thing/request-%d", i)
		root := adoptedRepo(t, id)
		source := filepath.Join(root, "src/value.go")
		write(t, source, "// @facet: "+id+"\npackage example\n")
		in := hookInput{CWD: root, SessionID: fmt.Sprintf("isolated-%d", i)}
		event, expected := "post-tool-use", "PostToolUse"
		if i%2 == 0 {
			in.ToolName = "apply_patch"
			in.ToolInput.Command = "*** Begin Patch\n*** Update File: " + source + "\n*** End Patch"
		} else {
			in.Source = "compact"
			event, expected = "session-start", "SessionStart"
		}
		payload, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		call := &invocation{id: id, event: expected, cmd: NewCommand(Options{})}
		call.cmd.SetArgs([]string{"--root", root, "hook", event})
		call.cmd.SetIn(bytes.NewReader(payload))
		call.cmd.SetOut(&call.output)
		calls = append(calls, call)
	}
	start := make(chan struct{})
	var group sync.WaitGroup
	errors := make(chan error, len(calls))
	for _, call := range calls {
		group.Add(1)
		go func(call *invocation) {
			defer group.Done()
			<-start
			errors <- call.cmd.Execute()
		}(call)
	}
	close(start)
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, call := range calls {
		var out hookOutput
		if err := json.Unmarshal(call.output.Bytes(), &out); err != nil {
			t.Fatalf("%s must receive exactly one JSON response: %v: %s", call.id, err, &call.output)
		}
		if out.HookSpecificOutput.HookEventName != call.event || !strings.Contains(out.HookSpecificOutput.AdditionalContext, call.id) {
			t.Fatalf("%s received another invocation's response: %s", call.id, &call.output)
		}
		for _, other := range calls {
			if other != call && strings.Contains(out.HookSpecificOutput.AdditionalContext, other.id) {
				t.Fatalf("%s response contains %s: %s", call.id, other.id, &call.output)
			}
		}
	}
}

func TestCodexPatchPathsAreHeadersNotCommands(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: src/a b.go\n*** Move to: src/c d.go\n@@\n+*** Delete File: bogus\n*** Delete File: old.go\n*** Add File: src/a b.go\n+x\n*** End Patch"
	got := patchHookPaths(patch)
	want := []string{"src/a b.go", "src/c d.go", "old.go"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("paths: %q", got)
	}
	if len(patchHookPaths("*** Add File: bogus\n")) != 0 {
		t.Fatal("accepted a missing envelope")
	}
	var in hookInput
	in.ToolName = "exec_command"
	in.ToolInput.Cmd = "git commit -m test"
	in = normalizeHookInput(in)
	if in.ToolName != "Bash" || in.ToolInput.Command != "git commit -m test" {
		t.Fatalf("not normalized: %+v", in)
	}
}

func TestCodexPatchAggregatesOnlyAffectedFiles(t *testing.T) {
	root := adoptedRepo(t, "demo/thing/widget")
	other := adoptedRepo(t, "demo/thing/other")
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	t.Setenv("CLAUDE_PROJECT_DIR", root)
	a := filepath.Join(root, "src/a b.go")
	b := filepath.Join(other, "src/c d.go")
	write(t, a, "// @facet: demo/thing/widget\npackage example\n")
	write(t, b, "// @facet: demo/thing/other\npackage example\n")
	write(t, filepath.Join(root, "docs/facets/features/untouched/dirty/record.yaml"), "id: wrong/id/here\n")
	var in hookInput
	in.ToolName = "apply_patch"
	in.CWD = root
	in.SessionID = "patch"
	in.ToolInput.Command = "*** Begin Patch\n*** Update File: src/a b.go\n*** Update File: " + b + "\n*** Delete File: " + filepath.Join(other, "gone/old.go") + "\n*** End Patch"
	out := captureHook(t, func() { dispatchPatchHook(newHookCmd(), "post-tool-use", in) })
	var got hookOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("must emit one JSON: %s: %v", out, err)
	}
	c := got.HookSpecificOutput.AdditionalContext
	if !strings.Contains(c, "demo/thing/widget") || !strings.Contains(c, "demo/thing/other") || strings.Contains(c, "wrong/id/here") {
		t.Fatalf("wrong affected files: %s", out)
	}
	if len(rememberedRoots("patch")) != 2 {
		t.Fatalf("roots: %v", rememberedRoots("patch"))
	}
	plain := t.TempDir()
	write(t, filepath.Join(plain, "a.go"), "// @facet: absent/thing/record\n")
	in.ToolInput.Command = "*** Begin Patch\n*** Update File: " + filepath.Join(plain, "a.go") + "\n*** End Patch"
	if out := captureHook(t, func() { dispatchPatchHook(newHookCmd(), "post-tool-use", in) }); out != "" {
		t.Fatalf("unadopted noise: %s", out)
	}
}

func TestCodexPatchShellFallbackAggregatesRecords(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq unavailable")
	}
	root := adoptedRepo(t, "demo/thing/widget")
	write(t, filepath.Join(root, "docs/facets/features/demo/thing/other.yaml"), "id: wrong/id/here\n")
	write(t, filepath.Join(root, "docs/facets/features/demo/thing/widget.yaml"), "id: wrong/id/there\n")
	plugin, _ := filepath.Abs(cliModuleRoot(t))
	payload, _ := json.Marshal(map[string]any{"tool_name": "apply_patch", "cwd": root, "tool_input": map[string]string{"command": "*** Begin Patch\n*** Update File: docs/facets/features/demo/thing/widget.yaml\n*** Update File: docs/facets/features/demo/thing/other.yaml\n*** End Patch"}})
	cmd := exec.Command("sh", filepath.Join(plugin, "scripts/validate-record.sh"))
	cmd.Stdin = strings.NewReader(string(payload))
	cmd.Env = append(cmd.Environ(), "CLAUDE_PLUGIN_ROOT="+plugin, "FACET_NO_BINARY=1", "CLAUDE_PROJECT_DIR="+root)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script: %v %s", err, out)
	}
	var got hookOutput
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("one JSON: %v %s", err, out)
	}
	if strings.Count(got.HookSpecificOutput.AdditionalContext, "id mismatch") != 2 {
		t.Fatalf("missing per-file validation: %s", out)
	}
}

func TestCodexAndClaudeHookIngressDifferential(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	t.Setenv("FACET_SKIP_COMMIT_GATE", "")
	root := brokenRegistry(t)
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "baseline")
	record := filepath.Join(root, "docs/facets/features/demo/thing/widget.yaml")
	write(t, record, "id: wrong/id/here\n")
	run := func(event, tool, session string, input map[string]string) string {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"cwd": root, "tool_name": tool, "session_id": session, "tool_input": input})
		cmd := exec.Command(facetBinary(t), "--root", root, "hook", event)
		cmd.Stdin = strings.NewReader(string(payload))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("hook: %v %s", err, out)
		}
		return string(out)
	}
	claude := run("post-tool-use", "Write", "claude-write", map[string]string{"file_path": record})
	codex := run("post-tool-use", "apply_patch", "codex-patch", map[string]string{"command": "*** Begin Patch\n*** Update File: " + record + "\n@@\n+id: wrong/id/here\n*** End Patch"})
	if claude == "" || claude != codex {
		t.Fatalf("record differential Claude=%s Codex=%s", claude, codex)
	}
	write(t, filepath.Join(root, "src/new.ts"), "// @facet: billing/invoice/new\nexport const n = 1;\n")
	git(t, root, "add", "src/new.ts")
	claude = run("pre-tool-use", "Bash", "claude-commit", map[string]string{"command": "git commit -m next"})
	codex = run("pre-tool-use", "exec_command", "codex-commit", map[string]string{"cmd": "git commit -m next"})
	if !strings.Contains(codex, `"permissionDecision":"deny"`) || codex != claude {
		t.Fatalf("commit differential Claude=%s Codex=%s", claude, codex)
	}
	if out := run("post-tool-use", "apply_patch", "invalid", map[string]string{"command": "echo *** Add File: garbage"}); out != "" {
		t.Fatalf("invalid patch noise: %s", out)
	}
}

func TestCodexPrePatchSuppliesExistingSourceClaims(t *testing.T) {
	root := adoptedRepo(t, "demo/thing/widget")
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	write(t, filepath.Join(root, "docs/facets/features/demo/thing/widget.yaml"), "schema_version: '0.1'\nid: demo/thing/widget\nlifecycle: active\nintent:\n  summary: Shows the current result.\n")
	write(t, filepath.Join(root, "src/a.go"), "// @facet: demo/thing/widget\n// @facet-role: primary\npackage example\n")
	var in hookInput
	in.CWD = root
	in.SessionID = "pre-patch"
	in.ToolName = "apply_patch"
	in.ToolInput.Command = "*** Begin Patch\n*** Update File: src/a.go\n@@\n+package example\n*** End Patch"
	out := captureHook(t, func() { dispatchPatchHook(newHookCmd(), "pre-tool-use", in) })
	var got hookOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.HookSpecificOutput.HookEventName != "PreToolUse" || !strings.Contains(got.HookSpecificOutput.AdditionalContext, "Shows the current result.") {
		t.Fatalf("pre-patch claims missing: %v %s", err, out)
	}
}

func TestCodexCompactSessionStartReconstructsPersistentRules(t *testing.T) {
	root := adoptedRepo(t, "demo/thing/widget")
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	plugin, _ := filepath.Abs(cliModuleRoot(t))
	payload, _ := json.Marshal(map[string]any{"cwd": root, "session_id": "compact-session", "source": "compact"})
	run := func(shell bool, event string) hookOutput {
		t.Helper()
		var cmd *exec.Cmd
		if shell {
			cmd = exec.Command("sh", filepath.Join(plugin, "scripts/session-start.sh"))
			cmd.Env = append(cmd.Environ(), "CLAUDE_PLUGIN_ROOT="+plugin, "FACET_NO_BINARY=1", "CLAUDE_PROJECT_DIR="+root)
		} else {
			cmd = exec.Command(facetBinary(t), "--root", root, "hook", event)
		}
		cmd.Stdin = strings.NewReader(string(payload))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("hook: %v %s", err, out)
		}
		var got hookOutput
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("one envelope: %v %s", err, out)
		}
		return got
	}
	pre := run(false, "pre-compact")
	compact := run(false, "session-start")
	if compact.HookSpecificOutput.HookEventName != "SessionStart" || compact.HookSpecificOutput.AdditionalContext != pre.HookSpecificOutput.AdditionalContext {
		t.Fatalf("native rule reconstruction differs: %+v %+v", pre, compact)
	}
	if pre.HookSpecificOutput.HookEventName != "PreCompact" {
		t.Fatal("changed Claude event")
	}
	shell := run(true, "session-start")
	if shell.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Fatalf("shell wrong event: %+v", shell)
	}
	for _, text := range []string{"Carrying forward:", "Ground all five Knowledge slots", "Run 'facet lint'", "demo/thing/widget"} {
		if !strings.Contains(shell.HookSpecificOutput.AdditionalContext, text) {
			t.Fatalf("shell lost %q: %+v", text, shell)
		}
	}
}

// Missing absolute paths must stop walking at / instead of looping forever
// after a top-level parent is reduced to the empty string.
func TestCodexShellFallbackMissingOutsidePathsTerminate(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq unavailable")
	}
	root := adoptedRepo(t, "demo/thing/widget")
	plugin, _ := filepath.Abs(cliModuleRoot(t))
	for _, path := range []string{"/facet-review-definitely-absent/path/file.go", "/facet-review-definitely-absent-file.go"} {
		for _, tool := range []string{"apply_patch", "Write"} {
			t.Run(tool+path, func(t *testing.T) {
				input := map[string]string{"file_path": path}
				if tool == "apply_patch" {
					input = map[string]string{"command": "*** Begin Patch\n*** Update File: " + path + "\n@@\n+x\n*** End Patch"}
				}
				payload, _ := json.Marshal(map[string]any{"cwd": root, "tool_name": tool, "tool_input": input})
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, "sh", filepath.Join(plugin, "scripts/validate-record.sh"))
				cmd.WaitDelay = time.Second
				cmd.Stdin = strings.NewReader(string(payload))
				cmd.Env = append(cmd.Environ(), "CLAUDE_PLUGIN_ROOT="+plugin, "FACET_NO_BINARY=1", "CLAUDE_PROJECT_DIR="+root)
				out, err := cmd.CombinedOutput()
				if ctx.Err() != nil {
					t.Fatalf("missing outside path hung: %v", ctx.Err())
				}
				if err != nil || len(out) != 0 {
					t.Fatalf("missing outside path must be quiet: %v %s", err, out)
				}
			})
		}
	}
}

func TestSessionKnowledgeRulesAllowGroundedAnswers(t *testing.T) {
	root := adoptedRepo(t, "demo/thing/widget")
	plugin, _ := filepath.Abs(cliModuleRoot(t))
	for _, shell := range []bool{false, true} {
		for _, event := range []string{"session-start", "pre-compact", "session-start-compact"} {
			t.Run(fmt.Sprintf("shell=%t/%s", shell, event), func(t *testing.T) {
				hookEvent := event
				source := "startup"
				if event == "session-start-compact" {
					hookEvent = "session-start"
					source = "compact"
				}
				payload, _ := json.Marshal(map[string]any{"cwd": root, "session_id": "knowledge-rules", "source": source})
				var cmd *exec.Cmd
				if shell {
					script := "session-start.sh"
					if event == "pre-compact" {
						script = "pre-compact.sh"
					}
					cmd = exec.Command("sh", filepath.Join(plugin, "scripts", script))
					cmd.Env = append(cmd.Environ(), "CLAUDE_PLUGIN_ROOT="+plugin, "FACET_NO_BINARY=1", "CLAUDE_PROJECT_DIR="+root)
				} else {
					cmd = exec.Command(facetBinary(t), "--root", root, "hook", hookEvent)
				}
				cmd.Stdin = strings.NewReader(string(payload))
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("hook: %v %s", err, out)
				}
				var got hookOutput
				if err := json.Unmarshal(out, &got); err != nil {
					t.Fatalf("valid envelope: %v %s", err, out)
				}
				rules := got.HookSpecificOutput.AdditionalContext
				for _, required := range []string{"Ground all five Knowledge slots", "Draft supported answers", "identify inferred answers", "human review", "only when evidence", "missing owner input outside the YAML", "Never invent owner facts"} {
					if !strings.Contains(rules, required) {
						t.Errorf("missing %q in %s", required, rules)
					}
				}
				for _, forbidden := range []string{"leave them empty", "Never invent answers for owner-supplied", "Never invent answers for the Knowledge facet's owner-supplied"} {
					if strings.Contains(rules, forbidden) {
						t.Errorf("conflicting instruction %q in %s", forbidden, rules)
					}
				}
			})
		}
	}
}
