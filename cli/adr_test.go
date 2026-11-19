// @facet: authoring/guardrails/adr-status-proposal
// @facet-role: related
package cli

import (
	"bytes"
	"encoding/json"
	"github.com/facet-framework/core/registry"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Mutation: remove newADRCmd from the real root command, or replace the source
// instead of proposing. The real CLI must expose an applyable diff while the
// caller's branch, index, source bytes and metadata remain unchanged.
func TestBinaryADRStatusProposalIsReadOnlyAndDiffApplies(t *testing.T) {
	for _, directory := range []string{"docs/adr", "architecture/decisions"} {
		t.Run(directory, func(t *testing.T) {
			// The second body keeps the pre-rc.1 German status: the real CLI reads it
			// as its English alias and still proposes the change.
			for _, body := range []string{"# ADR 0001 — First\r\n\r\n**Status:** accepted, 2026-09-20\r\n\r\n---\r\n\r\nBody remains.\r\n", "# ADR 0001 — First\n**Status:** angenommen"} {
				t.Run(body, func(t *testing.T) {
					root := t.TempDir()
					dir := filepath.Join(root, directory)
					if directory != "docs/adr" {
						if err := os.WriteFile(filepath.Join(root, facet.ConfigName), []byte("adr:\n  directory: "+directory+"\n"), 0644); err != nil {
							t.Fatal(err)
						}
					}
					os.MkdirAll(dir, 0755)
					path := filepath.Join(dir, "0001-first.md")
					original := []byte(body)
					os.WriteFile(path, original, 0640)
					git := func(args ...string) []byte {
						t.Helper()
						command := exec.Command("git", append([]string{"-C", root}, args...)...)
						output, err := command.CombinedOutput()
						if err != nil {
							t.Fatalf("git %v: %s %v", args, output, err)
						}
						return output
					}
					git("init", "-q", "-b", "main")
					git("config", "user.name", "fixture")
					git("config", "user.email", "fixture@example.test")
					git("add", ".")
					git("commit", "-qm", "fixture")
					head, index := git("rev-parse", "HEAD"), git("write-tree")
					branch, refs := git("symbolic-ref", "HEAD"), git("for-each-ref", "--format=%(refname) %(objectname)")
					command := exec.Command(facetBinary(t), "--root", root, "adr", "propose-status", directory+"/0001-first.md", "--status", "rejected", "--reason", "Reviewed alternative", "--json")
					output, err := command.Output()
					if err != nil {
						t.Fatal(err)
					}
					var proposal facet.ADRStatusProposal
					if err := json.Unmarshal(output, &proposal); err != nil {
						t.Fatal(err)
					}
					if proposal.Path != directory+"/0001-first.md" || proposal.Diff == "" || proposal.Target != "rejected" || !strings.Contains(proposal.Diff, "+**Status:** rejected") || proposal.Achieved {
						t.Fatalf("missing CLI proposal: %+v", proposal)
					}
					after, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(after, original) || !bytes.Equal(head, git("rev-parse", "HEAD")) || !bytes.Equal(index, git("write-tree")) || len(git("status", "--porcelain")) != 0 {
						t.Fatal("proposal modified its repository")
					}
					if !bytes.Equal(branch, git("symbolic-ref", "HEAD")) || !bytes.Equal(refs, git("for-each-ref", "--format=%(refname) %(objectname)")) {
						t.Fatal("proposal created or changed branches/refs")
					}
					info, err := os.Stat(path)
					if err != nil || info.Mode().Perm() != 0640 {
						t.Fatal("proposal changed source metadata")
					}
					git("checkout", "-qb", "test/proposal")
					apply := exec.Command("git", "-C", root, "apply", "--check")
					apply.Stdin = bytes.NewBufferString(proposal.Diff)
					if output, err := apply.CombinedOutput(); err != nil {
						t.Fatalf("proposed diff cannot apply: %q %s %v", proposal.Diff, output, err)
					}
				})
			}
		})
	}
}
