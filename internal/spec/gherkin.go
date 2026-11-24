package spec

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	gherkin "github.com/cucumber/gherkin/go/v41"
	messages "github.com/cucumber/messages/go/v33"
)

func dialectFor(language string) *gherkin.Dialect {
	return gherkin.DialectsBuiltin().GetDialect(language)
}

// fixedDialect keeps a Markdown spec or record statement in its declared language.
type fixedDialect struct{ dialect *gherkin.Dialect }

func (d fixedDialect) GetDialect(language string) *gherkin.Dialect {
	if language == d.dialect.Language {
		return d.dialect
	}
	return nil
}

func parse(body string, dialect *gherkin.Dialect) (*messages.Feature, error) {
	var provider gherkin.DialectProvider = gherkin.DialectsBuiltin()
	language := gherkin.DefaultDialect
	if dialect != nil {
		provider = fixedDialect{dialect}
		language = dialect.Language
	}
	var id int
	builder := gherkin.NewAstBuilder(func() string {
		id++
		return strconv.Itoa(id)
	})
	parser := gherkin.NewParser(builder)
	parser.StopAtFirstError(true)
	err := parser.Parse(gherkin.NewScanner(strings.NewReader(body)), gherkin.NewLanguageMatcher(provider, language))
	if err != nil {
		return nil, err
	}
	feature := builder.GetGherkinDocument().Feature
	if feature == nil {
		return nil, fmt.Errorf("expected a Gherkin feature")
	}
	return feature, nil
}

var parserLocation = regexp.MustCompile(`(?m)^\((\d+):\d+\): (.*)$`)

func (doc *Document) readBlocks(path string, blocks []sourceBlock, meta metadata) []Finding {
	var findings []Finding
	seen := make(map[string]int)
	for _, block := range blocks {
		var dialect *gherkin.Dialect
		if meta.Title != "" {
			dialect = dialectFor(meta.Language)
			var err error
			block, err = block.withFeature(meta.Title, dialect)
			if err != nil {
				findings = append(findings, block.parseFinding(path, err))
				continue
			}
		}
		feature, err := parse(block.body, dialect)
		if err != nil {
			findings = append(findings, block.parseFinding(path, err))
			continue
		}
		if doc.Language == "" {
			doc.Language = feature.Language
		}
		body := block.body
		if meta.Title != "" && !block.hasLanguage {
			body = "# language: " + meta.Language + "\n" + body
		}
		blockIndex := len(doc.blocks)
		doc.blocks = append(doc.blocks, FeatureBlock{Body: body, CaseTag: meta.CaseTag})
		scenarios(feature, func(s *messages.Scenario, featureBackground, ruleBackground []Step) {
			slug, line, err := caseSlug(s, meta.CaseTag)
			line = block.fileLine(line)
			if err != nil {
				findings = append(findings, Finding{path, line, err.Error(), Exact})
				return
			}
			if first, exists := seen[slug]; exists {
				findings = append(findings, Finding{path, line, fmt.Sprintf("duplicate case ID %q (first used on line %d)", slug, first), Exact})
				return
			}
			seen[slug] = line
			sequence := normaliseScenario(s)
			doc.Units = append(doc.Units, Unit{
				Address:       doc.ID + "#" + slug,
				Line:          line,
				Digest:        sequence.Digest(),
				ContextDigest: digest([2][]Step{featureBackground, ruleBackground}),
				Binding:       Exact,
				Sequence:      &sequence,
				block:         blockIndex,
			})
		})
	}
	return findings
}

func (b sourceBlock) withFeature(title string, dialect *gherkin.Dialect) (sourceBlock, error) {
	matcher := gherkin.NewLanguageMatcher(fixedDialect{dialect}, dialect.Language)
	lines := strings.Split(b.body, "\n")
	languageLine := -1
	for i, raw := range lines {
		raw = strings.TrimSuffix(raw, "\r")
		line := &gherkin.Line{LineText: raw, TrimmedLineText: strings.TrimLeft(raw, " \t"), LineNumber: i + 1}
		if ok, _, err := matcher.MatchLanguage(line); err != nil {
			return b, err
		} else if ok {
			languageLine = i
			b.hasLanguage = true
		}
		if line.IsEmpty() || line.StartsWith("#") || line.StartsWith("@") {
			continue
		}
		if ok, _, _ := matcher.MatchFeatureLine(line); ok {
			return b, nil
		}
		break
	}
	header := dialect.FeatureKeywords()[0] + ": " + strings.Join(strings.Fields(title), " ")
	b.body = strings.Join(slices.Insert(lines, languageLine+1, header), "\n")
	b.syntheticLine = languageLine + 2
	return b, nil
}

func (b sourceBlock) parseFinding(path string, err error) Finding {
	line, message := b.line, err.Error()
	if parts := parserLocation.FindStringSubmatch(message); parts != nil {
		n, _ := strconv.Atoi(parts[1])
		line, message = b.fileLine(n), parts[2]
	}
	return Finding{path, line, "invalid Gherkin: " + message, Exact}
}

func scenarios(feature *messages.Feature, visit func(*messages.Scenario, []Step, []Step)) {
	var background []Step
	for _, child := range feature.Children {
		switch {
		case child.Background != nil:
			background = normaliseSteps(child.Background.Steps)
		case child.Scenario != nil:
			visit(child.Scenario, background, nil)
		case child.Rule != nil:
			var ruleBackground []Step
			for _, ruleChild := range child.Rule.Children {
				switch {
				case ruleChild.Background != nil:
					ruleBackground = normaliseSteps(ruleChild.Background.Steps)
				case ruleChild.Scenario != nil:
					visit(ruleChild.Scenario, background, ruleBackground)
				}
			}
		}
	}
}

func caseSlug(s *messages.Scenario, tagKind string) (string, int, error) {
	prefix := "@" + tagKind + ":"
	var tags []*messages.Tag
	for _, tag := range s.Tags {
		if strings.HasPrefix(tag.Name, prefix) {
			tags = append(tags, tag)
		}
	}
	switch len(tags) {
	case 0:
		return "", int(s.Location.Line), fmt.Errorf("scenario needs one %s<slug> tag", prefix)
	case 1:
		slug := strings.TrimPrefix(tags[0].Name, prefix)
		line := int(tags[0].Location.Line)
		schemas, err := loadSchemas()
		if err != nil {
			return "", line, err
		}
		if err := schemas.caseID.Validate(slug); err != nil {
			return "", line, fmt.Errorf("invalid case ID %q", slug)
		}
		return slug, line, nil
	default:
		return "", int(tags[1].Location.Line), fmt.Errorf("scenario has more than one %s<slug> tag", prefix)
	}
}
