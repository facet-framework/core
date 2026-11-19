// @facet: registry/integrity/backing-gaps
// @facet-role: primary
package facet

import (
	"regexp"
	"sort"
)

// Backing states a scenario may declare.
const (
	Backed   = "backed"
	Unbacked = "unbacked"
)

// featureIDPattern matches the canonical domain/capability/feature id, and is
// how `unblocked_by` is told from a prose reference. A store name, a team, or
// an issue url is a legitimate answer to "what would supply this"; only an id
// can be resolved against the registry, so only an id is looked up.
var featureIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*/[a-z][a-z0-9-]*/[a-z][a-z0-9-]*$`)

// Backing is one declared backing state: a scenario's own, or one variant's
// override of it.
type Backing struct {
	// Feature is the record's id.
	Feature string `json:"feature"`
	// Scenario is the scenario id the state belongs to.
	Scenario string `json:"scenario"`
	// Variant names the variant this entry overrides, empty for the scenario's
	// shared state.
	Variant string `json:"variant,omitempty"`
	// Path is the record's location, for reporting.
	Path string `json:"path,omitempty"`

	State       string `json:"state"`
	Reason      string `json:"reason,omitempty"`
	UnblockedBy string `json:"unblocked_by,omitempty"`
}

// Backings returns every declared backing state on the record, flattened: one
// entry per scenario that declares a state, plus one per per-variant override.
func (r Record) Backings() []Backing {
	contract, ok := r.Fields["contract"].(map[string]any)
	if !ok {
		return nil
	}
	scenarios, ok := contract["scenarios"].([]any)
	if !ok {
		return nil
	}

	var out []Backing
	for _, raw := range scenarios {
		scenario, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := scenario["id"].(string)
		backing, ok := scenario["backing"].(map[string]any)
		if !ok {
			continue
		}

		entry := backingFrom(backing)
		entry.Feature, entry.Scenario, entry.Path = r.ID, id, r.Path
		if entry.State != "" {
			out = append(out, entry)
		}

		byVariant, ok := backing["by_variant"].(map[string]any)
		if !ok {
			continue
		}
		for variant, rawOverride := range byVariant {
			override, ok := rawOverride.(map[string]any)
			if !ok {
				continue
			}
			o := backingFrom(override)
			if o.State == "" {
				continue
			}
			o.Feature, o.Scenario, o.Path, o.Variant = r.ID, id, r.Path, variant
			out = append(out, o)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Scenario != out[j].Scenario {
			return out[i].Scenario < out[j].Scenario
		}
		return out[i].Variant < out[j].Variant
	})
	return out
}

func backingFrom(m map[string]any) Backing {
	var b Backing
	b.State, _ = m["state"].(string)
	b.Reason, _ = m["reason"].(string)
	b.UnblockedBy, _ = m["unblocked_by"].(string)
	return b
}

// UnbackedScenarios lists every unbacked state across the records, in registry
// order. This is the query the field exists to make answerable: which declared
// behaviour is not backed by data, and what would supply it.
func UnbackedScenarios(records []Record) []Backing {
	var out []Backing
	for _, rec := range records {
		for _, b := range rec.Backings() {
			if b.State == Unbacked {
				out = append(out, b)
			}
		}
	}
	return out
}
