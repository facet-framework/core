package spec

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestSpecGherkinMarkdownMatchesClassic(t *testing.T) {
	cases := map[string]string{
		"minimal":    "Feature: Minimal\nScenario: minimalistic\nGiven the minimalism\n",
		"datatables": "Feature: DataTables\nScenario: minimalistic\nGiven a simple data table\n| foo | bar |\n| boz | boo |\n",
		"docstrings": "Feature: DocString variations\nScenario: minimalistic\nAnd a DocString with an implicitly escaped separator inside\n\"\"\"\n```\n\"\"\"\n",
		"misc":       "Feature: Markdown document without \"# Feature:\" header\nScenario: Something about math\nGiven step one\nWhen step two\nThen step three\nScenario: Something about gravity\nGiven step one\nWhen step two\nThen step three\n",
		"tags":       "Feature: Minimal Scenario Outline\nScenario: minimalistic\nGiven the minimalism\nScenario Outline: minimalistic outline\nGiven the <what>\nExamples:\n| what |\n| minimalism |\nExamples:\n| what |\n| more minimalism |\nScenario: comments\nScenario: hash in tags\nRule:\nScenario: joined tags\n",
	}
	heading := regexp.MustCompile(`(?m)^(?:#{1,6} )?Scenario(?: Outline)?:`)

	const backgrounds = "# Feature: Backgrounds\n## Background:\n- Given a store\n## Rule: Checkout\n### Background:\n+ Given a cart\n### Scenario: Place order\n* When I submit `@order`\n* Then checkout confirms the order\n"
	cases["backgrounds"] = "Feature: Backgrounds\nBackground:\nGiven a store\nRule: Checkout\nBackground:\nGiven a cart\nScenario: Place order\nWhen I submit `@order`\nThen checkout confirms the order\n"
	for name, classic := range cases {
		t.Run(name, func(t *testing.T) {
			md := backgrounds
			if name != "backgrounds" {
				data, err := os.ReadFile(filepath.Join("testdata/cucumber-markdown", name+".feature.md"))
				if err != nil {
					t.Fatal(err)
				}
				md = string(data)
			}
			root := t.TempDir()
			read := func(suffix, body string) Document {
				t.Helper()
				next := 'a'
				body = heading.ReplaceAllStringFunc(body, func(header string) string {
					tag := "@case:" + string(next)
					next++
					if suffix == ".feature.md" {
						tag = "`" + tag + "`"
					}
					return tag + "\n" + header
				})
				path := filepath.Join(root, name+suffix)
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				doc, findings := ReadFile(root, path)
				if len(findings) != 0 || len(doc.Units) == 0 {
					t.Fatalf("%s: units=%+v, findings=%v", path, doc.Units, findings)
				}
				return doc
			}
			got, want := read(".feature.md", md), read(".feature", classic)
			for i := range got.Units {
				got.Units[i].Line = 0
			}
			for i := range want.Units {
				want.Units[i].Line = 0
			}
			if got.ID != name || got.Language != want.Language || !reflect.DeepEqual(got.Units, want.Units) {
				t.Fatalf("Markdown units differ from classic Gherkin\n%+v\n%+v", got.Units, want.Units)
			}
			if name == "tags" && !strings.Contains(got.blocks[0].Body, "@comment_tag#2") {
				t.Fatal("tag with a hash was lost")
			}
		})
	}
}

func TestSpecGherkinMarkdownKeepsDashCellsAsData(t *testing.T) {
	root := t.TempDir()
	md := "# Feature: Codes\n`@case:codes`\n## Scenario Outline: Codes\n* Given the code <code> and <note>\n## Examples:\n  | code | note |\n  | ---- | ---- |\n  | - | y |\n  | x | - |\n  | x | --- |\n"
	classic := "Feature: Codes\n@case:codes\nScenario Outline: Codes\nGiven the code <code> and <note>\nExamples:\n| code | note |\n| - | y |\n| x | - |\n| x | --- |\n"
	read := func(name, body string) Document {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		doc, findings := ReadFile(root, path)
		if len(findings) != 0 || len(doc.Units) != 1 {
			t.Fatalf("%s: units=%+v findings=%v", name, doc.Units, findings)
		}
		return doc
	}
	got, want := read("codes.feature.md", md), read("codes.feature", classic)
	if rows := len(want.Units[0].Sequence.Examples[0]); rows != 4 {
		t.Fatalf("classic twin does not carry a header and three data rows: %d", rows)
	}
	if got.Units[0].Digest != want.Units[0].Digest || !reflect.DeepEqual(got.Units[0].Sequence.Examples, want.Units[0].Sequence.Examples) {
		t.Fatalf("dash cells were not kept as data:\n got %v\nwant %v", got.Units[0].Sequence.Examples, want.Units[0].Sequence.Examples)
	}
}

func TestSpecGaugeNeedsItsSpecsTree(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) string {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	rpm := write("pkg/foo.spec", "Name: foo\nVersion: 1.0\n")
	if doc, findings := ReadFile(root, rpm); !doc.Skipped || len(findings) != 0 {
		t.Fatalf("a .spec outside specs/ must be skipped: %+v %v", doc, findings)
	}
	notes := write("specs/notes.md", "# Notes\n\n## Open points\n\n* ask the owner\n")
	if doc, findings := ReadFile(root, notes); !doc.Skipped || len(findings) != 0 {
		t.Fatalf("plain markdown under specs/ must not be read as Gauge: %+v %v", doc, findings)
	}
	write("manifest.json", "{\"Language\": \"js\"}\n")
	if doc, findings := ReadFile(root, write("specs/cart.md", "Cart\n====\n\nAdd item\n--------\n\n* Add an item\n")); doc.Skipped || len(findings) != 0 || len(doc.Units) != 1 {
		t.Fatalf("Markdown in a Gauge project was not read as Gauge: %+v %v", doc, findings)
	}
	gauge := write("specs/cart.spec", "# Cart\n\n## Add item\nTags: id:add-item\n\n* Add an item\n* The cart has one item\n")
	if doc, findings := ReadFile(root, gauge); doc.Skipped || len(findings) != 0 || len(doc.Units) != 1 || doc.Units[0].Strength() != Provenance {
		t.Fatalf("a Gauge spec under specs/ was not read: %+v %v", doc, findings)
	}
}
