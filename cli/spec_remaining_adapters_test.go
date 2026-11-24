package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/facet-framework/core/internal/spec"
	"github.com/facet-framework/core/registry"
)

func TestSpecProvenanceAdapters(t *testing.T) {
	tests := []struct {
		format, path, body, id, text, status string
	}{
		{"kiro", ".kiro/specs/checkout/requirements.md",
			"# Requirements Document\n\n## Requirements\n\n### Requirement 1: Checkout\n\n**User Story:** As a customer, I want to check out.\n\n#### Acceptance Criteria\n\n1. WHEN a cart is submitted, THE SYSTEM SHALL\n   create an order.\n",
			"checkout#requirement-1-1", "WHEN a cart is submitted, THE SYSTEM SHALL create an order.", "approved"},
		{"bmad", "_bmad-output/implementation-artifacts/1-2-checkout.md",
			"# Story 1.2: Checkout\n\nStatus: ready-for-dev\n\n## Story\n\nAs a customer, I want to check out.\n\n## Acceptance Criteria\n\n1. **AC4:** Given a cart, when it is submitted, then create an order.\n\n> Checkout covers submitted carts.\n\n## Tasks / Subtasks\n\n- [ ] Implement checkout (AC: 4)\n",
			"1-2-checkout#ac-4", "Given a cart, when it is submitted, then create an order.", "draft"},
		{"bmad", "_bmad-output/implementation-artifacts/1-3-checkout.md",
			"# Story 1.3: Checkout\n\nStatus: Approved\n\n## Acceptance Criteria\n\n- Given a cart, when it is submitted, then create an order.\n",
			"1-3-checkout#story", "- Given a cart, when it is submitted, then create an order.", "approved"},
		{"gauge", "specs/checkout.spec",
			"# Checkout specification\nTags: checkout\n\n* Sign in as \"customer\"\n\n## Successful checkout\nTags: successful,\n  id:submit\n\n* Submit the cart\n* create an order.\n\n___\n* Clear the cart\n",
			"checkout#submit", "* Submit the cart\n* create an order.", "approved"},
		{"gauge", "specs/checkout.md",
			"Checkout specification\n======================\n\nSuccessful checkout\n-------------------\n\n* Submit the cart\n* create an order.\n",
			"checkout#successful-checkout", "* Submit the cart\n* create an order.", "approved"},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			p := newSpecProject(t)
			config := fmt.Sprintf("spec_sources: [{id: product, repo: %s, format: %s", p.remote, tc.format)
			p.configure(t, config+"}]\n")
			writeSpecFixture(t, p.upstream, tc.path, tc.body)
			if tc.format == "gauge" {
				writeSpecFixture(t, p.upstream, "manifest.json", `{"Language": "js", "Plugins": ["html-report"]}`+"\n")
			}
			commit := p.commit(t)
			if report := specRepoLint(t, p, 0); report.Checked != 1 || len(report.Findings) != 0 {
				t.Fatalf("unexpected spec lint: %+v", report)
			}
			if out := p.command(t, 0, "spec", "update"); !strings.Contains(out, "added product:"+tc.id+" [provenance]") {
				t.Fatal(out)
			}
			lock, raw := p.pin(t)
			file, id, _ := strings.Cut(tc.id, "#")
			if pin := lock.Scenarios[tc.id]; len(lock.Scenarios) != 1 || pin.Binding != spec.Provenance ||
				pin.Commit != commit || pin.Text != tc.text || pin.Sequence != nil || pin.Context != "" || lock.Files[file].Status != tc.status {
				t.Fatalf("unexpected lock: %s", raw)
			}
			specExecutionRecord(t, p, file, specExecutionCase{id: id, statement: "Then an intentionally different result occurs"})
			p.command(t, 0, "spec", "check-lock")
			specExecutionRecord(t, p, file, specExecutionCase{id: "missing-unit", statement: "Then a missing unit is reported"})
			if out := p.command(t, 1, "spec", "check-lock"); !strings.Contains(out, "unresolved:") || !strings.Contains(out, "[provenance]") {
				t.Fatal(out)
			}
			if err := os.Remove(filepath.Join(p.root, "docs/facets/features/execution/spec/missing-unit.yaml")); err != nil {
				t.Fatal(err)
			}
			out := specExecutionCLI(t, p, 0, "--only", "facet", "--fail-unmeasured")
			if !strings.Contains(out, "provenance record-"+id+" (product:"+tc.id+") [provenance]") {
				t.Fatal(out)
			}
			p.configure(t, config+"}]\n"+specTestRunnerConfig)
			writeSpecFixture(t, p.root, "run.sh", specTestRunner)
			specExecutionCLI(t, p, 0, "--only", "facet")
			specReceivedFiles(t, p, 0)
			p.configure(t, config+"}]\n")
			writeSpecFixture(t, p.upstream, tc.path, strings.Replace(tc.body, "create an order", "confirm an order", 1))
			p.commit(t)
			findings := specDriftJSON(t, p, 0, "--diff")
			diffText := tc.text
			if tc.format == "gauge" {
				_, diffText, _ = strings.Cut(tc.text, "\n")
			}
			expectedDiff := "- " + diffText + "\n+ " + strings.Replace(diffText, "create an order", "confirm an order", 1)
			if !slices.ContainsFunc(findings, func(f facet.SpecDriftFinding) bool {
				return f.State == spec.SpecMoved && f.Address == "product:"+tc.id && f.Binding == spec.Provenance && slices.Equal(f.Records, []string{"execution/spec/" + id}) && strings.Contains(f.Diff, expectedDiff)
			}) {
				t.Fatalf("missing provenance text diff: %+v", findings)
			}
			p.configure(t, config+", runner: cucumber}]\n"+specTestRunnerConfig)
			if out := p.command(t, 1, "spec", "update"); !strings.Contains(out, "cannot configure a runner for "+tc.format+" [provenance]") {
				t.Fatal(out)
			}
			if _, after := p.pin(t); !bytes.Equal(raw, after) {
				t.Fatal("drift or rejected update rewrote the lock")
			}
		})
	}
}

func TestSpecGherkinMarkdownPinDriftAndExecution(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	p := newSpecProject(t)
	p.configure(t, fmt.Sprintf("spec_sources: [{id: product, repo: %s, format: gherkin-markdown, runner: cucumber}]\n%s", p.remote, specTestRunnerConfig))
	const path = "pricing.feature.md"
	const body = "# Feature: Pricing\n\n`@case:cap`\n## Scenario: Discount cap\n\n- Given a cart\n* Then the discount is 50%\n"
	writeSpecFixture(t, p.upstream, path, body)
	commit := p.commit(t)
	if report := specRepoLint(t, p, 0); report.Checked != 1 || len(report.Findings) != 0 {
		t.Fatalf("unexpected spec lint: %+v", report)
	}
	p.command(t, 0, "spec", "update")
	lock, raw := p.pin(t)
	pin := lock.Scenarios["pricing#cap"]
	if len(lock.Scenarios) != 1 || pin.Commit != commit || pin.Binding != "" || pin.Sequence == nil {
		t.Fatalf("unexpected exact pin: %s", raw)
	}
	c := specExecutionCase{id: "cap", statement: "Given a cart\nThen the discount is 50%", state: facet.TestPassed}
	specExecutionRecord(t, p, "pricing", c)
	p.command(t, 0, "spec", "check-lock")
	writeSpecFixture(t, p.root, "run.sh", specTestRunner)
	writeSpecFixture(t, p.upstream, path, strings.Replace(body, "50%", "40%", 1))
	p.commit(t)
	findings := specDriftJSON(t, p, 0, "--diff")
	if !slices.ContainsFunc(findings, func(f facet.SpecDriftFinding) bool {
		return f.State == spec.SpecMoved && f.Binding == spec.Exact && strings.Contains(f.Diff, "50%") && strings.Contains(f.Diff, "40%")
	}) {
		t.Fatalf("missing exact step drift: %+v", findings)
	}
	specExecutionJSON(t, specExecutionCLI(t, p, 0, "--json", "--only", "facet"), "pricing", c)
	materialised := specReceivedFiles(t, p, 1)[0]
	if materialised != "Feature: Pricing\n\n@case:cap\nScenario: Discount cap\n\nGiven a cart\nThen the discount is 50%\n" {
		t.Fatalf("runner did not receive pinned classic Gherkin:\n%s", materialised)
	}
	c.statement = strings.Replace(c.statement, "50%", "40%", 1)
	specExecutionRecord(t, p, "pricing", c)
	if out := p.command(t, 1, "spec", "check-lock"); !strings.Contains(out, "diverged") ||
		!strings.Contains(out, "50%") || !strings.Contains(out, "40%") {
		t.Fatal(out)
	}
	writeSpecFixture(t, p.upstream, path, strings.Replace(body, "`@case:cap`", "", 1))
	p.commit(t)
	if report := specRepoLint(t, p, 1); len(report.Findings) != 1 || report.Findings[0].Binding != spec.Exact ||
		report.Findings[0].Line != 4 || !strings.Contains(report.Findings[0].Message, "@case:") {
		t.Fatalf("missing case-tag finding: %+v", report)
	}
	p.command(t, 1, "spec", "update")
	if _, after := p.pin(t); !bytes.Equal(raw, after) {
		t.Fatal("failed update rewrote the lock")
	}
}

func TestSpecExistingFormatsKeepLockBytes(t *testing.T) {
	p := newSpecProject(t)
	p.markdown(t, "approved", "@case:cap\nScenario: Cap\n  Given a cart\n  Then the discount is capped")
	writeSpecFixture(t, p.upstream, "features/checkout.feature",
		"Feature: Checkout\n@case:submit\nScenario: Submit\n  When a cart is submitted\n  Then checkout confirms the order\n")
	writeSpecFixture(t, p.upstream, openSpecPath, openSpecBody)
	writeSpecFixture(t, p.upstream, specKitPath, specKitBody)
	commit := p.commit(t)
	p.configure(t, fmt.Sprintf(`spec_sources:
  - {id: facet-source, repo: %[1]s, format: facet-spec, path: specs}
  - {id: gherkin-source, repo: %[1]s, format: gherkin, path: features}
  - {id: openspec-source, repo: %[1]s, format: openspec}
  - {id: speckit-source, repo: %[1]s, format: speckit}
`, p.remote))
	p.command(t, 0, "spec", "update")
	raw := specExecutionRead(t, p.root, spec.LockName)
	got := strings.NewReplacer(commit, "COMMIT", p.remote, "REPO").Replace(raw)
	want := specExecutionRead(t, ".", "testdata/spec-lock-exact-formats.yaml")
	if got != want {
		t.Fatalf("exact-format lock changed:\n%s", got)
	}
}

func TestSpecProvenanceAdaptersRejectDuplicateUnits(t *testing.T) {
	for _, tc := range []struct {
		format, path, head, unit, id string
	}{
		{"kiro", ".kiro/specs/checkout/requirements.md",
			"# Requirements\n## Requirement 1: Checkout\n### Acceptance Criteria\n",
			"1. WHEN checkout starts, THE SYSTEM SHALL create an order.\n", "requirement-1-1"},
		{"bmad", "_bmad-output/implementation-artifacts/1-2-checkout.md",
			"# Story 1.2: Checkout\nStatus: ready-for-dev\n## Acceptance Criteria\n",
			"1. **AC4:** Create an order.\n", "ac-4"},
		{"gauge", "specs/checkout.spec", "# Checkout\n",
			"## Submit cart\nTags: id:submit\n* Submit the cart\n", "submit"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			p := newSpecProject(t)
			p.configure(t, fmt.Sprintf("spec_sources: [{id: product, repo: %s, format: %s}]\n", p.remote, tc.format))
			writeSpecFixture(t, p.upstream, tc.path, tc.head+tc.unit)
			p.commit(t)
			p.command(t, 0, "spec", "update")
			first, before := p.pin(t)
			if len(first.Scenarios) != 1 {
				t.Fatalf("fixture did not pin one unit: %s", before)
			}
			writeSpecFixture(t, p.upstream, tc.path, tc.head+tc.unit+tc.unit)
			p.commit(t)
			report := specRepoLint(t, p, 1)
			message := fmt.Sprintf("duplicate unit ID %q", tc.id)
			if len(report.Findings) != 1 || report.Findings[0].Binding != spec.Provenance ||
				!strings.Contains(report.Findings[0].Message, message) ||
				report.Findings[0].Line != strings.Count(tc.head+tc.unit, "\n")+1 {
				t.Fatalf("missing duplicate ID diagnostic: %+v", report)
			}
			if out := p.command(t, 1, "spec", "update"); !strings.Contains(out, message) || !strings.Contains(out, "[provenance]") {
				t.Fatal(out)
			}
			if _, after := p.pin(t); !bytes.Equal(before, after) {
				t.Fatal("failed update rewrote the lock")
			}
		})
	}
}
