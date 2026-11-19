// Fixtures here contain example @facet: tags; this file drives
// `facet setup status` against throwaway repositories rather than implementing
// a feature.
// @facet-ignore-file

package cli

import (
	"debug/buildinfo"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const statusRecord = `schema_version: "0.1"
id: demo/thing/widget
lifecycle: planned
intent:
  summary: |
    Keeps a promise for the status fixtures.
contract:
  scenarios:
    - id: promise-kept
      statement: |
        Given input
        When read
        Then output
lineage:
  owner:
    team: demo
  created_at: "2026-10-01"
`

// statusSchema accepts the current record edition and
// nothing else about a record, so the schema check cannot decide the outcome.
const statusSchema = `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","properties":{"schema_version":{"enum":["0.1"]}}}`

// statusRepo builds a git repository holding the files given and stages all of
// them, so `git ls-files` sees them. Nothing is committed: the checks that ask
// git ask for tracked files, and the index is enough.
func statusRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	git(t, root, "init", "-q")
	git(t, root, "config", "user.email", "t@example.com")
	git(t, root, "config", "user.name", "t")
	for rel, body := range files {
		write(t, filepath.Join(root, rel), body)
	}
	git(t, root, "add", "-A")
	return root
}

// withRecord is the smallest adopted project: one record, nothing else.
func withRecord(extra map[string]string) map[string]string {
	files := map[string]string{"docs/facets/features/demo/thing/widget.yaml": statusRecord}
	for k, v := range extra {
		files[k] = v
	}
	return files
}

type statusRun struct {
	rows  map[string]statusRow
	order []string
	raw   string
	exit  int
}

// runStatus runs the real binary's `setup status --json` and parses the rows.
// The environment is closed: a fake login shell, and cache, plugin and home
// directories of its own, so nothing of the machine running the tests answers
// for the project.
func runStatus(t *testing.T, bin, repo string, extraEnv []string, flags ...string) statusRun {
	t.Helper()
	skipOnWindows(t)
	home := t.TempDir()
	env := baseEnv(home,
		"CLAUDE_PLUGIN_DATA="+filepath.Join(home, "plugin-data"),
		"FACET_PLUGINS_DIR="+filepath.Join(home, "plugins"),
		"SHELL="+fakeLoginShell(t, "bash"),
	)
	env = append(env, extraEnv...)
	args := append([]string{"setup", "status", "--json"}, flags...)
	stdout, stderr, exit := runBin(t, bin, repo, env, "", args...)
	var rows []statusRow
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("setup status --json did not print a JSON array: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	run := statusRun{rows: map[string]statusRow{}, raw: stdout, exit: exit}
	for _, r := range rows {
		run.rows[r.ID] = r
		run.order = append(run.order, r.ID)
	}
	return run
}

func (r statusRun) want(t *testing.T, id string, state checkState, finding string) {
	t.Helper()
	got, ok := r.rows[id]
	if !ok {
		t.Fatalf("no row %q in %v", id, r.order)
	}
	if got.State != state {
		t.Fatalf("%s: state = %q, want %q\n  finding:  %s\n  evidence: %s", id, got.State, state, got.Finding, got.Evidence)
	}
	if finding != "" && !strings.Contains(got.Finding, finding) {
		t.Fatalf("%s: finding %q does not contain %q", id, got.Finding, finding)
	}
	if got.Evidence == "" {
		t.Fatalf("%s: a row without evidence says what nobody can check", id)
	}
}

// The report is a table: one row per check id, in order,
// each with the four fields. An id that silently disappears is a check nobody
// runs any more.
func TestSetupStatusReportsEveryCheckInOrder(t *testing.T) {
	run := runStatus(t, facetBinary(t), statusRepo(t, withRecord(nil)), nil)
	want := []string{
		"binary.resolved", "binary.current-pointer", "binary.on-shell-path", "scaffold", "lint.strict",
		"ci.trigger-coverage", "facetignore", "taxonomy", "conventions", "schema.current", "hooks.plugin",
		"mcp", "lsp", "statusline", "filter", "git.precommit", "ui", "rag.index", "plugins", "test.runners",
	}
	if strings.Join(run.order, ",") != strings.Join(want, ",") {
		t.Fatalf("rows = %v\nwant   %v", run.order, want)
	}
	for _, r := range run.rows {
		switch r.State {
		case stateOK, stateRed, stateUnmeasured, stateNotApplicable:
		default:
			t.Errorf("%s: %q is not one of the four states", r.ID, r.State)
		}
		if r.Finding == "" || r.Evidence == "" {
			t.Errorf("%s: finding %q / evidence %q — both are required", r.ID, r.Finding, r.Evidence)
		}
	}
}

// The text form is a table a person reads; the test reads the rendered output
// of the real binary, not the rows behind it.
func TestSetupStatusPrintsAnAlignedTable(t *testing.T) {
	skipOnWindows(t)
	repo := statusRepo(t, withRecord(nil))
	home := t.TempDir()
	env := baseEnv(home, "CLAUDE_PLUGIN_DATA="+filepath.Join(home, "d"), "FACET_PLUGINS_DIR="+filepath.Join(home, "p"), "SHELL="+fakeLoginShell(t, "bash"))
	stdout, _, exit := runBin(t, facetBinary(t), repo, env, "", "setup", "status")
	if exit != 0 {
		t.Fatalf("exit = %d", exit)
	}
	lines := strings.Split(stdout, "\n")
	column := -1
	for _, id := range []string{"binary.resolved", "taxonomy", "lint.strict"} {
		for _, line := range lines {
			if strings.HasPrefix(line, id+" ") {
				fields := strings.Fields(line)
				state := fields[1]
				if state != "ok" && state != "red" && state != "unmeasured" && state != "not-applicable" {
					t.Fatalf("second column of %q is %q, not a state", line, state)
				}
				at := strings.Index(line, state)
				if column >= 0 && at != column {
					t.Fatalf("the state column is not aligned: %d vs %d in\n%s", at, column, stdout)
				}
				column = at
			}
		}
	}
	if column < 0 {
		t.Fatalf("no row found in\n%s", stdout)
	}
	if !strings.Contains(stdout, " ok, ") || !strings.Contains(stdout, " unmeasured, ") {
		t.Fatalf("the summary line is missing:\n%s", stdout)
	}
}

// It is a report: red rows do not fail it, --strict does.
func TestSetupStatusExitsZeroWithRedRowsUnlessStrict(t *testing.T) {
	repo := statusRepo(t, map[string]string{"README.md": "# nothing adopted\n"})
	bin := facetBinary(t)
	plain := runStatus(t, bin, repo, nil)
	plain.want(t, "scaffold", stateRed, "")
	if plain.exit != 0 {
		t.Fatalf("exit = %d with red rows and no --strict, want 0", plain.exit)
	}
	strict := runStatus(t, bin, repo, nil, "--strict")
	if strict.exit == 0 {
		t.Fatal("--strict exited 0 with red rows")
	}
}

// Development builds can identify their source; absent metadata remains unmeasured.
func TestBinaryResolvedNamesTheVersionAndAdmitsNone(t *testing.T) {
	repo := statusRepo(t, withRecord(nil))
	versioned := runStatus(t, versionedFacetBinary(t), repo, nil)
	versioned.want(t, "binary.resolved", stateOK, harnessVersion)
	// Whether a plain build can name its source depends on how it was built:
	// the build records the commit unless it was told not to (-buildvcs=false)
	// or the tree is not a checkout. The build's own metadata says which.
	devBinary := facetBinary(t)
	dev := runStatus(t, devBinary, repo, nil)
	if buildRecordsItsCommit(t, devBinary) {
		dev.want(t, "binary.resolved", stateOK, "0.1.0-dev.")
	} else {
		dev.want(t, "binary.resolved", stateUnmeasured, "")
	}
	for _, version := range []string{"dev", "0.1.0-dev.unknown"} {
		env := newStatusEnv(repo)
		env.version = version
		if got := checkBinaryResolved(env); got.State != stateUnmeasured {
			t.Fatalf("%+v", got)
		}
	}
}

// buildRecordsItsCommit reads the Go build metadata of the binary at path and
// reports whether the build stamped the commit it was built from.
func buildRecordsItsCommit(t *testing.T, path string) bool {
	t.Helper()
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		t.Fatalf("read build metadata of %s: %v", path, err)
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && setting.Value != "" {
			return true
		}
	}
	return false
}

// The dispatcher's `selected` pointer is compared with the running binary
// through the version its install.json records.
func TestCurrentPointerMustPointAtTheRunningVersion(t *testing.T) {
	bin := versionedFacetBinary(t)
	repo := statusRepo(t, withRecord(nil))
	cache := func(t *testing.T, pointAt string, withInstallJSON bool) string {
		data := t.TempDir()
		platform := filepath.Join(data, "dist", "core", "github-facet-framework-core", "linux_amd64")
		for _, v := range []string{harnessVersion, "1.0.0"} {
			write(t, filepath.Join(platform, v, "facet"), "#!/bin/sh\n")
			if err := os.Chmod(filepath.Join(platform, v, "facet"), 0o755); err != nil {
				t.Fatal(err)
			}
			if withInstallJSON {
				write(t, filepath.Join(platform, v, "install.json"), `{"product": "core", "version": "`+v+`"}`)
			}
		}
		if err := os.Symlink(pointAt, filepath.Join(platform, "current")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join("github-facet-framework-core", "linux_amd64", "current"), filepath.Join(data, "dist", "core", "selected")); err != nil {
			t.Fatal(err)
		}
		return data
	}
	good := runStatus(t, bin, repo, []string{"CLAUDE_PLUGIN_DATA=" + cache(t, harnessVersion, true)})
	good.want(t, "binary.current-pointer", stateOK, harnessVersion)
	stale := runStatus(t, bin, repo, []string{"CLAUDE_PLUGIN_DATA=" + cache(t, "1.0.0", true)})
	stale.want(t, "binary.current-pointer", stateRed, "not at this binary's version")
	incomplete := runStatus(t, bin, repo, []string{"CLAUDE_PLUGIN_DATA=" + cache(t, harnessVersion, false)})
	incomplete.want(t, "binary.current-pointer", stateRed, "no install.json")
	none := runStatus(t, bin, repo, []string{"CLAUDE_PLUGIN_DATA=" + t.TempDir()})
	none.want(t, "binary.current-pointer", stateRed, "no selected pointer")

	// The retired 1.x layout is not read: a bin/current pointing at the
	// running version still leaves the row red.
	old := t.TempDir()
	write(t, filepath.Join(old, "bin", harnessVersion, "facet"), "#!/bin/sh\n")
	if err := os.Symlink(harnessVersion, filepath.Join(old, "bin", "current")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(old, "bin", "current", "facet")); err != nil {
		t.Fatalf("the old-layout fixture does not resolve, so ignoring it proves nothing: %v", err)
	}
	retired := runStatus(t, bin, repo, []string{"CLAUDE_PLUGIN_DATA=" + old})
	retired.want(t, "binary.current-pointer", stateRed, "no selected pointer")
}

// A login shell that finds a facet reporting this version is ok; one that finds
// none is red; the probe is the shell's own `command -v`.
func TestShellPathProbeFindsOrMissesFacet(t *testing.T) {
	bin := versionedFacetBinary(t)
	repo := statusRepo(t, withRecord(nil))
	bare := t.TempDir()
	onPath := t.TempDir()
	write(t, filepath.Join(onPath, "facet"), "#!/bin/sh\necho \"facet version "+harnessVersion+"\"\n")
	if err := os.Chmod(filepath.Join(onPath, "facet"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Precondition: the fixture's PATH really lacks facet, so red means missed.
	probe := exec.Command("/bin/sh", "-c", "command -v facet")
	probe.Env = []string{"PATH=" + bare + ":/usr/bin:/bin"}
	if out, _ := probe.Output(); strings.TrimSpace(string(out)) != "" {
		t.Skipf("a facet is already on the base PATH: %s", out)
	}
	missing := runStatus(t, bin, repo, []string{"PATH=" + bare + ":/usr/bin:/bin"})
	missing.want(t, "binary.on-shell-path", stateRed, "does not find facet")
	found := runStatus(t, bin, repo, []string{"PATH=" + onPath + ":/usr/bin:/bin"})
	found.want(t, "binary.on-shell-path", stateOK, harnessVersion)
}

// Mutation: skip the spec-version comparison.
func TestScaffoldNamesWhatIsMissingAndWhatIsStale(t *testing.T) {
	bin := versionedFacetBinary(t)
	bare := runStatus(t, bin, statusRepo(t, withRecord(nil)), nil)
	bare.want(t, "scaffold", stateRed, "missing")
	full := withRecord(map[string]string{
		"docs/facets/schema/facet.schema.json": statusSchema,
		"docs/facets/framework/FACET.md":       "# The FACET Framework 0.1.0\n",
		"tools/facet-lint/run-all.sh":          "#!/bin/sh\n",
	})
	ok := runStatus(t, bin, statusRepo(t, full), nil)
	ok.want(t, "scaffold", stateOK, "0.1.0")
	full["docs/facets/framework/FACET.md"] = "# The FACET Framework 1.0.0\n"
	stale := runStatus(t, bin, statusRepo(t, full), nil)
	stale.want(t, "scaffold", stateRed, "1.0.0")
}

// A lint that checked nothing is not green. The adopted-but-empty project has a
// features directory and no record in it.
func TestLintStrictIsRedWhenItCheckedNothing(t *testing.T) {
	bin := facetBinary(t)
	empty := statusRepo(t, map[string]string{"docs/facets/features/.gitkeep": ""})
	// Precondition: the project really is adopted, so zero is the lint's own count.
	if _, err := os.Stat(filepath.Join(empty, "docs/facets/features")); err != nil {
		t.Fatal(err)
	}
	run := runStatus(t, bin, empty, nil)
	run.want(t, "lint.strict", stateRed, "checked 0 records")

	full := runStatus(t, bin, statusRepo(t, withRecord(nil)), nil)
	full.want(t, "lint.strict", stateOK, "1 record(s)")

	broken := statusRepo(t, withRecord(map[string]string{"docs/facets/features/demo/thing/other.yaml": "schema_version: \"0.1\"\nid: demo/thing/mismatch\nlifecycle: planned\n"}))
	run = runStatus(t, bin, broken, nil)
	run.want(t, "lint.strict", stateRed, "error")
}

// A path pattern that selects no tracked file never triggers the workflow.
func TestCITriggerCoverageNamesAPatternThatMatchesNothing(t *testing.T) {
	bin := facetBinary(t)
	workflow := func(paths ...string) string {
		var b strings.Builder
		b.WriteString("name: FACET Lint\non:\n  pull_request:\n    paths:\n")
		for _, p := range paths {
			b.WriteString("      - '" + p + "'\n")
		}
		b.WriteString("  workflow_dispatch:\njobs: {}\n")
		return b.String()
	}
	dead := statusRepo(t, withRecord(map[string]string{
		".github/workflows/facet-lint.yml": workflow("docs/facets/**", "apps/**"),
		"src/main.go":                      "package main\n",
	}))
	run := runStatus(t, bin, dead, nil)
	run.want(t, "ci.trigger-coverage", stateRed, "apps/**")
	if strings.Contains(run.rows["ci.trigger-coverage"].Finding, "docs/facets/**") {
		t.Fatalf("a pattern that matches was named as dead: %s", run.rows["ci.trigger-coverage"].Finding)
	}

	live := statusRepo(t, withRecord(map[string]string{
		".github/workflows/facet-lint.yml": workflow("docs/facets/**", "src/**", "*.md"),
		"src/main.go":                      "package main\n",
		"README.md":                        "# r\n",
	}))
	run = runStatus(t, bin, live, nil)
	run.want(t, "ci.trigger-coverage", stateOK, "all 3")

	none := statusRepo(t, withRecord(nil))
	run = runStatus(t, bin, none, nil)
	run.want(t, "ci.trigger-coverage", stateNotApplicable, "")
}

// An ignore rule that hides a file with an anchor makes lint call it an orphan.
func TestFacetignoreIsRedWhenAPatternHidesAnAnchor(t *testing.T) {
	bin := facetBinary(t)
	anchored := "// @facet: demo/thing/widget\n// @facet-role: primary\npackage x\n"
	hiding := statusRepo(t, withRecord(map[string]string{".facetignore": "*.go\n", "src/x.go": anchored}))
	// Precondition: the file really carries an anchor and really is tracked.
	if out := git(t, hiding, "ls-files", "src/x.go"); !strings.Contains(out, "src/x.go") {
		t.Fatalf("fixture: src/x.go is not tracked: %q", out)
	}
	run := runStatus(t, bin, hiding, nil)
	run.want(t, "facetignore", stateRed, "src/x.go")

	// An anchor naming no record (an example) is hidden on purpose and orphans
	// nothing. Mutation: drop the recordExists check in anchorsHiddenFromTheScan;
	// this case then turns red. Precondition: the example's id has no record.
	example := "// @facet: examples/demo/sample\n// @facet-role: primary\npackage x\n"
	exampleRepo := statusRepo(t, withRecord(map[string]string{".facetignore": "examples/\n", "examples/x.go": example}))
	if _, err := os.Stat(filepath.Join(exampleRepo, "docs/facets/features/examples/demo/sample.yaml")); err == nil {
		t.Fatal("fixture: the example's id must have no record")
	}
	run = runStatus(t, bin, exampleRepo, nil)
	run.want(t, "facetignore", stateOK, "none hides an anchor")

	// A record anchored in scanned code keeps its anchor when a copy in a
	// draft is hidden. Mutation: drop the `!anchored[...]` condition; this
	// case then turns red.
	draft := statusRepo(t, withRecord(map[string]string{".facetignore": "drafts/\n", "src/x.go": anchored, "drafts/x.go": anchored}))
	run = runStatus(t, bin, draft, nil)
	run.want(t, "facetignore", stateOK, "none hides an anchor")

	harmless := statusRepo(t, withRecord(map[string]string{".facetignore": "*.mp3\n", "src/x.go": anchored}))
	run = runStatus(t, bin, harmless, nil)
	run.want(t, "facetignore", stateOK, "none hides an anchor")

	empty := statusRepo(t, withRecord(map[string]string{".facetignore": "# nothing yet\n"}))
	run = runStatus(t, bin, empty, nil)
	run.want(t, "facetignore", stateOK, "no patterns")

	absent := statusRepo(t, withRecord(nil))
	run = runStatus(t, bin, absent, nil)
	run.want(t, "facetignore", stateRed, "no .facetignore")
}

// Mutation: treat a missing taxonomy.yaml as ok.
func TestTaxonomyOkWhenDeclaredRedWhenAbsent(t *testing.T) {
	bin := facetBinary(t)
	with := statusRepo(t, withRecord(map[string]string{"docs/facets/taxonomy.yaml": "schema_version: \"0.1\"\ndomains:\n  demo:\n    title: Demo\n    capabilities:\n      thing:\n        title: Thing\n"}))
	runStatus(t, bin, with, nil).want(t, "taxonomy", stateOK, "1 domain")
	runStatus(t, bin, statusRepo(t, withRecord(nil)), nil).want(t, "taxonomy", stateRed, "no taxonomy.yaml")
}

// Mutation: treat a missing conventions document as ok.
func TestConventionsOkWhenPresentRedWhenAbsent(t *testing.T) {
	bin := facetBinary(t)
	with := statusRepo(t, withRecord(map[string]string{"FACET-CONVENTIONS.md": "# conventions\n"}))
	runStatus(t, bin, with, nil).want(t, "conventions", stateOK, "FACET-CONVENTIONS.md")
	runStatus(t, bin, statusRepo(t, withRecord(nil)), nil).want(t, "conventions", stateRed, "no conventions")
}

// A record on any other edition is red; a vendored schema that accepts
// anything but exactly 0.1 is not a schema this binary can compare against.
func TestSchemaCurrentRequiresTheDeclaredEdition(t *testing.T) {
	bin := facetBinary(t)
	current := statusRepo(t, withRecord(map[string]string{"docs/facets/schema/facet.schema.json": statusSchema}))
	runStatus(t, bin, current, nil).want(t, "schema.current", stateOK, "schema 0.1")
	old := strings.Replace(statusRecord, `"0.1"`, `"1.7"`, 1)
	behind := statusRepo(t, map[string]string{
		"docs/facets/schema/facet.schema.json":        statusSchema,
		"docs/facets/features/demo/thing/widget.yaml": old,
	})
	runStatus(t, bin, behind, nil).want(t, "schema.current", stateRed, "facet.schema.json")
	runStatus(t, bin, statusRepo(t, withRecord(nil)), nil).want(t, "schema.current", stateUnmeasured, "no schema")
	wide := strings.Replace(statusSchema, `["0.1"]`, `["0.1", "1.7"]`, 1)
	if wide == statusSchema {
		t.Fatal("test setup is wrong: statusSchema has no enum [\"0.1\"] to widen")
	}
	widened := statusRepo(t, withRecord(map[string]string{"docs/facets/schema/facet.schema.json": wide}))
	runStatus(t, bin, widened, nil).want(t, "schema.current", stateUnmeasured, "exactly schema_version 0.1")
}

// This binary cannot ask Claude Code whether the plugin is active, so the row
// must never claim it.
func TestHooksPluginIsNeverReportedOk(t *testing.T) {
	bin := facetBinary(t)
	repo := statusRepo(t, withRecord(nil))
	runStatus(t, bin, repo, nil).want(t, "hooks.plugin", stateUnmeasured, "cannot ask")
	runStatus(t, bin, repo, []string{"CLAUDE_PLUGIN_ROOT=" + t.TempDir()}).want(t, "hooks.plugin", stateUnmeasured, "cannot ask")
}

// The servers are started in this process and asked a real handshake.
func TestMCPAndLSPAnswerTheirHandshakes(t *testing.T) {
	run := runStatus(t, facetBinary(t), statusRepo(t, withRecord(nil)), nil)
	run.want(t, "mcp", stateOK, "feature_find")
	run.want(t, "lsp", stateOK, "initialize")
}

// Mutation: match any settings file, not only one naming facet-statusline.
func TestStatuslineMustBeWiredInSettings(t *testing.T) {
	bin := facetBinary(t)
	wired := statusRepo(t, withRecord(map[string]string{".claude/settings.json": `{"statusLine":{"type":"command","command":"facet-statusline"}}`}))
	runStatus(t, bin, wired, nil).want(t, "statusline", stateOK, "facet-statusline is wired")
	other := statusRepo(t, withRecord(map[string]string{".claude/settings.json": `{"statusLine":{"type":"command","command":"date"}}`}))
	runStatus(t, bin, other, nil).want(t, "statusline", stateRed, "neither")
}

// Not installed is not a fault; installed with a binary that is gone is, and
// is what `filter.required` turns into a refused `git add`.
func TestFilterStateFollowsGitConfig(t *testing.T) {
	bin := facetBinary(t)
	repo := statusRepo(t, withRecord(nil))
	runStatus(t, bin, repo, nil).want(t, "filter", stateNotApplicable, "not installed")

	facetRun(t, bin, repo, "filter", "install", "--root", repo, "--command", bin)
	runStatus(t, bin, repo, nil).want(t, "filter", stateOK, "none orphaned")

	gone := statusRepo(t, withRecord(nil))
	facetRun(t, bin, gone, "filter", "install", "--root", gone, "--command", "/nonexistent/facet")
	runStatus(t, bin, gone, nil).want(t, "filter", stateRed, "gone")
}

// A pre-commit hook counts only if git would run it.
func TestGitPrecommitNeedsAHookThatRunsFacetLint(t *testing.T) {
	skipOnWindows(t)
	bin := facetBinary(t)
	none := statusRepo(t, withRecord(nil))
	runStatus(t, bin, none, nil).want(t, "git.precommit", stateRed, "never checked")

	hooked := statusRepo(t, withRecord(nil))
	hook := filepath.Join(hooked, ".git", "hooks", "pre-commit")
	write(t, hook, "#!/bin/sh\nfacet lint --staged\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	runStatus(t, bin, hooked, nil).want(t, "git.precommit", stateOK, "calls facet lint")

	if err := os.Chmod(hook, 0o644); err != nil {
		t.Fatal(err)
	}
	runStatus(t, bin, hooked, nil).want(t, "git.precommit", stateRed, "not executable")

	husky := statusRepo(t, withRecord(map[string]string{".husky/pre-commit": "facet lint\n"}))
	runStatus(t, bin, husky, nil).want(t, "git.precommit", stateOK, ".husky/pre-commit")
}

// A comment that mentions the command checks nothing.
func TestGitPrecommitIgnoresACommentedFacetLint(t *testing.T) {
	skipOnWindows(t)
	bin := facetBinary(t)
	repo := statusRepo(t, withRecord(nil))
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	write(t, hook, "#!/bin/sh\n# facet lint --staged\nexit 0\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	runStatus(t, bin, repo, nil).want(t, "git.precommit", stateRed, "never checked")

	husky := statusRepo(t, withRecord(map[string]string{".husky/pre-commit": "# facet lint\n"}))
	runStatus(t, bin, husky, nil).want(t, "git.precommit", stateRed, "never checked")
}

// Mutation: report red for an embedded bundle (invert the Stat error test).
func TestCoreSetupDoesNotRequireAnUnselectedBrowserApplication(t *testing.T) {
	runStatus(t, facetBinary(t), statusRepo(t, withRecord(nil)), nil).want(t, "ui", stateNotApplicable, "not included")
}

// Mutation: report not-applicable when an embedding provider is configured.
func TestRAGIndexNeedsBuildingOnlyWhenEmbeddingIsConfigured(t *testing.T) {
	bin := facetBinary(t)
	runStatus(t, bin, statusRepo(t, withRecord(nil)), nil).want(t, "rag.index", stateNotApplicable, "no semantic index")
	configured := statusRepo(t, withRecord(map[string]string{".facetrc.yaml": "schema_version: \"0.1\"\nembedding:\n  provider: openai\n  model: m\n"}))
	runStatus(t, bin, configured, nil).want(t, "rag.index", stateRed, "no index was ever built")
}

// Mutation: skip plugins whose State is not ok.
func TestPluginsReportsOnlyWhatVerifies(t *testing.T) {
	bin := facetBinary(t)
	repo := statusRepo(t, withRecord(nil))
	runStatus(t, bin, repo, nil).want(t, "plugins", stateNotApplicable, "no plugin")
	dir := t.TempDir()
	write(t, filepath.Join(dir, "acme-notes", "plugin.json"), `{"not":"a manifest"}`)
	runStatus(t, bin, repo, []string{"FACET_PLUGINS_DIR=" + dir}).want(t, "plugins", stateRed, "acme-notes")
}

// Mutation: report ok with zero runners declared.
func TestTestRunnersAreDeclaredOrAbsent(t *testing.T) {
	bin := facetBinary(t)
	runStatus(t, bin, statusRepo(t, withRecord(nil)), nil).want(t, "test.runners", stateNotApplicable, "no test runner")
	with := statusRepo(t, withRecord(map[string]string{".facetrc.yaml": "schema_version: \"0.1\"\nrunners:\n  go:\n    command: go test ./...\n"}))
	runStatus(t, bin, with, nil).want(t, "test.runners", stateOK, "1 runner")
}
