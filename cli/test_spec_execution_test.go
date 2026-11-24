package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/facet-framework/core/registry"
)

const specTestRunnerConfig = `runners:
  cucumber:
    command: 'sh run.sh "$@"'
    report: cucumber-json
    report_file: cucumber.json
`

// The runner records the received paths and their contents before reporting.
// Scenario lines are deliberately unrelated to source lines.
const specTestRunner = `set -eu
printf '%s\n' "$#" > received-count
: > received-paths
printf '[' > cucumber.json
separator=
for feature do
  test -f "$feature"
  printf '%s\n' "$feature" >> received-paths
  cp "$feature" "received-${feature##*/}"
  printf '%s{"uri":"%s","elements":[' "$separator" "$feature" >> cucumber.json
  separator=,
  awk '
    function emit() {
      if (tag == "" || tag ~ /:missing$/) return
      printf "%s{\"type\":\"scenario\",\"line\":9000,\"tags\":[{\"name\":\"%s\"}],\"steps\":[%s]}", sep, tag, steps
      sep=","
    }
    function step(line, status) {
      return sprintf("{\"line\":%d,\"result\":{\"status\":\"%s\"}}", line, status)
    }
    /^[[:space:]]*(@case:|@id:)/ {
      emit()
      tag=$1
      steps=background
      next
    }
    /^[[:space:]]*(Given|When|Then|And) / {
      if (tag == "") {
        background = background (background == "" ? "" : ",") step(NR, "passed")
      } else {
        status="passed"
        if (tag ~ /:failure$/) status="failed"
        if (tag ~ /:undefined$/) status="undefined"
        if (tag ~ /:skipped$/) status="skipped"
        if (tag ~ /:pending$/) status="pending"
        steps = steps (steps == "" ? "" : ",") step(NR, status)
      }
    }
    END { emit() }
  ' "$feature" >> cucumber.json
  printf ']}' >> cucumber.json
done
printf ']\n' >> cucumber.json
`

type specExecutionCase struct {
	id, statement, state string
}

func specExecutionRecord(t *testing.T, p specProject, file string, cases ...specExecutionCase) {
	t.Helper()
	for _, c := range cases {
		data := fmt.Sprintf("id: execution/spec/%s\ncontract:\n  scenarios:\n    - id: record-%s\n      specified_by: product:%s#%s\n      statement: |\n", c.id, c.id, file, c.id)
		for _, line := range strings.Split(c.statement, "\n") {
			data += "        " + line + "\n"
		}
		writeSpecFixture(t, p.root, "docs/facets/features/execution/spec/"+c.id+".yaml", data)
	}
}

func specExecutionCLI(t *testing.T, p specProject, code int, args ...string) string {
	t.Helper()
	cmd := newRootCmd()
	var output, diagnostics bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&diagnostics)
	cmd.SetArgs(append([]string{"--root", p.root, "test"}, args...))
	if got := Run(cmd); got != code {
		t.Fatalf("facet test %v: exit %d, want %d\n%s%s", args, got, code, output.String(), diagnostics.String())
	}
	return output.String()
}

func specExecutionJSON(t *testing.T, output, file string, cases ...specExecutionCase) map[string]facet.FeatureTestResult {
	t.Helper()
	var runs []facet.TestExecution
	if err := json.Unmarshal([]byte(output), &runs); err != nil || len(runs) != 1 || len(runs[0].Features) != len(cases) {
		t.Fatalf("unexpected report: %s (%v)", output, err)
	}
	features := map[string]facet.FeatureTestResult{}
	for _, feature := range runs[0].Features {
		features[feature.ID] = feature
	}
	for _, c := range cases {
		result := features["execution/spec/"+c.id]
		featureState := facet.TestUnmeasured
		if c.state == facet.TestPassed || c.state == facet.TestFailed {
			featureState = c.state
		}
		if result.State != featureState || len(result.Files) != 0 || len(result.Scenarios) != 1 {
			t.Fatalf("%s: unexpected evidence %+v", c.id, result)
		}
		got := result.Scenarios[0]
		if got.ID != "record-"+c.id || got.SpecifiedBy != "product:"+file+"#"+c.id || got.State != c.state {
			t.Fatalf("%s: unexpected scenario %+v", c.id, got)
		}
	}
	return features
}

func specExecutionRead(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func specReceivedFiles(t *testing.T, p specProject, want int) []string {
	t.Helper()
	if got := strings.TrimSpace(specExecutionRead(t, p.root, "received-count")); got != fmt.Sprint(want) {
		t.Fatalf("runner received %s files, want %d", got, want)
	}
	data := strings.TrimSpace(specExecutionRead(t, p.root, "received-paths"))
	if want == 0 {
		if data != "" {
			t.Fatalf("unexpected received paths: %s", data)
		}
		return nil
	}
	var contents []string
	for _, path := range strings.Split(data, "\n") {
		if !filepath.IsAbs(path) || filepath.Ext(path) != ".feature" {
			t.Fatalf("runner received an unexpected path: %s", path)
		}
		contents = append(contents, specExecutionRead(t, p.root, "received-"+filepath.Base(path)))
		if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
			t.Fatalf("materialisation directory %q survives: %v", filepath.Dir(path), err)
		}
	}
	return contents
}

func TestSpecExecutionMarkdownBlocksAndCaseResults(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("FACET_STRICT_DEPS", "")
	p := newSpecProject(t)
	cases := []specExecutionCase{
		{"passing", "Given the product is available", facet.TestPassed},
		{"failure", "Then the failed outcome is visible", facet.TestFailed},
		{"undefined", "When an undefined step runs", facet.TestUnproven},
		{"skipped", "When a skipped step runs", facet.TestUnproven},
		{"pending", "When a pending step runs", facet.TestUnproven},
		{"missing", "Then the omitted case is absent", facet.TestNotRun},
		{"divergent", "Then the record promises something different", facet.TestDiverged},
		{"divergent-block", "Then this changed record has no matching case", facet.TestDiverged},
		{"z-isolated", "Given an isolated case", facet.TestPassed},
	}
	var block strings.Builder
	block.WriteString("Background: Shared setup\n  Given the block is set up\nRule: Same block\n  Background: Rule setup\n    Given the rule is set up\n")
	for _, c := range cases[:len(cases)-2] {
		statement := c.statement
		if c.id == "divergent" {
			statement = "Then the pinned statement runs"
		}
		fmt.Fprintf(&block, "  @case:%s\n  Scenario: %s\n    %s\n", c.id, c.id, statement)
	}
	commit := p.markdown(t, "approved", block.String()+"```\n\n```gherkin\n"+
		"@case:divergent-block\nScenario: Excluded block\n  Then the second pinned statement runs\n```\n\n```gherkin\n"+
		"@case:z-isolated\nScenario: Isolated block\n  Given an isolated case")
	p.configure(t, fmt.Sprintf("spec_sources:\n  - id: product\n    repo: %s\n    path: specs\n    runner: cucumber\n%s", p.remote, specTestRunnerConfig))
	writeSpecFixture(t, p.root, "run.sh", specTestRunner)
	specExecutionRecord(t, p, "pricing/discounts", cases...)
	p.command(t, 0, "spec", "update")
	pinned, _ := p.pin(t)
	for _, c := range cases {
		if got := pinned.Scenarios["pricing/discounts#"+c.id]; got.Commit != commit {
			t.Fatalf("%s: expected pin at %s, got %+v", c.id, commit, got)
		}
	}

	output := specExecutionCLI(t, p, 1, "--json", "--only", "facet")
	features := specReceivedFiles(t, p, 2)
	for _, part := range []string{"Feature: Pricing", "Background: Shared setup", "Rule: Same block", "Background: Rule setup", "Then the pinned statement runs"} {
		if !strings.Contains(features[0], part) {
			t.Fatalf("materialised feature misses %q:\n%s", part, features[0])
		}
	}
	for _, c := range cases[:len(cases)-3] {
		if !strings.Contains(features[0], "@case:"+c.id+"\n") || !strings.Contains(features[0], c.statement) {
			t.Fatalf("materialised feature does not contain %s", c.id)
		}
	}
	for _, feature := range features {
		if strings.Contains(feature, "Excluded block") {
			t.Fatalf("unexpected diverged block: %s", feature)
		}
	}
	if !strings.Contains(features[1], "@case:z-isolated") || !strings.Contains(features[1], "Given an isolated case") ||
		strings.Contains(features[1], "Background:") || strings.Contains(features[0], "@case:z-isolated") {
		t.Fatalf("feature blocks were not isolated: %v", features)
	}
	specExecutionJSON(t, output, "pricing/discounts", cases...)
	text := specExecutionCLI(t, p, 1)
	if !strings.Contains(text, "passed record-passing (product:pricing/discounts#passing)") ||
		!strings.Contains(text, "diverged record-divergent (product:pricing/discounts#divergent)") {
		t.Fatalf("text report loses scenario evidence: %s", text)
	}
	specReceivedFiles(t, p, 2)
}

func TestSpecExecutionTagsAndPinnedFeatureFormat(t *testing.T) {
	for _, format := range []string{"gherkin", "facet-spec"} {
		t.Run(format, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			p := newSpecProject(t)
			c := specExecutionCase{"passing", "Given the pinned product", facet.TestPassed}
			file, ext, tag := "pricing", ".feature", "case"
			body := "Feature: Original feature\n@case:passing\nScenario: Pass\n  " + c.statement + "\n"
			if format == "facet-spec" {
				ext, tag = ".md", "id"
				body = "---\nschema: facet-spec/0.1\nid: pricing\ntitle: Pricing\nstatus: approved\nowner: checkout\ncase_tag: id\n---\n" +
					"```gherkin\n@id:passing @case:different\nScenario: Pass\n  " + c.statement + "\n```\n"
			}
			writeSpecFixture(t, p.upstream, "pricing"+ext, body)
			commit := p.commit(t)
			p.configure(t, fmt.Sprintf("spec_sources:\n  - id: product\n    repo: %s\n    format: %s\n    runner: cucumber\n%s", p.remote, format, specTestRunnerConfig))
			writeSpecFixture(t, p.root, "run.sh", specTestRunner)
			specExecutionRecord(t, p, file, c)
			p.command(t, 0, "spec", "update")
			pinned, _ := p.pin(t)
			if pinned.Scenarios[file+"#passing"].Commit != commit {
				t.Fatalf("scenario is not pinned at %s: %+v", commit, pinned)
			}
			writeSpecFixture(t, p.upstream, "pricing"+ext, strings.Replace(body, c.statement, "Given the newer product", 1))
			if p.commit(t) == commit {
				t.Fatal("upstream did not advance")
			}
			// Fetch the old commit into a fresh cache from a local Git remote.
			if err := os.RemoveAll(filepath.Join(p.root, ".facet/spec-cache")); err != nil {
				t.Fatal(err)
			}
			got := specExecutionCLI(t, p, 0, "--json", "--only", "facet")
			materialised := specReceivedFiles(t, p, 1)[0]
			if !strings.Contains(materialised, "@"+tag+":passing") || !strings.Contains(materialised, c.statement) ||
				strings.Contains(materialised, "Given the newer product") || format == "gherkin" && materialised != body {
				t.Fatalf("runner did not receive the expected pinned feature: %s", materialised)
			}
			specExecutionJSON(t, got, file, c)
		})
	}
}

func TestSpecExecutionUnavailableDependencies(t *testing.T) {
	for _, mode := range []string{"no runner", "offline", "no report"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			t.Setenv("FACET_STRICT_DEPS", "")
			p := newSpecProject(t)
			c := specExecutionCase{"passing", "Given a pinned step", facet.TestUnmeasured}
			commit := p.markdown(t, "approved", "@case:passing\nScenario: Pass\n  "+c.statement)
			base := fmt.Sprintf("spec_sources:\n  - id: product\n    repo: %s\n    path: specs\n", p.remote)
			config := base + "    runner: cucumber\n" + specTestRunnerConfig
			if mode == "no runner" {
				config = base
			}
			p.configure(t, config)
			writeSpecFixture(t, p.root, "run.sh", specTestRunner)
			specExecutionRecord(t, p, "pricing/discounts", c)
			p.command(t, 0, "spec", "update")
			pinned, _ := p.pin(t)
			if pinned.Scenarios["pricing/discounts#passing"].Commit != commit {
				t.Fatalf("missing pin at %s: %+v", commit, pinned)
			}
			switch mode {
			case "offline":
				if err := os.Rename(p.upstream, filepath.Join(t.TempDir(), "offline")); err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(filepath.Join(p.root, ".facet/spec-cache")); err != nil {
					t.Fatal(err)
				}
			case "no report":
				writeSpecFixture(t, p.root, "run.sh", "set -eu\ntest -f \"$1\"\ngrep -Fq '@case:passing' \"$1\"\nprintf '%s\\n' \"$1\" > attempted\n")
			}
			got := specExecutionCLI(t, p, 0, "--json", "--only", "facet")
			result := specExecutionJSON(t, got, "pricing/discounts", c)["execution/spec/passing"].Scenarios[0]
			switch mode {
			case "no runner":
				if !strings.Contains(result.Message, "spec_sources runner") {
					t.Fatalf("missing setting diagnosis: %+v", result)
				}
			case "offline":
				specReceivedFiles(t, p, 0)
				if !strings.Contains(result.Message, "cannot fetch pinned specification commit") {
					t.Fatalf("missing offline diagnosis: %+v", result)
				}
			case "no report":
				path := strings.TrimSpace(specExecutionRead(t, p.root, "attempted"))
				if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
					t.Fatalf("materialised directory was retained: %v", err)
				}
				if !strings.Contains(result.Message, "fresh report not available") {
					t.Fatalf("missing report diagnosis: %+v", result)
				}
			}
			t.Setenv("FACET_STRICT_DEPS", "1")
			specExecutionCLI(t, p, 1, "--json", "--only", "facet")
		})
	}
}

func TestSpecExecutionValidatesRunnerConfiguration(t *testing.T) {
	p := newSpecProject(t)
	base := fmt.Sprintf("spec_sources:\n  - id: product\n    repo: %s\n    runner: cucumber\n", p.remote)
	for _, config := range []string{base, base + strings.Replace(specTestRunnerConfig, "cucumber-json", "junit", 1)} {
		p.configure(t, config)
		out := p.command(t, 1, "test", "--json")
		if !strings.Contains(out, `runner "cucumber" must name a declared cucumber-json runner`) {
			t.Fatalf("invalid runner configuration: %s", out)
		}
	}
}

func TestSpecExecutionSelectivePinsUseEachScenarioCommit(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	p := newSpecProject(t)
	stays := specExecutionCase{"stays", "Given the old first case", facet.TestPassed}
	moves := specExecutionCase{"moves", "Given the new second case", facet.TestPassed}
	document := func(first, second string) string {
		return "Feature: Selective pins\n@case:stays\nScenario: First\n  " + first +
			"\n@case:moves\nScenario: Second\n  " + second + "\n"
	}
	writeSpecFixture(t, p.upstream, "pricing.feature", document(stays.statement, "Given the old second case"))
	old := p.commit(t)
	p.configure(t, fmt.Sprintf("spec_sources:\n  - id: product\n    repo: %s\n    format: gherkin\n    runner: cucumber\n%s", p.remote, specTestRunnerConfig))
	writeSpecFixture(t, p.root, "run.sh", specTestRunner)
	specExecutionRecord(t, p, "pricing", stays, moves)
	p.command(t, 0, "spec", "update")
	writeSpecFixture(t, p.upstream, "pricing.feature", document("Given the new first case", moves.statement))
	current := p.commit(t)
	p.command(t, 0, "spec", "update", "--only", "pricing#moves")
	pinned, _ := p.pin(t)
	if pinned.Scenarios["pricing#stays"].Commit != old || pinned.Scenarios["pricing#moves"].Commit != current {
		t.Fatalf("selective update did not create mixed pins: %+v", pinned)
	}
	got := specExecutionCLI(t, p, 0, "--json", "--only", "facet")
	files := specReceivedFiles(t, p, 2)
	if !strings.Contains(files[0], document("Given the new first case", moves.statement)) ||
		!strings.Contains(files[1], document(stays.statement, "Given the old second case")) {
		t.Fatalf("runner did not receive both pinned files: %v", files)
	}
	specExecutionJSON(t, got, "pricing", stays, moves)
}

func TestSpecExecutionRejectsIncompleteAndDuplicateEvidence(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	p := newSpecProject(t)
	c := specExecutionCase{"passing", "Given a pinned step\nThen a pinned outcome", facet.TestUnproven}
	commit := p.markdown(t, "approved", "@case:passing\nScenario: Pass\n  Given a pinned step\n  Then a pinned outcome")
	p.configure(t, fmt.Sprintf("spec_sources:\n  - id: product\n    repo: %s\n    path: specs\n    runner: cucumber\n%s", p.remote, specTestRunnerConfig))
	specExecutionRecord(t, p, "pricing/discounts", c)
	p.command(t, 0, "spec", "update")
	pinned, _ := p.pin(t)
	if pinned.Scenarios["pricing/discounts#passing"].Commit != commit {
		t.Fatalf("missing pin at %s: %+v", commit, pinned)
	}
	for _, mode := range []string{"missing step", "duplicate element"} {
		t.Run(mode, func(t *testing.T) {
			runner := specTestRunner
			if mode == "missing step" {
				runner = strings.Replace(runner, "      if (tag == \"\") {", "      if ($1 == \"Then\") next\n      if (tag == \"\") {", 1)
			} else {
				runner = strings.Replace(runner, "END { emit() }", "END { emit(); emit() }", 1)
			}
			writeSpecFixture(t, p.root, "run.sh", runner)
			got := specExecutionCLI(t, p, 0, "--json", "--only", "facet")
			file := specReceivedFiles(t, p, 1)[0]
			if !strings.Contains(file, "@case:passing") || !strings.Contains(file, "Then a pinned outcome") {
				t.Fatalf("source premise missing: %s", file)
			}
			specExecutionJSON(t, got, "pricing/discounts", c)
		})
	}
}

func TestSpecExecutionOutlineRowsAndHooks(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	p := newSpecProject(t)
	c := specExecutionCase{"outline", "Given a <value> product", facet.TestPassed}
	commit := p.markdown(t, "approved", "@case:outline\nScenario Outline: Outline\n  "+c.statement+
		"\n  Examples:\n    | value |\n    | first |\n    | second |")
	p.configure(t, fmt.Sprintf("spec_sources:\n  - id: product\n    repo: %s\n    path: specs\n    runner: cucumber\n%s", p.remote, specTestRunnerConfig))
	specExecutionRecord(t, p, "pricing/discounts", c)
	recordPath := "docs/facets/features/execution/spec/outline.yaml"
	record := specExecutionRead(t, p.root, recordPath)
	writeSpecFixture(t, p.root, recordPath, record+"      examples:\n        - columns: [value]\n          rows: [[first], [second]]\n")
	p.command(t, 0, "spec", "update")
	pinned, _ := p.pin(t)
	if got := pinned.Scenarios["pricing/discounts#outline"]; got.Commit != commit || len(got.Sequence.Examples) != 1 || len(got.Sequence.Examples[0]) != 3 {
		t.Fatalf("outline rows not pinned at %s: %+v", commit, got)
	}

	const runner = `set -eu
test "$#" -eq 1
test -f "$1"
printf '1\n' > received-count
printf '%s\n' "$1" > received-paths
cp "$1" "received-${1##*/}"
printf '[{"uri":"%s","elements":[' "$1" > cucumber.json
awk '
  function emit(line) {
    printf "%s{\"type\":\"scenario\",\"line\":%d,\"tags\":[{\"name\":\"@case:outline\"}],\"steps\":[{\"hidden\":true,\"result\":{\"status\":\"HOOK\"}},{\"line\":%d,\"result\":{\"status\":\"passed\"}}]}", sep, line, source
    sep=","
  }
  /Given a <value> product/ { source=NR }
  /\| first \|/ { emit(NR) }
  /\| second \|/ { emit(NR) }
' "$1" >> cucumber.json
printf ']}]\n' >> cucumber.json
`
	for _, tc := range []struct {
		name, script, state string
		exit                int
	}{
		{"both rows", strings.ReplaceAll(runner, "HOOK", "passed"), facet.TestPassed, 0},
		{"missing row", strings.ReplaceAll(strings.Replace(runner, "/\\| second \\|/ { emit(NR) }", "", 1), "HOOK", "passed"), facet.TestNotRun, 0},
		{"failed hook", strings.ReplaceAll(runner, "HOOK", "failed"), facet.TestFailed, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeSpecFixture(t, p.root, "run.sh", tc.script)
			got := specExecutionCLI(t, p, tc.exit, "--json", "--only", "facet")
			feature := specReceivedFiles(t, p, 1)[0]
			for _, part := range []string{"@case:outline", c.statement, "| first |", "| second |"} {
				if !strings.Contains(feature, part) {
					t.Fatalf("outline premise %s absent from %s", part, feature)
				}
			}
			c.state = tc.state
			specExecutionJSON(t, got, "pricing/discounts", c)
		})
	}
}
