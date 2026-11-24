package cli

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gherkin "github.com/cucumber/gherkin/go/v41"
	"github.com/facet-framework/core/internal/spec"
	"github.com/facet-framework/core/schema"
)

const specCommandHelper = "FACET_TEST_SPEC_COMMAND"

func TestSpecRootCommandHelper(t *testing.T) {
	if os.Getenv(specCommandHelper) != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			root := newRootCmd()
			root.SetArgs(os.Args[i+1:])
			os.Exit(Run(root))
		}
	}
	t.Fatal("missing command arguments")
}

type specProject struct {
	root, upstream, remote string
}

func newSpecProject(t *testing.T) specProject {
	t.Helper()
	root, upstream := t.TempDir(), t.TempDir()
	specGit(t, upstream, "init", "--initial-branch=main")
	remote := (&url.URL{Scheme: "file", Path: filepath.ToSlash(upstream)}).String()
	p := specProject{root, upstream, remote}
	p.configure(t, fmt.Sprintf("spec_sources:\n  - id: product\n    repo: %s\n    path: specs/\n", remote))
	return p
}

func (p specProject) configure(t *testing.T, config string) {
	t.Helper()
	writeSpecFixture(t, p.root, ".facetrc.yaml", config)
}

func (p specProject) markdown(t *testing.T, status, body string) string {
	t.Helper()
	writeSpecFixture(t, p.upstream, "specs/pricing.md", "---\nschema: facet-spec/0.1\nid: pricing/discounts\ntitle: Pricing\nstatus: "+status+"\nowner: checkout\n---\n"+
		"```gherkin\n"+body+"\n```\n")
	return p.commit(t)
}

func (p specProject) commit(t *testing.T) string {
	t.Helper()
	specGit(t, p.upstream, "add", ".")
	specGit(t, p.upstream, "-c", "user.name=FACET Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Specification")
	return specGit(t, p.upstream, "rev-parse", "HEAD")
}

func (p specProject) command(t *testing.T, code int, args ...string) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, append([]string{"-test.run=^TestSpecRootCommandHelper$", "--", "--root", p.root}, args...)...)
	cmd.Env = append(os.Environ(), specCommandHelper+"=1")
	out, err := cmd.CombinedOutput()
	if actual := cmd.ProcessState; actual == nil || actual.ExitCode() != code {
		t.Fatalf("facet %v: want exit %d, got %v (%v)\n%s", args, code, actual, err, out)
	}
	return string(out)
}

func (p specProject) pin(t *testing.T) (spec.LockedSource, []byte) {
	t.Helper()
	lock, err := spec.ReadLock(p.root)
	if err != nil {
		t.Fatal(err)
	}
	locked, ok := lock["product"]
	if !ok {
		t.Fatalf("product absent from lock: %v", lock)
	}
	raw, err := os.ReadFile(filepath.Join(p.root, spec.LockName))
	if err != nil {
		t.Fatal(err)
	}
	return locked, raw
}

// lintSuite returns the shell lint suite of the repository holding the test,
// found by walking up from the working directory to the directory that has both
// go.mod and tools/facet-lint/run-all.sh. A missing suite fails the test.
func lintSuite(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		suite := filepath.Join(dir, "tools", "facet-lint", "run-all.sh")
		_, modErr := os.Stat(filepath.Join(dir, "go.mod"))
		_, suiteErr := os.Stat(suite)
		if modErr == nil && suiteErr == nil {
			return suite
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no directory above the working directory holds go.mod and tools/facet-lint/run-all.sh")
		}
		dir = parent
	}
}

func writeSpecFixture(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func specGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

const pricingScenarios = `@case:cap
Scenario: Cap
  Given a cart
  When the code is applied
  Then the discount is 50%
@case:unknown
Scenario: Unknown
  Given a cart
  When an unknown code is applied
  Then the discount is 0%`

const capCase = "pricing/discounts#cap"

func TestSpecUpdateDeterministicAndSelective(t *testing.T) {
	p := newSpecProject(t)
	first := p.markdown(t, "approved", pricingScenarios)
	out := p.command(t, 0, "spec", "update", "product")
	initial, initialBytes := p.pin(t)
	if initial.Commit != first || initial.Scenarios[capCase].Commit != first ||
		initial.Scenarios["pricing/discounts#unknown"].Commit != first ||
		initial.Files["pricing/discounts"] != (spec.LockedFile{Status: "approved", Language: "en"}) ||
		!strings.Contains(out, "added product:"+capCase) {
		t.Fatalf("initial pin: %+v\n%s", initial, out)
	}
	assertPinBytes := func(expected []byte) {
		t.Helper()
		_, actual := p.pin(t)
		if !bytes.Equal(expected, actual) {
			t.Fatalf("lock changed\nwant:\n%s\ngot:\n%s", expected, actual)
		}
	}
	p.command(t, 0, "spec", "update")
	assertPinBytes(initialBytes)
	changed := strings.NewReplacer("50%", "40%", "0%", "10%").Replace(pricingScenarios)
	second := p.markdown(t, "deprecated", changed)
	out = p.command(t, 0, "spec", "update", "--only", capCase)
	part, partBytes := p.pin(t)
	if part.Commit != first || part.Scenarios[capCase].Commit != second ||
		part.Scenarios[capCase].Steps == initial.Scenarios[capCase].Steps ||
		part.Files["pricing/discounts"] != initial.Files["pricing/discounts"] ||
		out != "changed product:"+capCase+" [exact]\n" {
		t.Fatalf("selective pin: %+v\n%s", part, out)
	}
	unselectedBytes := func(raw []byte) string {
		t.Helper()
		before, selectedAndAfter, present := strings.Cut(string(raw), "    "+capCase+":\n")
		_, after, next := strings.Cut(selectedAndAfter, "    pricing/discounts#unknown:\n")
		if !present || !next {
			t.Fatalf("cannot locate both scenario entries in %s", raw)
		}
		return before + after
	}
	if unselectedBytes(initialBytes) != unselectedBytes(partBytes) {
		t.Fatalf("bytes outside the selected scenario changed\n%s\n%s", initialBytes, partBytes)
	}
	if out := p.command(t, 1, "spec", "update", "--only", "pricing/discounts#missing"); !strings.Contains(out, "absent from the selected upstream") {
		t.Fatal(out)
	}
	assertPinBytes(partBytes)
	p.markdown(t, "approved", strings.Split(changed, "@case:unknown")[0])
	out = p.command(t, 0, "spec", "update")
	if current, _ := p.pin(t); len(current.Scenarios) != 1 || !strings.Contains(out, "removed product:pricing/discounts#unknown") {
		t.Fatalf("full pin: %+v\n%s", current, out)
	}
}

func TestSpecUpdateAbortsAndKeepsLock(t *testing.T) {
	p := newSpecProject(t)
	p.markdown(t, "approved", pricingScenarios)
	p.command(t, 0, "spec", "update")
	_, before := p.pin(t)
	p.markdown(t, "approved", strings.Replace(pricingScenarios, "@case:cap\n", "", 1))
	got := p.command(t, 1, "spec", "update")
	if !strings.Contains(got, `spec source "product"`) || !strings.Contains(got, "pricing.md:") || !strings.Contains(got, "needs one @case") {
		t.Fatalf("missing spec lint findings: %s", got)
	}
	_, current := p.pin(t)
	if !bytes.Equal(before, current) {
		t.Fatal("invalid spec changed the lock")
	}

	p.markdown(t, "approved", strings.ReplaceAll(pricingScenarios, "50%", "40%"))
	p.configure(t, fmt.Sprintf("spec_sources:\n  - id: product\n    repo: %s\n    path: specs\n"+
		"  - id: missing\n    repo: %s/does-not-exist\n", p.remote, p.remote))
	got = p.command(t, 1, "spec", "update")
	if !strings.Contains(got, `spec source "missing": cannot fetch branch "main"`) || strings.Contains(got, p.remote) {
		t.Fatalf("unexpected fetch error: %s", got)
	}
	_, current = p.pin(t)
	if !bytes.Equal(before, current) {
		t.Fatal("failed second source changed the lock")
	}
}

func TestSpecUpdateLocalOverrideUsesFollowBranch(t *testing.T) {
	p := newSpecProject(t)
	german := gherkin.DialectsBuiltin().GetDialect("de")
	keyword := func(name string) string { return strings.TrimSpace(german.Keywords[name][0]) }
	writeSpecFixture(t, p.upstream, "discount.feature", "# language: de\n"+keyword("feature")+": Price\n@case:cap\n"+keyword("scenario")+": Discount\n"+keyword("given")+" a cart\n")
	wanted := p.commit(t)
	specGit(t, p.upstream, "branch", "review/spec")
	specGit(t, p.upstream, "switch", "--create", "work")
	writeSpecFixture(t, p.upstream, "discount.feature", "This working tree is not a specification")
	p.commit(t)
	writeSpecFixture(t, p.upstream, "discount.feature", "Neither is this uncommitted edit")
	p.configure(t, "spec_sources:\n  - id: product\n    repo: file:///does-not-exist\n    follow: review/spec\n    format: gherkin\n")
	writeSpecFixture(t, p.root, ".facet/sources.local.yaml", fmt.Sprintf("spec_sources:\n  product: %q\n", p.upstream))
	p.command(t, 0, "spec", "update")
	pinned, _ := p.pin(t)
	if pinned.Commit != wanted || pinned.Scenarios["discount#cap"].Commit != wanted ||
		pinned.Files["discount"] != (spec.LockedFile{Status: "approved", Language: "de"}) {
		t.Fatalf("override did not pin the German follow branch: %+v", pinned)
	}
}

func TestSpecUpdateAndLintRootCommands(t *testing.T) {
	p := newSpecProject(t)
	p.markdown(t, "draft", pricingScenarios)
	p.command(t, 0, "spec", "update")
	pinned, _ := p.pin(t)
	if _, exists := pinned.Scenarios[capCase]; !exists {
		t.Fatal("test case not pinned")
	}
	writeSpecFixture(t, p.root, ".facet/sources.local.yaml", "spec_sources: [malformed override]\n")
	schemaBytes, err := schema.Files.ReadFile("facet.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	writeSpecFixture(t, p.root, "schema/facet.schema.json", string(schemaBytes))
	record := `schema_version: "0.1"
id: checkout/pricing/discounts
lifecycle: planned
intent:
  summary: Apply a discount to a cart.
lineage:
  owner: {team: checkout}
  created_at: "2026-11-23"
  specified_by: ["product:pricing/discounts"]
contract:
  scenarios:
    - id: cap
      specified_by: product:pricing/discounts#cap
      statement: |
        Given a cart
        When the code is applied
        Then the discount is 50%
`
	const recordPath = "docs/facets/features/checkout/pricing/discounts.yaml"
	writeSpecFixture(t, p.root, recordPath, record)
	specGit(t, p.upstream, "branch", "-m", "not-main")
	if out := p.command(t, 0, "lint"); strings.Contains(out, "product:pricing/discounts") {
		t.Fatalf("matching scenario produced a finding: %s", out)
	}
	writeSpecFixture(t, p.root, recordPath, strings.Replace(record, "50%", "40%", 1))
	out := p.command(t, 1, "lint")
	for _, want := range []string{"error:", "diverged", "--- lock", "+++ record", "- Outcome the discount is 50%", "+ Outcome the discount is 40%", "The spec is the source.", "report the deviation"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}
}

func TestSpecConformanceShellSuite(t *testing.T) {
	for _, tool := range []string{"ajv", "yq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is required by the shell lint suite", tool)
		}
	}
	p := newSpecProject(t)
	p.markdown(t, "approved", pricingScenarios)
	p.command(t, 0, "spec", "update")
	pinned, _ := p.pin(t)
	if _, exists := pinned.Scenarios[capCase]; !exists {
		t.Fatal("test case not pinned")
	}
	schemaBytes, err := schema.Files.ReadFile("facet.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	writeSpecFixture(t, p.root, "schema/facet.schema.json", string(schemaBytes))
	writeSpecFixture(t, p.root, "docs/facets/features/checkout/pricing/discounts.yaml", `schema_version: "0.1"
id: checkout/pricing/discounts
lifecycle: planned
intent:
  summary: Apply a discount to a cart.
lineage:
  owner: {team: checkout}
  created_at: "2026-11-23"
contract:
  scenarios:
    - id: cap
      specified_by: product:pricing/discounts#cap
      statement: |
        Given a cart
        When the code is applied
        Then the discount is 40%
`)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	wrapper := filepath.Join(bin, "facet")
	writeSpecFixture(t, bin, "facet", "#!/bin/sh\nexec \"$FACET_SPEC_TEST_EXECUTABLE\" -test.run=^TestSpecRootCommandHelper$ -- \"$@\"\n")
	if err := os.Chmod(wrapper, 0755); err != nil {
		t.Fatal(err)
	}
	run := func(expected string) {
		t.Helper()
		cmd := exec.Command("sh", lintSuite(t), p.root)
		cmd.Env = append(os.Environ(), "FACET_STRICT_DEPS=1", "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
			specCommandHelper+"=1", "FACET_SPEC_TEST_EXECUTABLE="+executable)
		out, err := cmd.CombinedOutput()
		if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 1 ||
			!strings.Contains(string(out), expected) {
			t.Fatalf("shell suite: %v\n%s", err, out)
		}
	}
	run("diverged: scenario")
	if err := os.Remove(filepath.Join(p.root, spec.LockName)); err != nil {
		t.Fatal(err)
	}
	run("docs/facets/spec-lock.yaml is missing")
}
