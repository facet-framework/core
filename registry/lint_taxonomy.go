// Checks record IDs against the product taxonomy.
package facet

import (
	"fmt"
	"strings"
)

// lintTaxonomy holds every record's id against the declared product
// decomposition, and reports a declared node nothing was written under.
func lintTaxonomy(reg Registry, rep *Report, features []Record, tax *Taxonomy) {
	used := map[string]bool{}

	for _, rec := range features {
		domain, capability, _, ok := SplitSegments(rec.ID)
		if !ok {
			// A malformed id is the schema's finding, not this one's. Reporting
			// it twice would make one defect look like two.
			continue
		}
		used[domain] = true
		used[domain+"/"+capability] = true

		if !tax.DomainDeclared(domain) {
			rep.add(Finding{
				Severity: SeverityError,
				Check:    "taxonomy",
				ID:       rec.ID,
				Path:     reg.Rel(rec.Path),
				Message:  fmt.Sprintf("id names domain %q, which the taxonomy does not declare", domain),
				Hint: fmt.Sprintf("declared domains: %s — correct the id, or declare the domain in %s",
					nameList(tax.DomainNames()), reg.Rel(reg.TaxonomyPath())),
			})
			continue
		}

		if !tax.CapabilityDeclared(domain, capability) {
			rep.add(Finding{
				Severity: SeverityError,
				Check:    "taxonomy",
				ID:       rec.ID,
				Path:     reg.Rel(rec.Path),
				Message: fmt.Sprintf("id names capability %q, which the taxonomy does not declare under domain %q",
					capability, domain),

				Hint: fmt.Sprintf("capabilities under %q: %s — correct the id, or declare the capability in %s; if the feature fits none of them, check it against the sizing test (FACET §2.1.1) before adding one",
					domain, nameList(tax.CapabilityNames(domain)), reg.Rel(reg.TaxonomyPath())),
			})
		}
	}

	taxPath := reg.Rel(reg.TaxonomyPath())
	for _, domain := range tax.DomainNames() {
		caps := tax.CapabilityNames(domain)
		if len(caps) == 0 {
			rep.add(Finding{
				Severity: SeverityWarning,
				Check:    "taxonomy",
				Path:     taxPath,
				Message:  fmt.Sprintf("domain %q declares no capabilities", domain),
				Hint:     "declare the capabilities under it, or remove the domain",
			})
			continue
		}
		if !used[domain] {
			rep.add(Finding{
				Severity: SeverityWarning,
				Check:    "taxonomy",
				Path:     taxPath,
				Message:  fmt.Sprintf("domain %q is declared but no record is written under it", domain),
				Hint:     "write the records, or remove the domain if the product does not have it",
			})
			continue
		}
		for _, capability := range caps {
			if used[domain+"/"+capability] {
				continue
			}
			rep.add(Finding{
				Severity: SeverityWarning,
				Check:    "taxonomy",
				Path:     taxPath,
				Message:  fmt.Sprintf("capability %q under domain %q is declared but no record is written under it", capability, domain),
				Hint:     "write the records, or remove the capability if the product does not have it",
			})
		}
	}
}

// nameList renders declared names for a hint, or says there are none.
func nameList(names []string) string {
	if len(names) == 0 {
		return "(none declared)"
	}
	return strings.Join(names, ", ")
}
