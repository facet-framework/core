package facet

import (
	"errors"
	"fmt"

	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/facet-framework/core/internal/spec"
)

type specPin struct {
	lock spec.Lock
	err  error
}

func readSpecPin(root string) specPin {
	lock, err := spec.ReadLock(root)
	return specPin{lock, err}
}

// LintSpecConformance runs the offline specification check for the shell suite.
func LintSpecConformance(root string) (Report, error) {
	var rep Report
	cfg, err := LoadConfigUnresolved(root)
	if err != nil {
		return rep, err
	}
	pin := readSpecPin(root)
	for _, reg := range cfg.All() {
		features, err := reg.LoadFeatures()
		if err != nil {
			return rep, err
		}
		lintSpecConformance(reg, &rep, features, pin)
		rep.Checked.Records += len(features)
	}
	rep.Findings = dedupeProjectFindings(rep.Findings)
	return rep, nil
}

// @facet: registry/integrity/spec-conformance
// @facet-role: primary

func lintSpecConformance(reg Registry, rep *Report, features []Record, pin specPin) {
	if pin.err != nil && !os.IsNotExist(pin.err) {
		rep.add(Finding{Severity: SeverityError, Check: "spec-conformance", Path: spec.LockName,
			Message: fmt.Sprintf("unresolved: invalid specification lock [unknown]: %v", pin.err)})
		return
	}
	sources := specSourcesByID(reg)
	for _, rec := range features {
		check := specRecordCheck{reg: reg, rep: rep, record: rec, sources: sources, pin: pin}
		for _, ref := range lineageIDs(rec, "specified_by") {
			check.reference("lineage.specified_by", ref, false)
		}

		contract, _ := rec.Fields["contract"].(map[string]any)
		scenarios, _ := contract["scenarios"].([]any)
		var outlines map[string][]ExamplesBlock
		for _, raw := range scenarios {
			scenario, _ := raw.(map[string]any)
			ref, ok := scenario["specified_by"].(string)
			if !ok {
				continue
			}
			id, _ := scenario["id"].(string)
			field := fmt.Sprintf("scenario %q specified_by", id)
			file, pinned, resolved := check.reference(field, ref, true)
			if !resolved {
				continue
			}
			if err := compareSpecScenario(scenario, file, pinned, func() []ExamplesBlock {
				if outlines == nil {
					outlines = make(map[string][]ExamplesBlock)
					for _, outline := range rec.Outlines() {
						outlines[outline.Scenario] = outline.Blocks
					}
				}
				return outlines[id]
			}); err != nil {
				check.add(SeverityError, TestDiverged, field, ref, pinned.Strength(), err.Error(), specDeviationHint)
			}
		}
	}
}

func compareSpecScenario(scenario map[string]any, file spec.LockedFile, pinned spec.LockedScenario, examples func() []ExamplesBlock) error {
	if pinned.Strength() == spec.Provenance {
		return nil
	}
	statement, _ := scenario["statement"].(string)
	sequence, err := spec.NormaliseStatement(statement, file.Language)
	if err != nil {
		return err
	}
	if scenario["examples"] != nil {
		sequence.Examples = specExampleTables(examples(), pinned.Sequence.Examples)
	}
	if sequence.Digest() != pinned.Steps {
		return fmt.Errorf("sequence differs from the specification pin\n%s", spec.SequenceDiff(*pinned.Sequence, sequence))
	}
	return nil
}

func specSourcesByID(reg Registry) map[string]spec.Source {
	sources := make(map[string]spec.Source, len(reg.specSources))
	for _, source := range reg.specSources {
		sources[source.ID] = source
	}
	return sources
}

const specDeviationHint = "The spec is the source. Change it in the spec repository, or report the deviation. Do not edit the scenario to silence this finding."

type specRecordCheck struct {
	reg     Registry
	rep     *Report
	record  Record
	sources map[string]spec.Source
	pin     specPin
}

func (c specRecordCheck) add(severity Severity, state, field, ref string, binding spec.BindingStrength, message, hint string) {
	c.rep.add(Finding{Severity: severity, Check: "spec-conformance", Registry: c.reg.Name, Path: c.reg.Rel(c.record.Path), ID: c.record.ID,
		Message: fmt.Sprintf("%s: %s %q [%s]: %s", state, field, ref, binding, message), Hint: hint})
}

func (c specRecordCheck) reference(field, ref string, scenario bool) (spec.LockedFile, spec.LockedScenario, bool) {
	metadata, pinned, err := resolveSpecReference(c.reg.Name, c.sources, c.pin, ref, scenario)
	if err != nil {
		c.add(SeverityError, "unresolved", field, ref, referenceBinding(c.sources, ref), err.Error(), "")
		return metadata, pinned, false
	}
	if metadata.Status == "deprecated" {
		binding := pinned.Strength()
		if !scenario {
			id, _, _, _ := spec.Reference(ref, false)
			binding = c.pin.lock[id].Format.Strength()
		}
		c.add(SeverityWarning, "deprecated", field, ref, binding, "spec file is deprecated", "")
	}
	return metadata, pinned, true
}

func referenceBinding(sources map[string]spec.Source, ref string) spec.BindingStrength {
	id, _, _ := strings.Cut(ref, ":")
	if source, ok := sources[id]; ok {
		return source.Format.Strength()
	}
	return spec.UnknownBinding
}

func resolveSpecReference(registry string, sources map[string]spec.Source, pin specPin, ref string, scenario bool) (spec.LockedFile, spec.LockedScenario, error) {
	fail := func(reason string) (spec.LockedFile, spec.LockedScenario, error) {
		return spec.LockedFile{}, spec.LockedScenario{}, errors.New(reason)
	}
	id, file, address, valid := spec.Reference(ref, scenario)
	if !valid {
		return fail("invalid specification reference")
	}
	source, ok := sources[id]
	if !ok {
		return fail("spec source is not declared")
	}
	if source.Registries != nil && !slices.Contains(source.Registries, registry) {
		return fail("spec source is restricted to other registries")
	}
	if os.IsNotExist(pin.err) {
		return fail(spec.LockName + " is missing")
	}
	if pin.err != nil {
		return fail("invalid specification lock: " + pin.err.Error())
	}
	locked, ok := pin.lock[id]
	if !ok {
		return fail("spec source is not in the lock")
	}
	metadata, ok := locked.Files[file]
	if !ok {
		return fail("spec file is not in the lock")
	}
	pinned, found := locked.Scenarios[address]
	if scenario && !found {
		return fail("spec case is not in the lock")
	}
	return metadata, pinned, nil
}

// specExampleTables renders a record's examples as text cells. A record cell
// is a YAML scalar, so 0.50 arrives as the number 0.5; where the pinned spec
// cell at the same position denotes the same scalar, its text is used, and
// only a different value counts as a divergence. A block without columns
// inherits the columns of the block before it.
func specExampleTables(blocks []ExamplesBlock, pinned [][][]string) [][][]string {
	tables := make([][][]string, 0, len(blocks))
	var columns []string
	for b, block := range blocks {
		if block.Columns != nil {
			columns = block.Columns
		}
		table := make([][]string, 0, 1+len(block.Rows))
		table = append(table, columns)
		for r, row := range block.Rows {
			cells := make([]string, 0, len(row))
			for c, value := range row {
				text, hasPinned := pinnedCell(pinned, b, r+1, c)
				cells = append(cells, exampleCell(value, text, hasPinned))
			}
			table = append(table, cells)
		}
		tables = append(tables, table)
	}
	return tables
}

func pinnedCell(pinned [][][]string, table, row, column int) (string, bool) {
	if table >= len(pinned) || row >= len(pinned[table]) || column >= len(pinned[table][row]) {
		return "", false
	}
	return pinned[table][row][column], true
}

func exampleCell(value any, pinned string, hasPinned bool) string {
	switch v := value.(type) {
	case string:
		return v
	case bool:
		if parsed, err := strconv.ParseBool(pinned); hasPinned && err == nil && parsed == v {
			return pinned
		}
		return strconv.FormatBool(v)
	}
	if number, ok := exampleNumber(value); ok {
		if parsed, err := strconv.ParseFloat(pinned, 64); hasPinned && err == nil && parsed == number {
			return pinned
		}
		return strconv.FormatFloat(number, 'f', -1, 64)
	}
	return fmt.Sprint(value)
}

func exampleNumber(value any) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float64:
		return v, true
	}
	return 0, false
}
