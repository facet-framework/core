// @facet: registry/integrity/declared-deviations
// @facet-role: primary
package facet

import "sort"

// How a deviation's behaviour was established. Closed, because a reader weighs
// the claim by it — see the field's description in schema/facet.schema.json.
const (
	ConfirmedByCodeReview = "code-review"
	ConfirmedByTest       = "test"
	ConfirmedByRuntime    = "runtime"
)

// Deviation is one declared defect: behaviour the anchored code has that
// nobody wants, recorded against the scenario that describes it.
type Deviation struct {
	// Feature is the record's id.
	Feature string `json:"feature"`
	// Scenario is the scenario id the deviation belongs to.
	Scenario string `json:"scenario"`
	// Path is the record's location, for reporting.
	Path string `json:"path,omitempty"`

	Reason string `json:"reason"`
	// ConfirmedBy is how the behaviour was established, or "" when nobody
	// recorded it. Empty means unrecorded, never unconfirmed.
	ConfirmedBy string `json:"confirmed_by,omitempty"`
	// TrackedAs is where the fix is tracked. Empty covers both an absent field
	// and an explicit null: neither resolves to anything, and the distinction
	// between "nobody said" and "nothing tracks it" is one a reader of the
	// record makes, not one any check here acts on.
	TrackedAs string `json:"tracked_as,omitempty"`
}

// Deviations returns every declared deviation on the record, one per scenario
// that declares one.
func (r Record) Deviations() []Deviation {
	contract, ok := r.Fields["contract"].(map[string]any)
	if !ok {
		return nil
	}
	scenarios, ok := contract["scenarios"].([]any)
	if !ok {
		return nil
	}

	var out []Deviation
	for _, raw := range scenarios {
		scenario, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		declared, ok := scenario["deviation"].(map[string]any)
		if !ok {
			continue
		}
		id, _ := scenario["id"].(string)

		d := Deviation{Feature: r.ID, Scenario: id, Path: r.Path}
		d.Reason, _ = declared["reason"].(string)
		d.ConfirmedBy, _ = declared["confirmed_by"].(string)
		// A YAML null decodes to a nil any, so the type assertion fails and
		// TrackedAs stays empty — which is what an explicit "nothing tracks
		// this" should resolve to.
		d.TrackedAs, _ = declared["tracked_as"].(string)
		if d.Reason == "" {
			// A deviation with no reason is the schema's finding. Carrying it
			// into the query surface would let a claim nobody explained be
			// counted among the ones somebody did.
			continue
		}
		out = append(out, d)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Scenario < out[j].Scenario })
	return out
}

// DeclaredDeviations lists every deviation across the records, in registry
// order. This is the question the field exists to make answerable: which
// documented behaviour is a defect rather than a promise, and where is each one
// tracked.
func DeclaredDeviations(records []Record) []Deviation {
	var out []Deviation
	for _, rec := range records {
		out = append(out, rec.Deviations()...)
	}
	return out
}
