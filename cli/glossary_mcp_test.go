// Fixtures here build throwaway git repositories and carry example @facet:
// tags; the file demonstrates the agent surface rather than implementing
// anything, so it opts out of scanning.
// @facet-ignore-file

package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/facet-framework/core/registry"
	"github.com/goccy/go-yaml"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tests exercise glossary reads and writes, including conflicting renames,
// commits and term references in project prose.

// categorisedFixture is a glossary with two declared categories, a term in
// each, and one term nobody categorised. The declaration order is deliberately
// not alphabetical: the order of the groups is one of the things under test.
const categorisedFixture = `schema_version: "0.1"
categories:
  - id: parties
    title: Parties
    purpose: The people and organisations a shipment passes between.
  - id: documents
    title: Documents
terms:
  - term: Consignee
    category: parties
    definition: |
      The party named in the transport document who receives the goods.
  - term: Bill of Lading
    category: documents
    definition: |
      The document the carrier issues for goods received.
  - term: Deviation
    definition: |
      A documented defect that is not a promise.
`

// glossaryAgentProject builds a real git repository holding a glossary.
func glossaryAgentProject(t *testing.T, glossary string) facet.RegistrySet {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	root := glossaryCLIProject(t)
	if glossary != "" {
		writeFile(t, filepath.Join(root, "docs/facets", facet.GlossaryName), glossary)
	}
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "test@example.invalid"},
		{"config", "user.name", "Test"},
		{"add", "-A"},
		{"commit", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	return facet.OpenSet(root)
}

// loadWritten reads the glossary as it stands on the branch, which is where a
// write lands. Reading the working-tree copy would pass whatever the tool did.
func loadWritten(t *testing.T, state *glossaryToolState, set facet.RegistrySet, branch string) *facet.Glossary {
	t.Helper()
	session, err := state.glossarySessionFor(set.Primary(), branch)
	if err != nil {
		t.Fatalf("opening the branch: %v", err)
	}
	g, err := facet.LoadGlossary(session.session.GlossaryPath)
	if err != nil {
		t.Fatalf("reading the written glossary: %v", err)
	}
	return g
}

func termIn(g *facet.Glossary, term string) (facet.GlossaryTerm, int) {
	var found facet.GlossaryTerm
	count := 0
	for _, entry := range g.Terms {
		if entry.Term == term {
			found = entry
			count++
		}
	}
	return found, count
}

func TestGlossaryAuthoringIsScopedToTheServerAndRegistry(t *testing.T) {
	first := glossaryAgentProject(t, categorisedFixture)
	second := glossaryAgentProject(t, categorisedFixture)
	firstServer := mcp.NewServer(&mcp.Implementation{Name: "first-glossary", Version: "1"}, nil)
	state := addGlossaryTools(firstServer, first)
	t.Cleanup(state.close)
	const branch = "glossary/shared-name"
	for index, set := range []facet.RegistrySet{first, second} {
		definition := []string{"First project", "Second project"}[index]
		if _, err := state.glossarySetTool(set, glossarySetInput{Branch: branch, Term: "Scoped", Definition: definition}); err != nil {
			t.Fatalf("different repositories may use the same branch: %v", err)
		}
		term, count := termIn(loadWritten(t, state, set, branch), "Scoped")
		if count != 1 || strings.TrimSpace(term.Definition) != definition {
			t.Fatalf("write reached another project's glossary: %+v", term)
		}
		if _, err := state.glossaryCommitTool(set, glossaryCommitInput{Branch: branch, Message: definition}); err != nil {
			t.Fatalf("commit must reach the selected project's glossary: %v", err)
		}
	}
	firstTerm, _ := termIn(loadWritten(t, state, first, branch), "Scoped")
	if strings.TrimSpace(firstTerm.Definition) != "First project" {
		t.Fatal("second project's write replaced the first project's definition")
	}
	otherRegistry := first.Primary()
	otherRegistry.Name = "other"
	otherRegistry.RecordsDir = filepath.Join(otherRegistry.Root, "other/records")
	_, err := state.glossarySetTool(facet.RegistrySet{otherRegistry}, glossarySetInput{Branch: branch, Term: "Wrong", Definition: "Wrong target"})
	if err == nil {
		t.Fatal("another registry must not inherit this branch's session")
	}
	if _, count := termIn(loadWritten(t, state, first, branch), "Wrong"); count != 0 {
		t.Fatal("refused registry call touched the first registry")
	}
	if _, err := state.glossaryCommitTool(facet.RegistrySet{otherRegistry}, glossaryCommitInput{Branch: branch, Message: "Wrong registry"}); err == nil {
		t.Fatal("another registry inherited commit state")
	}
	if _, err := state.glossaryProposeTool(facet.RegistrySet{otherRegistry}, glossaryProposeInput{Branch: branch, Title: "Wrong registry"}); err == nil {
		t.Fatal("another registry inherited proposal state")
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "isolated-glossary", Version: "1"}, nil)
	otherState := addGlossaryTools(server, first)
	t.Cleanup(otherState.close)
	result := callTool(t, server, "glossary_set", []byte(`{"branch":"glossary/shared-name","term":"Wrong server","definition":"Unauthorized reuse"}`))
	if !result.IsError {
		t.Fatal("a separately registered server inherited another server's branch")
	}
	result = callTool(t, server, "glossary_propose", []byte(`{"branch":"glossary/shared-name","title":"Unauthorized reuse"}`))
	if !result.IsError {
		t.Fatal("a separately registered server inherited proposal state")
	}
}

func TestGlossaryConcurrentAuthoringPreservesEveryTerm(t *testing.T) {
	set := glossaryAgentProject(t, categorisedFixture)
	server := mcp.NewServer(&mcp.Implementation{Name: "concurrent-glossary", Version: "1"}, nil)
	state := addGlossaryTools(server, set)
	t.Cleanup(state.close)
	client := session(t, server)
	const branch = "glossary/concurrent"
	const count = 8
	var inputs []any
	for index := range count {
		inputs = append(inputs, glossarySetInput{
			Branch: branch, Term: fmt.Sprintf("Concurrent%d", index),
			Definition: fmt.Sprintf("Definition %d", index),
		})
	}
	callAuthoringToolsConcurrently(t, client, "glossary_set", inputs)
	result := callTool(t, server, "glossary_commit", []byte(`{"branch":"glossary/concurrent","message":"All concurrent terms"}`))
	if result.IsError {
		t.Fatalf("commit refused: %s", firstText(result))
	}
	cmd := exec.Command("git", "show", branch+":docs/facets/glossary.yaml")
	cmd.Dir = set.Primary().Root
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("read committed glossary: %v\n%s", err, data)
	}
	var glossary facet.Glossary
	if err := yaml.Unmarshal(data, &glossary); err != nil {
		t.Fatalf("concurrent writes corrupted the glossary: %v", err)
	}
	if len(glossary.Terms) != 3+count {
		t.Errorf("lost or added terms: got %d, want %d", len(glossary.Terms), 3+count)
	}
	for index := range count {
		input := inputs[index].(glossarySetInput)
		term, occurrences := termIn(&glossary, input.Term)
		if occurrences != 1 || strings.TrimSpace(term.Definition) != input.Definition {
			t.Errorf("committed term %s lost or changed: %+v (%d occurrences)", input.Term, term, occurrences)
		}
	}
}

// a-glossary-change-arrives-as-a-proposal
func TestGlossaryWriteLandsOnABranchNotInTheWorkingTree(t *testing.T) {
	state := newGlossaryToolState()
	t.Cleanup(state.close)
	set := glossaryAgentProject(t, categorisedFixture)
	reg := set.Primary()
	before, err := os.ReadFile(reg.GlossaryPath())
	if err != nil {
		t.Fatal(err)
	}

	out, err := state.glossarySetTool(set, glossarySetInput{
		Branch: "glossary/new-term", Term: "Shipper",
		Definition: "The party handing the goods to the carrier.",
		Category:   "parties",
	})
	if err != nil {
		t.Fatalf("setting a term: %v", err)
	}
	if out.Diff == "" {
		t.Error("a write tool must answer with a diff, not a confirmation")
	}
	if out.Branch != "glossary/new-term" {
		t.Errorf("the answer must name the branch it wrote on, got %q", out.Branch)
	}

	after, err := os.ReadFile(reg.GlossaryPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("the glossary in the working tree changed; every write must land on the branch")
	}

	if _, count := termIn(loadWritten(t, state, set, "glossary/new-term"), "Shipper"); count != 1 {
		t.Errorf("the branch must carry the new term exactly once, got %d", count)
	}
}

// setting-a-term-that-exists-changes-it
func TestGlossarySetChangesAnExistingTermInPlace(t *testing.T) {
	state := newGlossaryToolState()
	t.Cleanup(state.close)
	set := glossaryAgentProject(t, categorisedFixture)
	const branch = "glossary/changed"

	if _, err := state.glossarySetTool(set, glossarySetInput{
		Branch: branch, Term: "Consignee",
		Definition: "The party who receives the goods — not the buyer.",
		Category:   "parties",
	}); err != nil {
		t.Fatalf("changing a term: %v", err)
	}

	entry, count := termIn(loadWritten(t, state, set, branch), "Consignee")
	if count != 1 {
		t.Fatalf("want exactly one entry for the term, got %d", count)
	}
	if !strings.Contains(entry.Definition, "not the buyer") {
		t.Errorf("the definition was not changed: %q", entry.Definition)
	}
}

// an-undeclared-category-is-refused
func TestGlossarySetRefusesAnUndeclaredCategory(t *testing.T) {
	state := newGlossaryToolState()
	t.Cleanup(state.close)
	set := glossaryAgentProject(t, categorisedFixture)
	const branch = "glossary/typo"

	_, err := state.glossarySetTool(set, glossarySetInput{
		Branch: branch, Term: "Tenant",
		Definition: "One deployment's customer.",
		Category:   "tenancy",
	})
	if err == nil {
		t.Fatal("an undeclared category must be refused")
	}
	if !strings.Contains(err.Error(), "tenancy") {
		t.Errorf("the refusal must name the category, got %q", err)
	}

	g := loadWritten(t, state, set, branch)
	if _, count := termIn(g, "Tenant"); count != 0 {
		t.Error("the term must not be written when its category is refused")
	}
	for _, id := range g.CategoryIDs() {
		if id == "tenancy" {
			t.Error("the category must not be invented")
		}
	}
}

// a-term-with-no-category-stays-uncategorised
func TestGlossarySetWithoutACategoryWritesNone(t *testing.T) {
	state := newGlossaryToolState()
	t.Cleanup(state.close)
	set := glossaryAgentProject(t, categorisedFixture)
	const branch = "glossary/no-category"

	if _, err := state.glossarySetTool(set, glossarySetInput{
		Branch: branch, Term: "Commitment",
		Definition: "A promise the product makes, backed by a test.",
	}); err != nil {
		t.Fatalf("setting an uncategorised term: %v", err)
	}

	entry, count := termIn(loadWritten(t, state, set, branch), "Commitment")
	if count != 1 {
		t.Fatalf("want one entry, got %d", count)
	}
	if entry.Category != "" {
		t.Errorf("no category was asked for; want empty, got %q", entry.Category)
	}
}

// nothing-writes-where-a-term-occurs
func TestGlossaryWriteNeverWritesUsedBy(t *testing.T) {
	state := newGlossaryToolState()
	t.Cleanup(state.close)
	set := glossaryAgentProject(t, categorisedFixture)
	const branch = "glossary/no-used-by"

	if _, err := state.glossarySetTool(set, glossarySetInput{
		Branch: branch, Term: "Consignee",
		Definition: "The party named in the transport document.",
		Category:   "parties",
	}); err != nil {
		t.Fatal(err)
	}

	session, err := state.glossarySessionFor(set.Primary(), branch)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(session.session.GlossaryPath)
	if err != nil {
		t.Fatal(err)
	}
	// Read as bytes rather than through the struct: the struct has no field for
	// it, so unmarshalling could not see such a key even if one were written.
	for _, forbidden := range []string{"usedBy", "used_by", "occurrences"} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("the written file carries %q; occurrence is found by reading the prose", forbidden)
		}
	}
}

// a-rename-says-what-it-orphaned
func TestGlossaryRenameNamesTheRecordsItOrphaned(t *testing.T) {
	state := newGlossaryToolState()
	t.Cleanup(state.close)
	set := glossaryAgentProject(t, categorisedFixture)
	reg := set.Primary()
	// A record whose prose uses the term, so the rename has something to
	// orphan. 'Consignee' appears in the summary.
	writeFile(t, filepath.Join(reg.RecordsDir, "features/logistics/booking/handover.yaml"),
		`schema_version: "0.1"
id: logistics/booking/handover
lifecycle: active
intent:
  summary: |
    Hands a booking to the Consignee named on it.
contract:
  scenarios:
    - id: handover-names-the-party
      statement: |
        Given a booking
        When it is handed over
        Then the Consignee is named
lineage:
  owner:
    team: logistics
  created_at: "2026-09-17"
`)

	const branch = "glossary/renamed"
	out, err := state.glossaryRenameTool(set, glossaryRenameInput{
		Branch: branch, From: "Consignee", To: "Destinatário",
	})
	if err != nil {
		t.Fatalf("renaming: %v", err)
	}

	g := loadWritten(t, state, set, branch)
	if _, count := termIn(g, "Destinatário"); count != 1 {
		t.Errorf("the new spelling must be defined once, got %d", count)
	}
	if _, count := termIn(g, "Consignee"); count != 0 {
		t.Error("the old spelling must be gone from the glossary")
	}

	// The records are not touched: the tool writes the glossary and nothing
	// else, which is also why it can only report what it orphaned.
	found := false
	for _, id := range out.StillSaying {
		if id == "logistics/booking/handover" {
			found = true
		}
	}
	if !found {
		t.Errorf("the answer must name the record whose prose still says the old word, got %v", out.StillSaying)
	}

	onDisk, err := os.ReadFile(filepath.Join(reg.RecordsDir, "features/logistics/booking/handover.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(onDisk), "Consignee") {
		t.Error("no record may be rewritten by a rename")
	}
}

// removing-a-term-that-is-not-defined-is-an-error
func TestGlossaryRemoveRefusesAnUnknownTerm(t *testing.T) {
	state := newGlossaryToolState()
	t.Cleanup(state.close)
	set := glossaryAgentProject(t, categorisedFixture)
	const branch = "glossary/does-not-exist"

	if _, err := state.glossaryRemoveTool(set, glossaryRemoveInput{
		Branch: branch, Term: "Carrier",
	}); err == nil {
		t.Fatal("removing a term the glossary does not define must be refused")
	}

	// And the one that is defined goes.
	if _, err := state.glossaryRemoveTool(set, glossaryRemoveInput{
		Branch: branch, Term: "Bill of Lading",
	}); err != nil {
		t.Fatalf("removing a defined term: %v", err)
	}
	if _, count := termIn(loadWritten(t, state, set, branch), "Bill of Lading"); count != 0 {
		t.Error("the term was not removed")
	}
}

// the-glossary-tools-reach-only-the-glossary
func TestGlossaryToolsTakeNoPath(t *testing.T) {
	state := newGlossaryToolState()
	t.Cleanup(state.close)
	set := glossaryAgentProject(t, categorisedFixture)
	const branch = "glossary/only-the-glossary"

	if _, err := state.glossarySetTool(set, glossarySetInput{
		Branch: branch, Term: "Shipper",
		Definition: "The party handing the goods to the carrier.",
		Category:   "parties",
	}); err != nil {
		t.Fatal(err)
	}

	session, err := state.glossarySessionFor(set.Primary(), branch)
	if err != nil {
		t.Fatal(err)
	}
	// Exactly one file differs on the branch, and it is the glossary. A tool
	// that could reach further would show up here as a second path.
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = session.session.Dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	var touched []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			touched = append(touched, line)
		}
	}
	if len(touched) != 1 || !strings.HasSuffix(touched[0], facet.GlossaryName) {
		t.Errorf("only the glossary may change on the branch, got %v", touched)
	}
}

// reading-returns-the-groups-as-a-reader-would-draw-them
func TestGlossaryListReturnsTheGroupsInDisplayOrder(t *testing.T) {
	set := glossaryAgentProject(t, categorisedFixture+`  - term: Tenant
    category: tenancy
    definition: |
      One deployment's customer.
`)

	out, err := glossaryListTool(set, glossaryListInput{})
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(out.Groups) != 4 {
		t.Fatalf("want four groups (two declared, one undeclared, one loose), got %d: %+v", len(out.Groups), out.Groups)
	}

	// Declared first, in file order rather than alphabetically.
	if out.Groups[0].ID != "parties" || !out.Groups[0].Declared {
		t.Errorf("first group must be the declared 'parties', got %+v", out.Groups[0])
	}
	if out.Groups[1].ID != "documents" || !out.Groups[1].Declared {
		t.Errorf("second group must be the declared 'documents', got %+v", out.Groups[1])
	}
	// Then the undeclared id, the defect state.
	if out.Groups[2].ID != "tenancy" || out.Groups[2].Declared {
		t.Errorf("third group must be the undeclared 'tenancy', got %+v", out.Groups[2])
	}
	// Then the uncategorised terms, last and nameless — never 'Other'.
	if out.Groups[3].ID != "" || out.Groups[3].Declared {
		t.Errorf("last group must be the nameless uncategorised one, got %+v", out.Groups[3])
	}
	if out.Groups[3].Title != "" {
		t.Errorf("the uncategorised group must carry no title, got %q", out.Groups[3].Title)
	}

	// The declared ids are what glossary_set will accept, so they are said
	// plainly rather than left to be derived from the groups.
	if strings.Join(out.DeclaredCategories, ",") != "parties,documents" {
		t.Errorf("want the declared ids in file order, got %v", out.DeclaredCategories)
	}
}

// A rename onto a term the glossary already defines is refused — and the
// refusal is what keeps two definitions of one word out of the file.
func TestGlossaryRenameOntoAnExistingTermIsRefused(t *testing.T) {
	state := newGlossaryToolState()
	t.Cleanup(state.close)
	set := glossaryAgentProject(t, categorisedFixture)

	const branch = "glossary/collision"
	_, err := state.glossaryRenameTool(set, glossaryRenameInput{
		Branch: branch, From: "Deviation", To: "Consignee",
	})
	if err == nil {
		t.Fatal("renaming onto a defined term must be refused, got no error")
	}
	if !errors.Is(err, facet.ErrGlossaryTermExists) {
		t.Errorf("want ErrGlossaryTermExists, got %v", err)
	}
	// Named, so the caller can act on it rather than guess which word collided.
	if !strings.Contains(err.Error(), "Consignee") {
		t.Errorf("the refusal must name the term it collided with, got %q", err)
	}

	// Nothing was written: both spellings stand as they did, and there is
	// exactly one of each.
	g := loadWritten(t, state, set, branch)
	if _, count := termIn(g, "Deviation"); count != 1 {
		t.Errorf("the refused rename must leave 'Deviation' defined once, got %d", count)
	}
	if _, count := termIn(g, "Consignee"); count != 1 {
		t.Errorf("'Consignee' must still be defined exactly once, got %d", count)
	}
}

// a-glossary-change-can-be-proposed
func TestGlossaryChangeReachesACommitOnTheBranch(t *testing.T) {
	state := newGlossaryToolState()
	t.Cleanup(state.close)
	set := glossaryAgentProject(t, categorisedFixture)
	const branch = "glossary/proposed"

	if _, err := state.glossarySetTool(set, glossarySetInput{
		Branch: branch, Term: "Shipper",
		Definition: "The party handing the goods to the carrier.",
		Category:   "parties",
	}); err != nil {
		t.Fatalf("setting a term: %v", err)
	}

	out, err := state.glossaryCommitTool(set, glossaryCommitInput{
		Branch: branch, Message: "docs(glossary): Shipper",
	})
	if err != nil {
		t.Fatalf("committing: %v", err)
	}
	if out.SHA == "" {
		t.Error("a commit must name the commit it made")
	}

	session, err := state.glossarySessionFor(set.Primary(), branch)
	if err != nil {
		t.Fatal(err)
	}
	// What the commit holds, asked of git rather than of the tool. A tool that
	// answered with a SHA it never made would pass every check on its own
	// output and fail this one.
	show := exec.Command("git", "show", "--name-only", "--format=", "HEAD")
	show.Dir = session.session.Dir
	named, err := show.CombinedOutput()
	if err != nil {
		t.Fatalf("git show: %v\n%s", err, named)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(named)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	if len(files) != 1 || !strings.HasSuffix(files[0], facet.GlossaryName) {
		t.Fatalf("the commit must hold exactly the glossary, got %v", files)
	}

	// And the branch is clean afterwards: the change is in the commit rather
	// than still lying beside it.
	status := exec.Command("git", "status", "--porcelain")
	status.Dir = session.session.Dir
	left, err := status.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, left)
	}
	if strings.TrimSpace(string(left)) != "" {
		t.Errorf("the write is committed, so nothing may be left uncommitted: %s", left)
	}

	// The term is in the committed tree, not merely in the worktree.
	committed := exec.Command("git", "show", "HEAD:"+strings.TrimSpace(files[0]))
	committed.Dir = session.session.Dir
	body, err := committed.CombinedOutput()
	if err != nil {
		t.Fatalf("git show HEAD:<glossary>: %v\n%s", err, body)
	}
	if !strings.Contains(string(body), "Shipper") {
		t.Errorf("the committed glossary does not carry the term:\n%s", body)
	}
}

// a-commit-with-nothing-written-is-refused
func TestGlossaryCommitRefusesABranchNothingWasWrittenOn(t *testing.T) {
	state := newGlossaryToolState()
	t.Cleanup(state.close)
	set := glossaryAgentProject(t, categorisedFixture)

	_, err := state.glossaryCommitTool(set, glossaryCommitInput{
		Branch: "glossary/nothing-written", Message: "docs(glossary): nothing",
	})
	if err == nil {
		t.Fatal("committing a branch nothing was written on must be refused")
	}
	if !errors.Is(err, facet.ErrNothingToCommit) {
		t.Errorf("want ErrNothingToCommit, got %v", err)
	}
}

// An empty commit message is refused before staging any changes, so every
// created commit carries a message.
func TestGlossaryCommitRefusesAnEmptyMessage(t *testing.T) {
	state := newGlossaryToolState()
	t.Cleanup(state.close)
	set := glossaryAgentProject(t, categorisedFixture)
	const branch = "glossary/no-message"

	if _, err := state.glossarySetTool(set, glossarySetInput{
		Branch: branch, Term: "Shipper",
		Definition: "The party handing the goods to the carrier.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.glossaryCommitTool(set, glossaryCommitInput{
		Branch: branch, Message: "   ",
	}); err == nil {
		t.Fatal("a commit without a message must be refused")
	}
}

// close removes only the throwaway worktrees owned by this test state.
func (state *glossaryToolState) close() {
	state.mu.Lock()
	defer state.mu.Unlock()
	for key, session := range state.by {
		session.mu.Lock()
		_ = session.session.Close()
		session.mu.Unlock()
		delete(state.by, key)
	}
}
