// Checks glossary definitions, categories and term references.
package facet

import (
	"fmt"
	"sort"
	"strings"
)

// lintDuplicateGlossaryTerms reports a term the glossary defines more than once.
func lintDuplicateGlossaryTerms(reg Registry, rep *Report, glossary *Glossary) {
	for _, term := range glossary.DuplicateTerms() {
		rep.add(Finding{
			Severity: SeverityError,
			Check:    "duplicate-glossary-term",
			Path:     reg.Rel(reg.GlossaryPath()),
			Message:  fmt.Sprintf("term %q is defined more than once", term),
			Hint:     "merge the definitions into one entry, or give the second term a name of its own — a word with two definitions has none",
		})
	}
}

// lintDuplicateGlossaryCategories reports a category id declared more than
// once.
func lintDuplicateGlossaryCategories(reg Registry, rep *Report, glossary *Glossary) {
	for _, id := range glossary.DuplicateCategories() {
		rep.add(Finding{
			Severity: SeverityError,
			Check:    "duplicate-glossary-category",
			Path:     reg.Rel(reg.GlossaryPath()),
			Message:  fmt.Sprintf("category %q is declared more than once", id),
			Hint:     "merge the declarations into one, or give the second an id of its own — two headings with one name are one heading a reader cannot read",
		})
	}
}

// lintUndeclaredGlossaryCategories reports a term naming a category the file
// does not declare.
func lintUndeclaredGlossaryCategories(reg Registry, rep *Report, glossary *Glossary) {
	declared := glossary.CategoryIDs()
	// In the order the terms are written, which is the project's own and the
	// order somebody reading the file will meet them in.
	for _, term := range glossary.Terms {
		id := strings.TrimSpace(term.Category)
		if id == "" || glossary.CategoryDeclared(id) {
			continue
		}
		rep.add(Finding{
			Severity: SeverityError,
			Check:    "undeclared-glossary-category",
			Path:     reg.Rel(reg.GlossaryPath()),
			Message: fmt.Sprintf("term %q names category %q, which the glossary does not declare",
				strings.TrimSpace(term.Term), id),
			Hint: fmt.Sprintf("declared categories: %s — correct the term's category, or declare the category in the 'categories' list of %s",
				nameList(declared), reg.Rel(reg.GlossaryPath())),
		})
	}
}

// lintEmptyGlossaryCategories reports a declared category no term names.
func lintEmptyGlossaryCategories(reg Registry, rep *Report, glossary *Glossary) {
	for _, id := range glossary.UnusedCategories() {
		rep.add(Finding{
			Severity: SeverityWarning,
			Check:    "empty-glossary-category",
			Path:     reg.Rel(reg.GlossaryPath()),
			Message:  fmt.Sprintf("category %q is declared but no term names it", id),
			Hint:     "put the terms under it, or remove the category — if a rename left it behind, the terms that still name the old id are reported separately",
		})
	}
}

// lintGlossaryNearMiss reports a word in a record's prose that is not a defined
// term but is very nearly one.
func lintGlossaryNearMiss(reg Registry, rep *Report, features []Record, glossary *Glossary) {
	terms := glossary.TermSet()
	judge := newNearMissJudge(terms)

	for _, rec := range features {
		// Sorted output from a per-record map, so a registry linted twice says
		// the same thing in the same order (FACET §6.3).
		type miss struct{ word, term, where string }
		seen := map[string]miss{}
		var order []string

		for _, field := range scannedProse(rec) {
			// Exact occurrences are blanked before the walk, so a term that
			// contains a space is never judged one word at a time — see
			// maskTerms.
			for _, word := range words(maskTerms(field.Text, terms)) {
				if _, already := seen[word]; already {
					continue
				}
				term := judge.suggest(word)
				if term == "" {
					continue
				}
				seen[word] = miss{word: word, term: term, where: field.Where}
				order = append(order, word)
			}
		}
		sort.Strings(order)

		for _, word := range order {
			m := seen[word]
			rep.add(Finding{
				Severity: SeverityWarning,
				Check:    "glossary-near-miss",
				ID:       rec.ID,
				Path:     reg.Rel(rec.Path),
				Message: fmt.Sprintf("%q in %s is not a defined glossary term — did you mean %q?",
					m.word, m.where, m.term),
				Hint: "correct the spelling so it links to its definition, or add the word to " +
					reg.Rel(reg.GlossaryPath()) + " if it really is a term of its own",
			})
		}
	}
}
