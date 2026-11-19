package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/facet-framework/core/release"
	"github.com/spf13/cobra"
)

// anchorLine builds an anchor comment at run time, so this test file carries
// no anchor of its own for the registry scanner to find.
func anchorLine(id string) string { return "// @" + "facet: " + id + "\n" }

// pinnedProject is a project with one record and, unless pin is empty, a
// .facetrc.yaml pinning that Core version.
func pinnedProject(t *testing.T, pin string) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml"),
		"schema_version: \"0.1\"\nid: billing/invoice/list\nlifecycle: planned\n")
	if pin != "" {
		write(t, filepath.Join(root, ".facetrc.yaml"), "requires:\n  core: \""+pin+"\"\n")
	}
	return root
}

// runTree runs the real command tree through Run, the function main hands to
// os.Exit, and returns the exit status with everything written to stdout and
// stderr — including what Run itself prints for a failed command, and what
// commands print to the process streams rather than to the command's writer.
func runTree(t *testing.T, version string, extra []*cobra.Command, args ...string) (int, string, string) {
	t.Helper()
	identity := release.Resolve(version, "", "", "", "", "")
	cmd := NewCommand(Options{Identity: &identity, Commands: extra})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(args)
	var code int
	var stdout string
	stderr := captureStream(t, &os.Stderr, func() {
		stdout = captureStream(t, &os.Stdout, func() { code = Run(cmd) })
	})
	return code, out.String() + stdout, stderr
}

// captureStream redirects one process stream while run executes. The pipe is
// drained concurrently, so a large write cannot block the command.
func captureStream(t *testing.T, stream **os.File, run func()) string {
	t.Helper()
	old := *stream
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	*stream = w
	read := make(chan []byte)
	go func() {
		out, _ := io.ReadAll(r)
		read <- out
	}()
	run()
	w.Close()
	*stream = old
	return string(<-read)
}

func TestAPinMismatchStopsACommandBeforeItWorks(t *testing.T) {
	// Premise: the same command in the same project does work when the pin is met.
	root := pinnedProject(t, "v0.1.0")
	code, out, stderr := runTree(t, "0.1.0", nil, "--root", root, "registries")
	if code != 0 || !strings.Contains(out, "1 feature record(s)") {
		t.Fatalf("matching pin: exit %d, stdout %q, stderr %q", code, out, stderr)
	}

	code, out, stderr = runTree(t, "0.2.0", nil, "--root", root, "registries")
	if code == 0 {
		t.Fatalf("mismatch exited 0, stdout %q", out)
	}
	if out != "" {
		t.Fatalf("the command did work despite the mismatch: %q", out)
	}
	install := release.CurrentInstall()
	for _, part := range []string{
		filepath.Join(root, ".facetrc.yaml"),
		"requires FACET Core 0.1.0",
		"this is Core 0.2.0",
		"install origin: " + install.Origin,
		install.Path,
	} {
		if !strings.Contains(stderr, part) {
			t.Errorf("stderr lacks %q:\n%s", part, stderr)
		}
	}
	// The test binary is neither in node_modules nor beside an install.json.
	if install.Origin != release.OriginUnmanaged || !strings.Contains(stderr, "point FACET_BINARY at a Core 0.1.0 build") {
		t.Errorf("want the unmanaged remedy for origin %q:\n%s", install.Origin, stderr)
	}
}

func TestADevelopmentBuildDoesNotSatisfyItsReleasePin(t *testing.T) {
	root := pinnedProject(t, "0.1.0")
	code, _, stderr := runTree(t, "0.1.0-dev.1794528000+gabcdef123456", nil, "--root", root, "registries")
	if code == 0 || !strings.Contains(stderr, "this is Core 0.1.0-dev.1794528000+gabcdef123456") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}

func TestAnInvalidPinIsAConfigurationError(t *testing.T) {
	for _, tc := range []struct{ name, config, mention string }{
		{"caret", "requires:\n  core: \"^0.1.0\"\n", `requires.core "^0.1.0" is not a SemVer version`},
		{"two parts", "requires:\n  core: \"0.1\"\n", `requires.core "0.1" is not a SemVer version`},
		{"empty", "requires:\n  core: \"\"\n", `requires.core "" is not a SemVer version`},
		{"typo key", "requires:\n  cor: \"0.1.0\"\n", "requires.cor is not a known key"},
		{"number", "requires:\n  core: 1.0\n", "not 1.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := pinnedProject(t, "")
			write(t, filepath.Join(root, ".facetrc.yaml"), tc.config)
			code, out, stderr := runTree(t, "0.1.0", nil, "--root", root, "registries")
			if code == 0 || out != "" {
				t.Fatalf("exit %d, stdout %q", code, out)
			}
			for _, part := range []string{filepath.Join(root, ".facetrc.yaml"), tc.mention} {
				if !strings.Contains(stderr, part) {
					t.Errorf("stderr lacks %q:\n%s", part, stderr)
				}
			}
		})
	}
}

// Each exempt command class keeps working with a pin the running build does
// not meet; a command is exempt only by opting in, never by its name.
func TestRepairCommandsRunDespiteAPinMismatch(t *testing.T) {
	root := pinnedProject(t, "9.9.9")
	// Premise: this project really stops an ordinary command.
	if code, _, _ := runTree(t, "0.1.0", nil, "--root", root, "registries"); code == 0 {
		t.Fatal("premise: the mismatch does not stop an ordinary command")
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "version", args: []string{"version"}, want: "facet core 0.1.0"},
		{name: "help command", args: []string{"help", "lint"}, want: "Usage:"},
		{name: "help flag", args: []string{"lint", "--help"}, want: "Usage:"},
		{name: "version flag", args: []string{"--version"}, want: "0.1.0"},
		{name: "completion script", args: []string{"completion", "bash"}, want: "bash completion"},
		{name: "shell completion request", args: []string{"__complete", "li"}, want: "lint"},
		{name: "setup subcommand", args: []string{"setup", "status"}, want: "lint.strict"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, stderr := runTree(t, "0.1.0", nil, append([]string{"--root", root}, tc.args...)...)
			if code != 0 || !strings.Contains(out, tc.want) {
				t.Fatalf("exit %d, stdout %q, stderr %q", code, out, stderr)
			}
		})
	}
}

// An application or product command called init is checked like any other.
func TestACommandNamedInitIsNotExempt(t *testing.T) {
	root := pinnedProject(t, "9.9.9")
	ran := false
	newProduct := func() *cobra.Command {
		product := &cobra.Command{Use: "product"}
		product.AddCommand(&cobra.Command{Use: "init", RunE: func(*cobra.Command, []string) error { ran = true; return nil }})
		return product
	}
	// Premise: the command runs when the pin is met.
	if code, _, stderr := runTree(t, "9.9.9", []*cobra.Command{newProduct()}, "--root", root, "product", "init"); code != 0 || !ran {
		t.Fatalf("premise: product init did not run with a met pin: exit %d, %s", code, stderr)
	}
	ran = false
	code, _, stderr := runTree(t, "0.1.0", []*cobra.Command{newProduct()}, "--root", root, "product", "init")
	if code == 0 || ran || !strings.Contains(stderr, "requires FACET Core 9.9.9") {
		t.Fatalf("product init ran under a mismatch: exit %d, ran %v, stderr %q", code, ran, stderr)
	}
}

// The pin is project-level: a project with several registries is refused once
// and reports one pin.
func TestAMultiRegistryProjectIsPinnedOnce(t *testing.T) {
	root := t.TempDir()
	for _, product := range []string{"a", "b"} {
		write(t, filepath.Join(root, "products", product, "docs/facets/features/billing/invoice/list.yaml"),
			"schema_version: \"0.1\"\nid: billing/invoice/list\nlifecycle: planned\n")
	}
	registries := "registries:\n  a:\n    records: products/a/docs/facets\n  b:\n    records: products/b/docs/facets\n"
	write(t, filepath.Join(root, ".facetrc.yaml"), registries)
	// Premise: without a pin, both registries are compared.
	if code, out, stderr := runTree(t, "0.1.0", nil, "--root", root, "registries"); code != 0 || !strings.Contains(out, "billing/invoice/list") {
		t.Fatalf("premise: exit %d, stdout %q, stderr %q", code, out, stderr)
	}
	write(t, filepath.Join(root, ".facetrc.yaml"), "requires:\n  core: \"9.9.9\"\n"+registries)
	code, out, stderr := runTree(t, "0.1.0", nil, "--root", root, "registries")
	if code == 0 || out != "" || strings.Count(stderr, "requires FACET Core 9.9.9") != 1 {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, stderr)
	}
	_, out, _ = runTree(t, "0.1.0", nil, "--root", root, "version", "--json")
	if strings.Count(out, `"project_pin"`) != 1 || !strings.Contains(out, `"core":"9.9.9","satisfied":false`) {
		t.Fatalf("version --json: %s", out)
	}
}

func TestVersionJSONReportsInstallAndPin(t *testing.T) {
	type report struct {
		Version string `json:"version"`
		Install *struct {
			Origin string `json:"origin"`
			Path   string `json:"path"`
		} `json:"install"`
		ProjectPin *struct {
			File      string `json:"file"`
			Core      string `json:"core"`
			Satisfied *bool  `json:"satisfied"`
			Error     string `json:"error"`
		} `json:"project_pin"`
	}
	install := release.CurrentInstall()
	for _, tc := range []struct {
		name      string
		config    string // empty: no .facetrc.yaml
		satisfied *bool  // nil with error empty: project_pin must be absent
		error     string
	}{
		{name: "satisfied", config: "requires:\n  core: \"0.1.0\"\n", satisfied: ptr(true)},
		{name: "unsatisfied", config: "requires:\n  core: \"0.2.0\"\n", satisfied: ptr(false)},
		{name: "no pin", config: "conventions: CONVENTIONS.md\n"},
		{name: "no config"},
		{name: "invalid pin", config: "requires:\n  core: \"latest\"\n", error: `requires.core "latest" is not a SemVer version`},
		{name: "unknown key", config: "requires:\n  cor: \"0.1.0\"\n", error: "requires.cor is not a known key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := pinnedProject(t, "")
			if tc.config != "" {
				write(t, filepath.Join(root, ".facetrc.yaml"), tc.config)
			}
			code, out, stderr := runTree(t, "0.1.0", nil, "--root", root, "version", "--json")
			if code != 0 {
				t.Fatalf("exit %d, stderr %q", code, stderr)
			}
			var got report
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			if got.Version != "0.1.0" || got.Install == nil || got.Install.Origin != install.Origin || got.Install.Path != install.Path || got.Install.Path == "" {
				t.Fatalf("identity or install missing: %s", out)
			}
			pin := got.ProjectPin
			switch {
			case tc.satisfied == nil && tc.error == "":
				if pin != nil || strings.Contains(out, "project_pin") {
					t.Fatalf("project_pin must be absent: %s", out)
				}
			case tc.error != "":
				if pin == nil || pin.File != filepath.Join(root, ".facetrc.yaml") || !strings.Contains(pin.Error, tc.error) || pin.Satisfied != nil || pin.Core != "" {
					t.Fatalf("project_pin must carry the error: %s", out)
				}
			default:
				if pin == nil || pin.Satisfied == nil || *pin.Satisfied != *tc.satisfied || pin.File != filepath.Join(root, ".facetrc.yaml") || pin.Core == "" || pin.Error != "" {
					t.Fatalf("project_pin wrong: %s", out)
				}
			}
		})
	}
	// The readable form names the install on its own line.
	_, out, _ := runTree(t, "0.1.0", nil, "version")
	if !strings.Contains(out, "\ninstall: "+install.Origin+" ("+install.Path+")\n") {
		t.Fatalf("human output lacks the install line: %q", out)
	}
}

func ptr(b bool) *bool { return &b }

// runHook runs one hook event through the real command tree and returns the
// decoded response, or nil when the hook printed nothing.
func runHook(t *testing.T, version, root, event string, payload map[string]any) *hookOutput {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	identity := release.Resolve(version, "", "", "", "", "")
	cmd := NewCommand(Options{Identity: &identity})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(bytes.NewReader(raw))
	cmd.SetArgs([]string{"--root", root, "hook", event})
	if code := Run(cmd); code != 0 {
		t.Fatalf("%s failed with exit %d: %s", event, code, out.String())
	}
	if out.Len() == 0 {
		return nil
	}
	dec := json.NewDecoder(&out)
	var response hookOutput
	if err := dec.Decode(&response); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if dec.More() {
		t.Fatalf("more than one response object: %s", out.String())
	}
	return &response
}

const (
	mismatchNotice = "does not match this project's pin"
	invalidNotice  = "this project's Core pin is invalid"
)

func noticeCount(response *hookOutput, notice string) int {
	if response == nil {
		return 0
	}
	return strings.Count(response.HookSpecificOutput.AdditionalContext, notice)
}

func commitPayload(root, session string) map[string]any {
	return map[string]any{
		"hook_event_name": "PreToolUse", "session_id": session, "cwd": root,
		"tool_name": "Bash", "tool_input": map[string]any{"command": "git commit -m wip"},
	}
}

// A hook never fails over a pin: it says so once in its response, and the
// guards it carries keep running. Here the commit gate must still deny.
func TestAHookReportsAPinMismatchOnceAndKeepsItsGuard(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	t.Setenv("FACET_SKIP_COMMIT_GATE", "")
	root := brokenRegistry(t)
	write(t, filepath.Join(root, ".facetrc.yaml"), "requires:\n  core: \"9.9.9\"\n")
	git(t, root, "add", ".")
	response := runHook(t, "0.1.0", root, "pre-tool-use", commitPayload(root, "pin-session"))
	if response == nil {
		t.Fatal("no response")
	}
	h := response.HookSpecificOutput
	if h.HookEventName != "PreToolUse" || noticeCount(response, mismatchNotice) != 1 ||
		!strings.Contains(h.AdditionalContext, "requires FACET Core 9.9.9, but this is Core 0.1.0") {
		t.Fatalf("pin notice missing or repeated: %+v", h)
	}
	if h.PermissionDecision != "deny" || !strings.Contains(h.PermissionDecisionReason, "billing/invoice/ghost") {
		t.Fatalf("the commit gate stopped guarding: %+v", h)
	}
}

func TestAHookIsSilentAboutAMetPin(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	root := adoptedRepo(t, "billing/invoice/list")
	write(t, filepath.Join(root, ".facetrc.yaml"), "requires:\n  core: \"0.1.0\"\n")
	start := map[string]any{"hook_event_name": "SessionStart", "session_id": "met-session", "cwd": root, "source": "startup"}
	// Premise: the same hook in the same project speaks when the pin is not met.
	if response := runHook(t, "9.9.9", root, "session-start", start); noticeCount(response, mismatchNotice) != 1 {
		t.Fatalf("premise: no notice under a mismatch: %+v", response)
	}
	response := runHook(t, "0.1.0", root, "session-start", start)
	if response == nil || !strings.Contains(response.HookSpecificOutput.AdditionalContext, "FACET record index") {
		t.Fatalf("premise: the session start says nothing at all: %+v", response)
	}
	if noticeCount(response, mismatchNotice) != 0 || noticeCount(response, invalidNotice) != 0 {
		t.Fatalf("a met pin produced a notice: %s", response.HookSpecificOutput.AdditionalContext)
	}
}

// shopRegistry is a project whose records live outside the default layout,
// with a staged dangling anchor the commit gate must refuse.
func shopRegistry(t *testing.T, requires string) string {
	t.Helper()
	root := adoptedRepo(t, "unused/default/record")
	if err := os.RemoveAll(filepath.Join(root, "docs")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "products/shop/docs/facets/features/billing/invoice/list.yaml"),
		"schema_version: \"0.1\"\nid: billing/invoice/list\nlifecycle: active\nintent:\n  summary: |\n    Lists a customer's invoices so they can reconcile their own billing.\ncontract:\n  scenarios:\n    - id: shown\n      statement: |\n        Given invoices\n        Then they are listed\n    - id: empty\n      statement: |\n        Given none\n        Then the empty state is shown\nlineage:\n  owner:\n    team: billing\n  created_at: \"2026-09-11\"\n")
	write(t, filepath.Join(root, "src/list.ts"), anchorLine("billing/invoice/list")+"// @facet-role: primary\nexport const list = 1;\n")
	write(t, filepath.Join(root, "src/ghost.ts"), anchorLine("billing/invoice/ghost")+"// @facet-role: primary\nexport const ghost = 1;\n")
	write(t, filepath.Join(root, ".facetrc.yaml"), requires+"registries:\n  shop:\n    records: products/shop/docs/facets\n    sources: [src]\n")
	git(t, root, "add", ".")
	return root
}

// A broken pin must never silence the hooks of a project with its own layout:
// the session start says the pin is invalid and the commit gate still denies.
func TestAnInvalidPinKeepsTheHooksOfANonDefaultLayout(t *testing.T) {
	t.Setenv("FACET_SKIP_COMMIT_GATE", "")
	// Premise: without requires, this layout's commit gate denies.
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	root := shopRegistry(t, "")
	if response := runHook(t, "0.1.0", root, "pre-tool-use", commitPayload(root, "premise")); response == nil || response.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("premise: the commit gate does not deny: %+v", response)
	}
	for name, requires := range map[string]string{
		"typo key":     "requires:\n  cor: \"9.9.9\"\n",
		"not a semver": "requires:\n  core: \"latest\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
			root := shopRegistry(t, requires)
			start := runHook(t, "0.1.0", root, "session-start", map[string]any{"hook_event_name": "SessionStart", "session_id": "s", "cwd": root, "source": "startup"})
			if noticeCount(start, invalidNotice) != 1 || noticeCount(start, mismatchNotice) != 0 || !strings.Contains(start.HookSpecificOutput.AdditionalContext, filepath.Join(root, ".facetrc.yaml")) {
				t.Fatalf("session start lacks the invalid-pin notice: %+v", start)
			}
			if !strings.Contains(start.HookSpecificOutput.AdditionalContext, "shop") {
				t.Fatalf("session start lost the registry context: %s", start.HookSpecificOutput.AdditionalContext)
			}
			gate := runHook(t, "0.1.0", root, "pre-tool-use", commitPayload(root, "s"))
			if gate == nil || gate.HookSpecificOutput.PermissionDecision != "deny" || !strings.Contains(gate.HookSpecificOutput.PermissionDecisionReason, "billing/invoice/ghost") {
				t.Fatalf("the commit gate stopped guarding: %+v", gate)
			}
		})
	}
}

// Tool-use hooks carry the notice once per session; a session start always
// does, and a different mismatch in the same session is new again.
func TestToolUseHooksCarryThePinNoticeOncePerSession(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	root := adoptedRepo(t, "billing/invoice/list")
	write(t, filepath.Join(root, ".facetrc.yaml"), "requires:\n  core: \"9.9.9\"\n")
	edit := map[string]any{
		"hook_event_name": "PostToolUse", "session_id": "once-session", "cwd": root, "tool_name": "Edit",
		"tool_input": map[string]any{"file_path": filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml")},
	}
	if first := runHook(t, "0.1.0", root, "post-tool-use", edit); noticeCount(first, mismatchNotice) != 1 {
		t.Fatalf("first PostToolUse lacks the notice: %+v", first)
	}
	if second := runHook(t, "0.1.0", root, "post-tool-use", edit); noticeCount(second, mismatchNotice) != 0 {
		t.Fatalf("second PostToolUse repeated the notice: %+v", second)
	}
	start := map[string]any{"hook_event_name": "SessionStart", "session_id": "once-session", "cwd": root, "source": "startup"}
	if response := runHook(t, "0.1.0", root, "session-start", start); noticeCount(response, mismatchNotice) != 1 {
		t.Fatalf("SessionStart after tool use lacks the notice: %+v", response)
	}
	if changed := runHook(t, "0.2.0", root, "post-tool-use", edit); noticeCount(changed, mismatchNotice) != 1 ||
		!strings.Contains(changed.HookSpecificOutput.AdditionalContext, "this is Core 0.2.0") {
		t.Fatalf("a different running version was not reported as new: %+v", changed)
	}
}

// One Codex patch touching two files answers once, with the pin notice once
// and every file's own context kept.
func TestACodexPatchCarriesThePinNoticeOnce(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	root := adoptedRepo(t, "demo/thing/widget")
	write(t, filepath.Join(root, "docs/facets/features/demo/thing/other.yaml"), "schema_version: \"0.1\"\nid: demo/thing/other\nlifecycle: active\n")
	write(t, filepath.Join(root, "src/a.go"), anchorLine("demo/thing/widget")+"package example\n")
	write(t, filepath.Join(root, "src/b.go"), anchorLine("demo/thing/other")+"package example\n")
	write(t, filepath.Join(root, ".facetrc.yaml"), "requires:\n  core: \"9.9.9\"\n")
	patch := map[string]any{
		"hook_event_name": "PostToolUse", "session_id": "patch-session", "cwd": root, "tool_name": "apply_patch",
		"tool_input": map[string]any{"command": "*** Begin Patch\n*** Update File: src/a.go\n*** Update File: src/b.go\n*** End Patch"},
	}
	response := runHook(t, "0.1.0", root, "post-tool-use", patch)
	if noticeCount(response, mismatchNotice) != 1 {
		t.Fatalf("want one pin notice: %+v", response)
	}
	c := response.HookSpecificOutput.AdditionalContext
	if !strings.Contains(c, "demo/thing/widget") || !strings.Contains(c, "demo/thing/other") {
		t.Fatalf("a file's own context was lost: %s", c)
	}
}

// No patch handler denies today, so the merge that every hook response goes
// through is asserted directly: a deny survives beside the notice.
func TestMergingHookOutputsKeepsADeny(t *testing.T) {
	var deny, note hookOutput
	deny.HookSpecificOutput.PermissionDecision = "deny"
	deny.HookSpecificOutput.PermissionDecisionReason = "gate reason"
	note.HookSpecificOutput.AdditionalContext = "handler context"
	merged := mergeHookOutputs("PreToolUse", []string{"pin notice"}, []hookOutput{note, deny}).HookSpecificOutput
	if merged.PermissionDecision != "deny" || merged.PermissionDecisionReason != "gate reason" ||
		merged.AdditionalContext != "pin notice\n\nhandler context" || merged.HookEventName != "PreToolUse" {
		t.Fatalf("%+v", merged)
	}
}

// git runs clean and smudge inside 'git add' and 'git checkout'. A pinned
// project with a mismatching binary must get its bytes through exactly as an
// unpinned one does.
func TestGitFiltersPassContentThroughDespiteAPinMismatch(t *testing.T) {
	root := adoptedRepo(t, "billing/invoice/list")
	content := "package x\n\n" + anchorLine("billing/invoice/list") + "func List() {}\n"
	run := func(filter string) (int, string) {
		t.Helper()
		in, err := os.CreateTemp(t.TempDir(), "stdin")
		if err != nil {
			t.Fatal(err)
		}
		defer in.Close()
		if _, err := in.WriteString(content); err != nil {
			t.Fatal(err)
		}
		if _, err := in.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		old := os.Stdin
		os.Stdin = in
		defer func() { os.Stdin = old }()
		code, out, _ := runTree(t, "0.1.0", nil, "--root", root, "filter", filter, "src/list.go")
		return code, out
	}
	unpinned := map[string]string{}
	for _, filter := range []string{"clean", "smudge"} {
		code, out := run(filter)
		if code != 0 || out == "" {
			t.Fatalf("premise: unpinned %s exit %d, stdout %q", filter, code, out)
		}
		unpinned[filter] = out
	}
	write(t, filepath.Join(root, ".facetrc.yaml"), "requires:\n  core: \"9.9.9\"\n")
	if code, _, _ := runTree(t, "0.1.0", nil, "--root", root, "registries"); code == 0 {
		t.Fatal("premise: the mismatch does not stop an ordinary command")
	}
	for _, filter := range []string{"clean", "smudge"} {
		code, out := run(filter)
		if code != 0 || out != unpinned[filter] {
			t.Fatalf("pinned %s: exit %d, stdout %q, want %q", filter, code, out, unpinned[filter])
		}
	}
}

// mcpSession runs 'facet mcp' through the real command tree on the process's
// stdio and returns a function that sends one JSON-RPC message and, for a
// request, returns the response line with that id.
func mcpSession(t *testing.T, version, root string) (func(msg map[string]any) map[string]any, func()) {
	t.Helper()
	t.Setenv("FACET_PLUGINS_DIR", t.TempDir())
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW
	identity := release.Resolve(version, "", "", "", "", "")
	cmd := NewCommand(Options{Identity: &identity})
	cmd.SetArgs([]string{"--root", root, "mcp"})
	done := make(chan int, 1)
	go func() { done <- Run(cmd) }()
	lines := bufio.NewScanner(outR)
	lines.Buffer(make([]byte, 1<<20), 1<<24)
	send := func(msg map[string]any) map[string]any {
		t.Helper()
		msg["jsonrpc"] = "2.0"
		raw, err := json.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inW.Write(append(raw, '\n')); err != nil {
			t.Fatal(err)
		}
		id, isRequest := msg["id"]
		if !isRequest {
			return nil
		}
		// A server that never answers fails the test instead of hanging it.
		if err := outR.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
			t.Fatal(err)
		}
		for lines.Scan() {
			var reply map[string]any
			if err := json.Unmarshal(lines.Bytes(), &reply); err != nil {
				t.Fatalf("%v: %s", err, lines.Text())
			}
			// JSON numbers decode as float64; compare the rendered ids.
			if fmt.Sprint(reply["id"]) == fmt.Sprint(id) {
				return reply
			}
		}
		t.Fatalf("no reply to %v: %v", msg, lines.Err())
		return nil
	}
	stop := func() {
		inW.Close()
		code := <-done
		outW.Close()
		os.Stdin, os.Stdout = oldIn, oldOut
		inR.Close()
		outR.Close()
		if code != 0 {
			t.Errorf("mcp exited %d", code)
		}
	}
	send(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "pin-test", "version": "1"},
	}})
	send(map[string]any{"method": "notifications/initialized", "params": map[string]any{}})
	return send, stop
}

// toolText is the text of a tools/call result and whether it is an error.
func toolText(t *testing.T, reply map[string]any) (string, bool) {
	t.Helper()
	result, ok := reply["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", reply)
	}
	var text strings.Builder
	for _, item := range result["content"].([]any) {
		if s, ok := item.(map[string]any)["text"].(string); ok {
			text.WriteString(s)
		}
	}
	isError, _ := result["isError"].(bool)
	return text.String(), isError
}

// The MCP server starts under an unmet pin and answers every tool call with
// it instead of doing the tool's work; the pin is read again on each call.
func TestTheMCPServerAnswersEveryToolCallWithAnUnmetPin(t *testing.T) {
	root := pinnedProject(t, "9.9.9")
	find := func(id int) map[string]any {
		return map[string]any{"id": id, "method": "tools/call", "params": map[string]any{"name": "feature_find", "arguments": map[string]any{"query": "invoice"}}}
	}
	send, stop := mcpSession(t, "0.1.0", root)
	defer stop()

	text, isError := toolText(t, send(find(2)))
	if !isError || !strings.Contains(text, "requires FACET Core 9.9.9, but this is Core 0.1.0") || strings.Contains(text, "billing/invoice/list") {
		t.Fatalf("mismatch: isError %v, text %q", isError, text)
	}
	write(t, filepath.Join(root, ".facetrc.yaml"), "requires:\n  cor: \"0.1.0\"\n")
	text, isError = toolText(t, send(find(3)))
	if !isError || !strings.Contains(text, invalidNotice) || !strings.Contains(text, "requires.cor is not a known key") {
		t.Fatalf("invalid pin: isError %v, text %q", isError, text)
	}
	// A corrected pin takes effect in the same session, and the tool does its work.
	write(t, filepath.Join(root, ".facetrc.yaml"), "requires:\n  core: \"0.1.0\"\n")
	text, isError = toolText(t, send(find(4)))
	if isError || !strings.Contains(text, "billing/invoice/list") {
		t.Fatalf("met pin: isError %v, text %q", isError, text)
	}
}
