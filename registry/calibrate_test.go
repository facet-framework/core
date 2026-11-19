// Fixtures here contain example @facet: tags; the file demonstrates syntax
// rather than implementing anything, so it opts out of scanning.
// @facet-ignore-file

package facet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// calibrateProject writes n records under the given domain/capability, each
// with the given scenario count, all anchored in one file per record.
func calibrateProject(t *testing.T, root string, domain, capability string, n, scenarios int) {
	t.Helper()
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s/%s/feature-%d", domain, capability, i)
		var body strings.Builder
		for s := 0; s < scenarios; s++ {
			body.WriteString(fmt.Sprintf("    - id: scenario-%d\n", s))
			body.WriteString("      statement: |\n        Given a customer\n        When they act\n        Then something observable happens\n")
		}
		if scenarios == 0 {
			body.WriteString("    []\n")
		}
		mustWrite(t, filepath.Join(root, "docs/facets/features", domain, capability, fmt.Sprintf("feature-%d.yaml", i)),
			`schema_version: "0.1"
id: `+id+`
lifecycle: active
intent:
  summary: |
    Does something a customer can name, for a reason they would recognise.
contract:
  scenarios:
`+body.String()+`lineage:
  owner:
    team: billing
  created_at: "2026-09-11"
`)
		mustWrite(t, filepath.Join(root, fmt.Sprintf("src/%s-%s-%d.ts", domain, capability, i)),
			"// @facet: "+id+"\n// @facet-role: primary\nexport function run() {}\n")
	}
}

func calibrate(t *testing.T, root string) Calibration {
	t.Helper()
	cal, err := Calibrate(Open(root), ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return cal
}

func hasReading(cal Calibration, substr string) bool {
	for _, r := range cal.Readings {
		if strings.Contains(r, substr) {
			return true
		}
	}
	return false
}

// A large registry, in miniature: every record carries one scenario,
// which is what documenting at scenario granularity looks like from outside.
func TestCalibrateNamesScenarioGranularity(t *testing.T) {
	root := t.TempDir()
	calibrateProject(t, root, "billing", "invoice", 12, 1)
	cal := calibrate(t, root)

	if !hasReading(cal, "scenario granularity") {
		t.Fatalf("expected the reading to name scenario granularity, got %+v", cal.Readings)
	}
	if got := cal.find(MetricScenariosPerRecord); got.Median != 1 || got.Below != 12 {
		t.Fatalf("expected median 1 with 12 below the band, got %+v", got)
	}
}

// A skewed sample distinguishes median 2 from mean 6. Flat fixtures cannot
// detect a mean reported as a median; rendering tests only verify supplied values.
func TestCalibrateReportsTheMedianAndNotTheMean(t *testing.T) {
	root := t.TempDir()
	calibrateProject(t, root, "billing", "invoice", 8, 2)
	calibrateProject(t, root, "billing", "bulk", 1, 38)

	got := calibrate(t, root).find(MetricScenariosPerRecord)

	// The precondition, and the whole point of this fixture: it must be skewed,
	// or median and mean coincide and the assertion below holds for either.
	const mean = float64(8*2+38) / 9
	if got.Samples != 9 || got.Min != 2 || got.Max != 38 {
		t.Fatalf("this is not the skewed corpus the case needs: %+v", got)
	}
	if mean == 2 {
		t.Fatal("the fixture's mean and median coincide, so it proves nothing")
	}

	if got.Median != 2 {
		t.Errorf("median = %v, want 2 — the mean of this corpus is %v", got.Median, mean)
	}
}

// A small registry: few records, each carrying a product area's worth of
// behaviour.
func TestCalibrateNamesCapabilityGranularity(t *testing.T) {
	root := t.TempDir()
	calibrateProject(t, root, "billing", "invoice", 6, 20)
	cal := calibrate(t, root)

	if !hasReading(cal, "capability granularity") {
		t.Fatalf("expected the reading to name capability granularity, got %+v", cal.Readings)
	}
}

func TestCalibrateNamesUninformativeDomain(t *testing.T) {
	root := t.TempDir()
	calibrateProject(t, root, "platform", "cicd", 13, 5)
	cal := calibrate(t, root)

	if !hasReading(cal, "domain segment carries no information") {
		t.Fatalf("expected the reading to name the flat domain, got %+v", cal.Readings)
	}
}

func TestCalibrateAcceptsAHealthyShape(t *testing.T) {
	root := t.TempDir()
	calibrateProject(t, root, "billing", "invoice", 4, 5)
	calibrateProject(t, root, "billing", "dunning", 3, 4)
	calibrateProject(t, root, "catalog", "search", 5, 6)
	calibrateProject(t, root, "catalog", "ranking", 3, 3)
	calibrateProject(t, root, "identity", "session", 4, 7)
	calibrateProject(t, root, "identity", "credential", 3, 5)
	cal := calibrate(t, root)

	if len(cal.Readings) != 1 || !hasReading(cal, "inside the bands") {
		t.Fatalf("expected a clean shape to report as inside the bands, got %+v", cal.Readings)
	}
}

// A tiny sample does not support a registry-shape diagnosis, so calibration
// readings remain empty below the minimum sample size.
func TestCalibrateSilentOnATinyRegistry(t *testing.T) {
	root := t.TempDir()
	calibrateProject(t, root, "billing", "invoice", 3, 1)
	if cal := calibrate(t, root); len(cal.Readings) != 0 {
		t.Fatalf("expected no reading for a three-record registry, got %+v", cal.Readings)
	}
}

// Silent must mean an empty array on the wire, never null. A nil slice
// marshals to `null`, and a JSON consumer that iterates the declared array
// must receive [] rather than null — otherwise the registries too small to
// earn a reading are exactly the ones that break it.
func TestCalibrationReadingsAreNeverNullOnTheWire(t *testing.T) {
	root := t.TempDir()
	calibrateProject(t, root, "billing", "invoice", 3, 1)
	cal := calibrate(t, root)

	if cal.Readings == nil {
		t.Fatal("Readings is nil; it must be an empty slice so JSON carries []")
	}
	body, err := json.Marshal(cal)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"readings":null`) {
		t.Fatalf("readings marshalled as null: %s", body)
	}
	if !strings.Contains(string(body), `"readings":[]`) {
		t.Fatalf("want an empty array, got %s", body)
	}
}

// A file carrying both a primary and a related tag for one record is one
// record's file, not two. The metric is named "records per anchored file".
func TestCalibrateCountsRecordsPerFileNotAnchors(t *testing.T) {
	root := t.TempDir()
	calibrateProject(t, root, "billing", "invoice", 6, 3)
	// A second tag for the same record in the same file.
	extra := filepath.Join(root, "src/billing-invoice-0.ts")
	body, err := os.ReadFile(extra)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, extra, string(body)+"\n// @facet: billing/invoice/feature-0\n// @facet-role: related\n")

	m := calibrate(t, root).find(MetricRecordsPerFile)
	if m.Max != 1 {
		t.Fatalf("want at most one record per file, got max %d", m.Max)
	}
}

// The command has to work before the taxonomy exists: the registries most in
// need of the answer are the ones that never declared one.
func TestCalibrateWorksWithoutTaxonomy(t *testing.T) {
	root := t.TempDir()
	calibrateProject(t, root, "billing", "invoice", 8, 1)
	cal := calibrate(t, root)

	if cal.HasTaxonomy {
		t.Fatal("expected HasTaxonomy false with no file")
	}
	if cal.Domains != 1 || cal.Capabilities != 1 {
		t.Fatalf("expected grouping to fall back to the id segments, got %d domains and %d capabilities", cal.Domains, cal.Capabilities)
	}
}

// A declared capability with nothing under it is the finding, not an absence.
// Dropping it would report a registry as healthier the more of it was missing.
func TestCalibrateCountsEmptyDeclaredNodes(t *testing.T) {
	root := t.TempDir()
	calibrateProject(t, root, "billing", "invoice", 8, 4)
	mustWrite(t, filepath.Join(root, "docs/facets/taxonomy.yaml"), `schema_version: "0.1"
domains:
  billing:
    title: Billing
    capabilities:
      invoice:
        title: Invoices
      dunning:
        title: Dunning
      refund:
        title: Refunds
`)
	cal := calibrate(t, root)

	if !cal.HasTaxonomy {
		t.Fatal("expected HasTaxonomy true")
	}
	if cal.Capabilities != 3 {
		t.Fatalf("expected the two empty capabilities to count, got %d", cal.Capabilities)
	}
	m := cal.find(MetricRecordsPerCapability)
	if m.Samples != 3 || m.Below != 2 {
		t.Fatalf("expected the empty capabilities to appear as zeroes below the band, got %+v", m)
	}
}

// Report contrasting distributions within the same registry: well-sized
// records, one-record capabilities and capabilities holding many records.
// The tail alone does not describe the whole registry.
func TestCalibrateNamesTheContrastBesideTheThinTail(t *testing.T) {
	root := t.TempDir()
	calibrateProject(t, root, "billing", "invoice", 8, 5)
	calibrateProject(t, root, "billing", "dunning", 1, 5)
	calibrateProject(t, root, "billing", "refunds", 1, 5)
	calibrateProject(t, root, "catalog", "search", 1, 5)
	calibrateProject(t, root, "catalog", "ranking", 1, 5)
	calibrateProject(t, root, "identity", "session", 1, 5)
	calibrateProject(t, root, "identity", "credential", 1, 5)
	cal := calibrate(t, root)

	if !hasReading(cal, "repeats the feature name") {
		t.Fatalf("expected the thin-capability reading, got %+v", cal.Readings)
	}
	if !hasReading(cal, "the largest holds 8") {
		t.Fatalf("the reading must carry the working end of the same registry, got %+v", cal.Readings)
	}
	if !hasReading(cal, "the work is the tail rather than the registry") {
		t.Fatalf("the reading must say which part is the work, got %+v", cal.Readings)
	}
}

// Where there is no contrast to draw — every capability holds one record — the
// reading stays the plain one. Claiming a working practice that is not there
// would be worse than saying less.
func TestCalibrateDrawsNoContrastWhereThereIsNone(t *testing.T) {
	root := t.TempDir()
	for _, c := range []struct{ domain, capability string }{
		{"billing", "invoice"}, {"billing", "dunning"}, {"billing", "refunds"},
		{"catalog", "search"}, {"catalog", "ranking"}, {"catalog", "facets"},
		{"identity", "session"}, {"identity", "credential"},
	} {
		calibrateProject(t, root, c.domain, c.capability, 1, 5)
	}
	cal := calibrate(t, root)

	if !hasReading(cal, "repeats the feature name") {
		t.Fatalf("expected the thin-capability reading, got %+v", cal.Readings)
	}
	if hasReading(cal, "the largest holds") {
		t.Fatalf("there is no working end to contrast against, got %+v", cal.Readings)
	}
}

func TestCalibrateReportsUnanchoredHotspotsBesideAHealthyShape(t *testing.T) {
	root := t.TempDir()
	calibrateProject(t, root, "billing", "invoice", 4, 5)
	calibrateProject(t, root, "billing", "dunning", 3, 4)
	calibrateProject(t, root, "catalog", "search", 5, 6)
	calibrateProject(t, root, "identity", "session", 4, 7)

	// calibrateProject writes a three-line anchored file per record, which is
	// too little to establish a neighbourhood by line count. One substantial
	// anchored file does, and then one unanchored file stands out against it.
	var anchored strings.Builder
	anchored.WriteString("// @facet: billing/invoice/feature-0\n// @facet-role: primary\n")
	for i := 0; i < 400; i++ {
		anchored.WriteString("export const documented = 1;\n")
	}
	mustWrite(t, filepath.Join(root, "src/documented.ts"), anchored.String())

	var body strings.Builder
	for i := 0; i < 200; i++ {
		body.WriteString("export const line = 1;\n")
	}
	mustWrite(t, filepath.Join(root, "src/undocumented.ts"), body.String())

	cal := calibrate(t, root)

	if len(cal.Hotspots) != 1 || cal.Hotspots[0].Path != "src/undocumented.ts" {
		t.Fatalf("want the one unanchored file, got %+v", cal.Hotspots)
	}
	if !hasReading(cal, "src/undocumented.ts") {
		t.Fatalf("the reading must name the file, got %+v", cal.Readings)
	}
	if hasReading(cal, "inside the bands") {
		t.Fatalf("a hotspot is a reading, so the fallback line must not appear: %+v", cal.Readings)
	}
}

// Never nil on the wire, for the same reason Readings is not: a JSON consumer
// that iterates the declared array must receive [] rather than null.
func TestCalibrationHotspotsAreNeverNullOnTheWire(t *testing.T) {
	root := t.TempDir()
	calibrateProject(t, root, "billing", "invoice", 6, 5)

	body, err := json.Marshal(calibrate(t, root))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"hotspots":[]`) {
		t.Fatalf("want an empty array for hotspots, got %s", body)
	}
}

// thinCapability writes one capability holding exactly one feature, with the
// feature named as the caller asks. The shared helper names every feature
// "feature-N", which is the one thing these cases turn on.
func thinCapability(t *testing.T, root, domain, capability, feature string) {
	t.Helper()
	id := domain + "/" + capability + "/" + feature
	mustWrite(t, filepath.Join(root, "docs/facets/features", domain, capability, feature+".yaml"),
		`schema_version: "0.1"
id: `+id+`
lifecycle: active
intent:
  summary: |
    Does something a customer can name, for a reason they would recognise.
contract:
  scenarios:
    - id: it-happens
      statement: |
        Given a customer
        When they act
        Then something observable happens
lineage:
  owner:
    team: billing
  created_at: "2026-09-20"
`)
	mustWrite(t, filepath.Join(root, "src/"+domain+"-"+capability+"-"+feature+".ts"),
		"// @facet: "+id+"\n// @facet-role: primary\nexport function run() {}\n")
}

// Echoed names count lexical repetition: the capability segment appears
// verbatim or as a prefix or suffix of the feature segment.
func TestCalibrateCountsCapabilitiesThatEchoTheirFeatureName(t *testing.T) {
	root := t.TempDir()
	// Identical, the plainest case: job-heartbeat/job-heartbeat.
	thinCapability(t, root, "scheduler", "job-heartbeat", "job-heartbeat")
	// Suffix: quality-check/quality-check-run.
	thinCapability(t, root, "scheduler", "quality-check", "quality-check-run")
	// Neither — a thin capability whose name says something the feature does
	// not. This is the case the guide says is legitimate, and counting it
	// would make the number meaningless.
	thinCapability(t, root, "billing", "invoice", "monthly-statement")
	// Enough records that the reading is not suppressed as a tiny registry.
	calibrateProject(t, root, "catalog", "search", 6, 4)

	cal := calibrate(t, root)

	// The fixture asserts its own premise: three thin capabilities exist, or
	// this case measures an empty set.
	if cal.Capabilities < 4 {
		t.Fatalf("the fixture must carry four capabilities, got %d", cal.Capabilities)
	}
	if cal.EchoedNames != 2 {
		t.Errorf("two of the three thin capabilities echo their feature name, counted %d", cal.EchoedNames)
	}
}

// A capability inside the band is not thin, and its first feature sharing its
// name is not the thing this counts.
func TestAHealthyCapabilityIsNotCountedAsAnEcho(t *testing.T) {
	root := t.TempDir()
	calibrateProject(t, root, "billing", "invoice", 6, 4)
	// Six features under billing/invoice, one of them named for it.
	thinCapability(t, root, "billing", "invoice", "invoice")

	cal := calibrate(t, root)

	if cal.EchoedNames != 0 {
		t.Errorf("a capability inside the band must not be counted, got %d", cal.EchoedNames)
	}
}

// The diagnostic sentence includes the measured count so that the computed
// value reaches the user-facing reading.
func TestTheReadingNamesTheCountAndNotAGuess(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 8; i++ {
		name := fmt.Sprintf("thin-%d", i)
		thinCapability(t, root, "scheduler", name, name)
	}
	calibrateProject(t, root, "catalog", "search", 6, 4)

	cal := calibrate(t, root)

	var reading string
	for _, r := range cal.Readings {
		if strings.Contains(r, "capabilities hold fewer than") {
			reading = r
		}
	}
	if reading == "" {
		t.Fatal("no reading about thin capabilities, so this case measures nothing")
	}
	if strings.Contains(reading, "usually") {
		t.Errorf("the reading still guesses a proportion: %s", reading)
	}
	if !strings.Contains(reading, "repeats the feature name") {
		t.Errorf("the reading does not carry the count: %s", reading)
	}
	if !strings.Contains(reading, fmt.Sprintf("in %d of", cal.EchoedNames)) {
		t.Errorf("the reading names a number other than the measured %d: %s", cal.EchoedNames, reading)
	}
}
