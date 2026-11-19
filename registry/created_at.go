package facet

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// CreatedAtSlackDays is the allowed lead time before the oldest anchored source
// file. The 14-day tolerance favours silence when documentation and code arrive
// separately: Git dates file entries, not the start of a product decision.
const CreatedAtSlackDays = 14

// FirstAdded maps repository-relative paths to their first commit day in one
// git log pass, avoiding a subprocess per file in hooks. It returns no result
// outside a repository, on Git failure, or for shallow history. Callers must
// treat an unavailable measurement as unexamined, never as passing dates.
func FirstAdded(root string) map[string]string {

	if shallow, err := exec.Command("git", "-C", root, "rev-parse", "--is-shallow-repository").Output(); err != nil ||
		strings.TrimSpace(string(shallow)) == "true" {
		return nil
	}
	out, err := exec.Command("git", "-C", root, "log",
		"--diff-filter=A", "--name-only", "--reverse",
		"--date=short", "--format=\x1f%ad").Output()
	if err != nil {
		return nil
	}
	added := map[string]string{}
	day := ""
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "\x1f") {
			day = strings.TrimSpace(line[1:])
			continue
		}
		path := strings.TrimSpace(line)
		if path == "" || day == "" {
			continue
		}
		// --reverse walks oldest first, so the first time a path appears is the
		// day it arrived. A path that was added, deleted and added again keeps
		// its first day: the later add is a return, not an origin.
		if _, seen := added[path]; !seen {
			added[path] = day
		}
	}
	return added
}

// createdAt reads a record's lineage.created_at, or "" when it carries none.
func createdAt(rec Record) string {
	lineage, ok := rec.Fields["lineage"].(map[string]any)
	if !ok {
		return ""
	}
	switch raw := lineage["created_at"].(type) {
	case string:
		return raw
	case time.Time:
		// A bare YYYY-MM-DD parses as a timestamp under YAML 1.1, so the field
		// arrives typed about half the time depending on quoting.
		return raw.Format("2006-01-02")
	default:
		return ""
	}
}

// earliestAnchorDay is the day the oldest file this record anchors first
// entered history, or "" when none of them is in git.
func earliestAnchorDay(anchors []Anchor, added map[string]string) string {
	earliest := ""
	for _, anchor := range anchors {
		day, ok := added[anchor.Path]
		if !ok {
			continue
		}
		if earliest == "" || day < earliest {
			earliest = day
		}
	}
	return earliest
}

// lintCreatedAt warns when created_at follows the record file's first commit,
// or precedes the oldest anchored source file by more than CreatedAtSlackDays.
// Comparing code as well as the record catches implausibly early dates even
// when a record is split from an older parent. Git cannot date product intent,
// so both findings are warnings. Unavailable history produces no finding.
// @facet: registry/integrity/created-at-history
// @facet-role: primary
func lintCreatedAt(reg Registry, rep *Report, features []Record, byID map[string][]Anchor, added map[string]string) {
	if len(added) == 0 {
		return
	}
	for _, rec := range features {
		declared := createdAt(rec)
		if declared == "" {
			continue
		}
		day, err := time.Parse("2006-01-02", declared)
		if err != nil {
			// The shape of the field is the schema's business. This check reads
			// dates it can read and stays out of the rest.
			continue
		}
		rel := reg.Rel(rec.Path)

		if fileDay, ok := added[rel]; ok {
			if arrived, err := time.Parse("2006-01-02", fileDay); err == nil && day.After(arrived) {
				rep.add(Finding{
					Severity: SeverityWarning,
					Check:    "created-at-history",
					ID:       rec.ID,
					Path:     rel,
					Message: fmt.Sprintf("lineage.created_at is %s, but this file first appeared on %s — the record claims to be newer than the file it stands in",
						declared, fileDay),
					Hint: "read the file's history ('git log --follow --diff-filter=A -- <path>'): either the date was guessed, or the record was moved and the date belongs to its earlier path",
				})
			}
		}

		codeDay := earliestAnchorDay(byID[rec.ID], added)
		if codeDay == "" {
			continue
		}
		appeared, err := time.Parse("2006-01-02", codeDay)
		if err != nil {
			continue
		}
		gap := int(appeared.Sub(day).Hours() / 24)
		if gap > CreatedAtSlackDays {
			rep.add(Finding{
				Severity: SeverityWarning,
				Check:    "created-at-history",
				ID:       rec.ID,
				Path:     rel,
				Message: fmt.Sprintf("lineage.created_at is %s, %d days before the oldest code this record anchors (%s)",
					declared, gap, codeDay),
				Hint: fmt.Sprintf("a gap over %d days usually means the date was estimated; the code this record describes did not exist yet. If the feature really did predate its code here, say so — the date is a claim, and this check only asks", CreatedAtSlackDays),
			})
		}
	}
}
