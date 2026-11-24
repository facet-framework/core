package spec

import (
	"regexp"
	"strings"
)

type gherkinMarkdownAdapter struct{}

const gherkinMarkdownSuffix = ".feature.md"

var (
	mdFeature        = regexp.MustCompile(`^#{1,6}\s(Feature|Business Need|Ability):`)
	mdTitle          = regexp.MustCompile(`^#{1,6}\s((?:Background|Rule|Scenario|Example|Scenario Outline|Scenario Template|Examples|Scenarios):.*)$`)
	mdStep           = regexp.MustCompile(`^[*+-]\s*((?:Given|When|Then|And|But) .*)$`)
	mdTag            = regexp.MustCompile("`(@[^`]+)`")
	mdTable          = regexp.MustCompile(`^\s{2,5}\|`)
	mdTableSeparator = regexp.MustCompile(`^\|(\s*:?-+:?\s*\|)+\s*$`)
	mdDocString      = regexp.MustCompile("^(`{3,})(.*)$")
)

func (gherkinMarkdownAdapter) Matches(path string) bool {
	return strings.HasSuffix(path, gherkinMarkdownSuffix)
}

// @facet: registry/integrity/spec-adapters
// @facet-role: primary
// @facet: registry/integrity/spec-execution
// @facet-role: related

func (gherkinMarkdownAdapter) Read(root, path string, data []byte) (Document, []Finding) {
	return readFeature(root, path, gherkinMarkdownSuffix, markdownGherkin(string(data)))
}

// markdownGherkin emits classic Gherkin tokens on their original line numbers.
func markdownGherkin(body string) string {
	lines := strings.Split(strings.TrimPrefix(body, "\ufeff"), "\n")
	var feature bool
	var fence string
	// tableRows counts consecutive table lines; a Markdown separator can only
	// be the second line of a table, so a later row of dashes is data.
	tableRows := 0
	for i, raw := range lines {
		raw = strings.TrimSuffix(raw, "\r")
		trimmed := strings.TrimLeft(raw, " \t")
		lines[i] = ""
		if strings.HasPrefix(trimmed, "|") && fence == "" {
			tableRows++
		} else {
			tableRows = 0
		}
		switch {
		case fence != "":
			if trimmed == fence {
				lines[i] = `"""`
				fence = ""
			} else {
				lines[i] = strings.ReplaceAll(raw, `"""`, `\"\"\"`)
			}
		case strings.TrimSpace(raw) == "":
		case tableRows == 2 && mdTableSeparator.MatchString(trimmed):
		case feature && mdTitle.MatchString(raw):
			lines[i] = mdTitle.FindStringSubmatch(raw)[1]
		case feature && mdStep.MatchString(trimmed):
			lines[i] = mdStep.FindStringSubmatch(trimmed)[1]
		case mdTag.MatchString(trimmed):
			var tags []string
			for _, tag := range mdTag.FindAllStringSubmatch(trimmed, -1) {
				tags = append(tags, tag[1])
			}
			lines[i] = strings.Join(tags, " ")
		case !feature:
			feature = true
			if mdFeature.MatchString(raw) {
				lines[i] = raw[strings.IndexAny(raw, " \t")+1:]
			} else {
				lines[i] = "Feature: " + trimmed
			}
		case mdTable.MatchString(raw):
			lines[i] = raw
		case mdDocString.MatchString(trimmed):
			match := mdDocString.FindStringSubmatch(trimmed)
			fence = match[1]
			lines[i] = raw[:len(raw)-len(trimmed)] + `"""` + match[2]
		}
	}
	return strings.Join(lines, "\n")
}
