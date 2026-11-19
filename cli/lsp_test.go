// Fixtures below contain example @facet: tags; this file demonstrates the
// syntax rather than implementing a feature.
// @facet-ignore-file

package cli

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/facet-framework/core/registry"
)

// lspFixtureFiles is everything a test needs to build position params without
// hardcoding line/column arithmetic.
type lspFixtureFiles struct {
	root       string
	recordPath string
	srcPath    string
	srcLine    string
	testPath   string
	testLine   string
}

// lspFixture builds a single-registry project with one record and two anchors
// pointing at it, and returns the server plus the fixture's files and lines.
func lspFixture(t *testing.T) (*lspServer, lspFixtureFiles) {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "docs/facets/features/payments/checkout/session.yaml"), `schema_version: "0.1"
id: payments/checkout/session
lifecycle: active
intent:
  summary: |
    Creates a checkout session so a customer can pay for a plan.
contract:
  scenarios:
    - id: created
      statement: |
        Given a plan
        When the customer subscribes
        Then a session is created
lineage:
  owner:
    team: payments
  created_at: "2026-01-01"
`)
	srcLine := "// @facet: payments/checkout/session"
	write(t, filepath.Join(root, "src/checkout.ts"), srcLine+"\n// @facet-role: primary\nexport function create() {}\n")
	testLine := "// @facet: payments/checkout/session"
	write(t, filepath.Join(root, "src/checkout_test.ts"), testLine+"\n// @facet-role: related\ntest('creates a session', () => {})\n")

	set := facet.OpenSet(root)
	return newLSPServer(set), lspFixtureFiles{
		root:       root,
		recordPath: set.Primary().PathForID("payments/checkout/session"),
		srcPath:    filepath.Join(root, "src/checkout.ts"),
		srcLine:    srcLine,
		testPath:   filepath.Join(root, "src/checkout_test.ts"),
		testLine:   testLine,
	}
}

func idPosition(t *testing.T, uri, line string, id string) json.RawMessage {
	t.Helper()
	col := strings.Index(line, id)
	if col < 0 {
		t.Fatalf("id %q not found in line %q", id, line)
	}
	return mustMarshal(t, map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 0, "character": col + 1},
	})
}

func referenceParamsJSON(t *testing.T, uri, line, id string, includeDeclaration bool) json.RawMessage {
	t.Helper()
	col := strings.Index(line, id)
	if col < 0 {
		t.Fatalf("id %q not found in line %q", id, line)
	}
	return mustMarshal(t, map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 0, "character": col + 1},
		"context":      map[string]any{"includeDeclaration": includeDeclaration},
	})
}

func renameRequestJSON(t *testing.T, uri, line, id, newName string) json.RawMessage {
	t.Helper()
	col := strings.Index(line, id)
	if col < 0 {
		t.Fatalf("id %q not found in line %q", id, line)
	}
	return mustMarshal(t, map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 0, "character": col + 1},
		"newName":      newName,
	})
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestReferencesFindsEveryAnchorForTheId(t *testing.T) {
	srv, f := lspFixture(t)
	raw := referenceParamsJSON(t, pathToURI(f.srcPath), f.srcLine, "payments/checkout/session", false)

	result := srv.references(raw)
	locs, ok := result.([]location)
	if !ok {
		t.Fatalf("want []location, got %T (%v)", result, result)
	}
	if len(locs) != 2 {
		t.Fatalf("want the two anchors (source and test), got %d: %+v", len(locs), locs)
	}
	uris := map[string]bool{locs[0].URI: true, locs[1].URI: true}
	if !uris[pathToURI(f.srcPath)] || !uris[pathToURI(f.testPath)] {
		t.Fatalf("want both anchor files, got %+v", locs)
	}
}

func TestReferencesIncludesTheDeclarationWhenAsked(t *testing.T) {
	srv, f := lspFixture(t)
	raw := referenceParamsJSON(t, pathToURI(f.srcPath), f.srcLine, "payments/checkout/session", true)

	result := srv.references(raw)
	locs, ok := result.([]location)
	if !ok {
		t.Fatalf("want []location, got %T (%v)", result, result)
	}
	if len(locs) != 3 {
		t.Fatalf("want 2 anchors + 1 declaration, got %d: %+v", len(locs), locs)
	}
	found := false
	for _, l := range locs {
		if l.URI == pathToURI(f.recordPath) {
			found = true
		}
	}
	if !found {
		t.Fatalf("want the record file among the results, got %+v", locs)
	}
}

// References is anchor-text search, not record resolution: it finds an id
// with no record too, and includes the very occurrence the cursor sits on -
// standard find-references behavior, and the reason a solitary anchor still
// gets exactly one result rather than none.
func TestReferencesOnUnresolvableAnchorFindsItsOwnOccurrence(t *testing.T) {
	srv, f := lspFixture(t)
	write(t, filepath.Join(f.root, "src/other.ts"), "// @facet: nope/does/not-exist\n")
	line := "// @facet: nope/does/not-exist"
	otherURI := pathToURI(filepath.Join(f.root, "src/other.ts"))
	raw := referenceParamsJSON(t, otherURI, line, "nope/does/not-exist", false)

	result := srv.references(raw)
	locs, ok := result.([]location)
	if !ok || len(locs) != 1 || locs[0].URI != otherURI {
		t.Fatalf("want exactly the one anchor itself, got %T %v", result, result)
	}
}

func TestPrepareRenameReturnsTheIdRangeAndPlaceholder(t *testing.T) {
	srv, f := lspFixture(t)
	raw := idPosition(t, pathToURI(f.srcPath), f.srcLine, "payments/checkout/session")

	result := srv.prepareRename(raw)
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("want a map result, got %T (%v)", result, result)
	}
	if m["placeholder"] != "payments/checkout/session" {
		t.Fatalf("want the bare id as the placeholder, got %v", m["placeholder"])
	}
}

func TestPrepareRenameOffTheIdRefusesRatherThanGuesses(t *testing.T) {
	srv, f := lspFixture(t)
	raw := mustMarshal(t, map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(f.srcPath)},
		"position":     map[string]any{"line": 0, "character": 0}, // on "//", not the id
	})

	if result := srv.prepareRename(raw); result != nil {
		t.Fatalf("want nil off the id, got %v", result)
	}
}

func TestRenameRewritesEveryAnchorTheRecordAndItsPath(t *testing.T) {
	srv, f := lspFixture(t)
	raw := renameRequestJSON(t, pathToURI(f.srcPath), f.srcLine, "payments/checkout/session", "payments/checkout/session-v2")

	result := srv.rename(raw)
	edit, ok := result.(workspaceEdit)
	if !ok {
		t.Fatalf("want a workspaceEdit, got %T (%v)", result, result)
	}

	var textEdits, renames int
	newPath := facet.OpenSet(f.root).Primary().PathForID("payments/checkout/session-v2")
	sawRecordEdit, sawRename := false, false
	for _, c := range edit.DocumentChanges {
		switch v := c.(type) {
		case textDocumentEdit:
			textEdits++
			if len(v.Edits) != 1 {
				t.Fatalf("want exactly one edit per textDocumentEdit, got %+v", v)
			}
			if v.Edits[0].NewText != "payments/checkout/session-v2" {
				t.Fatalf("want the new bare id as NewText, got %q", v.Edits[0].NewText)
			}
			if v.TextDocument.URI == pathToURI(f.recordPath) {
				sawRecordEdit = true
			}
		case renameFileOp:
			renames++
			if v.Kind != "rename" {
				t.Fatalf("want kind=rename, got %q", v.Kind)
			}
			if v.OldURI != pathToURI(f.recordPath) || v.NewURI != pathToURI(newPath) {
				t.Fatalf("want the record's old path renamed to its new id's path, got old=%q new=%q", v.OldURI, v.NewURI)
			}
			sawRename = true
		default:
			t.Fatalf("unexpected document change type %T", c)
		}
	}
	// Two anchors (source + test) and one record id: field, plus the file rename.
	if textEdits != 3 {
		t.Fatalf("want 3 text edits (2 anchors + the record's id field), got %d", textEdits)
	}
	if renames != 1 {
		t.Fatalf("want exactly one file rename, got %d", renames)
	}
	if !sawRecordEdit {
		t.Fatal("want the record's own id: field among the edits")
	}
	if !sawRename {
		t.Fatal("want the record file renamed to match its new id")
	}
}

func TestRenameRejectsAMalformedNewName(t *testing.T) {
	srv, f := lspFixture(t)
	raw := renameRequestJSON(t, pathToURI(f.srcPath), f.srcLine, "payments/checkout/session", "NotAValidId")

	if result := srv.rename(raw); result != nil {
		t.Fatalf("want nil for a newName that is not a valid id shape, got %v", result)
	}
}

func TestRenameToTheSameIdIsANoOp(t *testing.T) {
	srv, f := lspFixture(t)
	raw := renameRequestJSON(t, pathToURI(f.srcPath), f.srcLine, "payments/checkout/session", "payments/checkout/session")

	if result := srv.rename(raw); result != nil {
		t.Fatalf("want nil when newName equals the current id, got %v", result)
	}
}

func TestRenameUsesTheOpenBufferNotDisk(t *testing.T) {
	srv, f := lspFixture(t)
	edited := "// leading comment added\n" + f.srcLine + "\n// @facet-role: primary\nexport function create() {}\n"
	srv.setDoc(pathToURI(f.srcPath), edited)

	col := strings.Index(f.srcLine, "payments/checkout/session")
	raw := mustMarshal(t, map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(f.srcPath)},
		"position":     map[string]any{"line": 1, "character": col + 1}, // the anchor is now on line 1, not 0
		"newName":      "payments/checkout/session-v2",
	})

	result := srv.rename(raw)
	edit, ok := result.(workspaceEdit)
	if !ok {
		t.Fatalf("want a workspaceEdit, got %T (%v)", result, result)
	}
	for _, c := range edit.DocumentChanges {
		td, ok := c.(textDocumentEdit)
		if !ok || td.TextDocument.URI != pathToURI(f.srcPath) {
			continue
		}
		if td.Edits[0].Range.Start.Line != 1 {
			t.Fatalf("want the edit on line 1 (the buffer's line, not disk's line 0), got %+v", td.Edits[0].Range)
		}
		applied := applyEdits(t, edited, td.Edits)
		if !strings.Contains(applied, "// @facet: payments/checkout/session-v2\n") {
			t.Fatalf("applying the edit to the buffer text should yield the renamed anchor, got:\n%s", applied)
		}
		return
	}
	t.Fatal("no edit for the source file")
}

// A rename triggered from an id shared by two registries (the same feature,
// documented independently once per product) touches every anchor and every
// owning record - not just the registry whose Sources happen to cover the
// file the cursor was in.
func TestRenameFromAnUnqualifiedSharedAnchorTouchesEveryRegistry(t *testing.T) {
	set := monorepo(t)
	srv := newLSPServer(set)
	storefront, admin := set[0], set[1]
	if storefront.Name != "storefront" {
		storefront, admin = set[1], set[0]
	}

	line := "// @facet: auth/login/password"
	raw := renameRequestJSON(t, pathToURI(filepath.Join(storefront.Root, "apps/storefront/login.go")), line, "auth/login/password", "auth/login/passcode")

	result := srv.rename(raw)
	edit, ok := result.(workspaceEdit)
	if !ok {
		t.Fatalf("want a workspaceEdit, got %T (%v)", result, result)
	}

	renamedFiles := map[string]bool{}
	editedFiles := map[string]bool{}
	for _, c := range edit.DocumentChanges {
		switch v := c.(type) {
		case textDocumentEdit:
			editedFiles[v.TextDocument.URI] = true
		case renameFileOp:
			renamedFiles[v.OldURI] = true
		}
	}
	wantEdited := []string{
		pathToURI(filepath.Join(storefront.Root, "apps/storefront/login.go")),
		pathToURI(filepath.Join(admin.Root, "apps/admin/login.go")),
		pathToURI(storefront.PathForID("auth/login/password")),
		pathToURI(admin.PathForID("auth/login/password")),
	}
	for _, want := range wantEdited {
		if !editedFiles[want] {
			t.Errorf("want an edit for %s, got edited=%v", want, editedFiles)
		}
	}
	for _, reg := range []facet.Registry{storefront, admin} {
		if !renamedFiles[pathToURI(reg.PathForID("auth/login/password"))] {
			t.Errorf("want %s's record file renamed too, got renamed=%v", reg.Name, renamedFiles)
		}
	}
}

// A rename triggered from a QUALIFIED anchor names one registry
// unambiguously; it must not touch the other registry's anchor or record,
// which still (correctly) describe a distinct, unrenamed feature.
func TestRenameFromAQualifiedAnchorTouchesOnlyThatRegistry(t *testing.T) {
	set := monorepo(t)
	storefront, admin := set[0], set[1]
	if storefront.Name != "storefront" {
		storefront, admin = set[1], set[0]
	}
	qualifiedLine := "// @facet: storefront:auth/login/password"
	write(t, filepath.Join(storefront.Root, "apps/storefront/qualified.go"), qualifiedLine+"\npackage storefront\n")
	srv := newLSPServer(facet.OpenSet(storefront.Root))

	raw := renameRequestJSON(t, pathToURI(filepath.Join(storefront.Root, "apps/storefront/qualified.go")), qualifiedLine, "auth/login/password", "auth/login/passcode")

	result := srv.rename(raw)
	edit, ok := result.(workspaceEdit)
	if !ok {
		t.Fatalf("want a workspaceEdit, got %T (%v)", result, result)
	}

	editedFiles := map[string]bool{}
	renamedFiles := map[string]bool{}
	for _, c := range edit.DocumentChanges {
		switch v := c.(type) {
		case textDocumentEdit:
			editedFiles[v.TextDocument.URI] = true
		case renameFileOp:
			renamedFiles[v.OldURI] = true
		}
	}
	adminAnchor := pathToURI(filepath.Join(admin.Root, "apps/admin/login.go"))
	adminRecord := pathToURI(admin.PathForID("auth/login/password"))
	if editedFiles[adminAnchor] {
		t.Error("a qualified rename must not touch the other registry's unqualified anchor")
	}
	if editedFiles[adminRecord] || renamedFiles[adminRecord] {
		t.Error("a qualified rename must not touch the other registry's record")
	}
	storefrontUnqualifiedAnchor := pathToURI(filepath.Join(storefront.Root, "apps/storefront/login.go"))
	if editedFiles[storefrontUnqualifiedAnchor] {
		t.Error("a qualified rename must not touch that same registry's own unqualified anchor either - only anchors written with the same qualifier")
	}
	if !editedFiles[pathToURI(filepath.Join(storefront.Root, "apps/storefront/qualified.go"))] {
		t.Error("want the qualified anchor itself renamed")
	}
	if !renamedFiles[pathToURI(storefront.PathForID("auth/login/password"))] {
		t.Error("want storefront's own record renamed")
	}
}

// id: values are sometimes single-quoted - valid, lint-clean YAML. A rename
// must still find and edit the field, and must not move the file without
// also fixing what it declares.
func TestRenameFindsASingleQuotedIDField(t *testing.T) {
	srv, f := lspFixture(t)
	write(t, f.recordPath, `schema_version: "0.1"
id: 'payments/checkout/session'
lifecycle: active
intent:
  summary: Creates a checkout session.
lineage:
  owner:
    team: payments
  created_at: "2026-01-01"
`)

	raw := renameRequestJSON(t, pathToURI(f.srcPath), f.srcLine, "payments/checkout/session", "payments/checkout/session-v2")
	result := srv.rename(raw)
	edit, ok := result.(workspaceEdit)
	if !ok {
		t.Fatalf("want a workspaceEdit, got %T (%v)", result, result)
	}

	found := false
	for _, c := range edit.DocumentChanges {
		if td, ok := c.(textDocumentEdit); ok && td.TextDocument.URI == pathToURI(f.recordPath) {
			found = true
			if td.Edits[0].NewText != "payments/checkout/session-v2" {
				t.Fatalf("want the bare new id, quotes excluded, got %q", td.Edits[0].NewText)
			}
		}
	}
	if !found {
		t.Fatal("want the single-quoted id: field edited, not skipped")
	}
}

// Renaming onto an id that already has a record must refuse outright rather
// than overwrite or half-apply.
func TestRenameRefusesWhenTheNewIDAlreadyExists(t *testing.T) {
	srv, f := lspFixture(t)
	write(t, filepath.Join(f.root, "docs/facets/features/payments/checkout/other.yaml"),
		"schema_version: \"0.1\"\nid: payments/checkout/other\nlifecycle: active\nintent:\n  summary: Already exists.\n")

	raw := renameRequestJSON(t, pathToURI(f.srcPath), f.srcLine, "payments/checkout/session", "payments/checkout/other")

	if result := srv.rename(raw); result != nil {
		t.Fatalf("want nil when the new id already has a record, got %v", result)
	}
}

// If one owning registry's id: field cannot be found and edited, the whole
// rename refuses rather than renaming anchors and the other registry's
// record while leaving this one's anchors pointing at an id it never
// declared - a half-rename would be worse than doing nothing.
func TestRenameRefusesWhollyWhenAnyOwnersIDFieldCannotBeFound(t *testing.T) {
	set := monorepo(t)
	storefront, admin := set[0], set[1]
	if storefront.Name != "storefront" {
		storefront, admin = set[1], set[0]
	}
	// A folded scalar puts the value on its own line - valid YAML,
	// unmatched by the line-anchored id: regex.
	write(t, admin.PathForID("auth/login/password"), "schema_version: \"0.1\"\nid: >-\n  auth/login/password\nlifecycle: active\nintent:\n  summary: Staff sign-in.\n")
	srv := newLSPServer(set)

	line := "// @facet: auth/login/password"
	raw := renameRequestJSON(t, pathToURI(filepath.Join(storefront.Root, "apps/storefront/login.go")), line, "auth/login/password", "auth/login/passcode")

	if result := srv.rename(raw); result != nil {
		t.Fatalf("want nil (refuse whole) when admin's id: field can't be found, got %v", result)
	}
}

// applyEdits applies textEdits to source text for assertions, in reverse
// range order so earlier offsets are unaffected by later replacements.
func applyEdits(t *testing.T, text string, edits []textEdit) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	sorted := append([]textEdit{}, edits...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Range.Start.Line > sorted[j].Range.Start.Line })
	for _, e := range sorted {
		line := lines[e.Range.Start.Line]
		lines[e.Range.Start.Line] = line[:e.Range.Start.Character] + e.NewText + line[e.Range.End.Character:]
	}
	return strings.Join(lines, "\n")
}

// completionItemsFor drives the completion handler on a half-typed anchor.
func completionItemsFor(t *testing.T, s *lspServer, uri, line string) []completionItem {
	t.Helper()
	s.setDoc(uri, line+"\n")
	params := mustMarshal(t, map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 0, "character": len(line)},
	})
	got := s.completion(params)
	items, ok := got.([]completionItem)
	if !ok {
		t.Fatalf("completion returned %T, want []completionItem", got)
	}
	return items
}

func labels(items []completionItem) []string {
	out := make([]string, 0, len(items))
	for _, i := range items {
		out = append(out, i.Label)
	}
	sort.Strings(out)
	return out
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// Anchor completion suggests declared taxonomy prefixes for new IDs, keeping
// new immutable record names within the project taxonomy.
func TestCompletionOffersDeclaredTaxonomyPrefixes(t *testing.T) {
	s, files := lspFixture(t)
	write(t, filepath.Join(files.root, "docs/facets/taxonomy.yaml"), `schema_version: "0.1"
domains:
  payments:
    title: Payments
    classification: core
    capabilities:
      checkout:
        title: Checkout
        purpose: Turning an intent to buy into a paid order.
      refund:
        title: Refunds
        purpose: Giving money back.
`)
	s = newLSPServer(facet.OpenSet(files.root))

	got := labels(completionItemsFor(t, s, "file://"+files.srcPath, "// @facet: "))

	// The existing record is still offered.
	if !contains(got, "payments/checkout/session") {
		t.Fatalf("want the existing record among completions, got %v", got)
	}
	// And so is every declared capability, including the one with no record.
	for _, want := range []string{"payments/checkout/", "payments/refund/"} {
		if !contains(got, want) {
			t.Fatalf("want %q among completions, got %v", want, got)
		}
	}
}

// Without a taxonomy there are no declared prefixes to suggest; existing
// record IDs alone do not define the project taxonomy.
func TestCompletionInventsNoPrefixesWithoutATaxonomy(t *testing.T) {
	s, files := lspFixture(t)

	for _, label := range labels(completionItemsFor(t, s, "file://"+files.srcPath, "// @facet: ")) {
		if strings.HasSuffix(label, "/") {
			t.Fatalf("want no prefix suggestions without a taxonomy, got %q", label)
		}
	}
}
