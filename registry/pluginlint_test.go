// Fixtures here are throwaway plugins and registries written to temp dirs;
// the file implements no feature of its own.
// @facet-ignore-file

package facet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// The sidecar is this test binary itself, started in the "lint" helper mode
// that sidecar_test.go's TestMain dispatches (both files compile into one test
// binary). These two names must match the constants there.
const (
	pluginLintHelperEnv = "FACET_SIDECAR_HELPER"
	pluginLintReplyEnv  = "FACET_LINT_HELPER_REPLY"
)

// pluginLintProject writes a registry with one record and returns it, plus a
// file under rules/ (inside a directory a plugin may declare) and one under
// secret/ (outside every declaration).
func pluginLintProject(t *testing.T) Registry {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docs/facets/features/a/b/c.yaml"),
		"schema_version: \"0.1\"\nid: a/b/c\nlifecycle: planned\n")
	mustWrite(t, filepath.Join(root, "rules/pricing.dmn"), "<definitions/>")
	mustWrite(t, filepath.Join(root, "rules/notes.txt"), "not a declared extension")
	mustWrite(t, filepath.Join(root, "secret/key.txt"), "do not hand out")
	return Open(root)
}

// lintPluginOpts shapes one installed test plugin.
type lintPluginOpts struct {
	reply     string // what helperLint answers; becomes the reply variable
	noGrant   bool   // leave facet.lint out of grants.json
	noDeclare bool   // leave facet.lint out of the manifest's tools
}

// installLintPlugin installs a plugin <pluginsDir>/<id> whose sidecar is this
// test binary, answering facet.lint the way opts.reply says. The binary's
// checksum goes into the manifest and SHA256SUMS like a packed plugin's.
func installLintPlugin(t *testing.T, pluginsDir, id string, opts lintPluginOpts) string {
	t.Helper()
	dir := filepath.Join(pluginsDir, id)
	platform := runtime.GOOS + "-" + runtime.GOARCH
	binName := sidecarFileName("lint-probe", platform)
	sum := copyTestBinary(t, filepath.Join(dir, binName))

	tools := []string{PluginLintTool}
	if opts.noDeclare {
		tools = []string{"probe.other"}
	}
	// The object form: rules/ is readable for .dmn only, so rules/notes.txt
	// sits inside a declared directory with an undeclared extension.
	reads := []any{map[string]any{"dir": "rules", "ext": []string{".dmn"}}}
	manifest := map[string]any{
		"id": id, "name": "Lint Test", "version": "1.0.0", "api": 0,
		"entry": "plugin.js",
		"tools": tools,
		"reads": reads,
		"sidecar": map[string]any{
			"bin":    "lint-probe",
			"env":    []string{pluginLintHelperEnv, pluginLintReplyEnv},
			"sha256": map[string]string{platform: sum},
		},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "plugin.json"), string(raw))
	mustWrite(t, filepath.Join(dir, "plugin.js"), "export default function(){}")
	writePluginSums(t, dir)

	granted := []string{PluginLintTool}
	if opts.noGrant {
		granted = []string{}
	}
	grants, _ := json.Marshal(map[string]any{
		"tools": granted,
		"env":   []string{pluginLintHelperEnv, pluginLintReplyEnv},
	})
	mustWrite(t, filepath.Join(dir, "grants.json"), string(grants))

	t.Setenv(pluginLintHelperEnv, "lint")
	t.Setenv(pluginLintReplyEnv, opts.reply)
	return dir
}

// copyTestBinary copies the running test binary to dst, executable, and
// returns its SHA-256.
func copyTestBinary(t *testing.T, dst string) string {
	t.Helper()
	src, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatalf("opening the test binary: %v", err)
	}
	defer src.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), src); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// writePluginSums writes SHA256SUMS over every file in dir, as `facet plugin
// pack` would. grants.json is written afterwards and so is not listed — the
// host writes it, the package never carries it.
func writePluginSums(t *testing.T, dir string) {
	t.Helper()
	var names []string
	sums := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if rel == "SHA256SUMS" || rel == "grants.json" {
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		h := sha256.Sum256(body)
		sums[rel] = hex.EncodeToString(h[:])
		names = append(names, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "%s  %s\n", sums[n], n)
	}
	mustWrite(t, filepath.Join(dir, "SHA256SUMS"), b.String())
}

// pluginFindings runs the whole lint with the plugin directory set and returns
// the report and only the findings the plugin step produced.
func pluginFindings(t *testing.T, reg Registry, pluginsDir string) (Report, []Finding) {
	t.Helper()
	rep, err := Lint(reg, LintOptions{
		Skip:       map[string]bool{"schema": true, "knowledge": true},
		PluginsDir: pluginsDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	var mine []Finding
	for _, f := range rep.Findings {
		if strings.HasPrefix(f.Check, "plugin-lint") {
			mine = append(mine, f)
		}
	}
	return rep, mine
}

func hasMessage(list []Finding, sev Severity, substr string) bool {
	for _, f := range list {
		if f.Severity == sev && strings.Contains(f.Message, substr) {
			return true
		}
	}
	return false
}

// A plugin's findings reach the report, are attributed to the plugin, keep
// their severity, and an Error from a plugin fails the run like a core Error.
func TestPluginLintRunsAPluginsFindingsIntoTheReport(t *testing.T) {
	reg := pluginLintProject(t)
	plugins := t.TempDir()
	installLintPlugin(t, plugins, "dmn", lintPluginOpts{reply: "findings"})

	rep, mine := pluginFindings(t, reg, plugins)
	if !hasMessage(mine, SeverityError, "[plugin dmn] gap: row 3 has no case") {
		t.Fatalf("the plugin's Error did not arrive: %+v", mine)
	}
	if !hasMessage(mine, SeverityWarning, "[plugin dmn] overlap: rows 1 and 2 overlap") {
		t.Fatalf("the plugin's Warning did not arrive: %+v", mine)
	}
	for _, f := range mine {
		if f.Check != "plugin-lint:dmn" {
			t.Fatalf("a finding is not attributed to the plugin: %+v", f)
		}
		if strings.Contains(f.Message, "row 3") && f.ID != "a/b/c" {
			t.Fatalf("the finding lost its record id: %+v", f)
		}
	}
	if !rep.Failed() {
		t.Fatal("a plugin Error did not fail the run")
	}
}

// A plugin's Warning is visible and does not fail the run.
func TestPluginLintWarningDoesNotFailTheRun(t *testing.T) {
	reg := pluginLintProject(t)
	plugins := t.TempDir()
	installLintPlugin(t, plugins, "dmn", lintPluginOpts{reply: "warning"})

	rep, mine := pluginFindings(t, reg, plugins)
	if !hasMessage(mine, SeverityWarning, "only a warning") {
		t.Fatalf("the Warning did not arrive: %+v", mine)
	}
	if rep.Failed() {
		t.Fatalf("a plugin Warning failed the run: %+v", mine)
	}
}

// The sidecar gets the records and exactly the files under its declared reads
// directories with their declared extensions — never a file outside them, and
// never one repo.read would refuse.
func TestPluginLintHandsOverRecordsAndOnlyTheDeclaredFiles(t *testing.T) {
	reg := pluginLintProject(t)
	// Precondition: both files exist, so an absence below is a refusal and
	// not a missing fixture.
	for _, p := range []string{"rules/pricing.dmn", "secret/key.txt"} {
		if _, err := os.Stat(filepath.Join(reg.Root, p)); err != nil {
			t.Fatalf("fixture missing %s: %v", p, err)
		}
	}
	plugins := t.TempDir()
	installLintPlugin(t, plugins, "dmn", lintPluginOpts{reply: "files"})

	_, mine := pluginFindings(t, reg, plugins)
	if !hasMessage(mine, SeverityWarning, "rules/pricing.dmn") {
		t.Fatalf("the declared file was not handed over: %+v", mine)
	}
	if hasMessage(mine, SeverityWarning, "secret/key.txt") {
		t.Fatalf("a file outside reads was handed over: %+v", mine)
	}
	if hasMessage(mine, SeverityWarning, "rules/notes.txt") {
		t.Fatalf("a file with an undeclared extension was handed over: %+v", mine)
	}
	if !hasMessage(mine, SeverityWarning, "records=1") {
		t.Fatalf("the records were not handed over: %+v", mine)
	}
}

// A sidecar that does not answer in time is an Error of that plugin, never a
// clean run.
func TestPluginLintTimeoutIsAnErrorOfThePlugin(t *testing.T) {
	old := pluginLintTimeout
	pluginLintTimeout = 500 * time.Millisecond
	t.Cleanup(func() { pluginLintTimeout = old })

	reg := pluginLintProject(t)
	plugins := t.TempDir()
	installLintPlugin(t, plugins, "dmn", lintPluginOpts{reply: "hang"})

	start := time.Now()
	rep, mine := pluginFindings(t, reg, plugins)
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("the timeout did not bound the call: %s", elapsed)
	}
	if !hasMessage(mine, SeverityError, "[plugin dmn]") {
		t.Fatalf("a hanging sidecar produced no Error: %+v", mine)
	}
	if !rep.Failed() {
		t.Fatal("a hanging sidecar did not fail the run")
	}
}

// Every way a call can go wrong is an Error, never silence: a reply with an
// error, a result of the wrong shape, a result without findings.
func TestPluginLintFailureModesAreErrors(t *testing.T) {
	for _, reply := range []string{"error", "nonsense", "nofind"} {
		t.Run(reply, func(t *testing.T) {
			reg := pluginLintProject(t)
			plugins := t.TempDir()
			installLintPlugin(t, plugins, "dmn", lintPluginOpts{reply: reply})
			rep, mine := pluginFindings(t, reg, plugins)
			if !hasMessage(mine, SeverityError, "[plugin dmn]") {
				t.Fatalf("%s produced no Error: %+v", reply, mine)
			}
			if !rep.Failed() {
				t.Fatalf("%s did not fail the run", reply)
			}
		})
	}
}

// An empty findings list is a clean run: no plugin finding, no failure.
// The counterpart that keeps the failure-mode cases honest — an
// implementation that turned every reply into an Error would pass those.
func TestPluginLintAnEmptyAnswerIsACleanRun(t *testing.T) {
	reg := pluginLintProject(t)
	plugins := t.TempDir()
	installLintPlugin(t, plugins, "dmn", lintPluginOpts{reply: "empty"})

	rep, mine := pluginFindings(t, reg, plugins)
	if len(mine) != 0 {
		t.Fatalf("an empty answer produced findings: %+v", mine)
	}
	if rep.Failed() {
		t.Fatal("an empty answer failed the run")
	}
}

// A sidecar binary that no longer matches its checksum never runs, and the
// plugin's lint is reported as an Error rather than skipped.
func TestPluginLintRefusesABinaryThatFailsItsSum(t *testing.T) {
	reg := pluginLintProject(t)
	plugins := t.TempDir()
	dir := installLintPlugin(t, plugins, "dmn", lintPluginOpts{reply: "findings"})

	// Precondition: untouched, the plugin runs and reports.
	if _, mine := pluginFindings(t, reg, plugins); !hasMessage(mine, SeverityError, "row 3 has no case") {
		t.Fatalf("precondition: the untouched plugin did not run: %+v", mine)
	}

	// Replace the packed binary rather than rewriting an executable inode:
	// the OS may still hold that inode busy after the helper exits.
	bin := filepath.Join(dir, sidecarFileName("lint-probe", runtime.GOOS+"-"+runtime.GOARCH))
	body, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bin + ".tampered"
	if err := os.WriteFile(tampered, append(body, []byte("tampered")...), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tampered, bin); err != nil {
		t.Fatal(err)
	}

	rep, mine := pluginFindings(t, reg, plugins)
	if hasMessage(mine, SeverityError, "row 3 has no case") {
		t.Fatal("a tampered binary ran")
	}
	if !hasMessage(mine, SeverityError, "does not verify") {
		t.Fatalf("a tampered binary was not reported as not verifying: %+v", mine)
	}
	if !rep.Failed() {
		t.Fatal("a tampered binary did not fail the run")
	}
}

// A plugin that declares facet.lint but was not granted it is a Warning that
// says how to grant it — the user's decision, not a broken plugin, and not
// silence either.
func TestPluginLintAnUngrantedToolIsAWarning(t *testing.T) {
	reg := pluginLintProject(t)
	plugins := t.TempDir()
	installLintPlugin(t, plugins, "dmn", lintPluginOpts{reply: "findings", noGrant: true})

	rep, mine := pluginFindings(t, reg, plugins)
	if hasMessage(mine, SeverityError, "row 3 has no case") {
		t.Fatal("an ungranted lint tool ran")
	}
	if !hasMessage(mine, SeverityWarning, "--grant tools=facet.lint") {
		t.Fatalf("an ungranted lint tool was not said: %+v", mine)
	}
	if rep.Failed() {
		t.Fatal("an ungranted lint tool failed the run")
	}
}

// With no plugin that declares facet.lint, `facet lint` reports exactly what
// it reported without a plugins directory — the opt-in changes nothing for
// projects that never use it.
func TestPluginLintWithNoLintingPluginChangesNothing(t *testing.T) {
	reg := pluginLintProject(t)
	plugins := t.TempDir()
	installLintPlugin(t, plugins, "other", lintPluginOpts{reply: "findings", noDeclare: true})

	// Precondition: the plugin is installed and verifies, so its absence from
	// the findings is a decision and not a broken fixture.
	installed, err := InstalledPlugins(plugins)
	if err != nil || len(installed) != 1 || installed[0].VerifyError != nil {
		t.Fatalf("precondition: one verifying plugin, got %+v, %v", installed, err)
	}

	with, _ := pluginFindings(t, reg, plugins)
	without, _ := pluginFindings(t, reg, "")
	absent, _ := pluginFindings(t, reg, filepath.Join(t.TempDir(), "nope"))
	if !reflect.DeepEqual(with.Findings, without.Findings) {
		t.Fatalf("a non-linting plugin changed the report:\nwith    %+v\nwithout %+v", with.Findings, without.Findings)
	}
	if !reflect.DeepEqual(absent.Findings, without.Findings) {
		t.Fatalf("an absent plugins directory changed the report: %+v", absent.Findings)
	}
}
