package facet

import (
	"path/filepath"
	"sync"
	"testing"
)

// A product document stands for any declaration a linked product keeps in the
// registry. Registration is process-wide, so the test binary registers it once.
const exampleProductSchema = "example-product.schema.json"

func exampleProductPath(reg Registry) string {
	return filepath.Join(reg.RecordsDir, "example-product", "example-product.yaml")
}

var registerExampleProductOnce sync.Once

func registerExampleProductDocument() {
	registerExampleProductOnce.Do(func() {
		RegisterProductDocument(ProductDocument{Path: exampleProductPath, Schema: exampleProductSchema})
	})
}

// The full lint holds a registered product document to its schema, and leaves
// it alone where the project carries no such schema.
func TestLintValidatesARegisteredProductDocument(t *testing.T) {
	registerExampleProductDocument()
	root := project(t, "")
	reg := Open(root)
	writeFile(t, exampleProductPath(reg), "invalid: true\n")

	rep, err := Lint(reg, LintOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range rep.Findings {
		if finding.Path == reg.Rel(exampleProductPath(reg)) {
			t.Fatalf("a document without its schema in the project must not be validated: %+v", finding)
		}
	}

	writeFile(t, filepath.Join(reg.SchemaDir, exampleProductSchema), `{"type":"object","required":["valid"]}`)
	rep, err = Lint(reg, LintOptions{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, finding := range rep.Findings {
		if finding.Check == "schema" && finding.Path == reg.Rel(exampleProductPath(reg)) {
			found = true
		}
	}
	if !found {
		t.Fatalf("the registered document was not validated against its schema: %+v", rep.Findings)
	}
}

func TestRegisterProductDocumentRefusesAnIncompleteDocument(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a document without a schema must panic")
		}
	}()
	RegisterProductDocument(ProductDocument{Path: exampleProductPath})
}

var registerExampleProseOnce sync.Once

// A glossary rename names, after the records, the files a registered prose
// source says still write the old spelling — and only those.
func TestAGlossaryRenameNamesWhatARegisteredProseSourceStillSays(t *testing.T) {
	registerExampleProseOnce.Do(func() {
		RegisterProseSource(func(reg Registry, term string) []string {
			if term != "Consignee" {
				return nil
			}
			return []string{"notes/handover.md"}
		})
	})
	root := project(t, "")
	reg := Open(root)
	writeFile(t, reg.GlossaryPath(), "schema_version: \"0.1\"\nterms:\n  - term: Consignee\n    definition: The party goods are delivered to.\n")

	renamed, err := RenameGlossaryTerm(reg, reg.GlossaryPath(), "Consignee", "Recipient")
	if err != nil {
		t.Fatal(err)
	}
	if len(renamed.StillSaying) != 1 || renamed.StillSaying[0] != "notes/handover.md" {
		t.Fatalf("the rename must name the prose source's file, got %v", renamed.StillSaying)
	}
}
