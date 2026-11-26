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
)

// TestSessionRulesAskForDecisionsAndEdgeCases checks that every phase that
// states the working rules asks for decision records with their reason and
// for one contract scenario per edge case. An adopted registry without any
// record is included: that is where a new project writes its first records,
// and a silent session start there would never deliver the rules.
func TestSessionRulesAskForDecisionsAndEdgeCases(t *testing.T) {
	plugin, _ := filepath.Abs(cliModuleRoot(t))
	empty := adoptedRepo(t, "demo/thing/widget")
	if err := os.Remove(filepath.Join(empty, "docs/facets/features/demo/thing/widget.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(empty, "docs/facets/features")); err != nil {
		t.Fatalf("the empty registry must still be adopted: %v", err)
	}
	roots := map[string]string{"records": adoptedRepo(t, "demo/thing/widget"), "empty": empty}
	for name, root := range roots {
		for _, shell := range []bool{false, true} {
			for _, event := range []string{"session-start", "pre-compact", "session-start-compact"} {
				t.Run(fmt.Sprintf("%s/shell=%t/%s", name, shell, event), func(t *testing.T) {
					hookEvent, source := event, "startup"
					if event == "session-start-compact" {
						hookEvent, source = "session-start", "compact"
					}
					payload, _ := json.Marshal(map[string]any{"cwd": root, "session_id": "decision-rules-" + name, "source": source})
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
					rules := strings.Join(strings.Fields(got.HookSpecificOutput.AdditionalContext), " ")
					for _, required := range []string{
						"Record each design or architecture decision as a decision record in the ADR directory",
						"its context, the decision and the reason for it",
						"'intent.decision_records'",
						"Write each edge case as its own contract scenario",
						"Ground all five Knowledge slots",
					} {
						if !strings.Contains(rules, required) {
							t.Errorf("missing %q in %s", required, rules)
						}
					}
					if name == "empty" && event == "session-start" && !strings.Contains(rules, "docs/facets/features/<domain>/<capability>/<feature>.yaml") {
						t.Errorf("empty registry start must name the record layout: %s", rules)
					}
				})
			}
		}
	}
}
