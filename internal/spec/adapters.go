package spec

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

func specDirectory(relativePath string) (string, bool) {
	dir, name, ok := strings.Cut(filepath.ToSlash(relativePath), "/")
	return dir, ok && name == "spec.md"
}

// markdownLines visits visible lines with their original line numbers.
func markdownLines(data []byte, visit func(int, string)) {
	var fence string
	var comment bool
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if fence != "" {
			if closingFence(line, fence) {
				fence = ""
			}
			continue
		}
		if comment {
			var closed bool
			_, line, closed = strings.Cut(line, "-->")
			if !closed {
				continue
			}
			comment = false
		}
		if before, after, ok := strings.Cut(line, "<!--"); ok {
			_, rest, closed := strings.Cut(after, "-->")
			comment = !closed
			line = before + rest
		}
		if _, marker, _, ok := openingFence(line); ok {
			fence = marker
			continue
		}
		visit(i+1, line)
	}
}

type openSpecAdapter struct{}

var (
	openSpecSlugSeparator = regexp.MustCompile(`[^a-z0-9]+`)
	openSpecBullet        = regexp.MustCompile(`^-\s+\*\*([[:alpha:]]+)\*\*(.*)$`)
	openSpecKeywords      = map[string]string{"GIVEN": "Given", "WHEN": "When", "THEN": "Then", "AND": "And"}
)

func (openSpecAdapter) Matches(relativePath string) bool {
	dir, ok := specDirectory(relativePath)
	return ok && segmentPattern.MatchString(dir)
}

// @facet: registry/integrity/spec-adapters
// @facet-role: primary

func (openSpecAdapter) Read(_, path string, data []byte) (Document, []Finding) {
	doc := Document{ID: filepath.Base(filepath.Dir(path)), Status: "approved", Language: "en"}
	var body strings.Builder
	fmt.Fprintf(&body, "Feature: %s\n", doc.ID)
	var findings []Finding
	add := func(line int, message string) {
		findings = append(findings, Finding{path, line, message, Exact})
	}
	type scenario struct {
		name       string
		line       int
		when, then bool
		steps      []string
	}
	var current scenario
	var requirement string
	seen := make(map[string]int)
	finish := func() {
		if current.line == 0 {
			return
		}
		defer func() { current = scenario{} }()
		if !current.when || !current.then {
			add(current.line, "OpenSpec scenario must contain WHEN and THEN steps")
		}
		if requirement == "" || current.name == "" {
			return
		}
		slug := caseSlugFromName(requirement + " " + current.name)
		if first, exists := seen[slug]; exists {
			add(current.line, fmt.Sprintf("duplicate OpenSpec case ID %q (first used on line %d)", slug, first))
			return
		}
		seen[slug] = current.line
		if !current.when || !current.then {
			return
		}
		statement := strings.Join(current.steps, "\n")
		sequence, err := NormaliseStatement(statement, "en")
		if err != nil {
			add(current.line, err.Error())
			return
		}
		doc.Units = append(doc.Units, Unit{
			Address: doc.ID + "#" + slug, Line: current.line, Binding: Exact,
			Digest: sequence.Digest(), ContextDigest: digest([2][]Step{}), Sequence: &sequence,
		})
		fmt.Fprintf(&body, "\n  @case:%s\n  Scenario: %s\n%s\n", slug, current.name, statement)
	}
	markdownLines(data, func(number int, raw string) {
		line := strings.TrimSpace(raw)
		if heading, _, ok := strings.Cut(line, " "); ok {
			switch heading {
			case "#", "##", "###", "####":
				finish()
				if heading != "####" {
					name, _ := strings.CutPrefix(line, "### Requirement:")
					requirement = strings.TrimSpace(name)
				}
			}
		}
		if name, ok := strings.CutPrefix(line, "#### Scenario:"); ok {
			current = scenario{name: strings.TrimSpace(name), line: number}
			if requirement == "" || current.name == "" {
				add(number, "OpenSpec scenario requires a name and an enclosing named requirement")
			}
			return
		}
		if current.line == 0 {
			return
		}
		match := openSpecBullet.FindStringSubmatch(line)
		if match == nil {
			return
		}
		keyword := openSpecKeywords[match[1]]
		if keyword == "" {
			add(number, fmt.Sprintf("unknown OpenSpec step keyword %q", match[1]))
			return
		}
		if match[2] == "" || strings.TrimSpace(match[2]) == "" || !strings.HasPrefix(match[2], " ") && !strings.HasPrefix(match[2], "\t") {
			add(number, "OpenSpec step requires whitespace followed by text")
			return
		}
		if keyword == "And" && len(current.steps) == 0 {
			add(number, "OpenSpec AND step requires a preceding step")
			return
		}
		current.when = current.when || keyword == "When"
		current.then = current.then || keyword == "Then"
		current.steps = append(current.steps, "    "+keyword+" "+strings.TrimSpace(match[2]))
	})
	finish()
	doc.blocks = []FeatureBlock{{Body: body.String(), CaseTag: "case"}}
	return doc, findings
}

type specKitAdapter struct{}

var specKitItem = regexp.MustCompile(`^(?:-\s+)?\*\*((?:FR|SC)-[0-9]+)\*\*:\s*(.*)$`)

func (specKitAdapter) Matches(relativePath string) bool {
	dir, ok := specDirectory(relativePath)
	return ok && numberedSpecID.MatchString(dir)
}

// @facet: registry/integrity/spec-adapters
// @facet-role: primary

func (specKitAdapter) Read(_, path string, data []byte) (Document, []Finding) {
	doc := Document{ID: filepath.Base(filepath.Dir(path)), Status: "draft", Language: "en"}
	var findings []Finding
	seen := make(map[string]int)
	var id, text string
	var line, indent int
	finish := func() {
		if id == "" {
			return
		}
		defer func() { id, text = "", "" }()
		text = normaliseText(text)
		if first, exists := seen[id]; exists {
			findings = append(findings, Finding{path, line, fmt.Sprintf("duplicate Spec Kit ID %q (first used on line %d)", id, first), Provenance})
			return
		}
		seen[id] = line
		if text == "" {
			findings = append(findings, Finding{path, line, fmt.Sprintf("Spec Kit item %q requires text", id), Provenance})
			return
		}
		doc.Units = append(doc.Units, Unit{
			Address: doc.ID + "#" + id, Line: line, Binding: Provenance,
			Digest: digest(text), Text: text,
		})
	}
	markdownLines(data, func(number int, raw string) {
		trimmed := strings.TrimSpace(raw)
		match := specKitItem.FindStringSubmatch(trimmed)
		if match == nil && id != "" && trimmed != "" && len(raw)-len(strings.TrimLeft(raw, " \t")) > indent {
			text += " " + trimmed
			return
		}
		finish()
		if status, ok := strings.CutPrefix(trimmed, "**Status**:"); ok {
			doc.Status = approvedSpecStatus(status)
		}
		if match != nil {
			id, text, line = strings.ToLower(match[1]), match[2], number
			indent = len(raw) - len(strings.TrimLeft(raw, " \t"))
		}
	})
	finish()
	return doc, findings
}

func caseSlugFromName(name string) string {
	slug := strings.Trim(openSpecSlugSeparator.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if slug == "" || slug[0] < 'a' || slug[0] > 'z' {
		return strings.TrimSuffix("case-"+slug, "-")
	}
	return slug
}

func approvedSpecStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "accepted", "approved":
		return "approved"
	default:
		return "draft"
	}
}
