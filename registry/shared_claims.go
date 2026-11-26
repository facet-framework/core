package facet

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/facet-framework/core/internal/spec"
)

// SeverityInfo marks a finding that reports a fact for a person to judge. It
// never fails a run and is not a warning: shared use is often intended (one
// purchase case obliges both stock and invoicing), so a severity that asks for
// work would be wrong in exactly the cases that are fine.
const SeverityInfo Severity = "info"

// The four lint checks that report shared use. Each is its own category so a
// summary can count them apart (FACET §11.3).
const (
	CheckSharedSpecCase     = "shared-spec-case"
	CheckSharedSymbol       = "shared-symbol"
	CheckRepeatedPrimary    = "repeated-primary"
	CheckIdenticalScenarios = "identical-scenarios"
	// CheckSharedClaimsIncomplete names a part of the input the back-list
	// could not read. A warning, because the four lists may then lack entries.
	CheckSharedClaimsIncomplete = "shared-claims-incomplete"
)

// SharedClaims is the read-only back-list of everything more than one claim
// points at. It forbids nothing: a reader decides whether each entry is
// intended or one feature built twice.
type SharedClaims struct {
	// SpecCases lists every specification case referenced by more than one
	// scenario through specified_by.
	SpecCases []SharedSpecCase `json:"spec_cases"`
	// Symbols lists every source symbol anchored by more than one record.
	Symbols []SharedSymbol `json:"symbols"`
	// Primaries lists records whose primary anchors sit on more than one
	// symbol: the shape two implementations under one id take.
	Primaries []RepeatedPrimary `json:"primaries"`
	// Scenarios lists groups of scenarios in different records of one registry
	// whose normalised steps are identical.
	Scenarios []IdenticalScenarios `json:"scenarios"`
	// Incomplete lists what the measurement could not read. Empty means the
	// four lists are complete; an entry means they may lack what that file or
	// registry would have added.
	Incomplete []MeasurementGap `json:"incomplete"`
}

// MeasurementGap is one part of the input the back-list could not read.
type MeasurementGap struct {
	Registry string `json:"registry,omitempty"`
	Path     string `json:"path,omitempty"`
	Reason   string `json:"reason"`
}

// ClaimScenario names one scenario of one record.
type ClaimScenario struct {
	Registry string `json:"registry,omitempty"`
	Record   string `json:"record"`
	Scenario string `json:"scenario"`
	Path     string `json:"path,omitempty"`
	// SpecifiedBy is the scenario's specification address, when it has one.
	SpecifiedBy string `json:"specified_by,omitempty"`
}

// SharedSpecCase is one specification case and every scenario referencing it.
type SharedSpecCase struct {
	Address string `json:"address"`
	// Scope is the widest boundary the users cross: "record" when one record
	// references the case from several scenarios, "registry" when several
	// records of one registry do, "registries" when more than one registry does.
	Scope string          `json:"scope"`
	Users []ClaimScenario `json:"users"`
}

// ClaimAnchor is one anchor on a shared symbol.
type ClaimAnchor struct {
	Registry string `json:"registry,omitempty"`
	Record   string `json:"record"`
	Role     string `json:"role"`
	Line     int    `json:"line"`
}

// SharedSymbol is one declaration that anchors of several records attach to.
type SharedSymbol struct {
	Path string `json:"path"`
	// Line is the declaration's line, Symbol its declared name.
	Line    int           `json:"line"`
	Symbol  string        `json:"symbol"`
	Anchors []ClaimAnchor `json:"anchors"`
	// DoublePrimary is true when two or more records mark this symbol as their
	// primary implementation.
	DoublePrimary bool `json:"double_primary"`
}

// PrimarySite is one symbol a record's primary anchor attaches to.
type PrimarySite struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Symbol string `json:"symbol"`
}

// RepeatedPrimary is one record with primary anchors on several symbols.
type RepeatedPrimary struct {
	Registry string        `json:"registry,omitempty"`
	Record   string        `json:"record"`
	Sites    []PrimarySite `json:"sites"`
}

// IdenticalScenarios is one set of word-identical scenarios across records of
// one registry.
type IdenticalScenarios struct {
	Registry string          `json:"registry,omitempty"`
	Steps    string          `json:"steps"`
	Members  []ClaimScenario `json:"members"`
}

// SharedClaimsInput is what one registry contributes: its records, and the
// anchors its scan accepted.
type SharedClaimsInput struct {
	Registry Registry
	Features []Record
	Anchors  []Anchor
	Foreign  bool
	// Scan is the selection the registry's own scan uses; the zero value
	// means the registry's declared sources and view.
	Scan ScanOptions
	// Gaps are what loading this registry could not read.
	Gaps []MeasurementGap
}

// @facet: registry/layout/shared-claims
// @facet-role: primary

// ComputeSharedClaims builds the back-list over the given registries.
func ComputeSharedClaims(inputs []SharedClaimsInput) SharedClaims {
	out := SharedClaims{
		SpecCases: []SharedSpecCase{},
		Symbols:   []SharedSymbol{},
		Primaries: []RepeatedPrimary{},
		Scenarios: []IdenticalScenarios{},
	}
	inputs, gaps := withForeignAnchors(inputs)
	for _, in := range inputs {
		gaps = append(gaps, in.Gaps...)
	}
	out.SpecCases = sharedSpecCases(inputs)
	var read []MeasurementGap
	out.Symbols, out.Primaries, read = sharedAnchors(inputs)
	out.Scenarios = identicalScenarios(inputs)
	out.Incomplete = sortedGaps(append(gaps, read...))
	return out
}

// withForeignAnchors fills each foreign input's anchors by that registry's
// own scan selection, narrowed to the files the selected inputs' anchors sit
// in. The selection is the one its own lint would make, so a file its ignore
// rules or source view leave out stays out, and a file it reads keeps the
// portable reference its scan gives it.
func withForeignAnchors(inputs []SharedClaimsInput) ([]SharedClaimsInput, []MeasurementGap) {
	files := map[string]bool{}
	for _, in := range inputs {
		if in.Foreign {
			continue
		}
		for _, a := range in.Anchors {
			// Only Go sources are read for declarations, so another
			// registry's scan opens no other file.
			if abs := absAnchorPath(in.Registry, a); languageOf(abs) == "go" {
				files[abs] = true
			}
		}
	}
	var gaps []MeasurementGap
	out := slices.Clone(inputs)
	for i, in := range out {
		if !in.Foreign || in.Anchors != nil || len(files) == 0 {
			continue
		}
		opts := in.Scan
		if opts.View == nil && len(opts.Dirs) == 0 {
			opts = in.Registry.ScanOptions()
		}
		opts.files = files
		found, err := ScanAnchors(in.Registry.Root, opts)
		if err != nil {
			gaps = append(gaps, MeasurementGap{Registry: in.Registry.Name, Reason: "anchors not scanned: " + err.Error()})
			continue
		}
		anchors := []Anchor{}
		for _, a := range found {
			if a.BelongsTo(in.Registry.Name) {
				anchors = append(anchors, a)
			}
		}
		out[i].Anchors = anchors
	}
	return out, gaps
}

// sortedGaps orders gaps by registry, path and reason. They arrive in the
// order a parallel walk delivers files, and every report derived from them
// must be the same on every run. A complete measurement is an empty list,
// never null: the reader tells "nothing missing" from "not reported" by it.
func sortedGaps(gaps []MeasurementGap) []MeasurementGap {
	out := append([]MeasurementGap{}, gaps...)
	slices.SortStableFunc(out, func(a, b MeasurementGap) int {
		return cmp.Or(cmp.Compare(a.Registry, b.Registry), cmp.Compare(a.Path, b.Path), cmp.Compare(a.Reason, b.Reason))
	})
	return out
}

// UnreadSource is the gap a source file leaves that a scan selected but could
// not read: its anchors, and the claims they make, are missing from the lists.
func UnreadSource(registry, path string) MeasurementGap {
	return MeasurementGap{Registry: registry, Path: path, Reason: "source not read"}
}

func unreadGap(reg Registry, u unreadSource) MeasurementGap {
	if u.reference != "" {
		return UnreadSource(reg.Name, u.reference)
	}
	return UnreadSource(reg.Name, reg.Rel(u.path))
}

func absAnchorPath(reg Registry, a Anchor) string {
	abs := a.Path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(reg.Root, abs)
	}
	if full, err := filepath.Abs(abs); err == nil {
		abs = full
	}
	return filepath.Clean(abs)
}

func sharedSpecCases(inputs []SharedClaimsInput) []SharedSpecCase {
	users := map[string][]ClaimScenario{}
	for _, in := range inputs {
		for _, rec := range in.Features {
			for _, s := range recordScenarios(rec) {
				if s.ref == "" {
					continue
				}
				users[s.ref] = append(users[s.ref], ClaimScenario{
					Registry: in.Registry.Name, Record: rec.ID, Scenario: s.id,
					Path: in.Registry.Rel(rec.Path), SpecifiedBy: s.ref,
				})
			}
		}
	}
	out := []SharedSpecCase{}
	for _, address := range claimKeys(users) {
		list := users[address]
		if len(list) < 2 {
			continue
		}
		sortClaimScenarios(list)
		out = append(out, SharedSpecCase{Address: address, Scope: specCaseScope(list), Users: list})
	}
	return out
}

func specCaseScope(list []ClaimScenario) string {
	registries, records := map[string]bool{}, map[string]bool{}
	for _, u := range list {
		registries[u.Registry] = true
		records[u.Registry+"\x00"+u.Record] = true
	}
	switch {
	case len(registries) > 1:
		return "registries"
	case len(records) > 1:
		return "registry"
	default:
		return "record"
	}
}

type scenarioEntry struct {
	id, statement, ref string
	examples           any
	raw                map[string]any
}

func recordScenarios(rec Record) []scenarioEntry {
	contract, _ := rec.Fields["contract"].(map[string]any)
	list, _ := contract["scenarios"].([]any)
	out := make([]scenarioEntry, 0, len(list))
	for _, raw := range list {
		s, _ := raw.(map[string]any)
		id, _ := s["id"].(string)
		if id == "" {
			continue
		}
		statement, _ := s["statement"].(string)
		ref, _ := s["specified_by"].(string)
		out = append(out, scenarioEntry{id: id, statement: statement, ref: ref, examples: s["examples"], raw: s})
	}
	return out
}

// sharedAnchors resolves every anchor to the declaration it attaches to and
// reports declarations claimed by several records, and records whose primary
// anchors sit on several declarations. A record is the pair of registry and
// id throughout: the same id in two registries is two records of one feature.
func sharedAnchors(inputs []SharedClaimsInput) ([]SharedSymbol, []RepeatedPrimary, []MeasurementGap) {
	type placed struct {
		anchor ClaimAnchor
		path   string
		abs    string
	}
	held := map[string]map[string]bool{}
	heldAnywhere := map[string]bool{}
	for _, in := range inputs {
		ids := map[string]bool{}
		for _, rec := range in.Features {
			ids[rec.ID] = true
			heldAnywhere[rec.ID] = true
		}
		held[in.Registry.Name] = ids
	}
	seen := map[string]bool{}
	var all []placed
	for _, in := range inputs {
		for _, a := range in.Anchors {
			registry := a.Registry
			switch {
			case registry != "" && registry != in.Registry.Name:
				// A tag naming another registry is claimed by that registry's
				// own scan of the file, or by none.
				continue
			case registry == "":
				registry = in.Registry.Name
				// An unqualified anchor in code several registries scan is a
				// claim of each registry holding the record. A registry that
				// does not hold it leaves it to the ones that do.
				if !held[registry][a.ID] && heldAnywhere[a.ID] {
					continue
				}
			}
			abs := absAnchorPath(in.Registry, a)
			if languageOf(abs) != "go" {
				// Only Go sources are read for declarations; no other file
				// is opened again here.
				continue
			}
			key := fmt.Sprintf("%s\x00%d\x00%s\x00%s", abs, a.Line, registry, a.ID)
			if seen[key] {
				continue
			}
			seen[key] = true
			role := a.Role
			if role == "" {
				role = "related"
			}
			// The portable reference of a source view, never the local
			// checkout path the file was read from.
			path := a.Reference
			if path == "" {
				path = in.Registry.Rel(abs)
			}
			all = append(all, placed{
				anchor: ClaimAnchor{Registry: registry, Record: a.ID, Role: role, Line: a.Line},
				path:   path,
				abs:    abs,
			})
		}
	}

	display := map[string]string{}
	for _, p := range all {
		display[p.abs] = p.path
	}
	all, gaps := admittedAnchors(all, func(p placed) (string, int, string) { return p.abs, p.anchor.Line, p.anchor.Record }, display)

	perFile := map[string]int{}
	recordFiles := map[string]map[string]bool{}
	for _, p := range all {
		perFile[p.abs]++
		key := recordKey(p.anchor.Registry, p.anchor.Record)
		if recordFiles[key] == nil {
			recordFiles[key] = map[string]bool{}
		}
		recordFiles[key][p.abs] = true
	}
	needed := map[string][]int{}
	for _, p := range all {
		key := recordKey(p.anchor.Registry, p.anchor.Record)
		if perFile[p.abs] > 1 || len(recordFiles[key]) > 1 {
			needed[p.abs] = append(needed[p.abs], p.anchor.Line)
		}
	}
	declarations := map[string]map[int]declaration{}
	for _, path := range claimKeys(needed) {
		found, err := goDeclarations(path, needed[path])
		if err != nil {
			gaps = append(gaps, MeasurementGap{Path: display[path], Reason: err.Error()})
			continue
		}
		declarations[path] = found
	}

	type site struct {
		path   string
		line   int
		symbol string
	}
	bySite := map[site][]ClaimAnchor{}
	primaries := map[string][]PrimarySite{}
	primaryOwner := map[string]ClaimAnchor{}
	for _, p := range all {
		decl, ok := declarations[p.abs][p.anchor.Line]
		if !ok {
			continue
		}
		p.anchor.Role = decl.role
		at := site{path: p.path, line: decl.line, symbol: decl.name}
		bySite[at] = append(bySite[at], p.anchor)
		if p.anchor.Role == "primary" {
			key := recordKey(p.anchor.Registry, p.anchor.Record)
			ps := PrimarySite{Path: at.path, Line: at.line, Symbol: at.symbol}
			if !slices.Contains(primaries[key], ps) {
				primaries[key] = append(primaries[key], ps)
			}
			primaryOwner[key] = p.anchor
		}
	}

	symbols := []SharedSymbol{}
	for at, anchors := range bySite {
		records := map[string]bool{}
		primary := map[string]bool{}
		for _, a := range anchors {
			records[recordKey(a.Registry, a.Record)] = true
			if a.Role == "primary" {
				primary[recordKey(a.Registry, a.Record)] = true
			}
		}
		if len(records) < 2 {
			continue
		}
		sort.Slice(anchors, func(i, j int) bool {
			if anchors[i].Line != anchors[j].Line {
				return anchors[i].Line < anchors[j].Line
			}
			if anchors[i].Registry != anchors[j].Registry {
				return anchors[i].Registry < anchors[j].Registry
			}
			return anchors[i].Record < anchors[j].Record
		})
		symbols = append(symbols, SharedSymbol{
			Path: at.path, Line: at.line, Symbol: at.symbol,
			Anchors: anchors, DoublePrimary: len(primary) > 1,
		})
	}
	sort.Slice(symbols, func(i, j int) bool {
		if symbols[i].Path != symbols[j].Path {
			return symbols[i].Path < symbols[j].Path
		}
		return symbols[i].Line < symbols[j].Line
	})

	repeated := []RepeatedPrimary{}
	for _, key := range claimKeys(primaries) {
		sites := twinPrimarySites(primaries[key])
		if len(sites) < 2 {
			continue
		}
		sort.Slice(sites, func(i, j int) bool {
			if sites[i].Path != sites[j].Path {
				return sites[i].Path < sites[j].Path
			}
			return sites[i].Line < sites[j].Line
		})
		owner := primaryOwner[key]
		repeated = append(repeated, RepeatedPrimary{Registry: owner.Registry, Record: owner.Record, Sites: sites})
	}
	return symbols, repeated, gaps
}

func recordKey(registry, id string) string { return registry + "\x00" + id }

type declaration struct {
	name string
	line int
	// role is the anchor's role as the comment group binding it states it:
	// the last role tag after the anchor's id tag and before the next id tag
	// of that group, or related.
	role string
}

func goDeclarations(path string, anchorLines []int) (map[int]declaration, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("declarations not read: %w", err)
	}
	if info.Size() > scanWholeFileLimit {
		return nil, errors.New("too large to parse")
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("declarations not read: %w", err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, errors.New("parse error")
	}
	tf := fset.File(file.Pos())
	line := func(p token.Pos) int { return fset.PositionFor(p, false).Line }
	offset := tf.Offset

	type named struct {
		declaration
		doc   *ast.CommentGroup
		start token.Pos
	}
	var decls []named
	add := func(doc *ast.CommentGroup, start token.Pos, name *ast.Ident, prefix string) {
		if name.Name == "_" {
			return
		}
		decls = append(decls, named{declaration{name: prefix + name.Name, line: line(name.Pos())}, doc, start})
	}
	for _, d := range file.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			prefix := ""
			if d.Recv != nil && len(d.Recv.List) == 1 {
				recv := receiverType(d.Recv.List[0].Type)
				if recv == "" || recv == "_" {
					continue
				}
				prefix = recv + "."
			}
			add(d.Doc, d.Pos(), d.Name, prefix)
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				var name *ast.Ident
				var doc *ast.CommentGroup
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					name, doc = spec.Name, spec.Doc
				case *ast.ValueSpec:
					if len(spec.Names) == 1 {
						name, doc = spec.Names[0], spec.Doc
					}
				}
				if name == nil {
					continue
				}
				if !d.Lparen.IsValid() {
					add(d.Doc, d.Pos(), name, "")
					continue
				}
				add(doc, spec.Pos(), name, "")
				if len(d.Specs) == 1 {
					add(d.Doc, d.Pos(), name, "")
				}
			}
		}
	}

	// commentEnd is the physical end of a comment in the raw bytes: a line
	// comment runs to the end of its line, a block comment to just after its
	// first "*/".
	commentEnd := func(c *ast.Comment) int {
		from := offset(c.Slash)
		if bytes.HasPrefix(src[from:], []byte("//")) {
			if i := bytes.IndexByte(src[from:], '\n'); i >= 0 {
				return from + i
			}
			return len(src)
		}
		if i := bytes.Index(src[from+2:], []byte("*/")); i >= 0 {
			return from + 2 + i + 2
		}
		return len(src)
	}
	groupEnd := func(g *ast.CommentGroup) int { return commentEnd(g.List[len(g.List)-1]) }
	// commentAt is the comment group one of whose comments spans the byte
	// offset, or nil.
	commentAt := func(at int) *ast.CommentGroup {
		for _, g := range file.Comments {
			for _, c := range g.List {
				if offset(c.Slash) <= at && at < commentEnd(c) {
					return g
				}
			}
		}
		return nil
	}
	lineCount := tf.LineCount()
	lineBytes := func(n int) []byte {
		start := offset(tf.LineStart(n))
		end := len(src)
		if n < lineCount {
			end = offset(tf.LineStart(n + 1))
		}
		return src[start:end]
	}
	lineAt := func(off int) int { return fset.PositionFor(tf.Pos(off), false).Line }
	out := map[int]declaration{}
	for _, at := range anchorLines {
		if at < 1 || at > lineCount {
			continue
		}
		// The scanner keeps the first tag of a line.
		tag := anchorRe.FindIndex(lineBytes(at))
		if tag == nil {
			continue
		}
		group := commentAt(offset(tf.LineStart(at)) + tag[0])
		if group == nil {
			continue
		}
		for _, d := range decls {
			bound := d.doc == group
			if end := groupEnd(group); d.doc == nil && line(d.start) == lineAt(end-1)+1 {
				bound = len(bytes.TrimSpace(src[end:offset(d.start)])) == 0
			}
			if bound {
				decl := d.declaration
				decl.role = groupRole(src, group, offset(tf.LineStart(at))+tag[0], func(c *ast.Comment) (int, int) {
					return offset(c.Slash), commentEnd(c)
				})
				out[at] = decl
				break
			}
		}
	}
	return out, nil
}

// groupRole is the role the comment group states for the id tag at byte
// offset tag: the last role tag between it and the group's next id tag, read
// within the physical span of each of the group's comments, or related.
func groupRole(src []byte, group *ast.CommentGroup, tag int, span func(*ast.Comment) (int, int)) string {
	role := "related"
	next := len(src)
	for _, c := range group.List {
		from, to := span(c)
		for _, m := range anchorRe.FindAllIndex(src[from:to], -1) {
			if at := from + m[0]; at > tag && at < next {
				next = at
			}
		}
	}
	for _, c := range group.List {
		from, to := span(c)
		for _, m := range roleRe.FindAllSubmatchIndex(src[from:to], -1) {
			if at := from + m[0]; at > tag && at < next {
				role = string(src[from+m[2] : from+m[3]])
			}
		}
	}
	return role
}

// receiverType is the name of a method receiver's type, without pointer or
// type parameters.
func receiverType(expr ast.Expr) string {
	for {
		switch e := expr.(type) {
		case *ast.StarExpr:
			expr = e.X
		case *ast.IndexExpr:
			expr = e.X
		case *ast.IndexListExpr:
			expr = e.X
		case *ast.ParenExpr:
			expr = e.X
		case *ast.Ident:
			return e.Name
		default:
			return ""
		}
	}
}

// admittedAnchors keeps the anchors the scanner itself admits. A file larger
// than the scanner reads whole is searched for anchors only in its head, so an
// anchor a caller found past that head is dropped here, and every consumer of
// the back-list measures the same anchors.
func admittedAnchors[P any](all []P, at func(P) (path string, line int, id string), display map[string]string) ([]P, []MeasurementGap) {
	admitted := map[string]map[string]bool{}
	var gaps []MeasurementGap
	key := func(line int, id string) string { return fmt.Sprintf("%d %s", line, id) }
	for _, p := range all {
		path, _, _ := at(p)
		if _, done := admitted[path]; done {
			continue
		}
		admitted[path] = nil
		info, err := os.Stat(path)
		if err != nil || info.Size() <= scanWholeFileLimit {
			continue
		}
		found, err := ScanFile(path)
		if err != nil {
			gaps = append(gaps, MeasurementGap{Path: display[path], Reason: "anchors not read: " + err.Error()})
			admitted[path] = map[string]bool{}
			continue
		}
		keys := map[string]bool{}
		for _, a := range found {
			keys[key(a.Line, a.ID)] = true
		}
		admitted[path] = keys
	}
	out := all[:0:0]
	for _, p := range all {
		path, line, id := at(p)
		if keys := admitted[path]; keys != nil && !keys[key(line, id)] {
			continue
		}
		out = append(out, p)
	}
	return out, gaps
}

// languageFamilies maps file extensions to the language they are written in,
// so that the variants of one language's extension count as that language.
var languageFamilies = map[string]string{
	".go": "go",
	".js": "javascript", ".mjs": "javascript", ".cjs": "javascript", ".jsx": "javascript",
	".ts": "typescript", ".tsx": "typescript", ".mts": "typescript", ".cts": "typescript",
	".py": "python", ".pyi": "python",
	".rs": "rust", ".java": "java", ".kt": "kotlin", ".kts": "kotlin",
	".rb": "ruby", ".swift": "swift", ".cs": "csharp", ".php": "php", ".scala": "scala",
	".c": "c", ".h": "c",
	".cc": "cpp", ".cpp": "cpp", ".cxx": "cpp", ".hpp": "cpp", ".hh": "cpp", ".hxx": "cpp",
	".sh": "shell", ".bash": "shell",
}

func languageOf(path string) string {
	return languageFamilies[strings.ToLower(filepath.Ext(path))]
}

// twinPrimarySites keeps the primary sites that share a language with a
// primary site in another file. A record whose primary anchors are a Go
// handler and a TypeScript page has one implementation per layer; two primary
// declarations in two files of one language is the shape a second
// implementation under the same id takes, and the only one worth a line. A
// file whose language is not known shares a language with nothing.
func twinPrimarySites(sites []PrimarySite) []PrimarySite {
	files := map[string]map[string]bool{}
	for _, s := range sites {
		language := languageOf(s.Path)
		if language == "" {
			continue
		}
		if files[language] == nil {
			files[language] = map[string]bool{}
		}
		files[language][s.Path] = true
	}
	out := []PrimarySite{}
	for _, s := range sites {
		if language := languageOf(s.Path); language != "" && len(files[language]) > 1 {
			out = append(out, s)
		}
	}
	return out
}

// identicalScenarios groups scenarios of different records in one registry
// whose normalised Gherkin steps are the same. A scenario bound to a pinned
// spec case is read in that case's Gherkin dialect; an outline's examples are
// compared as the effective tables, inherited columns included and block
// names ignored. A statement that is not Gherkin is compared by its
// whitespace-normalised, case-folded text, so a copied prose scenario is found
// as well.
func identicalScenarios(inputs []SharedClaimsInput) []IdenticalScenarios {
	out := []IdenticalScenarios{}
	pins := map[string]specPin{}
	for _, in := range inputs {
		if in.Foreign {
			continue
		}
		pin, ok := pins[in.Registry.Root]
		if !ok {
			pin = readSpecPin(in.Registry.Root)
			pins[in.Registry.Root] = pin
		}
		sources := specSourcesByID(in.Registry)
		dialects := pinnedDialects(sources, pin)
		groups := map[string][]ClaimScenario{}
		conforming := map[string]map[string]bool{}
		display := map[string]string{}
		for _, rec := range in.Features {
			var outlines map[string][]ExamplesBlock
			blocks := func(id string) []ExamplesBlock {
				if outlines == nil {
					outlines = map[string][]ExamplesBlock{}
					for _, o := range rec.Outlines() {
						outlines[o.Scenario] = o.Blocks
					}
				}
				return outlines[id]
			}
			for _, s := range recordScenarios(rec) {
				key, text, exactCase := scenarioKey(in.Registry.Name, sources, pin, dialects, s, blocks)
				if key == "" {
					continue
				}
				groups[key] = append(groups[key], ClaimScenario{
					Registry: in.Registry.Name, Record: rec.ID, Scenario: s.id,
					Path: in.Registry.Rel(rec.Path), SpecifiedBy: s.ref,
				})
				if conforming[key] == nil {
					conforming[key] = map[string]bool{}
				}
				conforming[key][exactCase] = true
				display[key] = text
			}
		}
		for _, key := range claimKeys(groups) {
			members := groups[key]
			records := map[string]bool{}
			for _, m := range members {
				records[m.Record] = true
			}
			if len(records) < 2 {
				continue
			}
			if len(conforming[key]) == 1 && !conforming[key][""] {
				continue
			}
			sortClaimScenarios(members)
			out = append(out, IdenticalScenarios{Registry: in.Registry.Name, Steps: display[key], Members: members})
		}
	}
	return out
}

// pinnedDialects lists the Gherkin dialects of the spec files pinned for a
// registry's sources, English first. A scenario without a resolved spec case
// has no dialect of its own; these are the ones its project writes in.
func pinnedDialects(sources map[string]spec.Source, pin specPin) []string {
	found := map[string]bool{}
	for id := range sources {
		for _, file := range pin.lock[id].Files {
			if file.Language != "" && file.Language != "en" {
				found[file.Language] = true
			}
		}
	}
	return append([]string{"en"}, claimKeys(found)...)
}

// scenarioKey returns what identical scenarios share, the display text, and
// the spec case the scenario conforms to exactly, or "" when it conforms to
// none.
func scenarioKey(registry string, sources map[string]spec.Source, pin specPin, dialects []string, s scenarioEntry, blocks func(string) []ExamplesBlock) (key, text, exactCase string) {
	statement := strings.TrimSpace(s.statement)
	if statement == "" {
		return "", "", ""
	}
	languages := dialects
	var pinnedTables [][][]string
	if s.ref != "" {
		// Only an exact case binds the steps, and with them their dialect. A
		// provenance case binds no steps, so its users are read in the
		// dialects an unbound scenario is read in.
		if file, locked, err := resolveSpecReference(registry, sources, pin, s.ref, true); err == nil && locked.Strength() == spec.Exact {
			languages = []string{"en"}
			if file.Language != "" {
				languages = []string{file.Language}
			}
			if locked.Sequence != nil {
				pinnedTables = locked.Sequence.Examples
				if compareSpecScenario(s.raw, file, locked, func() []ExamplesBlock { return blocks(s.id) }) == nil {
					exactCase = s.ref
				}
			}
		}
	}
	var tables [][][]string
	if s.examples != nil {
		tables = specExampleTables(blocks(s.id), pinnedTables)
	}
	text = strings.Join(strings.Fields(statement), " ")
	for _, language := range languages {
		if sequence, err := spec.NormaliseStatement(statement, language); err == nil {
			sequence.Examples = tables
			return "gherkin:" + sequence.Digest(), text, exactCase
		}
	}
	examples := ""
	if tables != nil {
		if data, err := json.Marshal(tables); err == nil {
			examples = string(data)
		}
	}
	return "text:" + strings.ToLower(text) + "\x00" + examples, text, exactCase
}

// lintSharedClaims turns the back-list into info findings, one per entry.
func lintSharedClaims(rep *Report, claims SharedClaims, regs map[string]Registry, skip map[string]bool) {
	recordPath := func(registry, id string) string {
		reg, ok := regs[registry]
		if !ok {
			return ""
		}
		return reg.Rel(reg.PathForID(id))
	}
	if !skip[CheckSharedClaimsIncomplete] {
		for _, g := range claims.Incomplete {
			where := g.Path
			if where == "" {
				where = "registry " + qualifiedClaim("", g.Registry)
			}
			rep.add(Finding{
				Severity: SeverityWarning, Check: CheckSharedClaimsIncomplete, Registry: g.Registry, Path: g.Path,
				Message: fmt.Sprintf("shared claims are incomplete: %s: %s", where, g.Reason),
			})
		}
	}
	if !skip[CheckSharedSpecCase] {
		for _, c := range claims.SpecCases {
			first := c.Users[0]
			rep.add(Finding{
				Severity: SeverityInfo, Check: CheckSharedSpecCase, Registry: first.Registry,
				ID: first.Record, Path: first.Path,
				Message: fmt.Sprintf("spec case %q is referenced by %d scenarios (%s): %s",
					c.Address, len(c.Users), scopeWords(c.Scope), joinScenarios(c.Users)),
			})
		}
	}
	if !skip[CheckSharedSymbol] {
		for _, s := range claims.Symbols {
			parts := make([]string, 0, len(s.Anchors))
			for _, a := range s.Anchors {
				parts = append(parts, fmt.Sprintf("%s (%s)", qualifiedClaim(a.Registry, a.Record), a.Role))
			}
			lead := "symbol"
			if s.DoublePrimary {
				lead = "symbol is primary for more than one record:"
			}
			first := s.Anchors[0]
			rep.add(Finding{
				Severity: SeverityInfo, Check: CheckSharedSymbol, Registry: first.Registry,
				ID: first.Record, Path: s.Path,
				Message: fmt.Sprintf("%s %s at %s:%d is anchored by %d records: %s",
					lead, s.Symbol, s.Path, s.Line, len(distinctRecords(s.Anchors)), strings.Join(parts, ", ")),
			})
		}
	}
	if !skip[CheckRepeatedPrimary] {
		for _, p := range claims.Primaries {
			sites := make([]string, 0, len(p.Sites))
			for _, s := range p.Sites {
				sites = append(sites, fmt.Sprintf("%s at %s:%d", s.Symbol, s.Path, s.Line))
			}
			rep.add(Finding{
				Severity: SeverityInfo, Check: CheckRepeatedPrimary, Registry: p.Registry,
				ID: p.Record, Path: recordPath(p.Registry, p.Record),
				Message: fmt.Sprintf("record %q has primary anchors on %d symbols: %s",
					p.Record, len(p.Sites), strings.Join(sites, "; ")),
			})
		}
	}
	if !skip[CheckIdenticalScenarios] {
		for _, g := range claims.Scenarios {
			first := g.Members[0]
			rep.add(Finding{
				Severity: SeverityInfo, Check: CheckIdenticalScenarios, Registry: g.Registry,
				ID: first.Record, Path: first.Path,
				Message: fmt.Sprintf("%d scenarios in different records have identical steps: %s",
					len(g.Members), joinScenarios(g.Members)),
			})
		}
	}
}

func scopeWords(scope string) string {
	switch scope {
	case "record":
		return "within one record"
	case "registry":
		return "across records of one registry"
	default:
		return "across registries"
	}
}

func joinScenarios(list []ClaimScenario) string {
	parts := make([]string, 0, len(list))
	for _, s := range list {
		part := qualifiedClaim(s.Registry, s.Record) + "#" + s.Scenario
		if s.SpecifiedBy != "" {
			part += " [" + s.SpecifiedBy + "]"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}

func qualifiedClaim(registry, id string) string {
	if registry == "" {
		return id
	}
	return registry + ":" + id
}

func distinctRecords(anchors []ClaimAnchor) map[string]bool {
	out := map[string]bool{}
	for _, a := range anchors {
		out[recordKey(a.Registry, a.Record)] = true
	}
	return out
}

func sortClaimScenarios(list []ClaimScenario) {
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.Registry != b.Registry {
			return a.Registry < b.Registry
		}
		if a.Record != b.Record {
			return a.Record < b.Record
		}
		return a.Scenario < b.Scenario
	})
}

func claimKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// SharedClaimsFor returns the entries of claims that involve the named
// registry, which is what a reader of one registry can link to.
func SharedClaimsFor(claims SharedClaims, registry string) SharedClaims {
	out := SharedClaims{SpecCases: []SharedSpecCase{}, Symbols: []SharedSymbol{}, Primaries: []RepeatedPrimary{}, Scenarios: []IdenticalScenarios{}, Incomplete: claims.Incomplete}
	if out.Incomplete == nil {
		out.Incomplete = []MeasurementGap{}
	}
	for _, c := range claims.SpecCases {
		if slices.ContainsFunc(c.Users, func(u ClaimScenario) bool { return u.Registry == registry }) {
			out.SpecCases = append(out.SpecCases, c)
		}
	}
	for _, s := range claims.Symbols {
		if slices.ContainsFunc(s.Anchors, func(a ClaimAnchor) bool { return a.Registry == registry }) {
			out.Symbols = append(out.Symbols, s)
		}
	}
	for _, p := range claims.Primaries {
		if p.Registry == registry {
			out.Primaries = append(out.Primaries, p)
		}
	}
	for _, g := range claims.Scenarios {
		if g.Registry == registry {
			out.Scenarios = append(out.Scenarios, g)
		}
	}
	return out
}

// RegistrySharedClaims computes the back-list a reader of one registry shows:
// the registry's own anchors, and the records of every registry the project
// declares, each read by the selection its own scan makes, so a reader of one
// registry and 'facet lint --registry' list the same entries. read are the
// gaps the caller already met while reading the registry itself.
func RegistrySharedClaims(reg Registry, features []Record, anchors []Anchor, read ...MeasurementGap) SharedClaims {
	inputs := []SharedClaimsInput{{Registry: reg, Features: features, Anchors: anchors, Gaps: read}}
	var cfg *Config
	var err error
	if reg.SourceView != nil {
		cfg, err = LoadConfigWithResolvedSourceView(reg.Root, reg.SourceView)
	} else {
		cfg, err = LoadConfig(reg.Root)
	}
	var gaps []MeasurementGap
	if err != nil {
		gaps = append(gaps, MeasurementGap{Reason: "other registries not read: " + err.Error()})
	} else {
		for _, other := range cfg.All() {
			if other.Name == reg.Name {
				continue
			}
			recs, missing := featuresForClaims(other)
			inputs = append(inputs, SharedClaimsInput{Registry: other, Features: recs, Foreign: true, Gaps: missing})
		}
	}
	claims := SharedClaimsFor(ComputeSharedClaims(inputs), reg.Name)
	// The caller's gaps join the measured ones in the one order.
	claims.Incomplete = sortedGaps(append(gaps, claims.Incomplete...))
	return claims
}

// ClaimFeatures loads a registry's feature records for the back-list. A
// record or directory that will not load is reported as a gap and every
// readable record still counts, so one broken record does not take the
// registry's other claims out of the measurement.
func ClaimFeatures(reg Registry) ([]Record, []MeasurementGap) { return featuresForClaims(reg) }

func featuresForClaims(reg Registry) ([]Record, []MeasurementGap) {
	if recs, err := reg.LoadFeatures(); err == nil {
		return recs, nil
	}
	dir := reg.CategoryDir("features")
	var paths []string
	var gaps []MeasurementGap
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// The unreadable part is a gap; its readable siblings are walked on.
			gaps = append(gaps, MeasurementGap{Registry: reg.Name, Path: reg.Rel(path), Reason: "records not read"})
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() && filepath.Ext(path) == ".yaml" {
			paths = append(paths, path)
		}
		return nil
	})
	sort.Strings(paths)
	var recs []Record
	for _, path := range paths {
		rec, err := LoadRecord(path, "features")
		if err != nil {
			gaps = append(gaps, MeasurementGap{Registry: reg.Name, Path: reg.Rel(path), Reason: "record not read: " + err.Error()})
			continue
		}
		recs = append(recs, rec)
	}
	return recs, gaps
}
