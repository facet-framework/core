// @facet-ignore-file

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// Mutations: remove the Read dispatch; omit lifecycle/role filtering; omit the
// reset on pre-compact or session-start; replace the per-file digest key by ID.
// Each mutation builds and changes the real binary's hook output.
func TestReadContextBinaryBeforeEditAndAfterContextReset(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "facet")
	build := exec.Command("go", "build", "-o", binary, cliCommandPackage(t))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	root := adoptedRepo(t, "demo/thing/widget")
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	t.Setenv("CLAUDE_PROJECT_DIR", adoptedRepo(t, "wrong/tree/widget"))
	record := filepath.Join(root, "docs/facets/features/demo/thing/widget.yaml")
	write(t, record, "schema_version: \"0.1\"\nid: demo/thing/widget\nlifecycle: active\nintent:\n  summary: Keeps the user's promise.\ncontract:\n  scenarios:\n    - id: promised-result\n      statement: Given input When read Then output\n")
	source := filepath.Join(root, "src/main.go")
	write(t, source, "// @facet: demo/thing/widget\n// @facet-role: related\npackage example\n")
	run := func(event, tool, path string) string {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"session_id": "read-context", "cwd": root, "tool_name": tool, "tool_input": map[string]any{"file_path": path}})
		cmd := exec.Command(binary, "hook", event)
		cmd.Stdin = strings.NewReader(string(payload))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("hook: %v %s", err, out)
		}
		return string(out)
	}
	assertContext := func(out string) {
		t.Helper()
		var envelope hookOutput
		if err := json.Unmarshal([]byte(out), &envelope); err != nil {
			t.Fatalf("expected JSON context: %s (%v)", out, err)
		}
		c := envelope.HookSpecificOutput.AdditionalContext
		if envelope.HookSpecificOutput.HookEventName != "PostToolUse" || !strings.Contains(c, "Keeps the user's promise.") || !strings.Contains(c, "promised-result") {
			t.Fatalf("missing Read claims: %s", out)
		}
	}
	assertContext(run("post-tool-use", "Read", source))
	if out := run("post-tool-use", "Read", source); out != "" {
		t.Fatalf("duplicate: %s", out)
	}
	other := filepath.Join(root, "src/other.go")
	write(t, other, "// @facet: demo/thing/widget\n// @facet-role: primary\npackage example\n")
	assertContext(run("post-tool-use", "Read", other))
	run("pre-compact", "", "")
	assertContext(run("post-tool-use", "Read", source))
	run("session-start", "", "")
	assertContext(run("post-tool-use", "Read", source))
	run("session-start", "", "")
	t.Setenv("FACET_BINARY", binary)
	t.Setenv("CLAUDE_PLUGIN_ROOT", cliModuleRoot(t))
	payload, _ := json.Marshal(map[string]any{"session_id": "read-context", "cwd": root, "tool_name": "Read", "tool_input": map[string]any{"file_path": source}})
	script := exec.Command("sh", filepath.Join(cliModuleRoot(t), "scripts/read-context.sh"))
	script.Stdin = strings.NewReader(string(payload))
	out, err := script.CombinedOutput()
	if err != nil {
		t.Fatalf("wired script: %v %s", err, out)
	}
	assertContext(string(out))
	// A Read of a broken record must never use the write-validation handler.
	write(t, record, "schema_version: broken\n")
	if out := run("post-tool-use", "Read", record); out != "" {
		t.Fatalf("record Read validated: %s", out)
	}
	for _, life := range []string{"retired", "deprecated", "planned"} {
		write(t, record, "schema_version: \"0.1\"\nid: demo/thing/widget\nlifecycle: "+life+"\nintent:\n  summary: Keeps the user's promise.\ncontract:\n  scenarios:\n    - id: promised-result\n")
		run("session-start", "", "")
		out := run("post-tool-use", "Read", source)
		if life == "planned" {
			assertContext(out)
		} else if out != "" {
			t.Fatalf("%s context: %s", life, out)
		}
	}
	write(t, source, "// @facet: demo/thing/widget\n// @facet-role: config\npackage example\n")
	run("session-start", "", "")
	if out := run("post-tool-use", "Read", source); out != "" {
		t.Fatalf("config-role context: %s", out)
	}
}

// Mutation: remove the PostToolUse Read matcher or attach validate-record.sh.
func TestReadContextWiringSeparatesReadFromWrites(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(cliModuleRoot(t), "hooks/hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Hooks map[string][]struct {
			Matcher string
			Hooks   []struct{ Command string }
		}
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, hook := range config.Hooks["PostToolUse"] {
		if hook.Matcher != "Read" {
			continue
		}
		found = true
		if len(hook.Hooks) != 1 || !strings.Contains(hook.Hooks[0].Command, "read-context.sh") {
			t.Fatalf("Read wiring: %+v", hook)
		}
	}
	if !found {
		t.Fatal("PostToolUse Read context is not wired")
	}
	gate := false
	for _, hook := range config.Hooks["PreToolUse"] {
		if hook.Matcher == "Read" {
			for _, command := range hook.Hooks {
				if strings.Contains(command.Command, "read-gate.sh") {
					gate = true
				}
			}
		}
	}
	if !gate {
		t.Fatal("existing PreToolUse Read gate is missing")
	}
}

// Mutations: remove the fallback notice, or let the fallback pretend to deliver
// a digest. The anchored fixture first proves that a notice actually arrives.
func TestReadContextShellNamesItsMissingCapability(t *testing.T) {
	t.Setenv("FACET_NO_BINARY", "1")
	t.Setenv("CLAUDE_PLUGIN_ROOT", cliModuleRoot(t))
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	root := adoptedRepo(t, "demo/thing/widget")
	source := filepath.Join(root, "src/main.go")
	write(t, source, "// @facet: demo/thing/widget\npackage example\n")
	run := func(path string) string {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"session_id": "fallback", "cwd": root, "tool_name": "Read", "tool_input": map[string]any{"file_path": path}})
		cmd := exec.Command("sh", filepath.Join(cliModuleRoot(t), "scripts/read-context.sh"))
		cmd.Stdin = strings.NewReader(string(payload))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fallback: %v %s", err, out)
		}
		return string(out)
	}
	out := run(source)
	if !strings.Contains(out, "unavailable without the facet binary") {
		t.Fatalf("missing limitation: %s", out)
	}
	if out := run(source); out != "" {
		t.Fatalf("repeated limitation: %s", out)
	}
	plain := filepath.Join(root, "src/plain.go")
	write(t, plain, "package example\n")
	if out := run(plain); out != "" {
		t.Fatalf("unanchored file: %s", out)
	}
}

// Mutation: remove the aggregate context budget or inject the full raw record.
func TestReadContextDoesNotInjectAnUnboundedRecordSet(t *testing.T) {
	root := adoptedRepo(t, "demo/thing/feature-0")
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	var source strings.Builder
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("demo/thing/feature-%d", i)
		record := "schema_version: \"0.1\"\nid: " + id + "\nlifecycle: active\nintent:\n  summary: " + strings.Repeat("claim ", 200) + "\ncontract:\n  scenarios:\n    - id: promised-result\nknowledge:\n  secret: RAW-KNOWLEDGE-MUST-NOT-BE-INJECTED\n"
		if !strings.Contains(record, "RAW-KNOWLEDGE-MUST-NOT-BE-INJECTED") {
			t.Fatal("fixture has no excluded record content")
		}
		write(t, filepath.Join(root, "docs/facets/features", id+".yaml"), record)
		fmt.Fprintf(&source, "// @facet: %s\n// @facet-role: primary\nvar value%d = %d\n", id, i, i)
	}
	path := filepath.Join(root, "src/claims.go")
	write(t, path, source.String())
	var in hookInput
	in.SessionID = "budget"
	in.CWD = root
	in.ToolInput.FilePath = path
	out := captureHook(t, func() { hookReadContext(newHookContext(root), in) })
	var envelope hookOutput
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("JSON: %v %s", err, out)
	}
	c := envelope.HookSpecificOutput.AdditionalContext
	if len(c) > 8192 || !strings.Contains(c, "Further anchored claims omitted") || !strings.Contains(c, "promised-result") || strings.Contains(c, "RAW-KNOWLEDGE") {
		t.Fatalf("unbounded or unhelpful digest (%d bytes): %s", len(c), c)
	}
}

// Mutations: remove the Read-specific UTF-8 normalisation before budgeting, or
// remove the root-containment guard. Both changes build and fail real hook JSON.
func TestReadContextBinaryRejectsForeignFilesAndKeepsUnicodeBudget(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "facet")
	if out, err := exec.Command("go", "build", "-o", binary, cliCommandPackage(t)).CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	root := adoptedRepo(t, "demo/thing/feature-0")
	t.Setenv("CLAUDE_PROJECT_DIR", root)
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	run := func(path, session string) string {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"cwd": root, "session_id": session, "tool_name": "Read", "tool_input": map[string]any{"file_path": path}})
		cmd := exec.Command(binary, "hook", "post-tool-use")
		cmd.Stdin = strings.NewReader(string(payload))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("hook: %v %s", err, out)
		}
		return string(out)
	}
	var source strings.Builder
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("demo/thing/feature-%d", i)
		// The old 280-byte condense boundary falls inside this first four-byte rune.
		intent := strings.Repeat("a", 278) + strings.Repeat("😀", 100)
		if utf8.ValidString(intent[:280]) || !strings.Contains(intent, "😀") {
			t.Fatal("fixture misses the unicode boundary")
		}
		write(t, filepath.Join(root, "docs/facets/features", id+".yaml"), "schema_version: \"0.1\"\nid: "+id+"\nlifecycle: active\nintent:\n  summary: "+intent+"\ncontract:\n  scenarios:\n    - id: promisex\n")
		fmt.Fprintf(&source, "// @facet: %s\n// @facet-role: primary\nvar value%d = %d\n", id, i, i)
	}
	path := filepath.Join(root, "src/many.go")
	write(t, path, source.String())
	t.Run("unicode-budget", func(t *testing.T) {
		var out hookOutput
		if err := json.Unmarshal([]byte(run(path, "unicode")), &out); err != nil {
			t.Fatal(err)
		}
		c := out.HookSpecificOutput.AdditionalContext
		if !strings.Contains(c, "promisex") || !strings.Contains(c, "Further anchored claims omitted") {
			t.Fatalf("claims missing: %s", c)
		}
		if len(c) > 8192 || strings.Contains(c, "�") {
			t.Fatalf("decoded Unicode digest has %d bytes or replacement runes", len(c))
		}
	})
	t.Run("foreign-unadopted-repository", func(t *testing.T) {
		foreign := t.TempDir()
		if out, err := exec.Command("git", "-C", foreign, "init", "-q").CombinedOutput(); err != nil {
			t.Fatalf("fixture git: %v %s", err, out)
		}
		external := filepath.Join(foreign, "foreign.go")
		write(t, external, "// @facet: demo/thing/feature-0\n// @facet-role: primary\npackage foreign\n")
		if !isFileOnDisk(filepath.Join(root, "docs/facets/features/demo/thing/feature-0.yaml")) {
			t.Fatal("matching cwd record missing")
		}
		if out := run(external, "foreign"); out != "" {
			t.Fatalf("foreign file received cwd claims: %s", out)
		}
	})
}
