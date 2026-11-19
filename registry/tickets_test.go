// Fixtures here contain example @facet: tags; the file demonstrates syntax
// rather than implementing anything, so it opts out of scanning.
// @facet-ignore-file

package facet

import (
	"path/filepath"
	"strings"
	"testing"
)

// ticketsProject builds a one-record project whose lineage and first scenario
// carry the given ticket lists, and lints it against the shipped schema — the
// shipped one, for business_rules_test.go's reason: a hand-written copy would
// keep passing after the real schema stopped enforcing the shape.
func ticketsProject(t *testing.T, scenarioTickets, lineageTickets string) Report {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docs/facets/features/billing/invoice/send.yaml"),
		`schema_version: "0.1"
id: billing/invoice/send
lifecycle: active
intent:
  summary: |
    Sends the invoice once an order is paid.
contract:
  scenarios:
    - id: invoice-is-sent
      statement: |
        Given a paid order
        When payment settles
        Then the invoice is sent
`+scenarioTickets+`lineage:
  owner:
    team: billing
  created_at: "2026-10-08"
`+lineageTickets)
	mustWrite(t, filepath.Join(root, "src/send.ts"), `// @facet: billing/invoice/send
// @facet-role: primary
export function send() {}
`)
	copySchemas(t, root)
	return lintWithSchema(t, Open(root))
}

const validScenarioTickets = `      tickets:
        - KAN-12
        - key: KAN-14
          relation: verifies
`

const validLineageTickets = `  tickets:
    - key: KAN-3
      relation: specifies
      title: Invoice dispatch epic
    - key: example-org/library#12
      relation: implements
      tracker: github
`

// Both shapes — the bare key and the object — are valid on a scenario and on
// lineage, and several tickets per record are the normal case.
func TestTicketReferencesAreValidOnLineageAndScenario(t *testing.T) {
	if errs := ruleSchemaErrors(ticketsProject(t, validScenarioTickets, validLineageTickets)); len(errs) > 0 {
		t.Fatalf("valid ticket references were rejected: %v", errs)
	}
}

// Each shape differs from the valid lists in one place and must be a schema
// error. The valid record is checked first: if it were rejected, every
// rejection below would be about something else and prove nothing.
func TestMalformedTicketReferencesAreRejected(t *testing.T) {
	if errs := ruleSchemaErrors(ticketsProject(t, validScenarioTickets, validLineageTickets)); len(errs) > 0 {
		t.Fatalf("precondition: the valid record must pass first, got %v", errs)
	}

	type swap struct {
		inScenario bool
		from, to   string
	}
	cases := map[string]swap{
		"unknown relation":   {false, "      relation: specifies\n", "      relation: blocks\n"},
		"unknown field":      {false, "      title: Invoice dispatch epic\n", "      title: Invoice dispatch epic\n      status: open\n"},
		"key missing":        {true, "        - key: KAN-14\n          relation: verifies\n", "        - relation: verifies\n"},
		"empty key":          {true, "        - KAN-12\n", "        - \"\"\n"},
		"duplicate ticket":   {false, "    - key: example-org/library#12\n      relation: implements\n      tracker: github\n", "    - key: KAN-3\n      relation: specifies\n      title: Invoice dispatch epic\n"},
		"tracker not a slug": {false, "      tracker: github\n", "      tracker: GitHub Cloud\n"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			scen, lin := validScenarioTickets, validLineageTickets
			target := &lin
			if c.inScenario {
				target = &scen
			}
			if !strings.Contains(*target, c.from) {
				t.Fatalf("fixture drift: %q is not in the valid list", c.from)
			}
			*target = strings.Replace(*target, c.from, c.to, 1)
			errs := ruleSchemaErrors(ticketsProject(t, scen, lin))
			if len(errs) == 0 {
				t.Fatalf("%s was accepted and must be rejected", name)
			}
			named := false
			for _, e := range errs {
				if strings.Contains(e.Message, "tickets") {
					named = true
				}
			}
			if !named {
				t.Fatalf("%s was rejected, but not at tickets: %v", name, errs)
			}
		})
	}
}

// Ticket references are optional in schema 0.1.
func TestARecordWithoutTicketsIsUnchanged(t *testing.T) {
	rep := ticketsProject(t, "", "")
	if errs := ruleSchemaErrors(rep); len(errs) > 0 {
		t.Fatalf("a record without tickets was rejected: %v", errs)
	}
	for _, f := range rep.Findings {
		if strings.Contains(f.Message, "tickets") {
			t.Fatalf("absence of tickets produced a finding: %v", f)
		}
	}
}
