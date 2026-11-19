// @facet: registry/integrity/record-validation
// @facet-role: related
package facet

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/goccy/go-yaml"
)

// Record is a loaded FACET record. Fields is the full decoded document, kept
// alongside the typed fields so schema validation and generic access work
// without a second parse.
type Record struct {
	Path          string
	Category      string
	ID            string         `yaml:"id"`
	SchemaVersion string         `yaml:"schema_version"`
	Lifecycle     string         `yaml:"lifecycle"`
	Fields        map[string]any `yaml:"-"`
}

// loadRecordDecode is the private, one-source-parse view used by LoadRecord.
// Record deliberately has no YAML hook: callers that unmarshal a Record keep
// the legacy behaviour, including its Path and Category fields. The decoder
// callback reuses the parsed YAML AST for both legacy decode targets.
type loadRecordDecode struct {
	record Record
}

func (d *loadRecordDecode) UnmarshalYAML(unmarshal func(any) error) error {
	if err := unmarshal(&d.record); err != nil {
		return err
	}
	return unmarshal(&d.record.Fields)
}

// Intent returns the record's intent summary, or "" when absent.
func (r Record) Intent() string {
	intent, ok := r.Fields["intent"].(map[string]any)
	if !ok {
		return ""
	}
	s, _ := intent["summary"].(string)
	return s
}

// ContractScenarios lists the ids of the record's contract scenarios, in the
// order they are written.
func (r Record) ContractScenarios() []string {
	contract, ok := r.Fields["contract"].(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := contract["scenarios"].([]any)
	if !ok {
		return nil
	}
	var ids []string
	for _, item := range raw {
		scenario, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := scenario["id"].(string); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// ScenarioStatements lists the prose statement of every contract scenario, in
// declaration order — the Gherkin-style sentence a Given/When/Then is authored
// as. Unlike ContractScenarios, which returns the stable ids, this is the
// human-authored text an identifier extractor (facet errors) reads.
func (r Record) ScenarioStatements() []string {
	contract, ok := r.Fields["contract"].(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := contract["scenarios"].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range raw {
		scenario, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if s, _ := scenario["statement"].(string); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// CommonPitfallsText returns every prose string under
// knowledge.common_pitfalls — the shared answer, plus any per-variant
// override answers — for identifier extraction. A slot with a question but no
// answer contributes nothing: that is a legitimate Knowledge gap
// (KnowledgeGaps reports it), not text to scan.
func (r Record) CommonPitfallsText() []string {
	knowledge, ok := r.Fields["knowledge"].(map[string]any)
	if !ok {
		return nil
	}
	slot, ok := knowledge["common_pitfalls"].(map[string]any)
	if !ok {
		return nil
	}
	var out []string
	if answer, _ := slot["answer"].(string); answer != "" {
		out = append(out, answer)
	}
	bySurface, ok := slot["by_variant"].(map[string]any)
	if !ok {
		return out
	}
	// Sorted so extraction over a record with several overrides produces the
	// same mention order on every run — spec §6.3.
	names := make([]string, 0, len(bySurface))
	for name := range bySurface {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		override, ok := bySurface[name].(map[string]any)
		if !ok {
			continue
		}
		if answer, _ := override["answer"].(string); answer != "" {
			out = append(out, answer)
		}
	}
	return out
}

// verificationKinds are the four buckets `verification` may declare, in the
// order the schema lists them.
var verificationKinds = []string{"unit_tests", "property_tests", "integration_tests", "e2e_tests"}

// VerificationKinds returns the schema-defined test categories in display order.
// Callers receive their own slice and cannot change Core discovery behavior.
func VerificationKinds() []string { return append([]string(nil), verificationKinds...) }

// Provenance is one `proves` entry: a single test named as evidence for a
// single contract scenario. A test_ref with three scenarios in `proves`
// flattens to three Provenance values, one per scenario.
type Provenance struct {
	// Kind is which verification bucket the test was declared under
	// (unit_tests, property_tests, integration_tests, e2e_tests).
	Kind string
	// TestID is the test_ref's own id — a file path, a file-and-test-name
	// pair, or a CI job id, in whatever form the project writes it.
	TestID string
	// Scenario is the contract scenario id this test claims to prove.
	Scenario string
}

// Provenances flattens every verification.*[].proves entry on the record, in
// the order the document declares them.
func (r Record) Provenances() []Provenance {
	verification, ok := r.Fields["verification"].(map[string]any)
	if !ok {
		return nil
	}
	var out []Provenance
	for _, kind := range verificationKinds {
		raw, ok := verification[kind].([]any)
		if !ok {
			continue
		}
		for _, item := range raw {
			ref, ok := item.(map[string]any)
			if !ok {
				continue
			}
			testID, _ := ref["id"].(string)
			proves, ok := ref["proves"].([]any)
			if !ok {
				continue
			}
			for _, p := range proves {
				scenario, ok := p.(string)
				if !ok || scenario == "" {
					continue
				}
				out = append(out, Provenance{Kind: kind, TestID: testID, Scenario: scenario})
			}
		}
	}
	return out
}

// LoadRecord reads and decodes a single record.
func LoadRecord(path, category string) (Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	decoded := loadRecordDecode{record: Record{Path: path, Category: category}}
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		return Record{}, err
	}
	return decoded.record, nil
}

// loadYAMLFields decodes any YAML document into a generic map, for documents
// that are validated against a schema without being feature records.
func loadYAMLFields(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var fields map[string]any
	if err := yaml.Unmarshal(data, &fields); err != nil {
		return nil
	}
	return fields
}

type categoryLoad struct {
	records []Record
	err     error
}

// LoadCategory reads every record of one category, sorted by path so output is
// deterministic — FACET spec 6.3 requires byte-identical results for identical
// inputs at the same registry revision.
func (r Registry) LoadCategory(category string) (records []Record, err error) {
	if r.categorySnapshot != nil {
		if loaded, ok := r.categorySnapshot[category]; ok {
			return loaded.records, loaded.err
		}
		defer func() { r.categorySnapshot[category] = categoryLoad{records, err} }()
	}
	return r.loadCategory(category)
}

func (r Registry) loadCategory(category string) ([]Record, error) {
	dir := r.CategoryDir(category)
	if !isDir(dir) {
		return nil, nil
	}
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".yaml" {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)

	records := make([]Record, 0, len(paths))
	for _, p := range paths {
		rec, err := LoadRecord(p, category)
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, nil
}

// LoadFeatures is the common case: every feature record in the registry.
func (r Registry) LoadFeatures() ([]Record, error) {
	return r.LoadCategory("features")
}
