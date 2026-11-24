package spec

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"gopkg.in/yaml.v3"

	"github.com/facet-framework/core/schema"
)

type metadata struct {
	Title, Language, CaseTag string
}

func defaults() metadata {
	return metadata{Language: "en", CaseTag: "case"}
}

var loadSchemas = sync.OnceValues(compileSchemas)

type schemas struct {
	frontmatter *jsonschema.Schema
	caseID      *jsonschema.Schema
}

func compileSchemas() (schemas, error) {
	compiler := jsonschema.NewCompiler()
	for _, name := range []string{"spec.schema.json", "facet.schema.json"} {
		file, err := schema.Files.Open(name)
		if err != nil {
			return schemas{}, err
		}
		value, err := jsonschema.UnmarshalJSON(file)
		file.Close()
		if err != nil {
			return schemas{}, err
		}
		if err := compiler.AddResource(name, value); err != nil {
			return schemas{}, err
		}
	}
	frontmatter, err := compiler.Compile("spec.schema.json")
	if err != nil {
		return schemas{}, err
	}
	caseID, err := compiler.Compile("facet.schema.json#/definitions/scenario/properties/id")
	return schemas{frontmatter: frontmatter, caseID: caseID}, err
}

type markdownAdapter struct{}

func (markdownAdapter) Matches(relativePath string) bool {
	return filepath.Ext(relativePath) == ".md"
}

var (
	schemaMarker = regexp.MustCompile(`(?m)^(?:schema|"schema"|'schema'):[ \t]*["']?facet-spec/`)
	yamlLine     = regexp.MustCompile(`line (\d+):`)
)

func (markdownAdapter) Read(_, path string, data []byte) (Document, []Finding) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	first, rest, _ := bytes.Cut(data, []byte("\n"))
	if strings.TrimRight(string(first), "\r \t") != "---" {
		return Document{Skipped: true}, nil
	}
	header, body, closed := splitHeader(rest)
	var node yaml.Node
	yamlErr := yaml.Unmarshal(header, &node)
	var values map[string]any
	if yamlErr == nil && len(node.Content) > 0 {
		yamlErr = node.Decode(&values)
	}
	// Any facet-spec version counts as a spec, so an unsupported version is
	// reported by the schema instead of being skipped as ordinary Markdown.
	if declared, _ := values["schema"].(string); !strings.HasPrefix(declared, "facet-spec/") && (yamlErr == nil || !schemaMarker.Match(header)) {
		return Document{Skipped: true}, nil
	}
	fail := func(line int, message string) (Document, []Finding) {
		return Document{}, []Finding{{path, line, message, Exact}}
	}
	if !closed {
		return fail(1, "unclosed spec frontmatter")
	}
	if yamlErr != nil {
		line := 1
		if match := yamlLine.FindStringSubmatch(yamlErr.Error()); match != nil {
			line, _ = strconv.Atoi(match[1])
			line++
		}
		return fail(line, fmt.Sprintf("invalid spec frontmatter: %v", yamlErr))
	}
	schemas, err := loadSchemas()
	if err != nil {
		return fail(1, err.Error())
	}
	if err := schemas.frontmatter.Validate(values); err != nil {
		var findings []Finding
		for _, cause := range err.(*jsonschema.ValidationError).Causes {
			line := 1
			if len(cause.InstanceLocation) > 0 {
				line = keyLine(&node, cause.InstanceLocation[0])
			}
			if extra, ok := cause.ErrorKind.(*kind.AdditionalProperties); ok && len(extra.Properties) > 0 {
				line = keyLine(&node, extra.Properties[0])
			}
			findings = append(findings, Finding{path, line, "invalid spec frontmatter: " + cause.Error(), Exact})
		}
		if len(findings) == 0 {
			return fail(1, "invalid spec frontmatter: "+err.Error())
		}
		return Document{}, findings
	}
	meta := defaults()
	meta.Title = values["title"].(string)
	if language, ok := values["language"].(string); ok {
		meta.Language = language
	}
	if tag, ok := values["case_tag"].(string); ok {
		meta.CaseTag = tag
	}
	if dialectFor(meta.Language) == nil {
		return fail(keyLine(&node, "language"), fmt.Sprintf("unsupported Gherkin language %q", meta.Language))
	}
	doc := Document{ID: values["id"].(string), Status: values["status"].(string), Language: meta.Language}
	findings := doc.readBlocks(path, markdownBlocks(body, 3+bytes.Count(header, []byte("\n"))), meta)
	return doc, findings
}

func splitHeader(rest []byte) (header, body []byte, closed bool) {
	for start := 0; start < len(rest); {
		line, after, hasNewline := bytes.Cut(rest[start:], []byte("\n"))
		if strings.TrimRight(string(line), "\r \t") == "---" {
			return rest[:start], after, true
		}
		if !hasNewline {
			break
		}
		start += len(line) + 1
	}
	return rest, nil, false
}

func keyLine(node *yaml.Node, key string) int {
	mapping := node.Content[0]
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i].Line + 1
		}
	}
	return 1
}

type sourceBlock struct {
	body          string
	line          int
	lineMap       []int
	syntheticLine int
	hasLanguage   bool
}

func (b sourceBlock) fileLine(parserLine int) int {
	if b.syntheticLine != 0 && parserLine >= b.syntheticLine {
		parserLine--
	}
	if len(b.lineMap) > 0 {
		return b.lineMap[min(max(parserLine, 1), len(b.lineMap))-1]
	}
	return b.line + max(parserLine-1, 0)
}

// markdownBlocks returns the fenced code blocks whose info string is exactly
// "gherkin". It follows the CommonMark rules for top-level fences: an opening
// run of at least three backticks or tildes indented by at most three spaces,
// closed by a run of the same character at least as long; a fence left open
// runs to the end of the document. Fences nested in lists or block quotes are
// not specification.
func markdownBlocks(body []byte, startLine int) []sourceBlock {
	var blocks []sourceBlock
	lines := strings.Split(string(body), "\n")
	for i := 0; i < len(lines); i++ {
		indent, marker, info, ok := openingFence(strings.TrimSuffix(lines[i], "\r"))
		if !ok {
			continue
		}
		block := sourceBlock{line: startLine + i}
		var content strings.Builder
		j := i + 1
		for ; j < len(lines); j++ {
			line := strings.TrimSuffix(lines[j], "\r")
			if closingFence(line, marker) {
				break
			}
			if j == len(lines)-1 && line == "" {
				break
			}
			block.lineMap = append(block.lineMap, startLine+j)
			content.WriteString(stripIndent(line, indent))
			content.WriteString("\n")
		}
		if info == "gherkin" {
			block.body = content.String()
			blocks = append(blocks, block)
		}
		i = j
	}
	return blocks
}

func leadingSpaces(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

func openingFence(line string) (indent int, marker string, info string, ok bool) {
	indent = leadingSpaces(line)
	if indent > 3 {
		return 0, "", "", false
	}
	rest := line[indent:]
	if rest == "" || (rest[0] != '`' && rest[0] != '~') {
		return 0, "", "", false
	}
	run := len(rest) - len(strings.TrimLeft(rest, rest[:1]))
	if run < 3 {
		return 0, "", "", false
	}
	marker, info = rest[:run], strings.TrimSpace(rest[run:])
	if marker[0] == '`' && strings.Contains(info, "`") {
		return 0, "", "", false
	}
	return indent, marker, info, true
}

func closingFence(line, marker string) bool {
	indent := leadingSpaces(line)
	if indent > 3 {
		return false
	}
	rest := strings.TrimRight(line[indent:], " \t")
	return len(rest) >= len(marker) && strings.Trim(rest, marker[:1]) == ""
}

func stripIndent(line string, indent int) string {
	return line[min(indent, leadingSpaces(line)):]
}
