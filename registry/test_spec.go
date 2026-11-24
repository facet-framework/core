package facet

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/facet-framework/core/internal/spec"
)

const (
	TestUnproven   = "unproven"
	TestNotRun     = "not-run"
	TestDiverged   = "diverged"
	TestProvenance = string(spec.Provenance)
)

type ScenarioTestResult struct {
	ID          string               `json:"id"`
	SpecifiedBy string               `json:"specified_by"`
	State       string               `json:"state"`
	Message     string               `json:"message,omitempty"`
	Binding     spec.BindingStrength `json:"binding"`
}

type specTestFile struct {
	path     string
	expected []compiledCucumberCase
	cases    map[string][]*ScenarioTestResult
}

type specTestPlan map[string][]*specTestFile

func specifiedScenarios(rec Record) []map[string]any {
	contract, _ := rec.Fields["contract"].(map[string]any)
	scenarios, _ := contract["scenarios"].([]any)
	var specified []map[string]any
	for _, raw := range scenarios {
		scenario, _ := raw.(map[string]any)
		if ref, ok := scenario["specified_by"].(string); ok && ref != "" {
			specified = append(specified, scenario)
		}
	}
	return specified
}

// @facet: registry/integrity/spec-execution
// @facet-role: primary

func prepareSpecTests(ctx context.Context, reg Registry, records []Record, selected string) (specTestPlan, map[string][]*ScenarioTestResult, func()) {
	type batchKey struct{ source, commit string }
	type batch struct {
		pins   map[string]spec.LockedScenario
		claims map[string][]*ScenarioTestResult
		order  []string
	}
	var keys []batchKey
	batches := map[batchKey]*batch{}
	results := map[string][]*ScenarioTestResult{}
	sources := specSourcesByID(reg)
	pin := readSpecPin(reg.Root)
	for _, rec := range records {
		for _, scenario := range specifiedScenarios(rec) {
			id, _ := scenario["id"].(string)
			ref := scenario["specified_by"].(string)
			result := &ScenarioTestResult{ID: id, SpecifiedBy: ref, State: TestUnmeasured, Binding: referenceBinding(sources, ref)}
			results[rec.ID] = append(results[rec.ID], result)
			file, pinned, err := resolveSpecReference(reg.Name, sources, pin, ref, true)
			if err != nil {
				result.Message = err.Error()
				continue
			}
			result.Binding = pinned.Strength()
			if result.Binding == spec.Provenance {
				result.State = TestProvenance
				continue
			}
			if err := compareSpecScenario(scenario, file, pinned, func() []ExamplesBlock {
				for _, outline := range rec.Outlines() {
					if outline.Scenario == id {
						return outline.Blocks
					}
				}
				return nil
			}); err != nil {
				result.State, result.Message = TestDiverged, err.Error()
				continue
			}
			sourceID, _, address, _ := spec.Reference(ref, true)
			source := sources[sourceID]
			locked := pin.lock[sourceID]
			switch {
			case source.Runner == "":
				result.Message = fmt.Sprintf("configure spec_sources runner for %q", sourceID)
			case selected != "" && selected != source.Runner:
				result.Message = fmt.Sprintf("runner %q was not selected", source.Runner)
			case source.Repo != locked.Repo || source.Format != locked.Format:
				result.Message = fmt.Sprintf("spec source %q differs from the lock; run facet spec update", sourceID)
			default:
				key := batchKey{sourceID, pinned.Commit}
				group := batches[key]
				if group == nil {
					group = &batch{pins: map[string]spec.LockedScenario{}, claims: map[string][]*ScenarioTestResult{}}
					keys = append(keys, key)
					batches[key] = group
				}
				if _, ok := group.pins[address]; !ok {
					group.order = append(group.order, address)
				}
				group.pins[address] = pinned
				group.claims[address] = append(group.claims[address], result)
			}
		}
	}

	plan := specTestPlan{}
	if len(keys) == 0 {
		return plan, results, func() {}
	}
	dir, dirErr := os.MkdirTemp("", "facet-spec-")
	cleanup := func() {
		if dir != "" {
			os.RemoveAll(dir)
		}
	}
	next := 0
	for _, key := range keys {
		group := batches[key]
		source := sources[key.source]
		var blocks map[string]*spec.FeatureBlock
		err := dirErr
		if err == nil {
			blocks, err = spec.PinnedFeatures(ctx, reg.Root, source, key.commit, group.pins)
		}
		files := map[*spec.FeatureBlock]*specTestFile{}
		writeErrors := map[*spec.FeatureBlock]error{}
		for _, address := range group.order {
			block := blocks[address]
			message := "pinned specification case is unavailable or differs from the lock"
			switch {
			case err != nil:
				message = err.Error()
			case block != nil:
				file := files[block]
				if file == nil && writeErrors[block] == nil {
					next++
					path := filepath.Join(dir, fmt.Sprintf("spec-%d.feature", next))
					compiled, compileErr := compileCucumberCases(strings.NewReader(block.Body), path)
					if compileErr != nil {
						writeErrors[block] = fmt.Errorf("cannot compile pinned feature: %w", compileErr)
					} else if err := os.WriteFile(path, []byte(block.Body), 0600); err != nil {
						writeErrors[block] = err
					} else {
						file = &specTestFile{path: path, expected: compiled, cases: map[string][]*ScenarioTestResult{}}
						files[block] = file
						plan[source.Runner] = append(plan[source.Runner], file)
					}
				}
				if file != nil {
					_, slug, _ := strings.Cut(address, "#")
					file.cases["@"+block.CaseTag+":"+slug] = group.claims[address]
					continue
				}
				message = writeErrors[block].Error()
			}
			for _, result := range group.claims[address] {
				result.Message = message
			}
		}
	}
	return plan, results, cleanup
}

func specTestPaths(files []*specTestFile) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.path)
	}
	return paths
}
