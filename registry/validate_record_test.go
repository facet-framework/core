// @facet-ignore-file

package facet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func savedRecord(t *testing.T, root, id, body string) string {
	t.Helper()
	path := filepath.Join(root, "docs", "facets", "features", filepath.FromSlash(id)+".yaml")
	writeFile(t, path, "schema_version: \"0.1\"\nid: "+id+"\nlifecycle: planned\n"+body)
	return path
}

func hasFinding(rep Report, check, phrase string) bool {
	for _, finding := range rep.Findings {
		if finding.Check == check && strings.Contains(finding.Message, phrase) {
			return true
		}
	}
	return false
}

func TestValidateRecordKeepsLocalTaxonomyVariantAndBackingDiagnostics(t *testing.T) {
	root := project(t, "")
	reg := Open(root)
	writeFile(t, reg.TaxonomyPath(), "schema_version: \"0.1\"\ndomains:\n  billing:\n    title: Billing\n    capabilities:\n      invoice:\n        title: Invoice\n")
	writeFile(t, reg.VariantMapPath(), "schema_version: \"0.1\"\nvariants:\n  web:\n    paths: [web/**]\n")
	path := savedRecord(t, root, "ledger/invoice/list", `variants:
  mobile:
    lifecycle: planned
contract:
  scenarios:
    - id: data
      backing:
        by_variant:
          mobile:
            state: unbacked
            reason: data is absent
            unblocked_by: ledger/data/source
`)

	rep, err := ValidateRecord(reg, path)
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(rep, "taxonomy", "taxonomy does not declare") {
		t.Fatalf("saved record lost taxonomy diagnosis: %+v", rep.Findings)
	}
	if !hasFinding(rep, "variants", "record declares variant") {
		t.Fatalf("saved record lost variant-map diagnosis: %+v", rep.Findings)
	}
	if !hasFinding(rep, "backing", "backing for variant") {
		t.Fatalf("saved record lost backing variant diagnosis: %+v", rep.Findings)
	}
}

func TestValidateRecordSchemaChecksRegistryDocuments(t *testing.T) {
	root := project(t, "")
	reg := Open(root)
	registerExampleProductDocument()
	for _, name := range []string{"generation-manifest.schema.json", "taxonomy.schema.json", "glossary.schema.json", "variants.schema.json", exampleProductSchema} {
		writeFile(t, filepath.Join(reg.SchemaDir, name), `{"type":"object","required":["valid"]}`)
	}
	for _, path := range []string{reg.ManifestPath(), reg.TaxonomyPath(), reg.GlossaryPath(), reg.VariantMapPath(), exampleProductPath(reg)} {
		writeFile(t, path, "invalid: true\n")
		rep, err := ValidateRecord(reg, path)
		if err != nil || !hasFinding(rep, "schema", "valid") {
			t.Fatalf("%s was not schema-checked on save: %+v, %v", path, rep.Findings, err)
		}
	}
}

func TestValidateRecordKeepsLocalGlossaryAndVariantDiagnostics(t *testing.T) {
	root := project(t, "")
	reg := Open(root)
	writeFile(t, reg.GlossaryPath(), `schema_version: "0.1"
categories:
  - id: parties
    title: Parties
  - id: parties
    title: Duplicate parties
terms:
  - term: Consignee
    category: partys
    definition: The party receiving goods.
  - term: Consignee
    category: parties
    definition: A duplicate definition.
`)
	rep, err := ValidateRecord(reg, reg.GlossaryPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []string{"duplicate-glossary-term", "duplicate-glossary-category", "undeclared-glossary-category"} {
		if !hasFinding(rep, check, "") {
			t.Fatalf("saved glossary lost %s diagnosis: %+v", check, rep.Findings)
		}
	}

	writeFile(t, reg.VariantMapPath(), `schema_version: "0.1"
variants:
  v2-api:
    paths: ["v2/api/**"]
    replaces: legacy-api
`)
	rep, err = ValidateRecord(reg, reg.VariantMapPath())
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(rep, "variants", "replaces") {
		t.Fatalf("saved variant map lost replaces diagnosis: %+v", rep.Findings)
	}
}

func TestValidateRecordReportsMalformedRootDeclarationsWithoutSchemas(t *testing.T) {
	root := project(t, "")
	reg := Open(root)
	for _, declaration := range []struct {
		name  string
		path  string
		check string
	}{
		{name: "taxonomy", path: reg.TaxonomyPath(), check: "taxonomy"},
		{name: "glossary", path: reg.GlossaryPath(), check: "glossary"},
		{name: "variants", path: reg.VariantMapPath(), check: "variants"},
	} {
		t.Run(declaration.name, func(t *testing.T) {
			writeFile(t, declaration.path, "broken: [yaml\n")
			rep, err := ValidateRecord(reg, declaration.path)
			if err != nil {
				t.Fatal(err)
			}
			if !hasFinding(rep, declaration.check, "could not be read") {
				t.Fatalf("saved %s lost parse diagnosis without a schema: %+v", declaration.name, rep.Findings)
			}
		})
	}
}

func TestVerificationTargetOnlyReadsSubmodulesForMissingFiles(t *testing.T) {
	root := project(t, "")
	reg := Open(root)
	path := savedRecord(t, root, "ui/panel/data", `contract:
  scenarios:
    - id: shown
      statement: Then shown
verification:
  unit_tests:
    - id: tests/data_test.go
      proves: [shown]
`)
	writeFile(t, filepath.Join(root, "tests", "data_test.go"), "package tests\n")
	called := false
	original := findUninitializedSubmodules
	findUninitializedSubmodules = func(string) []string { called = true; return nil }
	t.Cleanup(func() { findUninitializedSubmodules = original })
	if _, err := ValidateRecord(reg, path); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("an existing verification target must not read the Git index")
	}
}

func BenchmarkValidateRecordSiblingCounts(b *testing.B) {
	for _, siblings := range []int{1, 100, 1000} {
		b.Run(fmt.Sprintf("siblings=%d", siblings), func(b *testing.B) {
			root := b.TempDir()
			reg := Open(root)
			target := filepath.Join(root, "docs", "facets", "features", "sample", "source", "target.yaml")
			write := func(path, id string) {
				writeFileForBenchmark(b, path, "schema_version: \"0.1\"\nid: "+id+"\nlifecycle: planned\n")
			}
			write(target, "sample/source/target")
			for i := 0; i < siblings; i++ {
				id := fmt.Sprintf("sample/source/sibling-%04d", i)
				write(filepath.Join(root, "docs", "facets", "features", filepath.FromSlash(id)+".yaml"), id)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := ValidateRecord(reg, target); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func writeFileForBenchmark(b *testing.B, path, body string) {
	b.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		b.Fatal(err)
	}
}
