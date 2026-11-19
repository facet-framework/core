package facet

import (
	"strings"
	"testing"
)

// The parent of the incident: the one record in the registry that carried an
// observation block, and a lineage with edges. Both were dropped by a
// throwaway split script, and nothing said so.
const incidentParent = `schema_version: "0.1"
id: search/query/full-text
intent:
  summary: Finds a phrase across the stored corpus.
contract:
  scenarios:
    - id: a-rare-term-is-found
      statement: Given a corpus, when a rare term is queried, then it is found.
    - id: a-common-term-is-found
      statement: Given a corpus, when a common term is queried, then it is found.
lineage:
  owner:
    team: search
  depends_on:
    - storage/index/inverted
    - storage/blob/chunking
observation:
  slos:
    - id: query-under-50ms
      statement: p95 under 50ms at 100000 chunks.
`

func parse(t *testing.T, name, body string) (string, []byte) {
	t.Helper()
	return name, []byte(body)
}

// 1. The incident itself. Two children that took the scenarios and left
// everything that was not a scenario behind.
func TestASplitThatDropsObservationAndLineageIsReported(t *testing.T) {
	// The fixture asserts its own premise: if the parent did not carry these,
	// a green run would prove nothing about the detector.
	if !strings.Contains(incidentParent, "observation:") || !strings.Contains(incidentParent, "depends_on:") {
		t.Fatal("the parent fixture carries neither observation nor lineage edges, so this case measures something else")
	}

	children := map[string][]byte{}
	n, b := parse(t, "search/query/rare-term", `schema_version: "0.1"
id: search/query/rare-term
intent:
  summary: Finds a rare term.
contract:
  scenarios:
    - id: a-rare-term-is-found
      statement: Given a corpus, when a rare term is queried, then it is found.
lineage:
  owner:
    team: search
`)
	children[n] = b
	n, b = parse(t, "search/query/common-term", `schema_version: "0.1"
id: search/query/common-term
intent:
  summary: Finds a common term.
contract:
  scenarios:
    - id: a-common-term-is-found
      statement: Given a corpus, when a common term is queried, then it is found.
lineage:
  owner:
    team: search
`)
	children[n] = b

	got, err := CheckSplit([]byte(incidentParent), children)
	if err != nil {
		t.Fatalf("CheckSplit: %v", err)
	}

	var sawObservation, sawEdges bool
	for _, l := range got.Losses {
		if l.Facet == "observation" {
			sawObservation = true
		}
		if l.Facet == "lineage" && strings.Contains(l.Detail, "depends_on") {
			sawEdges = true
		}
	}
	if !sawObservation {
		t.Errorf("the observation block was dropped and not reported; losses: %v", got.Losses)
	}
	if !sawEdges {
		t.Errorf("every depends_on edge was dropped and not reported; losses: %v", got.Losses)
	}
}

// 2. The half loss, and the reason this tool goes deeper than facet names. The
// children declare a lineage block, so a name-level check is satisfied — and
// every edge inside it is gone.
func TestLineageThatKeepsItsBlockAndLosesItsEdgesIsReported(t *testing.T) {
	children := map[string][]byte{}
	n, b := parse(t, "search/query/rare-term", `schema_version: "0.1"
id: search/query/rare-term
intent:
  summary: Finds a rare term.
contract:
  scenarios:
    - id: a-rare-term-is-found
      statement: Given a corpus, when a rare term is queried, then it is found.
lineage:
  owner:
    team: search
  depends_on:
    - storage/index/inverted
observation:
  slos:
    - id: query-under-50ms
      statement: p95 under 50ms at 100000 chunks.
`)
	children[n] = b

	got, err := CheckSplit([]byte(incidentParent), children)
	if err != nil {
		t.Fatalf("CheckSplit: %v", err)
	}

	// storage/index/inverted survived; storage/blob/chunking did not.
	var named bool
	for _, l := range got.Losses {
		if l.Facet == "lineage" && strings.Contains(l.Detail, "storage/blob/chunking") {
			named = true
		}
		if strings.Contains(l.Detail, "storage/index/inverted") {
			t.Errorf("an edge that a child kept was reported as lost: %v", l)
		}
	}
	if !named {
		t.Errorf("an edge that no child kept was not reported; losses: %v", got.Losses)
	}
}

// 3. A split that carried everything reports nothing. Without this the
// detector could pass case 1 by reporting every facet every time.
func TestASplitThatCarriesEverythingReportsNothing(t *testing.T) {
	children := map[string][]byte{}
	n, b := parse(t, "search/query/rare-term", `schema_version: "0.1"
id: search/query/rare-term
intent:
  summary: Finds a rare term.
contract:
  scenarios:
    - id: a-rare-term-is-found
      statement: Given a corpus, when a rare term is queried, then it is found.
lineage:
  owner:
    team: search
  depends_on:
    - storage/index/inverted
observation:
  slos:
    - id: query-under-50ms
      statement: p95 under 50ms at 100000 chunks.
`)
	children[n] = b
	n, b = parse(t, "search/query/common-term", `schema_version: "0.1"
id: search/query/common-term
intent:
  summary: Finds a common term.
contract:
  scenarios:
    - id: a-common-term-is-found
      statement: Given a corpus, when a common term is queried, then it is found.
lineage:
  owner:
    team: search
  depends_on:
    - storage/blob/chunking
`)
	children[n] = b

	got, err := CheckSplit([]byte(incidentParent), children)
	if err != nil {
		t.Fatalf("CheckSplit: %v", err)
	}
	if len(got.Losses) != 0 {
		t.Errorf("a clean split reported %d loss(es): %v", len(got.Losses), got.Losses)
	}
}

// 4. Scenarios are the unit a split is made along, so a child holding one of
// two is the split working, not a loss. A detector that reported this would
// fire on every split and be switched off.
func TestScenariosSplitAcrossChildrenAreNotALoss(t *testing.T) {
	children := map[string][]byte{}
	n, b := parse(t, "search/query/rare-term", `schema_version: "0.1"
id: search/query/rare-term
intent:
  summary: Finds a rare term.
contract:
  scenarios:
    - id: a-rare-term-is-found
      statement: Given a corpus, when a rare term is queried, then it is found.
lineage:
  owner:
    team: search
  depends_on:
    - storage/index/inverted
    - storage/blob/chunking
observation:
  slos:
    - id: query-under-50ms
      statement: p95 under 50ms at 100000 chunks.
`)
	children[n] = b

	got, err := CheckSplit([]byte(incidentParent), children)
	if err != nil {
		t.Fatalf("CheckSplit: %v", err)
	}
	for _, l := range got.Losses {
		if l.Facet == "contract" {
			t.Errorf("a scenario taken by one child was reported as lost: %v", l)
		}
	}
	if len(got.Losses) != 0 {
		t.Errorf("nothing was lost here, but %v was reported", got.Losses)
	}
}

// 5. A parent whose facet is present but empty is not something a child can
// lose. Without this an empty lineage block in the parent would report
// against every split.
func TestAnEmptyFacetInTheParentIsNotALoss(t *testing.T) {
	parent := `schema_version: "0.1"
id: search/query/full-text
intent:
  summary: Finds a phrase.
contract:
  scenarios:
    - id: a-rare-term-is-found
      statement: Given a corpus, when a rare term is queried, then it is found.
lineage:
  owner:
    team: search
defects:
  fixes: []
`
	children := map[string][]byte{"search/query/rare-term": []byte(`schema_version: "0.1"
id: search/query/rare-term
intent:
  summary: Finds a rare term.
contract:
  scenarios:
    - id: a-rare-term-is-found
      statement: Given a corpus, when a rare term is queried, then it is found.
lineage:
  owner:
    team: search
`)}

	got, err := CheckSplit([]byte(parent), children)
	if err != nil {
		t.Fatalf("CheckSplit: %v", err)
	}
	for _, l := range got.Losses {
		if l.Facet == "defects" {
			t.Errorf("an empty facet was reported as lost: %v", l)
		}
	}
}
