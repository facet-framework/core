package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/facet-framework/core/registry"
)

// buildPackedPlugin writes a plugin author's folder declaring the given
// tools, packs it, and returns the .facetplugin path — the shape every
// `facet plugin add` test in this file installs from.
func buildPackedPlugin(t *testing.T, id string, tools []string) string {
	t.Helper()
	src := t.TempDir()
	manifest := map[string]any{
		"id": id, "name": "Test Plugin", "version": "1.0.0",
		"api": 0, "entry": "plugin.js", "tools": tools,
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "plugin.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "plugin.js"), []byte("export default function(){}"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), id+"-1.0.0.facetplugin")
	if _, err := facet.Pack(src, out); err != nil {
		t.Fatalf("fixture precondition failed: packing did not succeed: %v", err)
	}
	return out
}

func runPluginAddCLI(t *testing.T, stdin string, extraArgs ...string) (dir, output string, err error) {
	t.Helper()
	dir = t.TempDir()
	t.Setenv("FACET_PLUGINS_DIR", dir)

	cmd := newPluginAddCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(extraArgs)
	err = cmd.Execute()
	return dir, out.String(), err
}

// runPluginAddCLIRealStdin is runPluginAddCLI's counterpart with a genuine
// *os.File as stdin — /dev/null, the same file a real
// `facet plugin add … </dev/null` redirects. This is what actually reaches
// isInteractive's `in.(*os.File)` branch; a strings.Reader never does.
func runPluginAddCLIRealStdin(t *testing.T, extraArgs ...string) (dir, output string, err error) {
	t.Helper()
	dir = t.TempDir()
	t.Setenv("FACET_PLUGINS_DIR", dir)

	devNull, openErr := os.Open(os.DevNull)
	if openErr != nil {
		t.Fatalf("fixture precondition failed: could not open %s: %v", os.DevNull, openErr)
	}
	defer devNull.Close()

	cmd := newPluginAddCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(devNull)
	cmd.SetArgs(extraArgs)
	err = cmd.Execute()
	return dir, out.String(), err
}

// TestGrantNamingAnUndeclaredToolIsRefused proves --grant cannot widen what a
// manifest itself declared: naming a tool the plugin never asked for would
// grant something nobody could have reviewed against the printed summary.
func TestGrantNamingAnUndeclaredToolIsRefused(t *testing.T) {
	source := buildPackedPlugin(t, "acme-notes", []string{"notes.read"})

	_, _, err := runPluginAddCLI(t, "", source, "--grant", "tools=notes.write")

	// The grant must be rejected when the tool was not declared in the manifest;
	// removing requireDeclared would make the following assertion fail.
	if err == nil {
		t.Fatal("a --grant naming an undeclared tool was not refused")
	}
	if !strings.Contains(err.Error(), "notes.write") {
		t.Fatalf("the refusal does not name the undeclared tool: %v", err)
	}
}

// TestNoTTYGrantsNothing proves that without a terminal and without --grant,
// installing a plugin declaring tools writes no grants.json at all — the
// default has to be silence, not an unattended yes.
func TestNoTTYGrantsNothing(t *testing.T) {
	source := buildPackedPlugin(t, "acme-notes", []string{"notes.read"})

	dir, _, err := runPluginAddCLI(t, "", source)
	if err != nil {
		t.Fatalf("a plain install without --grant was refused: %v", err)
	}

	// Mutation: grant manifest.Tools whenever they are non-empty, without
	// checking interactive first, and this goes red the other way —
	// grants.json appears though nobody with a terminal ever answered.
	if _, err := os.Stat(filepath.Join(dir, "acme-notes", "grants.json")); !os.IsNotExist(err) {
		t.Fatalf("a non-interactive install granted something nobody asked for: err=%v", err)
	}
}

// /dev/null is a character device but not a terminal. With no interactive
// stdin, print no grant question; say nothing was granted and how to grant it.
func TestRealNonTTYStdinPrintsNoQuestion(t *testing.T) {
	source := buildPackedPlugin(t, "acme-notes", []string{"notes.read"})

	_, output, err := runPluginAddCLIRealStdin(t, source)
	if err != nil {
		t.Fatalf("a plain install over a real non-TTY stdin was refused: %v", err)
	}

	if strings.Contains(output, "[y/N]") {
		t.Fatalf("a question was printed on a redirect nobody could answer:\n%s", output)
	}
	if !strings.Contains(output, "nothing granted") {
		t.Fatalf("the non-interactive run did not say that nothing was granted:\n%s", output)
	}
	if !strings.Contains(output, "--grant") {
		t.Fatalf("the non-interactive run did not say how to grant explicitly:\n%s", output)
	}
}

func TestGrantSummaryPrintsBeforeInstalledLine(t *testing.T) {
	source := buildPackedPlugin(t, "acme-notes", []string{"notes.read"})

	_, output, err := runPluginAddCLI(t, "", source)
	if err != nil {
		t.Fatalf("a plain install was refused: %v", err)
	}

	summaryAt := strings.Index(output, "— from acme-notes (local, unsigned)")
	installedAt := strings.Index(output, "installed acme-notes")
	if summaryAt == -1 {
		t.Fatalf("the grant summary line is missing from the output:\n%s", output)
	}
	if installedAt == -1 {
		t.Fatalf("the installed confirmation line is missing from the output:\n%s", output)
	}
	if !(summaryAt < installedAt) {
		t.Fatalf("the summary did not print before the installed confirmation:\n%s", output)
	}
}

// TestInterruptedPromptStillInstallsWithNoGrant verifies that EOF denies
// the interrupted grant while the already verified and staged installation
// remains complete; an interrupted answer must not grant rights.
func TestInterruptedPromptStillInstallsWithNoGrant(t *testing.T) {
	source := buildPackedPlugin(t, "acme-notes", []string{"notes.read"})

	// A pipe closed mid-read is the concrete shape of "interrupted": the
	// reader side sees a partial (here: empty) line and then EOF, exactly
	// what askYesNo sees when a human's terminal disconnects mid-answer.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("fixture precondition failed: could not open a pipe: %v", err)
	}
	if closeErr := w.Close(); closeErr != nil {
		t.Fatalf("fixture precondition failed: could not close the write end: %v", closeErr)
	}
	defer r.Close()

	dir := t.TempDir()
	t.Setenv("FACET_PLUGINS_DIR", dir)
	cmd := newPluginAddCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(r)
	cmd.SetArgs([]string{source})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("an install with an interrupted prompt must still succeed: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(dir, "acme-notes", "plugin.json")); statErr != nil {
		t.Fatalf("the plugin was not fully installed after an interrupted prompt: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "acme-notes", "grants.json")); !os.IsNotExist(statErr) {
		t.Fatalf("an interrupted prompt granted something nobody confirmed: err=%v", statErr)
	}
}

// TestAskYesNoTreatsAnEOFMidAnswerAsNo drives askYesNo directly with a reader
// that stops mid-word — the terminal-disconnect shape of "interrupted": two of
// "yes"'s four letters arrive, then the connection drops before a newline. A
// truncated, not-yet-valid answer must read as "no", the same as an empty
// one; it must never round up to "yes" just because it started the same way.
func TestAskYesNoTreatsAnEOFMidAnswerAsNo(t *testing.T) {
	for _, partial := range []string{"j", "ja", "y", "yes", "ye"} {
		var out strings.Builder
		if askYesNo(&out, strings.NewReader(partial), "grant?") {
			t.Fatalf("unfinished answer %q was treated as confirmation", partial)
		}
	}
	for _, complete := range []string{"j\n", "ja\n", "y\n", "yes\n"} {
		var out strings.Builder
		if !askYesNo(&out, strings.NewReader(complete), "grant?") {
			t.Fatalf("Enter-confirmed answer %q was refused", complete)
		}
	}
	var out strings.Builder
	if askYesNo(&out, iotest.DataErrReader(strings.NewReader("j")), "grant?") {
		t.Fatal("reader returning the affirmative byte and EOF together granted access")
	}
}

func TestWritingAnEmptyGrantRemovesEvenAnExistingEmptyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "acme-notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "acme-notes", "grants.json")
	for _, body := range []string{`{}`, `{"tools":null}`, `{"tools":[]}`} {
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := writeGrant(dir, "acme-notes", facet.Grant{}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatalf("empty grant file retained: %v", err)
		}
	}
}

// `facet plugin pack` packs a fresh folder with a sidecar whose sums are not
// written yet, under the default output name, and `facet plugin add` installs
// the result with the sidecar binary in place.
func TestPackCommandPacksAFreshSidecarPluginThatAddInstalls(t *testing.T) {
	platform := runtime.GOOS + "-" + runtime.GOARCH
	bin := "probe-" + platform
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	src := t.TempDir()
	manifest := map[string]any{
		"id": "acme-lint", "name": "Acme Lint", "version": "1.0.0",
		"api": 0, "entry": "plugin.js", "tools": []string{"facet.lint"},
		"sidecar": map[string]any{"bin": "probe", "sha256": map[string]any{}},
	}
	raw, _ := json.Marshal(manifest)
	for name, body := range map[string][]byte{
		"plugin.json": raw,
		"plugin.js":   []byte("export default function(){}"),
		bin:           []byte("bytes standing in for a build"),
	} {
		if err := os.WriteFile(filepath.Join(src, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// The default output name lands in the working directory.
	work := t.TempDir()
	wd, _ := os.Getwd()
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	pack := newPluginPackCmd()
	var packOut strings.Builder
	pack.SetOut(&packOut)
	pack.SetErr(&packOut)
	pack.SetArgs([]string{src})
	if err := pack.Execute(); err != nil {
		t.Fatalf("pack refused a fresh plugin with a sidecar: %v\n%s", err, packOut.String())
	}
	packed := filepath.Join(work, "acme-lint-1.0.0.facetplugin")
	if _, err := os.Stat(packed); err != nil {
		t.Fatalf("pack did not write the default-named package: %v", err)
	}

	dir, output, err := runPluginAddCLI(t, "", packed, "--grant", "tools=facet.lint")
	if err != nil {
		t.Fatalf("add refused pack's own output: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(dir, "acme-lint", bin)); err != nil {
		t.Fatalf("the installed plugin has no sidecar binary: %v", err)
	}
}
