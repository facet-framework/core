// @facet: registry/integrity/record-validation
// @facet-role: related

package facet

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/goccy/go-yaml"
)

// legacyRecord is the methodless decoding target, including Path and Category.
type legacyRecord = Record

// loadRecordDualParse is the test-only dual-parse reference for LoadRecord
// parity. Production parses source once; this checks its observable result.
func loadRecordDualParse(path, category string) (Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	rec := legacyRecord{Path: path, Category: category}
	if err := yaml.Unmarshal(data, &rec); err != nil {
		return Record{}, err
	}
	if err := yaml.Unmarshal(data, &rec.Fields); err != nil {
		return Record{}, err
	}
	return rec, nil
}

func decodeRecordOnce(data []byte, rec Record) (Record, error) {
	decoded := loadRecordDecode{record: rec}
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		return Record{}, err
	}
	return decoded.record, nil
}

func decodeLegacyRecordOnce(data []byte, rec legacyRecord) (Record, error) {
	if err := yaml.Unmarshal(data, &rec); err != nil {
		return Record{}, err
	}
	if err := yaml.Unmarshal(data, &rec.Fields); err != nil {
		return Record{}, err
	}
	return rec, nil
}

func TestLoadRecordMatchesDualParseAcrossCategoriesAndYAMLEdges(t *testing.T) {
	valid := `schema_version: &version "0.1"
id: &record_id registry/integrity/record-validation
lifecycle: active
intent:
  summary: >-
    A folded summary
    with a second line.
contract:
  scenarios:
    - id: &scenario parses-once
      statement: |
        Given an alias
        Then it expands
    - id: *scenario
      statement: !!str 007
metadata: {empty: null, integer: 42, truth: true, date: 2026-10-22}
knowledge:
  common_pitfalls: &slot
    question: What changes?
  not_for: *slot
`
	cases := []struct {
		name, category, body string
		wantError            bool
	}{
		{name: "feature aliases and scalar forms", category: "features", body: valid},
		{name: "policy category", category: "policies", body: valid},
		{name: "assumption category", category: "assumptions", body: valid},
		{name: "data entity category", category: "data-entities", body: valid},
		{name: "path and category overrides", category: "features", body: valid + "path: overridden-path\ncategory: overridden-category\n"},
		{name: "path scalar coercion", category: "features", body: valid + "path: 42\n"},
		{name: "category scalar coercion", category: "features", body: valid + "category: true\n"},
		{name: "path type error", category: "features", body: valid + "path: [wrong]\n", wantError: true},
		{name: "category type error", category: "features", body: valid + "category: {wrong: type}\n", wantError: true},
		{name: "string scalar coercion", category: "features", body: "id: 42\nschema_version: true\nlifecycle: null\n"},
		{name: "merge aliases", category: "features", body: "defaults: &defaults\n  summary: inherited\n  scalar: 42\nintent:\n  <<: *defaults\n  summary: overridden\nid: merged\nschema_version: '0.1'\nlifecycle: active\n"},
		{name: "multiple documents", category: "features", body: "---\nid: first\nschema_version: '0.1'\nlifecycle: active\n---\nid: second\nschema_version: '0.1'\nlifecycle: planned\n"},
		{name: "malformed yaml", category: "features", body: "id: [unterminated\n", wantError: true},
		{name: "non mapping document", category: "features", body: "- not\n- a record\n", wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "record.yaml")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			got, gotErr := LoadRecord(path, tc.category)
			want, wantErr := loadRecordDualParse(path, tc.category)
			if (wantErr != nil) != tc.wantError {
				t.Fatalf("legacy fixture error = %v, want error: %v", wantErr, tc.wantError)
			}
			if fmt.Sprintf("%T: %v", gotErr, gotErr) != fmt.Sprintf("%T: %v", wantErr, wantErr) {
				t.Fatalf("error = %T: %v, want %T: %v", gotErr, gotErr, wantErr, wantErr)
			}
			if gotErr == nil && !reflect.DeepEqual(got, want) {
				t.Fatalf("record differs from dual parse:\n got: %#v\nwant: %#v", got, want)
			}
		})
	}
}

func TestLoadRecordDecodeMatchesLegacyWhenReused(t *testing.T) {
	initial := Record{
		Path:          "existing-path",
		Category:      "existing-category",
		ID:            "existing-id",
		SchemaVersion: "existing-version",
		Lifecycle:     "existing-lifecycle",
		Fields:        map[string]any{"existing": "field"},
	}
	for _, body := range []string{
		"id: replacement\npath: decoded-path\ncategory: decoded-category\n",
		"intent:\n  summary: leaves typed fields intact\n",
	} {
		got, gotErr := decodeRecordOnce([]byte(body), initial)
		want, wantErr := decodeLegacyRecordOnce([]byte(body), initial)
		if fmt.Sprintf("%T: %v", gotErr, gotErr) != fmt.Sprintf("%T: %v", wantErr, wantErr) {
			t.Fatalf("reuse error = %T: %v, want %T: %v", gotErr, gotErr, wantErr, wantErr)
		}
		if gotErr == nil && !reflect.DeepEqual(got, want) {
			t.Fatalf("reused record differs from legacy decode:\n got: %#v\nwant: %#v", got, want)
		}
	}
}

func BenchmarkLoadRecord(b *testing.B) {
	path := PackageTestPath(b, "testdata", "representative-record.yaml")
	for _, loader := range []struct {
		name string
		load func(string, string) (Record, error)
	}{
		{name: "single-parse", load: LoadRecord},
		{name: "dual-parse-legacy", load: loadRecordDualParse},
	} {
		b.Run(loader.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := loader.load(path, "features"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
