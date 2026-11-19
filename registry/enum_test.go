// Fixtures here demonstrate the enum-constraint spellings this file parses;
// none of them implement a feature, so this file opts out of scanning.
// @facet-ignore-file

package facet

import (
	"path/filepath"
	"strings"
	"testing"
)

// dataEntity writes a minimal but schema-shaped data-entity record whose
// canonical_schema.fields is supplied whole, since the enum parser is
// entirely about what lives there.
func dataEntity(t *testing.T, root, id, fields string) {
	t.Helper()
	slug := strings.TrimPrefix(id, "data/")
	path := filepath.Join(root, "docs", "facets", "data-entities", filepath.FromSlash(slug)+".yaml")
	body := "schema_version: \"0.1\"\nid: " + id + "\nretention:\n  policy: retain\nownership:\n  team: test\n"
	if fields != "" {
		body += "canonical_schema:\n  fields:\n" + fields
	}
	writeFile(t, path, body)
}

// loadDataEntity reads back one data-entity record by id, for tests that need
// the decoded Record rather than just the lint report.
func loadDataEntity(t *testing.T, root, id string) Record {
	t.Helper()
	reg := Open(root)
	recs, err := reg.LoadCategory("data-entities")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no data-entity record with id %q", id)
	return Record{}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Recognized constraint spellings normalize to the same ordered value list.
func TestParseEnumConstraintRecognizesFourSpellings(t *testing.T) {
	cases := []struct {
		name       string
		constraint string
		want       []string
	}{
		{"enum pipe with space", "enum: free|pro|enterprise", []string{"free", "pro", "enterprise"}},
		{"values pipe no space", "values:x25519|ed25519|curve25519-identity", []string{"x25519", "ed25519", "curve25519-identity"}},
		{"values bracketed csv", "values:[active,draining,retired]", []string{"active", "draining", "retired"}},
		{"csv prefix", "csv:free,pro,enterprise", []string{"free", "pro", "enterprise"}},
		{"variant prefix", "variant:Exact|Domain|Wildcard", []string{"Exact", "Domain", "Wildcard"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseEnumConstraint(c.constraint)
			if !ok {
				t.Fatalf("ParseEnumConstraint(%q) reported not enum-shaped", c.constraint)
			}
			if !slicesEqual(got, c.want) {
				t.Errorf("ParseEnumConstraint(%q) = %v, want %v", c.constraint, got, c.want)
			}
		})
	}
}

// Most constraints are not enum declarations at all, and that is not a
// defect — the parser must say so rather than fabricate values.
func TestParseEnumConstraintIgnoresOrdinaryConstraints(t *testing.T) {
	for _, c := range []string{"not-null", "unique", "max-length:255", "encrypted-at-rest", "", "   ", "no-colon-at-all"} {
		if got, ok := ParseEnumConstraint(c); ok {
			t.Errorf("ParseEnumConstraint(%q) was recognized as enum-shaped, got %v", c, got)
		}
	}
}

// A constraint whose prefix is recognized but whose value list is empty
// (e.g. a stray "enum:") must not be reported as enum-shaped either.
func TestParseEnumConstraintRejectsEmptyValueList(t *testing.T) {
	for _, c := range []string{"enum:", "enum: ", "values:[]", "csv:,,"} {
		if got, ok := ParseEnumConstraint(c); ok {
			t.Errorf("ParseEnumConstraint(%q) reported values from an empty list: %v", c, got)
		}
	}
}

func TestEnumDeclarationsExtractsAllFourSpellings(t *testing.T) {
	root := t.TempDir()
	dataEntity(t, root, "data/billing/subscription", `    - name: tier
      type: string
      constraints: ["enum: free|pro|enterprise"]
    - name: key_type
      type: string
      constraints: ["values:x25519|ed25519|curve25519-identity"]
    - name: state
      type: string
      constraints: ["values:[active,draining,retired]"]
    - name: kind
      type: string
      constraints: ["csv:a,b,c"]
    - name: match
      type: string
      constraints: ["variant:Exact|Domain|Wildcard"]
    - name: id
      type: uuid
      constraints: ["not-null"]
`)
	rec := loadDataEntity(t, root, "data/billing/subscription")
	got := rec.EnumDeclarations()
	if len(got) != 5 {
		t.Fatalf("want 5 enum-shaped fields, got %d: %+v", len(got), got)
	}

	byField := map[string][]string{}
	for _, d := range got {
		byField[d.Field] = d.Values
		if d.Entity != "data/billing/subscription" {
			t.Errorf("Entity = %q, want data/billing/subscription", d.Entity)
		}
		if d.Path == "" {
			t.Error("Path must be set so a finding can point at the record")
		}
	}
	want := map[string][]string{
		"tier":     {"free", "pro", "enterprise"},
		"key_type": {"x25519", "ed25519", "curve25519-identity"},
		"state":    {"active", "draining", "retired"},
		"kind":     {"a", "b", "c"},
		"match":    {"Exact", "Domain", "Wildcard"},
	}
	for field, values := range want {
		if !slicesEqual(byField[field], values) {
			t.Errorf("field %q = %v, want %v", field, byField[field], values)
		}
	}
}

// A field declaring `type: enum` but writing its values in some fifth
// spelling the parser does not recognize must not be fabricated — the parser
// is tolerant of what it knows, not exhaustive of everything that could mean
// "enum".
func TestEnumDeclarationsSkipsUnrecognizedSpellingEvenWithEnumType(t *testing.T) {
	root := t.TempDir()
	dataEntity(t, root, "data/billing/subscription", `    - name: tier
      type: enum
      constraints: ["one of free, pro, or enterprise"]
`)
	rec := loadDataEntity(t, root, "data/billing/subscription")
	if got := rec.EnumDeclarations(); len(got) != 0 {
		t.Fatalf("a constraint in none of the four spellings must not be parsed, got %+v", got)
	}
}

// A record with no canonical_schema at all — most records before this field
// was populated — must not error.
func TestEnumDeclarationsIsEmptyWithoutCanonicalSchema(t *testing.T) {
	root := t.TempDir()
	dataEntity(t, root, "data/billing/subscription", "")
	rec := loadDataEntity(t, root, "data/billing/subscription")
	if got := rec.EnumDeclarations(); len(got) != 0 {
		t.Fatalf("want nothing, got %+v", got)
	}
}

// The same tier field has free|pro|enterprise on three entities and adds
// suspended on another; normalized sets must expose the divergence.
func TestEnumDivergenceWarnsOnDifferentValueSets(t *testing.T) {
	root := project(t, "")
	dataEntity(t, root, "data/billing/subscription", `    - name: tier
      type: string
      constraints: ["enum: free|pro|enterprise|suspended"]
`)
	dataEntity(t, root, "data/billing/tier-catalogue", `    - name: id
      type: string
      constraints: ["enum: free|pro|enterprise"]
`)
	dataEntity(t, root, "data/identity/subscription-link", `    - name: tier
      type: string
      constraints: ["enum: free|pro|enterprise"]
`)

	rep := lintProject(t, root)
	got := findings(rep, "enum-divergence")
	var warns []Finding
	for _, f := range got {
		if f.Severity == SeverityWarning {
			warns = append(warns, f)
		} else {
			t.Errorf("enum-divergence must never be an error, got %+v", f)
		}
	}
	if len(warns) != 1 {
		t.Fatalf("want one enum-divergence warning, got %+v", got)
	}
	if warns[0].ID != "data/billing/subscription" {
		t.Errorf("finding must name the minority entity, got %q", warns[0].ID)
	}
	if !strings.Contains(warns[0].Message, "tier") {
		t.Errorf("finding must name the field, got %q", warns[0].Message)
	}
	if !strings.Contains(warns[0].Message, "suspended") {
		t.Errorf("finding must show the differing values, got %q", warns[0].Message)
	}
	if !strings.Contains(warns[0].Message, "data/billing/tier-catalogue") &&
		!strings.Contains(warns[0].Message, "data/identity/subscription-link") {
		t.Errorf("finding must name the entity it diverges from, got %q", warns[0].Message)
	}
	if rep.Failed() {
		t.Fatal("enum divergence is a warning, not a broken build")
	}
}

// The sibling case: same field name, same value set (written via
// different spellings and in different declared order) across entities must
// not diverge — this is the ordinary, expected shape.
func TestEnumDivergenceIsCleanWhenValueSetsMatch(t *testing.T) {
	root := project(t, "")
	dataEntity(t, root, "data/billing/subscription", `    - name: status
      type: string
      constraints: ["enum: active|canceled"]
`)
	dataEntity(t, root, "data/identity/subscription-link", `    - name: status
      type: string
      constraints: ["values:active|canceled"]
`)
	// Same set, declared in a different order — order is not meaning.
	dataEntity(t, root, "data/release/license-certificate", `    - name: status
      type: string
      constraints: ["enum: canceled|active"]
`)

	rep := lintProject(t, root)
	if got := findings(rep, "enum-divergence"); len(got) != 0 {
		t.Fatalf("identical value sets across entities must not diverge, got %+v", got)
	}
}

// A field name that appears on only one entity has nothing to diverge from.
func TestEnumDivergenceIsCleanWithASingleDeclaration(t *testing.T) {
	root := project(t, "")
	dataEntity(t, root, "data/billing/subscription", `    - name: tier
      type: string
      constraints: ["enum: free|pro|enterprise"]
`)

	rep := lintProject(t, root)
	if got := findings(rep, "enum-divergence"); len(got) != 0 {
		t.Fatalf("a single declaration has nothing to diverge from, got %+v", got)
	}
}

// The `--skip enum-divergence` escape hatch must actually turn the check
// off.
func TestEnumDivergenceCanBeSkipped(t *testing.T) {
	root := project(t, "")
	dataEntity(t, root, "data/billing/subscription", `    - name: tier
      type: string
      constraints: ["enum: free|pro|enterprise|suspended"]
`)
	dataEntity(t, root, "data/billing/tier-catalogue", `    - name: id
      type: string
      constraints: ["enum: free|pro|enterprise"]
`)

	rep, err := LintAll(root, LintOptions{
		Skip:  map[string]bool{"schema": true, "knowledge": true, "enum-divergence": true},
		Today: "2026-07-27",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := findings(rep, "enum-divergence"); len(got) != 0 {
		t.Fatalf("--skip enum-divergence must suppress the check entirely, got %+v", got)
	}
}

// The baseline a divergent minority is judged against is the largest group,
// not the first one the sort happens to reach. Chosen so the two rules
// disagree: 'archived|free|pro' sorts before 'enterprise|free|pro' as a
// normalized key, so a lexicographic-only tie-break would report the two
// entities that agree and let the singleton stand as correct.
func TestEnumDivergenceJudgesAgainstTheLargestGroup(t *testing.T) {
	root := project(t, "")
	dataEntity(t, root, "data/billing/subscription", `    - name: tier
      type: string
      constraints: ["enum: free|pro|enterprise"]
`)
	dataEntity(t, root, "data/billing/invoice", `    - name: tier
      type: string
      constraints: ["enum: free|pro|enterprise"]
`)
	dataEntity(t, root, "data/identity/legacy-account", `    - name: tier
      type: string
      constraints: ["enum: free|pro|archived"]
`)

	got := findings(lintProject(t, root), "enum-divergence")
	if len(got) != 1 {
		t.Fatalf("want the one divergent entity reported, got %+v", got)
	}
	if got[0].ID != "data/identity/legacy-account" {
		t.Fatalf("the minority of one must be the finding, got %q", got[0].ID)
	}
	if !strings.Contains(got[0].Message, "data/billing/invoice") ||
		!strings.Contains(got[0].Message, "data/billing/subscription") {
		t.Errorf("the message must name the whole baseline group, got %q", got[0].Message)
	}
}
