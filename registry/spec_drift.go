package facet

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/facet-framework/core/internal/spec"
)

type SpecDriftFinding struct {
	State   string               `json:"state"`
	Source  string               `json:"source"`
	Address string               `json:"address"`
	Records []string             `json:"records"`
	Detail  string               `json:"detail"`
	Diff    string               `json:"diff,omitempty"`
	Binding spec.BindingStrength `json:"binding"`
}

type SpecDriftReport struct {
	Findings []SpecDriftFinding `json:"findings"`
}

// @facet: registry/history/spec-drift
// @facet-role: primary

func CompareSpecDrift(ctx context.Context, root string, regs RegistrySet, sources []spec.Source, cached, diff bool) (SpecDriftReport, error) {
	report := SpecDriftReport{Findings: []SpecDriftFinding{}}
	lock, err := spec.ReadLock(root)
	if err != nil && !os.IsNotExist(err) {
		return report, fmt.Errorf("%s: %w", spec.LockName, err)
	}
	refs, err := loadSpecReferences(regs, sources)
	if err != nil {
		return report, err
	}
	for _, source := range sources {
		current, err := spec.ReadFollow(ctx, root, source, cached)
		if err != nil {
			report.Findings = append(report.Findings, specDriftFinding(spec.Unmeasured, source.ID, "", source.Format.Strength(), nil, err.Error()))
			continue
		}
		report.compare(source.ID, lock[source.ID], current, refs, diff)
	}
	return report, nil
}

type specReferences struct {
	cases, lineage, files map[string][]string
}

func loadSpecReferences(regs RegistrySet, sources []spec.Source) (specReferences, error) {
	refs := specReferences{map[string][]string{}, map[string][]string{}, map[string][]string{}}
	selected := make(map[string]spec.Source, len(sources))
	for _, source := range sources {
		selected[source.ID] = source
	}
	for _, reg := range regs {
		records, err := reg.LoadFeatures()
		if err != nil {
			return refs, err
		}
		for _, record := range records {
			id := regs.Qualify(reg, record.ID)
			add := func(ref string, scenario bool) {
				source, file, _, valid := spec.Reference(ref, scenario)
				entry, included := selected[source]
				if !valid || !included || (entry.Registries != nil && !slices.Contains(entry.Registries, reg.Name)) {
					return
				}
				fileRef := source + ":" + file
				refs.files[fileRef] = append(refs.files[fileRef], id)
				if scenario {
					refs.cases[ref] = append(refs.cases[ref], id)
				} else {
					refs.lineage[ref] = append(refs.lineage[ref], id)
				}
			}
			for _, ref := range lineageIDs(record, "specified_by") {
				add(ref, false)
			}
			contract, _ := record.Fields["contract"].(map[string]any)
			scenarios, _ := contract["scenarios"].([]any)
			for _, raw := range scenarios {
				scenario, _ := raw.(map[string]any)
				if ref, ok := scenario["specified_by"].(string); ok {
					add(ref, true)
				}
			}
		}
	}
	return refs, nil
}

func (r *SpecDriftReport) compare(source string, before, after spec.LockedSource, refs specReferences, diff bool) {
	for _, address := range slices.Sorted(maps.Keys(before.Scenarios)) {
		pin := before.Scenarios[address]
		ref := source + ":" + address
		file, _, _ := strings.Cut(address, "#")
		fileRef := source + ":" + file
		current, found := after.Scenarios[address]
		switch {
		case !found:
			finding := specDriftFinding(spec.Orphaned, source, ref, pin.Strength(), refs.cases[ref], "pinned unit is absent from follow")
			if diff {
				finding.Diff = pin.FollowDiff(spec.LockedScenario{})
			}
			r.Findings = append(r.Findings, finding)
		case pin.Steps != current.Steps || pin.Context != current.Context || pin.Strength() != current.Strength():
			var moved []string
			if pin.Steps != current.Steps {
				if pin.Strength() == spec.Provenance {
					moved = append(moved, "text")
				} else {
					moved = append(moved, "steps")
				}
			}
			if pin.Context != current.Context {
				moved = append(moved, "context")
			}
			if pin.Strength() != current.Strength() {
				moved = append(moved, fmt.Sprintf("binding from %s to %s", pin.Strength(), current.Strength()))
			}
			finding := specDriftFinding(spec.SpecMoved, source, ref, pin.Strength(), refs.cases[ref], strings.Join(moved, " and ")+" changed at follow")
			if diff && pin.Steps != current.Steps {
				finding.Diff = pin.FollowDiff(current)
			}
			r.Findings = append(r.Findings, finding)
		}
		if found && after.Files[file].Status == "approved" && len(refs.cases[ref]) == 0 {
			detail := "no record scenario references this approved pin"
			if len(refs.lineage[fileRef]) == 0 {
				detail += ", and no record claims the file"
			}
			r.Findings = append(r.Findings, specDriftFinding(spec.Unclaimed, source, ref, current.Strength(), refs.lineage[fileRef], detail))
		}
	}
	for _, file := range slices.Sorted(maps.Keys(before.Files)) {
		ref := source + ":" + file
		if before.Files[file].Status != "deprecated" && after.Files[file].Status == "deprecated" && len(refs.files[ref]) != 0 {
			r.Findings = append(r.Findings, specDriftFinding(spec.Deprecated, source, ref, after.Format.Strength(), refs.files[ref], "referenced spec file became deprecated at follow"))
		}
	}
}

func specDriftFinding(state, source, address string, binding spec.BindingStrength, records []string, detail string) SpecDriftFinding {
	records = append([]string{}, records...)
	slices.Sort(records)
	if state == spec.SpecMoved || state == spec.Orphaned {
		detail += ". " + spec.ChangedSpecAdvice
	}
	return SpecDriftFinding{State: state, Binding: binding, Source: source, Address: address, Records: slices.Compact(records), Detail: detail}
}
