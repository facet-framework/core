// @facet: registry/layout/granularity-calibration
// @facet-role: related

package facet

import (
	"fmt"
	"sort"
	"strings"
)

// Band is the range a metric is ordinarily inside. Guidance, never a rule:
// nothing in this file fails a run, and a project whose shape is genuinely
// unusual keeps its shape.
type Band struct {
	Low  int `json:"low"`
	High int `json:"high"`
}

// Metric is one measurement of the registry's shape, with the band it is
// judged against and how many samples fell outside it.
type Metric struct {
	Name    string  `json:"name"`
	Median  float64 `json:"median"`
	Min     int     `json:"min"`
	Max     int     `json:"max"`
	Samples int     `json:"samples"`
	Below   int     `json:"below"`
	Above   int     `json:"above"`
	Band    Band    `json:"band"`
}

// Calibration is the shape of one registry.
type Calibration struct {
	Registry      string `json:"registry,omitempty"`
	Records       int    `json:"records"`
	Scenarios     int    `json:"scenarios"`
	AnchoredFiles int    `json:"anchoredFiles"`
	Domains       int    `json:"domains"`
	Capabilities  int    `json:"capabilities"`
	HasTaxonomy   bool   `json:"hasTaxonomy"`
	// Knowledge counts answered, pending and not-applicable slots. Counts have
	// no median or band; the fixed struct always marshals as an object.
	Knowledge KnowledgeTally `json:"knowledge"`
	// EchoedNames counts single-feature capabilities whose segment repeats the
	// feature name, including names that differ only by a prefix or suffix.
	EchoedNames int `json:"echoedNames"`
	// SuffixPairs counts sibling IDs shaped as 'x' and 'x-<word>' within one
	// capability. Both records contribute to the metrics; the count flags
	// possible duplication without deciding whether they describe one feature.
	SuffixPairs int      `json:"suffixPairs"`
	Metrics     []Metric `json:"metrics"`
	// Hotspots are the files no record claims, in directories where the
	// neighbours are claimed — the one measurement here taken from the
	// repository rather than from the registry. Never nil, for the same reason
	// Readings is not.
	Hotspots []UnanchoredFile `json:"hotspots"`
	// Readings contains ordered diagnostic sentences. It is never nil, so JSON
	// consumers always receive an array, including [] for a small registry.
	Readings []string `json:"readings"`
}

// Metric names, exported so a caller can find one without matching prose.
const (
	MetricScenariosPerRecord    = "scenarios per record"
	MetricRecordsPerCapability  = "records per capability"
	MetricCapabilitiesPerDomain = "capabilities per domain"
	MetricRecordsPerFile        = "records per anchored file"
)

// The bands, as §11.4 states them.
var (
	bandScenariosPerRecord    = Band{Low: 2, High: 9}
	bandRecordsPerCapability  = Band{Low: 2, High: 12}
	bandCapabilitiesPerDomain = Band{Low: 2, High: 10}
	bandRecordsPerFile        = Band{Low: 1, High: 2}
)

// Calibrate measures one registry's shape.
func Calibrate(reg Registry, opts ScanOptions) (Calibration, error) {
	features, err := reg.LoadFeatures()
	if err != nil {
		return Calibration{}, err
	}
	scanned, walked, err := ScanWithFiles(reg.Root, opts)
	if err != nil {
		return Calibration{}, err
	}
	return CalibrateFrom(reg, features, scanned, walked)
}

// CalibrateFrom measures a registry a caller has already read.
func CalibrateFrom(reg Registry, features []Record, scanned []Anchor, walked []SourceFile) (Calibration, error) {
	taxonomy, err := LoadTaxonomy(reg.TaxonomyPath())
	if err != nil {
		return Calibration{}, err
	}

	cal := Calibration{
		Registry:    reg.Name,
		Records:     len(features),
		HasTaxonomy: taxonomy.Configured(),
	}

	// Without a declared taxonomy, grouping falls back to id segments so
	// calibration remains available before taxonomy adoption.
	recordsPerCapability := map[string]int{}
	capabilitiesPerDomain := map[string]map[string]bool{}
	// The feature names under each capability, so the echo can be counted
	// after the sizes are known. Kept here rather than derived later because
	// readings() sees only the finished Calibration, where the ids are gone.
	featuresPerCapability := map[string][]string{}
	var scenarioCounts []int

	for _, rec := range features {
		cal.Knowledge.Add(rec.KnowledgeCounts())
		n := len(rec.ContractScenarios())
		cal.Scenarios += n
		scenarioCounts = append(scenarioCounts, n)

		domain, capability, feature, ok := SplitSegments(rec.ID)
		if !ok {
			continue
		}
		recordsPerCapability[domain+"/"+capability]++
		featuresPerCapability[domain+"/"+capability] = append(featuresPerCapability[domain+"/"+capability], feature)
		if capabilitiesPerDomain[domain] == nil {
			capabilitiesPerDomain[domain] = map[string]bool{}
		}
		capabilitiesPerDomain[domain][capability] = true
	}

	for _, domain := range taxonomy.DomainNames() {
		if capabilitiesPerDomain[domain] == nil {
			capabilitiesPerDomain[domain] = map[string]bool{}
		}
		for _, capability := range taxonomy.CapabilityNames(domain) {
			capabilitiesPerDomain[domain][capability] = true
			if _, seen := recordsPerCapability[domain+"/"+capability]; !seen {
				recordsPerCapability[domain+"/"+capability] = 0
			}
		}
	}

	// Distinct records per file, not anchors: one file may carry a primary and
	// a related tag for the same record, and counting both would report it as
	// claimed twice by the metric whose name says records.
	idsPerFile := map[string]map[string]bool{}
	for _, a := range scanned {
		if !a.BelongsTo(reg.Name) {
			continue
		}
		rel := reg.Rel(a.Path)
		if idsPerFile[rel] == nil {
			idsPerFile[rel] = map[string]bool{}
		}
		idsPerFile[rel][a.ID] = true
	}
	recordsPerFile := map[string]int{}
	for rel, ids := range idsPerFile {
		recordsPerFile[rel] = len(ids)
	}
	cal.AnchoredFiles = len(recordsPerFile)

	cal.Domains = len(capabilitiesPerDomain)
	for _, caps := range capabilitiesPerDomain {
		cal.Capabilities += len(caps)
	}

	cal.Metrics = []Metric{
		metric(MetricScenariosPerRecord, scenarioCounts, bandScenariosPerRecord),
		metric(MetricRecordsPerCapability, values(recordsPerCapability), bandRecordsPerCapability),
		metric(MetricCapabilitiesPerDomain, sizes(capabilitiesPerDomain), bandCapabilitiesPerDomain),
		metric(MetricRecordsPerFile, values(recordsPerFile), bandRecordsPerFile),
	}
	cal.Hotspots = UnanchoredHotspots(reg.Root, walked, nil)
	if cal.Hotspots == nil {
		cal.Hotspots = []UnanchoredFile{}
	}
	cal.EchoedNames = countEchoedNames(recordsPerCapability, featuresPerCapability, bandRecordsPerCapability.Low)
	cal.SuffixPairs = len(SiblingSuffixPairs(featuresPerCapability))
	cal.Readings = readings(cal)
	return cal, nil
}

func values(m map[string]int) []int {
	out := make([]int, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func sizes(m map[string]map[string]bool) []int {
	out := make([]int, 0, len(m))
	for _, v := range m {
		out = append(out, len(v))
	}
	return out
}

func metric(name string, samples []int, band Band) Metric {
	m := Metric{Name: name, Samples: len(samples), Band: band}
	if len(samples) == 0 {
		return m
	}
	sorted := append([]int(nil), samples...)
	sort.Ints(sorted)
	m.Min = sorted[0]
	m.Max = sorted[len(sorted)-1]
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		m.Median = float64(sorted[mid])
	} else {
		m.Median = float64(sorted[mid-1]+sorted[mid]) / 2
	}
	for _, v := range sorted {
		switch {
		case v < band.Low:
			m.Below++
		case v > band.High:
			m.Above++
		}
	}
	return m
}

// find searches the short, fixed Metrics slice by name without maintaining
// a second index that could disagree with it.
func (c Calibration) find(name string) Metric {
	for _, m := range c.Metrics {
		if m.Name == name {
			return m
		}
	}
	return Metric{}
}

// readings turns the table into sentences, most consequential first.
func readings(c Calibration) []string {
	out := []string{}

	// Readings require at least five records to avoid interpreting a smaller
	// sample as a registry-wide distribution.
	if c.Records < 5 {
		return out
	}

	// Report potential duplicate records before median-based readings because
	// duplicates contribute twice to the measurements those readings interpret.
	if c.SuffixPairs > 0 {
		out = append(out, fmt.Sprintf(
			"%d id pair(s) of the shape 'x' and 'x-<word>' sit in one capability — 'facet lint' names them. Where those are one feature documented twice, the numbers below count it twice: FACET's answer for one feature on two paths is a variant on one record, never a second record (FACET §2.4).",
			c.SuffixPairs))
	}

	scenarios := c.find(MetricScenariosPerRecord)
	perCapability := c.find(MetricRecordsPerCapability)
	perDomain := c.find(MetricCapabilitiesPerDomain)
	perFile := c.find(MetricRecordsPerFile)

	if scenarios.Median <= 1 {
		out = append(out, fmt.Sprintf(
			"records are being written at scenario granularity: %d of %d declare fewer than %d contract scenarios (FACET §2.1.1 gate 3). A record with one observable behaviour is usually a scenario of the record above it.",
			scenarios.Below, scenarios.Samples, scenarios.Band.Low))
	} else if scenarios.Median > float64(scenarios.Band.High) {
		out = append(out, fmt.Sprintf(
			"records are being written at capability granularity: the median declares %s contract scenarios against a band of %d–%d (FACET §2.1.1 gate 3). Past the band the scenarios have usually formed groups that do not reference each other, and those groups are the features.",
			formatMedian(scenarios.Median), scenarios.Band.Low, scenarios.Band.High))
	}

	if c.Domains > 0 && c.Domains <= 2 && c.Records >= 10 {
		out = append(out, fmt.Sprintf(
			"the domain segment carries no information: %d records share %d domain(s). The first id segment is distinguishing nothing, so a reader gets a flat list where the id promises a decomposition.",
			c.Records, c.Domains))
	}

	if perCapability.Median > float64(perCapability.Band.High) {
		out = append(out, fmt.Sprintf(
			"a level is missing between capability and feature: the median capability holds %s records against a band of %d–%d. Either the capabilities are too few, or records that belong together were never grouped.",
			formatMedian(perCapability.Median), perCapability.Band.Low, perCapability.Band.High))
	}

	// Capabilities holding exactly one record are the naming mistake at the
	// other end: the capability and the feature are the same thing said twice.

	if perCapability.Samples >= 4 && perCapability.Below > perCapability.Samples/2 {
		inside := perCapability.Samples - perCapability.Below - perCapability.Above
		if inside > 0 && perCapability.Max >= perCapability.Band.Low {
			out = append(out, fmt.Sprintf(
				"%d of %d capabilities hold fewer than %d records, while %d are inside the band and the largest holds %d; in %d of the thin ones the capability segment repeats the feature name. This is a tail beside a grouping that works elsewhere in the same registry, so the work is the tail rather than the registry.",
				perCapability.Below, perCapability.Samples, perCapability.Band.Low, inside, perCapability.Max, c.EchoedNames))
		} else {
			out = append(out, fmt.Sprintf(
				"%d of %d capabilities hold fewer than %d records; in %d of them the capability segment repeats the feature name.",
				perCapability.Below, perCapability.Samples, perCapability.Band.Low, c.EchoedNames))
		}
	}

	if perDomain.Samples >= 2 && perDomain.Median > float64(perDomain.Band.High) {
		out = append(out, fmt.Sprintf(
			"the median domain declares %s capabilities against a band of %d–%d: the domains are probably too few for the product.",
			formatMedian(perDomain.Median), perDomain.Band.Low, perDomain.Band.High))
	}

	if perFile.Median > float64(perFile.Band.High) {
		out = append(out, fmt.Sprintf(
			"records are finer than the code: the median anchored file is claimed by %s records. When many records point at one file, the boundaries between them exist only in the documentation.",
			formatMedian(perFile.Median)))
	}

	if n := len(c.Hotspots); n > 0 {
		biggest := c.Hotspots[0]
		out = append(out, fmt.Sprintf(
			"%d file(s) carry no anchor in directories that are otherwise documented, the largest being %s at %d lines in a directory %d%% anchored: this is the one gap no record can show you, because every other check starts from a record.",
			n, biggest.Path, biggest.Loc, biggest.DirAnchoredPercent))
	}

	if len(out) == 0 {
		out = append(out, "the registry's shape is inside the bands.")
	}
	return out
}

func formatMedian(v float64) string {
	if v == float64(int(v)) {
		return fmt.Sprintf("%d", int(v))
	}
	return fmt.Sprintf("%.1f", v)
}

// countEchoedNames counts thin capabilities whose segment repeats the name of
// the one feature under it.
func countEchoedNames(sizes map[string]int, features map[string][]string, floor int) int {
	n := 0
	for cap, size := range sizes {
		if size >= floor || size == 0 {
			continue
		}
		names := features[cap]
		if len(names) != 1 {
			continue
		}
		segment := cap
		if i := strings.LastIndex(cap, "/"); i >= 0 {
			segment = cap[i+1:]
		}
		if echoes(segment, names[0]) {
			n++
		}
	}
	return n
}

// SuffixPair is one 'x' beside one 'x-<word>' in the same capability.
type SuffixPair struct {
	Capability string `json:"capability"`
	Base       string `json:"base"`
	Suffixed   string `json:"suffixed"`
	Suffix     string `json:"suffix"`
}

// SiblingSuffixPairs returns sorted pairs of ids x and x-<word> in the same
// capability. Only a single alphanumeric suffix after the last hyphen matches.
// The shape suggests a duplicated feature but can also name legitimate
// neighbours, so callers warn and ask for review rather than reject records.
// @facet: registry/layout/doubled-features
// @facet-role: primary
func SiblingSuffixPairs(featuresPerCapability map[string][]string) []SuffixPair {
	var pairs []SuffixPair
	for capability, names := range featuresPerCapability {
		have := make(map[string]bool, len(names))
		for _, n := range names {
			have[n] = true
		}
		for _, n := range names {
			hyphen := strings.LastIndex(n, "-")
			if hyphen <= 0 || hyphen == len(n)-1 {
				continue
			}
			base, suffix := n[:hyphen], n[hyphen+1:]
			if !isWord(suffix) || !have[base] {
				continue
			}
			pairs = append(pairs, SuffixPair{Capability: capability, Base: base, Suffixed: n, Suffix: suffix})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Capability != pairs[j].Capability {
			return pairs[i].Capability < pairs[j].Capability
		}
		return pairs[i].Suffixed < pairs[j].Suffixed
	})
	return pairs
}

// isWord reports whether a suffix is one word of letters and digits.
func isWord(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// echoes reports whether two id segments name the same thing twice.
func echoes(capability, feature string) bool {
	// HasPrefix also covers equality, so no separate equality branch is needed.
	return strings.HasPrefix(feature, capability) || strings.HasSuffix(feature, capability) ||
		strings.HasPrefix(capability, feature) || strings.HasSuffix(capability, feature)
}
