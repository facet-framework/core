package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/facet-framework/core/internal/spec"
	"github.com/facet-framework/core/registry"
)

const openSpecPath = "openspec/specs/checkout/spec.md"
const openSpecBody = `# Checkout Specification

## Purpose
Define how a cart can be checked out.

## Requirements
### Requirement: Submit Cart

Checkout SHALL accept an available cart.

#### Scenario: Add items!

- **GIVEN** a cart
- **AND** inventory is available
- **WHEN** the customer submits the cart
- **THEN** the order is created
- **AND** the cart is cleared

#### Scenario: Empty cart

- **WHEN** the customer submits an empty cart
- **THEN** checkout rejects the empty cart
`
const openSpecCase = "submit-cart-add-items"
const openSpecStatement = "Given a cart\nAnd inventory is available\nWhen the customer submits the cart\nThen the order is created\nAnd the cart is cleared"

const specKitPath = "specs/003-checkout/spec.md"
const specKitBody = `# Feature Specification: Checkout

**Feature Branch**: ` + "`003-checkout`" + `
**Created**: 2026-11-23
**Status**: Approved

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Submit a cart (Priority: P1)
**Acceptance Scenarios**:
1. **Given** a cart, **When** it is submitted, **Then** checkout confirms the order.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST create an order
  for a submitted cart.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Checkout completes within two seconds.
`
const specKitText = "The system MUST create an order for a submitted cart."

func specRepoLint(t *testing.T, p specProject, code int) specLintReport {
	t.Helper()
	out := (specProject{root: p.upstream}).command(t, code, "spec", "lint", "--json")
	var report specLintReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("spec lint output: %v\n%s", err, out)
	}
	return report
}

func TestSpecOpenSpecPinLintAndCollision(t *testing.T) {
	p := newSpecProject(t)
	p.configure(t, fmt.Sprintf("spec_sources: [{id: product, repo: %s, format: openspec}]\n", p.remote))
	writeSpecFixture(t, p.upstream, openSpecPath, openSpecBody)
	writeSpecFixture(t, p.upstream, "openspec/changes/proposal/specs/checkout/spec.md", "### Requirement: Draft\n#### Scenario: Broken\n- **WHAT** not a step\n")
	commit := p.commit(t)

	if report := specRepoLint(t, p, 0); report.Checked != 1 || len(report.Findings) != 0 {
		t.Fatalf("accepted OpenSpec file was not the only checked file: %+v", report)
	}
	out := p.command(t, 0, "spec", "update")
	lock, before := p.pin(t)
	scenario := lock.Scenarios["checkout#"+openSpecCase]
	if lock.Commit != commit || scenario.Commit != commit || scenario.Strength() != spec.Exact || scenario.Binding != "" ||
		len(lock.Scenarios) != 2 || len(lock.Files) != 1 || lock.Files["checkout"] != (spec.LockedFile{Status: "approved", Language: "en"}) ||
		!strings.Contains(out, "added product:checkout#"+openSpecCase+" [exact]") {
		t.Fatalf("OpenSpec did not pin two accepted scenarios: %+v\n%s", lock, out)
	}
	want, err := spec.NormaliseStatement(openSpecStatement, "en")
	if err != nil || scenario.Sequence == nil || scenario.Steps != want.Digest() {
		t.Fatalf("OpenSpec and Gherkin differ: %+v, %v", scenario, err)
	}
	driftRecord(t, p, "checkout-spec", "checkout", true)
	specExecutionRecord(t, p, "checkout", specExecutionCase{id: openSpecCase, statement: openSpecStatement})
	if out := p.command(t, 0, "spec", "check-lock"); strings.Contains(out, "diverged") || strings.Contains(out, "unresolved") {
		t.Fatal(out)
	}
	specExecutionRecord(t, p, "checkout", specExecutionCase{id: openSpecCase, statement: strings.Replace(openSpecStatement, "order is created", "order is delayed", 1)})
	out = p.command(t, 1, "spec", "check-lock")
	for _, want := range []string{"diverged:", "[exact]", "- Outcome the order is created", "+ Outcome the order is delayed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}
	invalid := openSpecBody + "\n#### Scenario: Add items?\n- **WHAT** invalid\n- **THEN** an outcome\n"
	writeSpecFixture(t, p.upstream, openSpecPath, invalid)
	p.commit(t)
	report := specRepoLint(t, p, 1)
	wantLines := map[string]int{
		"duplicate OpenSpec case ID":    strings.Count(openSpecBody, "\n") + 2,
		"must contain WHEN and THEN":    strings.Count(openSpecBody, "\n") + 2,
		"unknown OpenSpec step keyword": strings.Count(openSpecBody, "\n") + 3,
	}
	if report.Checked != 1 || len(report.Findings) != len(wantLines) {
		t.Fatalf("malformed OpenSpec findings: %+v", report)
	}
	for want, line := range wantLines {
		if !slices.ContainsFunc(report.Findings, func(f spec.Finding) bool {
			return f.Line == line && f.Binding == spec.Exact && strings.HasSuffix(filepath.ToSlash(f.File), openSpecPath) && strings.Contains(f.Message, want)
		}) {
			t.Fatalf("missing %q on line %d: %+v", want, line, report)
		}
	}
	if out = p.command(t, 1, "spec", "update"); !strings.Contains(out, "duplicate OpenSpec case ID") || !strings.Contains(out, "[exact]") {
		t.Fatal(out)
	}
	if _, after := p.pin(t); !bytes.Equal(before, after) {
		t.Fatal("rejected OpenSpec update changed the lock")
	}
}

func TestSpecKitPinLintAndTextDrift(t *testing.T) {
	p := newSpecProject(t)
	p.configure(t, fmt.Sprintf("spec_sources: [{id: product, repo: %s, format: speckit}]\n", p.remote))
	writeSpecFixture(t, p.upstream, specKitPath, specKitBody)
	statuses := map[string]struct{ value, want string }{
		"004-accounts": {"Draft", "draft"},
		"005-delivery": {"Accepted", "approved"},
		"006-returns":  {"In review", "draft"},
	}
	for id, status := range statuses {
		writeSpecFixture(t, p.upstream, "specs/"+id+"/spec.md", strings.Replace(specKitBody, "Approved", status.value, 1))
	}
	commit := p.commit(t)
	if report := specRepoLint(t, p, 0); report.Checked != 4 || len(report.Findings) != 0 {
		t.Fatalf("Spec Kit lint report: %+v", report)
	}
	out := p.command(t, 0, "spec", "update")
	lock, raw := p.pin(t)
	const fr = "003-checkout#fr-001"
	const sc = "003-checkout#sc-001"
	for _, address := range []string{fr, sc} {
		pin := lock.Scenarios[address]
		if pin.Binding != spec.Provenance || pin.Commit != commit || pin.Text == "" || pin.Sequence != nil || pin.Context != "" ||
			!strings.Contains(out, "added product:"+address+" [provenance]") {
			t.Fatalf("Spec Kit unit %s: %+v\n%s", address, pin, out)
		}
	}
	if len(lock.Scenarios) != 8 || lock.Files["003-checkout"].Status != "approved" ||
		lock.Scenarios[fr].Text != specKitText || bytes.Contains(raw, []byte("sequence:")) {
		t.Fatalf("Spec Kit pinned unexpected units or text: %s", raw)
	}
	for id, status := range statuses {
		if got := lock.Files[id].Status; got != status.want {
			t.Fatalf("%s: status %q, want %q", id, got, status.want)
		}
	}

	specExecutionRecord(t, p, "003-checkout", specExecutionCase{id: "fr-001", statement: "This is deliberately prose."})
	lineageID := driftRecord(t, p, "checkout-spec", "003-checkout", true)
	if out := p.command(t, 0, "spec", "check-lock"); strings.Contains(out, "diverged") || strings.Contains(out, "unresolved") {
		t.Fatal(out)
	}
	specExecutionRecord(t, p, "003-checkout", specExecutionCase{id: "fr-002", statement: "This requirement is missing."})
	if out := p.command(t, 1, "spec", "check-lock"); !strings.Contains(out, "unresolved:") || !strings.Contains(out, "[provenance]") {
		t.Fatal(out)
	}
	newText := strings.Replace(specKitText, "create", "confirm", 1)
	writeSpecFixture(t, p.upstream, specKitPath, strings.Replace(specKitBody, "create", "confirm", 1))
	p.commit(t)
	report := specDriftJSON(t, p, 0, "--diff")
	if !slices.ContainsFunc(report, func(f facet.SpecDriftFinding) bool {
		return f.State == spec.SpecMoved && f.Address == "product:"+fr && f.Binding == spec.Provenance &&
			slices.Equal(f.Records, []string{"execution/spec/fr-001"}) &&
			strings.Contains(f.Diff, "- "+specKitText+"\n+ "+newText)
	}) || !slices.ContainsFunc(report, func(f facet.SpecDriftFinding) bool {
		return f.State == spec.Unclaimed && f.Address == "product:"+sc && f.Binding == spec.Provenance &&
			slices.Equal(f.Records, []string{lineageID})
	}) {
		t.Fatalf("missing text drift or unclaimed lineage: %+v", report)
	}
	if _, after := p.pin(t); !bytes.Equal(raw, after) {
		t.Fatal("drift rewrote the Spec Kit lock")
	}
	p.configure(t, fmt.Sprintf("spec_sources: [{id: product, repo: %s, format: speckit, runner: cucumber}]\n%s", p.remote, specTestRunnerConfig))
	if out := p.command(t, 1, "spec", "update"); !strings.Contains(out, "cannot configure a runner for speckit") {
		t.Fatal(out)
	}
}

func TestSpecAdapterExecution(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("FACET_STRICT_DEPS", "1")
	p := newSpecProject(t)
	p.configure(t, fmt.Sprintf("spec_sources:\n  - {id: product, repo: %s, format: openspec, runner: cucumber}\n  - {id: plan, repo: %s, format: speckit}\n%s", p.remote, p.remote, specTestRunnerConfig))
	writeSpecFixture(t, p.upstream, openSpecPath, openSpecBody)
	writeSpecFixture(t, p.upstream, specKitPath, specKitBody)
	commit := p.commit(t)
	writeSpecFixture(t, p.root, "run.sh", specTestRunner)
	specExecutionRecord(t, p, "checkout", specExecutionCase{id: openSpecCase, statement: openSpecStatement})
	writeSpecFixture(t, p.root, "docs/facets/features/execution/spec/provenance.yaml", `id: execution/spec/provenance
contract:
  scenarios:
    - id: provenance
      specified_by: plan:003-checkout#fr-001
      statement: Requirements provenance.
`)
	p.command(t, 0, "spec", "update")
	lock, err := spec.ReadLock(p.root)
	if err != nil || lock["product"].Scenarios["checkout#"+openSpecCase].Commit != commit ||
		lock["plan"].Scenarios["003-checkout#fr-001"].Commit != commit {
		t.Fatalf("missing expected pins: %+v, %v", lock, err)
	}
	out := specExecutionCLI(t, p, 0, "--json", "--only", "facet")
	var runs []facet.TestExecution
	if err := json.Unmarshal([]byte(out), &runs); err != nil || len(runs) != 1 || len(runs[0].Features) != 2 {
		t.Fatalf("execution report: %v\n%s", err, out)
	}
	for _, feature := range runs[0].Features {
		state, binding, ref := facet.TestPassed, spec.Exact, "product:checkout#"+openSpecCase
		if feature.ID == "execution/spec/provenance" {
			state, binding, ref = "provenance", spec.Provenance, "plan:003-checkout#fr-001"
		}
		if feature.State != state || len(feature.Files) != 0 || len(feature.Scenarios) != 1 ||
			feature.Scenarios[0].State != state || feature.Scenarios[0].Binding != binding || feature.Scenarios[0].SpecifiedBy != ref {
			t.Fatalf("unexpected evidence for %+v", feature)
		}
	}
	features := specReceivedFiles(t, p, 1)
	for _, part := range []string{"Feature: checkout", "@case:" + openSpecCase, "@case:submit-cart-empty-cart", "And inventory is available", "Then the order is created"} {
		if !strings.Contains(features[0], part) {
			t.Fatalf("runner input missing %q:\n%s", part, features[0])
		}
	}
	if strings.Contains(features[0], "fr-001") || strings.Count(features[0], "\n  Scenario:") != 2 {
		t.Fatalf("unexpected materialised scenarios:\n%s", features[0])
	}
	p.configure(t, fmt.Sprintf("spec_sources: [{id: plan, repo: %s, format: speckit}]\n", p.remote))
	exactRecord := filepath.Join(p.root, "docs/facets/features/execution/spec", openSpecCase+".yaml")
	if err := os.Remove(exactRecord); err != nil {
		t.Fatal(err)
	}
	out = specExecutionCLI(t, p, 0, "--only", "facet", "--fail-unmeasured")
	if !strings.Contains(out, "provenance provenance (plan:003-checkout#fr-001) [provenance]") {
		t.Fatalf("provenance-only report:\n%s", out)
	}
}

func TestSpecExactLockWithoutBindingStaysByteIdentical(t *testing.T) {
	p := newSpecProject(t)
	p.configure(t, fmt.Sprintf("spec_sources: [{id: product, repo: %s, format: gherkin}]\n", p.remote))
	writeSpecFixture(t, p.upstream, "pricing.feature", `Feature: Pricing
  @case:cap
  Scenario: Discount cap
    Given a cart
    When the code is applied
    Then the discount is 50%
`)
	commit := p.commit(t)
	template, err := os.ReadFile("testdata/spec-lock-exact-without-binding.yaml")
	if err != nil {
		t.Fatal(err)
	}
	old := strings.NewReplacer("COMMIT", commit, "REPO", p.remote).Replace(string(template))
	writeSpecFixture(t, p.root, spec.LockName, old)
	lock, _ := p.pin(t)
	if pin := lock.Scenarios["pricing#cap"]; pin.Binding != "" || pin.Strength() != spec.Exact || pin.Commit != commit {
		t.Fatalf("fixture is not an exact lock without a binding field: %+v", pin)
	}
	if out := p.command(t, 0, "spec", "update"); !strings.Contains(out, "no scenario pins changed") {
		t.Fatal(out)
	}
	if _, after := p.pin(t); !bytes.Equal([]byte(old), after) {
		t.Fatalf("exact lock changed:\n%s", after)
	}
	writeSpecFixture(t, p.upstream, "pricing.feature", "Feature: Pricing\n  @case:cap\n  Scenario: Discount cap\n    Given a cart\n    When the code is applied\n    Then the discount is 40%\n")
	moved := p.commit(t)
	p.command(t, 0, "spec", "update")
	lock, raw := p.pin(t)
	if pin := lock.Scenarios["pricing#cap"]; pin.Commit != moved || strings.Contains(string(raw), "binding:") {
		t.Fatalf("an exact pin must stay without a binding field after its commit moves: %+v\n%s", pin, raw)
	}
}
