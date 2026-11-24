package facet

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/facet-framework/core/internal/spec"
)

func TestSpecConformance(t *testing.T) {
	upstream := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", upstream}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "--initial-branch=main")
	writeFile(t, filepath.Join(upstream, "pricing.md"), `---
schema: facet-spec/0.1
id: pricing/discounts
title: Pricing
status: deprecated
owner: checkout
language: en
---
`+"\n```gherkin\n"+`@case:cap
Scenario Outline: Cap
  Given a cart
  When the code is applied
  Then the discount is <cap>
  Examples: One
    | cap |
    | 50  |
  Examples: Two
    | cap |
    | 40  |
    | 30  |
`+"\n```\n")
	git("add", ".")
	git("-c", "user.name=FACET Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Specification")
	remote := (&url.URL{Scheme: "file", Path: filepath.ToSlash(upstream)}).String()
	const ref = "product:pricing/discounts#cap"
	const address = "pricing/discounts#cap"
	const lineageRef = "product:pricing/discounts"
	const statement = "        Given a cart\n        When the code is applied\n        Then the discount is <cap>\n"
	const examples = "      examples:\n        - columns: [cap]\n          rows: [[50]]\n        - rows: [[40], [30]]\n"
	type expected struct {
		severity Severity
		message  string
	}
	warn := expected{SeverityWarning, "spec file is deprecated"}
	cases := []struct {
		name, scenarioRef, lineageRef string
		configSuffix, lockState       string
		statement, examples           string
		want                          []expected
	}{
		{name: "matching deprecated references warn", scenarioRef: ref, lineageRef: lineageRef, want: []expected{warn, warn}},
		{name: "unknown source", scenarioRef: "missing:pricing/discounts#cap", want: []expected{{SeverityError, "spec source is not declared"}}},
		{name: "restricted registry", scenarioRef: ref, configSuffix: "\n    registries: [admin]", want: []expected{{SeverityError, "restricted to other registries"}}},
		{name: "empty registry allowlist", scenarioRef: ref, configSuffix: "\n    registries: []", want: []expected{{SeverityError, "restricted to other registries"}}},
		{name: "allowed registry", scenarioRef: ref, configSuffix: "\n    registries: [shop]", want: []expected{warn}},
		{name: "unpinned source", scenarioRef: ref, lockState: "empty", want: []expected{{SeverityError, "spec source is not in the lock"}}},
		{name: "missing spec file", scenarioRef: "product:pricing/other#cap", want: []expected{{SeverityError, "spec file is not in the lock"}}},
		{name: "missing case", scenarioRef: "product:pricing/discounts#unknown", want: []expected{{SeverityError, "spec case is not in the lock"}}},
		{name: "missing lock", scenarioRef: ref, lockState: "missing", want: []expected{{SeverityError, spec.LockName + " is missing"}}},
		{name: "missing lock with lineage only", lineageRef: lineageRef, lockState: "missing", want: []expected{{SeverityError, spec.LockName + " is missing"}}},
		{name: "missing lock without references", lockState: "missing"},
		{name: "invalid lock schema even without references", lockState: "invalid", want: []expected{{SeverityError, "unresolved: invalid specification lock"}}},
		{name: "missing lineage file", lineageRef: "product:pricing/unknown", want: []expected{{SeverityError, "lineage.specified_by \"product:pricing/unknown\" [exact]: spec file is not in the lock"}}},
		{name: "invalid statement", scenarioRef: ref, statement: "        A prose statement\n", want: []expected{warn, {SeverityError, "statement must contain only Gherkin steps"}}},
		{name: "different examples row order", scenarioRef: ref, examples: strings.Replace(examples, "[[40], [30]]", "[[30], [40]]", 1),
			want: []expected{warn, {SeverityError, "--- lock\n+++ record\n-   [\"40\"]\n-   [\"30\"]\n+   [\"30\"]\n+   [\"40\"]"}}},
		{name: "different examples header", scenarioRef: ref, examples: strings.Replace(examples, "columns: [cap]", "columns: [limit]", 1),
			want: []expected{warn, {SeverityError, "-   [\"cap\"]"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := fmt.Sprintf("spec_sources:\n  - id: product\n    repo: %s%s\nregistries:\n  shop:\n    records: shop/facets\n", remote, tc.configSuffix)
			root := project(t, config)
			cfg, err := LoadConfigUnresolved(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := spec.Update(t.Context(), root, cfg.SpecSources, nil, nil); err != nil {
				t.Fatal(err)
			}
			lock, err := spec.ReadLock(root)
			if err != nil {
				t.Fatal(err)
			}
			pinned, ok := lock["product"].Scenarios[address]
			if !ok || len(pinned.Sequence.Examples) != 2 || pinned.Sequence.Examples[1][1][0] != "40" {
				t.Fatalf("fixture did not pin the expected examples: %+v", pinned)
			}
			path := filepath.Join(root, spec.LockName)
			switch tc.lockState {
			case "empty":
				writeFile(t, path, "{}\n")
			case "invalid":
				writeFile(t, path, "product: {commit: bad}\n")
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			record := "schema_version: '0.1'\nid: checkout/pricing/discounts\nlifecycle: planned\n"
			if tc.lineageRef != "" {
				record += fmt.Sprintf("lineage:\n  specified_by: [%q]\n", tc.lineageRef)
			}
			if tc.scenarioRef != "" {
				body, tables := tc.statement, tc.examples
				if body == "" {
					body = statement
				}
				if tables == "" {
					tables = examples
				}
				record += fmt.Sprintf("contract:\n  scenarios:\n    - id: cap\n      specified_by: %q\n      statement: |\n%s%s", tc.scenarioRef, body, tables)
			}
			writeFile(t, filepath.Join(root, "shop/facets/features/checkout/pricing/discounts.yaml"), record)
			actual := findings(lintProject(t, root), "spec-conformance")
			if len(actual) != len(tc.want) {
				t.Fatalf("got %v, want %v", actual, tc.want)
			}
			for i, want := range tc.want {
				if actual[i].Severity != want.severity || !strings.Contains(actual[i].Message, want.message) {
					t.Fatalf("got %+v, want %+v", actual[i], want)
				}
			}
		})
	}
}

func TestSpecExampleCellsKeepThePinnedSpelling(t *testing.T) {
	blocks := []ExamplesBlock{{Columns: []string{"rate", "active", "code"}, Rows: [][]any{{0.5, true, "A1"}, {0.25, false, "B2"}}}}
	pinned := [][][]string{{{"rate", "active", "code"}, {"0.50", "true", "A1"}, {"0.30", "false", "B2"}}}
	got := specExampleTables(blocks, pinned)
	want := [][][]string{{{"rate", "active", "code"}, {"0.50", "true", "A1"}, {"0.25", "false", "B2"}}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
