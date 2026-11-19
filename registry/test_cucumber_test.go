// @facet-ignore-file
package facet

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCucumberFileEvidence(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "sample.feature"), []byte("Feature: sample\n Scenario: measured\n  Given observed\n"), 0600)
	cases := []struct {
		name, report, want string
		invalid            bool
	}{
		{"pass", `[{"uri":"sample.feature","elements":[{"type":"scenario","line":2,"steps":[{"line":3,"result":{"status":"passed"}}]}]}]`, TestPassed, false},
		{"failure beats skip", `[{"uri":"sample.feature","elements":[{"type":"scenario","line":2,"steps":[{"line":3,"result":{"status":"failed"}},{"line":3,"result":{"status":"skipped"}}]}]}]`, TestFailed, false},
		{"skip", `[{"uri":"sample.feature","elements":[{"type":"scenario","line":2,"steps":[{"line":3,"result":{"status":"skipped"}}]}]}]`, TestUnmeasured, false},
		{"undefined", `[{"uri":"sample.feature","elements":[{"type":"scenario","line":2,"steps":[{"line":3,"result":{"status":"undefined"}}]}]}]`, TestUnmeasured, false},
		{"pending", `[{"uri":"sample.feature","elements":[{"type":"scenario","line":2,"steps":[{"line":3,"result":{"status":"pending"}}]}]}]`, TestUnmeasured, false},
		{"ambiguous", `[{"uri":"sample.feature","elements":[{"type":"scenario","line":2,"steps":[{"line":3,"result":{"status":"ambiguous"}}]}]}]`, TestFailed, false},
		{"empty steps", `[{"uri":"sample.feature","elements":[{"type":"scenario","line":2,"steps":[]}]}]`, TestUnmeasured, false},
		{"no scenarios", `[{"uri":"sample.feature","elements":[]}]`, TestUnmeasured, false},
		{"failed hook", `[{"uri":"sample.feature","elements":[{"type":"scenario","line":2,"steps":[{"line":3,"result":{"status":"passed"}}],"after":[{"line":3,"result":{"status":"failed"}}]}]}]`, TestFailed, false},
		{"unknown status", `[{"uri":"sample.feature","elements":[{"type":"scenario","line":2,"steps":[{"line":3,"result":{"status":"surprise"}}]}]}]`, TestUnmeasured, false},
		{"unbound", `[{"name":"sample.feature","elements":[{"type":"scenario","line":2,"steps":[{"line":3,"result":{"status":"passed"}}]}]}]`, "", false},
		{"escape", `[{"uri":"../sample.feature","elements":[]}]`, "", false},
		{"empty report", `[]`, "", false},
		{"failed background", `[{"uri":"sample.feature","elements":[{"type":"background","steps":[{"line":3,"result":{"status":"failed"}}]},{"type":"scenario","line":2,"steps":[{"line":3,"result":{"status":"passed"}}]}]}]`, TestFailed, false},
		{"duplicate URI cannot hide failure", `[{"uri":"sample.feature","elements":[{"type":"scenario","line":2,"steps":[{"line":3,"result":{"status":"failed"}}]}]},{"uri":"sample.feature","elements":[{"type":"scenario","line":2,"steps":[{"line":3,"result":{"status":"passed"}}]}]}]`, TestFailed, false},
		{"duplicate URI cannot hide missing result", `[{"uri":"sample.feature","elements":[{"type":"scenario","line":2,"steps":[{"line":3,"result":{"status":"pending"}}]}]},{"uri":"sample.feature","elements":[{"type":"scenario","line":2,"steps":[{"line":3,"result":{"status":"passed"}}]}]}]`, TestUnmeasured, false},
		{"mixed scenarios preserve failure", `[{"uri":"sample.feature","elements":[{"type":"scenario","line":2,"steps":[{"line":3,"result":{"status":"passed"}}]},{"type":"scenario","line":2,"steps":[{"line":3,"result":{"status":"failed"}}]}]}]`, TestFailed, false},
		{"null", `null`, "", true},
		{"trailing document", `[] []`, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCucumberResults(root, root, []byte(tc.report), []string{"sample.feature"})
			if (err != nil) != tc.invalid || got["sample.feature"] != tc.want {
				t.Fatalf("got %v, %v; want %s invalid=%v", got, err, tc.want, tc.invalid)
			}
		})
	}
}

func TestCucumberExecutionUsesFreshReports(t *testing.T) {
	cfg, reg := discoveryFixture(t, "printf 'tests/a.feature\\n'")
	writeDiscoveryRecord(t, reg, "tests/a.feature")
	os.WriteFile(filepath.Join(reg.Root, "tests/a.feature"), []byte("Feature: sample\n Scenario: measured\n  Given observed\n"), 0600)
	runner := cfg.Runners["go"]
	runner.Report, runner.ReportFile = "cucumber-json", "cucumber.json"
	runner.Command = `printf '%s' '[{"uri":"tests/a.feature","elements":[{"type":"scenario","line":2,"steps":[{"line":3,"result":{"status":"passed"}}]}]}]' > cucumber.json`
	cfg.Runners["go"] = runner
	first, err := ExecuteTests(context.Background(), cfg, reg, "", false, nil, nil)
	if err != nil || first.Features[0].State != TestPassed {
		t.Fatalf("fresh pass: %+v %v", first, err)
	}
	runner.Command = "true"
	cfg.Runners["go"] = runner
	second, err := ExecuteTests(context.Background(), cfg, reg, "", false, nil, nil)
	if err != nil || second.Features[0].State != TestUnmeasured {
		t.Fatalf("stale pass invented: %+v %v", second, err)
	}
	runner.Command = `printf '%s' '[{"uri":"tests/a.feature","elements":[{"type":"scenario","line":2,"steps":[{"line":3,"result":{"status":"failed"}}]}]}]' > cucumber.json; exit 7`
	cfg.Runners["go"] = runner
	third, err := ExecuteTests(context.Background(), cfg, reg, "", false, nil, nil)
	if err != nil || third.Features[0].State != TestFailed || third.ExitCode() != 7 {
		t.Fatalf("failure/exit lost: %+v %v", third, err)
	}
}

func TestExecutionExplicitFeaturePrefix(t *testing.T) {
	cfg, reg := discoveryFixture(t, "printf 'tests/a.go\\n'")
	writeDiscoveryRecord(t, reg, "tests/a.go")
	records, err := reg.LoadFeatures()
	if err != nil || len(records) != 1 {
		t.Fatalf("fixture: %v %v", records, err)
	}
	cfg.TestFeaturePrefix = records[0].ID
	got, err := ExecuteTests(context.Background(), cfg, reg, "", false, nil, nil)
	if err != nil || len(got.Features) != 1 || got.Discovery.FeaturePrefix != cfg.TestFeaturePrefix {
		t.Fatalf("scope lost: %+v %v", got, err)
	}
	cfg.TestFeaturePrefix = "missing/area/"
	if _, err = ExecuteTests(context.Background(), cfg, reg, "", false, nil, nil); err == nil {
		t.Fatal("empty scope must fail, not report complete coverage")
	}
}

func TestFeatureScopeUnionAndSourceProtection(t *testing.T) {
	cfg, reg := discoveryFixture(t, "printf 'tests/a.go\\n'")
	writeDiscoveryRecord(t, reg, "tests/a.go")
	dir := filepath.Join(reg.RecordsDir, "features/other/area")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "record.yaml"), []byte("id: other/area/record\nverification:\n  unit_tests:\n    - id: tests/b.go\n      proves: [one]\n"), 0644)
	cfg.TestFeaturePrefixes = []string{"a/b/", "other/area/"}
	got, err := testScopeFeatures(cfg, reg)
	if err != nil || len(got) != 2 {
		t.Fatalf("union: %v %v", got, err)
	}
	cfg.TestFeaturePrefixes = []string{"a/b/", "missing/area/"}
	if _, err = testScopeFeatures(cfg, reg); err == nil {
		t.Fatal("partially unknown scope accepted")
	}
	cfg.TestFeaturePrefixes = []string{""}
	if _, err = testScopeFeatures(cfg, reg); err == nil {
		t.Fatal("empty configured prefix silently widened the scope")
	}
	cfg.TestFeaturePrefixes = []string{"a/b/"}
	runner := cfg.Runners["go"]
	runner.List = "printf 'tests/a.go\\ntests/b.go\\n'"
	cfg.Runners["go"] = runner
	discovery, err := CheckTests(context.Background(), cfg, reg, "", false)
	if err != nil || discovery.Failed() {
		t.Fatalf("unselected but declared test became unclaimed: %+v %v", discovery, err)
	}
	runner = cfg.Runners["go"]
	runner.Report = "cucumber-json"
	runner.ReportFile = "tests/b.go"
	runner.Command = "touch forbidden"
	cfg.Runners["go"] = runner
	if _, err = ExecuteTests(context.Background(), cfg, reg, "", false, nil, nil); err == nil {
		t.Fatal("scope allowed unselected test source as report")
	}
	if _, err = os.Stat(filepath.Join(reg.Root, "forbidden")); !os.IsNotExist(err) {
		t.Fatal("unsafe runner executed")
	}
}

func TestCucumberRequiresEveryDeclaredCaseAndStep(t *testing.T) {
	root := t.TempDir()
	const source = "Feature: examples\n  Rule: reader\n    Background: fixture\n      Given ready\n    Scenario Outline: repeated\n      When <action>\n    Examples:\n      | action |\n      | open |\n      | close |\n    Scenario: ordinary\n      Given ready\n"
	os.WriteFile(filepath.Join(root, "all.feature"), []byte(source), 0600)
	step := func(line int, status string) map[string]any {
		return map[string]any{"line": line, "result": map[string]any{"status": status}}
	}
	scenario := func(line int, steps ...map[string]any) map[string]any {
		return map[string]any{"type": "scenario", "line": line, "steps": steps}
	}
	first := scenario(9, step(4, "passed"), step(6, "passed"))
	second := scenario(10, step(4, "passed"), step(6, "passed"))
	ordinary := scenario(11, step(4, "passed"), step(12, "passed"))
	cases := []struct {
		name     string
		elements []map[string]any
		want     string
	}{
		{"complete rule and examples", []map[string]any{first, second, ordinary}, TestPassed},
		{"tag filtered report", []map[string]any{first}, TestUnmeasured},
		{"missing example", []map[string]any{first, ordinary}, TestUnmeasured},
		{"duplicate case cannot replace missing case", []map[string]any{first, first, ordinary}, TestUnmeasured},
		{"duplicate case cannot count as a clean run", []map[string]any{first, second, ordinary, first}, TestUnmeasured},
		{"missing source step", []map[string]any{scenario(9, step(4, "passed")), second, ordinary}, TestUnmeasured},
		{"wrong source step", []map[string]any{scenario(9, step(4, "passed"), step(999, "passed")), second, ordinary}, TestUnmeasured},
		{"failure beats omitted cases", []map[string]any{scenario(9, step(4, "passed"), step(6, "failed"))}, TestFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, _ := json.Marshal([]map[string]any{{"uri": "all.feature", "elements": tc.elements}})
			got, err := parseCucumberResults(root, root, data, []string{"all.feature"})
			if err != nil || got["all.feature"] != tc.want {
				t.Fatalf("got %v %v; want %s", got, err, tc.want)
			}
		})
	}
}

func TestCucumberSourceDialectAndSeparateBackground(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "fr.feature"), []byte("# language: fr\nFonctionnalité: Exemple\n Scénario: complet\n  Soit un lecteur prêt\n"), 0600)
	data := []byte(`[{"uri":"fr.feature","elements":[{"type":"scenario","line":3,"steps":[{"line":4,"result":{"status":"passed"}}]}]}]`)
	got, err := parseCucumberResults(root, root, data, []string{"fr.feature"})
	if err != nil || got["fr.feature"] != TestPassed {
		t.Fatalf("dialect: %v %v", got, err)
	}
	os.WriteFile(filepath.Join(root, "bg.feature"), []byte("Feature: Sample\n Background: host\n  Given ready\n Scenario: read\n  Then visible\n"), 0600)
	data = []byte(`[{"uri":"bg.feature","elements":[{"type":"background","steps":[{"line":3,"result":{"status":"passed"}}]},{"type":"scenario","line":4,"steps":[{"line":5,"result":{"status":"passed"}}]}]}]`)
	got, err = parseCucumberResults(root, root, data, []string{"bg.feature"})
	if err != nil || got["bg.feature"] != TestPassed {
		t.Fatalf("background: %v %v", got, err)
	}
}
