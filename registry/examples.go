// @facet: registry/integrity/scenario-outlines
// @facet-role: primary
package facet

import "regexp"

// placeholderPattern finds the parameters of a Scenario Outline's statement:
// a name in angle brackets, which a reader replaces with a column's value.
var placeholderPattern = regexp.MustCompile(`<([\p{L}_][\p{L}\p{N}_-]*)>`)

// ExamplesBlock is one named set of cases under a Scenario Outline: a heading
// row and the rows beneath it, for one rule.
type ExamplesBlock struct {
	// Name is what the set of cases has in common, or "" when the block is
	// unnamed. Empty means the author wrote no name, never that the block has
	// no cases in common.
	Name string
	// Columns are the columns in effect for this block, inherited or its own.
	Columns []string
	// Inherited records where Columns came from, because a finding about a row
	// in an inheriting block has to point the author at the block that
	// actually declares the headings.
	Inherited bool
	// Rows are the cases, one slice of cells per case, positionally matched to
	// Columns. Cells are kept as decoded values rather than as strings: what
	// every caller here needs is how many there are, and stringifying them
	// would invent a rendering that belongs to a reader.
	Rows [][]any
}

// Outline is one contract scenario that declares examples: the statement whose
// placeholders the cases fill, and the blocks that fill them.
type Outline struct {
	// Feature is the record's id.
	Feature string
	// Scenario is the scenario id the examples belong to.
	Scenario string
	// Path is the record's location, for reporting.
	Path string
	// Statement is the scenario's prose, carried along because the placeholders
	// in it are half of what an outline asserts: without it the blocks are a
	// table nobody can say is used.
	Statement string
	// Blocks are the example blocks, in the order the record declares them.
	Blocks []ExamplesBlock
}

// Placeholders lists the distinct parameters named in the statement, in the
// order they first appear.
func (o Outline) Placeholders() []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range placeholderPattern.FindAllStringSubmatch(o.Statement, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, m[1])
	}
	return out
}

// DeclaredColumns is the union of every block's columns, which is the set a
// placeholder may name.
func (o Outline) DeclaredColumns() map[string]bool {
	out := map[string]bool{}
	for _, b := range o.Blocks {
		for _, c := range b.Columns {
			out[c] = true
		}
	}
	return out
}

// Outlines returns every contract scenario on the record that declares
// examples, in the order the document declares them.
func (r Record) Outlines() []Outline {
	contract, ok := r.Fields["contract"].(map[string]any)
	if !ok {
		return nil
	}
	scenarios, ok := contract["scenarios"].([]any)
	if !ok {
		return nil
	}

	var out []Outline
	for _, raw := range scenarios {
		scenario, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		declared, ok := scenario["examples"].([]any)
		if !ok || len(declared) == 0 {
			continue
		}

		o := Outline{Feature: r.ID, Path: r.Path}
		o.Scenario, _ = scenario["id"].(string)
		o.Statement, _ = scenario["statement"].(string)

		// The inherited set is the first one anybody declares. The schema
		// requires that at least one block does, so an outline reaching this
		// point with none is a record the schema has already faulted; the
		// blocks are still returned, with no columns, so that a caller reports
		// the schema's finding rather than a second one about the rows.
		var shared []string
		for _, rawBlock := range declared {
			block, ok := rawBlock.(map[string]any)
			if !ok {
				continue
			}
			if cols := stringList(block["columns"]); len(cols) > 0 {
				shared = cols
				break
			}
		}

		for _, rawBlock := range declared {
			block, ok := rawBlock.(map[string]any)
			if !ok {
				continue
			}
			b := ExamplesBlock{}
			b.Name, _ = block["name"].(string)
			if cols := stringList(block["columns"]); len(cols) > 0 {
				b.Columns = cols
			} else {
				b.Columns, b.Inherited = shared, true
			}
			if rows, ok := block["rows"].([]any); ok {
				for _, rawRow := range rows {
					row, ok := rawRow.([]any)
					if !ok {
						continue
					}
					b.Rows = append(b.Rows, row)
				}
			}
			o.Blocks = append(o.Blocks, b)
		}

		out = append(out, o)
	}
	return out
}

// stringList reads a YAML sequence of strings, dropping anything that is not
// one. A column name that decoded to something other than a string is the
// schema's finding, and carrying it here as an empty name would make the row
// checks report a second, confusing one.
func stringList(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range raw {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}
