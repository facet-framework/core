package facet

import (
	"path/filepath"
	"strings"
	"testing"
)

// conventionsOf resolves every registry of a project and returns them keyed by
// name, so a case can ask about one registry without caring about order.
func conventionsOf(t *testing.T, root string) map[string]Registry {
	t.Helper()
	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Registry{}
	for _, reg := range cfg.All() {
		out[reg.Name] = reg
	}
	return out
}

// Absence is the ordinary case and must change nothing: no path, no source,
// and no finding from lint.
func TestNoConventionsDocumentResolvesToNothing(t *testing.T) {
	root := project(t, "")

	reg := conventionsOf(t, root)[""]
	if reg.ConventionsPath != "" || reg.ConventionsSource != "" {
		t.Fatalf("a project without a conventions document must report none, got %q from %q",
			reg.ConventionsPath, reg.ConventionsSource)
	}
}

// The smallest loading convention: a file at the repository root, by name,
// with nothing declared anywhere.
func TestTheRootFileIsFoundWithoutConfiguration(t *testing.T) {
	root := project(t, "")
	writeFile(t, filepath.Join(root, "FACET-CONVENTIONS.md"), "# Conventions\n")

	reg := conventionsOf(t, root)[""]
	if want := filepath.Join(root, "FACET-CONVENTIONS.md"); reg.ConventionsPath != want {
		t.Fatalf("conventions = %q, want %q", reg.ConventionsPath, want)
	}
	if reg.ConventionsSource != ConventionsFromDefault {
		t.Errorf("source = %q, want %q", reg.ConventionsSource, ConventionsFromDefault)
	}
	// The premise of the rest of the suite: the relative form is what every
	// surface prints, so it has to be the repository-relative one.
	if got := reg.Rel(reg.ConventionsPath); got != "FACET-CONVENTIONS.md" {
		t.Errorf("relative path = %q", got)
	}
}

// A declared path wins over the root file, so a project can keep its
// conventions beside its records without a second file being picked up.
func TestADeclaredPathWinsOverTheRootFile(t *testing.T) {
	root := project(t, "conventions: docs/facets/CONVENTIONS.md\n")
	writeFile(t, filepath.Join(root, "FACET-CONVENTIONS.md"), "# root\n")
	writeFile(t, filepath.Join(root, "docs/facets/CONVENTIONS.md"), "# declared\n")

	reg := conventionsOf(t, root)[""]
	if want := filepath.Join(root, "docs/facets/CONVENTIONS.md"); reg.ConventionsPath != want {
		t.Fatalf("conventions = %q, want the declared %q", reg.ConventionsPath, want)
	}
	if reg.ConventionsSource != ConventionsFromProject {
		t.Errorf("source = %q, want %q", reg.ConventionsSource, ConventionsFromProject)
	}
}

// Several registries share the project's document unless one deliberately
// names its own, and the override replaces rather than merges: two prose
// documents merged by a tool would be a third document nobody wrote.
func TestARegistryOverridesTheSharedDocumentAndTheOthersKeepIt(t *testing.T) {
	root := project(t, `
conventions: CONVENTIONS.md
registries:
  storefront:
    records: apps/storefront/docs/facets
    conventions: apps/storefront/CONVENTIONS.md
  admin:
    records: apps/admin/docs/facets
`)
	writeFile(t, filepath.Join(root, "CONVENTIONS.md"), "# shared\n")
	writeFile(t, filepath.Join(root, "apps/storefront/CONVENTIONS.md"), "# storefront\n")

	regs := conventionsOf(t, root)
	if got, want := regs["admin"].ConventionsPath, filepath.Join(root, "CONVENTIONS.md"); got != want {
		t.Errorf("admin conventions = %q, want the shared %q", got, want)
	}
	if regs["admin"].ConventionsSource != ConventionsFromProject {
		t.Errorf("admin source = %q", regs["admin"].ConventionsSource)
	}
	if got, want := regs["storefront"].ConventionsPath, filepath.Join(root, "apps/storefront/CONVENTIONS.md"); got != want {
		t.Errorf("storefront conventions = %q, want its own %q", got, want)
	}
	if regs["storefront"].ConventionsSource != ConventionsFromRegistry {
		t.Errorf("storefront source = %q", regs["storefront"].ConventionsSource)
	}
}

// A path outside the repository would make the effective document depend on
// the machine reading it — exactly what .facetrc.yaml exists to prevent.
func TestAConventionsPathOutsideTheRepositoryIsRefused(t *testing.T) {
	for _, bad := range []string{"/etc/conventions.md", "../shared/CONVENTIONS.md", "docs/../../x.md"} {
		if _, err := LoadConfig(project(t, "conventions: "+bad+"\n")); err == nil {
			t.Errorf("conventions %q was accepted, but it is not inside the repository", bad)
		}
		cfg := "registries:\n  admin:\n    records: a/facets\n    conventions: " + bad + "\n"
		if _, err := LoadConfig(project(t, cfg)); err == nil {
			t.Errorf("registry conventions %q was accepted", bad)
		}
	}
	if _, err := LoadConfig(project(t, "conventions: docs/CONVENTIONS.md\n")); err != nil {
		t.Errorf("an ordinary repository path was refused: %v", err)
	}
}

// A declared document that does not exist is an error: the declaration looks
// configured and behaves as though absent, which is the worst shape a key can
// take.
func TestADeclaredConventionsDocumentThatIsMissingIsAnError(t *testing.T) {
	root := project(t, "conventions: docs/CONVENTIONS.md\n")
	writeFile(t, filepath.Join(root, "docs/facets/features/.keep"), "")

	rep, err := LintAll(root, LintOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var found *Finding
	for i, f := range rep.Findings {
		if f.Check == "conventions" {
			found = &rep.Findings[i]
		}
	}
	if found == nil {
		t.Fatalf("want a conventions finding, got %+v", rep.Findings)
	}
	if found.Severity != SeverityError {
		t.Errorf("severity = %q, want error", found.Severity)
	}
	if !strings.Contains(found.Message, "docs/CONVENTIONS.md") {
		t.Errorf("the finding must name the path it could not find: %q", found.Message)
	}

	// And the same project with the file present is silent — otherwise the
	// assertion above would hold for a check that fires unconditionally.
	writeFile(t, filepath.Join(root, "docs/CONVENTIONS.md"), "# ok\n")
	rep, err = LintAll(root, LintOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rep.Findings {
		if f.Check == "conventions" {
			t.Errorf("a present document must not be reported: %+v", f)
		}
	}
}

// Absence produces no conventions finding, and the root file is never a
// declaration that could be "missing".
func TestNoConventionsProducesNoFinding(t *testing.T) {
	root := project(t, "")
	writeFile(t, filepath.Join(root, "docs/facets/features/.keep"), "")

	rep, err := LintAll(root, LintOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rep.Findings {
		if f.Check == "conventions" {
			t.Errorf("no conventions document must mean no finding: %+v", f)
		}
	}
}

// The schema refuses a path that leaves the repository, beside LoadConfig,
// because the shell suite validates the file with ajv and never runs LoadConfig.
func TestTheSchemaRefusesAConventionsPathOutsideTheRepository(t *testing.T) {
	root := project(t, "")
	copySchemas(t, root)
	compiled, err := compileSchema(filepath.Join(root, "docs/facets/schema/facetrc.schema.json"))
	if err != nil {
		t.Fatalf("the shipped config schema does not compile: %v", err)
	}
	for _, p := range []string{"../x.md", "docs/../../x.md", "/etc/x.md", "a/.."} {
		if compiled.Validate(map[string]any{"conventions": p}) == nil {
			t.Errorf("the schema accepted conventions %q", p)
		}
	}
	for _, p := range []string{"CONVENTIONS.md", "docs/facets/CONVENTIONS.md", "a..b/c.md"} {
		if err := compiled.Validate(map[string]any{"conventions": p}); err != nil {
			t.Errorf("the schema refused the ordinary path %q: %v", p, err)
		}
	}
}

// The key is new on a schema whose levels are additionalProperties: false, so
// without a schema change declaring it would fail the run it exists to help.
func TestConventionsKeysPassTheRealSchema(t *testing.T) {
	for _, cfg := range []string{
		"schema_version: \"0.1\"\nconventions: CONVENTIONS.md\n",
		"schema_version: \"0.1\"\nconventions: CONVENTIONS.md\nregistries:\n  admin:\n    records: docs/facets\n    conventions: apps/admin/CONVENTIONS.md\n",
	} {
		root := project(t, cfg)
		copySchemas(t, root)
		writeFile(t, filepath.Join(root, "CONVENTIONS.md"), "# shared\n")
		writeFile(t, filepath.Join(root, "apps/admin/CONVENTIONS.md"), "# admin\n")

		rep, err := LintAll(root, LintOptions{Skip: map[string]bool{"knowledge": true}})
		if err != nil {
			t.Fatal(err)
		}
		// Premise: the schema really was in place and compiles, or a clean run
		// proves nothing. lintConfig skips a schema that will not compile in
		// silence, so the check is that the same schema rejects a value it must.
		if !isFile(filepath.Join(root, "docs/facets/schema/facetrc.schema.json")) {
			t.Fatal("the shipped config schema was not copied; this case would be vacuous")
		}
		if _, err := compileSchema(filepath.Join(root, "docs/facets/schema/facetrc.schema.json")); err != nil {
			t.Fatalf("the shipped config schema does not compile, so it validates nothing: %v", err)
		}
		for _, f := range rep.Findings {
			if f.Check == "schema" || f.Check == "config" || f.Check == "conventions" {
				t.Errorf("config %q must validate, got %+v", cfg, f)
			}
		}
	}
}
