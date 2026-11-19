// Fixtures contain example anchors rather than implementing those features.
// @facet-ignore-file
package facet

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/goccy/go-yaml"
)

// Named mutation: suppress the new-unclaimed finding in CheckTests. The binary
// still builds, but a new unclaimed test no longer fails the ratchet.
func TestDiscoveryRatchet(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "tests"), 0755)
	os.WriteFile(filepath.Join(root, "tests/a.go"), []byte("package tests"), 0644)
	os.WriteFile(filepath.Join(root, ConfigName), []byte("runners:\n  go:\n    command: touch forbidden\n    list: printf 'tests/a.go\\n'\n"), 0644)
	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	report, err := CheckTests(context.Background(), cfg, cfg.All()[0], "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Failed() || len(report.Runners) != 1 || len(report.Runners[0].Files) != 1 {
		t.Fatalf("missing drift: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(root, "forbidden")); !os.IsNotExist(err) {
		t.Fatal("test command executed")
	}
}

// Named mutation: append unclaimed paths while updating the baseline. The
// second check then incorrectly passes instead of rejecting the new path.
func TestBaselineUpdateNeverAdds(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "tests"), 0755)
	os.WriteFile(filepath.Join(root, "tests/a.go"), []byte("package tests"), 0644)
	os.MkdirAll(filepath.Join(root, "docs/facets"), 0755)
	os.WriteFile(filepath.Join(root, "docs/facets/test-baseline.yaml"), []byte("runners:\n  go: [tests/old.go]\n"), 0644)
	cfg := &Config{root: root, Runners: map[string]Runner{"go": {Command: "true", List: "printf 'tests/a.go\\n'"}}}
	report, err := CheckTests(context.Background(), cfg, cfg.All()[0], "", true)
	if err != nil || !report.Failed() {
		t.Fatalf("new path passed: %+v %v", report, err)
	}
	baseline, err := readTestBaseline(filepath.Join(root, "docs/facets/test-baseline.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline.Runners["go"]) != 0 {
		t.Fatalf("baseline grew: %+v", baseline)
	}
}

func discoveryFixture(t *testing.T, list string) (*Config, Registry) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tests"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(root, "tests", name), []byte("package tests"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &Config{root: root, Runners: map[string]Runner{"go": {Command: "touch forbidden", List: list}}}
	return cfg, cfg.All()[0]
}
func findingCode(r TestDiscovery, code string) bool {
	for _, f := range r.Findings {
		if f.Code == code {
			return true
		}
	}
	return false
}
func writeDiscoveryRecord(t *testing.T, reg Registry, id string) {
	t.Helper()
	dir := filepath.Join(reg.RecordsDir, "features/a/b")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	data := "id: a/b/cc\nverification:\n  unit_tests:\n    - id: " + id + "\n      proves: [one]\n"
	if err := os.WriteFile(filepath.Join(dir, "c.yaml"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

// Named mutation: skip not-discovered findings. Existing verification files
// that disappear from discovery then pass unnoticed.
func TestDiscoveryExistingVerificationMustRun(t *testing.T) {
	cfg, reg := discoveryFixture(t, "printf 'tests/a.go\\n'")
	writeDiscoveryRecord(t, reg, "tests/b.go::case with spaces")
	report, err := CheckTests(context.Background(), cfg, reg, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Failed() || !findingCode(report, "not-discovered") {
		t.Fatalf("missing B-A: %+v", report)
	}
	writeDiscoveryRecord(t, reg, "tests/a.go::case with spaces")
	report, err = CheckTests(context.Background(), cfg, reg, "", false)
	if err != nil || report.Failed() {
		t.Fatalf("matching file rejected: %+v %v", report, err)
	}
}

// Named mutation: treat a missing list as an empty measured set. It would
// incorrectly mark the existing verification absent from runner execution.
func TestMissingListIsUnmeasured(t *testing.T) {
	cfg, reg := discoveryFixture(t, "")
	writeDiscoveryRecord(t, reg, "tests/a.go")
	report, err := CheckTests(context.Background(), cfg, reg, "", false)
	if err != nil || report.Failed() || report.Runners[0].Measured || !findingCode(report, "unmeasured") || findingCode(report, "not-discovered") {
		t.Fatalf("false measurement: %+v %v", report, err)
	}
}

// Named mutation: ignore list exit status, emptiness or path validation. Each
// fixture has an actual file, so success cannot be blamed on missing setup.
func TestDiscoveryRejectsBadOutput(t *testing.T) {
	for _, list := range []string{"exit 7", "true", "printf 'tests\\n'", "printf '../outside.go\\n'", "printf 'tests/missing.go\\n'"} {
		t.Run(list, func(t *testing.T) {
			cfg, reg := discoveryFixture(t, list)
			report, err := CheckTests(context.Background(), cfg, reg, "", false)
			if err == nil && !report.Failed() {
				t.Fatalf("bad discovery passed: %+v", report)
			}
		})
	}
	cfg, reg := discoveryFixture(t, "printf 'tests/link.go\\n'")
	outside := filepath.Join(t.TempDir(), "outside.go")
	os.WriteFile(outside, []byte("package x"), 0644)
	if err := os.Symlink(outside, filepath.Join(reg.Root, "tests/link.go")); err != nil {
		t.Fatal(err)
	}
	report, err := CheckTests(context.Background(), cfg, reg, "", false)
	if err != nil || !findingCode(report, "invalid-path") {
		t.Fatalf("symlink escape passed: %+v %v", report, err)
	}
}

// Named mutation: resolve list paths against root instead of runner cwd.
// The real nested fixture would disappear or fail path validation.
func TestDiscoveryCwdAndAbsoluteDeduplication(t *testing.T) {
	cfg, reg := discoveryFixture(t, "printf 'a.go\\n./a.go\\n'")
	runner := cfg.Runners["go"]
	runner.Cwd = "tests"
	runner.List += `; printf '%s\n' "$PWD/a.go"`
	cfg.Runners["go"] = runner
	writeDiscoveryRecord(t, reg, "tests/a.go")
	report, err := CheckTests(context.Background(), cfg, reg, "", false)
	if err != nil || report.Failed() || len(report.Runners[0].Files) != 1 || report.Runners[0].Files[0] != "tests/a.go" {
		t.Fatalf("wrong cwd/dedup: %+v %v", report, err)
	}
}

// Named mutation: remove strict decoding or runner validation. A misspelled
// list key would become an apparently legitimate unmeasured declaration.
func TestRunnerConfigurationRejectsInvalidDeclarations(t *testing.T) {
	for _, data := range []string{"runners:\n  go:\n    command: true\n    lsit: true\n", "runners:\n  go:\n    list: true\n", "runners:\n  go:\n    command: echo ok\n    cwd: ../escape\n", "runners:\n  go:\n    command: echo ok\n    report: tap\n"} {
		root := t.TempDir()
		os.WriteFile(filepath.Join(root, ConfigName), []byte(data), 0644)
		if _, err := LoadConfig(root); err == nil {
			t.Fatalf("invalid config passed: %s", data)
		}
	}
}

// Named mutation: omit the proves-mismatch warning. The fixture first proves
// a matching anchor passes, then changes only the claimed scenario.
func TestDiscoveryProvesMismatch(t *testing.T) {
	cfg, reg := discoveryFixture(t, "printf 'tests/a.go\\n'")
	writeDiscoveryRecord(t, reg, "tests/a.go")
	path := filepath.Join(reg.Root, "tests/a.go")
	os.WriteFile(path, []byte("// @facet: a/b/cc\n// @facet-proves: one\npackage tests\n"), 0644)
	anchors := ScanContent("tests/a.go", []byte("// @facet: a/b/cc\n// @facet-proves: one\n"))
	if len(anchors) != 1 || len(anchors[0].Proves) != 1 {
		t.Fatal("anchor fixture did not parse")
	}
	report, err := CheckTests(context.Background(), cfg, reg, "", false)
	if err != nil || findingCode(report, "proves-mismatch") {
		t.Fatalf("valid anchor rejected: %+v %v", report, err)
	}
	os.WriteFile(path, []byte("// @facet: a/b/cc\n// @facet-proves: other\npackage tests\n"), 0644)
	report, err = CheckTests(context.Background(), cfg, reg, "", false)
	if err != nil || !findingCode(report, "proves-mismatch") {
		t.Fatalf("mismatch missed: %+v %v", report, err)
	}
}

// Named mutation: compare B-A against each runner individually rather than the
// union; valid verification files supplied by the other runner then fail.
func TestDiscoveryUsesRunnerUnionAndKeepsUnselectedBaseline(t *testing.T) {
	cfg, reg := discoveryFixture(t, "printf 'tests/a.go\\n'")
	cfg.Runners["other"] = Runner{Command: "true", List: "printf 'tests/b.go\\n'"}
	writeDiscoveryRecord(t, reg, "tests/a.go")
	path := filepath.Join(reg.RecordsDir, "features/a/b/other.yaml")
	os.WriteFile(path, []byte("id: a/b/other\nverification:\n  unit_tests:\n    - id: tests/b.go\n      proves: [two]\n"), 0644)
	report, err := CheckTests(context.Background(), cfg, reg, "", false)
	if err != nil || report.Failed() {
		t.Fatalf("union rejected: %+v %v", report, err)
	}
	baselinePath := filepath.Join(reg.RecordsDir, "test-baseline.yaml")
	os.WriteFile(baselinePath, []byte("runners:\n  go: [tests/a.go]\n  other: [tests/b.go]\n"), 0644)
	report, err = CheckTests(context.Background(), cfg, reg, "go", true)
	if err != nil || report.Failed() || !findingCode(report, "absence-unmeasured") {
		t.Fatalf("partial selection failed: %+v %v", report, err)
	}
	baseline, err := readTestBaseline(baselinePath)
	if err != nil || len(baseline.Runners["other"]) != 1 || len(baseline.Runners["go"]) != 0 {
		t.Fatalf("unselected baseline changed: %+v %v", baseline, err)
	}
}

// Named mutation: always read top-level runners instead of the selected registry
// declaration. Each registry must discover only its own cwd and baseline.
func TestRunnerConfigurationIsPerRegistry(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one", "two"} {
		os.MkdirAll(filepath.Join(root, name), 0755)
		os.WriteFile(filepath.Join(root, name, "test.go"), []byte("package tests"), 0644)
	}
	data := "registries:\n  one:\n    records: docs/one\n    runners:\n      local:\n        cwd: one\n        command: echo one\n        list: printf 'test.go\\n'\n  two:\n    records: docs/two\n    runners:\n      local:\n        cwd: two\n        command: echo two\n        list: printf 'test.go\\n'\n"
	os.WriteFile(filepath.Join(root, ConfigName), []byte(data), 0644)
	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, reg := range cfg.All() {
		report, err := CheckTests(context.Background(), cfg, reg, "", false)
		if err != nil || len(report.Runners) != 1 || len(report.Runners[0].Files) != 1 || report.Runners[0].Files[0] != reg.Name+"/test.go" {
			t.Fatalf("registry isolation failed: %+v %v", report, err)
		}
	}
	if _, err := cfg.Select("unknown"); err == nil {
		t.Fatal("unknown registry accepted")
	}
}

// Named mutation: interpret JSON as newline paths, accept trailing documents,
// or silently drop entries without explicit paths. Real discovery must fail.
func TestDiscoveryJSONUsesExplicitPathsAndRejectsMalformedLists(t *testing.T) {
	for _, data := range []string{`["tests/a.go","tests/b.go"]`, `[{"file":"tests/a.go"},{"file":"tests/b.go"}]`} {
		cfg, reg := discoveryFixture(t, "printf '%s' '"+data+"'")
		r := cfg.Runners["go"]
		r.ListFormat = "json"
		cfg.Runners["go"] = r
		report, err := CheckTests(context.Background(), cfg, reg, "", false)
		if err != nil || len(report.Runners) != 1 || !report.Runners[0].Measured || len(report.Runners[0].Files) != 2 {
			t.Fatalf("JSON paths were not discovered: %+v %v", report, err)
		}
	}
	for _, data := range []string{`null`, `{}`, `[]`, `["tests/a.go"] []`, `[{"name":"tests/a.go"}]`, `["tests/a.go",null]`, `[1]`, `["../escape.go"]`} {
		cfg, reg := discoveryFixture(t, "printf '%s' '"+data+"'")
		r := cfg.Runners["go"]
		r.ListFormat = "json"
		cfg.Runners["go"] = r
		report, err := CheckTests(context.Background(), cfg, reg, "", false)
		if err == nil && !report.Failed() {
			t.Fatalf("Invalid JSON discovery passed: %s %+v", data, report)
		}
	}
}

// Named mutation: tolerate an unknown format or a format without a command.
func TestDiscoveryFormatConfigurationIsValidated(t *testing.T) {
	for _, r := range []Runner{
		{Command: "true", List: "true", ListFormat: "xml"},
		{Command: "true", ListFormat: "json"},
	} {
		if validateRunners(map[string]Runner{"local": r}) == nil {
			t.Fatalf("Invalid discovery format accepted: %+v", r)
		}
	}
}

// Named mutation: omit list_format from the shipped schema while runtime
// accepts it. Real example configuration must also pass consumer validation.
func TestExampleRunnerDeclarationsPassTheShippedSchema(t *testing.T) {
	root := project(t, "")
	copySchemas(t, root)
	compiled, err := compileSchema(filepath.Join(root, "docs/facets/schema/facetrc.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"jest", "vitest", "phpunit"} {
		data, err := os.ReadFile(SourceTestPath(t, "examples", "test-runners", name, ConfigName))
		if err != nil {
			t.Fatal(err)
		}
		var config map[string]any
		if err := yaml.Unmarshal(data, &config); err != nil {
			t.Fatal(err)
		}
		if err := compiled.Validate(config); err != nil {
			t.Fatalf("Example %s rejected by shipped schema: %v", name, err)
		}
	}
	for _, fields := range []map[string]any{
		{"command": "true", "list": "true", "list_format": "xml"},
		{"command": "true", "list_format": "json"},
	} {
		if err := compiled.Validate(map[string]any{"runners": map[string]any{"local": fields}}); err == nil {
			t.Fatalf("Schema accepted invalid discovery declaration: %+v", fields)
		}
	}
}
