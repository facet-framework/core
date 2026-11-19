// @facet-ignore-file
package cli

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/facet-framework/core/registry"
)

// Mutation: use the accepted default instead of explicit HEAD for the Bash
// snapshot. A read-only command then emits reminders for committed changes.
func TestBashAndStopUseDifferentBaselines(t *testing.T) {
	root := adoptedRepo(t, "demo/thing/widget")
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	source := filepath.Join(root, "src/widget.go")
	write(t, source, "// @facet: demo/thing/widget\n// @facet-role: primary\npackage example\nconst Value = 1\n")
	commit := func() {
		t.Helper()
		for _, args := range [][]string{{"add", "."}, {"commit", "-m", "fixture"}} {
			if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("fixture: %v %s", err, out)
			}
		}
	}
	commit()
	if _, err := facet.AcceptReview(root, nil, "behavior-unchanged"); err != nil {
		t.Fatal(err)
	}
	write(t, source, "// @facet: demo/thing/widget\n// @facet-role: primary\npackage example\nconst Value = 2\n")
	commit()
	dirty, err := exec.Command("git", "-C", root, "status", "--porcelain").Output()
	if err != nil || len(dirty) != 0 {
		t.Fatalf("fixture is not clean: %s %v", dirty, err)
	}
	if changed := facet.ChangedFiles(root); len(changed) != 1 || changed[0] != "src/widget.go" {
		t.Fatalf("accepted baseline did not retain the committed change: %v", changed)
	}
	run := func(event, tool string) string {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"cwd": root, "session_id": "integration-baselines", "tool_name": tool, "tool_input": map[string]any{"command": "git status"}})
		cmd := exec.Command(facetBinary(t), "--root", root, "hook", event)
		cmd.Stdin = strings.NewReader(string(payload))
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("hook: %v %s", err, output)
		}
		return string(output)
	}
	if output := run("post-tool-use", "Bash"); output != "" {
		t.Fatalf("read-only Bash emitted accepted history: %s", output)
	}
	var stop struct {
		Decision string
		Reason   string
	}
	output := run("stop", "")
	if err := json.Unmarshal([]byte(output), &stop); err != nil || stop.Decision != "block" || !strings.Contains(stop.Reason, "demo/thing/widget") {
		t.Fatalf("Stop lost the pending baseline review: %s %v", output, err)
	}
	if output := run("stop", ""); output != "" {
		t.Fatalf("delivered Stop review repeated: %s", output)
	}
}
