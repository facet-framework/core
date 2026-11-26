// Fixtures below contain example @facet: tags; this file demonstrates the
// syntax rather than implementing a feature.
// @facet-ignore-file

package facet

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gherkin "github.com/cucumber/gherkin/go/v41"
	"github.com/facet-framework/core/internal/spec"
)

// sharedCodeRegistries declares aa and bb, each with exclusive sources and a shared
// directory both scan.
const sharedCodeRegistries = `
schema_version: "0.1"
registries:
  aa:
    records: r/aa
    sources: [aa, shared]
  bb:
    records: r/bb
    sources: [bb, shared]
`

func lintSharedRegistry(t *testing.T, root, registry string) Report {
	t.Helper()
	rep, err := LintAll(root, LintOptions{
		Registry: registry,
		Skip:     map[string]bool{"schema": true, "knowledge": true, "spec-conformance": true, "lost-anchors": true, "created-at-history": true, "orphan-records": true},
		Today:    "2026-11-24",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.SharedClaims == nil {
		t.Fatal("the report carries no shared claims")
	}
	return rep
}

// The same id in two registries is two records. Each registry's unqualified
// primary anchor in its own sources is one implementation of its own record,
// not a second implementation of the other registry's.
func TestTheSameIDInTwoRegistriesIsTwoRecords(t *testing.T) {
	root := project(t, sharedCodeRegistries)
	sharedUseRecord(t, root, "r/aa", "a/b/one", [3]string{"s", "Given aa", ""})
	sharedUseRecord(t, root, "r/bb", "a/b/one", [3]string{"s", "Given bb", ""})
	writeFile(t, filepath.Join(root, "aa/x.js"), "// @facet: a/b/one\n// @facet-role: primary\nexport function foo() {}\n")
	writeFile(t, filepath.Join(root, "bb/y.js"), "// @facet: a/b/one\n// @facet-role: primary\nexport function foo() {}\n")

	rep := lintSharedRegistry(t, root, "")
	if rep.Checked.Anchors != 2 {
		t.Fatalf("fixture must place one anchor per registry, checked %d", rep.Checked.Anchors)
	}
	if got := rep.SharedClaims.Primaries; len(got) != 0 {
		t.Fatalf("each record has one implementation, got %+v", got)
	}
}

// Two registries naming their own record of one id on one declaration are
// two records sharing a symbol, both primary.
func TestQualifiedAnchorsOfOneIDInTwoRegistriesShareTheSymbol(t *testing.T) {
	root := project(t, sharedCodeRegistries)
	sharedUseRecord(t, root, "r/aa", "a/b/one", [3]string{"s", "Given aa", ""})
	sharedUseRecord(t, root, "r/bb", "a/b/one", [3]string{"s", "Given bb", ""})
	writeFile(t, filepath.Join(root, "shared/s.go"),
		"package s\n\n// @facet: aa:a/b/one\n// @facet-role: primary\n// @facet: bb:a/b/one\n// @facet-role: primary\nfunc Foo() {}\n")

	got := lintSharedRegistry(t, root, "").SharedClaims.Symbols
	if len(got) != 1 || !got[0].DoublePrimary || len(got[0].Anchors) != 2 {
		t.Fatalf("want one doubly primary symbol, got %+v", got)
	}
	if got[0].Anchors[0].Registry != "aa" || got[0].Anchors[1].Registry != "bb" {
		t.Fatalf("each anchor keeps its registry, got %+v", got[0].Anchors)
	}
}

// A run narrowed to one registry still sees the other registry's anchor on a
// symbol they share, in lint and in the corpus a reader of that registry gets.
func TestARegistryFilterKeepsTheOtherAnchorOfASharedSymbol(t *testing.T) {
	root := project(t, sharedCodeRegistries)
	sharedUseRecord(t, root, "r/aa", "a/b/one", [3]string{"s", "Given aa", ""})
	sharedUseRecord(t, root, "r/bb", "a/b/two", [3]string{"s", "Given bb", ""})
	writeFile(t, filepath.Join(root, "shared/s.go"),
		"package s\n\n// @facet: aa:a/b/one\n// @facet-role: primary\n// @facet: bb:a/b/two\n// @facet-role: primary\nfunc Foo() {}\n")

	if got := lintSharedRegistry(t, root, "").SharedClaims.Symbols; len(got) != 1 || !got[0].DoublePrimary {
		t.Fatalf("premise: the full run finds the shared symbol, got %+v", got)
	}
	if got := lintSharedRegistry(t, root, "bb").SharedClaims.Symbols; len(got) != 1 || !got[0].DoublePrimary {
		t.Fatalf("the filtered run lost the shared symbol, got %+v", got)
	}

	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	var bb Registry
	for _, reg := range cfg.All() {
		if reg.Name == "bb" {
			bb = reg
		}
	}
	features, err := bb.LoadFeatures()
	if err != nil {
		t.Fatal(err)
	}
	scanned, err := ScanAnchors(bb.Root, bb.ScanOptions())
	if err != nil {
		t.Fatal(err)
	}
	var own []Anchor
	for _, a := range scanned {
		if a.BelongsTo("bb") {
			own = append(own, a)
		}
	}
	if got := RegistrySharedClaims(bb, features, own).Symbols; len(got) != 1 || len(got[0].Anchors) != 2 {
		t.Fatalf("the corpus of bb lost the shared symbol, got %+v", got)
	}
}

// Anchors above a statement that declares nothing share no symbol: a call is
// not a declaration.
func TestAnchorsAboveACallShareNoSymbol(t *testing.T) {
	root := project(t, "")
	sharedUseRecord(t, root, "docs/facets", "a/b/one", [3]string{"s", "Given one", ""})
	sharedUseRecord(t, root, "docs/facets", "a/b/two", [3]string{"s", "Given two", ""})
	writeFile(t, filepath.Join(root, "src/declared.go"), "package s\n\n// @facet: a/b/one\n// @facet: a/b/two\nfunc Foo() {}\n")
	if got := lintShared(t, root).SharedClaims.Symbols; len(got) != 1 {
		t.Fatalf("premise: a declaration is shared, got %+v", got)
	}
	writeFile(t, filepath.Join(root, "src/declared.go"), "package s\n\nfunc init() {\n\t// @facet: a/b/one\n\t// @facet: a/b/two\n\tprintln(\"hello\")\n}\n")
	rep := lintShared(t, root)
	if rep.Checked.Anchors != 2 {
		t.Fatalf("premise: both anchors are read, checked %d", rep.Checked.Anchors)
	}
	if got := rep.SharedClaims.Symbols; len(got) != 0 {
		t.Fatalf("a call is not a shared declaration, got %+v", got)
	}
}

// Two primary implementations of one record are named when both are Go
// files, and never across two languages or for a file type that is not
// known. Only Go sources are read for declarations, so primaries in two files
// of another language are not named.
func TestPrimaryAnchorsAreGroupedByLanguage(t *testing.T) {
	cases := []struct {
		first, second string
		want          int
	}{
		{"q.go", "r.go", 1},
		{"q.go", "r.ts", 0},
		{"q.js", "r.js", 0},
		{"q.js", "r.mjs", 0},
		{"q.ts", "r.tsx", 0},
		{"q.unknown", "r.unknown", 0},
	}
	for _, tc := range cases {
		t.Run(tc.first+"+"+tc.second, func(t *testing.T) {
			root := project(t, "")
			sharedUseRecord(t, root, "docs/facets", "a/b/one", [3]string{"s", "Given one", ""})
			for _, name := range []string{tc.first, tc.second} {
				impl := "// @facet: a/b/one\n// @facet-role: primary\nexport function " + name[:1] + "Impl() {}\n"
				if strings.HasSuffix(name, ".go") {
					impl = "package src\n\n// @facet: a/b/one\n// @facet-role: primary\nfunc " + strings.ToUpper(name[:1]) + "Impl() {}\n"
				}
				writeFile(t, filepath.Join(root, "src", name), impl)
			}
			rep := lintShared(t, root)
			if rep.Checked.Anchors != 2 {
				t.Fatalf("premise: both anchors are read, checked %d", rep.Checked.Anchors)
			}
			if got := rep.SharedClaims.Primaries; len(got) != tc.want {
				t.Fatalf("want %d repeated primaries, got %+v", tc.want, got)
			}
		})
	}
}

// A shared symbol in a source view is named by its portable reference, the
// same from every checkout, never by the local path it was read from.
func TestASharedSymbolInASourceViewIsNamedByItsReference(t *testing.T) {
	files := map[string]string{"src/f.go": "package s\n\n// @facet: a/b/one\n// @facet-role: primary\n// @facet: a/b/two\n// @facet-role: primary\nfunc Foo() {}\n"}
	for _, name := range []string{"machine-local-checkout", "moved-checkout"} {
		t.Run(name, func(t *testing.T) {
			checkout, _ := gitProject(t, files, nil)
			placed := filepath.Join(t.TempDir(), name)
			if err := os.Rename(checkout, placed); err != nil {
				t.Fatal(err)
			}
			config := "source_views:\n  repositories:\n    product: {}\n  default_view: default\n  views:\n    default:\n      sources:\n        product: {binding: here}\n"
			root := project(t, config)
			writeFile(t, filepath.Join(root, ".facet/sources.local.yaml"),
				"bindings:\n  here:\n    repository: product\n    checkout: "+strings.ReplaceAll(placed, "\\", "/")+"\n")
			sharedUseRecord(t, root, "docs/facets", "a/b/one", [3]string{"s", "Given one", ""})
			sharedUseRecord(t, root, "docs/facets", "a/b/two", [3]string{"s", "Given two", ""})

			got := lintShared(t, root).SharedClaims.Symbols
			if len(got) != 1 {
				t.Fatalf("premise: the view's shared symbol is found, got %+v", got)
			}
			if got[0].Path != "product:src/f.go" || strings.Contains(got[0].Path, name) {
				t.Fatalf("want the portable reference, got %q", got[0].Path)
			}
		})
	}
}

// A Go file the scanner treats as too large to hold is not parsed: the pass
// reads neither its whole content nor its line split, and the file is a gap
// rather than a source that names no symbol.
func TestALargeFileIsAGapReadInABoundedWindow(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "big.go")
	head := "package s\n\n// @facet: a/b/one\n// @facet-role: primary\n// @facet: a/b/two\n// @facet-role: primary\nfunc Foo() {}\n"
	body := strings.Repeat("// "+strings.Repeat("x", 200)+"\n", 5243)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(head); err != nil {
		t.Fatal(err)
	}
	for range 8 {
		if _, err := f.WriteString(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() <= scanWholeFileLimit {
		t.Fatalf("premise: the fixture takes the scanner's large-file path, %v %v", info, err)
	}
	anchors, err := ScanFile(path)
	if err != nil || len(anchors) != 2 {
		t.Fatalf("premise: the large-file scan finds both anchors, got %+v %v", anchors, err)
	}
	inputs := []SharedClaimsInput{{Registry: Registry{Root: root}, Anchors: anchors}}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	claims := ComputeSharedClaims(inputs)
	runtime.ReadMemStats(&after)

	if len(claims.Symbols) != 0 {
		t.Fatalf("a file too large to parse named %+v", claims.Symbols)
	}
	if len(claims.Incomplete) != 1 || claims.Incomplete[0].Path != "big.go" || claims.Incomplete[0].Reason != "too large to parse" {
		t.Fatalf("want big.go as a gap, got %+v", claims.Incomplete)
	}
	// The race detector inflates allocation counts; see raceEnabled.
	if raceEnabled {
		return
	}
	// The bound covers the scanner's own read of the file's 1 MiB head, which
	// admits the anchors. It stays far below the 8 MiB the file holds.
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 3<<20 {
		t.Fatalf("the pass allocated %d bytes for an 8 MiB file", allocated)
	}
}

// pinnedSpecProject commits spec files to a local upstream, declares it as
// spec source "product" and pins it, so scenarios can reference real cases.
func pinnedSpecProject(t *testing.T, format string, files map[string]string) string {
	t.Helper()
	return pinnedSpecSources(t, specFixture{id: "product", format: format, files: files})
}

// specFixture is one spec source of a pinned fixture project.
type specFixture struct {
	id, format string
	files      map[string]string
}

// pinnedSpecSources commits each source's files to its own local upstream,
// declares them all and pins them.
func pinnedSpecSources(t *testing.T, sources ...specFixture) string {
	t.Helper()
	config := "spec_sources:\n"
	for _, source := range sources {
		upstream := t.TempDir()
		run := func(args ...string) {
			t.Helper()
			cmd := exec.Command("git", append([]string{"-C", upstream}, args...)...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		run("init", "--initial-branch=main")
		for name, body := range source.files {
			writeFile(t, filepath.Join(upstream, filepath.FromSlash(name)), body)
		}
		run("add", ".")
		run("-c", "user.name=FACET Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Specification")
		remote := (&url.URL{Scheme: "file", Path: filepath.ToSlash(upstream)}).String()
		config += fmt.Sprintf("  - id: %s\n    repo: %s\n", source.id, remote)
		if source.format != "" {
			config += "    format: " + source.format + "\n"
		}
	}
	root := project(t, config)
	cfg, err := LoadConfigUnresolved(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := spec.Update(t.Context(), root, cfg.SpecSources, nil, nil); err != nil {
		t.Fatal(err)
	}
	return root
}

// lintSharedSpec runs with spec conformance on, so a scenario's spec binding
// is the one lint itself checks.
func lintSharedSpec(t *testing.T, root string) Report {
	t.Helper()
	rep, err := LintAll(root, LintOptions{
		Skip:  map[string]bool{"schema": true, "knowledge": true, "lost-anchors": true, "created-at-history": true, "orphan-records": true},
		Today: "2026-11-24",
	})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// deKeyword returns the German dialect's keyword of a kind, skipping the
// wildcard, so the fixtures are German Gherkin with English step content.
func deKeyword(kind string) string {
	for _, keyword := range gherkin.DialectsBuiltin().GetDialect("de").Keywords[kind] {
		if keyword = strings.TrimSpace(keyword); keyword != "*" {
			return keyword
		}
	}
	panic("no German keyword for " + kind)
}

var (
	// bookingOne and bookingTwo differ only in the keyword of their second
	// step; bookingCaps differs from bookingOne only in a value's case.
	bookingOne  = deKeyword("given") + " amount A\n" + deKeyword("and") + " account B\n" + deKeyword("when") + " booked\n" + deKeyword("then") + " done"
	bookingTwo  = deKeyword("given") + " amount A\n" + deKeyword("given") + " account B\n" + deKeyword("when") + " booked\n" + deKeyword("then") + " done"
	bookingCaps = deKeyword("given") + " amount a\n" + deKeyword("and") + " account B\n" + deKeyword("when") + " booked\n" + deKeyword("then") + " done"
)

var germanBooking = "---\nschema: facet-spec/0.1\nid: money/booking\ntitle: Booking\nstatus: approved\nowner: finance\nlanguage: de\n---\n\n```gherkin\n" +
	"@case:one\n" + deKeyword("scenario") + ": One\n" + indentSteps(bookingOne) + "\n" +
	"@case:two\n" + deKeyword("scenario") + ": Two\n" + indentSteps(bookingTwo) + "\n" +
	"@case:caps\n" + deKeyword("scenario") + ": Lowercase\n" + indentSteps(bookingCaps) + "```\n"

func indentSteps(steps string) string {
	return "  " + strings.ReplaceAll(steps, "\n", "\n  ") + "\n"
}

// Scenarios pinned to German cases are read in German: the two whose steps
// the lock pins identically are a pair, and the one differing in a value's
// case is not.
func TestPinnedScenariosAreComparedInTheirDialect(t *testing.T) {
	root := pinnedSpecProject(t, "", map[string]string{"booking.md": germanBooking})
	lock, err := spec.ReadLock(root)
	if err != nil {
		t.Fatal(err)
	}
	scenarios := lock["product"].Scenarios
	if scenarios["money/booking#one"].Steps != scenarios["money/booking#two"].Steps || scenarios["money/booking#one"].Steps == scenarios["money/booking#caps"].Steps {
		t.Fatalf("premise: the lock pins one and two alike and caps apart, got %+v", scenarios)
	}
	sharedUseRecord(t, root, "docs/facets", "a/b/one", [3]string{"s", bookingOne, "product:money/booking#one"})
	sharedUseRecord(t, root, "docs/facets", "a/b/two", [3]string{"s", bookingTwo, "product:money/booking#two"})
	sharedUseRecord(t, root, "docs/facets", "a/b/caps", [3]string{"s", bookingCaps, "product:money/booking#caps"})

	rep := lintSharedSpec(t, root)
	if got := findings(rep, "spec-conformance"); len(got) != 0 {
		t.Fatalf("premise: all three conform to their pins, got %+v", got)
	}
	got := rep.SharedClaims.Scenarios
	if len(got) != 1 || len(got[0].Members) != 2 || got[0].Members[0].Record != "a/b/one" || got[0].Members[1].Record != "a/b/two" {
		t.Fatalf("want the pair one and two, got %+v", got)
	}
}

// Scenarios conforming to one and the same exact case are that case's users,
// named once by the spec case. Two cases with the same steps are still a pair,
// and so are scenarios whose reference does not resolve.
func TestOnlyConformingUsersOfOneExactCaseAreLeftOut(t *testing.T) {
	root := pinnedSpecProject(t, "", map[string]string{"booking.md": germanBooking})
	one := bookingOne
	sharedUseRecord(t, root, "docs/facets", "a/b/first", [3]string{"s", one, "product:money/booking#one"})
	sharedUseRecord(t, root, "docs/facets", "a/b/second", [3]string{"s", one, "product:money/booking#one"})
	sharedUseRecord(t, root, "docs/facets", "a/c/third", [3]string{"s", "Given p\nWhen q\nThen r", "product:not/here#missing"})
	sharedUseRecord(t, root, "docs/facets", "a/c/fourth", [3]string{"s", "Given p\nWhen q\nThen r", "product:not/here#missing"})

	rep := lintSharedSpec(t, root)
	if got := findings(rep, CheckSharedSpecCase); len(got) != 2 {
		t.Fatalf("premise: both addresses are shared, got %+v", got)
	}
	got := rep.SharedClaims.Scenarios
	if len(got) != 1 || got[0].Members[0].Record != "a/c/fourth" {
		t.Fatalf("want only the unresolved pair, got %+v", got)
	}

	// A local copy of the pinned steps beside its conforming users is named.
	sharedUseRecord(t, root, "docs/facets", "a/d/copy", [3]string{"s", one, ""})
	got = lintSharedSpec(t, root).SharedClaims.Scenarios
	if len(got) != 2 {
		t.Fatalf("want the local copy named with the case's users, got %+v", got)
	}
}

// A provenance binding does not force the steps, so two scenarios citing one
// provenance case with identical local steps are a duplicated local promise.
func TestProvenanceUsersAreComparedLikeLocalScenarios(t *testing.T) {
	const kit = "# Feature Specification: Checkout\n\n**Feature Branch**: `003-checkout`\n**Created**: 2026-11-23\n**Status**: Approved\n\n" +
		"## Requirements *(mandatory)*\n\n### Functional Requirements\n\n- **FR-001**: The system MUST create an order for a submitted cart.\n"
	root := pinnedSpecProject(t, "speckit", map[string]string{"specs/003-checkout/spec.md": kit})
	lock, err := spec.ReadLock(root)
	if err != nil {
		t.Fatal(err)
	}
	var address string
	for a, s := range lock["product"].Scenarios {
		if s.Strength() == spec.Provenance {
			address = a
		}
	}
	if address == "" {
		t.Fatalf("premise: a provenance case is pinned, got %+v", lock["product"].Scenarios)
	}
	steps := "Given a cart\nWhen it is submitted\nThen an order exists"
	sharedUseRecord(t, root, "docs/facets", "a/b/one", [3]string{"s", steps, "product:" + address})
	sharedUseRecord(t, root, "docs/facets", "a/b/two", [3]string{"s", steps, "product:" + address})

	rep := lintSharedSpec(t, root)
	if got := findings(rep, "spec-conformance"); len(got) != 0 {
		t.Fatalf("premise: both references resolve, got %+v", got)
	}
	if got := rep.SharedClaims.SpecCases; len(got) != 1 {
		t.Fatalf("premise: the case is shared, got %+v", got)
	}
	if got := rep.SharedClaims.Scenarios; len(got) != 1 || len(got[0].Members) != 2 {
		t.Fatalf("want the duplicated local steps named, got %+v", got)
	}
}

// outlineRecord writes a record with one outline scenario.
func outlineRecord(t *testing.T, root, id, examples string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "docs/facets/features", filepath.FromSlash(id)+".yaml"),
		"schema_version: \"0.1\"\nid: "+id+"\nlifecycle: active\ncontract:\n  scenarios:\n    - id: s\n      statement: |\n        Given <x>\n        When y\n        Then z\n      examples:\n"+examples)
}

// Outlines are compared by their effective tables: a block repeating the
// inherited columns, or carrying a name, is the same table. A changed cell is
// not.
func TestOutlinesAreComparedByTheirEffectiveTables(t *testing.T) {
	root := project(t, "")
	outlineRecord(t, root, "a/b/one", "        - columns: [x]\n          rows: [[\"1\"]]\n        - rows: [[\"2\"]]\n")
	outlineRecord(t, root, "a/b/two", "        - columns: [x]\n          rows: [[\"1\"]]\n        - name: second\n          columns: [x]\n          rows: [[\"2\"]]\n")
	if got := lintShared(t, root).SharedClaims.Scenarios; len(got) != 1 || len(got[0].Members) != 2 {
		t.Fatalf("want one pair, got %+v", got)
	}
	outlineRecord(t, root, "a/b/two", "        - columns: [x]\n          rows: [[\"1\"]]\n        - rows: [[\"3\"]]\n")
	if got := lintShared(t, root).SharedClaims.Scenarios; len(got) != 0 {
		t.Fatalf("a changed cell is a different table, got %+v", got)
	}
}
