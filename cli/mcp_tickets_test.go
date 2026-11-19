package cli

import (
	"path/filepath"
	"testing"

	"github.com/facet-framework/core/registry"
)

// An agent reading a record gets every ticket it names with the tracker and a
// link, so it can follow the record to the why that the specification does
// not carry (FACET §3.7.1). Asserted through featureGet, the function the MCP
// tool calls — not through ResolveTicket beside it.
func TestFeatureGetResolvesTicketsAgainstTheProjectsTrackers(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".facetrc.yaml"), `schema_version: "0.1"
trackers:
  - id: jira
    pattern: '^[A-Z][A-Z0-9_]+-[0-9]+$'
    url: https://example.atlassian.net/browse/{key}
  - id: github
    pattern: '^(?P<repo>[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)#(?P<number>[0-9]+)$'
    url: https://github.com/{repo}/issues/{number}
`)
	write(t, filepath.Join(root, "docs/facets/features/billing/invoice/send.yaml"), `schema_version: "0.1"
id: billing/invoice/send
lifecycle: active
intent:
  summary: Sends the invoice.
contract:
  scenarios:
    - id: invoice-is-sent
      statement: Given a paid order When it settles Then the invoice is sent
      tickets:
        - key: KAN-2
          relation: implements
lineage:
  owner:
    team: billing
  created_at: "2026-10-09"
  tickets:
    - acme/billing#7
`)
	out, err := featureGet(facet.OpenSet(root), getInput{ID: "billing/invoice/send"})
	if err != nil {
		t.Fatal(err)
	}
	got := out.Records[0].Tickets
	if len(got) != 2 {
		t.Fatalf("tickets = %+v, want the two the record names", got)
	}
	want := map[string]ticketView{
		"KAN-2": {Key: "KAN-2", Source: "contract.scenarios.tickets", Scenario: "invoice-is-sent",
			Relation: "implements", Tracker: "jira", URL: "https://example.atlassian.net/browse/KAN-2"},
		"acme/billing#7": {Key: "acme/billing#7", Source: "lineage.tickets",
			Relation: "relates", Tracker: "github", URL: "https://github.com/acme/billing/issues/7"},
	}
	for _, g := range got {
		if g != want[g.Key] {
			t.Errorf("ticket %s = %+v, want %+v", g.Key, g, want[g.Key])
		}
	}
}
