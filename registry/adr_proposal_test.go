// @facet-ignore-file
package facet

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func proposalFixture(t *testing.T, body string) (string, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "docs/adr")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "0001-first.md")
	if err := os.WriteFile(path, []byte(body), 0640); err != nil {
		t.Fatal(err)
	}
	return root, path
}

// Mutation: overwrite the source while generating the proposal. A proposal is
// inspection only; both exact bytes and mode must survive its creation.
func TestADRStatusProposalPreservesSourceAndOpaqueSuffix(t *testing.T) {
	original := []byte("# ADR 0001 — First\r\n\r\n**Status:** accepted, 2026-09-20\r\n\r\n---\r\n\r\nBody stays.\r\n")
	root, path := proposalFixture(t, string(original))
	proposal, err := ProposeADRStatus(root, "0001-first.md", "rejected", "Review found a better option.", "")
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Achieved || proposal.Before != "**Status:** accepted, 2026-09-20" || proposal.After != "**Status:** rejected, 2026-09-20" || proposal.Diff == "" {
		t.Fatalf("wrong proposal: %+v", proposal)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatal("proposal changed source bytes")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0640 {
		t.Fatal("proposal changed source permissions")
	}
}

// Mutation: invent a status or silently choose one of duplicate status lines.
func TestADRStatusProposalRejectsUnknownOrAmbiguousHeaders(t *testing.T) {
	for _, body := range []string{"# ADR\n---\n**Status:** accepted\n", "# ADR\n**Status:** approved\n---\n", "# ADR\n**Status:** accepted\n**Status:** rejected\n---\n"} {
		root, _ := proposalFixture(t, body)
		if _, err := ProposeADRStatus(root, "0001-first.md", "rejected", "why", ""); err == nil {
			t.Fatalf("ambiguous header accepted: %s", body)
		}
	}
	root, _ := proposalFixture(t, "# ADR\n**Status:** accepted\n---\n")
	if _, err := ProposeADRStatus(root, "0001-first.md", "approved", "why", ""); err == nil {
		t.Fatal("invented target status accepted")
	}
	if _, err := ProposeADRStatus(root, "0001-first.md", "rejected", "", ""); err == nil {
		t.Fatal("unexplained transition accepted")
	}
}

// Mutation: emit a new proposal for an already attained state, or allow an
// superseded transition without a real different successor document.
func TestADRStatusProposalNoopAndSuccessor(t *testing.T) {
	root, _ := proposalFixture(t, "# ADR\n**Status:** accepted\n---\n")
	noop, err := ProposeADRStatus(root, "0001-first.md", "accepted", "", "")
	if err != nil || !noop.Achieved || noop.Diff != "" {
		t.Fatalf("already attained target produced a proposal: %+v %v", noop, err)
	}
	for _, successor := range []string{"", "0001-first.md", "0002-missing.md", "../outside.md"} {
		if _, err := ProposeADRStatus(root, "0001-first.md", "superseded", "why", successor); err == nil {
			t.Fatalf("invalid successor accepted: %s", successor)
		}
	}
	os.WriteFile(filepath.Join(root, "docs/adr/0002-next.md"), []byte("# ADR 0002 — Next\n**Status:** accepted\n"), 0644)
	proposal, err := ProposeADRStatus(root, "0001-first.md", "superseded", "why", "0002-next.md")
	if err != nil || proposal.Successor != "docs/adr/0002-next.md" {
		t.Fatalf("existing successor refused: %+v %v", proposal, err)
	}
}

// Mutation: bypass the established repo.read containment checks. Outside paths,
// escaping symlinks and an ADR directory redirected outside root must fail.
func TestADRStatusProposalUsesReaderPathBoundary(t *testing.T) {
	root, path := proposalFixture(t, "# ADR\n**Status:** accepted\n---\n")
	outside := t.TempDir()
	external := filepath.Join(outside, "outside.md")
	if err := os.WriteFile(external, []byte("# ADR\n**Status:** accepted\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "docs/adr/link.md")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../outside.md", "/outside.md", "link.md"} {
		if _, err := ProposeADRStatus(root, name, "rejected", "why", ""); err == nil {
			t.Fatalf("escaping path accepted: %s", name)
		}
	}
	otherRoot := t.TempDir()
	os.MkdirAll(filepath.Join(otherRoot, "docs"), 0755)
	if err := os.Symlink(outside, filepath.Join(otherRoot, "docs/adr")); err != nil {
		t.Fatal(err)
	}
	if _, err := ProposeADRStatus(otherRoot, "outside.md", "rejected", "why", ""); err == nil {
		t.Fatal("external ADR directory accepted")
	}
	original, err := os.ReadFile(path)
	if err != nil || string(original) != "# ADR\n**Status:** accepted\n---\n" {
		t.Fatal("path refusal changed source")
	}
}

// Mutation: remove the fence/comment refusal from header recognition. A status
// written only in an example would become a proposed change to ADR metadata.
func TestADRStatusProposalRefusesExampleAndCommentHeaders(t *testing.T) {
	root, _ := proposalFixture(t, "# ADR\n**Status:** accepted\n---\n\n```markdown\n**Status:** rejected\n```\n")
	positive, err := ProposeADRStatus(root, "0001-first.md", "rejected", "why", "")
	if err != nil || positive.Before != "**Status:** accepted" {
		t.Fatalf("actual metadata fixture failed: %+v %v", positive, err)
	}
	for _, body := range []string{
		"# ADR\n\n```markdown\n**Status:** accepted\n```\n\n---\nBody\n",
		"# ADR\n\n~~~markdown\n**Status:** accepted\n~~~\n\n---\nBody\n",
		"# ADR\n<!--\n**Status:** accepted\n-->\n---\nBody\n",
	} {
		root, path := proposalFixture(t, body)
		if _, err := ProposeADRStatus(root, "0001-first.md", "rejected", "why", ""); err == nil {
			t.Fatalf("example/comment became metadata: %s", body)
		}
		after, err := os.ReadFile(path)
		if err != nil || string(after) != body {
			t.Fatal("refusing an example changed source")
		}
	}
}

func TestADRProposalConfiguredDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "architecture", "decisions")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ConfigName), []byte("adr:\n  directory: architecture/decisions\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0001-first.md", "0002-next.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("# ADR\n**Status:** accepted\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	proposal, err := ProposeADRStatus(root, "architecture/decisions/0001-first.md", "superseded", "replacement", "architecture/decisions/0002-next.md")
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Path != "architecture/decisions/0001-first.md" || proposal.Successor != "architecture/decisions/0002-next.md" || !strings.Contains(proposal.Diff, "architecture/decisions/0001-first.md") {
		t.Fatalf("unexpected proposal: %+v", proposal)
	}
}

// Every German status an ADR was written in before rc.1 is read as its English
// value, in both places a status appears: on disk and as the requested target.
// The proposal always writes English.
func TestADRStatusGermanAliasesMapToEnglish(t *testing.T) {
	want := map[string]string{
		"vorgeschlagen": "proposed",
		"angenommen":    "accepted",
		"abgelöst":      "superseded",
		"verworfen":     "rejected",
	}
	if len(ADRStatusAliases) != len(want) {
		t.Fatalf("aliases = %v, want exactly %v", ADRStatusAliases, want)
	}
	for german, english := range want {
		t.Run(german+" maps to "+english, func(t *testing.T) {
			if got := NormalizeADRStatus(german); got != english {
				t.Fatalf("NormalizeADRStatus(%q) = %q, want %q", german, got, english)
			}
			if got := NormalizeADRStatus(strings.ToUpper(german[:1]) + german[1:]); got != english {
				t.Fatalf("capitalised alias %q = %q, want %q", german, got, english)
			}

			// German on disk, English target: read as the English value.
			root, _ := proposalFixture(t, "# ADR\n**Status:** "+german+" (2026-09-20)\n---\n")
			noop, err := ProposeADRStatus(root, "0001-first.md", english, "", "")
			if err != nil || !noop.Achieved || noop.Current != english {
				t.Fatalf("German %q on disk is not read as %q: %+v %v", german, english, noop, err)
			}

			// English on disk, German target: the target is normalised and
			// the proposal writes English.
			other := "rejected"
			if english == "rejected" {
				other = "accepted"
			}
			root, _ = proposalFixture(t, "# ADR\n**Status:** "+other+" (2026-09-20)\n---\n")
			successor := ""
			if english == "superseded" {
				successor = "0002-next.md"
				if err := os.WriteFile(filepath.Join(root, "docs/adr/0002-next.md"), []byte("# ADR\n**Status:** accepted\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			proposal, err := ProposeADRStatus(root, "0001-first.md", german, "why", successor)
			if err != nil {
				t.Fatal(err)
			}
			if proposal.Target != english || proposal.After != "**Status:** "+english+" (2026-09-20)" {
				t.Fatalf("German target %q did not write %q: %+v", german, english, proposal)
			}
		})
	}
}

// A German word on disk and a different target: the rewritten line carries the
// English value and never the alias it replaced.
func TestADRStatusProposalRewritesAGermanStatusInEnglish(t *testing.T) {
	root, _ := proposalFixture(t, "# ADR\n**Status:** angenommen, 2026-09-20\n---\n")
	proposal, err := ProposeADRStatus(root, "0001-first.md", "rejected", "why", "")
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Current != "accepted" || proposal.Before != "**Status:** angenommen, 2026-09-20" || proposal.After != "**Status:** rejected, 2026-09-20" {
		t.Fatalf("wrong proposal over a German status: %+v", proposal)
	}
	if !strings.Contains(proposal.Diff, "-**Status:** angenommen, 2026-09-20\n+**Status:** rejected, 2026-09-20\n") {
		t.Fatalf("diff does not replace the German line:\n%s", proposal.Diff)
	}
}

// The refusal names the English values, so a user learns what to type.
func TestADRStatusRefusalNamesTheEnglishValues(t *testing.T) {
	root, _ := proposalFixture(t, "# ADR\n**Status:** accepted\n---\n")
	_, err := ProposeADRStatus(root, "0001-first.md", "approved", "why", "")
	if err == nil || !strings.Contains(err.Error(), "proposed, accepted, superseded or rejected") {
		t.Fatalf("refusal = %v, want it to name the English values", err)
	}
}
