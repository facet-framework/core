// @facet: authoring/guardrails/large-record-read
// @facet-role: primary
package facet

import (
	"fmt"
	"os"
	"strings"
)

// Section is one addressable slice of a record file: a top-level key, or a
// key one level below it written as "parent.child".
type Section struct {
	Name  string `json:"name"`
	Bytes int    `json:"bytes"`
	Lines int    `json:"lines"`
	// start and end are line indexes, end exclusive.
	start, end int
}

// SectionOutline splits a record's YAML into its sections without parsing it.
func SectionOutline(data string) []Section {
	lines := strings.SplitAfter(data, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	var out []Section
	parent := ""
	for i, line := range lines {
		name, indent := yamlKey(line)
		if name == "" {
			continue
		}
		switch indent {
		case 0:
			parent = name
			out = append(out, Section{Name: name, start: i})
		case 2:
			if parent != "" {
				out = append(out, Section{Name: parent + "." + name, start: i})
			}
		}
	}
	// Ends: a section runs to the next section at the same or a shallower
	// level. Children end at the next child or the next top-level key.
	for i := range out {
		out[i].end = len(lines)
		depth := strings.Count(out[i].Name, ".")
		for j := i + 1; j < len(out); j++ {
			if strings.Count(out[j].Name, ".") <= depth {
				out[i].end = out[j].start
				break
			}
		}
		out[i].Lines = out[i].end - out[i].start
		for _, l := range lines[out[i].start:out[i].end] {
			out[i].Bytes += len(l)
		}
	}
	if len(out) > 0 {
		// Leading comments and blank lines travel with the first section.
		out[0].start = 0
		out[0].Lines = out[0].end
		out[0].Bytes = 0
		for _, l := range lines[:out[0].end] {
			out[0].Bytes += len(l)
		}
	}
	return out
}

// yamlKey returns the mapping key a line opens and its indentation, or "" for a
// comment, a blank line, a list item or a continuation of a block scalar.
func yamlKey(line string) (string, int) {
	trimmed := strings.TrimLeft(line, " ")
	indent := len(line) - len(trimmed)
	if trimmed == "" || trimmed[0] == '#' || trimmed[0] == '-' || trimmed[0] == '"' || trimmed[0] == '\'' {
		return "", 0
	}
	colon := strings.Index(trimmed, ":")
	if colon <= 0 {
		return "", 0
	}
	key := trimmed[:colon]
	if strings.ContainsAny(key, " \t") {
		return "", 0
	}
	if rest := trimmed[colon+1:]; rest != "" && rest[0] != ' ' && rest[0] != '\n' && rest[0] != '\r' {
		return "", 0
	}
	return key, indent
}

// ReadSections returns the text of the named sections, each headed by a
// comment line naming it, in the order asked. An unknown name is an error that
// lists the names that exist, never a guess at the nearest one.
func ReadSections(data string, names []string) (string, error) {
	outline := SectionOutline(data)
	lines := strings.SplitAfter(data, "\n")
	byName := make(map[string]Section, len(outline))
	var known []string
	for _, s := range outline {
		byName[s.Name] = s
		known = append(known, s.Name)
	}
	var b strings.Builder
	for _, name := range names {
		s, ok := byName[name]
		if !ok {
			return "", fmt.Errorf("no section %q — sections are: %s", name, strings.Join(known, ", "))
		}
		fmt.Fprintf(&b, "# --- %s ---\n", name)
		b.WriteString(strings.Join(lines[s.start:s.end], ""))
		if !strings.HasSuffix(b.String(), "\n") {
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

// LargeRecordBytes is the size above which a record is read by section rather
// than whole. Roughly 1.5k tokens: below it the outline costs about what the
// file does, so there is nothing to save.
const LargeRecordBytes = 6000

// ReadLargeRecordThreshold resolves the threshold, FACET_READ_GATE_BYTES
// overriding the default. A value that is not a positive integer is ignored
// rather than switching the gate off by accident.
func ReadLargeRecordThreshold() int {
	var n int
	if _, err := fmt.Sscanf(os.Getenv("FACET_READ_GATE_BYTES"), "%d", &n); err == nil && n > 0 {
		return n
	}
	return LargeRecordBytes
}

// FormatOutline renders the section list as the one-line-per-section table the
// hook denial and the tool both show.
func FormatOutline(sections []Section) string {
	var b strings.Builder
	for _, s := range sections {
		indent := ""
		if strings.Contains(s.Name, ".") {
			indent = "  "
		}
		fmt.Fprintf(&b, "%s- %s  (%d bytes, %d lines)\n", indent, s.Name, s.Bytes, s.Lines)
	}
	return b.String()
}
