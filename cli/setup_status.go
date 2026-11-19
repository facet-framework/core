package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/facet-framework/core/registry"
	"github.com/facet-framework/core/release"
	"github.com/goccy/go-yaml"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"slices"
)

func init() {
	setupSubcommands = append(setupSubcommands, newSetupLinkCmd)
}

// checkState is the answer to one question about a project's setup.
type checkState string

const (
	stateOK            checkState = "ok"
	stateRed           checkState = "red"
	stateUnmeasured    checkState = "unmeasured"
	stateNotApplicable checkState = "not-applicable"
)

// statusRow is one line of `facet setup status`: what was asked, the answer,
// one sentence about it, and what was actually run or read to get there.
type statusRow struct {
	ID       string     `json:"id"`
	State    checkState `json:"state"`
	Finding  string     `json:"finding"`
	Evidence string     `json:"evidence"`
}

func row(id string, state checkState, finding, evidence string) statusRow {
	return statusRow{ID: id, State: state, Finding: oneLine(finding), Evidence: oneLine(evidence)}
}

// oneLine keeps a table row on one line however the message was built.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

// setupRedError makes `--strict` fail the command without a stack of its own.
type setupRedError struct{ red int }

func (e setupRedError) Error() string {
	return fmt.Sprintf("%d setup check(s) are red", e.red)
}
func (e setupRedError) ExitCode() int { return 1 }

// statusEnv is everything the checks share: the project as it was found, and
// the lists several of them need (read once, on first use).
type statusEnv struct {
	readerAssets fs.FS
	version      string
	root         string
	home         string

	cfg    *facet.Config
	cfgErr error
	// regs holds the registries that exist on disk. A project that declares
	// one in .facetrc.yaml but has not created it yet has none here.
	regs []facet.Registry

	trackedDone bool
	tracked     []string
	trackedErr  error
}

func newStatusEnv(root string) *statusEnv {
	e := &statusEnv{root: root, version: "dev"}
	e.home, _ = os.UserHomeDir()
	e.cfg, e.cfgErr = facet.LoadConfig(root)
	all := []facet.Registry{facet.Open(root)}
	if e.cfgErr == nil {
		all = e.cfg.All()
	}
	for _, reg := range all {
		if reg.Adopted() {
			e.regs = append(e.regs, reg)
		}
	}
	return e
}

// trackedFiles lists what git tracks, relative to the root. Tracked rather
// than present: a file git does not know is not part of the project a CI
// trigger or an ignore rule has to account for.
func (e *statusEnv) trackedFiles() ([]string, error) {
	if e.trackedDone {
		return e.tracked, e.trackedErr
	}
	e.trackedDone = true
	out, err := exec.Command("git", "-C", e.root, "ls-files", "-z").Output()
	if err != nil {
		e.trackedErr = fmt.Errorf("git ls-files in %s failed: %w", e.root, err)
		return nil, e.trackedErr
	}
	for _, rel := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		if rel != "" {
			e.tracked = append(e.tracked, rel)
		}
	}
	return e.tracked, nil
}

// setupChecks is the report, in the order the setup walks it.
var setupChecks = []func(*statusEnv) statusRow{
	checkBinaryResolved,
	checkBinaryCurrentPointer,
	checkBinaryOnShellPath,
	checkScaffold,
	checkLintStrict,
	checkCITriggerCoverage,
	checkFacetignore,
	checkTaxonomy,
	checkConventions,
	checkSchemaCurrent,
	checkHooksPlugin,
	checkMCP,
	checkLSP,
	checkStatusline,
	checkFilter,
	checkGitPrecommit,
	checkUI,
	checkRAGIndex,
	checkPlugins,
	checkTestRunners,
}

// @facet: delivery/distribution/setup-status
// @facet-role: primary
func newSetupStatusCmd(readerAssets fs.FS, version string) *cobra.Command {
	var asJSON, strict, withEvidence bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report, check by check, how far this project's FACET setup goes",
		Long: `Measures the setup instead of describing it. One row per check, each with
a state, one sentence, and the evidence — what was run or read to get there.

  ok              measured, and as it should be
  red             measured, and wrong or missing
  unmeasured      could not be measured here; the row says why
  not-applicable  the thing is not in use, which is not a fault

This is a report: the exit code is 0 even with red rows. --strict turns any red
row into exit 1. unmeasured never counts as ok, and never as red.

Nothing is written. The checks that start something (the MCP and LSP servers)
do so in this process, against the project, and stop again.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			env := newStatusEnv(ResolveRoot(cmd))
			env.readerAssets, env.version = readerAssets, version
			rows := make([]statusRow, 0, len(setupChecks))
			red := 0
			for _, check := range withProductSetupChecks(setupChecks) {
				r := check(env)
				if r.State == stateRed {
					red++
				}
				rows = append(rows, r)
			}
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(rows); err != nil {
					return err
				}
			} else {
				printStatusTable(out, rows, withEvidence)
			}
			if strict && red > 0 {
				return setupRedError{red: red}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the rows as a JSON array of {id, state, finding, evidence}")
	cmd.Flags().BoolVar(&strict, "strict", false, "exit 1 when any row is red")
	cmd.Flags().BoolVar(&withEvidence, "evidence", false, "print each row's evidence under it")
	return cmd
}

func printStatusTable(w io.Writer, rows []statusRow, withEvidence bool) {
	counts := map[checkState]int{}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, r := range rows {
		counts[r.State]++
		fmt.Fprintf(tw, "%s\t%s\t%s\n", r.ID, r.State, r.Finding)
		if withEvidence {
			fmt.Fprintf(tw, "\t\t  evidence: %s\n", r.Evidence)
		}
	}
	tw.Flush()
	fmt.Fprintf(w, "\n%d ok, %d red, %d unmeasured, %d not-applicable\n",
		counts[stateOK], counts[stateRed], counts[stateUnmeasured], counts[stateNotApplicable])
}

// ---------------------------------------------------------------------------
// several registries

type regResult struct {
	state             checkState
	finding, evidence string
}

var stateRank = map[checkState]int{stateNotApplicable: 0, stateOK: 1, stateUnmeasured: 2, stateRed: 3}

// overRegistries asks one question of every registry and folds the answers
// into one row: the worst state wins, and with several registries each
// finding says whose it is.
func overRegistries(e *statusEnv, id string, noRegistry regResult, ask func(facet.Registry) regResult) statusRow {
	if len(e.regs) == 0 {
		return row(id, noRegistry.state, noRegistry.finding, noRegistry.evidence)
	}
	state := stateNotApplicable
	var findings, evidence []string
	for _, reg := range e.regs {
		r := ask(reg)
		if stateRank[r.state] > stateRank[state] {
			state = r.state
		}
		prefix := ""
		if len(e.regs) > 1 {
			prefix = registryName(reg) + ": "
		}
		findings = append(findings, prefix+r.finding)
		evidence = append(evidence, prefix+r.evidence)
	}
	return row(id, state, strings.Join(findings, "; "), strings.Join(evidence, "; "))
}

var noRegistryHere = regResult{
	state:    stateNotApplicable,
	finding:  "no registry here",
	evidence: "no docs/facets/features (or the records directory .facetrc.yaml names) exists",
}

// ---------------------------------------------------------------------------
// binary

func unknownBuildVersion(version string) bool {
	return version == "" || version == "dev" || strings.Contains(version, "-dev.unknown")
}

func checkBinaryResolved(e *statusEnv) statusRow {
	const id = "binary.resolved"
	exe, err := os.Executable()
	if err != nil {
		return row(id, stateUnmeasured, "cannot tell which binary is running", "os.Executable(): "+err.Error())
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	origin := "outside the dispatcher's cache (a source build or a copy; the binary cannot tell which)"
	if dist := facetDistDir(); dist != "" && strings.HasPrefix(exe, dist+string(filepath.Separator)) {
		origin = "the dispatcher's cache"
	}
	evidence := "os.Executable() = " + exe + "; the checksum is verified by bin/facet when it downloads, and this binary keeps no record of that, so it is not claimed here"
	if unknownBuildVersion(e.version) {
		return row(id, stateUnmeasured, "this binary carries no version (built without -ldflags -X main.version)", evidence)
	}
	return row(id, stateOK, fmt.Sprintf("facet %s, running from %s", e.version, origin), evidence)
}

// checkBinaryCurrentPointer compares the install behind the dispatcher's
// version-free `selected` pointer with the running binary. The version is read
// from that install's install.json, which the dispatcher writes last, because
// a development build lives in a directory named `dev`, not after its version.
func checkBinaryCurrentPointer(e *statusEnv) statusRow {
	const id = "binary.current-pointer"
	dist := facetDistDir()
	if dist == "" {
		return row(id, stateUnmeasured, "no cache directory to look in", "neither CLAUDE_PLUGIN_DATA, XDG_CACHE_HOME nor a home directory is available")
	}
	selected := filepath.Join(dist, "selected")
	evidence := "Lstat on " + selected + ", the install it resolves to, and that install's install.json"
	st, err := os.Lstat(selected)
	if err != nil {
		return row(id, stateRed, "there is no selected pointer; the dispatcher creates it the first time it resolves from its cache", evidence)
	}
	if st.Mode()&os.ModeSymlink == 0 {
		return row(id, stateRed, selected+" exists but is not a symlink", evidence)
	}
	install, err := filepath.EvalSymlinks(selected)
	if err != nil {
		return row(id, stateRed, "selected points at nothing: "+err.Error(), evidence)
	}
	raw, err := os.ReadFile(filepath.Join(install, "install.json"))
	if err != nil {
		return row(id, stateRed, "selected points at "+install+", which holds no install.json, so it is not a complete install", evidence)
	}
	var meta struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil || meta.Version == "" {
		return row(id, stateRed, "the install.json in "+install+" names no version", evidence)
	}
	if unknownBuildVersion(e.version) {
		return row(id, stateUnmeasured, "points at "+install+" ("+meta.Version+"), but this binary has no version to compare it with", evidence)
	}
	if strings.TrimPrefix(meta.Version, "v") != strings.TrimPrefix(e.version, "v") {
		return row(id, stateRed, fmt.Sprintf("selected points at %s (%s), not at this binary's version %s", install, meta.Version, e.version), evidence)
	}
	if _, err := currentPointerBinary(); err != nil {
		return row(id, stateRed, "selected points at the right version but holds no binary: "+err.Error(), evidence)
	}
	return row(id, stateOK, "selected points at "+install+" ("+meta.Version+")", evidence)
}

func checkBinaryOnShellPath(e *statusEnv) statusRow {
	const id = "binary.on-shell-path"
	if setupGOOS == "windows" {
		return row(id, stateUnmeasured, "Windows has no login shell to ask", "probe is `$SHELL -lc 'command -v facet'`")
	}
	shell := detectLoginShell()
	if shell.Path == "" {
		return row(id, stateUnmeasured, "$SHELL is not set, so there is no login shell to ask", "probe is `$SHELL -lc 'command -v facet'`")
	}
	if shell.Name == "pwsh" || shell.Name == "powershell" {
		return row(id, stateUnmeasured, shell.Name+" has no `-lc` form to ask with", "probe is `$SHELL -lc 'command -v facet'`")
	}
	evidence := fmt.Sprintf("%s -lc 'command -v facet' (timeout %s)", shell.Path, shellProbeTimeout)
	out, timedOut, _ := runLoginShell(shell.Path, "command -v facet", shellProbeTimeout)
	if timedOut {
		return row(id, stateUnmeasured, "the login shell did not answer in time (a slow or interactive profile?)", evidence)
	}
	found := lastOutputLine(out)
	if found == "" {
		return row(id, stateRed, "your login shell does not find facet; a terminal, an editor or a git hook will not either", evidence)
	}
	if !filepath.IsAbs(found) {
		return row(id, stateUnmeasured, "your login shell resolves facet to "+found+", which is not a file path", evidence)
	}

	realPath := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	foundReal := realPath(found)
	if exe, err := os.Executable(); err == nil && foundReal == realPath(exe) {
		return row(id, stateOK, "your login shell finds this binary at "+found, evidence)
	}
	if pointer, err := currentPointerBinary(); err == nil && foundReal == realPath(pointer) {
		return row(id, stateOK, "your login shell finds the current binary at "+found, evidence)
	}
	// A different file can still be the same release. Ask it.
	if !unknownBuildVersion(e.version) {
		ctx, cancel := context.WithTimeout(context.Background(), shellProbeTimeout)
		defer cancel()
		said, _ := exec.CommandContext(ctx, found, "--version").Output()
		if strings.Contains(string(said), strings.TrimPrefix(e.version, "v")) {
			return row(id, stateOK, fmt.Sprintf("your login shell finds %s, a different file reporting %s", found, e.version), evidence+"; "+found+" --version")
		}
		return row(id, stateRed, fmt.Sprintf("your login shell finds a different facet at %s, which does not report %s", found, e.version), evidence+"; "+found+" --version")
	}
	return row(id, stateUnmeasured, "your login shell finds "+found+", but this binary has no version to compare it with", evidence)
}

// ---------------------------------------------------------------------------
// scaffold and lint

var specHeaderRe = regexp.MustCompile(`(?m)^# The FACET Framework (\S+)`)

func checkScaffold(e *statusEnv) statusRow {
	const id = "scaffold"
	return overRegistries(e, id,
		regResult{stateRed, "FACET is not scaffolded here: no records directory", "looked for docs/facets/features and the directory .facetrc.yaml names"},
		func(reg facet.Registry) regResult {
			var missing []string
			schema := filepath.Join(reg.SchemaDir, "facet.schema.json")
			if !isRegularFile(schema) {
				missing = append(missing, "schema ("+reg.Rel(schema)+")")
			}
			specs := []string{
				filepath.Join(reg.RecordsDir, "framework", "FACET.md"),
				filepath.Join(reg.Root, "docs", "framework", "FACET.md"),
			}
			spec := ""
			for _, s := range specs {
				if isRegularFile(s) {
					spec = s
					break
				}
			}
			if spec == "" {
				missing = append(missing, "spec ("+reg.Rel(specs[0])+")")
			}
			lintSuite := filepath.Join(reg.Root, "tools", "facet-lint", "run-all.sh")
			if !isRegularFile(lintSuite) {
				missing = append(missing, "lint suite (tools/facet-lint/run-all.sh)")
			}
			evidence := "stat of the schema, the spec and the lint suite; the spec header compared with this Core build's declared specification compatibility"
			if len(missing) > 0 {
				return regResult{stateRed, "missing: " + strings.Join(missing, ", "), evidence}
			}
			data, err := os.ReadFile(spec)
			m := specHeaderRe.FindSubmatch(data)
			if err != nil || m == nil {
				return regResult{stateUnmeasured, "the spec at " + reg.Rel(spec) + " has no readable version header", evidence}
			}
			supported := release.Supported().CompatibleSpecifications
			if !slices.Contains(supported, string(m[1])) {
				return regResult{stateRed, fmt.Sprintf("the vendored spec is %s; this Core supports %s", m[1], strings.Join(supported, ", ")), evidence}
			}
			return regResult{stateOK, "schema, spec and lint suite present, spec " + string(m[1]), evidence}
		})
}

func isRegularFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// checkLintStrict runs the lint in this process. A lint that checked no record
// has not passed anything, so zero records is red, not ok — it is the way a
// lint goes green without looking: a trigger, a path or an ignore rule that
// leaves it nothing to read.
func checkLintStrict(e *statusEnv) statusRow {
	const id = "lint.strict"
	if len(e.regs) == 0 {
		return row(id, stateRed, "nothing to lint: no registry here", "looked for docs/facets/features and the directory .facetrc.yaml names")
	}
	opts := facet.LintOptions{
		Scan:       facet.ScanOptionsFromEnv(),
		Skip:       map[string]bool{},
		Today:      time.Now().UTC().Format("2006-01-02"),
		PluginsDir: PluginsDir(),
	}
	report, err := facet.LintAll(e.root, opts)
	evidence := "facet.LintAll(" + e.root + ") in this process, every check on"
	if err != nil {
		return row(id, stateRed, "the lint could not run: "+err.Error(), evidence)
	}
	errs := 0
	for _, f := range report.Findings {
		if f.Severity == facet.SeverityError {
			errs++
		}
	}
	checked := fmt.Sprintf("%d record(s), %d anchor(s) checked", report.Checked.Records, report.Checked.Anchors)
	switch {
	case report.Checked.Records == 0:
		return row(id, stateRed, "the lint checked 0 records — a green lint that read nothing proves nothing", evidence)
	case errs > 0:
		return row(id, stateRed, fmt.Sprintf("%d error(s); %s", errs, checked), evidence)
	}
	return row(id, stateOK, fmt.Sprintf("no errors; %s", checked), evidence)
}

// ---------------------------------------------------------------------------
// CI trigger

// workflowGlob turns a GitHub Actions path pattern into a regular expression.
// `*` stops at a slash, `**` does not, and `?` and `+` act on the character
// before them — the workflow syntax, not the shell's.
func workflowGlob(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?', '+':
			if i > 0 {
				b.WriteByte(c)
			} else {
				b.WriteString(regexp.QuoteMeta(string(c)))
			}
		case '[':
			if end := strings.IndexByte(pattern[i:], ']'); end > 0 {
				b.WriteString(pattern[i : i+end+1])
				i += end
			} else {
				b.WriteString(`\[`)
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// countPatternMatches says how many of the files a path pattern selects.
// Zero is the answer this check exists to give: a trigger that selects nothing
// never runs the workflow and never says so.
func countPatternMatches(pattern string, files []string) int {
	re, err := workflowGlob(pattern)
	if err != nil {
		return 0
	}
	n := 0
	for _, f := range files {
		if re.MatchString(f) {
			n++
		}
	}
	return n
}

// workflowPathPatterns collects every `paths:` pattern under `on:`, in the
// order written, and says whether any event filters by path at all.
func workflowPathPatterns(on any) (patterns []string, filtered bool) {
	events, ok := on.(map[string]any)
	if !ok {
		return nil, false
	}
	names := make([]string, 0, len(events))
	for name := range events {
		names = append(names, name)
	}
	sort.Strings(names)
	seen := map[string]bool{}
	for _, name := range names {
		event, ok := events[name].(map[string]any)
		if !ok {
			continue
		}
		list, ok := event["paths"].([]any)
		if !ok {
			continue
		}
		filtered = true
		for _, item := range list {
			if p, ok := item.(string); ok && !seen[p] {
				seen[p] = true
				patterns = append(patterns, p)
			}
		}
	}
	return patterns, filtered
}

func checkCITriggerCoverage(e *statusEnv) statusRow {
	const id = "ci.trigger-coverage"
	path := filepath.Join(e.root, ".github", "workflows", "facet-lint.yml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return row(id, stateNotApplicable, "no .github/workflows/facet-lint.yml: CI is not set up here, or lints elsewhere", "looked for "+path)
	}
	if err != nil {
		return row(id, stateUnmeasured, "cannot read the workflow: "+err.Error(), "read "+path)
	}
	var wf struct {
		On any `yaml:"on"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return row(id, stateRed, "the workflow does not parse: "+err.Error(), "parsed "+path)
	}
	patterns, filtered := workflowPathPatterns(wf.On)
	if !filtered {
		return row(id, stateOK, "the workflow has no paths: filter, so it runs on every matching event", "parsed on: in "+path)
	}
	files, err := e.trackedFiles()
	if err != nil {
		return row(id, stateUnmeasured, "cannot count matches without git's file list: "+err.Error(), "git ls-files")
	}
	evidence := fmt.Sprintf("parsed on.*.paths in %s and matched each pattern against the %d file(s) `git ls-files` lists", path, len(files))
	var dead []string
	counted := 0
	for _, p := range patterns {
		if strings.HasPrefix(p, "!") {
			continue // an exclusion selects nothing by design
		}
		counted++
		if countPatternMatches(p, files) == 0 {
			dead = append(dead, p)
		}
	}
	if len(dead) > 0 {
		return row(id, stateRed, fmt.Sprintf("%d of %d pattern(s) match no tracked file, so they never trigger the workflow: %s", len(dead), counted, strings.Join(dead, ", ")), evidence)
	}
	return row(id, stateOK, fmt.Sprintf("all %d pattern(s) match at least one tracked file", counted), evidence)
}

// ---------------------------------------------------------------------------
// .facetignore, taxonomy, conventions, schema

// maxHiddenAnchorRead bounds the files read to look for an anchor the ignore
// rules hide. A file this large is not source that carries one.
const maxHiddenAnchorRead = 2 << 20

// anchorsHiddenFromTheScan names tracked files that carry an @facet: anchor
// and that the scan never opens — the ones .facetignore (or .gitignore)
// excludes. The scan sees the walked set; everything git tracks and the walk
// skipped is read directly.
func anchorsHiddenFromTheScan(e *statusEnv) ([]string, error) {
	files, err := e.trackedFiles()
	if err != nil {
		return nil, err
	}
	visible, walked, err := facet.ScanWithFiles(e.root, facet.ScanOptions{})
	if err != nil {
		return nil, err
	}
	// Ids the scan already anchors somewhere it reads. Hiding a second anchor
	// for one of them orphans nothing.
	anchored := make(map[string]bool, len(visible))
	for _, a := range visible {
		anchored[stripRegistry(a.ID)] = true
	}
	seen := make(map[string]bool, len(walked))
	for _, w := range walked {
		seen[filepath.Clean(w.Path)] = true
	}
	var hidden []string
	for _, rel := range files {
		abs := filepath.Join(e.root, filepath.FromSlash(rel))
		if seen[abs] {
			continue
		}
		st, err := os.Lstat(abs)
		if err != nil || !st.Mode().IsRegular() || st.Size() > maxHiddenAnchorRead {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		for _, a := range facet.ScanContent(abs, data) {

			if e.recordExists(a.ID) && !anchored[stripRegistry(a.ID)] {
				hidden = append(hidden, fmt.Sprintf("%s (%s)", rel, a.ID))
				break
			}
		}
	}
	return hidden, nil
}

// stripRegistry drops a 'registry:' qualifier from an anchor id.
func stripRegistry(id string) string {
	if i := strings.Index(id, ":"); i >= 0 {
		return id[i+1:]
	}
	return id
}

// recordExists reports whether any registry here holds a record for id. A
// registry-qualified id ('product:billing/x/y') is looked up in that
// registry only.
func (e *statusEnv) recordExists(id string) bool {
	name := ""
	if i := strings.Index(id, ":"); i >= 0 {
		name, id = id[:i], id[i+1:]
	}
	for _, reg := range e.regs {
		if name != "" && reg.Name != name {
			continue
		}
		if isRegularFile(reg.PathForID(id)) {
			return true
		}
	}
	return false
}

func checkFacetignore(e *statusEnv) statusRow {
	const id = "facetignore"
	path := filepath.Join(e.root, facet.IgnoreFile)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return row(id, stateRed, "no .facetignore; the scaffold ships one", "looked for "+path)
	}
	if err != nil {
		return row(id, stateUnmeasured, "cannot read .facetignore: "+err.Error(), "read "+path)
	}
	patterns := 0
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			patterns++
		}
	}
	if patterns == 0 {
		return row(id, stateOK, "present with no patterns, so it hides nothing", "read "+path)
	}
	hidden, err := anchorsHiddenFromTheScan(e)
	evidence := fmt.Sprintf("read %s (%d pattern(s)); read every tracked file the scan skipped, up to %d bytes each, for an @facet: anchor", path, patterns, maxHiddenAnchorRead)
	if err != nil {
		return row(id, stateUnmeasured, "cannot tell what the patterns hide: "+err.Error(), evidence)
	}
	if len(hidden) > 0 {
		return row(id, stateRed, fmt.Sprintf("%d tracked file(s) carry an anchor the scan never reads, so lint reports it orphaned: %s", len(hidden), strings.Join(hidden, ", ")), evidence)
	}
	return row(id, stateOK, fmt.Sprintf("%d pattern(s), none hides an anchor", patterns), evidence)
}

func checkTaxonomy(e *statusEnv) statusRow {
	return overRegistries(e, "taxonomy", noRegistryHere, func(reg facet.Registry) regResult {
		path := reg.TaxonomyPath()
		evidence := "read " + path
		if !isRegularFile(path) {
			return regResult{stateRed, "no taxonomy.yaml; ids are unconstrained until one is declared (declining is a choice this check cannot see)", evidence}
		}
		t, err := facet.LoadTaxonomy(path)
		if err != nil {
			return regResult{stateRed, "taxonomy.yaml does not parse: " + err.Error(), evidence}
		}
		if !t.Configured() {
			return regResult{stateRed, "taxonomy.yaml declares no domains", evidence}
		}
		return regResult{stateOK, fmt.Sprintf("%d domain(s) declared", len(t.DomainNames())), evidence}
	})
}

func checkConventions(e *statusEnv) statusRow {
	return overRegistries(e, "conventions", noRegistryHere, func(reg facet.Registry) regResult {
		evidence := "reg.ConventionsPath, resolved by registry, project, then FACET-CONVENTIONS.md at the root"
		if reg.ConventionsPath == "" {
			return regResult{stateRed, "no conventions document (declining is a choice this check cannot see)", evidence}
		}
		if !isRegularFile(reg.ConventionsPath) {
			return regResult{stateRed, fmt.Sprintf("%s is declared (%s) but does not exist", reg.Rel(reg.ConventionsPath), reg.ConventionsSource), evidence}
		}
		return regResult{stateOK, fmt.Sprintf("%s (%s)", reg.Rel(reg.ConventionsPath), reg.ConventionsSource), evidence}
	})
}

// currentSchemaVersion reads the one record edition the vendored schema accepts.
func currentSchemaVersion(schemaDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(schemaDir, "facet.schema.json"))
	if err != nil {
		return "", err
	}
	var s struct {
		Properties struct {
			SchemaVersion struct {
				Enum []string `json:"enum"`
			} `json:"schema_version"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return "", err
	}
	versions := s.Properties.SchemaVersion.Enum
	if len(versions) != 1 || versions[0] != "0.1" {
		return "", fmt.Errorf("the schema must accept exactly schema_version 0.1")
	}
	return versions[0], nil
}

func checkSchemaCurrent(e *statusEnv) statusRow {
	return overRegistries(e, "schema.current", noRegistryHere, func(reg facet.Registry) regResult {
		newest, err := currentSchemaVersion(reg.SchemaDir)
		evidence := "schema_version of every record compared with the edition the vendored facet.schema.json accepts"
		if err != nil {
			return regResult{stateUnmeasured, "no schema to compare against: " + err.Error(), evidence}
		}
		records, err := reg.LoadFeatures()
		if err != nil {
			return regResult{stateUnmeasured, "cannot load the records: " + err.Error(), evidence}
		}
		if len(records) == 0 {
			return regResult{stateNotApplicable, "no records yet", evidence}
		}
		behind := 0
		for _, rec := range records {
			if rec.SchemaVersion != newest {
				behind++
			}
		}
		if behind > 0 {
			return regResult{stateRed, fmt.Sprintf("%d of %d record(s) are not at schema %s — review those records against %s", behind, len(records), newest, reg.Rel(filepath.Join(reg.SchemaDir, "facet.schema.json"))), evidence}
		}
		return regResult{stateOK, fmt.Sprintf("all %d record(s) at schema %s", len(records), newest), evidence}
	})
}

// ---------------------------------------------------------------------------
// agent layer

func checkHooksPlugin(e *statusEnv) statusRow {
	root := os.Getenv("CLAUDE_PLUGIN_ROOT")
	if root == "" {
		root = "unset"
	}
	return row("hooks.plugin", stateUnmeasured,
		"whether the plugin is active is known to Claude Code only; this binary cannot ask it",
		"CLAUDE_PLUGIN_ROOT="+root+"; verify that the host invokes the configured hooks with real hook JSON")
}

func checkMCP(e *statusEnv) statusRow {
	const id = "mcp"
	evidence := "in-process MCP handshake (initialize, tools/list) against buildMCPServer over an in-memory transport; stdio itself is not exercised"
	ctx, cancel := context.WithTimeout(context.Background(), shellProbeTimeout)
	defer cancel()
	clientT, serverT := mcp.NewInMemoryTransports()
	serverSession, err := buildMCPServer(facet.OpenSet(e.root), e.version).Connect(ctx, serverT, nil)
	if err != nil {
		return row(id, stateRed, "the server did not start: "+err.Error(), evidence)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "facet-setup-status", Version: e.version}, nil)
	session, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		return row(id, stateRed, "the server did not answer the handshake: "+err.Error(), evidence)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		return row(id, stateRed, "the server did not list its tools: "+err.Error(), evidence)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "feature_find" {
			return row(id, stateOK, fmt.Sprintf("answered the handshake and lists %d tool(s), feature_find among them", len(tools.Tools)), evidence)
		}
	}
	return row(id, stateRed, "answered the handshake but does not list feature_find", evidence)
}

func checkLSP(e *statusEnv) statusRow {
	const id = "lsp"
	evidence := "in-process LSP initialize request through lspServer.serve over buffers; stdio itself is not exercised"
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"rootUri": pathToURI(e.root)},
	})
	var in, out bytes.Buffer
	fmt.Fprintf(&in, "Content-Length: %d\r\n\r\n%s", len(body), body)
	if err := newLSPServer(facet.OpenSet(e.root), e.version).serve(&in, &out); err != nil {
		return row(id, stateRed, "the server failed: "+err.Error(), evidence)
	}
	raw, err := readMessage(bufio.NewReader(&out))
	if err != nil {
		return row(id, stateRed, "the server did not answer initialize: "+err.Error(), evidence)
	}
	var reply struct {
		Result struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil || reply.Result.ServerInfo.Name == "" {
		return row(id, stateRed, "the reply to initialize carries no serverInfo", evidence)
	}
	return row(id, stateOK, "answered initialize as "+reply.Result.ServerInfo.Name, evidence)
}

func checkStatusline(e *statusEnv) statusRow {
	const id = "statusline"
	candidates := []string{filepath.Join(e.root, ".claude", "settings.json")}
	if e.home != "" {
		candidates = append(candidates, filepath.Join(e.home, ".claude", "settings.json"))
	}
	evidence := "read " + strings.Join(candidates, " and ") + " for facet-statusline"
	for _, path := range candidates {
		if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), "facet-statusline") {
			return row(id, stateOK, "facet-statusline is wired in "+path, evidence)
		}
	}
	return row(id, stateRed, "facet-statusline is in neither settings file", evidence)
}

// ---------------------------------------------------------------------------
// git

func gitOut(root string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

// filterBinary extracts the executable from filter.facet.clean, which
// `facet filter install` writes as `<binary> filter clean %f`, shell-quoted
// when the path needs it.
func filterBinary(cleanCommand string) string {
	cleanCommand = strings.TrimSpace(cleanCommand)
	if strings.HasPrefix(cleanCommand, "'") {
		if end := strings.Index(cleanCommand[1:], "'"); end >= 0 {
			return cleanCommand[1 : 1+end]
		}
	}
	if fields := strings.Fields(cleanCommand); len(fields) > 0 {
		return fields[0]
	}
	return ""
}

func checkFilter(e *statusEnv) statusRow {
	const id = "filter"
	clean, err := gitOut(e.root, "config", "--get", "filter.facet.clean")
	evidence := "git config --get filter.facet.clean; the sidecar listed as `facet filter status` does"
	if err != nil || clean == "" {
		return row(id, stateNotApplicable, "private-anchor filter is not installed (only needed to keep anchors out of commits)", evidence)
	}
	if bin := filterBinary(clean); bin == "" || !isRegularFile(bin) {
		return row(id, stateRed, fmt.Sprintf("installed, but the binary it names (%q) is gone; with filter.required set, git add refuses", bin), evidence)
	}
	commonDir, err := facet.GitCommonDir(e.root)
	if err != nil {
		return row(id, stateUnmeasured, "installed, but the git directory cannot be read: "+err.Error(), evidence)
	}
	all, err := facet.ListPrivateAnchors(commonDir)
	if err != nil {
		return row(id, stateUnmeasured, "installed, but the sidecar cannot be read: "+err.Error(), evidence)
	}
	blocks, orphans := 0, 0
	for _, pa := range all {
		working, _ := os.ReadFile(filepath.Join(e.root, filepath.FromSlash(pa.Path)))
		orphans += len(pa.Orphans(working))
		blocks += len(pa.Blocks)
	}
	if orphans > 0 {
		return row(id, stateRed, fmt.Sprintf("installed; %d of %d private anchor(s) have no place in the current file", orphans, blocks), evidence)
	}
	// The round trip through git itself (an anchor that stays out of
	// `git diff --cached`) is not run here; it needs a scratch file and the
	// setup skill drives it.
	return row(id, stateOK, fmt.Sprintf("installed, binary present, %d private anchor(s), none orphaned", blocks), evidence+"; the add/diff round trip is not run by this check")
}

// runsFacetLint reports whether a hook or manager configuration has a line
// that runs `facet lint`. A comment mentioning it does not count: a hook that
// only says "# facet lint --staged" checks nothing, and counting it would
// report a gate that is not there.
func runsFacetLint(content string) bool {
	for _, l := range strings.Split(content, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "#") {
			continue
		}
		if strings.Contains(t, "facet lint") {
			return true
		}
	}
	return false
}

func checkGitPrecommit(e *statusEnv) statusRow {
	const id = "git.precommit"
	hooksPath, err := gitOut(e.root, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return row(id, stateNotApplicable, "not a git repository", "git rev-parse --git-path hooks failed")
	}
	if !filepath.IsAbs(hooksPath) {
		hooksPath = filepath.Join(e.root, hooksPath)
	}
	hook := filepath.Join(hooksPath, "pre-commit")
	evidence := "read " + hook + ", then the configuration of husky, lefthook and pre-commit for `facet lint`"
	if data, err := os.ReadFile(hook); err == nil && runsFacetLint(string(data)) {
		if st, serr := os.Stat(hook); serr == nil && st.Mode()&0o111 == 0 && setupGOOS != "windows" {
			return row(id, stateRed, hook+" calls facet lint but is not executable, so git skips it", evidence)
		}
		return row(id, stateOK, "pre-commit hook calls facet lint", evidence)
	}
	for _, rel := range []string{".husky/pre-commit", "lefthook.yml", "lefthook.yaml", ".lefthook.yml", ".pre-commit-config.yaml"} {
		if data, err := os.ReadFile(filepath.Join(e.root, rel)); err == nil && runsFacetLint(string(data)) {
			return row(id, stateOK, rel+" runs facet lint (that the manager installed its git hook is not checked)", evidence)
		}
	}
	return row(id, stateRed, "no pre-commit hook runs facet lint; a commit from a terminal is never checked", evidence)
}

// ---------------------------------------------------------------------------
// reading and extending

func checkUI(e *statusEnv) statusRow {
	const id = "ui"
	if e.readerAssets == nil {
		return row(id, stateNotApplicable, "a browser application is not included in this binary", "no front-end assets were selected by the application")
	}
	if _, err := fs.Stat(e.readerAssets, "index.html"); err != nil {
		return row(id, stateRed, "the browser application's bundle is not embedded in this binary", "fs.Stat(index.html) on the embedded assets: "+err.Error())
	}
	return row(id, stateOK, "a browser application is embedded in this binary (`facet ui`)", "fs.Stat(index.html) on the embedded assets; serving it is not probed")
}

func checkRAGIndex(e *statusEnv) statusRow {
	return overRegistries(e, "rag.index", noRegistryHere, func(reg facet.Registry) regResult {
		index := e.cfg.IndexPath(reg)
		evidence := "stat " + index + "; its mtime against the newest record's"
		st, err := os.Stat(index)
		if err != nil {
			settings := e.cfg.EmbeddingSettings()
			if settings.Provider != "" || settings.Model != "" {
				return regResult{stateRed, "embedding is configured but no index was ever built (`facet rag index`)", evidence}
			}
			return regResult{stateNotApplicable, "no semantic index and no embedding configuration", evidence}
		}
		records, _ := reg.LoadFeatures()
		for _, rec := range records {
			if rs, err := os.Stat(rec.Path); err == nil && rs.ModTime().After(st.ModTime()) {
				return regResult{stateRed, fmt.Sprintf("the index is older than %s; rebuild it with `facet rag index`", reg.Rel(rec.Path)), evidence}
			}
		}
		return regResult{stateOK, "index present and newer than every record", evidence}
	})
}

func checkPlugins(e *statusEnv) statusRow {
	const id = "plugins"
	dir := PluginsDir()
	if dir == "" {
		return row(id, stateNotApplicable, "no plugins directory (no home directory and FACET_PLUGINS_DIR unset)", "PluginsDir()")
	}
	evidence := "facet.InstalledPlugins(" + dir + ")"
	plugins, err := facet.InstalledPlugins(dir)
	if err != nil {
		return row(id, stateUnmeasured, "cannot read the plugins directory: "+err.Error(), evidence)
	}
	if len(plugins) == 0 {
		return row(id, stateNotApplicable, "no plugin installed", evidence)
	}
	var bad []string
	for _, p := range plugins {
		if p.State != "ok" {
			bad = append(bad, p.ID+" ("+p.State+")")
		}
	}
	if len(bad) > 0 {
		return row(id, stateRed, "installed but not loadable: "+strings.Join(bad, ", "), evidence)
	}
	return row(id, stateOK, fmt.Sprintf("%d plugin(s) installed and verified", len(plugins)), evidence)
}

func checkTestRunners(e *statusEnv) statusRow {
	const id = "test.runners"
	evidence := "runners: in " + facet.ConfigName
	if e.cfgErr != nil {
		return row(id, stateUnmeasured, "cannot read "+facet.ConfigName+": "+e.cfgErr.Error(), evidence)
	}
	n := len(e.cfg.Runners)
	for _, entry := range e.cfg.Registries {
		n += len(entry.Runners)
	}
	if n == 0 {
		return row(id, stateNotApplicable, "no test runner declared", evidence)
	}
	return row(id, stateOK, fmt.Sprintf("%d runner(s) declared (they are not run by this check)", n), evidence)
}
