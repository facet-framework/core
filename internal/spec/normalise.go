package spec

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	messages "github.com/cucumber/messages/go/v33"
)

type Sequence struct {
	Steps    []Step       `json:"steps" yaml:"steps"`
	Examples [][][]string `json:"examples,omitempty" yaml:"examples,omitempty,flow"`
}

type Step struct {
	Type      messages.StepKeywordType `json:"type" yaml:"type"`
	Text      string                   `json:"text" yaml:"text"`
	DocString *DocString               `json:"doc_string,omitempty" yaml:"doc_string,omitempty"`
	DataTable [][]string               `json:"data_table,omitempty" yaml:"data_table,omitempty,flow"`
}

type DocString struct {
	MediaType string `json:"media_type" yaml:"media_type"`
	Content   string `json:"content" yaml:"content"`
}

func (s Sequence) Digest() string { return digest(s) }

// These value types contain only JSON primitives and slices in source order.
func digest(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data))
}

func normaliseText(text string) string { return strings.Join(strings.Fields(text), " ") }

func normaliseSteps(source []*messages.Step) []Step {
	steps := make([]Step, 0, len(source))
	previous := messages.StepKeywordType_UNKNOWN
	for _, raw := range source {
		kind := raw.KeywordType
		if kind == messages.StepKeywordType_CONJUNCTION {
			kind = previous
		}
		step := Step{Type: kind, Text: normaliseText(raw.Text)}
		if raw.DocString != nil {
			step.DocString = &DocString{MediaType: raw.DocString.MediaType, Content: raw.DocString.Content}
		}
		if raw.DataTable != nil {
			for _, row := range raw.DataTable.Rows {
				step.DataTable = append(step.DataTable, cells(row))
			}
		}
		steps = append(steps, step)
		previous = kind
	}
	return steps
}

func cells(row *messages.TableRow) []string {
	result := make([]string, 0, len(row.Cells))
	for _, cell := range row.Cells {
		result = append(result, cell.Value)
	}
	return result
}

func normaliseScenario(s *messages.Scenario) Sequence {
	sequence := Sequence{Steps: normaliseSteps(s.Steps)}
	for _, example := range s.Examples {
		table := make([][]string, 0, 1+len(example.TableBody))
		if example.TableHeader != nil {
			table = append(table, cells(example.TableHeader))
		}
		for _, row := range example.TableBody {
			table = append(table, cells(row))
		}
		sequence.Examples = append(sequence.Examples, table)
	}
	return sequence
}

// NormaliseStatement parses one record statement in the given dialect (English
// when empty). A statement consists of steps and their arguments.
func NormaliseStatement(statement, language string) (Sequence, error) {
	if language == "" {
		language = "en"
	}
	dialect := dialectFor(language)
	if dialect == nil {
		return Sequence{}, fmt.Errorf("unsupported Gherkin language %q", language)
	}
	body := dialect.FeatureKeywords()[0] + ": Record\n" + dialect.ScenarioKeywords()[0] + ": Statement\n" + statement
	feature, err := parse(body, dialect)
	if err != nil {
		return Sequence{}, fmt.Errorf("invalid Gherkin statement: %w", err)
	}
	if len(feature.Children) == 1 {
		s := feature.Children[0].Scenario
		if s != nil && s.Description == "" && len(s.Steps) > 0 && len(s.Examples) == 0 {
			return normaliseScenario(s), nil
		}
	}
	return Sequence{}, fmt.Errorf("statement must contain only Gherkin steps and their arguments")
}
