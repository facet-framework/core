// @facet: registry/layout/glossary-tools
// @facet-role: primary

package facet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Named glossary refusal errors let callers distinguish failure conditions
// without matching diagnostic text.
var (
	// ErrGlossaryUndeclaredCategory refuses a category not declared in the
	// file, preventing a typo from creating a separate implicit group.
	ErrGlossaryUndeclaredCategory = errors.New("the glossary does not declare that category")
	// ErrGlossaryNoSuchTerm refuses renames and removals of undefined terms;
	// no change is reported for a term the glossary does not contain.
	ErrGlossaryNoSuchTerm = errors.New("the glossary does not define that term")
	// ErrGlossaryTermExists refuses a rename onto an existing term, preserving
	// both definitions rather than selecting one by overwrite.
	ErrGlossaryTermExists = errors.New("the glossary already defines that term")
	// ErrGlossaryEmptyTerm rejects blank names or definitions at the write
	// boundary, enforcing the schema requirement before changing the file.
	ErrGlossaryEmptyTerm = errors.New("a term needs a name and a definition")
)

// GlossarySession is one open branch and the glossary on it.
type GlossarySession struct {
	*BranchSession
	// GlossaryPath is the glossary inside the worktree. The only path any
	// glossary write resolves to: no caller supplies one, so no caller can name
	// another file.
	GlossaryPath string
	// glossaryRel is that path relative to the repository root, for the commit.
	glossaryRel string
}

// OpenGlossaryBranch opens a branch to write a glossary change on.
func OpenGlossaryBranch(reg Registry, branch string) (*GlossarySession, error) {
	session, err := OpenBranchSession(reg.Root, branch)
	if err != nil {
		return nil, err
	}
	rel := reg.Rel(reg.GlossaryPath())
	return &GlossarySession{
		BranchSession: session,
		GlossaryPath:  filepath.Join(session.Dir, filepath.FromSlash(rel)),
		glossaryRel:   rel,
	}, nil
}

// Commit records the glossary change on the branch.
func (s *GlossarySession) Commit(message string) (CommitResult, error) {
	if strings.TrimSpace(message) == "" {
		return CommitResult{}, errors.New("a commit needs a message")
	}
	spec := s.glossaryRel
	if out, err := runGitIn(s.Dir, "add", "--", spec); err != nil {
		return CommitResult{}, fmt.Errorf("could not stage the glossary: %v: %s", err, out)
	}

	if _, err := runGitIn(s.Dir, "diff", "--cached", "--quiet", "--", spec); err == nil {
		return CommitResult{}, fmt.Errorf("%w: nothing was written to %s on this branch",
			ErrNothingToCommit, spec)
	}
	if out, err := runGitIn(s.Dir, "commit", "-m", message, "--", spec); err != nil {
		return CommitResult{}, fmt.Errorf("could not commit: %v: %s", err, out)
	}
	sha, err := runGitIn(s.Dir, "rev-parse", "HEAD")
	if err != nil {
		return CommitResult{}, err
	}
	return CommitResult{SHA: strings.TrimSpace(sha), Files: []string{spec}}, nil
}

// GlossaryEdited reports what a glossary write did.
type GlossaryEdited struct {
	// Diff contains the proposed unified change required by FACET §6.4,
	// allowing the caller to present it for review.
	Diff string `json:"diff"`
	// Before and After are the file's bytes, so a caller needing the content
	// does not have to read it back off the branch.
	Before string `json:"-"`
	After  string `json:"-"`
}

// SetGlossaryTerm defines a term or changes the one that is there.
func SetGlossaryTerm(path, term, definition, category string) (GlossaryEdited, error) {
	term, definition = strings.TrimSpace(term), strings.TrimSpace(definition)
	category = strings.TrimSpace(category)
	if term == "" || definition == "" {
		return GlossaryEdited{}, ErrGlossaryEmptyTerm
	}

	g, before, err := readGlossaryFile(path)
	if err != nil {
		return GlossaryEdited{}, err
	}
	// Checked before anything is written, and against the file's own list: the
	// set of correct answers is in the same file, so a reference outside it is a
	// typo or a category somebody deleted, and neither is a judgement call.
	if category != "" && !g.CategoryDeclared(category) {
		return GlossaryEdited{}, fmt.Errorf("%w: %q — declare it under 'categories' first, or leave the term uncategorised",
			ErrGlossaryUndeclaredCategory, category)
	}

	replaced := false
	for i := range g.Terms {
		if strings.TrimSpace(g.Terms[i].Term) != term {
			continue
		}
		if replaced {
			// Preserve duplicate definitions for the linter to report; merging them
			// here would silently choose which definition holds.
			continue
		}
		g.Terms[i].Term = term
		g.Terms[i].Definition = definition
		g.Terms[i].Category = category
		replaced = true
	}
	if !replaced {
		g.Terms = append(g.Terms, GlossaryTerm{Term: term, Category: category, Definition: definition})
	}
	return writeGlossaryFile(path, before, g)
}

// GlossaryRenamed reports what a rename did, including what it could not do.
type GlossaryRenamed struct {
	GlossaryEdited
	From string `json:"from"`
	To   string `json:"to"`
	// StillSaying names what still writes the old spelling: record ids first,
	// then the paths a registered prose source names, each in the order the
	// registry holds them.
	StillSaying []string `json:"stillSaying"`
}

// RenameGlossaryTerm changes a term's spelling, and reports what that orphaned.
func RenameGlossaryTerm(reg Registry, path, from, to string) (GlossaryRenamed, error) {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	if from == "" || to == "" {
		return GlossaryRenamed{}, ErrGlossaryEmptyTerm
	}

	g, before, err := readGlossaryFile(path)
	if err != nil {
		return GlossaryRenamed{}, err
	}

	defined := false
	for _, entry := range g.Terms {
		if strings.TrimSpace(entry.Term) == from {
			defined = true
		}
		if strings.TrimSpace(entry.Term) == to {
			return GlossaryRenamed{}, fmt.Errorf("%w: %q", ErrGlossaryTermExists, to)
		}
	}
	if !defined {
		return GlossaryRenamed{}, fmt.Errorf("%w: %q", ErrGlossaryNoSuchTerm, from)
	}

	// Read before the write, because afterwards the old spelling is no longer a
	// defined term and FindTerms would have nothing to look for.
	orphaned := recordsSaying(reg, from)

	for i := range g.Terms {
		if strings.TrimSpace(g.Terms[i].Term) == from {
			g.Terms[i].Term = to
		}
	}
	edited, err := writeGlossaryFile(path, before, g)
	if err != nil {
		return GlossaryRenamed{}, err
	}
	return GlossaryRenamed{GlossaryEdited: edited, From: from, To: to, StillSaying: orphaned}, nil
}

// RemoveGlossaryTerm undefines a term.
func RemoveGlossaryTerm(path, term string) (GlossaryEdited, error) {
	term = strings.TrimSpace(term)
	if term == "" {
		return GlossaryEdited{}, ErrGlossaryEmptyTerm
	}

	g, before, err := readGlossaryFile(path)
	if err != nil {
		return GlossaryEdited{}, err
	}
	kept := make([]GlossaryTerm, 0, len(g.Terms))
	for _, entry := range g.Terms {
		if strings.TrimSpace(entry.Term) == term {
			continue
		}
		kept = append(kept, entry)
	}
	if len(kept) == len(g.Terms) {
		return GlossaryEdited{}, fmt.Errorf("%w: %q", ErrGlossaryNoSuchTerm, term)
	}

	g.Terms = kept
	return writeGlossaryFile(path, before, g)
}

// ProseSource names the files of a product's prose, beside the records, whose
// text still writes term, as paths relative to the repository root. A source
// that cannot read its prose names nothing: the rename that asks has already
// succeeded, and an unreadable file is a finding for that product's own check.
type ProseSource func(reg Registry, term string) []string

var proseSources struct {
	sync.Mutex
	sources []ProseSource
}

// RegisterProseSource adds a product's prose to what a glossary rename
// reports. A build registers from init; a build that links no such product
// reports the records alone.
func RegisterProseSource(source ProseSource) {
	if source == nil {
		panic("facet: RegisterProseSource needs a source")
	}
	proseSources.Lock()
	defer proseSources.Unlock()
	proseSources.sources = append(proseSources.sources, source)
}

func registeredProseSources() []ProseSource {
	proseSources.Lock()
	defer proseSources.Unlock()
	return append([]ProseSource(nil), proseSources.sources...)
}

// recordsSaying returns what still writes a term after it stops being one: the
// ids of the records, then the paths the registered prose sources name, each
// in the order the registry holds them.
func recordsSaying(reg Registry, term string) []string {
	var out []string

	if records, err := reg.LoadFeatures(); err == nil {
		for _, rec := range records {
			for _, field := range scannedProse(rec) {
				if len(FindTerms(field.Text, []string{term})) > 0 {
					out = append(out, rec.ID)
					break
				}
			}
		}
	}

	for _, source := range registeredProseSources() {
		out = append(out, source(reg, term)...)
	}
	return out
}

// readGlossaryFile loads the glossary and keeps its bytes for the diff.
func readGlossaryFile(path string) (*Glossary, string, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, "", err
	}
	g, err := LoadGlossary(path)
	if err != nil {
		// Said plainly rather than repaired. A file that will not parse has
		// content somebody wrote, and writing a rendered version over it would
		// discard whatever the parser could not read.
		return nil, "", fmt.Errorf("the glossary could not be read: %w", err)
	}
	return g, string(data), nil
}

// writeGlossaryFile renders the glossary and writes it, answering with the diff.
func writeGlossaryFile(path, before string, g *Glossary) (GlossaryEdited, error) {
	after := RenderGlossary(g)
	if after == before {
		// An identical file is not written, preserving its mtime; the result
		// reports that the proposed edit changed nothing.
		return GlossaryEdited{Before: before, After: after}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return GlossaryEdited{}, err
	}
	if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
		return GlossaryEdited{}, err
	}
	return GlossaryEdited{
		Diff:   UnifiedDiff(filepath.Base(path), before, after),
		Before: before,
		After:  after,
	}, nil
}

// RenderGlossary writes a glossary back out.
func RenderGlossary(g *Glossary) string {
	var b strings.Builder
	version := strings.TrimSpace(g.SchemaVersion)
	if version == "" {
		// The only value the schema's enum allows. Written rather than left
		// empty because a file without it fails validation, and a project whose
		// first term arrives through a tool would get an invalid file.
		version = GlossarySchemaVersion
	}
	fmt.Fprintf(&b, "schema_version: %s\n", yamlScalar(version))

	if len(g.Categories) > 0 {
		b.WriteString("categories:\n")
		for _, c := range g.Categories {
			fmt.Fprintf(&b, "  - id: %s\n", yamlScalar(c.ID))
			fmt.Fprintf(&b, "    title: %s\n", yamlScalar(c.Title))
			if strings.TrimSpace(c.Purpose) != "" {
				writeGlossaryBlock(&b, "    ", "purpose", c.Purpose)
			}
		}
	}

	b.WriteString("terms:\n")
	for _, t := range g.Terms {
		fmt.Fprintf(&b, "  - term: %s\n", yamlScalar(t.Term))
		if strings.TrimSpace(t.Category) != "" {
			fmt.Fprintf(&b, "    category: %s\n", yamlScalar(t.Category))
		}
		writeGlossaryBlock(&b, "    ", "definition", t.Definition)
		if len(t.Renderings) > 0 {
			b.WriteString("    renderings:\n")
			for _, locale := range sortedKeys(t.Renderings) {
				fmt.Fprintf(&b, "      %s: %s\n", locale, yamlScalar(t.Renderings[locale]))
			}
		}
	}
	return b.String()
}

// GlossarySchemaVersion is the schema family a glossary written here declares.
const GlossarySchemaVersion = "0.1"

// writeGlossaryBlock writes a multi-line value as a block scalar.
func writeGlossaryBlock(b *strings.Builder, indent, key, value string) {
	value = strings.TrimRight(value, "\n")
	fmt.Fprintf(b, "%s%s: |\n", indent, key)
	for _, line := range strings.Split(value, "\n") {
		if strings.TrimSpace(line) == "" {
			b.WriteString("\n")
			continue
		}
		fmt.Fprintf(b, "%s  %s\n", indent, line)
	}
}

// sortedKeys returns a map's keys in a stable order, so the same glossary
// renders the same bytes on every run (§6.3).
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
