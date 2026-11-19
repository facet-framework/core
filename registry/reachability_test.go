// Fixtures below contain example @facet: tags; this file demonstrates the
// syntax rather than implementing a feature.
// @facet-ignore-file

package facet

import (
	"path/filepath"
	"strings"
	"testing"
)

// Anchoring and unit tests do not prove product wiring: warn when only tests
// use the implementation of an active record.
func TestActiveRecordReachableOnlyFromTestsIsWarned(t *testing.T) {
	root := project(t, "")
	record(t, root, "docs/facets", "sim/wave/generation", "active")
	writeFile(t, filepath.Join(root, "src", "wave.go"), `// @facet: sim/wave/generation
// @facet-role: primary
func GenerateWave() {}
`)
	writeFile(t, filepath.Join(root, "src", "wave_test.go"), `func TestGenerateWave(t *testing.T) {
	GenerateWave()
}
`)

	got := findings(lintProject(t, root), "test-only-anchors")
	if len(got) != 1 {
		t.Fatalf("want one test-only-anchors warning, got %+v", got)
	}
	if got[0].Severity != SeverityWarning {
		t.Errorf("must be a warning, not an error — got %q", got[0].Severity)
	}
	if got[0].ID != "sim/wave/generation" {
		t.Errorf("want the finding on sim/wave/generation, got %q", got[0].ID)
	}
}

func TestActiveRecordCalledFromProductionCodeIsNotWarned(t *testing.T) {
	root := project(t, "")
	record(t, root, "docs/facets", "sim/wave/generation", "active")
	writeFile(t, filepath.Join(root, "src", "wave.go"), `// @facet: sim/wave/generation
// @facet-role: primary
func GenerateWave() {}
`)
	writeFile(t, filepath.Join(root, "src", "wave_test.go"), `func TestGenerateWave(t *testing.T) {
	GenerateWave()
}
`)
	writeFile(t, filepath.Join(root, "src", "match.go"), `func startMatch() {
	GenerateWave()
}
`)

	if got := findings(lintProject(t, root), "test-only-anchors"); len(got) != 0 {
		t.Fatalf("a production caller exists — want no finding, got %+v", got)
	}
}

// A supporting (non-primary) anchor is not where a feature "starts" — see
// lintAnchorRoles. Without a primary anchor this check has nothing reliable
// to judge and must stay silent; the missing role is anchor-roles' finding.
func TestOnlyPrimaryAnchorsAreEvaluatedForReachability(t *testing.T) {
	root := project(t, "")
	record(t, root, "docs/facets", "sim/wave/generation", "active")
	writeFile(t, filepath.Join(root, "src", "wave.go"), `// @facet: sim/wave/generation
func GenerateWave() {}
`)
	writeFile(t, filepath.Join(root, "src", "wave_test.go"), `func TestGenerateWave(t *testing.T) {
	GenerateWave()
}
`)

	if got := findings(lintProject(t, root), "test-only-anchors"); len(got) != 0 {
		t.Fatalf("without a primary anchor this check must stay silent, got %+v", got)
	}
}

// A planned record makes no claim about production reachability — that is
// exactly what planned means, and it is the escape hatch from this check.
func TestPlannedRecordIsNeverFlaggedByReachability(t *testing.T) {
	root := project(t, "")
	record(t, root, "docs/facets", "sim/wave/generation", "planned")
	writeFile(t, filepath.Join(root, "src", "wave.go"), `// @facet: sim/wave/generation
// @facet-role: primary
func GenerateWave() {}
`)
	writeFile(t, filepath.Join(root, "src", "wave_test.go"), `func TestGenerateWave(t *testing.T) {
	GenerateWave()
}
`)

	if got := findings(lintProject(t, root), "test-only-anchors"); len(got) != 0 {
		t.Fatalf("a planned record must never be flagged, got %+v", got)
	}
}

// Nothing follows the anchor for the naive parser to read a symbol name from.
// It must give up rather than guess, and must never panic doing it.
func TestReachabilitySkipsADeclarationItCannotParse(t *testing.T) {
	root := project(t, "")
	record(t, root, "docs/facets", "sim/wave/generation", "active")
	writeFile(t, filepath.Join(root, "src", "wave.go"), `// @facet: sim/wave/generation
// @facet-role: primary
`)

	if got := findings(lintProject(t, root), "test-only-anchors"); len(got) != 0 {
		t.Fatalf("an anchor with nothing to parse must not be flagged, got %+v", got)
	}
}

// A doc comment commonly sits between the anchor and the declaration it
// documents (this repo's own primary anchors are written exactly this way).
// The parser must look past it rather than stopping on the first comment
// line it meets.
func TestReachabilityLooksPastADocCommentForTheDeclaration(t *testing.T) {
	root := project(t, "")
	record(t, root, "docs/facets", "sim/wave/generation", "active")
	writeFile(t, filepath.Join(root, "src", "wave.go"), `// @facet: sim/wave/generation
// @facet-role: primary
// GenerateWave builds the next wave of enemies for the arena mode.
func GenerateWave() {}
`)
	writeFile(t, filepath.Join(root, "src", "wave_test.go"), `func TestGenerateWave(t *testing.T) {
	GenerateWave()
}
`)

	got := findings(lintProject(t, root), "test-only-anchors")
	if len(got) != 1 {
		t.Fatalf("the doc comment must not stop the parser from finding GenerateWave, got %+v", got)
	}
}

// A record is a description, never a caller.
func TestRecordProseIsNotACaller(t *testing.T) {
	root := project(t, "")
	// The scenario names the symbol, the way a record written in the code's
	// own vocabulary does.
	writeFile(t, filepath.Join(root, "docs/facets/features/sim/wave/generation.yaml"),
		"schema_version: \"0.1\"\nid: sim/wave/generation\nlifecycle: active\nintent:\n  summary: |\n    Builds waves.\ncontract:\n  scenarios:\n    - id: it-works\n      statement: |\n        Given a corpus\n        When the generateWave is read\n        Then something happens\n")
	writeFile(t, filepath.Join(root, "src", "wave.ts"),
		"// @facet: sim/wave/generation\n// @facet-role: primary\nexport function generateWave() {\n  return [];\n}\n")
	writeFile(t, filepath.Join(root, "src", "wave.spec.ts"),
		"import { generateWave } from \"./wave\";\nit(\"builds\", () => { generateWave(); });\n")

	got := findings(lintProject(t, root), "test-only-anchors")
	if len(got) != 1 {
		t.Fatalf("the record's own prose must not count as a caller, got %+v", got)
	}
	if !strings.Contains(got[0].Message, "generateWave") {
		t.Errorf("the finding must name the symbol: %q", got[0].Message)
	}
}

// The other half: prose OUTSIDE the registry still counts.
func TestProseOutsideTheRegistryStillCounts(t *testing.T) {
	root := project(t, "")
	record(t, root, "docs/facets", "sim/wave/generation", "active")
	writeFile(t, filepath.Join(root, "src", "wave.ts"),
		"// @facet: sim/wave/generation\n// @facet-role: primary\nexport function generateWave() {\n  return [];\n}\n")
	writeFile(t, filepath.Join(root, "src", "wave.spec.ts"),
		"import { generateWave } from \"./wave\";\nit(\"builds\", () => { generateWave(); });\n")
	// A non-test source file elsewhere in the tree that names it.
	writeFile(t, filepath.Join(root, "tools", "report.ts"),
		"export const known = [\"generateWave\"];\n")

	if got := findings(lintProject(t, root), "test-only-anchors"); len(got) != 0 {
		t.Fatalf("a use outside the registry is evidence and must silence the check, got %+v", got)
	}
}

// A file-header TypeScript anchor can precede a JSDoc block and export.
// Reachability must look past the block, not stop at non-// comment syntax.
func TestReachabilityLooksPastABlockCommentInTypeScript(t *testing.T) {
	root := project(t, "")
	record(t, root, "docs/facets", "sim/wave/generation", "active")
	writeFile(t, filepath.Join(root, "src", "wave.ts"), `/**
 * The wave layer.
 *
 * @facet: sim/wave/generation
 * @facet-role: primary
 */
export function generateWave() {
  return [];
}
`)
	writeFile(t, filepath.Join(root, "src", "wave.spec.ts"), `import { generateWave } from "./wave";
it("builds a wave", () => {
  expect(generateWave()).toEqual([]);
});
`)

	got := findings(lintProject(t, root), "test-only-anchors")
	if len(got) != 1 {
		t.Fatalf("a block comment at the head of a .ts file must not hide generateWave, got %+v", got)
	}
	if !strings.Contains(got[0].Message, "generateWave") {
		t.Errorf("the finding must name the symbol it resolved to: %q", got[0].Message)
	}
}

func TestReachabilityLooksPastPackageAndImportBlock(t *testing.T) {
	root := project(t, "")
	record(t, root, "docs/facets", "sim/wave/generation", "active")
	writeFile(t, filepath.Join(root, "src", "wave.go"), `// @facet: sim/wave/generation
// @facet-role: primary
package sim

import (
	"fmt"
)

// GenerateWave builds the next wave.
func GenerateWave() {
	fmt.Println("wave")
}
`)
	writeFile(t, filepath.Join(root, "src", "wave_test.go"), `func TestGenerateWave(t *testing.T) {
	GenerateWave()
}
`)

	got := findings(lintProject(t, root), "test-only-anchors")
	if len(got) != 1 {
		t.Fatalf("want the finding on GenerateWave past package+import, got %+v", got)
	}
}

// The default heuristic does not know a bespoke directory is a check harness
// rather than production code; LintOptions.IsTestPath lets a project say so.
func TestCustomIsTestPathCanClassifyAParityHarnessAsATest(t *testing.T) {
	root := project(t, "")
	record(t, root, "docs/facets", "sim/wave/generation", "active")
	writeFile(t, filepath.Join(root, "src", "wave.go"), `// @facet: sim/wave/generation
// @facet-role: primary
func GenerateWave() {}
`)
	writeFile(t, filepath.Join(root, "qa", "parity_check.go"), `func checkParity() {
	GenerateWave()
}
`)

	base := LintOptions{Skip: map[string]bool{"schema": true, "knowledge": true}, Today: "2026-07-27"}
	rep, err := LintAll(root, base)
	if err != nil {
		t.Fatal(err)
	}
	if got := findings(rep, "test-only-anchors"); len(got) != 0 {
		t.Fatalf("the default heuristic does not know qa/ is a harness, got %+v", got)
	}

	override := base
	override.IsTestPath = func(rel string) bool {
		return IsTestPath(rel) || strings.HasPrefix(rel, "qa/")
	}
	repOverride, err := LintAll(root, override)
	if err != nil {
		t.Fatal(err)
	}
	if got := findings(repOverride, "test-only-anchors"); len(got) != 1 {
		t.Fatalf("the override should have caught the harness-only caller, got %+v", got)
	}
}

func TestDefaultIsTestPathRecognizesCommonConventions(t *testing.T) {
	cases := map[string]bool{
		"src/wave.go":           false,
		"src/wave_test.go":      true,
		"src/wave.test.ts":      true,
		"src/wave.spec.ts":      true,
		"tests/wave.go":         true,
		"test/wave.rb":          true,
		"__tests__/wave.js":     true,
		"spec/wave_spec.rb":     true,
		"testdata/fixture.json": true,
		"src/tests_helper.go":   false,
		// PHPUnit and JUnit name tests by suffix, and PHP projects often keep
		// them beside the code rather than under tests/.
		"src/InvoiceTest.php":  true,
		"src/InvoiceTest.java": true,
		"src/Invoice.php":      false,
		"src/Contest.php":      false,
		"src/Latest.php":       false,
	}
	for path, want := range cases {
		if got := IsTestPath(path); got != want {
			t.Errorf("IsTestPath(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestAnchoredIdentifierHandlesCommonDeclarationShapes(t *testing.T) {
	cases := []struct {
		name       string
		lines      []string
		anchorLine int
		want       string
		wantOK     bool
	}{
		{
			name:       "go function",
			lines:      []string{"// @facet: a/b/c", "// @facet-role: primary", "func DoThing() {}"},
			anchorLine: 1,
			want:       "DoThing",
			wantOK:     true,
		},
		{
			name:       "go method with pointer receiver",
			lines:      []string{"// @facet: a/b/c", "// @facet-role: primary", "func (s *Server) DoThing() {}"},
			anchorLine: 1,
			want:       "DoThing",
			wantOK:     true,
		},
		{
			name:       "typescript exported function",
			lines:      []string{"// @facet: a/b/c", "// @facet-role: primary", "export function doThing() {}"},
			anchorLine: 1,
			want:       "doThing",
			wantOK:     true,
		},
		{
			name:       "python def",
			lines:      []string{"# @facet: a/b/c", "# @facet-role: primary", "def do_thing():"},
			anchorLine: 1,
			want:       "do_thing",
			wantOK:     true,
		},
		{
			name:       "rust fn with pub",
			lines:      []string{"// @facet: a/b/c", "// @facet-role: primary", "pub fn do_thing() {}"},
			anchorLine: 1,
			want:       "do_thing",
			wantOK:     true,
		},
		{
			name:       "go const after doc comment",
			lines:      []string{"// @facet: a/b/c", "// @facet-role: primary", "// ManifestName is ...", `const ManifestName = "x"`},
			anchorLine: 1,
			want:       "ManifestName",
			wantOK:     true,
		},
		{
			name: "go type after package and import block",
			lines: []string{
				"// @facet: a/b/c", "// @facet-role: primary", "package sim",
				"", "import (", `	"fmt"`, ")", "",
				"// Corpus is ...", "type Corpus struct {",
			},
			anchorLine: 1,
			want:       "Corpus",
			wantOK:     true,
		},
		{
			name:       "nothing follows",
			lines:      []string{"// @facet: a/b/c"},
			anchorLine: 1,
			wantOK:     false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _, ok := anchoredIdentifier(c.lines, c.anchorLine)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v (got identifier %q)", ok, c.wantOK, got)
			}
			if ok && got != c.want {
				t.Errorf("identifier = %q, want %q", got, c.want)
			}
		})
	}
}

// The check weighs evidence of a test-only caller. A symbol nothing mentions
// anywhere — no test, no production code — is not evidence of that; it is a
// naive text search coming up empty, which happens for a dozen innocent
// reasons (a dynamic dispatch, a generated call site, a language the regexes
// do not know). Silence is not a finding.
func TestReachabilityIsSilentWithoutAnyOccurrence(t *testing.T) {
	root := project(t, "")
	record(t, root, "docs/facets", "sim/wave/generation", "active")
	writeFile(t, filepath.Join(root, "src", "wave.go"), `// @facet: sim/wave/generation
// @facet-role: primary
func GenerateWave() {}
`)

	if got := findings(lintProject(t, root), "test-only-anchors"); len(got) != 0 {
		t.Fatalf("no occurrence at all is no evidence — want no finding, got %+v", got)
	}
}
