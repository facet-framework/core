// Fixtures below contain example @facet: tags; this file demonstrates the
// syntax rather than implementing a feature.
// @facet-ignore-file

package facet

import (
	"fmt"
	"go/parser"
	"go/token"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/facet-framework/core/internal/spec"
)

const sharedFooFile = "package s\n\n// @facet: aa:a/b/one\n// @facet-role: primary\n// @facet: bb:a/b/two\n// @facet-role: primary\nfunc Foo() {}\n"

// registryClaims is what a reader of one registry gets: its own scan, then the
// back-list over the project.
func registryClaims(t *testing.T, root, name string) SharedClaims {
	t.Helper()
	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, reg := range cfg.All() {
		if reg.Name != name {
			continue
		}
		features, err := reg.LoadFeatures()
		if err != nil {
			t.Fatal(err)
		}
		scanned, err := ScanAnchors(reg.Root, reg.ScanOptions())
		if err != nil {
			t.Fatal(err)
		}
		var own []Anchor
		for _, a := range scanned {
			if a.BelongsTo(name) {
				own = append(own, a)
			}
		}
		return RegistrySharedClaims(reg, features, own)
	}
	t.Fatalf("no registry %q", name)
	return SharedClaims{}
}

func symbolCount(c SharedClaims) int { return len(c.Symbols) }

// The registry filter is a projection of the whole run: in a source view the
// symbol both registries anchor stays shared in the filtered lint and in the
// reader of one registry.
func TestARegistryFilterKeepsASharedSymbolInASourceView(t *testing.T) {
	checkout, _ := gitProject(t, map[string]string{"shared/s.go": sharedFooFile}, nil)
	config := sharedCodeRegistries + "source_views:\n  repositories:\n    product: {}\n  default_view: default\n  views:\n    default:\n      sources:\n        product: {binding: here}\n"
	root := project(t, config)
	writeFile(t, filepath.Join(root, ".facet/sources.local.yaml"),
		"bindings:\n  here:\n    repository: product\n    checkout: "+strings.ReplaceAll(checkout, "\\", "/")+"\n")
	sharedUseRecord(t, root, "r/aa", "a/b/one", [3]string{"s", "Given aa", ""})
	sharedUseRecord(t, root, "r/bb", "a/b/two", [3]string{"s", "Given bb", ""})

	whole := lintSharedRegistry(t, root, "").SharedClaims.Symbols
	if len(whole) != 1 || !whole[0].DoublePrimary || whole[0].Path != "product:shared/s.go" {
		t.Fatalf("premise: the whole run finds the shared symbol in the view, got %+v", whole)
	}
	if got := lintSharedRegistry(t, root, "bb").SharedClaims.Symbols; len(got) != 1 || got[0].Path != whole[0].Path || !got[0].DoublePrimary {
		t.Fatalf("the filtered lint changed the symbol, got %+v", got)
	}
	if got := registryClaims(t, root, "bb").Symbols; len(got) != 1 || got[0].Path != whole[0].Path || len(got[0].Anchors) != 2 {
		t.Fatalf("the reader of bb changed the symbol, got %+v", got)
	}
}

// A file the other registry's own scan ignores holds none of its anchors in
// the filtered run either, so the filter adds no symbol the whole run lacks.
func TestARegistryFilterRespectsTheOtherRegistrysIgnoreRules(t *testing.T) {
	root := project(t, `
schema_version: "0.1"
registries:
  aa:
    records: r/aa
    sources: [.]
  bb:
    records: r/bb
    sources: [shared]
`)
	writeFile(t, filepath.Join(root, IgnoreFile), "shared/\n")
	sharedUseRecord(t, root, "r/aa", "a/b/one", [3]string{"s", "Given aa", ""})
	sharedUseRecord(t, root, "r/bb", "a/b/two", [3]string{"s", "Given bb", ""})
	writeFile(t, filepath.Join(root, "aa/real.js"), "// @facet: aa:a/b/one\n// @facet-role: primary\nexport function real() {}\n")
	writeFile(t, filepath.Join(root, "shared/s.go"), sharedFooFile)

	if got := lintSharedRegistry(t, root, "").SharedClaims.Symbols; len(got) != 0 {
		t.Fatalf("premise: aa's scan ignores shared/, so nothing is shared, got %+v", got)
	}
	if got := lintSharedRegistry(t, root, "bb").SharedClaims.Symbols; len(got) != 0 {
		t.Fatalf("the filtered lint read an anchor aa's scan ignores, got %+v", got)
	}
	if got := registryClaims(t, root, "bb").Symbols; len(got) != 0 {
		t.Fatalf("the reader of bb read an anchor aa's scan ignores, got %+v", got)
	}
}

// A record of the other registry that will not load is a gap in the
// measurement; the other registry's readable records and their claims stay.
func TestABrokenForeignRecordIsAGapNotALoss(t *testing.T) {
	root := project(t, sharedCodeRegistries)
	sharedUseRecord(t, root, "r/aa", "a/b/one", [3]string{"s", "Given aa", ""})
	sharedUseRecord(t, root, "r/bb", "a/b/two", [3]string{"s", "Given bb", ""})
	writeFile(t, filepath.Join(root, "shared/s.go"), sharedFooFile)
	if got := lintSharedRegistry(t, root, "bb").SharedClaims.Symbols; len(got) != 1 {
		t.Fatalf("premise: the filtered run finds the shared symbol, got %+v", got)
	}
	writeFile(t, filepath.Join(root, "r/aa/features/a/b/broken.yaml"), "bad: [\n")

	rep := lintSharedRegistry(t, root, "bb")
	if got := rep.SharedClaims.Symbols; len(got) != 1 || !got[0].DoublePrimary {
		t.Fatalf("the readable record of aa lost its claim, got %+v", got)
	}
	gaps := rep.SharedClaims.Incomplete
	if len(gaps) != 1 || gaps[0].Registry != "aa" || gaps[0].Path != "r/aa/features/a/b/broken.yaml" {
		t.Fatalf("want the broken record named as a gap, got %+v", gaps)
	}
	warned := findings(rep, CheckSharedClaimsIncomplete)
	if len(warned) != 1 || warned[0].Severity != SeverityWarning {
		t.Fatalf("want one incomplete warning, got %+v", warned)
	}
	if rep.Failed() {
		t.Fatalf("a gap in another registry fails the selected lint: %+v", rep.Findings)
	}
	if got := registryClaims(t, root, "bb"); len(got.Symbols) != 1 || len(got.Incomplete) != 1 {
		t.Fatalf("the reader of bb lost the claim or the gap, got %+v", got)
	}
}

// lockOut makes path unreadable for the rest of the test. A process that can
// still read it (running as root) cannot establish the premise.
func lockOut(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, info.Mode().Perm()) })
	var readErr error
	if info.IsDir() {
		_, readErr = os.ReadDir(path)
	} else {
		_, readErr = os.ReadFile(path)
	}
	if readErr == nil {
		t.Skip("this process reads a file without permissions; the premise cannot be set up")
	}
}

// sharedSourceProject is two records that share one declaration in
// src/area.go and keep an anchor each elsewhere, so both stay anchored when
// the shared file cannot be read.
func sharedSourceProject(t *testing.T) string {
	t.Helper()
	root := project(t, "")
	sharedUseRecord(t, root, "docs/facets", "a/b/one", [3]string{"s", "Given one", ""})
	sharedUseRecord(t, root, "docs/facets", "a/b/two", [3]string{"s", "Given two", ""})
	writeFile(t, filepath.Join(root, "src/keep.go"), "package s\n\n// @facet: a/b/one\nfunc KeepOne() {}\n\n// @facet: a/b/two\nfunc KeepTwo() {}\n")
	writeFile(t, filepath.Join(root, "src/area.go"), "package s\n\n// @facet: a/b/one\n// @facet-role: primary\n// @facet: a/b/two\n// @facet-role: primary\nfunc Area() {}\n")
	return root
}

// A source file the scan selected but could not read leaves its claims out,
// and the measurement says so instead of reporting a complete list.
func TestAnUnreadableSourceIsAGap(t *testing.T) {
	root := sharedSourceProject(t)
	if got := lintShared(t, root).SharedClaims; len(got.Symbols) != 1 || got.Symbols[0].Symbol != "Area" || len(got.Incomplete) != 0 {
		t.Fatalf("premise: the readable file shares area and nothing is missing, got %+v", got)
	}
	lockOut(t, filepath.Join(root, "src/area.go"))

	rep := lintShared(t, root)
	if rep.Checked.Anchors != 2 {
		t.Fatalf("premise: the other anchors are still read, checked %d", rep.Checked.Anchors)
	}
	if got := rep.SharedClaims.Incomplete; len(got) != 1 || got[0].Path != "src/area.go" || got[0].Reason != "source not read" {
		t.Fatalf("want the unreadable source named as a gap, got %+v", got)
	}
	if got := findings(rep, CheckSharedClaimsIncomplete); len(got) != 1 || got[0].Severity != SeverityWarning {
		t.Fatalf("want one incomplete warning, got %+v", got)
	}
	if rep.Failed() {
		t.Fatalf("an unreadable source fails the run: %+v", rep.Findings)
	}
}

// A directory of another registry's records that cannot be read is a gap;
// the readable records after it in the walk still make their claims.
func TestAnUnreadableRecordDirectoryKeepsItsSiblings(t *testing.T) {
	root := pinnedSpecProject(t, "", map[string]string{"booking.md": germanBooking})
	config := filepath.Join(root, ConfigName)
	data, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, config, string(data)+strings.TrimPrefix(sharedCodeRegistries, "\nschema_version: \"0.1\"\n"))
	steps := bookingOne
	sharedUseRecord(t, root, "r/aa", "a/b/one", [3]string{"s", steps, "product:money/booking#one"})
	sharedUseRecord(t, root, "r/bb", "a/b/two", [3]string{"s", steps, "product:money/booking#one"})
	writeFile(t, filepath.Join(root, "shared/s.go"), sharedFooFile)
	locked := filepath.Join(root, "r/aa/features/0-unreadable")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := lintSharedRegistry(t, root, "bb").SharedClaims; len(got.SpecCases) != 1 || len(got.Symbols) != 1 || len(got.Incomplete) != 0 {
		t.Fatalf("premise: the filtered run finds the shared case and symbol and no gap, got %+v", got)
	}
	lockOut(t, locked)

	rep := lintSharedRegistry(t, root, "bb")
	if got := rep.SharedClaims.SpecCases; len(got) != 1 || len(got[0].Users) != 2 {
		t.Fatalf("a record after the unreadable directory lost its spec case, got %+v", got)
	}
	if got := rep.SharedClaims.Symbols; len(got) != 1 || !got[0].DoublePrimary {
		t.Fatalf("a record after the unreadable directory lost its symbol, got %+v", got)
	}
	if got := rep.SharedClaims.Incomplete; len(got) != 1 || got[0].Registry != "aa" || got[0].Path != "r/aa/features/0-unreadable" {
		t.Fatalf("want the unreadable directory named as a gap, got %+v", got)
	}
	if got := registryClaims(t, root, "bb"); len(got.SpecCases) != 1 || len(got.Symbols) != 1 || len(got.Incomplete) != 1 {
		t.Fatalf("the reader of bb lost a claim or the gap, got %+v", got)
	}
}

// A provenance reference binds no steps: German local steps citing one are
// read like unbound German steps, in the project's dialects.
func TestAProvenanceReferenceKeepsTheLocalDialect(t *testing.T) {
	const kit = "# Feature Specification: Checkout\n\n**Feature Branch**: `003-checkout`\n**Created**: 2026-11-23\n**Status**: Approved\n\n" +
		"## Requirements *(mandatory)*\n\n### Functional Requirements\n\n- **FR-001**: The system MUST create an order for a submitted cart.\n"
	root := pinnedSpecSources(t,
		specFixture{id: "product", files: map[string]string{"booking.md": germanBooking}},
		specFixture{id: "kit", format: "speckit", files: map[string]string{"specs/003-checkout/spec.md": kit}},
	)
	address := ""
	for a, locked := range readSpecPin(root).lock["kit"].Scenarios {
		if locked.Strength() == spec.Provenance {
			address = a
		}
	}
	if address == "" {
		t.Fatal("premise: a provenance case is pinned")
	}
	steps := map[string]string{
		"a/b/one":  bookingOne,
		"a/b/two":  bookingTwo,
		"a/b/caps": bookingCaps,
	}
	pair := func(ref string) []string {
		for id, statement := range steps {
			sharedUseRecord(t, root, "docs/facets", id, [3]string{"s", statement, ref})
		}
		rep := lintSharedSpec(t, root)
		if got := findings(rep, "spec-conformance"); len(got) != 0 {
			t.Fatalf("premise: every reference resolves, got %+v", got)
		}
		var out []string
		for _, g := range rep.SharedClaims.Scenarios {
			for _, m := range g.Members {
				out = append(out, m.Record)
			}
		}
		return out
	}
	if got := pair(""); strings.Join(got, ",") != "a/b/one,a/b/two" {
		t.Fatalf("premise: unbound, one and two are the pair, got %v", got)
	}
	if got := pair("kit:" + address); strings.Join(got, ",") != "a/b/one,a/b/two" {
		t.Fatalf("a provenance reference changed the pair, got %v", got)
	}
}

// Only Go sources are read for declarations. Every file below is valid in its
// language and has two read anchors; none names a shared symbol, neither the
// files whose comment or string text only looks like a class nor the ones
// holding a real declaration.
func TestOnlyGoSourcesNameASharedSymbol(t *testing.T) {
	const anchors = "// @facet: a/b/one\n// @facet: a/b/two\n"
	cases := []struct{ name, file string }{
		// Real declarations in other languages.
		{"s.js", anchors + "export function foo() {}\n"},
		{"s.js", anchors + "export const foo = () => 1\n"},
		{"s.js", anchors + "export default class Widget {}\n"},
		{"s.js", anchors + "export class Real {}\n"},
		{"s.js", "/**\n * @facet: a/b/one\n * @facet: a/b/two\n */\nexport function documented() {}\n"},
		{"s.js", "const marker = 1;\n" + anchors + "export function actual() {}\n"},
		{"s.js", anchors + "/**\n * class Widget\n */\nexport class Real {}\n"},
		{"s.js", anchors + "//go:embed assets/*\nexport class Real {}\n"},
		{"s.ts", anchors + "export function foo(): void {}\n"},
		{"s.py", "# @facet: a/b/one\n# @facet: a/b/two\ndef foo():\n    pass\n"},
		// Comment and string text that looks like a class declaration.
		{"s.js", anchors + "console.log(\"class Widget\")\n"},
		{"s.js", anchors + "/*\nclass Widget\n*/\nconsole.log(\"hello\")\n"},
		{"s.js", anchors + "/* heading *\n/ class Widget\n*/\nconsole.log(\"hello\")\n"},
		{"s.js", anchors + "/* first */ /* second\nclass Widget\n*/\nconsole.log(\"hello\")\n"},
		{"s.js", anchors + strings.Repeat(" ", 1024) + "/*\nclass Widget\n*/\nconsole.log(\"hello\")\n"},
		{"s.js", "/* note */ // src/*\n" + anchors + "const sample = `\n*/\nclass Widget\n`;\nconsole.log(sample)\n"},
		{"s.js", "/*/\n" + anchors + "class Widget\n*/\nconsole.log(\"hello\")\n"},
		{"s.js", "const version = 1; /* disabled\n" + anchors + "class Widget\n*/\nconsole.log(\"hello\")\n"},
		{"s.js", "/**\n * @facet: a/b/one\n * @facet: a/b/two\nclass Widget\n*/\nconsole.log(\"hello\")\n"},
		{"s.js", "/*\n// */ /*/\n" + anchors + "class Widget\n*/\nconsole.log(\"hello\")\n"},
		{"s.js", "const sample = `escaped \\`\n" + anchors + "class Widget\n\\` end`;\n"},
		{"s.js", "const sample = \"start \\\n// @facet: a/b/one \\\n// @facet: a/b/two \\\nclass Widget\\\n end\";\n"},
		{"s.js", "// heading\u2028/* open\n" + anchors + "class Widget\n*/\nconsole.log(\"hello\")\n"},
		{"s.js", "// heading\u2029/* open\n" + anchors + "class Widget\n*/\nconsole.log(\"hello\")\n"},
		{"s.js", "// heading\r/* open\n" + anchors + "class Widget\n*/\nconsole.log(\"hello\")\n"},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprintf("%d %s", i, tc.name), func(t *testing.T) {
			if got := sharedSymbolsIn(t, tc.name, tc.file); len(got) != 0 {
				t.Fatalf("want no symbol, got %v\n%s", got, tc.file)
			}
		})
	}
}

// sharedSymbolsIn lints one source file under src/ with two records and
// returns the shared symbols, after checking that both anchors were read.
func sharedSymbolsIn(t *testing.T, name, file string) []string {
	t.Helper()
	rep := lintSharedSource(t, name, file)
	var got []string
	for _, sym := range rep.SharedClaims.Symbols {
		got = append(got, sym.Symbol)
	}
	return got
}

func lintSharedSource(t *testing.T, name, file string) Report {
	t.Helper()
	root := project(t, "")
	sharedUseRecord(t, root, "docs/facets", "a/b/one", [3]string{"s", "Given one", ""})
	sharedUseRecord(t, root, "docs/facets", "a/b/two", [3]string{"s", "Given two", ""})
	writeFile(t, filepath.Join(root, "src", name), file)
	rep := lintShared(t, root)
	if rep.Checked.Anchors != 2 {
		t.Fatalf("premise: both anchors are read, checked %d", rep.Checked.Anchors)
	}
	return rep
}

// A Go anchor names the declaration its comment documents, as the Go parser
// reads it: a backtick in a rune, an interpreted string or a struct tag is no
// raw-string delimiter, an anchor inside a raw string is no comment, and a
// method is named Type.Method.
func TestGoAnchorsNameTheDeclarationTheyDocument(t *testing.T) {
	const anchors = "// @facet: a/b/one\n// @facet: a/b/two\n"
	const tags = "type T struct {\n\tA string `json:\"a\"`\n\tB int    `json:\"b,omitempty\" yaml:\"b\"`\n}\n\nvar pattern = `^[a-z]+$`\n\n"
	cases := []struct {
		name, file string
		want       string
	}{
		{"struct tags and a one-line raw string before a shared func",
			"package s\n\n" + tags + anchors + "func Shared() {}\n", "Shared"},
		{"a rune literal of a backtick before a shared func",
			"package s\n\nvar tick = '`'\n\n" + anchors + "func Shared() {}\n", "Shared"},
		{"a comment open inside a one-line raw string",
			"package s\n\nvar glob = `src/*.go`\n\n" + anchors + "func Shared() {}\n", "Shared"},
		{"a method on a pointer receiver",
			"package s\n\ntype Repo struct{}\n\n" + anchors + "func (r *Repo) Load() error { return nil }\n", "Repo.Load"},
		{"a method on a generic receiver",
			"package s\n\ntype List[T any] struct{ v []T }\n\n" + anchors + "func (l *List[T]) Push(v T) { l.v = append(l.v, v) }\n", "List.Push"},
		{"a type", "package s\n\n" + anchors + "type Widget struct{}\n", "Widget"},
		{"one spec of a parenthesised const",
			"package s\n\nconst (\n\tA = 1\n" + "\t// @facet: a/b/one\n\t// @facet: a/b/two\n" + "\tB = 2\n)\n", "B"},
		// A backtick in a rune or an interpreted string beside a raw string
		// spanning lines that holds the anchors.
		{"runes at both edges of a raw string",
			"package s\nconst text=string('`') + `\n" + anchors + "func Widget() {}\n` + string('`')\n", ""},
		{"interpreted strings at both edges of a raw string",
			"package s\nconst text=\"`\" + `\n" + anchors + "func Widget() {}\n` + \"`\"\n", ""},
		{"a comment with a backtick after the close",
			"package s\nconst text=string('`') + `\n" + anchors + "func Widget() {}\n` // `\n", ""},
		{"a raw string spanning lines between the anchors",
			"package s\n\n// @facet: a/b/one\nvar text = `\n*/\nfunc helper() {}\n// @facet: a/b/two\nfunc Widget() {}\n`\n\nfunc main() { println(text) }\n", ""},
		{"anchors in a comment inside a body",
			"package s\n\nfunc outer() {\n" + anchors + "\tprintln(1)\n}\n\nfunc after() {}\n", ""},
		{"anchors above the package clause",
			anchors + "package s\n\nfunc first() {}\n", ""},
		{"a blank line between the anchors and the func",
			"package s\n\n" + anchors + "\nfunc Shared() {}\n", ""},
		{"a declaration of two names",
			"package s\n\n" + anchors + "var a, b = 1, 2\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parser.ParseFile(token.NewFileSet(), "s.go", tc.file, 0); err != nil {
				t.Fatalf("premise: the file is valid Go: %v\n%s", err, tc.file)
			}
			if got := strings.Join(sharedSymbolsIn(t, "s.go", tc.file), ","); got != tc.want {
				t.Fatalf("want %q, got %q\n%s", tc.want, got, tc.file)
			}
		})
	}
}

// A Go file that does not parse names no symbol and is a measurement gap, so
// the view does not read as complete; the same file repaired names its func.
func TestAGoFileThatDoesNotParseIsAGap(t *testing.T) {
	const anchors = "package s\n\n// @facet: a/b/one\n// @facet: a/b/two\nfunc Shared() {}\n"
	if rep := lintSharedSource(t, "s.go", anchors); len(rep.SharedClaims.Symbols) != 1 || len(rep.SharedClaims.Incomplete) != 0 {
		t.Fatalf("premise: the parsed file names Shared and no gap, got %+v", rep.SharedClaims)
	}
	rep := lintSharedSource(t, "s.go", anchors+"func broken( {\n")
	if len(rep.SharedClaims.Symbols) != 0 {
		t.Fatalf("a file that does not parse named %+v", rep.SharedClaims.Symbols)
	}
	want := []MeasurementGap{{Path: "src/s.go", Reason: "parse error"}}
	if !slices.Equal(rep.SharedClaims.Incomplete, want) {
		t.Fatalf("want gap %+v, got %+v", want, rep.SharedClaims.Incomplete)
	}
	if len(findings(rep, CheckSharedClaimsIncomplete)) != 1 {
		t.Fatalf("want one incomplete warning, got %+v", rep.Findings)
	}
}

// A Go file larger than the scanner reads whole is not parsed: it names no
// symbol and is a gap, which bounds the memory a parse takes.
func TestAGoFileTooLargeToParseIsAGap(t *testing.T) {
	var b strings.Builder
	b.WriteString("package s\n\n// @facet: a/b/one\n// @facet: a/b/two\nfunc Shared() {}\n")
	for int64(b.Len()) <= scanWholeFileLimit {
		b.WriteString("// " + strings.Repeat("x", 200) + "\n")
	}
	rep := lintSharedSource(t, "s.go", b.String())
	if len(rep.SharedClaims.Symbols) != 0 {
		t.Fatalf("a file over the limit named %+v", rep.SharedClaims.Symbols)
	}
	want := []MeasurementGap{{Path: "src/s.go", Reason: "too large to parse"}}
	if !slices.Equal(rep.SharedClaims.Incomplete, want) {
		t.Fatalf("want gap %+v, got %+v", want, rep.SharedClaims.Incomplete)
	}
}

// goFile builds a Go file. Unless it is a control, both anchors and a func
// keyword sit inside a raw string spanning lines, among struct tags and
// one-line raw strings, and the only code after them is a call. A control
// keeps the anchors outside any raw string and ends with a real func.
func goFile(r *rand.Rand, control bool) (string, []int) {
	lines := []string{"package s", ""}
	var anchors []int
	filler := func() {
		switch r.IntN(4) {
		case 0:
			lines = append(lines, "type T"+fmt.Sprint(len(lines))+" struct {", "\tA string `json:\"a\"`", "}")
		case 1:
			lines = append(lines, "var re"+fmt.Sprint(len(lines))+" = `^[a-z]+ func Widget$`")
		case 2:
			lines = append(lines, "// func Widget() note")
		default:
			lines = append(lines, "/* func Widget() */")
		}
	}
	for range 1 + r.IntN(4) {
		filler()
	}
	if control {
		for _, id := range []string{"a/b/one", "a/b/two"} {
			lines = append(lines, "// @facet: "+id)
			anchors = append(anchors, len(lines))
		}
		lines = append(lines, "func Real() {}")
		for range r.IntN(3) {
			filler()
		}
		return strings.Join(lines, "\n") + "\n", anchors
	}
	lines = append(lines, "var text"+fmt.Sprint(len(lines))+" = `")
	inner := []string{"*/", "/*", "func helper() {}", "class Widget", "note"}
	for _, id := range []string{"a/b/one", "a/b/two"} {
		for range r.IntN(3) {
			lines = append(lines, inner[r.IntN(len(inner))])
		}
		lines = append(lines, "// @facet: "+id)
		anchors = append(anchors, len(lines))
	}
	lines = append(lines, "func Widget() {}", "`", "", "func init() { println(1) }")
	return strings.Join(lines, "\n") + "\n", anchors
}

// Generated Go files never yield a symbol from raw-string text; the controls
// find their real func.
func TestGoRawStringTextIsNeverADeclaration(t *testing.T) {
	r := rand.New(rand.NewPCG(1108, 9))
	dir := t.TempDir()
	var real, missed int
	for i := range 300 {
		control := i%3 == 2
		file, anchors := goFile(r, control)
		path := filepath.Join(dir, fmt.Sprintf("s%d.go", i))
		writeFile(t, path, file)
		var read []int
		for _, a := range ScanContent(path, []byte(file)) {
			read = append(read, a.Line)
		}
		if !slices.Equal(read, anchors) {
			t.Fatalf("case %d: premise: anchors on lines %v, the scanner read %v\n%s", i, anchors, read, file)
		}
		found, err := goDeclarations(path, anchors)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range anchors {
			d, ok := found[line]
			switch {
			case !control && ok:
				t.Fatalf("case %d: anchor on line %d invented %q on line %d\n%s", i, line, d.name, d.line, file)
			case control && ok && d.name != "Real":
				t.Fatalf("case %d: anchor on line %d found %q, want Real\n%s", i, line, d.name, file)
			case control && ok:
				real++
			case control:
				missed++
			}
		}
	}
	if missed != 0 {
		t.Fatalf("the Go control found Real for %d of %d anchors", real, real+missed)
	}
	t.Logf("Go control: Real found for %d of %d anchors", real, real+missed)
}

// Gaps arrive in the order the parallel walker delivers files. The list and
// the warnings derived from it are ordered by registry, path and reason, so
// the same project gives the same report on every run.
func TestGapsAreOrderedWhateverOrderTheyArrive(t *testing.T) {
	gaps := []MeasurementGap{
		{Registry: "bb", Path: "src/a.js", Reason: "source not read"},
		{Registry: "aa", Path: "src/z.js", Reason: "source not read"},
		{Registry: "aa", Path: "src/b.js", Reason: "source not read"},
		{Registry: "aa", Path: "src/b.js", Reason: "records not read"},
		{Registry: "aa", Reason: "anchors not scanned"},
	}
	want := []MeasurementGap{gaps[4], gaps[3], gaps[2], gaps[1], gaps[0]}
	var firstWarnings []string
	for i := range gaps {
		turned := append(slices.Clone(gaps[i:]), gaps[:i]...)
		if i%2 == 1 {
			slices.Reverse(turned)
		}
		half := len(turned) / 2
		claims := ComputeSharedClaims([]SharedClaimsInput{
			{Registry: Registry{Name: "aa", Root: t.TempDir()}, Gaps: turned[:half]},
			{Registry: Registry{Name: "bb", Root: t.TempDir()}, Gaps: turned[half:]},
		})
		if !slices.Equal(claims.Incomplete, want) {
			t.Fatalf("order %d: want %+v, got %+v", i, want, claims.Incomplete)
		}
		var rep Report
		lintSharedClaims(&rep, claims, map[string]Registry{}, map[string]bool{})
		var warnings []string
		for _, f := range findings(rep, CheckSharedClaimsIncomplete) {
			warnings = append(warnings, f.Message)
		}
		if len(warnings) != len(want) {
			t.Fatalf("order %d: want %d warnings, got %v", i, len(want), warnings)
		}
		if firstWarnings == nil {
			firstWarnings = warnings
		} else if !slices.Equal(warnings, firstWarnings) {
			t.Fatalf("order %d: warnings %v differ from %v", i, warnings, firstWarnings)
		}
	}
}

// Anchors spread through a Go file each find the func their comment
// documents, however many there are.
func TestSpreadGoAnchorsEachFindTheirOwnFunc(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spread.go")
	var b strings.Builder
	var lines []int
	line := 0
	write := func(s string) {
		b.WriteString(s + "\n")
		line++
	}
	write("package s")
	for i := range 48 {
		for range 20 {
			write("// " + strings.Repeat("x", 60))
		}
		write("")
		write("// @facet: a/b/one")
		lines = append(lines, line)
		write("// @facet: a/b/two")
		lines = append(lines, line)
		write(fmt.Sprintf("func F%d() {}", i))
	}
	writeFile(t, path, b.String())
	found, err := goDeclarations(path, lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != len(lines) {
		t.Fatalf("want a declaration for each of %d anchors, got %d", len(lines), len(found))
	}
	if d := found[lines[2]]; d.name != "F1" || d.line != lines[2]+2 {
		t.Fatalf("the second pair's declaration is misplaced: %+v", d)
	}
}

// Anchors past the head of a file too large for the scanner to read whole are
// not anchors to the scanner, so the back-list leaves them out for every
// consumer, however the consumer found them.
func TestAnchorsPastTheScannedHeadOfALargeFileAreLeftOut(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "big.go")
	var b strings.Builder
	b.WriteString("package s\n\n")
	for b.Len() < 1<<20+4096 {
		b.WriteString("// " + strings.Repeat("x", 200) + "\n")
	}
	b.WriteString("// @facet: a/b/one\n// @facet-role: primary\n// @facet: a/b/two\n// @facet-role: primary\nfunc Foo() {}\n")
	for b.Len() < 2<<20+4096 {
		b.WriteString("// " + strings.Repeat("y", 200) + "\n")
	}
	writeFile(t, path, b.String())
	whole := ScanContent(path, []byte(b.String()))
	if len(whole) != 2 {
		t.Fatalf("premise: reading the whole file finds both anchors, got %+v", whole)
	}
	if head, err := ScanFile(path); err != nil || len(head) != 0 {
		t.Fatalf("premise: the scanner reads only the head, got %+v %v", head, err)
	}
	claims := ComputeSharedClaims([]SharedClaimsInput{{Registry: Registry{Root: root}, Anchors: whole}})
	// An admitted anchor would send the file to the parser, which leaves a
	// file this large unread as a gap.
	if len(claims.Symbols) != 0 || len(claims.Incomplete) != 0 {
		t.Fatalf("an anchor the scanner does not admit was measured, got %+v", claims)
	}
}
