// Reading a plugin-declared directory of markdown files under the repository
// root, with a directory a plugin names rather than one fixed by the records
// layout.
package facet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ErrRepoReadPathEscape is a path that leaves the declared directory — judged
// after symlinks are resolved, because a string prefix is passed by a symlink.
var ErrRepoReadPathEscape = errors.New("path leaves the declared directory")

// ErrRepoReadPathGrammar is a path whose segments are outside the address
// grammar repo.read accepts.
var ErrRepoReadPathGrammar = errors.New("path is outside the read grammar")

// ErrRepoReadWrongExt is a path whose extension does not match what the
// caller declared readable: ".md" for the plain-string form of "reads", the
// listed extensions for the object form. An ADR is prose, not a place this
// tool would ever return a script or a credential from.
var ErrRepoReadWrongExt = errors.New("file extension is not one the manifest declares readable")

// repoReadSegmentRe is the grammar a path segment under a declared read
// directory follows.
var repoReadSegmentRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ErrRepoReadDirInvalid is a declared read directory that is not a plain
// relative path below the registry root: ".", "..", a hidden segment, an
// absolute path, or one that resolves outside the root through a symlink.
var ErrRepoReadDirInvalid = errors.New("declared read directory is not a directory below the repository root")

// ErrRepoReadTooLarge is a file above RepoReadMaxBytes. repo.read serves
// prose; a file this size is not one, and reading it whole into a JSON
// answer would hold it in memory twice.
var ErrRepoReadTooLarge = errors.New("file is larger than the read limit")

// RepoReadMaxBytes caps one repo.read answer.
const RepoReadMaxBytes = 1 << 20

// repoReadDirSegmentRe is the grammar of one segment of a directory a
// manifest declares under "reads". It must stay equal to the "reads" item
// pattern in schema/plugin-manifest.schema.json — the schema refuses at
// install, this refuses at every call, because a manifest on disk can change
// after install and a bundled one never passes the schema at all.
var repoReadDirSegmentRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

// DefaultReadExt is the only extension the plain-string form of "reads"
// allows. Nothing that existed before the object form changes meaning.
const DefaultReadExt = ".md"

// repoReadExtRe is the grammar of one extension in the object form of
// "reads". It must stay equal to the "ext" item pattern in
// schema/plugin-manifest.schema.json: the schema refuses at install, this
// refuses at every call.
var repoReadExtRe = regexp.MustCompile(`^\.[a-z0-9]{1,10}$`)

// repoReadDeniedExts are extensions no manifest may declare readable, even
// though they pass the grammar. Nothing here executes what it reads, so a
// script extension is not the concern; the concern is a file that
// conventionally holds a secret, which a directory of rules or notes has no
// reason to contain and which a plugin should not be handed by naming it. The schema carries the same list in "not.enum".
var repoReadDeniedExts = []string{".env", ".pem", ".key", ".p12", ".pfx", ".kdbx", ".htpasswd"}

// ValidReadExt reports whether ext is an extension a manifest may declare
// under the object form of "reads".
func ValidReadExt(ext string) bool {
	if !repoReadExtRe.MatchString(ext) {
		return false
	}
	for _, d := range repoReadDeniedExts {
		if ext == d {
			return false
		}
	}
	return true
}

// ReadDecl is one entry of a manifest's "reads": a directory and the
// extensions readable in it. The plain-string form "docs/adr" is a ReadDecl
// with no Ext and means DefaultReadExt only; the object form
// {"dir": "rules", "ext": [".dmn"]} names its extensions.
type ReadDecl struct {
	Dir string
	// Ext is empty for the string form. Use Exts for the effective list.
	Ext []string
	// object remembers which form was parsed so a re-marshalled manifest
	// keeps the form it came in.
	object bool
}

// Exts is the effective extension list: DefaultReadExt for the string form.
func (d ReadDecl) Exts() []string {
	if !d.object {
		return []string{DefaultReadExt}
	}
	return d.Ext
}

// String is how a reviewer is shown the entry when asked to approve it.
func (d ReadDecl) String() string {
	if !d.object {
		return d.Dir
	}
	return d.Dir + " (" + strings.Join(d.Ext, ", ") + ")"
}

// UnmarshalJSON accepts the string form and the object form and nothing else.
func (d *ReadDecl) UnmarshalJSON(raw []byte) error {
	var dir string
	if err := json.Unmarshal(raw, &dir); err == nil {
		*d = ReadDecl{Dir: dir}
		return nil
	}
	var obj struct {
		Dir string   `json:"dir"`
		Ext []string `json:"ext"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&obj); err != nil {
		return fmt.Errorf("a \"reads\" entry is a directory string or {\"dir\", \"ext\"}: %w", err)
	}
	*d = ReadDecl{Dir: obj.Dir, Ext: obj.Ext, object: true}
	return nil
}

// MarshalJSON writes the form the entry came in.
func (d ReadDecl) MarshalJSON() ([]byte, error) {
	if !d.object {
		return json.Marshal(d.Dir)
	}
	return json.Marshal(struct {
		Dir string   `json:"dir"`
		Ext []string `json:"ext"`
	}{d.Dir, d.Ext})
}

// ReadExtsFor answers the extensions a manifest allows in dir: the union over
// every entry naming dir, so one directory declared twice is allowed what
// both say. It is empty when no entry names dir — the caller refuses that —
// and an error when an entry for dir carries an extension outside the
// grammar, which the schema refused at install but a manifest on disk can
// change after it did.
func ReadExtsFor(reads []ReadDecl, dir string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, d := range reads {
		if d.Dir != dir {
			continue
		}
		exts := d.Exts()
		if len(exts) == 0 {
			return nil, fmt.Errorf("%w: %q declares no extension", ErrRepoReadWrongExt, dir)
		}
		for _, e := range exts {
			if !ValidReadExt(e) {
				return nil, fmt.Errorf("%w: %q is not an extension a manifest may declare", ErrRepoReadWrongExt, e)
			}
			if !seen[e] {
				seen[e] = true
				out = append(out, e)
			}
		}
	}
	return out, nil
}

// ValidReadDir reports whether rel is a directory a manifest may declare
// under "reads": relative, slash-separated, and every segment in grammar.
func ValidReadDir(rel string) bool {
	if rel == "" {
		return false
	}
	for _, segment := range strings.Split(rel, "/") {
		if !repoReadDirSegmentRe.MatchString(segment) {
			return false
		}
	}
	return true
}

// ResolveReadDir turns a declared read directory into an absolute path under
// root, refusing one outside the grammar and one whose resolved location
// leaves root — a declared "docs/adr" that is a symlink to $HOME passes the
// grammar and would otherwise hand out every .md file it points at.
func ResolveReadDir(root, rel string) (string, error) {
	if !ValidReadDir(rel) {
		return "", fmt.Errorf("%w: %q", ErrRepoReadDirInvalid, rel)
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	// Let os.Root enforce containment, including dangling and swapped links.
	// A nearest-existing-ancestor check can accept a dangling escape whose
	// target appears later. An absent ordinary directory is allowed.
	repo, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer repo.Close()
	sub, err := repo.OpenRoot(filepath.FromSlash(rel))
	switch {
	case err == nil:
		sub.Close()
	case errors.Is(err, os.ErrNotExist):
	default:
		return "", fmt.Errorf("%w: %q resolves outside the repository: %v", ErrRepoReadDirInvalid, rel, err)
	}
	return abs, nil
}

// openBeneath opens rel inside dir through os.Root: every component,
// symlinks included, is resolved by the kernel inside dir, so neither a link
// whose target cannot be resolved (a /proc/self/fd magic link, a dangling
// link whose target appears later) nor a link swapped between check and open
// leads outside it — the string check in ResolveRepoReadPath alone does not
// hold that.
func openBeneath(dir, rel string) (*os.File, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	name := filepath.FromSlash(rel)
	// Stat before Open: opening a FIFO for reading blocks until a writer
	// appears, and a listing that hung on one would hang the caller.
	info, err := root.Stat(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRepoReadPathEscape, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %q is not a regular file", ErrRepoReadPathGrammar, rel)
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRepoReadPathEscape, err)
	}
	return f, nil
}

// ResolveRepoReadPath turns a caller-supplied relative path into an absolute
// one inside dir, refusing everything a host write path would refuse about a
// path, with dir as the root.
func ResolveRepoReadPath(dir, rel, ext string) (string, error) {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" {
		return "", fmt.Errorf("%w: empty path", ErrRepoReadPathGrammar)
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("%w: %q is absolute", ErrRepoReadPathEscape, rel)
	}
	for _, segment := range strings.Split(rel, "/") {
		if segment == ".." {
			return "", fmt.Errorf("%w: %q", ErrRepoReadPathEscape, rel)
		}
	}
	if got := filepath.Ext(rel); got != ext {
		return "", fmt.Errorf("%w: %q ends in %q", ErrRepoReadWrongExt, rel, got)
	}
	segments := strings.Split(rel, "/")
	for i, segment := range segments {
		name := segment
		if i == len(segments)-1 {
			name = strings.TrimSuffix(segment, ext)
		}
		if !repoReadSegmentRe.MatchString(name) {
			return "", fmt.Errorf("%w: %q", ErrRepoReadPathGrammar, segment)
		}
	}
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	if !WithinResolved(dir, abs) {
		return "", fmt.Errorf("%w: %q", ErrRepoReadPathEscape, rel)
	}
	return abs, nil
}

// ReadRepoFile reads one file under a declared directory, refusing anything
// ResolveRepoReadPath refuses about the path.
func ReadRepoFile(dir, rel, ext string) (string, error) {
	abs, err := ResolveRepoReadPath(dir, rel, ext)
	if err != nil {
		return "", err
	}
	_ = abs
	f, err := openBeneath(dir, rel)
	if err != nil {
		return "", err
	}
	defer f.Close()
	// One byte past the limit is read so "exactly at the limit" and "above
	// it" stay distinguishable without a separate stat that could race.
	body, err := io.ReadAll(io.LimitReader(f, RepoReadMaxBytes+1))
	if err != nil {
		return "", err
	}
	if len(body) > RepoReadMaxBytes {
		return "", fmt.Errorf("%w: %q is above %d bytes", ErrRepoReadTooLarge, rel, RepoReadMaxBytes)
	}
	return string(body), nil
}

// ReadRepoFileExts is ReadRepoFile for a directory that allows several
// extensions: the file's own extension must be one of exts.
func ReadRepoFileExts(dir, rel string, exts []string) (string, error) {
	got := filepath.Ext(filepath.ToSlash(strings.TrimSpace(rel)))
	for _, e := range exts {
		if e == got {
			return ReadRepoFile(dir, rel, got)
		}
	}
	return "", fmt.Errorf("%w: %q ends in %q", ErrRepoReadWrongExt, rel, got)
}

// ListRepoFilesExts is ListRepoFiles for several extensions, merged and
// sorted. It offers exactly what ReadRepoFileExts reads.
func ListRepoFilesExts(dir string, exts []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, e := range exts {
		names, err := ListRepoFiles(dir, e)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// ListRepoFiles lists every file with the given extension directly under a
// declared directory, sorted, relative and slash-separated.
func ListRepoFiles(dir, ext string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if _, err := ResolveRepoReadPath(dir, e.Name(), ext); err != nil {
			continue
		}
		// The same open the read does, so a link out of the directory, a
		// link to a directory or a FIFO is left out here instead of listed
		// and then refused.
		f, err := openBeneath(dir, e.Name())
		if err != nil {
			continue
		}
		f.Close()
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}
