// Checks shared entity vocabularies and error codes.
package facet

import (
	"fmt"
	"sort"
	"strings"
)

// lintEnumDivergence warns when the same data-entity field name has different
// enum value sets after normalization by ParseEnumConstraint. Constraints are
// free text, so this is not a schema error; identically named fields may have
// different meanings and require owner review.
func lintEnumDivergence(reg Registry, rep *Report, entities []Record) {
	byField := map[string][]EnumDeclaration{}
	for _, rec := range entities {
		for _, d := range rec.EnumDeclarations() {
			byField[d.Field] = append(byField[d.Field], d)
		}
	}

	fields := make([]string, 0, len(byField))
	for f := range byField {
		fields = append(fields, f)
	}
	sort.Strings(fields)

	for _, field := range fields {
		groups := map[string][]EnumDeclaration{}
		var keys []string
		for _, d := range byField[field] {
			key := enumValueSetKey(d.Values)
			if _, seen := groups[key]; !seen {
				keys = append(keys, key)
			}
			groups[key] = append(groups[key], d)
		}
		if len(keys) < 2 {
			continue // every entity declaring this field agrees
		}
		sort.Strings(keys)

		// The largest group is the baseline a divergent minority is judged
		// against. Ties keep the lexicographically first key, which keeps
		// the choice deterministic without claiming one arbitrary group is
		// more "correct" than an equally-sized other.
		baseline := keys[0]
		for _, k := range keys[1:] {
			if len(groups[k]) > len(groups[baseline]) {
				baseline = k
			}
		}
		baselineEntities := enumEntityList(groups[baseline])
		baselineValues := strings.Join(groups[baseline][0].Values, "|")

		for _, key := range keys {
			if key == baseline {
				continue
			}
			for _, d := range groups[key] {
				rep.add(Finding{
					Severity: SeverityWarning,
					Check:    "enum-divergence",
					ID:       d.Entity,
					Path:     reg.Rel(d.Path),
					Message: fmt.Sprintf(
						"field %q declares %s, but %s declares %s for the same field name",
						field, strings.Join(d.Values, "|"), baselineEntities, baselineValues),
					Hint: "confirm this is a deliberate difference between entities, or align the value sets",
				})
			}
		}
	}
}

// enumEntityList renders the entities behind one value-set group, sorted for
// a deterministic message.
func enumEntityList(decls []EnumDeclaration) string {
	ids := make([]string, len(decls))
	for i, d := range decls {
		ids[i] = d.Entity
	}
	sort.Strings(ids)
	return strings.Join(ids, ", ")
}

// lintErrorCodeCollisions warns when an identifier appears in different
// domains (the first ID segment). Repetition within a record or domain is
// allowed. The identifier pattern is configurable.
// @facet: registry/integrity/error-code-register
// @facet-role: related
func lintErrorCodeCollisions(reg Registry, rep *Report, features []Record, pattern string) error {
	register, err := ExtractErrorCodes(features, pattern)
	if err != nil {
		return err
	}

	for _, ec := range register.Codes {
		byDomain := map[string][]string{}
		for _, feature := range ec.Features() {
			d := domainOf(feature)
			byDomain[d] = append(byDomain[d], feature)
		}
		if len(byDomain) < 2 {
			continue
		}

		domains := make([]string, 0, len(byDomain))
		for d := range byDomain {
			domains = append(domains, d)
		}
		sort.Strings(domains)

		parts := make([]string, 0, len(domains))
		for _, d := range domains {
			feats := byDomain[d]
			sort.Strings(feats)
			parts = append(parts, fmt.Sprintf("%s (%s)", d, strings.Join(feats, ", ")))
		}

		rep.add(Finding{
			Severity: SeverityWarning,
			Check:    "error-codes",
			ID:       ec.Code,
			Message:  fmt.Sprintf("%q appears in records from more than one domain: %s", ec.Code, strings.Join(parts, "; ")),
			Hint:     "confirm the code was assigned once — grep the whole band before reusing a code across domains, or correct the record that used it by mistake",
		})
	}
	return nil
}

// domainOf returns a feature id's top-level segment — the "domain" in
// domain/capability/feature, which under a banded convention maps 1:1 to a
// team and its error-code band.
func domainOf(id string) string {
	if i := strings.IndexByte(id, '/'); i >= 0 {
		return id[:i]
	}
	return id
}
