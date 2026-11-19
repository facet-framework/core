// Fixtures below carry example @facet: tags. This file compares the shell
// fallback against the binary rather than implementing a feature.
// @facet-ignore-file

package cli

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/facet-framework/core/registry"
)

// The binary and POSIX shell fallback must emit identical roster lines so
// projects receive the same record context with either implementation.
func TestTheShellFallbackBuildsTheSameRosterAsTheBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fallback is a POSIX shell script")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell here")
	}

	root := t.TempDir()
	write(t, filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml"),
		"schema_version: \"0.1\"\nid: billing/invoice/list\nlifecycle: active\nintent:\n  summary: |\n    Lists a customer's invoices, newest\n    first. The ordering is stable across pages.\nlineage:\n  owner:\n    team: billing\n")
	write(t, filepath.Join(root, "docs/facets/features/access/login/sso.yaml"),
		"schema_version: \"0.1\"\nid: access/login/sso\nlifecycle: planned\nintent:\n  summary: |\n    Single sign-on against the customer's identity provider.\nlineage:\n  owner:\n    team: access\n")
	// A variant record has no top-level lifecycle; both implementations must
	// report the same effective lifecycle for consistent roster output.
	write(t, filepath.Join(root, "docs/facets/features/billing/invoice/export.yaml"),
		"schema_version: \"0.1\"\nid: billing/invoice/export\nvariants:\n  web:\n    lifecycle: active\n  mobile:\n    lifecycle: planned\nintent:\n  summary: |\n    Exports invoices as a file.\nlineage:\n  owner:\n    team: billing\n")

	plugin, err := filepath.Abs(cliModuleRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", ". \""+plugin+"/scripts/hook-lib.sh\"; facet_record_index \""+root+"\"")
	cmd.Env = append(cmd.Environ(), "CLAUDE_PLUGIN_ROOT="+plugin, "FACET_NO_BINARY=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the fallback failed: %v\n%s", err, out)
	}

	want := rosterLines(facet.RecordIndex([]facet.Registry{facet.Open(root)}, facet.IndexOptions{Summaries: true}))
	got := rosterLines(string(out))

	if len(want) != 3 {
		t.Fatalf("the binary produced %d roster lines, want 3:\n%v", len(want), want)
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the fallback and the binary disagree.\nshell:\n%s\n\nbinary:\n%s",
			strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The roster lines are pinned above; the header is pinned here.
func TestTheShellFallbackAndTheBinaryAgreeOnTheLargeRegistryHeader(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fallback is a POSIX shell script")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell here")
	}

	root := t.TempDir()
	for i := 0; i <= 400; i++ {
		write(t, filepath.Join(root, "docs/facets/features/demo/feature", fmt.Sprintf("%03d.yaml", i)),
			fmt.Sprintf("schema_version: \"0.1\"\nid: demo/feature/%03d\nlifecycle: active\nintent:\n  summary: |\n    One line.\n", i))
	}

	plugin, err := filepath.Abs(cliModuleRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", ". \""+plugin+"/scripts/hook-lib.sh\"; facet_record_index \""+root+"\"")
	cmd.Env = append(cmd.Environ(), "CLAUDE_PLUGIN_ROOT="+plugin, "FACET_NO_BINARY=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the fallback failed: %v\n%s", err, out)
	}
	shell := string(out)
	binary := largeRegistryIndex(newHookContext(root))

	// Assert that both implementations took the fast path; matching rich
	// indexes alone would not verify the fast-path contract.
	for name, text := range map[string]string{"the shell fallback": shell, "the binary": binary} {
		if !strings.Contains(text, "the lines carry ids only") {
			t.Fatalf("%s did not take its fast path:\n%s", name, text)
		}
	}

	for name, text := range map[string]string{"the shell fallback": shell, "the binary": binary} {
		for _, sentence := range []string{
			"docs/facets/features/<id>.yaml",
			"relative to the repository root",
			"Never prefix one with '/'",
		} {
			if !strings.Contains(text, sentence) {
				t.Errorf("%s never says %q in its large-registry index:\n%s", name, sentence, text)
			}
		}
	}
}

// Both binary and fallback obey the byte budget. Test summary truncation and
// record omission separately; roster equality alone cannot bound total bytes.
func TestTheShellFallbackKeepsTheSameByteBudget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fallback is a POSIX shell script")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell here")
	}

	// Long enough that 390 of them do not fit, short enough that the ids alone
	// do. That is the step where summaries go and records stay.
	const summary = "Does a thing a customer can name, for a reason they would recognise, and says so in a sentence long enough to be worth reading twice."

	t.Run("summaries are given up before records", func(t *testing.T) {
		root := t.TempDir()
		for i := 0; i < 390; i++ {
			id := fmt.Sprintf("demo/cap/f%04d", i)
			write(t, filepath.Join(root, "docs/facets/features/demo/cap", fmt.Sprintf("f%04d.yaml", i)),
				"schema_version: \"0.1\"\nid: "+id+"\nlifecycle: active\nintent:\n  summary: |\n    "+summary+"\nlineage:\n  owner:\n    team: demo\n")
		}

		shell := runFallbackIndex(t, root)
		binary := facet.RecordIndex([]facet.Registry{facet.Open(root)}, facet.IndexOptions{Summaries: true})

		// The precondition: this corpus really does overrun the budget with its
		// summaries attached. Without it, two indexes that never had to give
		// anything up would agree and the case would prove nothing.
		unbounded := facet.RecordIndex([]facet.Registry{facet.Open(root)},
			facet.IndexOptions{Summaries: true, MaxBytes: 1 << 30})
		if len(unbounded) <= facet.DefaultIndexBytes {
			t.Fatalf("the fixture fits the budget (%d bytes) and so has nothing to give up", len(unbounded))
		}
		if !strings.Contains(unbounded, summary) {
			t.Fatal("the fixture's summaries were never read, so dropping them proves nothing")
		}

		for name, text := range map[string]string{"the shell fallback": shell, "the binary": binary} {
			if len(text) > facet.DefaultIndexBytes {
				t.Errorf("%s rendered %d bytes, over the %d-byte budget", name, len(text), facet.DefaultIndexBytes)
			}
			if strings.Contains(text, summary) {
				t.Errorf("%s kept its summaries instead of giving them up first", name)
			}
			// Records stay: giving up a summary must not give up the record.
			if !strings.Contains(text, "demo/cap/f0389") {
				t.Errorf("%s dropped records while summaries were still on the table", name)
			}
		}
	})

	t.Run("a dropped record is said out loud", func(t *testing.T) {
		root := t.TempDir()
		for i := 0; i < 1400; i++ {
			id := fmt.Sprintf("demo/cap/feature-with-a-long-enough-name-%04d", i)
			write(t, filepath.Join(root, "docs/facets/features/demo/cap", fmt.Sprintf("feature-with-a-long-enough-name-%04d.yaml", i)),
				"schema_version: \"0.1\"\nid: "+id+"\nlifecycle: active\nintent:\n  summary: |\n    One.\n")
		}

		shell := runFallbackIndex(t, root)
		binary := facet.RecordIndex([]facet.Registry{facet.Open(root)}, facet.IndexOptions{Summaries: true})

		for name, text := range map[string]string{"the shell fallback": shell, "the binary": binary} {
			if len(text) > facet.DefaultIndexBytes {
				t.Errorf("%s rendered %d bytes, over the %d-byte budget", name, len(text), facet.DefaultIndexBytes)
			}
			// The precondition and the claim in one: ids alone do not fit here,
			// so something was left out — and absence must never be readable as
			// non-existence.
			if !strings.Contains(text, "further record(s) are not listed here") {
				t.Errorf("%s truncated without saying so:\n%s", name, tail(text))
			}
		}
	})
}

// runFallbackIndex runs scripts/hook-lib.sh's facet_record_index over a tree.
func runFallbackIndex(t *testing.T, root string) string {
	t.Helper()
	plugin, err := filepath.Abs(cliModuleRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", ". \""+plugin+"/scripts/hook-lib.sh\"; facet_record_index \""+root+"\"")
	cmd.Env = append(cmd.Environ(), "CLAUDE_PLUGIN_ROOT="+plugin, "FACET_NO_BINARY=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the fallback failed: %v\n%s", err, out)
	}
	return string(out)
}

// tail is the last few lines, for an error message that would otherwise be
// the whole index.
func tail(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > 4 {
		lines = lines[len(lines)-4:]
	}
	return strings.Join(lines, "\n")
}

func rosterLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "- ") {
			out = append(out, line)
		}
	}
	return out
}
