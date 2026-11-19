// Fixtures below contain example @facet: tags; this file demonstrates the
// syntax rather than implementing a feature.
// @facet-ignore-file

package facet

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// record writes a minimal but valid-enough feature record for the checks that
// do not involve the JSON schema.
func record(t *testing.T, root, registryRecords, id, lifecycle string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(registryRecords), "features", filepath.FromSlash(id)+".yaml")
	writeFile(t, path, "schema_version: \"0.1\"\nid: "+id+"\nlifecycle: "+lifecycle+"\n")
}

func lintProject(t *testing.T, root string) Report {
	t.Helper()
	rep, err := LintAll(root, LintOptions{
		Skip:  map[string]bool{"schema": true, "knowledge": true},
		Today: "2026-07-27",
	})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func findings(rep Report, check string) []Finding {
	var out []Finding
	for _, f := range rep.Findings {
		if f.Check == check {
			out = append(out, f)
		}
	}
	return out
}

const twoRegistries = `
schema_version: "0.1"
registries:
  storefront:
    records: apps/storefront/docs/facets
    sources: [apps/storefront, packages/shared]
  admin:
    records: apps/admin/docs/facets
    sources: [apps/admin, packages/shared]
`

// Shared sources may be scanned by multiple registries. An anchor owned
// by one registry must not be reported as dangling in the others.
func TestSharedCodeAnchorIsNotDanglingInTheRegistryThatDoesNotOwnIt(t *testing.T) {
	root := project(t, twoRegistries)
	record(t, root, "apps/storefront/docs/facets", "billing/checkout/coupons", "active")
	writeFile(t, filepath.Join(root, "apps", "storefront", "docs", "facets", "features", ".keep"), "")
	writeFile(t, filepath.Join(root, "apps", "admin", "docs", "facets", "features", ".keep"), "")
	writeFile(t, filepath.Join(root, "packages", "shared", "coupon.go"),
		"// @facet: billing/checkout/coupons\npackage shared\n")

	rep := lintProject(t, root)
	if got := findings(rep, "orphan-ids"); len(got) != 0 {
		t.Fatalf("shared code anchoring another registry's feature was reported: %+v", got)
	}
	// The record is anchored, but only in code the other registry also compiles,
	// so this product may never reach the feature — the variants rule, one level
	// up. A warning, never an error: it is a question, not a defect.
	got := findings(rep, "orphan-records")
	if len(got) != 1 || got[0].Severity != SeverityWarning {
		t.Fatalf("want one shared-code warning, got %+v", got)
	}
	if got[0].Registry != "storefront" {
		t.Errorf("the warning belongs to the registry holding the record, got %q", got[0].Registry)
	}
}

// Code exclusive to one registry gets no benefit of the doubt: it implements a
// feature that registry has no record for, whoever else happens to own the id.
// The suppression above is for shared code alone.
func TestExclusiveCodeAnchoringAnotherRegistrysFeatureIsAnError(t *testing.T) {
	root := project(t, twoRegistries)
	record(t, root, "apps/storefront/docs/facets", "billing/checkout/coupons", "planned")
	writeFile(t, filepath.Join(root, "apps", "admin", "docs", "facets", "features", ".keep"), "")
	// Not packages/shared — a file only the admin registry claims.
	writeFile(t, filepath.Join(root, "apps", "admin", "coupon.go"),
		"// @facet: billing/checkout/coupons\npackage admin\n")

	rep := lintProject(t, root)
	got := findings(rep, "orphan-ids")
	if len(got) != 1 {
		t.Fatalf("want the admin-exclusive anchor reported, got %+v", rep.Findings)
	}
	if got[0].Registry != "admin" {
		t.Errorf("finding belongs to admin, got %q", got[0].Registry)
	}
	if !strings.Contains(got[0].Hint, "another registry has this feature") {
		t.Errorf("the hint should say the id exists elsewhere, got %q", got[0].Hint)
	}
}

// Ids are global: the same id in two registries is the same feature documented
// twice, which is what makes the two comparable.
func TestOwnersListsEveryRegistryDocumentingAnId(t *testing.T) {
	root := project(t, twoRegistries)
	record(t, root, "apps/storefront/docs/facets", "auth/login/password", "active")
	record(t, root, "apps/admin/docs/facets", "auth/login/password", "active")

	set := OpenSet(root)
	owners := set.Owners("auth/login/password")
	if len(owners) != 2 {
		t.Fatalf("want both registries, got %d", len(owners))
	}
	if owners[0].Name != "admin" || owners[1].Name != "storefront" {
		t.Errorf("owners must be in alphabetical order, got %q then %q", owners[0].Name, owners[1].Name)
	}
	// A prefix narrows it to one.
	if got := set.Owners("storefront:auth/login/password"); len(got) != 1 || got[0].Name != "storefront" {
		t.Errorf("a qualified id must resolve to exactly its registry, got %+v", got)
	}
	if got := set.Owners("auth/login/nonexistent"); len(got) != 0 {
		t.Errorf("want no owners for an unknown id, got %+v", got)
	}
}

// The suppression above must not become a blanket amnesty: an id no registry
// owns is still a dangling anchor.
func TestAnIdNoRegistryOwnsIsStillReported(t *testing.T) {
	root := project(t, twoRegistries)
	writeFile(t, filepath.Join(root, "apps", "storefront", "docs", "facets", "features", ".keep"), "")
	writeFile(t, filepath.Join(root, "apps", "admin", "docs", "facets", "features", ".keep"), "")
	writeFile(t, filepath.Join(root, "packages", "shared", "coupon.go"),
		"// @facet: billing/checkout/coupons\npackage shared\n")

	rep := lintProject(t, root)
	got := findings(rep, "orphan-ids")
	if len(got) == 0 {
		t.Fatal("an id owned by nobody must still be reported")
	}
	for _, f := range got {
		if f.Registry == "" {
			t.Errorf("a finding in a monorepo must name its registry: %+v", f)
		}
	}
}

// The same id in two registries is one feature documented independently by each
// product. Each registry proves its own presence with its own code; neither
// borrows the other's anchor.
func TestEachRegistryProvesItsOwnPresenceForAGlobalId(t *testing.T) {
	root := project(t, twoRegistries)
	record(t, root, "apps/storefront/docs/facets", "auth/login/password", "active")
	record(t, root, "apps/admin/docs/facets", "auth/login/password", "active")
	writeFile(t, filepath.Join(root, "apps", "storefront", "login.go"),
		"// @facet: auth/login/password\npackage storefront\n")
	writeFile(t, filepath.Join(root, "apps", "admin", "login.go"),
		"// @facet: auth/login/password\npackage admin\n")

	rep := lintProject(t, root)
	if got := findings(rep, "orphan-records"); len(got) != 0 {
		t.Fatalf("each registry anchors its own feature: %+v", got)
	}
	if got := findings(rep, "orphan-ids"); len(got) != 0 {
		t.Fatalf("both ids resolve within their own registry: %+v", got)
	}
}

// A qualified anchor lets shared code say which registry it is implementing for.
func TestAQualifiedAnchorCountsOnlyForTheRegistryItNames(t *testing.T) {
	root := project(t, twoRegistries)
	record(t, root, "apps/storefront/docs/facets", "auth/login/password", "active")
	record(t, root, "apps/admin/docs/facets", "auth/login/password", "active")
	writeFile(t, filepath.Join(root, "packages", "shared", "login.go"),
		"// @facet: storefront:auth/login/password\npackage shared\n")

	rep := lintProject(t, root)
	byRegistry := map[string]Finding{}
	for _, f := range findings(rep, "orphan-records") {
		byRegistry[f.Registry] = f
	}
	if len(byRegistry) != 2 {
		t.Fatalf("want a finding for each registry, got %+v", byRegistry)
	}
	// admin: the tag names storefront, so admin has no anchor at all.
	if got := byRegistry["admin"]; got.Severity != SeverityError {
		t.Errorf("admin has no anchor at all — want an error, got %q", got.Severity)
	}
	// storefront: the tag is its own, but it sits in shared code, which proves
	// ownership without proving the product reaches the feature.
	if got := byRegistry["storefront"]; got.Severity != SeverityWarning {
		t.Errorf("storefront is anchored only in shared code — want a warning, got %q", got.Severity)
	}
}

// A prefix nothing declares makes the anchor invisible to every registry, which
// is the silent failure the check exists to prevent.
func TestAnUndeclaredRegistryPrefixIsReportedOnce(t *testing.T) {
	root := project(t, twoRegistries)
	writeFile(t, filepath.Join(root, "apps", "storefront", "docs", "facets", "features", ".keep"), "")
	writeFile(t, filepath.Join(root, "apps", "admin", "docs", "facets", "features", ".keep"), "")
	writeFile(t, filepath.Join(root, "packages", "shared", "login.go"),
		"// @facet: storefont:auth/login/password\npackage shared\n")

	rep := lintProject(t, root)
	got := findings(rep, "registry-refs")
	if len(got) != 1 {
		t.Fatalf("want one finding, got %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Message, "storefont") {
		t.Errorf("the finding must name the bad prefix, got %q", got[0].Message)
	}
	// It must not also be reported as a dangling id: the prefix is the defect,
	// and two findings for one typo sends the author to the wrong fix.
	if ids := findings(rep, "orphan-ids"); len(ids) != 0 {
		t.Errorf("one typo produced a second, misleading finding: %+v", ids)
	}
}

// In a single-registry project no prefix is ever right, and one written by
// mistake would otherwise disconnect the file from the registry silently.
func TestAPrefixInASingleRegistryProjectIsReported(t *testing.T) {
	root := project(t, "")
	record(t, root, "docs/facets", "auth/login/password", "planned")
	writeFile(t, filepath.Join(root, "login.go"),
		"// @facet: web:auth/login/password\npackage app\n")

	rep := lintProject(t, root)
	if got := findings(rep, "registry-refs"); len(got) != 1 {
		t.Fatalf("want the stray prefix reported, got %+v", got)
	}
}

// A records path that does not exist has nothing to find fault with, so every
// other check passes and the typo survives.
func TestARegistryPointingAtNothingIsReported(t *testing.T) {
	root := project(t, `
registries:
  admin:
    records: apps/admin/docs/facets
`)

	rep, err := LintAll(root, LintOptions{Skip: map[string]bool{"knowledge": true}, Today: "2026-07-27"})
	if err != nil {
		t.Fatal(err)
	}
	got := findings(rep, "config")
	if len(got) != 1 {
		t.Fatalf("want the missing records directory reported, got %+v", rep.Findings)
	}
	if got[0].Registry != "admin" {
		t.Errorf("finding must name the registry, got %q", got[0].Registry)
	}
}

// Without a registries declaration, findings carry no registry name and the
// report contains no registry list, preserving single-registry output.
func TestSingleRegistryFindingsCarryNoRegistryName(t *testing.T) {
	root := project(t, "")
	record(t, root, "docs/facets", "auth/login/password", "active")

	rep := lintProject(t, root)
	got := findings(rep, "orphan-records")
	if len(got) != 1 {
		t.Fatalf("want the unanchored active record reported, got %+v", rep.Findings)
	}
	if got[0].Registry != "" {
		t.Errorf("a project declaring no registries must not be told it has one, got %q", got[0].Registry)
	}
	if len(rep.Checked.Registries) != 0 {
		t.Errorf("want no registry list, got %v", rep.Checked.Registries)
	}
}

// A record with lineage. `record` writes the minimum, and the graph checks need
// a lineage facet to look at.
func recordWithLineage(t *testing.T, root, registryRecords, id, lifecycle, lineage string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(registryRecords), "features", filepath.FromSlash(id)+".yaml")
	writeFile(t, path, "schema_version: \"0.1\"\nid: "+id+"\nlifecycle: "+lifecycle+"\nlineage:\n"+lineage)
}

// The hole this closes: a dependency on a record nobody wrote must not pass
// every gate.
func TestDependencyOnAMissingRecordIsAnError(t *testing.T) {
	root := project(t, "")
	recordWithLineage(t, root, "docs/facets", "ui/panel/data", "active",
		"  depends_on:\n    - ui/panel/never-written\n")
	writeFile(t, filepath.Join(root, "src", "data.ts"), "// @facet: ui/panel/data\n")

	rep := lintProject(t, root)

	got := findings(rep, "dependencies")
	if len(got) != 1 {
		t.Fatalf("want one finding, got %+v", rep.Findings)
	}
	if got[0].Severity != SeverityError {
		t.Errorf("want an error, got %q", got[0].Severity)
	}
	if !strings.Contains(got[0].Message, "ui/panel/never-written") {
		t.Errorf("the finding must name the missing id: %q", got[0].Message)
	}
}

// The reverse edge is written by hand just as often, and is just as able to
// name nothing.
func TestDependedOnByIsCheckedToo(t *testing.T) {
	root := project(t, "")
	recordWithLineage(t, root, "docs/facets", "ui/panel/data", "active",
		"  depended_on_by:\n    - ui/panel/gone\n")
	writeFile(t, filepath.Join(root, "src", "data.ts"), "// @facet: ui/panel/data\n")

	if got := findings(lintProject(t, root), "dependencies"); len(got) != 1 {
		t.Fatalf("want one finding, got %+v", got)
	}
}

// A dependency across categories is ordinary — a feature answers to a policy —
// and must not be reported merely because the target is not a feature.
func TestDependencyOnAPolicyResolves(t *testing.T) {
	root := project(t, "")
	recordWithLineage(t, root, "docs/facets", "ui/panel/data", "active",
		"  depends_on:\n    - policy/legal/retention\n")
	writeFile(t, filepath.Join(root, "docs", "facets", "policies", "legal", "retention.yaml"),
		"schema_version: \"0.1\"\nid: policy/legal/retention\nlifecycle: active\n")
	writeFile(t, filepath.Join(root, "src", "data.ts"), "// @facet: ui/panel/data\n")

	if got := findings(lintProject(t, root), "dependencies"); len(got) != 0 {
		t.Fatalf("a policy dependency was reported: %+v", got)
	}
}

// In a monorepo the products depend on each other on purpose, and the reference
// carries no registry prefix to say which one it means. Judged against the whole
// project, it resolves.
func TestDependencyAcrossRegistriesResolves(t *testing.T) {
	root := project(t, twoRegistries)
	recordWithLineage(t, root, "apps/storefront/docs/facets", "shop/cart/total", "active",
		"  depends_on:\n    - admin/pricing/rules\n")
	record(t, root, "apps/admin/docs/facets", "admin/pricing/rules", "active")
	writeFile(t, filepath.Join(root, "apps", "storefront", "cart.go"), "// @facet: shop/cart/total\npackage a\n")
	writeFile(t, filepath.Join(root, "apps", "admin", "rules.go"), "// @facet: admin/pricing/rules\npackage b\n")

	if got := findings(lintProject(t, root), "dependencies"); len(got) != 0 {
		t.Fatalf("a cross-registry dependency was reported: %+v", got)
	}
}

// superseded_by is single-valued, unlike depends_on and depended_on_by — the
// same dangling-reference rule has to cover it via the
// string branch lineageIDs gains for this, not just the []any branch the
// other two fields use.
func TestSupersededByOnAMissingRecordIsAnError(t *testing.T) {
	root := project(t, "")
	recordWithLineage(t, root, "docs/facets", "crm/contacts/minimal-store", "deprecated",
		"  superseded_by: crm/contacts/never-written\n")
	writeFile(t, filepath.Join(root, "src", "store.rs"), "// @facet: crm/contacts/minimal-store\n")

	got := findings(lintProject(t, root), "dependencies")
	if len(got) != 1 {
		t.Fatalf("want one finding, got %+v", got)
	}
	if got[0].Severity != SeverityError {
		t.Errorf("want an error, got %q", got[0].Severity)
	}
	if !strings.Contains(got[0].Message, "crm/contacts/never-written") {
		t.Errorf("the finding must name the missing id: %q", got[0].Message)
	}
}

// A superseded_by naming a record that exists resolves like any other
// lineage reference — the dependencies check has nothing to say about it.
func TestSupersededByOnAnExistingRecordResolves(t *testing.T) {
	root := project(t, "")
	recordWithLineage(t, root, "docs/facets", "crm/contacts/minimal-store", "deprecated",
		"  superseded_by: crm/contacts/crud\n")
	record(t, root, "docs/facets", "crm/contacts/crud", "active")
	writeFile(t, filepath.Join(root, "src", "crud.rs"), "// @facet: crm/contacts/crud\n")

	if got := findings(lintProject(t, root), "dependencies"); len(got) != 0 {
		t.Fatalf("a resolving superseded_by was reported: %+v", got)
	}
}

// A record that declares itself replaced and keeps calling itself the
// current implementation in the same breath.
func TestActiveRecordDeclaringSupersededByWarns(t *testing.T) {
	root := project(t, "")
	recordWithLineage(t, root, "docs/facets", "crm/contacts/minimal-store", "active",
		"  superseded_by: crm/contacts/crud\n")
	// Successor left planned so this test isolates check 2 from check 3 —
	// the "still anchored" warning needs the successor to be active too.
	record(t, root, "docs/facets", "crm/contacts/crud", "planned")
	writeFile(t, filepath.Join(root, "src", "store.rs"), "// @facet: crm/contacts/minimal-store\n")

	got := findings(lintProject(t, root), "superseded-by")
	if len(got) != 1 {
		t.Fatalf("want one finding, got %+v", got)
	}
	if got[0].Severity != SeverityWarning {
		t.Errorf("want a warning — a transition period is legitimate, got %q", got[0].Severity)
	}
	if !strings.Contains(got[0].Message, "but is still active") {
		t.Errorf("message must say the record is still active: %q", got[0].Message)
	}
	if !strings.Contains(got[0].Hint, "lifecycle: deprecated") {
		t.Errorf("hint must say how to fix it: %q", got[0].Hint)
	}
}

// A record that already moved to deprecated is doing exactly what the schema
// asks for — the check must not repeat what the record already says about
// itself.
func TestDeprecatedRecordDeclaringSupersededByDoesNotWarnAboutBeingActive(t *testing.T) {
	root := project(t, "")
	recordWithLineage(t, root, "docs/facets", "crm/contacts/minimal-store", "deprecated",
		"  superseded_by: crm/contacts/crud\n")
	record(t, root, "docs/facets", "crm/contacts/crud", "planned")
	writeFile(t, filepath.Join(root, "src", "store.rs"), "// @facet: crm/contacts/minimal-store\n")

	got := findings(lintProject(t, root), "superseded-by")
	for _, f := range got {
		if strings.Contains(f.Message, "but is still active") {
			t.Errorf("a deprecated record was reported as still active: %+v", f)
		}
	}
}

// The successor shipped and the old anchors never came out. The sibling of
// the existing backing check ("unbacked pending X, which is now active") —
// same sentence shape, warning instead of error.
func TestSupersededByStillAnchoredAfterSuccessorGoesActiveWarns(t *testing.T) {
	root := project(t, "")
	recordWithLineage(t, root, "docs/facets", "crm/contacts/minimal-store", "deprecated",
		"  superseded_by: crm/contacts/crud\n")
	record(t, root, "docs/facets", "crm/contacts/crud", "active")
	writeFile(t, filepath.Join(root, "src", "store.rs"), "// @facet: crm/contacts/minimal-store\n")
	writeFile(t, filepath.Join(root, "src", "seed.rs"), "// @facet: crm/contacts/minimal-store\n")
	writeFile(t, filepath.Join(root, "src", "crud.rs"), "// @facet: crm/contacts/crud\n")

	got := findings(lintProject(t, root), "superseded-by")
	var anchoredFinding *Finding
	for i := range got {
		if strings.Contains(got[i].Message, "still anchored") {
			anchoredFinding = &got[i]
		}
	}
	if anchoredFinding == nil {
		t.Fatalf("want a still-anchored finding, got %+v", got)
	}
	if anchoredFinding.Severity != SeverityWarning {
		t.Errorf("want a warning, got %q", anchoredFinding.Severity)
	}
	if !strings.Contains(anchoredFinding.Message, "crm/contacts/crud") {
		t.Errorf("message must name the successor: %q", anchoredFinding.Message)
	}
	if !strings.Contains(anchoredFinding.Message, "2 file(s)") {
		t.Errorf("message must count the anchored files: %q", anchoredFinding.Message)
	}
	if !strings.Contains(anchoredFinding.Hint, "store.rs") || !strings.Contains(anchoredFinding.Hint, "seed.rs") {
		t.Errorf("hint must name the anchored files: %q", anchoredFinding.Hint)
	}
}

// Without an anchor left there is nothing to warn about.
func TestSupersededByWithNoRemainingAnchorsDoesNotWarn(t *testing.T) {
	root := project(t, "")
	recordWithLineage(t, root, "docs/facets", "crm/contacts/minimal-store", "deprecated",
		"  superseded_by: crm/contacts/crud\n")
	record(t, root, "docs/facets", "crm/contacts/crud", "active")
	writeFile(t, filepath.Join(root, "src", "crud.rs"), "// @facet: crm/contacts/crud\n")

	got := findings(lintProject(t, root), "superseded-by")
	for _, f := range got {
		if strings.Contains(f.Message, "still anchored") {
			t.Errorf("a record with no anchors was reported as still anchored: %+v", f)
		}
	}
}

// The successor not being active yet is the ordinary "replacement not fully
// rolled out" state — nothing to warn about until it is live.
func TestSupersededByAnchoredButSuccessorNotActiveDoesNotWarn(t *testing.T) {
	root := project(t, "")
	recordWithLineage(t, root, "docs/facets", "crm/contacts/minimal-store", "deprecated",
		"  superseded_by: crm/contacts/crud\n")
	record(t, root, "docs/facets", "crm/contacts/crud", "planned")
	writeFile(t, filepath.Join(root, "src", "store.rs"), "// @facet: crm/contacts/minimal-store\n")

	got := findings(lintProject(t, root), "superseded-by")
	for _, f := range got {
		if strings.Contains(f.Message, "still anchored") {
			t.Errorf("a not-yet-active successor was reported: %+v", f)
		}
	}
}

// The clean case: no lineage.superseded_by at all produces nothing from this
// check, on any lifecycle.
func TestNoSupersededByProducesNoFindings(t *testing.T) {
	root := project(t, "")
	record(t, root, "docs/facets", "crm/contacts/crud", "active")
	writeFile(t, filepath.Join(root, "src", "crud.rs"), "// @facet: crm/contacts/crud\n")

	if got := findings(lintProject(t, root), "superseded-by"); len(got) != 0 {
		t.Fatalf("a record without superseded_by was reported: %+v", got)
	}
}

// Roles default to related. Missing primary anchors produce a warning,
// not an error, because roles are optional but primary identifies where
// the feature implementation starts.
func TestActiveRecordWithoutAPrimaryAnchorWarns(t *testing.T) {
	root := project(t, "")
	record(t, root, "docs/facets", "ui/panel/data", "active")
	writeFile(t, filepath.Join(root, "src", "data.ts"), "// @facet: ui/panel/data\n")

	got := findings(lintProject(t, root), "anchor-roles")
	if len(got) != 1 {
		t.Fatalf("want one finding, got %+v", got)
	}
	if got[0].Severity != SeverityWarning {
		t.Errorf("want a warning — this must never fail a build on adoption, got %q", got[0].Severity)
	}
}

func TestAPrimaryAnchorSatisfiesTheRoleCheck(t *testing.T) {
	root := project(t, "")
	record(t, root, "docs/facets", "ui/panel/data", "active")
	writeFile(t, filepath.Join(root, "src", "data.ts"),
		"// @facet: ui/panel/data\n// @facet-role: primary\n")

	if got := findings(lintProject(t, root), "anchor-roles"); len(got) != 0 {
		t.Fatalf("a record with a primary anchor was reported: %+v", got)
	}
}

// A record with no anchor at all is the orphan-records error. Saying it twice,
// in two vocabularies, would read as two problems.
func TestRecordWithoutAnyAnchorIsNotAlsoARoleFinding(t *testing.T) {
	root := project(t, "")
	record(t, root, "docs/facets", "ui/panel/data", "active")
	writeFile(t, filepath.Join(root, "src", "other.ts"), "export const x = 1;\n")

	rep := lintProject(t, root)
	if got := findings(rep, "anchor-roles"); len(got) != 0 {
		t.Fatalf("want the orphan-records error alone, got %+v", got)
	}
	if got := findings(rep, "orphan-records"); len(got) != 1 {
		t.Fatalf("want the orphan-records error, got %+v", got)
	}
}

// A nil slice marshals to `null`, and a JSON client that unconditionally
// iterates `findings` breaks on exactly the case a lint
// run is supposed to celebrate: nothing wrong.
func TestCleanProjectFindingsMarshalToAnEmptyArrayNotNull(t *testing.T) {
	root := project(t, "")

	rep := lintProject(t, root)
	if rep.Findings == nil {
		t.Fatal("Findings is nil - a JSON client iterating it unconditionally would crash")
	}

	out, err := json.Marshal(rep.Findings)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "[]" {
		t.Fatalf("want findings to marshal to [], got %s", out)
	}
}

// A direct call to Lint, outside LintAll, is a separate return point with its
// own nil-guard, so it needs its own coverage.
func TestSingleRegistryLintFindingsMarshalToAnEmptyArrayNotNull(t *testing.T) {
	root := project(t, "")
	reg := Open(root)

	rep, err := Lint(reg, LintOptions{Skip: map[string]bool{"schema": true, "knowledge": true}, Today: "2026-07-27"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Findings == nil {
		t.Fatal("Findings is nil on the direct Lint() path - a JSON client iterating it unconditionally would crash")
	}

	out, err := json.Marshal(rep.Findings)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "[]" {
		t.Fatalf("want findings to marshal to [], got %s", out)
	}
}

func TestSelectingARegistryKeepsTheOtherRegistriesAsContext(t *testing.T) {
	root := project(t, twoRegistries)
	recordWithLineage(t, root, "apps/storefront/docs/facets", "shop/cart/total", "active",
		"  depends_on:\n    - admin/pricing/rules\n")
	record(t, root, "apps/admin/docs/facets", "admin/pricing/rules", "active")
	writeFile(t, filepath.Join(root, "apps", "storefront", "cart.go"), "// @facet: shop/cart/total\npackage a\n")
	writeFile(t, filepath.Join(root, "apps", "admin", "rules.go"), "// @facet: admin/pricing/rules\npackage b\n")
	writeFile(t, filepath.Join(root, "packages", "shared", "price.go"), "// @facet: admin/pricing/rules\npackage shared\n")
	// An error that belongs to the other registry, which the selection must leave out.
	writeFile(t, filepath.Join(root, "apps", "admin", "broken.go"), "// @facet: admin/pricing/missing\npackage b\n")

	// The premise: linted on its own, the storefront reads both as dangling.
	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	alone, err := cfg.Select("storefront")
	if err != nil {
		t.Fatal(err)
	}
	isolated, err := Lint(alone, LintOptions{Scan: alone.ScanOptions(), Skip: map[string]bool{"schema": true, "knowledge": true}, Today: "2026-07-27"})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings(isolated, "orphan-ids")) == 0 || len(findings(isolated, "dependencies")) == 0 {
		t.Fatalf("premise: the isolated lint should misread the shared anchor and the dependency, got %+v", isolated.Findings)
	}

	rep, err := LintAll(root, LintOptions{Registry: "storefront", Skip: map[string]bool{"schema": true, "knowledge": true}, Today: "2026-07-27"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rep.Findings {
		if f.Severity == SeverityError {
			t.Errorf("selected storefront lint reported an error: %+v", f)
		}
		if f.Registry != "" && f.Registry != "storefront" {
			t.Errorf("a finding of another registry leaked into the selection: %+v", f)
		}
	}
	if got := rep.Checked.Registries; len(got) != 1 || got[0] != "storefront" {
		t.Errorf("want only storefront checked, got %v", got)
	}
	whole := lintProject(t, root)
	if got := findings(whole, "orphan-ids"); len(got) != 1 || got[0].Registry != "admin" {
		t.Fatalf("premise: the whole project holds the admin error the selection left out, got %+v", got)
	}

	if _, err := LintAll(root, LintOptions{Registry: "nonexistent"}); err == nil || !strings.Contains(err.Error(), "no registry named") {
		t.Errorf("an unknown registry must be refused by name, got %v", err)
	}
}
