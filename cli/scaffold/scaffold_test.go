package scaffold

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// moduleRoot is the source tree this package is built in, where the canonical
// files of the payload live.
func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := FindModuleRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func payload(t *testing.T) []File {
	t.Helper()
	files, err := Payload()
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func payloadFile(t *testing.T, name string) []byte {
	t.Helper()
	for _, f := range payload(t) {
		if f.Path == name {
			return f.Data
		}
	}
	t.Fatalf("the payload holds no %s", name)
	return nil
}

// emptyProject returns a project directory with one source file and nothing of
// FACET, after checking that is what it is.
func emptyProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "app.ts"), []byte("export const noop = () => null\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := snapshot(t, dir); len(got) != 3 {
		t.Fatalf("fixture: the project should hold src/ and src/app.ts only, got %v", keys(got))
	}
	return dir
}

// snapshot maps every path below dir to its mode and content.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		value := info.Mode().String()
		if d.Type().IsRegular() {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			value += " " + string(data)
		}
		out[filepath.ToSlash(rel)] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func run(t *testing.T, o Options) (Result, string) {
	t.Helper()
	var out bytes.Buffer
	o.Out = &out
	res, err := Run(o)
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	return res, out.String()
}

func TestTheEmbeddedPayloadIsGeneratedFromTheCanonicalFiles(t *testing.T) {
	want, err := Generate(moduleRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(Bundle(), want) {
		t.Fatal("payload.txtar is stale against the canonical files of this tree; run 'go generate' in this package")
	}
}

// The payload compared with its sources by an independent reading of the rule:
// each file byte for byte, the lint suite and the dispatcher with exactly their
// anchor lines gone, the workflow the published one.
func TestThePayloadIsTheCanonicalFilesWithOnlyAnchorLinesRemoved(t *testing.T) {
	root := moduleRoot(t)
	read := func(rel string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	column0 := regexp.MustCompile(`(?m)^#[ \t]*@facet(-role)?:[ \t].*\n`)
	indented := regexp.MustCompile(`(?m)^[ \t]*#[ \t]*@facet(-role)?:[ \t].*\n`)

	schemas, _ := filepath.Glob(filepath.Join(root, "schema", "*.json"))
	tools, _ := filepath.Glob(filepath.Join(root, "tools", "facet-lint", "*.sh"))
	if len(schemas) == 0 || len(tools) == 0 {
		t.Fatal("fixture: no schemas or lint scripts in the source tree")
	}
	seen := 0
	for _, s := range schemas {
		name := filepath.Base(s)
		if !bytes.Equal(payloadFile(t, "docs/facets/schema/"+name), read("schema/"+name)) {
			t.Errorf("schema %s differs from its source", name)
		}
		seen++
	}
	for _, s := range tools {
		name := filepath.Base(s)
		got := payloadFile(t, "tools/facet-lint/"+name)
		if want := column0.ReplaceAll(read("tools/facet-lint/"+name), nil); !bytes.Equal(got, want) {
			t.Errorf("lint script %s differs from its source by more than its anchor lines", name)
		}
		seen++
	}
	if !bytes.Equal(payloadFile(t, "docs/facets/framework/FACET.md"), read("docs/framework/FACET.md")) {
		t.Error("the specification differs from its source")
	}
	dispatcher := payloadFile(t, DispatcherCopy)
	if want := indented.ReplaceAll(read("bin/facet"), nil); !bytes.Equal(dispatcher, want) {
		t.Error("the dispatcher differs from bin/facet by more than its anchor lines")
	}
	if regexp.MustCompile(`(?m)^[ \t]*#[ \t]*@facet:`).Match(dispatcher) {
		t.Error("the dispatcher copy still declares an anchor")
	}
	published := "facet-framework/core/.github/workflows/facet-lint.yml"
	if _, err := os.Stat(filepath.Join(root, "facet-framework", "core")); err != nil {
		published = ".github/workflows/facet-lint.yml"
	}
	workflow := payloadFile(t, ".github/workflows/facet-lint.yml")
	if !bytes.Equal(workflow, read(published)) {
		t.Errorf("the workflow is not the published one (%s)", published)
	}
	if regexp.MustCompile(`secrets\.|paths:|gh release download|self-hosted`).Match(workflow) {
		t.Error("the workflow needs what a stranger's project does not have")
	}
	if !bytes.Contains(workflow, []byte("tools/facet-lint/facet")) {
		t.Error("the workflow does not run the copied dispatcher")
	}
	// Every payload file is accounted for: schemas, lint scripts, the spec,
	// the dispatcher, .facetignore and the workflow.
	if got, want := len(payload(t)), seen+4; got != want {
		t.Errorf("the payload holds %d files, want %d", got, want)
	}
}

func TestAFreshProjectGetsEveryPayloadFileAndTheRecordLayout(t *testing.T) {
	dir := emptyProject(t)
	res, out := run(t, Options{Root: dir})
	files := payload(t)
	if res.Written != len(files) || res.Kept != 0 {
		t.Fatalf("written %d kept %d, want %d and 0", res.Written, res.Kept, len(files))
	}
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f.Path))
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%s was not written: %v", f.Path, err)
		}
		if !bytes.Equal(data, f.Data) {
			t.Errorf("%s differs from the payload", f.Path)
		}
		info, _ := os.Stat(p)
		want := os.FileMode(0o644)
		if strings.HasPrefix(f.Path, "tools/facet-lint/") {
			want = 0o755
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s has mode %v, want %v", f.Path, info.Mode().Perm(), want)
		}
		if !strings.Contains(out, "  write       "+f.Path+"\n") {
			t.Errorf("the report does not name %s", f.Path)
		}
	}
	for _, d := range RecordSubdirs {
		if info, err := os.Stat(filepath.Join(dir, "docs", "facets", d)); err != nil || !info.IsDir() {
			t.Errorf("record directory %s missing", d)
		}
		if !strings.Contains(out, "  create dir  docs/facets/"+d+"\n") {
			t.Errorf("the report does not name the directory %s", d)
		}
	}
	if !strings.Contains(out, fmt.Sprintf("=== Done — %d file(s) written, 0 kept ===", len(files))) {
		t.Errorf("the closing count is wrong:\n%s", out)
	}
}

func TestASecondRunWritesNothing(t *testing.T) {
	dir := emptyProject(t)
	run(t, Options{Root: dir})
	before := snapshot(t, dir)
	res, out := run(t, Options{Root: dir})
	if res.Written != 0 || res.Kept != len(payload(t)) {
		t.Fatalf("second run: written %d kept %d\n%s", res.Written, res.Kept, out)
	}
	if !strings.Contains(out, "0 file(s) written") || strings.Contains(out, "  write ") || strings.Contains(out, "create dir") {
		t.Fatalf("the second run reported writing:\n%s", out)
	}
	after := snapshot(t, dir)
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("%s changed on the second run", k)
		}
	}
	if len(after) != len(before) {
		t.Fatal("the second run added files")
	}
}

const sentinel = "# written by the project, not by the scaffold\n"

func TestAHalfAdoptedProjectKeepsWhatItHasAndGainsTheRest(t *testing.T) {
	dir := emptyProject(t)
	schema := filepath.Join(dir, "docs", "facets", "schema", "facet.schema.json")
	if err := os.MkdirAll(filepath.Dir(schema), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{schema, filepath.Join(dir, ".facetignore")} {
		if err := os.WriteFile(p, []byte(sentinel), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "tools", "facet-lint", "run-all.sh")); err == nil {
		t.Fatal("fixture: the lint suite already exists, so nothing here is missing")
	}
	res, out := run(t, Options{Root: dir})
	for _, p := range []string{schema, filepath.Join(dir, ".facetignore")} {
		if data, _ := os.ReadFile(p); string(data) != sentinel {
			t.Errorf("%s was overwritten without --force", p)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "tools", "facet-lint", "run-all.sh")); err != nil {
		t.Error("the missing lint suite was not written")
	}
	if res.Kept != 2 || res.Written != len(payload(t))-2 {
		t.Errorf("written %d kept %d", res.Written, res.Kept)
	}
	for _, want := range []string{"  keep        .facetignore (already present)", "  keep        docs/facets/schema/facet.schema.json (already present)"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
}

func TestForceReplacesTheProjectsCopyButNotThroughALink(t *testing.T) {
	dir := emptyProject(t)
	schema := filepath.Join(dir, "docs", "facets", "schema", "facet.schema.json")
	if err := os.MkdirAll(filepath.Dir(schema), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(schema, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(outside, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, ".facetignore")); err != nil {
		t.Fatal(err)
	}
	run(t, Options{Root: dir, Force: true})
	if data, _ := os.ReadFile(schema); !bytes.Equal(data, payloadFile(t, "docs/facets/schema/facet.schema.json")) {
		t.Error("--force left the project's copy in place")
	}
	if data, _ := os.ReadFile(outside); string(data) != sentinel {
		t.Error("--force wrote through a symbolic link into a file outside the project")
	}
	if info, err := os.Lstat(filepath.Join(dir, ".facetignore")); err != nil || !info.Mode().IsRegular() {
		t.Error("--force did not replace the link with the scaffold's file")
	}
}

func TestADryRunNamesWhatItWouldWriteAndWritesNothing(t *testing.T) {
	dir := emptyProject(t)
	before := snapshot(t, dir)
	res, out := run(t, Options{Root: dir, DryRun: true})
	after := snapshot(t, dir)
	if len(after) != len(before) {
		t.Fatalf("the dry run changed the tree: %v", keys(after))
	}
	if !strings.Contains(out, "(dry run — nothing will be written)") ||
		!strings.Contains(out, "  [dry-run] write       docs/facets/framework/FACET.md") ||
		!strings.Contains(out, "  [dry-run] create dir  docs/facets/features") {
		t.Fatalf("the dry run did not name what it would write:\n%s", out)
	}
	if res.Written != len(payload(t)) {
		t.Errorf("the dry run counted %d files, want %d", res.Written, len(payload(t)))
	}
	// A target that does not exist yet stays absent under a dry run.
	missing := filepath.Join(t.TempDir(), "new")
	run(t, Options{Root: missing, DryRun: true})
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Error("the dry run created the target directory")
	}
}

func TestTheJudgementHalfIsLeftUndoneAndNamed(t *testing.T) {
	dir := emptyProject(t)
	_, out := run(t, Options{Root: dir})
	data, err := os.ReadFile(filepath.Join(dir, ".facetignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "#") {
			t.Errorf(".facetignore ships a pattern: %q", line)
		}
	}
	if !strings.Contains(string(data), "judgement about") {
		t.Error(".facetignore is empty but does not say why")
	}
	for _, want := range []string{"Still to do", ".facetignore", "first feature record", "'facet lint'", "no paths filter", "docs/facets/**"} {
		if !strings.Contains(out, want) {
			t.Errorf("the closing report does not say %q:\n%s", want, out)
		}
	}
}

func TestATargetThatIsAFileIsRefusedBeforeAnyWrite(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "README.md")
	if err := os.WriteFile(file, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	_, err := Run(Options{Root: file, Out: &out})
	if !errors.Is(err, ErrTarget) || !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("err = %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("the refusal printed a report:\n%s", out.String())
	}
	if got := snapshot(t, dir); len(got) != 2 {
		t.Errorf("the refusal wrote something: %v", keys(got))
	}
}

func TestATargetThatCannotBeWrittenIsRefusedBeforeAnyWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	// Premise: this process really cannot write here (a superuser can).
	if f, err := os.CreateTemp(dir, "probe"); err == nil {
		f.Close()
		os.Remove(f.Name())
		t.Skip("this user can write into a read-only directory; the refusal cannot be observed")
	}
	_, err := Run(Options{Root: dir, Out: &bytes.Buffer{}})
	if !errors.Is(err, ErrTarget) || !strings.Contains(err.Error(), "cannot write into") {
		t.Fatalf("err = %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("the refusal left %d entries", len(entries))
	}
}

func TestAReleaseVersionIsPinnedInTheDispatcherAndNothingElseChanges(t *testing.T) {
	dir := emptyProject(t)
	run(t, Options{Root: dir, Version: "0.1.0-rc.7"})
	got, err := os.ReadFile(filepath.Join(dir, "tools", "facet-lint", "facet"))
	if err != nil {
		t.Fatal(err)
	}
	want := payloadFile(t, DispatcherCopy)
	if !pinLine.Match(want) {
		t.Fatal("fixture: the payload's dispatcher has no FACET_VERSION default line")
	}
	if !bytes.Contains(got, []byte(`FACET_VERSION="${FACET_VERSION:-0.1.0-rc.7}"`)) {
		t.Fatal("the dispatcher does not pin the release version")
	}
	gotLines, wantLines := strings.Split(string(got), "\n"), strings.Split(string(want), "\n")
	if len(gotLines) != len(wantLines) {
		t.Fatal("pinning changed the number of lines")
	}
	diff := 0
	for i := range gotLines {
		if gotLines[i] != wantLines[i] {
			diff++
		}
	}
	if diff != 1 {
		t.Fatalf("pinning changed %d lines, want 1", diff)
	}

	empty := t.TempDir()
	if _, err := Run(Options{Root: empty, Version: "1.0; rm -rf /", Out: &bytes.Buffer{}}); err == nil {
		t.Fatal("a version the dispatcher would refuse was written")
	}
	if entries, _ := os.ReadDir(empty); len(entries) != 0 {
		t.Fatal("a refused version still wrote files")
	}
}

func TestAnotherRecordsDirectoryMovesTheRecordLayoutOnly(t *testing.T) {
	dir := emptyProject(t)
	run(t, Options{Root: dir, RecordsDir: "registry"})
	if _, err := os.Stat(filepath.Join(dir, "registry", "schema", "facet.schema.json")); err != nil {
		t.Error("the schemas did not follow the records directory")
	}
	if _, err := os.Stat(filepath.Join(dir, "docs")); err == nil {
		t.Error("docs/facets was still written")
	}
	if _, err := os.Stat(filepath.Join(dir, "tools", "facet-lint", "run-all.sh")); err != nil {
		t.Error("the lint suite moved with the records directory")
	}
	for _, bad := range []string{"..", "../x", "/abs"} {
		if _, err := Run(Options{Root: t.TempDir(), RecordsDir: bad, Out: &bytes.Buffer{}}); !errors.Is(err, ErrTarget) {
			t.Errorf("records directory %q was accepted", bad)
		}
	}
}

// A linked product's file is written beside the payload, follows the records
// directory and joins its section's heading rather than opening a second one;
// the bundle itself never carries it, and a path the payload already has is
// refused instead of letting one of two sources win.
func TestALinkedProductsFileIsWrittenBesideThePayloadAndNeverBundled(t *testing.T) {
	saved := registered
	t.Cleanup(func() { registered = saved })
	registered = nil
	const name = "product.schema.json"
	data := []byte(`{"type":"object"}` + "\n")
	RegisterFile(File{Path: RecordsDir + "/schema/" + name, Data: data})

	for _, f := range payload(t) {
		if strings.HasSuffix(f.Path, "/"+name) {
			t.Fatal("a registered file entered the bundle")
		}
	}
	dir := emptyProject(t)
	_, report := run(t, Options{Root: dir, RecordsDir: "registry"})
	if got, err := os.ReadFile(filepath.Join(dir, "registry", "schema", name)); err != nil || !bytes.Equal(got, data) {
		t.Errorf("the registered file was not written under the records directory: %q %v", got, err)
	}
	if n := strings.Count(report, "\nSchemas and spec:\n"); n != 1 {
		t.Errorf("the registered file opened a second section heading (%d):\n%s", n, report)
	}

	registered = nil
	RegisterFile(File{Path: RecordsDir + "/schema/facet.schema.json", Data: data})
	if _, err := Run(Options{Root: t.TempDir(), Out: &bytes.Buffer{}}); err == nil || !strings.Contains(err.Error(), "both in the payload and registered") {
		t.Errorf("a registered path the payload already carries was accepted: %v", err)
	}
	for _, bad := range []File{{Path: "../x", Data: data}, {Path: "/abs", Data: data}, {Path: "a//b", Data: data}, {Path: "x"}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("RegisterFile accepted %+v", bad)
				}
			}()
			RegisterFile(bad)
		}()
	}
}

// The strip on a sample that holds every form: anchors at column 0 and
// indented, and the tag in prose, in a pattern and in a fixture, which must
// all stay. The canonical files need not carry anchors (a published tree may
// have none), so the rule is proven here rather than on them.
func TestTheStripRemovesAnchorDeclarationsAndNothingElse(t *testing.T) {
	sample := strings.Join([]string{
		"#!/bin/sh",
		"# @facet: a/b/c",
		"# @facet-role: primary",
		"#@facet: a/b/d",
		"  # @facet: a/b/e",
		"\t# @facet-role: related",
		"# Lines carrying @facet: in prose stay.",
		"grep -h '@facet:' \"$f\"",
		"printf '// @facet: x/y/z\\n'",
		"# @facet:",
		"",
	}, "\n")
	column0 := string(stripAnchors([]byte(sample), stripColumnZero))
	indented := string(stripAnchors([]byte(sample), stripIndented))
	for _, gone := range []string{"# @facet: a/b/c\n", "# @facet-role: primary\n", "#@facet: a/b/d\n"} {
		if strings.Contains(column0, gone) || strings.Contains(indented, gone) {
			t.Errorf("%q survived the strip", gone)
		}
	}
	for _, kept := range []string{"  # @facet: a/b/e\n", "\t# @facet-role: related\n"} {
		if !strings.Contains(column0, kept) {
			t.Errorf("the column-0 strip removed the indented %q", kept)
		}
		if strings.Contains(indented, kept) {
			t.Errorf("the indented strip kept %q", kept)
		}
	}
	for _, kept := range []string{"#!/bin/sh\n", "in prose stay.\n", "grep -h '@facet:'", "printf '// @facet: x/y/z", "# @facet:\n"} {
		if !strings.Contains(column0, kept) || !strings.Contains(indented, kept) {
			t.Errorf("the strip removed %q, which is not an anchor declaration", kept)
		}
	}
	if stripped := string(stripAnchors([]byte(sample), keepAnchors)); stripped != sample {
		t.Error("keepAnchors changed the file")
	}
}

func TestTheBundleFormatRefusesWhatItCannotCarry(t *testing.T) {
	if err := checkBundleable("x", []byte("no newline")); err == nil {
		t.Error("a file without a final newline was accepted")
	}
	if err := checkBundleable("x", []byte("a\n-- y --\n")); err == nil {
		t.Error("a file holding a marker line was accepted")
	}
	files, err := parseBundle([]byte("comment\n-- a --\none\n-- b/c --\ntwo\nthree\n"))
	if err != nil || len(files) != 2 || files[0].Path != "a" || string(files[0].Data) != "one\n" || files[1].Path != "b/c" || string(files[1].Data) != "two\nthree\n" {
		t.Fatalf("parseBundle = %+v, %v", files, err)
	}
}
