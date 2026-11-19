// @facet: registry/layout/project-glossary
// @facet-role: primary
package facet

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/goccy/go-yaml"
)

// GlossaryName is the deployment's terminology list inside the records dir.
const GlossaryName = "glossary.yaml"

// GlossaryTerm is one term of art and what it means in this product.
type GlossaryTerm struct {
	Term string `yaml:"term"`
	// Category is the id of the group this term belongs under, from the
	// glossary's own Categories list. Empty means nobody has categorised the
	// term — not that it belongs somewhere general, which is the rule §4.4.2
	// states for classification and the reason nothing here defaults.
	Category   string `yaml:"category"`
	Definition string `yaml:"definition"`
	// Renderings stores per-locale wording for translation consumers. The
	// registry loads these values but does not apply them to generated output.
	Renderings map[string]string `yaml:"renderings"`
}

// GlossaryCategory is one group the terms fall into.
//
// @facet: registry/layout/glossary-categories
// @facet-role: primary
type GlossaryCategory struct {
	// ID is what a term names this category by. A slug rather than the heading
	// itself, so rewording the heading touches one line instead of every term.
	ID string `yaml:"id"`
	// Title is the heading in the product's own language.
	Title string `yaml:"title"`
	// Purpose is the one line saying which terms belong here, and by
	// implication which do not. Optional, and the sentence somebody consults
	// when deciding where a new term goes.
	Purpose string `yaml:"purpose"`
}

// Glossary is a project's terminology, declared once and linked everywhere.
type Glossary struct {
	SchemaVersion string `yaml:"schema_version"`
	// Categories are the groups, in the order the project wants them shown. A
	// list rather than a map, unlike the taxonomy's domains: the order of a
	// glossary is the project's own — already why nothing sorts Terms — and a
	// map would surrender it to whatever order the parser hands back.
	Categories []GlossaryCategory `yaml:"categories"`
	Terms      []GlossaryTerm     `yaml:"terms"`
}

// Configured reports whether the project declares a glossary at all. A project
// without one behaves exactly as it did before — the same rule that governs the
// variant map and the taxonomy, and the reason adoption is a decision rather
// than a migration.
func (g *Glossary) Configured() bool { return g != nil && len(g.Terms) > 0 }

// GlossaryPath returns where the glossary lives for a registry.
func (r Registry) GlossaryPath() string {
	return filepath.Join(r.RecordsDir, GlossaryName)
}

// LoadGlossary reads the deployment's terminology. A missing file is not an
// error — it means the project has not declared one.
func LoadGlossary(path string) (*Glossary, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Glossary{}, nil
	}
	if err != nil {
		return nil, err
	}
	var g Glossary
	if err := yaml.Unmarshal(data, &g); err != nil {
		return nil, err
	}
	return &g, nil
}

// TermSet lists the distinct terms the glossary defines, sorted.
func (g *Glossary) TermSet() []string {
	if g == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range g.Terms {
		term := strings.TrimSpace(t.Term)
		if term == "" || seen[term] {
			continue
		}
		seen[term] = true
		out = append(out, term)
	}
	sort.Strings(out)
	return out
}

// DuplicateTerms lists every term the glossary defines more than once, sorted.
func (g *Glossary) DuplicateTerms() []string {
	if g == nil {
		return nil
	}
	count := map[string]int{}
	for _, t := range g.Terms {
		if term := strings.TrimSpace(t.Term); term != "" {
			count[term]++
		}
	}
	var out []string
	for term, n := range count {
		if n > 1 {
			out = append(out, term)
		}
	}
	sort.Strings(out)
	return out
}

// Categorised reports whether anything in the file groups its terms at all.
func (g *Glossary) Categorised() bool {
	if g == nil {
		return false
	}
	if len(g.Categories) > 0 {
		return true
	}
	for _, t := range g.Terms {
		if strings.TrimSpace(t.Category) != "" {
			return true
		}
	}
	return false
}

// CategoryIDs lists the declared category ids, in the order the file declares
// them and without repeating one declared twice.
func (g *Glossary) CategoryIDs() []string {
	if g == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range g.Categories {
		id := strings.TrimSpace(c.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// CategoryDeclared reports whether the glossary declares a category id.
func (g *Glossary) CategoryDeclared(id string) bool {
	if g == nil {
		return false
	}
	for _, c := range g.Categories {
		if strings.TrimSpace(c.ID) == id {
			return true
		}
	}
	return false
}

// DuplicateCategories lists every category id the glossary declares more than
// once, sorted. The sibling of DuplicateTerms, one level up.
func (g *Glossary) DuplicateCategories() []string {
	if g == nil {
		return nil
	}
	count := map[string]int{}
	for _, c := range g.Categories {
		if id := strings.TrimSpace(c.ID); id != "" {
			count[id]++
		}
	}
	var out []string
	for id, n := range count {
		if n > 1 {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// UnusedCategories lists the declared categories no term names, in declared
// order.
func (g *Glossary) UnusedCategories() []string {
	if g == nil {
		return nil
	}
	used := map[string]bool{}
	for _, t := range g.Terms {
		if id := strings.TrimSpace(t.Category); id != "" {
			used[id] = true
		}
	}
	var out []string
	for _, id := range g.CategoryIDs() {
		if !used[id] {
			out = append(out, id)
		}
	}
	return out
}

// GlossaryGroup is one heading of a grouped glossary, with the terms under it.
type GlossaryGroup struct {
	// ID is the category id, or "" for the terms that name no category.
	ID string
	// Title and Purpose are the declared wording, empty when Declared is false
	// — there is none to show for a category the file does not declare, and
	// inventing one here is inventing it for every surface.
	Title   string
	Purpose string
	// Declared reports whether this group is a category the file declares. An
	// undeclared id (Declared false, ID set) is the error case; the
	// uncategorised group (Declared false, ID empty) is the absent one.
	Declared bool
	Terms    []GlossaryTerm
}

// Grouped arranges the terms under their categories.
func (g *Glossary) Grouped() []GlossaryGroup {
	if !g.Categorised() {
		return nil
	}

	declared := g.CategoryIDs()
	at := make(map[string]int, len(declared))
	groups := make([]GlossaryGroup, 0, len(declared)+1)
	for _, id := range declared {
		at[id] = len(groups)
		// The first declaration wins where an id is declared twice. Arbitrary,
		// and deliberately so: the linter reports that as an error, and picking
		// a winner here is only so a surface has something deterministic to
		// draw meanwhile.
		var meta GlossaryCategory
		for _, c := range g.Categories {
			if strings.TrimSpace(c.ID) == id {
				meta = c
				break
			}
		}
		groups = append(groups, GlossaryGroup{
			ID:       id,
			Title:    meta.Title,
			Purpose:  meta.Purpose,
			Declared: true,
		})
	}

	var undeclaredIDs []string
	undeclared := map[string][]GlossaryTerm{}
	var loose []GlossaryTerm

	for _, term := range g.Terms {
		id := strings.TrimSpace(term.Category)
		if id == "" {
			loose = append(loose, term)
			continue
		}
		if index, declared := at[id]; declared {
			groups[index].Terms = append(groups[index].Terms, term)
			continue
		}
		if _, seen := undeclared[id]; !seen {
			undeclaredIDs = append(undeclaredIDs, id)
		}
		undeclared[id] = append(undeclared[id], term)
	}

	sort.Strings(undeclaredIDs)
	for _, id := range undeclaredIDs {
		groups = append(groups, GlossaryGroup{ID: id, Terms: undeclared[id]})
	}
	if len(loose) > 0 {
		groups = append(groups, GlossaryGroup{Terms: loose})
	}
	return groups
}

// TermMatch is one occurrence of a defined term in a piece of prose, with the
// byte offsets a caller slices the text on.
type TermMatch struct {
	Term  string
	Start int
	End   int
}

// FindTerms reports every exact, case-sensitive, whole-word occurrence of a
// defined term in text, in the order they appear.
func FindTerms(text string, terms []string) []TermMatch {
	candidates := make([]string, 0, len(terms))
	for _, t := range terms {
		if t != "" {
			candidates = append(candidates, t)
		}
	}
	// Longest first, then lexicographic so the result is the same on every run
	// (FACET §6.3).
	sort.Slice(candidates, func(i, j int) bool {
		if len(candidates[i]) != len(candidates[j]) {
			return len(candidates[i]) > len(candidates[j])
		}
		return candidates[i] < candidates[j]
	})

	var out []TermMatch
	for i := 0; i < len(text); {
		if !startsWord(text, i) {
			i++
			continue
		}
		matched := false
		for _, term := range candidates {
			end := i + len(term)
			if end > len(text) || text[i:end] != term {
				continue
			}
			if !endsWord(text, end) {
				continue
			}
			out = append(out, TermMatch{Term: term, Start: i, End: end})
			i = end
			matched = true
			break
		}
		if !matched {
			i++
		}
	}
	return out
}

// startsWord reports whether a match beginning at i would begin at a word
// boundary — the byte at i opens a word, and nothing word-like precedes it.
func startsWord(text string, i int) bool {
	if !isWordByteAt(text, i) {
		return false
	}
	return i == 0 || !isWordByteAt(text, i-1)
}

// endsWord reports whether a match ending at end stops at a word boundary.
func endsWord(text string, end int) bool {
	return end >= len(text) || !isWordByteAt(text, end)
}

// isWordByteAt reports whether the byte at i belongs to a word. Multi-byte
// runes are word bytes throughout, which is what makes an umlaut inside a term
// behave like the letter it is rather than like a boundary.
func isWordByteAt(text string, i int) bool {
	if i < 0 || i >= len(text) {
		return false
	}
	b := text[i]
	if b >= 0x80 {
		return true
	}
	return isWordRune(rune(b))
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// nearMissMinTermLen is the shortest term that gets an edit-distance
// neighbourhood at all.
const nearMissMinTermLen = 4

// nearMissWideTermLen is where the tolerated distance goes from one to two.
const (
	nearMissWideTermLen  = 8
	nearMissNarrowBudget = 1
	nearMissWideBudget   = 2
)

// nearMissJudge answers, for one word, which defined term it was probably meant
// to be. It is built once per registry and memoises its verdicts, because the
// same word recurs across a corpus far more often than it is distinct.
type nearMissJudge struct {
	// singles are the single-word terms long enough to have a neighbourhood.
	// A multi-word term is excluded: comparing one word against "Order Line"
	// asks a question with no meaning.
	singles []string
	// exact holds every declared term, including the multi-word ones. A word
	// that is itself a term is never a near miss of another.
	exact map[string]bool
	// lower maps a term's lower-case form to the term, for the case-only
	// comparison — the one this check exists for first.
	lower map[string]string
	memo  map[string]string
}

func newNearMissJudge(terms []string) *nearMissJudge {
	j := &nearMissJudge{
		exact: make(map[string]bool, len(terms)),
		lower: map[string]string{},
		memo:  map[string]string{},
	}
	for _, term := range terms {
		j.exact[term] = true
		if key := strings.ToLower(term); j.lower[key] == "" {
			j.lower[key] = term
		}
		if len(term) >= nearMissMinTermLen && !strings.ContainsAny(term, " \t") {
			j.singles = append(j.singles, term)
		}
	}
	sort.Strings(j.singles)
	return j
}

// suggest returns the term a word was probably meant to be, or "" when the word
// is fine or is nobody's near miss.
func (j *nearMissJudge) suggest(word string) string {
	if verdict, seen := j.memo[word]; seen {
		return verdict
	}
	verdict := j.judge(word)
	j.memo[word] = verdict
	return verdict
}

func (j *nearMissJudge) judge(word string) string {
	// Using a term correctly is not a finding, and reporting it would make the
	// glossary look like a hazard.

	if j.exact[word] {
		return ""
	}
	// An inflection is correct usage rather than a typo, and a check that fires
	// on correct usage is one people learn to switch off.
	if j.isInflection(word) {
		return ""
	}
	// The case-only comparison, which needs no neighbourhood: the same letters,
	// written a way that links nowhere.
	if term, ok := j.lower[strings.ToLower(word)]; ok {
		return term
	}
	if len(word) < nearMissMinTermLen {
		return ""
	}

	best, bestDistance := "", 0
	for _, term := range j.singles {
		budget := nearMissNarrowBudget
		if len(term) >= nearMissWideTermLen {
			budget = nearMissWideBudget
		}
		if abs(len(word)-len(term)) > budget {
			continue
		}
		distance, ok := boundedDistance(word, term, budget)
		if !ok {
			continue
		}
		// Ties keep the lexicographically first term, so the suggestion is the
		// same on every run rather than depending on map order.
		if best == "" || distance < bestDistance {
			best, bestDistance = term, distance
		}
	}
	return best
}

// isInflection reports whether a word is a defined term with a regular English
// plural on the end.
func (j *nearMissJudge) isInflection(word string) bool {
	for _, suffix := range []string{"s", "es"} {
		if strings.HasSuffix(word, suffix) && j.exact[strings.TrimSuffix(word, suffix)] {
			return true
		}
	}
	return false
}

// boundedDistance is Levenshtein distance, abandoned as soon as it exceeds the
// budget. The bound is what keeps this affordable: a corpus has thousands of
// distinct words, and the answer for almost all of them is "not close".
func boundedDistance(a, b string, budget int) (int, bool) {
	ar, br := []rune(a), []rune(b)
	if len(ar) < len(br) {
		ar, br = br, ar
	}
	if len(ar)-len(br) > budget {
		return 0, false
	}

	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		row := curr[0]
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			curr[j] = min3(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
			if curr[j] < row {
				row = curr[j]
			}
		}
		if row > budget {
			return 0, false
		}
		prev, curr = curr, prev
	}
	if prev[len(br)] > budget {
		return 0, false
	}
	return prev[len(br)], true
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// proseField is one piece of a record's prose the near-miss check reads, named
// the way the finding will name it.
type proseField struct {
	Where string
	Text  string
}

// scannedProse returns the prose a near miss is judged in: the intent summary
// and every contract scenario statement.
func scannedProse(rec Record) []proseField {
	var out []proseField
	if summary := rec.Intent(); strings.TrimSpace(summary) != "" {
		out = append(out, proseField{Where: "intent.summary", Text: summary})
	}
	contract, ok := rec.Fields["contract"].(map[string]any)
	if !ok {
		return out
	}
	raw, ok := contract["scenarios"].([]any)
	if !ok {
		return out
	}
	for _, item := range raw {
		scenario, ok := item.(map[string]any)
		if !ok {
			continue
		}
		statement, _ := scenario["statement"].(string)
		if strings.TrimSpace(statement) == "" {
			continue
		}
		id, _ := scenario["id"].(string)
		where := "contract.scenarios[].statement"
		if id != "" {
			where = "contract.scenarios " + id
		}
		out = append(out, proseField{Where: where, Text: statement})
	}
	return out
}

// words splits prose into the runs of word characters a near miss is looked for
// in, keeping the order they appear. Punctuation, apostrophes and whitespace
// are separators, so "Consignee's" arrives as two words and the first of them
// is the term.
func words(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool { return !isWordRune(r) })
}

// maskTerms blanks every exact occurrence of a defined term, so the words left
// over are the ones a near miss can be looked for in.
func maskTerms(text string, terms []string) string {
	matches := FindTerms(text, terms)
	if len(matches) == 0 {
		return text
	}
	masked := []byte(text)
	for _, m := range matches {
		for i := m.Start; i < m.End; i++ {
			masked[i] = ' '
		}
	}
	return string(masked)
}
