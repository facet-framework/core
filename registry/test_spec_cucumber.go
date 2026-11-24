package facet

import (
	"path/filepath"
	"slices"
)

// @facet: registry/integrity/spec-execution
// @facet-role: related
func measureSpecCases(dir string, report []cucumberFeature, files []*specTestFile) {
	reportByPath := make(map[string][]cucumberFeature)
	for _, feature := range report {
		if feature.URI == "" {
			continue
		}
		path := feature.URI
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, filepath.FromSlash(path))
		}
		path = filepath.Clean(path)
		reportByPath[path] = append(reportByPath[path], feature)
	}
	for _, file := range files {
		measureSpecFile(file, reportByPath[filepath.Clean(file.path)])
	}
}

type reportedSpecCase struct {
	line  int64
	steps []cucumberStep
	state string
}

func measureSpecFile(file *specTestFile, reports []cucumberFeature) {
	expected := map[string][]compiledCucumberCase{}
	for _, c := range file.expected {
		for _, tag := range c.tags {
			if _, claimed := file.cases[tag]; claimed {
				expected[tag] = append(expected[tag], c)
			}
		}
	}

	observed := map[string][]reportedSpecCase{}
	for _, report := range reports {
		var background []cucumberStep
		for _, element := range report.Elements {
			switch element.Type {
			case "background":
				background = append(background, element.Steps...)
			case "scenario":
				steps := slices.Concat(background, element.Steps)
				state := specStepState(slices.Concat(element.Before, steps, element.After))
				// Some Cucumber formatters put hooks in the step array.
				sourceSteps := slices.DeleteFunc(steps, func(step cucumberStep) bool {
					return step.Hidden && step.Line == 0
				})
				tags := map[string]bool{}
				for _, tag := range element.Tags {
					if _, claimed := file.cases[tag.Name]; claimed {
						tags[tag.Name] = true
					}
				}
				if len(tags) > 1 {
					state = TestUnproven
				}
				for tag := range tags {
					observed[tag] = append(observed[tag], reportedSpecCase{element.Line, sourceSteps, state})
				}
				background = nil
			default:
				background = nil
			}
		}
	}

	for tag, claims := range file.cases {
		state := specCaseState(expected[tag], observed[tag])
		for _, result := range claims {
			result.State = state
		}
	}
}

func specStepState(steps []cucumberStep) string {
	state := TestPassed
	for _, step := range steps {
		switch step.Result.Status {
		case "passed":
		case "failed", "ambiguous":
			return TestFailed
		default:
			state = TestUnproven
		}
	}
	return state
}

func specCaseState(expected []compiledCucumberCase, observed []reportedSpecCase) string {
	if len(observed) == 0 {
		return TestNotRun
	}
	for _, c := range observed {
		if c.state == TestFailed {
			return TestFailed
		}
	}
	if len(expected) == 0 || len(expected) < len(observed) {
		return TestUnproven
	}
	// The tag selects the scenario. Source step locations check completeness,
	// and repeated outline rows cannot substitute for missing rows.
	seenRows := map[int64]bool{}
	for _, actual := range observed {
		if actual.state != TestPassed || len(actual.steps) == 0 {
			return TestUnproven
		}
		lines := make(map[int64]bool, len(actual.steps))
		for _, step := range actual.steps {
			lines[step.Line] = true
		}
		if len(lines) != len(actual.steps) {
			return TestUnproven
		}
		matched := false
		for _, candidate := range expected {
			if len(expected) > 1 && actual.line != candidate.line {
				continue
			}
			if seenRows[candidate.line] || len(lines) != len(candidate.steps) {
				continue
			}
			matched = true
			for line := range candidate.steps {
				if !lines[line] {
					matched = false
					break
				}
			}
			if matched {
				seenRows[candidate.line] = true
				break
			}
		}
		if !matched {
			return TestUnproven
		}
	}
	if len(observed) < len(expected) {
		return TestNotRun
	}
	return TestPassed
}

func markSpecReportUnavailable(files []*specTestFile, problem string) {
	for _, file := range files {
		for _, claims := range file.cases {
			for _, result := range claims {
				if result.State == TestUnmeasured && result.Message == "" {
					result.Message = problem
				}
			}
		}
	}
}
