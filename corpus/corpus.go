// Package corpus reads FACET registries into shareable snapshots with progress.
// It owns the wire data every consumer of the corpus receives, the CLI snapshot
// among them.
//
// @facet: registry/layout/source-scanning
// @facet-role: primary

package corpus

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/facet-framework/core/registry"
)

// Corpus carries configuration, record paths, verbatim record YAML and
// computed host data. Records remain unparsed for the consumer to interpret
// with its own parser and model.
type Corpus struct {
	SourceView *SourceViewSnapshot `json:"sourceView,omitempty"`
	Facetrc    string              `json:"facetrc"`
	Index      string              `json:"index"`
	Records    map[string]string   `json:"records"`
	Scan       Scan                `json:"scan"`
	// Taxonomy contains verbatim project taxonomy YAML. Empty means no
	// taxonomy was declared, not a declared taxonomy with no domains.
	Taxonomy string `json:"taxonomy,omitempty"`
	// Glossary contains verbatim terminology YAML. Empty means no glossary
	// was declared; consumers leave prose untouched in that case.
	Glossary string `json:"glossary,omitempty"`
	// GlossaryGroups shares facet.Grouped() with the linter to keep ordering
	// consistent: declared categories in file order
	// (including empty ones), then sorted undeclared IDs, then uncategorised
	// terms without a heading. Nil omits the key when no grouping is declared;
	// an absent key does not assert that a grouping produced an empty list.
	GlossaryGroups []GlossaryGroup `json:"glossaryGroups,omitempty"`
	// Calibration is a computed host report using the normative bands from
	// FACET §11.4, shared with consumers to keep those measurements consistent.
	Calibration *facet.Calibration `json:"calibration,omitempty"`
	// SharedClaims lists the specification cases, symbols and scenarios this
	// registry shares with other claims. Incomplete names any record or source
	// the measurement could not read; an empty Incomplete means the lists are
	// complete.
	SharedClaims *facet.SharedClaims `json:"sharedClaims,omitempty"`
	// sections are what linked products contribute, keyed by the top-level
	// name each is serialised under. Unexported and outside the struct's own
	// encoding, because Core does not know their shapes: MarshalJSON appends
	// them after Core's fields.
	sections map[string]any
}

// GlossaryGroup is one heading of the grouped glossary, as the consumer receives
// it: the category's own wording, whether the file declares it, and the names
// of the terms under it.
type GlossaryGroup struct {
	// ID is the category id, or "" for the terms that name none. The empty id
	// is the state, not a missing value: those terms are uncategorised, and
	// naming them here would put a category the project never declared into
	// every surface.
	ID string `json:"id"`
	// Title and Purpose are the declared wording, empty when Declared is false
	// — there is none to show for a category the file does not declare, and
	// inventing one here invents it for every surface at once.
	Title   string `json:"title,omitempty"`
	Purpose string `json:"purpose,omitempty"`
	// Declared reports whether the file declares this category. It is what
	// separates the two states that both carry no title: an undeclared id
	// (Declared false, ID set) is a term whose category was deleted or
	// mistyped, and the linter reports it as an error; the uncategorised group
	// (Declared false, ID empty) is a decision nobody made.
	Declared bool `json:"declared"`
	// Terms are the names under this heading, in the order the file lists them.
	// Empty under a declared category is the fourth drawn state: declared, and
	// no term names it.
	Terms []string `json:"terms"`
}

// glossaryGroups puts facet.Grouped() into the shape the consumer receives.
func glossaryGroups(reg facet.Registry) []GlossaryGroup {
	g, err := facet.LoadGlossary(reg.GlossaryPath())
	if err != nil {
		return nil
	}
	grouped := g.Grouped()
	if grouped == nil {
		return nil
	}
	out := make([]GlossaryGroup, 0, len(grouped))
	for _, group := range grouped {
		terms := make([]string, 0, len(group.Terms))
		for _, term := range group.Terms {
			terms = append(terms, term.Term)
		}
		out = append(out, GlossaryGroup{
			ID:       group.ID,
			Title:    group.Title,
			Purpose:  group.Purpose,
			Declared: group.Declared,
			Terms:    terms,
		})
	}
	return out
}

// Scan is host material: everything the consumer shows that no record declares.
// The consumer marks these values as coming from the scan, so the split matters.
type Scan struct {
	ScannedAt string `json:"scannedAt"`
	Root      string `json:"root"`
	Registry  string `json:"registry,omitempty"`
	TotalLoc  int    `json:"totalLoc"`
	Files     int    `json:"files"`
	// AnchoredFiles and UnanchoredFiles partition this registry's scanned
	// source files by presence of a belonging anchor, with per-file line counts.
	// The split is a map, not a finding or gate (spec §9.3).
	AnchoredFiles   []ScannedFile       `json:"anchoredFiles"`
	UnanchoredFiles []ScannedFile       `json:"unanchoredFiles"`
	Anchors         map[string][]Anchor `json:"anchors"`
	OrphanAnchors   []Orphan            `json:"orphanAnchors"`
	// Drift is anchored source that changed while its record did not — the
	// signal FACET spec 12.1 describes, and the same one the Stop hook reports.
	Drift []facet.Drift `json:"drift"`
	// DriftBaseline is what Drift and ReferenceChanges were measured against.
	// It travels with them so the consumer can say "no review recorded" or
	// "could not be determined" instead of presenting an empty list as
	// confirmed freshness.
	DriftBaseline     facet.Baseline             `json:"driftBaseline"`
	DriftRepositories []facet.RepositoryBaseline `json:"driftRepositories,omitempty"`
	// ReferenceChanges are the implementation associations a deletion, a
	// rename or a removed anchor moved or lost — including records whose
	// source file is gone, which no current anchor can name any more.
	ReferenceChanges []facet.ReferenceChange `json:"referenceChanges"`
	// LastChanged is the day of the newest commit that touched each record
	// file and each anchored file, relative to the registry root. The consumer
	// sorts what records say by it. Absent (nil, omitted) when there is no
	// usable history — no git, a failing git or a shallow clone — which is
	// different from an empty map: the dates were not measured.
	LastChanged map[string]string `json:"lastChanged,omitempty"`
}

// ScannedFile is one source file the scan read, named relative to the
// registry root, with the line count the scan already had to compute to
// produce TotalLoc.
type ScannedFile struct {
	Path string `json:"path"`
	Loc  int    `json:"loc"`
}

// Anchor is one @facet: tag, as the consumer draws it.
type Anchor struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Role string `json:"role"`
	// Excerpt is the first line of code below the tag — what the anchor points
	// at, quoted rather than summarised.
	Excerpt string `json:"excerpt,omitempty"`
}

// Orphan is an anchor whose id no record in this registry answers for.
type Orphan struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	ID   string `json:"id"`
}

// Status is one report from a run in progress.
type Status struct {
	Phase    string `json:"phase"`
	File     string `json:"file,omitempty"`
	Done     int    `json:"done,omitempty"`
	Total    int    `json:"total,omitempty"`
	Records  int    `json:"records,omitempty"`
	Files    int    `json:"files,omitempty"`
	Anchors  int    `json:"anchors,omitempty"`
	Root     string `json:"root,omitempty"`
	Registry string `json:"registry,omitempty"`
}

// Report receives progress synchronously from the worker goroutine.
// A blocking callback delays the run and provides backpressure.
type Report func(Status)

// updateSteps caps how many progress reports one phase sends.
const updateSteps = 200

// enumerateStride is how often the counting phase reports. It counts up without
// knowing where it stops, so it reports every so many files rather than every
// so many percent.
const enumerateStride = 64

// Build reads a registry into a corpus, reporting progress as it goes.
func Build(reg facet.Registry, report Report) (*Corpus, error) {
	if err := reg.ValidateSourceView(); err != nil {
		return nil, err
	}
	if report == nil {
		report = func(Status) {}
	}
	base := Status{Root: reg.Root, Registry: reg.Name}

	// --- configuration -----------------------------------------------------
	config := base
	config.Phase = "config"
	config.File = facet.ConfigName
	report(config)

	facetrc, err := readConfig(reg)
	if err != nil {
		return nil, err
	}

	// --- records -----------------------------------------------------------
	paths, err := recordPaths(reg)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no FACET records under %s — add records there or use --root to select a project with records", reg.Rel(reg.RecordsDir))
	}

	records := make(map[string]string, len(paths))
	ids := make(map[string]bool, len(paths))
	rels := make([]string, 0, len(paths))
	for i, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		rel := reg.Rel(path)
		records[rel] = string(data)
		rels = append(rels, rel)
		if id := recordID(data); id != "" {
			ids[id] = true
		}

		step := base
		step.Phase = "records"
		step.File = rel
		step.Done = i + 1
		step.Total = len(paths)
		step.Records = i + 1
		report(step)
	}

	// --- counting the source tree ------------------------------------------
	// The count has no denominator yet — finding it is the point of this phase —
	// so reports go out on a fixed stride rather than a fraction of a total.
	sources, identities, err := enumerateSources(reg, func(found int, path string) {
		if found%enumerateStride != 0 {
			return
		}
		step := base
		step.Phase = "enumerate"
		step.File = filepath.Base(path)
		step.Done = found
		step.Records = len(paths)
		report(step)
	})
	if err != nil {
		return nil, err
	}

	// The last file rarely lands on the stride, and the final count is the one
	// number this phase exists to produce.
	counted := base
	counted.Phase = "enumerate"
	counted.Done = len(sources)
	counted.Records = len(paths)
	report(counted)

	// --- searching it for anchors -------------------------------------------
	scan := Scan{
		ScannedAt: time.Now().UTC().Format(time.RFC3339),
		Root:      reg.Root,
		Registry:  reg.Name,
		Anchors:   map[string][]Anchor{},
	}

	if reg.SourceView != nil {
		scan.Root = reg.SourceView.RegistryRepository
	}
	every := len(sources)/updateSteps + 1
	var anchorCount int
	var orphans []Orphan
	var found []facet.Anchor
	var anchoredFiles, unanchoredFiles []ScannedFile
	var walked []facet.SourceFile
	// Sources this run could not read: the back-list names them, so a claim
	// they would have carried is not silently missing from a complete list.
	var unread []facet.MeasurementGap

	for i, path := range sources {
		data, err := os.ReadFile(path)
		if err != nil {
			// An unreadable file is not a reason to abandon the run.
			unread = append(unread, facet.UnreadSource(reg.Name, corpusSourcePath(reg, path, identities)))
			continue
		}
		loc := countLines(data)
		scan.TotalLoc += loc

		// anchoredHere tracks the same registry-ownership test the anchors
		// below are already filtered by: a file with only a foreign-registry
		// tag is not this registry's to claim, so it belongs on the
		// unanchored side of this registry's own split.
		anchoredHere := false
		for _, a := range facet.ScanContent(path, data) {
			if !a.BelongsTo(reg.Name) {
				continue
			}
			anchoredHere = true
			anchorCount++
			if source, ok := identities[path]; ok {
				a.Repository = source.Repository
				a.Reference = source.Reference
			}
			found = append(found, a)
			entry := Anchor{
				Path:    corpusSourcePath(reg, a.Path, identities),
				Line:    a.Line,
				Role:    a.Role,
				Excerpt: excerpt(data, a.Line),
			}
			if ids[a.ID] {
				scan.Anchors[a.ID] = append(scan.Anchors[a.ID], entry)
			} else {
				orphans = append(orphans, Orphan{Path: entry.Path, Line: a.Line, ID: a.ID})
			}
		}

		file := ScannedFile{Path: corpusSourcePath(reg, path, identities), Loc: loc}
		if anchoredHere {
			anchoredFiles = append(anchoredFiles, file)
		} else {
			unanchoredFiles = append(unanchoredFiles, file)
		}
		// Kept in the scanner's own shape so the calibration below can ask it
		// the one question the split above cannot answer:
		// which unanchored files declared themselves out rather than simply
		// having nobody claim them.
		walked = append(walked, facet.SourceFile{
			Path:       path,
			Repository: identities[path].Repository, Reference: identities[path].Reference,
			Loc:      loc,
			Anchored: anchoredHere,
			OptedOut: facet.DeclaresOptOut(data),
		})

		if (i+1)%every == 0 || i+1 == len(sources) {
			step := base
			step.Phase = "index"
			step.File = corpusSourcePath(reg, path, identities)
			step.Done = i + 1
			step.Total = len(sources)
			step.Records = len(paths)
			step.Files = i + 1
			step.Anchors = anchorCount
			report(step)
		}
	}

	scan.Files = len(sources)
	scan.AnchoredFiles = anchoredFiles
	if scan.AnchoredFiles == nil {
		scan.AnchoredFiles = []ScannedFile{}
	}
	scan.UnanchoredFiles = unanchoredFiles
	if scan.UnanchoredFiles == nil {
		scan.UnanchoredFiles = []ScannedFile{}
	}
	scan.OrphanAnchors = orphans
	if scan.OrphanAnchors == nil {
		scan.OrphanAnchors = []Orphan{}
	}

	cs := facet.LoadSourceChangeSet(reg.Root, []facet.Registry{reg}, "")
	scan.Drift, scan.ReferenceChanges = facet.RegistryDrift(&cs, reg, found, cs.FollowedRecords([]facet.Registry{reg}), ids)
	scan.DriftBaseline = cs.Baseline
	scan.DriftRepositories = cs.RepositoryBaselines()
	if scan.Drift == nil {
		scan.Drift = []facet.Drift{}
	}
	if scan.ReferenceChanges == nil {
		scan.ReferenceChanges = []facet.ReferenceChange{}
	}

	// Last-change days for the files the consumer can name: every record and
	// every anchored file. Restricted rather than the whole history, so the
	// payload grows with the registry and not with the repository.
	if all := facet.LastChanged(reg.Root); all != nil {
		scan.LastChanged = map[string]string{}
		for _, rel := range rels {
			if day, ok := all[rel]; ok {
				scan.LastChanged[rel] = day
			}
		}
		for _, file := range scan.AnchoredFiles {
			if day, ok := all[file.Path]; ok {
				scan.LastChanged[file.Path] = day
			}
		}
	}

	if err := reg.ValidateSourceView(); err != nil {
		return nil, err
	}
	corpus := &Corpus{SourceView: sourceViewSnapshot(reg, records), Facetrc: facetrc, Index: buildIndex(rels), Records: records, Scan: scan}

	if data, err := os.ReadFile(reg.TaxonomyPath()); err == nil {
		corpus.Taxonomy = string(data)
	}
	if data, err := os.ReadFile(reg.GlossaryPath()); err == nil {
		corpus.Glossary = string(data)

		corpus.GlossaryGroups = glossaryGroups(reg)
	}
	// Best-effort for the same reason as the two above: a section a linked
	// product cannot build is left out rather than costing the rest of the
	// corpus.
	corpus.sections = buildSections(reg)
	// From what this run already read. Calibrate would walk the whole tree for
	// anchors a second time, without progress and right after the consumer was
	// told the count was finished — doubling the scan and turning the pause
	// into something that looks like a hang.
	features, err := reg.LoadFeatures()
	if err == nil {
		if cal, calErr := facet.CalibrateFrom(reg, features, found, walked); calErr == nil {
			corpus.Calibration = &cal
		}
	}

	claimed, gaps := features, unread
	if err != nil {
		var missing []facet.MeasurementGap
		claimed, missing = facet.ClaimFeatures(reg)
		gaps = append(missing, unread...)
	}
	shared := facet.RegistrySharedClaims(reg, claimed, found, gaps...)
	corpus.SharedClaims = &shared

	return corpus, nil
}

// readConfig returns the project's .facetrc.yaml verbatim.
func readConfig(reg facet.Registry) (string, error) {
	data, err := os.ReadFile(facet.ConfigPath(reg.Root))
	switch {
	case err == nil:
		return string(data), nil
	case !os.IsNotExist(err):
		return "", err
	}

	var b strings.Builder
	b.WriteString("# This project has no " + facet.ConfigName + ".\n")
	b.WriteString("# Generated: the effective defaults, not a file in the repository.\n")
	fmt.Fprintf(&b, "records: %s\n", reg.Rel(reg.RecordsDir))
	fmt.Fprintf(&b, "schema: %s\n", reg.Rel(reg.SchemaDir))
	if len(reg.Sources) > 0 {
		b.WriteString("sources:\n")
		for _, s := range reg.Sources {
			fmt.Fprintf(&b, "  - %s\n", s)
		}
	}
	return b.String(), nil
}

// recordPaths lists every record file of the registry, in category order.
func recordPaths(reg facet.Registry) ([]string, error) {
	var paths []string
	for _, category := range facet.Categories {
		dir := reg.CategoryDir(category)
		st, err := os.Stat(dir)
		if err != nil || !st.IsDir() {
			continue
		}
		err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && filepath.Ext(path) == ".yaml" {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(paths)
	return paths, nil
}

// buildIndex writes the record list the consumer reads first.
func buildIndex(rels []string) string {
	var b strings.Builder
	b.WriteString("records:\n")
	for _, rel := range rels {
		fmt.Fprintf(&b, "  - %s\n", rel)
	}
	return b.String()
}

// recordID pulls the id out of a record without parsing the whole document.
// The id is a top-level scalar in every FACET schema, so a line prefix is
// enough, and the consumer parses the document properly anyway.
func recordID(data []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "id:") {
			continue
		}
		return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "id:")), `"'`)
	}
	return ""
}

// sourceFiles returns the files the scan reads, applying repository ignore rules
// and registry sources, or the enumeration error. Counting first provides a
// measured progress denominator. Use NewSourceWalker so progress and anchor
// scans agree, including hidden directories.
func sourceFiles(reg facet.Registry, found func(int, string)) ([]string, error) {
	if reg.SourceView != nil {
		paths, _, err := enumerateSources(reg, found)
		return paths, err
	}
	roots := []string{reg.Root}
	if len(reg.Sources) > 0 {
		roots = roots[:0]
		for _, dir := range reg.Sources {
			p := dir
			if !filepath.IsAbs(p) {
				p = filepath.Join(reg.Root, p)
			}
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				roots = append(roots, p)
			}
		}
	}

	var paths []string
	for _, root := range roots {
		walker, files := facet.NewSourceWalker(root)
		gitFiles, useGit := facet.GitRelevantFiles(root)
		go func() { _ = walker.Start() }()

		for f := range files {
			if useGit && !gitFiles.Contains(f.Location) {
				continue
			}
			paths = append(paths, f.Location)
			found(len(paths), f.Location)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

// countLines counts the lines of a file the way a line counter would: a final
// line without a newline still counts.
func countLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	n := bytes.Count(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		n++
	}
	return n
}

// excerpt quotes the first line of code below an anchor.
func excerpt(data []byte, anchorLine int) string {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	line := 0
	for scanner.Scan() {
		line++
		if line <= anchorLine {
			continue
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.Contains(text, "@facet-role:") {
			continue
		}
		if len(text) > 120 {
			text = text[:120]
		}
		return text
	}
	return ""
}
