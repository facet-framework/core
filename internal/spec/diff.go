package spec

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SequenceDiff shows the changed portion of the pinned and record sequences.
func SequenceDiff(pinned, record Sequence) string {
	return sequenceDiff(pinned, record, "record")
}

func sequenceDiff(pinned, other Sequence, label string) string {
	return textDiff(sequenceLines(pinned), sequenceLines(other), label)
}

func textLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func textDiff(before, after []string, label string) string {
	for len(before) > 0 && len(after) > 0 && before[0] == after[0] {
		before, after = before[1:], after[1:]
	}
	for len(before) > 0 && len(after) > 0 && before[len(before)-1] == after[len(after)-1] {
		before, after = before[:len(before)-1], after[:len(after)-1]
	}
	var diff strings.Builder
	diff.WriteString("--- lock\n+++ " + label)
	for _, line := range before {
		fmt.Fprintf(&diff, "\n- %s", line)
	}
	for _, line := range after {
		fmt.Fprintf(&diff, "\n+ %s", line)
	}
	return diff.String()
}

func sequenceLines(sequence Sequence) []string {
	var lines []string
	for _, step := range sequence.Steps {
		lines = append(lines, fmt.Sprintf("%s %s", step.Type, step.Text))
		if step.DocString != nil {
			raw, _ := json.Marshal(step.DocString)
			lines = append(lines, "  doc string "+string(raw))
		}
		for _, row := range step.DataTable {
			raw, _ := json.Marshal(row)
			lines = append(lines, "  "+string(raw))
		}
	}
	for _, table := range sequence.Examples {
		lines = append(lines, "Examples")
		for _, row := range table {
			raw, _ := json.Marshal(row)
			lines = append(lines, "  "+string(raw))
		}
	}
	return lines
}
