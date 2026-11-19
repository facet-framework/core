// Package scaffold writes FACET into a project that does not have it: the
// record layout, the JSON schemas, the specification, the lint suite with its
// pinned dispatcher, an empty scan filter and the CI workflow.
package scaffold

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

//go:generate go run gen.go

//go:embed payload.txtar
var bundle []byte

// File is one payload entry.
type File struct {
	Path string
	Data []byte
}

// Payload returns the embedded files in the order they are reported.
func Payload() ([]File, error) { return parseBundle(bundle) }

// registered are the files linked products add to what Run writes. They are
// not in the generated bundle, because the bundle is Core's alone: a product a
// build does not link must leave no file in the projects that build scaffolds.
var registered []File

// RegisterFile adds one file a linked product scaffolds beside the payload,
// from the product's init. The path is project-relative in the payload's own
// form, so a file under RecordsDir follows a chosen records directory the way
// the Core schemas do. Misuse is a build defect, so it panics at start-up
// rather than surfacing in some project's init.
func RegisterFile(f File) {
	clean := path.Clean(f.Path)
	if f.Path == "" || clean != f.Path || path.IsAbs(clean) || clean == "." || strings.HasPrefix(clean, "../") || len(f.Data) == 0 {
		panic(fmt.Sprintf("scaffold: unusable registered file %q", f.Path))
	}
	for _, r := range registered {
		if r.Path == f.Path {
			panic(fmt.Sprintf("scaffold: %s is registered twice", f.Path))
		}
	}
	registered = append(registered, File{Path: f.Path, Data: append([]byte(nil), f.Data...)})
}

// withRegistered places each registered file after the last payload file of
// its section, so the report keeps one heading per section. A registered path
// the payload already carries is refused: two sources for one file would make
// the written bytes depend on which one won.
func withRegistered(files []File) ([]File, error) {
	out := append([]File(nil), files...)
	for _, r := range registered {
		at := len(out)
		for i, f := range out {
			if f.Path == r.Path {
				return nil, fmt.Errorf("scaffold: %s is both in the payload and registered by a linked product", r.Path)
			}
			if sectionOf(f.Path) == sectionOf(r.Path) {
				at = i + 1
			}
		}
		out = append(out[:at], append([]File{r}, out[at:]...)...)
	}
	return out, nil
}

// Bundle returns the embedded bundle bytes.
func Bundle() []byte { return append([]byte(nil), bundle...) }

func parseBundle(data []byte) ([]File, error) {
	var files []File
	var current *File
	for _, line := range strings.SplitAfter(string(data), "\n") {
		trimmed := strings.TrimSuffix(line, "\n")
		if isMarker(trimmed) {
			files = append(files, File{Path: strings.TrimSuffix(strings.TrimPrefix(trimmed, "-- "), " --")})
			current = &files[len(files)-1]
			continue
		}
		if current != nil {
			current.Data = append(current.Data, line...)
		}
	}
	if len(files) == 0 {
		return nil, errors.New("scaffold: the embedded payload is empty")
	}
	return files, nil
}

// Options select where and how the scaffold writes.
type Options struct {
	// Root is the project directory. It is created when missing.
	Root string
	// RecordsDir is the record layout, relative to Root; empty means RecordsDir.
	RecordsDir string
	// DryRun reports what would be written and writes nothing.
	DryRun bool
	// Force replaces files the project already has.
	Force bool
	// Version, when set, is written as the dispatcher's pinned FACET_VERSION
	// default, so a project scaffolded by a release build installs that
	// release. Empty keeps the pin the payload carries.
	Version string
	// Out receives the report.
	Out io.Writer
}

// Result counts the outcome of a run.
type Result struct {
	Written int
	Kept    int
}

// ErrTarget matches every TargetError.
var ErrTarget = errors.New("unusable target")

// TargetError is a target the scaffold cannot write into at all. Its message
// is the reason alone, ready to be shown to the person who named the target.
type TargetError struct{ Reason string }

func (e *TargetError) Error() string        { return e.Reason }
func (e *TargetError) Is(target error) bool { return target == ErrTarget }

func targetError(format string, args ...any) error {
	return &TargetError{Reason: fmt.Sprintf(format, args...)}
}

var pinLine = regexp.MustCompile(`(?m)^FACET_VERSION="\$\{FACET_VERSION:-[0-9][0-9A-Za-z.+-]*\}"$`)
var versionPattern = regexp.MustCompile(`^[0-9][0-9A-Za-z.+-]*$`)

// Run scaffolds Options.Root and reports each step to Options.Out. A file the
// project already has is kept unless Force is set; the report names every file
// written and every file kept. Nothing is written before the target has been
// checked, so an unusable target leaves the disk as it was.
//
// @facet: delivery/distribution/project-scaffold
// @facet-role: primary
func Run(o Options) (Result, error) {
	var res Result
	out := o.Out
	if out == nil {
		out = io.Discard
	}
	files, err := Payload()
	if err != nil {
		return res, err
	}
	if files, err = withRegistered(files); err != nil {
		return res, err
	}
	records, err := recordsDir(o.RecordsDir)
	if err != nil {
		return res, err
	}
	if o.Version != "" {
		if !versionPattern.MatchString(o.Version) {
			return res, fmt.Errorf("scaffold: %q is not a version the dispatcher accepts", o.Version)
		}
		if files, err = pin(files, o.Version); err != nil {
			return res, err
		}
	}
	root, err := filepath.Abs(o.Root)
	if err != nil {
		return res, err
	}
	if err := checkTarget(root, o.DryRun); err != nil {
		return res, err
	}

	w := &writer{root: root, dry: o.DryRun, force: o.Force, out: out}
	fmt.Fprintf(out, "=== FACET init — %s ===\n", root)
	if o.DryRun {
		fmt.Fprintln(out, "  (dry run — nothing will be written)")
	}

	fmt.Fprintln(out, "\nRecord layout:")
	for _, d := range RecordSubdirs {
		if err := w.dir(path.Join(records, d)); err != nil {
			return w.res, err
		}
	}

	section := ""
	for _, f := range files {
		name := f.Path
		if rest, ok := strings.CutPrefix(name, RecordsDir+"/"); ok {
			name = path.Join(records, rest)
		}
		next := sectionOf(f.Path)
		if next != section {
			section = next
			fmt.Fprintf(out, "\n%s:\n", section)
			if section == "Lint suite" {
				if err := w.dir(lintSuiteDir); err != nil {
					return w.res, err
				}
			}
		}
		if err := w.file(name, f.Data, executable(f.Path)); err != nil {
			return w.res, err
		}
	}

	fmt.Fprintf(out, "\n=== Done — %d file(s) written, %d kept ===\n", w.res.Written, w.res.Kept)
	fmt.Fprint(out, closingReport)
	return w.res, nil
}

const closingReport = `
Still to do (these need judgement, not copying):
  1. Fill in .facetignore, which was written empty. Look at what this
     repository actually holds and list what a scan never needs to open.
     Media and documents are usually irrelevant; anything that could carry an
     @facet: anchor stays.
  2. Write the first feature record. The specification this run wrote to
     the record layout (framework/FACET.md) defines every field.
  3. Run 'facet lint' (or 'sh tools/facet-lint/run-all.sh') to verify the setup.

.github/workflows/facet-lint.yml lints every pull request with the FACET
version pinned in tools/facet-lint/facet. It has no paths filter on purpose;
if you add one, it must cover docs/facets/** and every directory with
@facet: anchors.
`

func sectionOf(p string) string {
	switch {
	case strings.HasPrefix(p, RecordsDir+"/"):
		return "Schemas and spec"
	case strings.HasPrefix(p, lintSuiteDir+"/"):
		return "Lint suite"
	case p == ".facetignore":
		return "Scan filter"
	case p == workflowPath:
		return "CI workflow"
	}
	return "Other files"
}

func executable(p string) bool { return strings.HasPrefix(p, lintSuiteDir+"/") }

// recordsDir accepts a relative directory that stays inside the project.
func recordsDir(dir string) (string, error) {
	if dir == "" {
		return RecordsDir, nil
	}
	clean := path.Clean(filepath.ToSlash(dir))
	if path.IsAbs(clean) || filepath.IsAbs(dir) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", targetError("the records directory %q must be a path inside the project", dir)
	}
	return clean, nil
}

// pin rewrites the dispatcher's FACET_VERSION default. The payload carries
// exactly one such line; a payload without it is refused rather than written
// with a pin nobody chose.
func pin(files []File, version string) ([]File, error) {
	out := make([]File, len(files))
	copy(out, files)
	for i, f := range out {
		if f.Path != DispatcherCopy {
			continue
		}
		if n := len(pinLine.FindAllIndex(f.Data, -1)); n != 1 {
			return nil, fmt.Errorf("scaffold: the dispatcher carries %d FACET_VERSION defaults, want 1", n)
		}
		out[i].Data = pinLine.ReplaceAllLiteral(f.Data, []byte(`FACET_VERSION="${FACET_VERSION:-`+version+`}"`))
		return out, nil
	}
	return nil, errors.New("scaffold: the payload holds no dispatcher to pin")
}

// checkTarget refuses a target that exists and is not a directory, and one
// the scaffold cannot write into. A missing target is created later, as its
// first directory.
func checkTarget(root string, dry bool) error {
	info, err := os.Stat(root)
	switch {
	case err == nil && !info.IsDir():
		return targetError("%s is not a directory", root)
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		return nil
	default:
		return targetError("%v", err)
	}
	if dry {
		return nil
	}
	probe, err := os.CreateTemp(root, ".facet-init-*")
	if err != nil {
		return targetError("cannot write into %s: %v", root, errors.Unwrap(err))
	}
	name := probe.Name()
	probe.Close()
	return os.Remove(name)
}

type writer struct {
	root       string
	dry, force bool
	out        io.Writer
	res        Result
}

func (w *writer) say(format string, args ...any) {
	if w.dry {
		fmt.Fprint(w.out, "  [dry-run] ")
	} else {
		fmt.Fprint(w.out, "  ")
	}
	fmt.Fprintf(w.out, format+"\n", args...)
}

func (w *writer) dir(rel string) error {
	if info, err := os.Stat(filepath.Join(w.root, filepath.FromSlash(rel))); err == nil && info.IsDir() {
		return nil
	}
	w.say("create dir  %s", rel)
	if w.dry {
		return nil
	}
	return os.MkdirAll(filepath.Join(w.root, filepath.FromSlash(rel)), 0o755)
}

func (w *writer) file(rel string, data []byte, exec bool) error {
	dst := filepath.Join(w.root, filepath.FromSlash(rel))
	info, err := os.Lstat(dst)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if exists && !w.force {
		w.say("keep        %s (already present)", rel)
		w.res.Kept++
		return nil
	}
	if exists && info.IsDir() {
		return fmt.Errorf("scaffold: %s is a directory; it cannot be replaced by a file", rel)
	}
	w.say("write       %s", rel)
	w.res.Written++
	if w.dry {
		return nil
	}
	mode := os.FileMode(0o644)
	if exec {
		mode = 0o755
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if exists {
		return replace(dst, data, mode)
	}
	return create(dst, data, mode)
}

// create writes a new file and refuses one that appeared since it was looked
// at, so a file the project wrote in the meantime is never overwritten.
func create(dst string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

// replace writes beside the file and renames over it, so a forced run never
// leaves half a file and never writes through a symbolic link.
func replace(dst string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".facet-init-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(name, mode)
	}
	if werr == nil {
		werr = os.Rename(name, dst)
	}
	if werr != nil {
		os.Remove(name)
	}
	return werr
}
