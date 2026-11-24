// @facet: registry/integrity/runner-test-execution
// @facet-role: related

package facet

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	gherkin "github.com/cucumber/gherkin/go/v41"
	messages "github.com/cucumber/messages/go/v33"
)

// Cucumber's legacy JSON format, emitted by Badeball in run mode. Explicit
// feature URIs bind evidence; names, tags and process exits never imply paths.
type cucumberStep struct {
	Line   int64 `json:"line"`
	Hidden bool  `json:"hidden"`
	Result struct {
		Status string `json:"status"`
	} `json:"result"`
}
type cucumberElement struct {
	Line int64 `json:"line"`
	Tags []struct {
		Name string `json:"name"`
	} `json:"tags"`
	Type   string         `json:"type"`
	Steps  []cucumberStep `json:"steps"`
	Before []cucumberStep `json:"before"`
	After  []cucumberStep `json:"after"`
}
type cucumberFeature struct {
	URI      string            `json:"uri"`
	Elements []cucumberElement `json:"elements"`
}

func parseCucumberResults(root, cwd string, data []byte, files []string, specFiles ...*specTestFile) (map[string]string, error) {
	var features []cucumberFeature
	if err := json.Unmarshal(data, &features); err != nil {
		return nil, err
	}
	if features == nil {
		return nil, fmt.Errorf("expected Cucumber JSON feature array")
	}
	results, err := cucumberFileResults(root, cwd, features, files)
	if err == nil && len(specFiles) > 0 {
		measureSpecCases(cwd, features, specFiles)
	}
	return results, err
}

func cucumberFileResults(root, cwd string, features []cucumberFeature, files []string) (map[string]string, error) {
	allowed := map[string]bool{}
	for _, file := range files {
		allowed[file] = true
	}
	results := map[string]string{}
	observed := map[string]map[int64][]cucumberStep{}
	duplicates := map[string]bool{}
	for _, feature := range features {
		file, err := normalizedTestPath(root, cwd, feature.URI)
		if err != nil || !allowed[file] || feature.URI == "" {
			continue
		}
		if observed[file] == nil {
			observed[file] = map[int64][]cucumberStep{}
		}
		var background []cucumberStep
		state, scenarios := TestPassed, 0
		for _, element := range feature.Elements {
			if element.Type == "background" {
				background = append(background, element.Steps...)
			}
			if element.Type == "scenario" {
				if _, exists := observed[file][element.Line]; exists {
					duplicates[file] = true
				}
				observed[file][element.Line] = append(append([]cucumberStep{}, background...), element.Steps...)
				background = nil
			}
			if element.Type != "background" {
				scenarios++
			}
			if element.Type != "background" && (element.Type != "scenario" || len(element.Steps) == 0) {
				mergeFileState(results, file, TestUnmeasured)
			}
			steps := append(append(append([]cucumberStep{}, element.Before...), element.Steps...), element.After...)
			for _, step := range steps {
				switch step.Result.Status {
				case "passed":
				case "failed", "ambiguous":
					state = TestFailed
				default:
					if state != TestFailed {
						state = TestUnmeasured
					}
				}
			}
		}
		if scenarios == 0 && state != TestFailed {
			state = TestUnmeasured
		}
		mergeFileState(results, file, state)
	}
	for file, state := range results {
		if state == TestFailed {
			continue
		}
		expected, err := expectedCucumberCases(root, file)
		if err != nil {
			return nil, fmt.Errorf("Cucumber source %s: %w", file, err)
		}
		complete := len(expected) > 0 && len(expected) == len(observed[file]) && !duplicates[file]
		for line, steps := range observed[file] {
			required, exists := expected[line]
			actual := map[int64]bool{}
			for _, step := range steps {
				actual[step.Line] = true
			}
			if !exists || len(actual) != len(required) {
				complete = false
			}
			for stepLine := range required {
				if !actual[stepLine] {
					complete = false
				}
			}
		}
		if !complete {
			mergeFileState(results, file, TestUnmeasured)
		}
	}
	return results, nil
}

// Compile real Gherkin, including dialects, rules, backgrounds and every example
// row. Explicit source lines bind the report to all cases and all source steps.
func expectedCucumberCases(root, file string) (map[int64]map[int64]bool, error) {
	source, err := os.Open(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil {
		return nil, err
	}
	defer source.Close()
	cases, err := compileCucumberCases(source, file)
	if err != nil {
		return nil, err
	}
	expected := make(map[int64]map[int64]bool, len(cases))
	for _, c := range cases {
		expected[c.line] = c.steps
	}
	return expected, nil
}

type compiledCucumberCase struct {
	line  int64
	steps map[int64]bool
	tags  []string
}

func compileCucumberCases(source io.Reader, file string) ([]compiledCucumberCase, error) {
	next := 0
	newID := func() string { next++; return strconv.Itoa(next) }
	document, err := gherkin.ParseGherkinDocument(source, newID)
	if err != nil {
		return nil, err
	}
	var expected []compiledCucumberCase
	if document.Feature == nil {
		return expected, nil
	}
	stepLines := map[string]int64{}
	scenarioTags := map[string][]string{}
	collect := func(steps []*messages.Step) {
		for _, step := range steps {
			stepLines[step.Id] = int64(step.Location.Line)
		}
	}
	collectScenario := func(scenario *messages.Scenario) {
		collect(scenario.Steps)
		for _, tag := range scenario.Tags {
			scenarioTags[scenario.Id] = append(scenarioTags[scenario.Id], tag.Name)
		}
	}
	for _, child := range document.Feature.Children {
		if child.Background != nil {
			collect(child.Background.Steps)
		}
		if child.Scenario != nil {
			collectScenario(child.Scenario)
		}
		if child.Rule != nil {
			for _, ruleChild := range child.Rule.Children {
				if ruleChild.Background != nil {
					collect(ruleChild.Background.Steps)
				}
				if ruleChild.Scenario != nil {
					collectScenario(ruleChild.Scenario)
				}
			}
		}
	}
	for _, pickle := range gherkin.Pickles(*document, file, newID) {
		steps := map[int64]bool{}
		for _, step := range pickle.Steps {
			for _, id := range step.AstNodeIds {
				if line, ok := stepLines[id]; ok {
					steps[line] = true
				}
			}
		}
		expected = append(expected, compiledCucumberCase{int64(pickle.Location.Line), steps, scenarioTags[pickle.AstNodeIds[0]]})
	}
	return expected, nil
}
