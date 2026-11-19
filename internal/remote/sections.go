package remote

import (
	"errors"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

type recordSection struct {
	Name              string
	Start, End, Lines int
}

// AST positions select exact source bytes. Scalar contents cannot introduce keys.
func splitRecordSections(data string) ([]recordSection, error) {
	if strings.ContainsRune(strings.ReplaceAll(data, "\r\n", ""), '\r') {
		return nil, errors.New("record contains unsupported bare carriage return")
	}
	tree, err := parser.ParseBytes([]byte(strings.ReplaceAll(data, "\r\n", "\n")), 0)
	if err != nil || len(tree.Docs) != 1 {
		return nil, errors.New("invalid section document")
	}
	mapping, ok := tree.Docs[0].Body.(*ast.MappingNode)
	if !ok || mapping.IsFlowStyle {
		return nil, errors.New("published records require a block mapping")
	}
	starts := []int{0}
	for i, c := range data {
		if c == '\n' {
			starts = append(starts, i+1)
		}
	}
	offset := func(line int) int {
		if line < 1 || line > len(starts) {
			return len(data)
		}
		return starts[line-1]
	}
	sections := []recordSection{}
	appendSection := func(name string, start, end int) {
		text := data[start:end]
		lines := strings.Count(text, "\n")
		if len(text) > 0 && !strings.HasSuffix(text, "\n") {
			lines++
		}
		sections = append(sections, recordSection{name, start, end, lines})
	}
	for i, value := range mapping.Values {
		key, ok := value.Key.(*ast.StringNode)
		if !ok {
			return nil, errors.New("section key must be a string")
		}
		start := offset(key.GetToken().Position.Line)
		if i == 0 {
			start = 0
		}
		end := len(data)
		if i+1 < len(mapping.Values) {
			end = offset(mapping.Values[i+1].Key.GetToken().Position.Line)
		}
		if end <= start || strings.Contains(key.Value, ".") {
			return nil, errors.New("ambiguous section layout")
		}
		appendSection(key.Value, start, end)
		children, ok := value.Value.(*ast.MappingNode)
		if !ok || children.IsFlowStyle {
			continue
		}
		for j, child := range children.Values {
			childKey, ok := child.Key.(*ast.StringNode)
			if !ok || strings.Contains(childKey.Value, ".") {
				continue
			}
			childStart := offset(childKey.GetToken().Position.Line)
			childEnd := end
			if j+1 < len(children.Values) {
				childEnd = offset(children.Values[j+1].Key.GetToken().Position.Line)
			}
			if childStart > start && childEnd > childStart {
				appendSection(key.Value+"."+childKey.Value, childStart, childEnd)
			}
		}
	}
	return sections, nil
}
func (r Record) section(name string) (recordSection, bool) {
	for _, s := range r.sections {
		if s.Name == name {
			return s, true
		}
	}
	return recordSection{}, false
}
