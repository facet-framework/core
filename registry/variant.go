// @facet: registry/integrity/variant-coverage
// @facet-role: primary

package facet

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// VariantMapName is the deployment's variant declaration inside the records dir.
const VariantMapName = "variants.yaml"

// SharedVariant is the reserved name for code belonging to no single variant.
const SharedVariant = "shared"

type variantEntry struct {
	Paths       []string `yaml:"paths"`
	Description string   `yaml:"description"`
	// Replaces names a variant this one is superseding — how a rewrite is
	// modelled. Reporting only: a lint firing on every unmigrated feature would
	// warn once per record for the whole migration.
	Replaces string `yaml:"replaces"`
}

// VariantMap declares which paths belong to which variant.
type VariantMap struct {
	SchemaVersion string                  `yaml:"schema_version"`
	Variants      map[string]variantEntry `yaml:"variants"`
	Shared        *variantEntry           `yaml:"shared"`
}

// rule is one glob with the variant it assigns.
type rule struct {
	pattern string
	variant string // "" means the shared bucket
}

// Configured reports whether the project uses variants at all. A project
// without a variant map has no variant declarations.
func (m *VariantMap) Configured() bool { return m != nil && len(m.Variants) > 0 }

// Names lists the declared variants, sorted.
func (m *VariantMap) Names() []string {
	if m == nil {
		return nil
	}
	names := make([]string, 0, len(m.Variants))
	for n := range m.Variants {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Declared reports whether a variant name exists in the map.
func (m *VariantMap) Declared(name string) bool {
	if m == nil {
		return false
	}
	_, ok := m.Variants[name]
	return ok
}

// Replaces returns the variant this one supersedes, if any.
func (m *VariantMap) Replaces(name string) string {
	if m == nil {
		return ""
	}
	return m.Variants[name].Replaces
}

// VariantMapPath returns where the variant map lives for a registry.
func (r Registry) VariantMapPath() string {
	return filepath.Join(r.RecordsDir, VariantMapName)
}

// LoadVariantMap reads the deployment's variant declaration. A missing file is
// not an error — it means the project is single-variant.
func LoadVariantMap(path string) (*VariantMap, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &VariantMap{}, nil
	}
	if err != nil {
		return nil, err
	}
	var m VariantMap
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// rules flattens the map into a match list, longest pattern first.
func (m *VariantMap) rules() []rule {
	if m == nil {
		return nil
	}
	var out []rule
	for name, entry := range m.Variants {
		for _, p := range entry.Paths {
			out = append(out, rule{pattern: p, variant: name})
		}
	}
	if m.Shared != nil {
		for _, p := range m.Shared.Paths {
			out = append(out, rule{pattern: p, variant: ""})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].pattern) != len(out[j].pattern) {
			return len(out[i].pattern) > len(out[j].pattern)
		}
		// Deterministic tie-break, so output is reproducible (spec §6.3).
		if out[i].pattern != out[j].pattern {
			return out[i].pattern < out[j].pattern
		}
		return out[i].variant < out[j].variant
	})
	return out
}

// VariantOf classifies a repository-relative path.
func (m *VariantMap) VariantOf(rel string) (string, bool) {
	rel = filepath.ToSlash(rel)
	rules := m.rules()
	if len(rules) == 0 {
		return "", false
	}

	// Collect every rule at the winning specificity: a file matched by two
	// variants at the same specificity is shared, since exclusivity is the test.
	bestLen := -1
	winners := map[string]struct{}{}
	for _, r := range rules {
		if !globMatch(r.pattern, rel) {
			continue
		}
		if len(r.pattern) < bestLen {
			break // rules are sorted longest-first
		}
		if len(r.pattern) > bestLen {
			bestLen = len(r.pattern)
			winners = map[string]struct{}{}
		}
		winners[r.variant] = struct{}{}
	}
	if bestLen < 0 {
		return "", false
	}
	if len(winners) != 1 {
		return SharedVariant, true
	}
	for s := range winners {
		if s == "" {
			return SharedVariant, true
		}
		return s, true
	}
	return SharedVariant, true
}

// globMatch supports `**` across separators in addition to filepath.Match's
// single-segment semantics, because a variant map is written in terms of
// directory trees.
func globMatch(pattern, name string) bool {
	pattern = filepath.ToSlash(pattern)
	if !strings.Contains(pattern, "**") {
		ok, err := filepath.Match(pattern, name)
		return err == nil && ok
	}
	return matchDoubleStar(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchDoubleStar(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			// Trailing ** matches the rest, including nothing.
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(name); i++ {
				if matchDoubleStar(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		ok, err := filepath.Match(pat[0], name[0])
		if err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

// VariantState is one variant's state for a feature.
type VariantState struct {
	Lifecycle string `yaml:"lifecycle"`
	Notes     string `yaml:"notes"`
}

// Variants returns the record's per-variant states, or nil when the record is
// single-variant and carries a plain lifecycle instead.
func (r Record) Variants() map[string]VariantState {
	raw, ok := r.Fields["variants"].(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make(map[string]VariantState, len(raw))
	for name, v := range raw {
		entry, ok := v.(map[string]any)
		if !ok {
			continue
		}
		lifecycle, _ := entry["lifecycle"].(string)
		notes, _ := entry["notes"].(string)
		out[name] = VariantState{Lifecycle: lifecycle, Notes: notes}
	}
	return out
}

// LifecycleOn reports the record's lifecycle for one variant. A record without
// a variants block applies its single lifecycle to every variant.
func (r Record) LifecycleOn(variant string) (string, bool) {
	variants := r.Variants()
	if variants == nil {
		return r.Lifecycle, r.Lifecycle != ""
	}
	state, ok := variants[variant]
	return state.Lifecycle, ok
}

// Flag is a feature flag gating a feature.
type Flag struct {
	Key      string
	System   string
	Variants []string
	Rollout  string
	RetireBy string
}

// Flags returns the record's declared feature flags.
func (r Record) Flags() []Flag {
	raw, ok := r.Fields["flags"].([]any)
	if !ok {
		return nil
	}
	out := make([]Flag, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		f := Flag{}
		f.Key, _ = entry["key"].(string)
		f.System, _ = entry["system"].(string)
		f.Rollout, _ = entry["rollout"].(string)
		f.RetireBy, _ = entry["retire_by"].(string)
		if variants, ok := entry["variants"].([]any); ok {
			for _, s := range variants {
				if name, ok := s.(string); ok {
					f.Variants = append(f.Variants, name)
				}
			}
		}
		if f.Key != "" {
			out = append(out, f)
		}
	}
	return out
}
