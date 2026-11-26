// @facet: registry/layout/source-scanning
// @facet-role: primary
package facet

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/boyter/gocodewalker"
	"github.com/facet-framework/core/internal/sourceview"
)

// anchorRe matches an @facet: tag carrying a full three-segment id, optionally
// prefixed with the registry that owns it. Tags with fewer segments are ignored
// rather than reported, matching the shell tooling and the documented format.
var anchorRe = regexp.MustCompile(`@facet:[ \t]*(?:([a-z][a-z0-9-]*):)?([a-z][a-z0-9-]*/[a-z][a-z0-9-]*/[a-z][a-z0-9-]+)`)

// roleRe matches the role tag that follows an anchor.
var roleRe = regexp.MustCompile(`@facet-role:[ \t]*(primary|related|config|data)`)

// provesRe matches the tag that follows an anchor to say which contract
// scenarios the anchored code exists for. The captured group is a
// comma-separated list of scenario ids in the schema's kebab-case form
// (`^[a-z][a-z0-9-]*$`) — never the slash-separated feature id an @facet:
// tag carries, since a scenario id is only unique within its own record.
//
// @facet: registry/layout/anchor-proves-tag
// @facet-role: primary
var provesRe = regexp.MustCompile(`@facet-proves:[ \t]*([a-z][a-z0-9-]*(?:[ \t]*,[ \t]*[a-z][a-z0-9-]*)*)`)

// anchorMarker is the cheap prefilter. Walking and reading dominate the cost of
// a scan, so files without the literal marker never reach the regex engine.
var anchorMarker = []byte("@facet:")

// ignoreMarker opts a whole file out of anchor scanning.
var ignoreMarker = []byte("@facet-ignore" + "-file")

// ignoreHeadLines bounds how far into a file the opt-out marker counts.
const ignoreHeadLines = 10

// DeclaresOptOut reports whether a file's bytes carry the ignore marker in the
// head. Exported for callers that do their own walk — the corpus builder reads
// every file once and must classify them the same way this scanner does.
func DeclaresOptOut(data []byte) bool { return optsOut(data) }

// optsOut reports whether a file declares the ignore marker in its head.
func optsOut(data []byte) bool {
	head := data
	for i, n := 0, 0; i < len(head); i++ {
		if head[i] != '\n' {
			continue
		}
		if n++; n == ignoreHeadLines {
			head = head[:i]
			break
		}
	}
	return bytes.Contains(head, ignoreMarker)
}

// proseExtensions never carry real anchors: an @facet: tag in documentation is
// an example of the syntax, not a claim that the file implements the feature.
var proseExtensions = map[string]bool{
	".md": true, ".mdx": true, ".markdown": true,
	".rst": true, ".adoc": true, ".asciidoc": true,
	".txt": true, ".rtf": true, ".org": true,
}

// proseBasenames says the same thing for files that carry their kind in the
// name instead of the extension. An extension blocklist cannot see them: a
// LICENSE has no extension at all, and to every check it therefore looks like
// 661 lines of code nobody documented.
var proseBasenames = map[string]bool{
	"LICENSE": true, "LICENCE": true, "NOTICE": true,
	"COPYING": true, "AUTHORS": true, "CONTRIBUTORS": true,
}

// isProse is true for a file whose kind rules out an anchor, by extension or
// by name.
func isProse(path string) bool {
	base := filepath.Base(path)
	if proseBasenames[strings.TrimSuffix(base, filepath.Ext(base))] {
		return true
	}
	return proseExtensions[strings.ToLower(filepath.Ext(path))]
}

// Anchor is one @facet: tag found in source.
type Anchor struct {
	// Repository and Reference identify the selected source independently of its checkout.
	Repository string `json:"repository,omitempty"`
	Reference  string `json:"reference,omitempty"`
	ID         string
	// Registry is the name written in front of the id, empty when the tag was
	// unqualified and therefore belongs to whichever registry scans the file.
	Registry string
	Path     string
	Line     int
	Role     string
	// Proves lists scenario IDs from the facet-proves tag following this
	// anchor, in declaration order. Empty means no scenario association was
	// declared; it does not establish whether the code implements a scenario.
	Proves []string
}

// Qualified renders the anchor as written, registry prefix included.
func (a Anchor) Qualified() string {
	if a.Registry == "" {
		return a.ID
	}
	return a.Registry + ":" + a.ID
}

// BelongsTo reports whether this anchor is one the named registry should judge.
// An unqualified anchor belongs to every registry that scans its file; a
// qualified one belongs only to the registry it names.
func (a Anchor) BelongsTo(registry string) bool {
	return a.Registry == "" || a.Registry == registry
}

// ScanOptions narrows a scan. Zero value means "the whole repository".
type ScanOptions struct {
	View  *sourceview.ResolvedView
	files map[string]bool
	Dirs  []string // relative to root; empty means the root itself
	// unread, when set, collects the selected files the scan could not read.
	// The scan itself still passes over them, as every other check expects.
	unread *[]unreadSource
}

// unreadSource is a selected file the scan could not read: its path, and its
// portable reference when it lies in a source view.
type unreadSource struct{ path, reference string }

// ScanOptionsFromEnv reads the source-directory override the shell tooling
// honours. Superseded by .facetrc.yaml's `sources`, and kept because CI jobs
// and migration scripts point the scan at a scratch tree, which a committed
// file cannot express.
func ScanOptionsFromEnv() ScanOptions {
	return ScanOptions{Dirs: strings.Fields(os.Getenv("FACET_SOURCE_DIRS"))}
}

// IgnoreFile is FACET's own ignore file, read alongside .gitignore.
const IgnoreFile = ".facetignore"

// SourceFile is one file the scan opened, with the size and the answer that
// make it comparable to its neighbours.
type SourceFile struct {
	Repository string `json:"repository,omitempty"`
	Reference  string `json:"reference,omitempty"`
	// Path is absolute, as the walker produced it.
	Path string
	// Loc is the file's line count.
	Loc int
	// Anchored is true when the file carried at least one anchor this scan
	// accepted.
	Anchored bool
	// OptedOut is true when the file declared the ignore marker in its head.
	OptedOut bool
}

// ScanAnchors finds every @facet: anchor under root.
func ScanAnchors(root string, opts ScanOptions) ([]Anchor, error) {
	anchors, _, err := ScanWithFiles(root, opts)
	return anchors, err
}

// ScanWithFiles is ScanAnchors plus the files it walked past.
func ScanWithFiles(root string, opts ScanOptions) ([]Anchor, []SourceFile, error) {
	if opts.View != nil {
		return scanSelectedView(opts)
	}
	roots := []string{root}
	if len(opts.Dirs) > 0 {
		roots = roots[:0]
		for _, d := range opts.Dirs {
			p := absUnder(root, d)
			if isDir(p) {
				roots = append(roots, p)
			}
		}
		if len(roots) == 0 {
			return nil, nil, nil
		}
	}

	var anchors []Anchor
	var files []SourceFile
	for _, r := range roots {
		found, walked, err := scanTreeAllowed(r, opts.files, opts.unread)
		if err != nil {
			return nil, nil, err
		}
		anchors = append(anchors, found...)
		files = append(files, walked...)
	}

	sort.Slice(anchors, func(i, j int) bool {
		if anchors[i].Path != anchors[j].Path {
			return anchors[i].Path < anchors[j].Path
		}
		return anchors[i].Line < anchors[j].Line
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return anchors, files, nil
}

// NewSourceWalker returns the shared source walker and its file channel. All
// scans, progress counts and reachability checks must use the same file selection
// to avoid contradictory counts or false test-only findings. The caller starts
// the walker and drains the channel; Start closes it on return. Capture Start's
// error after draining, not concurrently with the receive loop.
func NewSourceWalker(root string) (*gocodewalker.FileWalker, chan *gocodewalker.File) {
	files := make(chan *gocodewalker.File, 64)
	walker := gocodewalker.NewFileWalker(root, files)
	walker.IgnoreGitIgnore = false
	walker.IgnoreIgnoreFile = false
	walker.CustomIgnore = []string{IgnoreFile}
	// Hidden directories can carry anchors, such as .github/workflows. Direct
	// file reads and tree scans must find the same anchors.
	walker.IncludeHidden = true

	walker.ExcludeDirectory = []string{".git", ".svn", ".hg"}
	walker.SetErrorHandler(func(error) bool { return true })
	return walker, files
}

func scanTree(root string) ([]Anchor, []SourceFile, error) { return scanTreeAllowed(root, nil, nil) }
func scanTreeAllowed(root string, allowed map[string]bool, unread *[]unreadSource) ([]Anchor, []SourceFile, error) {
	walker, files := NewSourceWalker(root)

	var walkErr error

	done := make(chan struct{})

	// See GitRelevantFiles's own doc comment for why this filters the
	// walker's output rather than feeding gocodewalker's
	// CustomIgnorePatterns - that field cannot reproduce git's own
	// anchoring semantics correctly.
	gitFiles, useGit := GitRelevantFiles(root)

	go func() {
		walkErr = walker.Start()
		close(done)
	}()

	var anchors []Anchor
	var walked []SourceFile
	for f := range files {
		if allowed != nil && !allowed[filepath.Clean(f.Location)] {
			continue
		}
		if useGit && !gitFiles.Contains(f.Location) {
			continue
		}
		found, meta, err := scanFileWithLines(f.Location)
		if err != nil {
			// An unreadable file is not a lint finding; a caller measuring
			// across files is told, so it does not report a complete result.
			if unread != nil {
				*unread = append(*unread, unreadSource{path: f.Location})
			}
			continue
		}
		anchors = append(anchors, found...)
		walked = append(walked, meta)
	}
	<-done
	return anchors, walked, walkErr
}

// ScanFile extracts the anchors carried by a single file.
func ScanFile(path string) ([]Anchor, error) { return scanFile(path) }

func scanFile(path string) ([]Anchor, error) {
	anchors, _, err := scanFileWithLines(path)
	return anchors, err
}

// scanFileWithLines reads a file once and answers everything asked of it: which
// anchors it carries, how big it is, and whether it took itself out of the
// scan. Any of those in a second pass would double the only expensive part of a
// scan, which is the reading.
func scanFileWithLines(path string) ([]Anchor, SourceFile, error) {
	meta := SourceFile{Path: path}
	if isProse(path) {
		return nil, meta, nil
	}
	if info, err := os.Stat(path); err == nil && info.Size() > scanWholeFileLimit {
		return scanLargeFile(path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, meta, err
	}
	meta.Loc = bytes.Count(data, []byte("\n"))
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		meta.Loc++
	}
	meta.OptedOut = optsOut(data)
	anchors := ScanContent(path, data)
	meta.Anchored = len(anchors) > 0
	return anchors, meta, nil
}

// scanWholeFileLimit is the size above which a file is not read into memory
// whole. A variable so the tests can drive the large-file path with small
// fixtures.
var scanWholeFileLimit int64 = 2 << 20

// scanLargeHead is how much of a large file is searched for anchors. An anchor
// sits in a file's head comment; a file this large is generated or data, and
// one buried past its first megabyte is not one this scan promises to find.
const scanLargeHead = 1 << 20

// scanLargeFile answers what scanFileWithLines answers for a file too large to
// hold: the line count is exact, the anchors and the opt-out come from its head.
func scanLargeFile(path string) ([]Anchor, SourceFile, error) {
	meta := SourceFile{Path: path}
	f, err := os.Open(path)
	if err != nil {
		return nil, meta, err
	}
	defer f.Close()

	head := make([]byte, scanLargeHead)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, meta, err
	}
	head = head[:n]
	lines := bytes.Count(head, []byte("\n"))
	last := byte('\n')
	if n > 0 {
		last = head[n-1]
	}
	buf := make([]byte, 64*1024)
	for {
		m, rerr := f.Read(buf)
		if m > 0 {
			lines += bytes.Count(buf[:m], []byte("\n"))
			last = buf[m-1]
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, meta, rerr
		}
	}
	if last != '\n' {
		lines++
	}
	meta.Loc = lines
	// The head may end inside a line; ScanContent reads complete lines only.
	if i := bytes.LastIndexByte(head, '\n'); i >= 0 {
		head = head[:i+1]
	}
	meta.OptedOut = optsOut(head)
	anchors := ScanContent(path, head)
	meta.Anchored = len(anchors) > 0
	return anchors, meta, nil
}

// ScanContent extracts the anchors carried by a file whose bytes the caller
// already holds.
func ScanContent(path string, data []byte) []Anchor {
	if isProse(path) {
		return nil
	}
	// Prefilter: most files in a repository carry no anchor at all.
	if !bytes.Contains(data, anchorMarker) {
		return nil
	}
	if optsOut(data) {
		return nil
	}

	var anchors []Anchor
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	var pending *Anchor
	flush := func() {
		if pending != nil {
			anchors = append(anchors, *pending)
			pending = nil
		}
	}
	for scanner.Scan() {
		line++
		text := scanner.Text()
		if m := anchorRe.FindStringSubmatch(text); m != nil {
			flush()
			pending = &Anchor{Registry: m[1], ID: m[2], Path: path, Line: line}
			continue
		}
		if pending == nil {
			continue
		}
		// The role and proves tags are documented as following the anchor
		// directly, one per line, in either order — so both are checked on
		// every line while an anchor is pending, and reading continues as
		// long as consecutive lines keep matching one of the two. The first
		// line matching neither ends the run and flushes what was collected.
		if m := roleRe.FindStringSubmatch(text); m != nil {
			pending.Role = m[1]
			continue
		}
		if m := provesRe.FindStringSubmatch(text); m != nil {
			pending.Proves = append(pending.Proves, splitProves(m[1])...)
			continue
		}
		flush()
	}
	flush()
	// A scan error here can only be a line longer than the four-megabyte buffer.
	// The anchors found before it are still real, and dropping them would make
	// one pathological line hide every tag in the file.
	return anchors
}

// splitProves parses the comma-separated scenario-id list an @facet-proves:
// line captures, trimming the incidental whitespace the pattern already
// tolerates around each comma.
func splitProves(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// AnchorsByID groups anchors by the feature they point at.
func AnchorsByID(anchors []Anchor) map[string][]Anchor {
	byID := make(map[string][]Anchor)
	for _, a := range anchors {
		byID[a.ID] = append(byID[a.ID], a)
	}
	return byID
}

// AnchorPaths lists the distinct files anchoring one feature, sorted.
func AnchorPaths(anchors []Anchor) []string {
	seen := make(map[string]struct{}, len(anchors))
	var paths []string
	for _, a := range anchors {
		if _, dup := seen[a.Path]; dup {
			continue
		}
		seen[a.Path] = struct{}{}
		paths = append(paths, a.Path)
	}
	sort.Strings(paths)
	return paths
}

// ScanLiterals reports which nonempty needles occur in source files selected by
// opts. It searches literal bytes, including files without source anchors.
// excludePrefix skips walker paths beginning with that prefix, normally the
// absolute records directory, so a flag declaration cannot satisfy its own check.
func ScanLiterals(root string, opts ScanOptions, needles []string, excludePrefix string) (map[string]bool, error) {
	found := make(map[string]bool, len(needles))
	if opts.View != nil {
		if err := opts.View.Validate(); err != nil {
			return nil, err
		}
		for _, src := range opts.View.Ordered() {
			if err := src.ValidateWorkingTree(); err != nil {
				return nil, err
			}
			paths, e := src.Files()
			if e != nil {
				return nil, e
			}
			allowed := map[string]bool{}
			for _, rel := range paths {
				allowed[filepath.Join(src.Root, filepath.FromSlash(rel))] = true
			}
			part, err := ScanLiterals(src.Root, ScanOptions{Dirs: opts.Dirs, files: allowed}, needles, excludePrefix)
			if err != nil {
				return nil, err
			}
			for key, value := range part {
				found[key] = found[key] || value
			}
		}
		return found, nil
	}
	if len(needles) == 0 {
		return found, nil
	}
	raw := make([][]byte, 0, len(needles))
	for _, n := range needles {
		if n != "" {
			found[n] = false
			raw = append(raw, []byte(n))
		}
	}

	roots := []string{root}
	if len(opts.Dirs) > 0 {
		roots = roots[:0]
		for _, d := range opts.Dirs {
			p := absUnder(root, d)
			if isDir(p) {
				roots = append(roots, p)
			}
		}
	}

	for _, r := range roots {

		walker, files := NewSourceWalker(r)
		gitFiles, useGit := GitRelevantFiles(r)
		go func() { _ = walker.Start() }()

		for f := range files {
			if opts.files != nil && !opts.files[filepath.Clean(f.Location)] {
				continue
			}
			if useGit && !gitFiles.Contains(f.Location) {
				continue
			}
			if proseExtensions[strings.ToLower(filepath.Ext(f.Location))] {
				continue
			}
			if excludePrefix != "" && strings.HasPrefix(f.Location, excludePrefix) {
				continue
			}
			data, err := os.ReadFile(f.Location)
			if err != nil {
				continue
			}
			for i, needle := range raw {
				if !found[needles[i]] && bytes.Contains(data, needle) {
					found[needles[i]] = true
				}
			}
		}
	}
	return found, nil
}

// scanSelectedView consumes only the explicitly selected checkout for each logical repository.
// Private anchors already present in that checkout retain their placement; the scan never
// smudges another worktree or writes the common sidecar.
//
// @facet: registry/layout/source-views
// @facet-role: related
func scanSelectedView(opts ScanOptions) ([]Anchor, []SourceFile, error) {
	if err := opts.View.Validate(); err != nil {
		return nil, nil, err
	}
	var anchors []Anchor
	var files []SourceFile
	for _, src := range opts.View.Ordered() {
		if err := src.ValidateWorkingTree(); err != nil {
			return nil, nil, err
		}
		paths, err := src.Files()
		if err != nil {
			return nil, nil, err
		}
		allowed := map[string]bool{}
		for _, rel := range paths {
			path := filepath.Join(src.Root, filepath.FromSlash(rel))
			// A caller narrowing the scan to known files keeps the view's
			// selection: a file outside it stays unread.
			if opts.files == nil || opts.files[filepath.Clean(path)] {
				allowed[path] = true
			}
		}
		var missed []unreadSource
		sub := ScanOptions{Dirs: opts.Dirs, files: allowed}
		if opts.unread != nil {
			sub.unread = &missed
		}
		found, walked, err := ScanWithFiles(src.Root, sub)
		if err != nil {
			return nil, nil, err
		}
		for _, m := range missed {
			rel, e := filepath.Rel(src.Root, m.path)
			if e != nil {
				return nil, nil, e
			}
			m.reference, e = opts.View.Qualify(src.Repository, filepath.ToSlash(rel))
			if e != nil {
				return nil, nil, e
			}
			*opts.unread = append(*opts.unread, m)
		}
		for i := range found {
			rel, e := filepath.Rel(src.Root, found[i].Path)
			if e != nil {
				return nil, nil, e
			}
			found[i].Repository = src.Repository
			found[i].Reference, e = opts.View.Qualify(src.Repository, filepath.ToSlash(rel))
			if e != nil {
				return nil, nil, e
			}
		}
		for i := range walked {
			rel, e := filepath.Rel(src.Root, walked[i].Path)
			if e != nil {
				return nil, nil, e
			}
			walked[i].Repository = src.Repository
			walked[i].Reference, e = opts.View.Qualify(src.Repository, filepath.ToSlash(rel))
			if e != nil {
				return nil, nil, e
			}
		}
		anchors = append(anchors, found...)
		files = append(files, walked...)
		if err := src.ValidateWorkingTree(); err != nil {
			return nil, nil, err
		}
	}
	return anchors, files, nil
}

// SelectedSourceFiles enumerates precisely the files used by the authoritative
// selected-view scanner, with local Path plus portable Repository/Reference.
// Corpus consumers should share this rather than discover additional clones.
func SelectedSourceFiles(reg Registry) ([]SourceFile, error) {
	_, files, err := ScanWithFiles(reg.Root, reg.ScanOptions())
	return files, err
}
