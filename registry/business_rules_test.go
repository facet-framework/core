// Fixtures here contain example @facet: tags; the file demonstrates syntax
// rather than implementing anything, so it opts out of scanning.
// @facet-ignore-file

package facet

import (
	"path/filepath"
	"strings"
	"testing"
)

// businessRulesProject builds a one-record project whose contract carries the
// given business_rules body, and lints it against the shipped schema.
func businessRulesProject(t *testing.T, rules string) Report {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docs/facets/features/commerce/pricing/discount-codes.yaml"),
		`schema_version: "0.1"
id: commerce/pricing/discount-codes
lifecycle: active
intent:
  summary: |
    Applies a discount code to a cart and caps the discount at half the subtotal.
contract:
  scenarios:
    - id: discount-cap-applied
      statement: |
        Given a code that would produce more than 50% off
        When the code is applied
        Then the discount is exactly 50% of the subtotal
`+rules+`lineage:
  owner:
    team: commerce
  created_at: "2026-10-08"
`)
	mustWrite(t, filepath.Join(root, "src/discount.ts"), `// @facet: commerce/pricing/discount-codes
// @facet-role: primary
export function apply() {}
`)
	copySchemas(t, root)
	return lintWithSchema(t, Open(root))
}

const validBusinessRules = `  business_rules:
    - id: discount-eligibility
      kind: table
      model: rules/pricing.dmn
      element: d_discountEligibility
      states: Whether a code applies to this cart, and with which cap.
      scenarios: [discount-cap-applied]
      completeness: claimed
    - id: discounted-total
      kind: formula
      model: rules/pricing.dmn
      element: bkm_discountedTotal
      states: The total after the capped discount.
`

func ruleSchemaErrors(r Report) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Severity == SeverityError && f.Check == "schema" {
			out = append(out, f)
		}
	}
	return out
}

// A table and a formula, each with every field it may carry, are a valid
// contract under schema 0.1.
func TestBusinessRulesAreAValidPartOfTheContract(t *testing.T) {
	rep := businessRulesProject(t, validBusinessRules)
	if errs := ruleSchemaErrors(rep); len(errs) > 0 {
		t.Fatalf("a valid business_rules list was rejected: %v", errs)
	}
}

// Each shape below differs from the valid list in one place, and each must be
// a schema error naming business_rules. The valid list is checked first in
// the same test: if it were rejected, every rejection here would be about
// something else and prove nothing.
func TestMalformedBusinessRulesAreRejected(t *testing.T) {
	if errs := ruleSchemaErrors(businessRulesProject(t, validBusinessRules)); len(errs) > 0 {
		t.Fatalf("precondition: the valid list must pass first, got %v", errs)
	}

	cases := map[string]struct{ from, to string }{
		"missing states": {
			"      states: Whether a code applies to this cart, and with which cap.\n", ""},
		"unknown kind": {
			"      kind: table\n", "      kind: flowchart\n"},
		"completeness on a formula": {
			"      states: The total after the capped discount.\n",
			"      states: The total after the capped discount.\n      completeness: claimed\n"},
		"model that is not .dmn": {
			"      model: rules/pricing.dmn\n      element: d_discountEligibility\n",
			"      model: rules/pricing.xml\n      element: d_discountEligibility\n"},
		"absolute model path": {
			"      model: rules/pricing.dmn\n      element: d_discountEligibility\n",
			"      model: /rules/pricing.dmn\n      element: d_discountEligibility\n"},
		"unknown field": {
			"      completeness: claimed\n", "      completeness: claimed\n      logic: x > 1\n"},
		"id not kebab-case": {
			"    - id: discount-eligibility\n", "    - id: Discount_Eligibility\n"},
		"kind missing": {
			"      kind: formula\n", ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(validBusinessRules, c.from) {
				t.Fatalf("fixture drift: %q is not in the valid list", c.from)
			}
			bad := strings.Replace(validBusinessRules, c.from, c.to, 1)
			errs := ruleSchemaErrors(businessRulesProject(t, bad))
			if len(errs) == 0 {
				t.Fatalf("%s was accepted and must be rejected", name)
			}
			named := false
			for _, e := range errs {
				if strings.Contains(e.Message, "business_rules") {
					named = true
				}
			}
			if !named {
				t.Fatalf("%s was rejected, but not at business_rules: %v", name, errs)
			}
		})
	}
}

// A record may omit business_rules: the field
// is optional, and its absence is not a finding of any kind.
func TestARecordWithoutBusinessRulesIsUnchanged(t *testing.T) {
	rep := businessRulesProject(t, "")
	if errs := ruleSchemaErrors(rep); len(errs) > 0 {
		t.Fatalf("a record without business_rules was rejected: %v", errs)
	}
	for _, f := range rep.Findings {
		if strings.Contains(f.Message, "business_rules") {
			t.Fatalf("absence of business_rules produced a finding: %v", f)
		}
	}
}
