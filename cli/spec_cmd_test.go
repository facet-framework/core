package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestSpecLintCommand(t *testing.T) {
	t.Chdir(t.TempDir())
	writeSpec := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(name, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(want int, args ...string) string {
		t.Helper()
		cmd := newRootCmd()
		out := new(bytes.Buffer)
		cmd.SetOut(out)
		cmd.SetArgs(append([]string{"spec", "lint"}, args...))
		if code := Run(cmd); code != want {
			t.Fatalf("exit %d, want %d; output %s", code, want, out)
		}
		return out.String()
	}
	writeSpec("README.md", "---\ntitle: Context\n---\n# Notes\n")
	if got := run(0); got != "checked 0 spec files, skipped 1 markdown files\n" {
		t.Fatalf("empty scan: %s", got)
	}
	writeSpec("discounts.md", "---\nschema: facet-spec/0.1\nid: pricing/discounts\ntitle: Discounts\nstatus: draft\nowner: checkout\n---\n"+
		"```gherkin\n@case:cap\nScenario: Cap\nGiven a cart\n```\n")
	writeSpec("cart.feature", "Feature: Cart\n@case:open\nScenario: Open\nGiven a cart\n")
	if got := run(0); got != "checked 2 spec files, skipped 1 markdown files\n" {
		t.Fatalf("successful scan: %s", got)
	}
	writeSpec("cart.feature", "Feature: Cart\nScenario: Open\nGiven a cart\n")
	if got := run(1); !strings.Contains(got, "cart.feature:2: [exact] scenario needs one @case") ||
		!strings.HasSuffix(got, "checked 2 spec files, skipped 1 markdown files\n") {
		t.Fatalf("failed scan: %s", got)
	}
	got := run(1, "--json", "cart.feature", "README.md")
	var report specLintReport
	if err := json.Unmarshal([]byte(got), &report); err != nil {
		t.Fatalf("decode report: %v, output %s", err, got)
	}
	if report.Checked != 1 || report.Skipped != 1 || len(report.Findings) != 1 ||
		report.Findings[0].File != "cart.feature" || report.Findings[0].Line != 2 ||
		!strings.Contains(report.Findings[0].Message, "needs one @case") {
		t.Fatalf("unexpected JSON report: %s", got)
	}
}
