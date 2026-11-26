// The fixtures below carry @facet: tags for features that exist only inside a
// temporary directory, so this file opts itself out of anchor scanning:
// @facet-ignore-file

package corpus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/facet-framework/core/registry"
)

// project writes a small repository and returns its default registry.
func project(t *testing.T, files map[string]string) facet.Registry {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return facet.Open(root)
}

const quickAdd = `schema_version: "0.1"
id: tasks/create/quick-add
lifecycle: active
intent:
  summary: Adds a task from one line of input.
`

func TestBuildReadsRecordsAndAnchors(t *testing.T) {
	reg := project(t, map[string]string{
		".facetrc.yaml": "schema_version: \"0.1\"\nrecords: docs/facets\n",
		"docs/facets/features/tasks/create/quick-add.yaml": quickAdd,
		"src/quick_add.ts": "// @facet: tasks/create/quick-add\n// @facet-role: primary\nexport function quickAdd() {}\n",
	})

	corpus, err := Build(reg, nil)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(corpus.Facetrc, "schema_version") {
		t.Errorf("the configuration was not passed through verbatim: %q", corpus.Facetrc)
	}
	if got := corpus.Index; !strings.Contains(got, "docs/facets/features/tasks/create/quick-add.yaml") {
		t.Errorf("index does not list the record:\n%s", got)
	}
	if _, ok := corpus.Records["docs/facets/features/tasks/create/quick-add.yaml"]; !ok {
		t.Errorf("record source missing; got keys %v", keys(corpus.Records))
	}

	anchors := corpus.Scan.Anchors["tasks/create/quick-add"]
	if len(anchors) != 1 {
		t.Fatalf("want 1 anchor, got %d", len(anchors))
	}
	if anchors[0].Path != "src/quick_add.ts" || anchors[0].Line != 1 {
		t.Errorf("anchor lost its place: %+v", anchors[0])
	}
	if anchors[0].Role != "primary" {
		t.Errorf("want role primary, got %q", anchors[0].Role)
	}
	if anchors[0].Excerpt != "export function quickAdd() {}" {
		t.Errorf("the excerpt should quote the line under the tag, got %q", anchors[0].Excerpt)
	}
	if len(corpus.Scan.OrphanAnchors) != 0 {
		t.Errorf("want no orphans, got %v", corpus.Scan.OrphanAnchors)
	}
}

func TestBuildReportsAnAnchorWithoutARecordAsOrphaned(t *testing.T) {
	reg := project(t, map[string]string{
		"docs/facets/features/tasks/create/quick-add.yaml": quickAdd,
		"src/edit.ts": "// @facet: tasks/edit/inline\nexport function edit() {}\n",
	})

	corpus, err := Build(reg, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(corpus.Scan.OrphanAnchors) != 1 {
		t.Fatalf("want 1 orphan, got %v", corpus.Scan.OrphanAnchors)
	}
	if got := corpus.Scan.OrphanAnchors[0].ID; got != "tasks/edit/inline" {
		t.Errorf("want the unresolved id, got %q", got)
	}
	if _, claimed := corpus.Scan.Anchors["tasks/edit/inline"]; claimed {
		t.Error("an orphan must not also be listed as a record's anchor")
	}
}

func TestBuildWritesTheEffectiveConfigurationWhenTheProjectHasNone(t *testing.T) {
	reg := project(t, map[string]string{
		"docs/facets/features/tasks/create/quick-add.yaml": quickAdd,
	})

	corpus, err := Build(reg, nil)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(corpus.Facetrc, "records: docs/facets") {
		t.Errorf("the effective records directory is missing:\n%s", corpus.Facetrc)
	}
	if !strings.Contains(corpus.Facetrc, "# This project has no "+facet.ConfigName) {
		t.Errorf("a generated configuration must say it is generated:\n%s", corpus.Facetrc)
	}
}

func TestBuildRefusesAProjectWithoutRecords(t *testing.T) {
	reg := project(t, map[string]string{"src/main.go": "package main\n"})

	if _, err := Build(reg, nil); err == nil {
		t.Fatal("want an error naming the missing records directory")
	} else if !strings.Contains(err.Error(), "docs/facets") {
		t.Errorf("the error should say where records were expected, got %q", err)
	} else if !strings.Contains(err.Error(), "--root") || strings.Contains(err.Error(), "facet init") {
		t.Errorf("the error must give a supported adoption/root hint: %v", err)
	}
}

func TestBuildReportsEveryPhaseInOrder(t *testing.T) {
	reg := project(t, map[string]string{
		"docs/facets/features/tasks/create/quick-add.yaml": quickAdd,
		"src/quick_add.ts": "// @facet: tasks/create/quick-add\nexport function quickAdd() {}\n",
	})

	var seen []string
	var last Status
	_, err := Build(reg, func(s Status) {
		if len(seen) == 0 || seen[len(seen)-1] != s.Phase {
			seen = append(seen, s.Phase)
		}
		last = s
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"config", "records", "enumerate", "index"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Errorf("want phases %v, got %v", want, seen)
	}
	if last.Done != last.Total || last.Total == 0 {
		t.Errorf("the last report of the index phase should be complete, got %d/%d", last.Done, last.Total)
	}
	if last.Root != reg.Root {
		t.Errorf("every report should name the root it reads, got %q", last.Root)
	}
}

// The cap applies to anchor search. Counting reports on a stride and the
// record phase reports once per record, so those phases have distinct limits.
func TestBuildCapsTheAnchorSearchAndStridesTheCount(t *testing.T) {
	files := map[string]string{
		"docs/facets/features/tasks/create/quick-add.yaml": quickAdd,
	}
	for i := 0; i < 2000; i++ {
		files[filepath.Join("src", "f"+itoa(i)+".ts")] = "export const x = 1;\n"
	}
	reg := project(t, files)

	for i := 0; i < 205; i++ {
		id := fmt.Sprintf("tasks/create/extra-%d", i)
		path := filepath.Join(reg.RecordsDir, "features", id+".yaml")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.ReplaceAll(quickAdd, "tasks/create/quick-add", id)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var indexing, enumerating, recordsReported int
	built, err := Build(reg, func(s Status) {
		switch s.Phase {
		case "index":
			indexing++
		case "enumerate":
			enumerating++
		case "records":
			recordsReported++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if recordsReported != 206 {
		t.Fatalf("record progress was capped or omitted: got %d, want 206", recordsReported)
	}

	// One report per file would be two thousand events for a page that shows a
	// handful. The cap is what keeps a large repository from flooding the wire.
	if updateSteps != 200 || enumerateStride != 64 {
		t.Fatalf("progress contract changed: updates=%d stride=%d", updateSteps, enumerateStride)
	}
	if indexing > 200 {
		t.Errorf("want at most 200 anchor reports, got %d", indexing)
	}
	if indexing < 2 {
		t.Errorf("the index phase must still report progress, got %d", indexing)
	}

	// The counting phase is not capped — it is strided. Over 2000 source files
	// that is roughly 2000/64 reports plus the closing total, which is what the
	// contract now says rather than a cap it does not have.
	if want := built.Scan.Files/64 + 1; enumerating != want {
		t.Errorf("counting progress must occur every 64 files plus completion: got %d, want %d", enumerating, want)
	}
}

func TestStatusOmitsPhaseLocalFieldsSoTheyResetOnTheConsumer(t *testing.T) {
	body, err := json.Marshal(Status{Phase: "enumerate", Records: 5, Root: "/repo"})
	if err != nil {
		t.Fatal(err)
	}

	for _, field := range []string{"file", "done", "total"} {
		if strings.Contains(string(body), `"`+field+`"`) {
			t.Errorf("a phase without %s must omit it, got %s", field, body)
		}
	}
	if !strings.Contains(string(body), `"records":5`) {
		t.Errorf("running totals must survive the wire, got %s", body)
	}
}

// scannedPath returns the ScannedFile for rel, and whether it was present.
func scannedPath(files []ScannedFile, rel string) (ScannedFile, bool) {
	for _, f := range files {
		if f.Path == rel {
			return f, true
		}
	}
	return ScannedFile{}, false
}

// Partition the files already read for anchors and TotalLoc without a second walk.
func TestBuildSplitsFilesIntoAnchoredAndUnanchored(t *testing.T) {
	reg := project(t, map[string]string{
		"docs/facets/features/tasks/create/quick-add.yaml": quickAdd,
		"src/quick_add.ts": "// @facet: tasks/create/quick-add\nexport function quickAdd() {}\n",
		"src/util.ts":      "export function util() {}\n",
	})

	corpus, err := Build(reg, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := scannedPath(corpus.Scan.AnchoredFiles, "src/quick_add.ts"); !ok {
		t.Errorf("src/quick_add.ts carries an anchor, want it in AnchoredFiles, got %+v", corpus.Scan.AnchoredFiles)
	}
	if _, ok := scannedPath(corpus.Scan.UnanchoredFiles, "src/quick_add.ts"); ok {
		t.Errorf("src/quick_add.ts must not also appear in UnanchoredFiles, got %+v", corpus.Scan.UnanchoredFiles)
	}

	if _, ok := scannedPath(corpus.Scan.UnanchoredFiles, "src/util.ts"); !ok {
		t.Errorf("src/util.ts carries no anchor, want it in UnanchoredFiles, got %+v", corpus.Scan.UnanchoredFiles)
	}
	if _, ok := scannedPath(corpus.Scan.AnchoredFiles, "src/util.ts"); ok {
		t.Errorf("src/util.ts must not also appear in AnchoredFiles, got %+v", corpus.Scan.AnchoredFiles)
	}
}

// A foreign-registry anchor is not counted toward this registry's Anchors
// map (a.BelongsTo above), so the file carrying only that tag must not
// register as anchored on this registry's side of the split either — the
// same test, applied one layer up. Qualified-anchor handling lives in the
// registry library's Anchor.BelongsTo.
func TestBuildTreatsAForeignRegistryAnchorAsUnanchoredHere(t *testing.T) {
	reg := project(t, map[string]string{
		"docs/facets/features/tasks/create/quick-add.yaml": quickAdd,
		"src/other.ts": "// @facet: other-registry:tasks/create/quick-add\nexport function other() {}\n",
	})

	corpus, err := Build(reg, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := scannedPath(corpus.Scan.AnchoredFiles, "src/other.ts"); ok {
		t.Errorf("a foreign-registry anchor must not count toward this registry's anchored files, got %+v", corpus.Scan.AnchoredFiles)
	}
	if _, ok := scannedPath(corpus.Scan.UnanchoredFiles, "src/other.ts"); !ok {
		t.Errorf("src/other.ts should be reported unanchored from this registry's point of view, got %+v", corpus.Scan.UnanchoredFiles)
	}
}

// Every file the scan reads carries its own line count, matching what
// TotalLoc already summed — including the zero-length edge case, which must
// still appear rather than being silently skipped.
func TestBuildRecordsLocPerFile(t *testing.T) {
	reg := project(t, map[string]string{
		"docs/facets/features/tasks/create/quick-add.yaml": quickAdd,
		"src/quick_add.ts": "// @facet: tasks/create/quick-add\nexport function quickAdd() {\n  return 1;\n}\n",
		"src/empty.ts":     "",
	})

	corpus, err := Build(reg, nil)
	if err != nil {
		t.Fatal(err)
	}

	anchored, ok := scannedPath(corpus.Scan.AnchoredFiles, "src/quick_add.ts")
	if !ok {
		t.Fatalf("src/quick_add.ts missing from AnchoredFiles: %+v", corpus.Scan.AnchoredFiles)
	}
	if anchored.Loc != 4 {
		t.Errorf("want 4 lines for src/quick_add.ts, got %d", anchored.Loc)
	}

	empty, ok := scannedPath(corpus.Scan.UnanchoredFiles, "src/empty.ts")
	if !ok {
		t.Fatalf("an empty file must still be recorded, got %+v", corpus.Scan.UnanchoredFiles)
	}
	if empty.Loc != 0 {
		t.Errorf("want 0 lines for an empty file, got %d", empty.Loc)
	}
}

// A file the walker never visits at all — excluded via .facetignore — must
// not appear on either side of the split; it was never a candidate to begin
// with, unlike an unanchored file the scan did read.
func TestBuildIgnoredFilesAreOnNeitherSideOfTheSplit(t *testing.T) {
	reg := project(t, map[string]string{
		"docs/facets/features/tasks/create/quick-add.yaml": quickAdd,
		".facetignore":     "vendor/\n",
		"vendor/bundle.js": "export function bundled() {}\n",
		"src/util.ts":      "export function util() {}\n",
	})

	corpus, err := Build(reg, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := scannedPath(corpus.Scan.UnanchoredFiles, "vendor/bundle.js"); ok {
		t.Errorf(".facetignore must keep vendor/bundle.js out of the scan entirely, got %+v", corpus.Scan.UnanchoredFiles)
	}
	if _, ok := scannedPath(corpus.Scan.AnchoredFiles, "vendor/bundle.js"); ok {
		t.Errorf(".facetignore must keep vendor/bundle.js out of the scan entirely, got %+v", corpus.Scan.AnchoredFiles)
	}
	if _, ok := scannedPath(corpus.Scan.UnanchoredFiles, "src/util.ts"); !ok {
		t.Errorf("src/util.ts is not ignored and must still be scanned, got %+v", corpus.Scan.UnanchoredFiles)
	}
}

// A binary file carries no @facet: tag a human wrote, and the prefilter in
// ScanContent (bytes.Contains for the literal marker) means one never
// spuriously matches. It must still land on the unanchored side rather than
// being skipped or crashing the scan — the same read-every-file loop that
// already handles it for TotalLoc today.
func TestBuildClassifiesABinaryFileAsUnanchored(t *testing.T) {
	reg := project(t, map[string]string{
		"docs/facets/features/tasks/create/quick-add.yaml": quickAdd,
	})
	binary := []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0x00, '\n', 0x00, 0x01}
	if err := os.MkdirAll(filepath.Join(reg.Root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reg.Root, "assets/logo.bin"), binary, 0o644); err != nil {
		t.Fatal(err)
	}

	corpus, err := Build(reg, nil)
	if err != nil {
		t.Fatal(err)
	}

	file, ok := scannedPath(corpus.Scan.UnanchoredFiles, "assets/logo.bin")
	if !ok {
		t.Fatalf("a binary file with no anchor must still be recorded as unanchored, got %+v", corpus.Scan.UnanchoredFiles)
	}

	if file.Loc != 2 {
		t.Errorf("want 2 lines (one embedded newline byte, plus the trailing partial line), got %d", file.Loc)
	}
	if _, ok := scannedPath(corpus.Scan.AnchoredFiles, "assets/logo.bin"); ok {
		t.Error("a binary file must never be reported as anchored")
	}
}

// The two lists must never serialise as JSON null: an absent field reads as
// "the host didn't send this" rather than "the registry has zero of
// these", and every consumer would have to special-case it. Mirrors the existing
// nil-safety on OrphanAnchors.
func TestBuildNeverSerialisesTheSplitAsNull(t *testing.T) {
	reg := project(t, map[string]string{
		"docs/facets/features/tasks/create/quick-add.yaml": quickAdd,
	})

	corpus, err := Build(reg, nil)
	if err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(corpus.Scan)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"anchoredFiles":null`, `"unanchoredFiles":null`} {
		if strings.Contains(string(body), field) {
			t.Errorf("must never serialise as null, got %s", body)
		}
	}
}

func TestCountLines(t *testing.T) {
	cases := map[string]int{"": 0, "a\n": 1, "a\nb\n": 2, "a\nb": 2, "\n": 1}
	for input, want := range cases {
		if got := countLines([]byte(input)); got != want {
			t.Errorf("countLines(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestExcerptSkipsTheRoleTagAndBlankLines(t *testing.T) {
	source := "// @facet: a/b/c\n// @facet-role: primary\n\nexport function c() {}\n"
	if got := excerpt([]byte(source), 1); got != "export function c() {}" {
		t.Errorf("want the anchored declaration, got %q", got)
	}
}

func TestRecordIDReadsTheTopLevelScalar(t *testing.T) {
	cases := map[string]string{
		"id: tasks/create/quick-add\n":              "tasks/create/quick-add",
		"schema_version: \"0.1\"\nid: policy/a/b\n": "policy/a/b",
		"id: \"data/x/y\"\n":                        "data/x/y",
		"intent:\n  id: not-the-record\n":           "",
	}
	for source, want := range cases {
		if got := recordID([]byte(source)); got != want {
			t.Errorf("recordID(%q) = %q, want %q", source, got, want)
		}
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// The consumer must be able to tell "no taxonomy declared" from "a taxonomy with
// nothing in it" — the two mean opposite things, and an empty string is how the
// corpus says the first.
func TestCorpusShipsTheTaxonomyUnparsed(t *testing.T) {
	root := t.TempDir()
	mustWriteCorpus(t, filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml"),
		"schema_version: \"0.1\"\nid: billing/invoice/list\nlifecycle: active\nintent:\n  summary: |\n    Lists a customer's invoices so they can reconcile their billing.\ncontract:\n  scenarios:\n    - id: shown\n      statement: |\n        Given invoices\n        Then they are listed\n    - id: empty\n      statement: |\n        Given none\n        Then the empty state is shown\nlineage:\n  owner:\n    team: billing\n  created_at: \"2026-09-11\"\n")

	corpus, err := Build(facet.Open(root), nil)
	if err != nil {
		t.Fatal(err)
	}
	if corpus.Taxonomy != "" {
		t.Fatalf("want an empty taxonomy where none is declared, got %q", corpus.Taxonomy)
	}

	body := "schema_version: \"0.1\"\ndomains:\n  billing:\n    title: Billing\n    capabilities:\n      invoice:\n        title: Invoices\n"
	mustWriteCorpus(t, filepath.Join(root, "docs/facets/taxonomy.yaml"), body)

	corpus, err = Build(facet.Open(root), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Taxonomy is carried verbatim and unparsed, preserving the declared
	// capabilities for the consumer to interpret.
	if corpus.Taxonomy != body {
		t.Fatalf("want the taxonomy shipped verbatim, got %q", corpus.Taxonomy)
	}
}

// Same distinction, same reason, for the glossary: "no glossary declared" and
// "a glossary with nothing in it" are opposite statements, and the consumer links
// a term only where the project has actually defined one.
func TestCorpusShipsTheGlossaryUnparsed(t *testing.T) {
	root := t.TempDir()
	mustWriteCorpus(t, filepath.Join(root, "docs/facets/features/billing/invoice/list.yaml"),
		"schema_version: \"0.1\"\nid: billing/invoice/list\nlifecycle: active\nintent:\n  summary: |\n    Lists a customer's invoices so they can reconcile their billing.\ncontract:\n  scenarios:\n    - id: shown\n      statement: |\n        Given invoices\n        Then they are listed\n    - id: empty\n      statement: |\n        Given none\n        Then the empty state is shown\nlineage:\n  owner:\n    team: billing\n  created_at: \"2026-09-11\"\n")

	corpus, err := Build(facet.Open(root), nil)
	if err != nil {
		t.Fatal(err)
	}
	if corpus.Glossary != "" {
		t.Fatalf("want an empty glossary where none is declared, got %q", corpus.Glossary)
	}

	body := "schema_version: \"0.1\"\nterms:\n  - term: Consignee\n    definition: |\n      The party named in the transport document.\n"
	mustWriteCorpus(t, filepath.Join(root, "docs/facets/glossary.yaml"), body)

	corpus, err = Build(facet.Open(root), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Glossary YAML is carried verbatim and unparsed, preserving term
	// definitions for the consumer to interpret.
	if corpus.Glossary != body {
		t.Fatalf("want the glossary shipped verbatim, got %q", corpus.Glossary)
	}
}

// The bands are normative (FACET 11.4), so the numbers ship from the engine
// rather than being recomputed in the consumer against a second copy of them.
func TestCorpusShipsTheCalibration(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("billing/invoice/list-%d", i)
		mustWriteCorpus(t, filepath.Join(root, "docs/facets/features", id+".yaml"),
			"schema_version: \"0.1\"\nid: "+id+"\nlifecycle: active\nintent:\n  summary: |\n    Lists a customer's invoices so they can reconcile their billing.\ncontract:\n  scenarios:\n    - id: shown\n      statement: |\n        Given invoices\n        Then they are listed\nlineage:\n  owner:\n    team: billing\n  created_at: \"2026-09-11\"\n")
	}

	corpus, err := Build(facet.Open(root), nil)
	if err != nil {
		t.Fatal(err)
	}
	if corpus.Calibration == nil {
		t.Fatal("want a calibration in the corpus")
	}
	if corpus.Calibration.Records != 6 {
		t.Fatalf("Records = %d, want 6", corpus.Calibration.Records)
	}
	if len(corpus.Calibration.Readings) == 0 {
		t.Fatal("want the reading that names the level, not only the numbers")
	}
}

// The corpus carries the same shared-use back-list the linter reports, under
// the key 'sharedClaims'.
func TestCorpusShipsTheSharedClaims(t *testing.T) {
	root := t.TempDir()
	for _, id := range []string{"arithmetic/calculation/difference", "arithmetic/calculation/subtraction"} {
		mustWriteCorpus(t, filepath.Join(root, "docs/facets/features", id+".yaml"),
			"schema_version: \"0.1\"\nid: "+id+"\nlifecycle: active\ncontract:\n  scenarios:\n    - id: basic\n      statement: |\n        Given two integers\n        When one is subtracted from the other\n        Then the difference is returned\n")
	}
	mustWriteCorpus(t, filepath.Join(root, "src", "math.go"),
		"package src\n\n// @facet: arithmetic/calculation/difference\n// @facet-role: primary\n// @facet: arithmetic/calculation/subtraction\n// @facet-role: primary\nfunc Sub(a, b int) int { return a - b }\n")

	corpus, err := Build(facet.Open(root), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(corpus)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		SharedClaims *struct {
			Symbols []struct {
				Symbol        string `json:"symbol"`
				DoublePrimary bool   `json:"double_primary"`
			} `json:"symbols"`
			Scenarios []struct {
				Members []struct {
					Record string `json:"record"`
				} `json:"members"`
			} `json:"scenarios"`
		} `json:"sharedClaims"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	c := wire.SharedClaims
	if c == nil {
		t.Fatalf("corpus carries no sharedClaims: %s", data)
	}
	if len(c.Symbols) != 1 || c.Symbols[0].Symbol != "Sub" || !c.Symbols[0].DoublePrimary {
		t.Fatalf("symbols = %+v", c.Symbols)
	}
	if len(c.Scenarios) != 1 || len(c.Scenarios[0].Members) != 2 {
		t.Fatalf("scenarios = %+v", c.Scenarios)
	}
	if !strings.Contains(string(data), `"incomplete":[]`) {
		t.Fatalf("a complete measurement must ship an empty incomplete list: %s", data)
	}
}

// The reader and 'facet lint' measure the same anchors: a file too large for
// the scanner to read whole yields its anchors from its head only, in both,
// even though the corpus reads the whole file.
func TestCorpusAndLintShareTheLargeFileAnchors(t *testing.T) {
	root := t.TempDir()
	for _, id := range []string{"a/b/one", "a/b/two"} {
		mustWriteCorpus(t, filepath.Join(root, "docs/facets/features", id+".yaml"),
			"schema_version: \"0.1\"\nid: "+id+"\nlifecycle: active\ncontract:\n  scenarios:\n    - id: s\n      statement: |\n        Given "+id+"\n")
		mustWriteCorpus(t, filepath.Join(root, "src", filepath.Base(id)+".js"), "// @facet: "+id+"\nexport function "+filepath.Base(id)+"() {}\n")
	}
	var b strings.Builder
	b.WriteString("package src\n\n")
	for b.Len() < 1<<20+4096 {
		b.WriteString("// " + strings.Repeat("x", 200) + "\n")
	}
	b.WriteString("// @facet: a/b/one\n// @facet-role: primary\n// @facet: a/b/two\n// @facet-role: primary\nfunc Foo() {}\n")
	for b.Len() < 2<<20+4096 {
		b.WriteString("// " + strings.Repeat("y", 200) + "\n")
	}
	mustWriteCorpus(t, filepath.Join(root, "src", "big.go"), b.String())

	rep, err := facet.LintAll(root, facet.LintOptions{Skip: map[string]bool{"schema": true}, Today: "2026-11-24"})
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := Build(facet.Open(root), nil)
	if err != nil {
		t.Fatal(err)
	}
	if corpus.SharedClaims == nil || rep.SharedClaims == nil {
		t.Fatal("premise: both carry the back-list")
	}
	// An admitted anchor would send big.go to the parser, which leaves a file
	// this large unread as a gap.
	if len(rep.SharedClaims.Symbols)+len(rep.SharedClaims.Incomplete) != 0 || len(corpus.SharedClaims.Symbols)+len(corpus.SharedClaims.Incomplete) != 0 {
		t.Fatalf("lint %+v and corpus %+v must both leave out anchors past the scanned head", rep.SharedClaims, corpus.SharedClaims)
	}
}

// corpusClaims builds the corpus and returns its back-list.
func corpusClaims(t *testing.T, root string) *facet.SharedClaims {
	t.Helper()
	corpus, err := Build(facet.Open(root), nil)
	if err != nil {
		t.Fatal(err)
	}
	return corpus.SharedClaims
}

func corpusRecord(t *testing.T, root, id string) {
	t.Helper()
	mustWriteCorpus(t, filepath.Join(root, "docs/facets/features", id+".yaml"),
		"schema_version: \"0.1\"\nid: "+id+"\nlifecycle: active\ncontract:\n  scenarios:\n    - id: s\n      statement: |\n        Given "+id+"\n")
}

// The reader names a source file it could not read, and keeps the rest.
func TestCorpusNamesAnUnreadableSource(t *testing.T) {
	root := t.TempDir()
	corpusRecord(t, root, "a/b/one")
	corpusRecord(t, root, "a/b/two")
	mustWriteCorpus(t, filepath.Join(root, "src/keep.go"), "package src\n\n// @facet: a/b/one\nfunc KeepOne() {}\n\n// @facet: a/b/two\nfunc KeepTwo() {}\n")
	area := filepath.Join(root, "src/area.go")
	mustWriteCorpus(t, area, "package src\n\n// @facet: a/b/one\n// @facet-role: primary\n// @facet: a/b/two\n// @facet-role: primary\nfunc Area() {}\n")
	if got := corpusClaims(t, root); got == nil || len(got.Symbols) != 1 || len(got.Incomplete) != 0 {
		t.Fatalf("premise: the readable file shares area and nothing is missing, got %+v", got)
	}
	if err := os.Chmod(area, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(area, 0o644) })
	if _, err := os.ReadFile(area); err == nil {
		t.Skip("this process reads a file without permissions; the premise cannot be set up")
	}
	got := corpusClaims(t, root)
	if got == nil || len(got.Symbols) != 0 {
		t.Fatalf("want no symbol from the unreadable file, got %+v", got)
	}
	if len(got.Incomplete) != 1 || got.Incomplete[0].Path != "src/area.go" || got.Incomplete[0].Reason != "source not read" {
		t.Fatalf("want the unreadable source named as a gap, got %+v", got.Incomplete)
	}
}

// A record of the read registry that will not load costs the calibration, not
// the back-list: the readable records' claims stay and the broken one is named.
func TestCorpusKeepsTheBackListBesideABrokenRecord(t *testing.T) {
	root := t.TempDir()
	corpusRecord(t, root, "a/b/one")
	corpusRecord(t, root, "a/b/two")
	mustWriteCorpus(t, filepath.Join(root, "src/area.go"), "package src\n\n// @facet: a/b/one\n// @facet-role: primary\n// @facet: a/b/two\n// @facet-role: primary\nfunc Area() {}\n")
	if got := corpusClaims(t, root); got == nil || len(got.Symbols) != 1 {
		t.Fatalf("premise: the two records share area, got %+v", got)
	}
	mustWriteCorpus(t, filepath.Join(root, "docs/facets/features/a/b/broken.yaml"), "bad: [\n")

	got := corpusClaims(t, root)
	if got == nil {
		t.Fatal("a broken record dropped the whole back-list")
	}
	if len(got.Symbols) != 1 || !got.Symbols[0].DoublePrimary {
		t.Fatalf("the readable records lost their claim, got %+v", got.Symbols)
	}
	if len(got.Incomplete) != 1 || got.Incomplete[0].Path != "docs/facets/features/a/b/broken.yaml" {
		t.Fatalf("want the broken record named as a gap, got %+v", got.Incomplete)
	}
}

func mustWriteCorpus(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCorpusCalibratesWithoutASecondScan(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("billing/invoice/list-%d", i)
		mustWriteCorpus(t, filepath.Join(root, "docs/facets/features", id+".yaml"),
			"schema_version: \"0.1\"\nid: "+id+"\nlifecycle: active\nintent:\n  summary: |\n    Lists a customer's invoices so they can reconcile their own billing.\ncontract:\n  scenarios:\n    - id: shown\n      statement: |\n        Given invoices\n        Then they are listed\n    - id: empty\n      statement: |\n        Given none\n        Then the empty state is shown\nlineage:\n  owner:\n    team: billing\n  created_at: \"2026-09-11\"\n")
		mustWriteCorpus(t, filepath.Join(root, fmt.Sprintf("src/list-%d.ts", i)),
			"// @facet: "+id+"\n// @facet-role: primary\nexport const list = 1;\n")
	}

	// The enumerate phase reports each source file once. Assert the walked
	// file count as well, since progress reports alone cannot detect a silent
	// additional scan.
	var enumerated int
	corpus, err := Build(facet.Open(root), func(s Status) {
		if s.Phase == "enumerate" {
			enumerated = s.Done
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if corpus.Calibration == nil {
		t.Fatal("want a calibration")
	}
	if corpus.Calibration.Records != 6 {
		t.Fatalf("Records = %d, want 6", corpus.Calibration.Records)
	}
	// The calibration must see the same anchors the scan reported, not a second
	// opinion gathered separately.
	if corpus.Calibration.AnchoredFiles != len(corpus.Scan.AnchoredFiles) {
		t.Fatalf("calibration counted %d anchored files, the scan %d — they came from different walks",
			corpus.Calibration.AnchoredFiles, len(corpus.Scan.AnchoredFiles))
	}
	if enumerated == 0 {
		t.Fatal("the enumerate phase reported nothing")
	}
}

func TestBuildProgressNamesFilesAndKeepsUnknownTotalsUnmeasured(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("tasks/create/record-%d", i)
		files["docs/facets/features/"+id+".yaml"] = strings.ReplaceAll(quickAdd, "tasks/create/quick-add", id)
	}
	for i := 0; i < 70; i++ {
		files[fmt.Sprintf("src/file-%d.ts", i)] = "export const value = 1;\n"
	}
	reg := project(t, files)
	phases := map[string][]Status{}
	built, err := Build(reg, func(s Status) { phases[s.Phase] = append(phases[s.Phase], s) })
	if err != nil {
		t.Fatal(err)
	}
	if len(phases["config"]) != 1 || phases["config"][0].File != facet.ConfigName {
		t.Fatalf("configuration progress lost its file: %+v", phases["config"])
	}
	if len(phases["records"]) != 3 {
		t.Fatalf("records must report once per record: %+v", phases["records"])
	}
	seenRecords := map[string]bool{}
	for i, s := range phases["records"] {
		if seenRecords[s.File] {
			t.Fatalf("record progress repeated one filename: %s", s.File)
		}
		seenRecords[s.File] = true
		if s.Done != i+1 || s.Total != 3 || s.Records != i+1 || s.File == "" {
			t.Fatalf("record progress lost measured counters or file: %+v", s)
		}
		if _, err := os.Stat(filepath.Join(reg.Root, s.File)); err != nil {
			t.Fatal(err)
		}
	}
	if len(phases["enumerate"]) < 2 {
		t.Fatalf("fixture must cross the counting stride: %+v", phases["enumerate"])
	}
	for _, s := range phases["enumerate"] {
		if s.Total != 0 {
			t.Fatalf("counting phase invented a denominator: %+v", s)
		}
	}
	if first := phases["enumerate"][0]; first.Done != 64 || first.File == "" {
		t.Fatalf("counting stride must name file 64: %+v", first)
	}
	if last := phases["enumerate"][len(phases["enumerate"])-1]; last.Done != built.Scan.Files {
		t.Fatalf("final count differs from completed scan: %+v", last)
	}
	seenIndexFiles := map[string]bool{}
	for _, s := range phases["index"] {
		if seenIndexFiles[s.File] {
			t.Fatalf("anchor progress repeated one filename: %s", s.File)
		}
		seenIndexFiles[s.File] = true
	}
	if len(seenIndexFiles) != built.Scan.Files {
		t.Fatalf("small fixture must report each indexed file: got %d, want %d", len(seenIndexFiles), built.Scan.Files)
	}
	for phase, states := range phases {
		for _, s := range states {
			if s.Root != reg.Root {
				t.Fatalf("%s lost repository root: %+v", phase, s)
			}
			if phase == "index" && (s.File == "" || s.Total != built.Scan.Files || s.Done < 1 || s.Done > s.Total) {
				t.Fatalf("anchor progress lost measured file/total: %+v", s)
			}
		}
	}
}

func TestBuildSplitAccountsForEveryFileAndItsLines(t *testing.T) {
	reg := project(t, map[string]string{
		"docs/facets/features/tasks/create/quick-add.yaml": quickAdd,
		"src/anchored.ts":   "// @facet: tasks/create/quick-add\nexport const x = 1;\n",
		"src/unanchored.ts": "one\ntwo\nthree",
		"src/empty.ts":      "",
	})
	built, err := Build(reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"docs/facets/features/tasks/create/quick-add.yaml": 5, "src/anchored.ts": 2, "src/unanchored.ts": 3, "src/empty.ts": 0}
	seen := map[string]bool{}
	for _, file := range append(append([]ScannedFile{}, built.Scan.AnchoredFiles...), built.Scan.UnanchoredFiles...) {
		if seen[file.Path] {
			t.Fatalf("file appears more than once in split: %s", file.Path)
		}
		seen[file.Path] = true
		lines, ok := want[file.Path]
		if !ok || file.Loc != lines {
			t.Fatalf("unexpected file or line count: %+v", file)
		}
	}
	if len(seen) != 4 || built.Scan.Files != 4 || built.Scan.TotalLoc != 10 {
		t.Fatalf("incomplete file/line partition: files=%d lines=%d seen=%v", built.Scan.Files, built.Scan.TotalLoc, seen)
	}
}
