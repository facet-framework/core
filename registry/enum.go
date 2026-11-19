// @facet: registry/integrity/record-validation
// @facet-role: related
package facet

import (
	"sort"
	"strings"
)

// EnumDeclaration is one field's enum-shaped constraint, extracted from a
// data-entity record's canonical_schema. Entity and Path identify the record
// it came from, so a divergence finding can name both sides of a
// discrepancy.
type EnumDeclaration struct {
	// Entity is the owning data-entity record's id.
	Entity string
	// Path is the record's location, for reporting.
	Path string
	// Field is the canonical_schema field name the constraint belongs to.
	Field string
	// Values is the declared value list, in the order written.
	Values []string
}

// enumConstraintSeparators maps recognized constraint prefixes to their value
// delimiters. Bracketed values lists use commas instead of the default pipe.
var enumConstraintSeparators = map[string]byte{
	"enum":    '|',
	"values":  '|',
	"csv":     ',',
	"variant": '|',
}

// ParseEnumConstraint recognizes one constraint string as enum-shaped and
// returns its normalized values, or ok=false when the constraint is not
// enum-shaped at all — which is most constraints ("not-null", "unique",
// "max-length:255" are not enum declarations, and reporting them as such
// would be the defect, not the fix).
func ParseEnumConstraint(constraint string) (values []string, ok bool) {
	s := strings.TrimSpace(constraint)
	idx := strings.IndexByte(s, ':')
	if idx < 0 {
		return nil, false
	}
	prefix := strings.ToLower(strings.TrimSpace(s[:idx]))
	sep, known := enumConstraintSeparators[prefix]
	if !known {
		return nil, false
	}
	rest := strings.TrimSpace(s[idx+1:])
	if rest == "" {
		return nil, false
	}
	if strings.HasPrefix(rest, "[") && strings.HasSuffix(rest, "]") {
		rest = strings.TrimSpace(rest[1 : len(rest)-1])
		sep = ','
	}
	for _, part := range strings.Split(rest, string(sep)) {
		if v := strings.TrimSpace(part); v != "" {
			values = append(values, v)
		}
	}
	if len(values) == 0 {
		return nil, false
	}
	return values, true
}

// EnumDeclarations extracts every enum-shaped constraint from a data-entity
// record's canonical_schema.fields, normalizing all four spellings
// ParseEnumConstraint recognizes. A field with none — including one
// declaring `type: enum` whose values are written some other way —
// contributes nothing; see ParseEnumConstraint for why that is a feature,
// not a gap.
func (r Record) EnumDeclarations() []EnumDeclaration {
	schema, ok := r.Fields["canonical_schema"].(map[string]any)
	if !ok {
		return nil
	}
	rawFields, ok := schema["fields"].([]any)
	if !ok {
		return nil
	}

	var out []EnumDeclaration
	for _, item := range rawFields {
		field, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := field["name"].(string)
		if name == "" {
			continue
		}
		constraints, ok := field["constraints"].([]any)
		if !ok {
			continue
		}
		for _, c := range constraints {
			s, ok := c.(string)
			if !ok {
				continue
			}
			values, ok := ParseEnumConstraint(s)
			if !ok {
				continue
			}
			out = append(out, EnumDeclaration{
				Entity: r.ID,
				Path:   r.Path,
				Field:  name,
				Values: values,
			})
		}
	}
	return out
}

// enumValueSetKey normalizes a value list to an order-independent comparison
// key, so "a|b|c" and "c|b|a" count as the same set — the constraint's
// author chose an order to write it in, not a meaning.
func enumValueSetKey(values []string) string {
	sorted := append([]string(nil), values...)
	sort.Strings(sorted)
	return strings.Join(sorted, "\x00")
}
