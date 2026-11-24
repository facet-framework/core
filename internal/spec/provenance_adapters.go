package spec

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	mdHeading       = regexp.MustCompile(`^(#{1,6})[ \t]+(.+?)\s*#*\s*$`)
	numberedItem    = regexp.MustCompile(`^([0-9]+)[.)](?:[ \t]+(.*))?$`)
	kiroRequirement = regexp.MustCompile(`^Requirement[ \t]+([0-9]+)(?:\b|$)`)
	bmadCriterion   = regexp.MustCompile(`(?i)^(?:[-*+]\s+)?(?:\*\*)?AC[- ]?([0-9]+)(?:\*\*)?:(?:\*\*)?\s*(.*)$`)
	bmadStoryStem   = regexp.MustCompile("^" + storyID + "$")
	gaugeUnderline  = regexp.MustCompile(`^(?:={2,}|-{2,})$`)
	gaugeTeardown   = regexp.MustCompile(`^_{3,}$`)
)

type provenanceDocument struct {
	Document
	path     string
	findings []Finding
	seen     map[string]int
}

func newProvenanceDocument(id, path, status string) provenanceDocument {
	return provenanceDocument{
		Document: Document{ID: id, Status: status, Language: "en"},
		path:     path, seen: make(map[string]int),
	}
}

func (d *provenanceDocument) report(line int, message string) {
	d.findings = append(d.findings, Finding{d.path, line, message, Provenance})
}

func (d *provenanceDocument) add(id string, line int, text string) {
	switch {
	case !segmentPattern.MatchString(id):
		d.report(line, fmt.Sprintf("invalid unit ID %q", id))
	case d.seen[id] != 0:
		d.report(line, fmt.Sprintf("duplicate unit ID %q (first used on line %d)", id, d.seen[id]))
	case text == "":
		d.report(line, fmt.Sprintf("unit %q requires text", id))
	default:
		d.seen[id] = line
		d.Units = append(d.Units, Unit{
			Address: d.ID + "#" + id, Line: line, Binding: Provenance, Digest: digest(text), Text: text,
		})
	}
}

type criterion struct {
	id, text     string
	line, indent int
}

func (c *criterion) continueAt(raw string) bool {
	return c.line != 0 && strings.TrimSpace(raw) != "" && len(raw)-len(strings.TrimLeft(raw, " \t")) > c.indent
}

type kiroAdapter struct{}

func (kiroAdapter) Matches(path string) bool {
	dir, name, ok := strings.Cut(filepath.ToSlash(path), "/")
	return ok && name == "requirements.md" && segmentPattern.MatchString(dir)
}

// @facet: registry/integrity/spec-adapters
// @facet-role: primary

func (kiroAdapter) Read(_, path string, data []byte) (Document, []Finding) {
	doc := newProvenanceDocument(filepath.Base(filepath.Dir(path)), path, "approved")
	var requirement string
	var requirementLevel, criteriaLevel int
	var inCriteria bool
	var current criterion
	finish := func() {
		if current.line != 0 {
			doc.add(current.id, current.line, normaliseText(current.text))
			current = criterion{}
		}
	}
	markdownLines(data, func(line int, raw string) {
		text := strings.TrimSpace(raw)
		if heading := mdHeading.FindStringSubmatch(text); heading != nil {
			finish()
			level := len(heading[1])
			if level <= requirementLevel {
				requirement, inCriteria = "", false
			}
			if level <= criteriaLevel {
				inCriteria = false
			}
			if match := kiroRequirement.FindStringSubmatch(heading[2]); match != nil {
				requirement, requirementLevel, inCriteria = match[1], level, false
			} else if requirement != "" && strings.EqualFold(heading[2], "Acceptance Criteria") {
				inCriteria, criteriaLevel = true, level
			}
			return
		}
		if requirement != "" && strings.EqualFold(strings.Trim(text, "*_:"), "Acceptance Criteria") {
			finish()
			inCriteria, criteriaLevel = true, requirementLevel+1
			return
		}
		if !inCriteria {
			return
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " \t"))
		if current.continueAt(raw) {
			current.text += " " + text
		} else if item := numberedItem.FindStringSubmatch(text); item != nil {
			finish()
			current = criterion{"requirement-" + requirement + "-" + item[1], item[2], line, indent}
		} else if text != "" {
			finish()
		}
	})
	finish()
	if len(doc.Units) == 0 && len(doc.findings) == 0 {
		doc.report(1, "Kiro requirements need numbered acceptance criteria")
	}
	return doc.Document, doc.findings
}

type bmadAdapter struct{}

func (bmadAdapter) Matches(path string) bool {
	return filepath.Ext(path) == ".md"
}

// @facet: registry/integrity/spec-adapters
// @facet-role: primary

func (bmadAdapter) Read(_, path string, data []byte) (Document, []Finding) {
	stem := strings.TrimSuffix(filepath.Base(path), ".md")
	doc := newProvenanceDocument(stem, path, "draft")
	var story, inCriteria bool
	var sectionLine, sectionLevel int
	var all []string
	var items []criterion
	var current criterion
	markdownLines(data, func(line int, raw string) {
		text := strings.TrimSpace(raw)
		if heading := mdHeading.FindStringSubmatch(text); heading != nil {
			level := len(heading[1])
			story = story || level == 1 && strings.HasPrefix(heading[2], "Story ")
			if !inCriteria && sectionLine == 0 && strings.EqualFold(heading[2], "Acceptance Criteria") {
				sectionLine, sectionLevel, inCriteria = line, level, true
				return
			}
			if inCriteria && level <= sectionLevel {
				inCriteria = false
			}
		}
		if sectionLine == 0 {
			if status, ok := strings.CutPrefix(text, "Status:"); ok {
				doc.Status = approvedSpecStatus(status)
			}
		}
		if !inCriteria || text == "" {
			return
		}
		all = append(all, text)
		if current.continueAt(raw) {
			current.text += " " + text
			return
		}
		if current.line != 0 {
			items = append(items, current)
			current = criterion{}
		}
		id, body := "", text
		if ordered := numberedItem.FindStringSubmatch(text); ordered != nil {
			id, body = ordered[1], ordered[2]
		}
		if explicit := bmadCriterion.FindStringSubmatch(body); explicit != nil {
			id, body = explicit[1], explicit[2]
		}
		if id != "" {
			current = criterion{"ac-" + id, body, line, len(raw) - len(strings.TrimLeft(raw, " \t"))}
		}
	})
	if !story && sectionLine == 0 && !bmadStoryStem.MatchString(stem) {
		return Document{Skipped: true}, nil
	}
	if current.line != 0 {
		items = append(items, current)
	}
	if len(items) == 0 {
		doc.add("story", max(1, sectionLine), normaliseText(strings.Join(all, " ")))
	} else {
		for _, item := range items {
			doc.add(item.id, item.line, normaliseText(item.text))
		}
	}
	return doc.Document, doc.findings
}

func gaugeSpecsRoot(dir string) string {
	for filepath.Dir(dir) != dir {
		if filepath.Base(dir) == "specs" {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	return ""
}

type gaugeAdapter struct{}

func (gaugeAdapter) Matches(path string) bool {
	return filepath.Ext(path) == ".spec" || filepath.Ext(path) == ".md"
}

// @facet: registry/integrity/spec-adapters
// @facet-role: primary

func (gaugeAdapter) Read(root, path string, data []byte) (Document, []Finding) {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return Document{}, []Finding{{path, 1, err.Error(), Provenance}}
	}
	doc := newProvenanceDocument(filepath.ToSlash(strings.TrimSuffix(relative, filepath.Ext(path))), path, "approved")
	var feature bool
	var name, id string
	var steps []string
	var start int
	var previous, tags string
	finish := func() {
		if start == 0 {
			return
		}
		for tag := range strings.SplitSeq(tags, ",") {
			if value, ok := strings.CutPrefix(strings.TrimSpace(tag), "id:"); ok {
				value = strings.TrimSpace(value)
				if id != "" || !segmentPattern.MatchString(value) {
					doc.report(start, "Gauge scenario requires a single valid id: tag")
					continue
				}
				id = value
			}
		}
		if id == "" {
			id = caseSlugFromName(name)
		}
		doc.add(id, start, strings.Join(steps, "\n"))
		name, id, tags, steps, start = "", "", "", nil, 0
	}
	markdownLines(data, func(line int, raw string) {
		text := strings.TrimSpace(raw)
		prior := previous
		previous = text
		heading := mdHeading.FindStringSubmatch(text)
		if prior != "" && !strings.HasPrefix(prior, "#") && !strings.HasPrefix(prior, "*") && gaugeUnderline.MatchString(text) {
			line--
			level := "#"
			if text[0] == '-' {
				level = "##"
			}
			heading = []string{"", level, prior}
		}
		if heading != nil {
			switch heading[1] {
			case "#":
				finish()
				if feature {
					doc.report(line, "Gauge specification requires a single specification heading")
				}
				feature = true
			case "##":
				finish()
				if !feature {
					doc.report(line, "Gauge scenario requires a specification heading")
				}
				name, start = heading[2], line
			}
			return
		}
		if gaugeTeardown.MatchString(text) {
			finish()
		}
		if start == 0 {
			return
		}
		if value, ok := strings.CutPrefix(text, "Tags:"); ok {
			tags = strings.TrimSpace(value)
		} else if strings.HasSuffix(tags, ",") && text != raw && !strings.HasPrefix(text, "* ") {
			tags += text
		} else if step, ok := strings.CutPrefix(text, "* "); ok && strings.TrimSpace(step) != "" {
			steps = append(steps, "* "+normaliseText(step))
		}
	})
	finish()
	if !feature && len(doc.findings) == 0 && filepath.Ext(path) == ".md" {
		return Document{Skipped: true}, nil
	}
	if len(doc.Units) == 0 && len(doc.findings) == 0 {
		doc.report(1, "Gauge specification requires a scenario with steps")
	}
	return doc.Document, doc.findings
}

func normalisedGaugeSteps(text string) bool {
	for line := range strings.SplitSeq(text, "\n") {
		if !strings.HasPrefix(line, "* ") || normaliseText(line) != line {
			return false
		}
	}
	return true
}
