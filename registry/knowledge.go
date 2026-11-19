// @facet: registry/integrity/knowledge-gaps
// @facet-role: primary
package facet

import (
	"sort"
	"strings"
)

// KnowledgeGaps lists the Knowledge slots that carry a question but no answer
// anywhere. These are the facts only a feature owner can supply, so they are
// reported as work for a human rather than filled in.
func (r Record) KnowledgeGaps() []string {
	knowledge, ok := r.Fields["knowledge"].(map[string]any)
	if !ok {
		return nil
	}
	var gaps []string
	for name, raw := range knowledge {
		slot, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if answer, _ := slot["answer"].(string); answer != "" {
			continue
		}

		if derived, ok := slot["derive_from"].([]any); ok && len(derived) > 0 {
			continue
		}
		if slotDeclaredInapplicable(slot) {
			continue
		}
		if slotAnsweredPerVariant(slot) {
			continue
		}
		gaps = append(gaps, name)
	}
	sort.Strings(gaps)
	return gaps
}

// KnowledgeContradictions lists the Knowledge slots that both give an answer
// and declare there is none — an 'answer', or a 'derive_from' instructing a
// generator to render one, beside a 'not_applicable'.
func (r Record) KnowledgeContradictions() []string {
	knowledge, ok := r.Fields["knowledge"].(map[string]any)
	if !ok {
		return nil
	}
	var bad []string
	for name, raw := range knowledge {
		slot, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if _, declared := slot["not_applicable"]; !declared {
			continue
		}
		answer, _ := slot["answer"].(string)
		derived, _ := slot["derive_from"].([]any)
		if strings.TrimSpace(answer) != "" || len(derived) > 0 || slotRendersPerVariant(slot) {
			bad = append(bad, name)
		}
	}
	sort.Strings(bad)
	return bad
}

// slotDeclaredInapplicable reports whether this slot states, completely, that
// its question has no answer for this feature.
func slotDeclaredInapplicable(slot map[string]any) bool {
	na, ok := slot["not_applicable"].(map[string]any)
	if !ok {
		return false
	}
	reason, _ := na["reason"].(string)
	confirmedBy, _ := na["confirmed_by"].(string)
	return strings.TrimSpace(reason) != "" && strings.TrimSpace(confirmedBy) != ""
}

// slotRendersPerVariant reports whether any per-variant override would have the
// generator produce something for this slot — an answer, or a derivation that
// renders one.
func slotRendersPerVariant(slot map[string]any) bool {
	bySurface, ok := slot["by_variant"].(map[string]any)
	if !ok {
		return false
	}
	for _, raw := range bySurface {
		override, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if answer, _ := override["answer"].(string); strings.TrimSpace(answer) != "" {
			return true
		}
		if derived, _ := override["derive_from"].([]any); len(derived) > 0 {
			return true
		}
	}
	return false
}

// slotAnsweredPerVariant reports whether any per-variant override supplies an
// answer for this slot.
func slotAnsweredPerVariant(slot map[string]any) bool {
	bySurface, ok := slot["by_variant"].(map[string]any)
	if !ok {
		return false
	}
	for _, raw := range bySurface {
		override, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if answer, _ := override["answer"].(string); answer != "" {
			return true
		}
	}
	return false
}

// KnowledgeTally is the three-way split of a record's Knowledge slots:
// answered, still open, and declared to have no answer.
//
// @facet: registry/layout/granularity-calibration
// @facet-role: related
type KnowledgeTally struct {
	Answered     int `json:"answered"`
	Open         int `json:"open"`
	Inapplicable int `json:"inapplicable"`
}

// Add folds another tally into this one.
func (k *KnowledgeTally) Add(o KnowledgeTally) {
	k.Answered += o.Answered
	k.Open += o.Open
	k.Inapplicable += o.Inapplicable
}

// KnowledgeCounts splits this record's slots three ways.
//
// @facet: registry/layout/granularity-calibration
// @facet-role: related
func (r Record) KnowledgeCounts() KnowledgeTally {
	knowledge, ok := r.Fields["knowledge"].(map[string]any)
	if !ok {
		return KnowledgeTally{}
	}

	open := map[string]bool{}
	for _, name := range r.KnowledgeGaps() {
		open[name] = true
	}

	var t KnowledgeTally
	for name, raw := range knowledge {
		slot, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch {
		case open[name]:
			t.Open++
		case slotDeclaredInapplicable(slot):
			t.Inapplicable++
		default:
			t.Answered++
		}
	}
	return t
}
