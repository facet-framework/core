// Checks documentation coverage across source directories.
package facet

import (
	"fmt"
)

// lintUndocumentedDirectories reports a directory in which nothing is
// anchored at all.
func lintUndocumentedDirectories(reg Registry, rep *Report, walked []SourceFile, isTest func(string) bool) {
	for _, d := range UndocumentedDirectories(reg.Root, walked, isTest) {
		rep.add(Finding{
			Severity: SeverityWarning,
			Check:    "undocumented-directories",
			Path:     d.Dir,
			Message: fmt.Sprintf(
				"%s: %d line(s) across %d file(s), and not one @facet: anchor among them — largest is %s",
				d.Dir, d.Loc, d.Files, d.Largest),
			Hint: "start with the largest file: either it implements something a record already describes, and wants an anchor, or it is a capability nobody wrote down",
		})
	}
}

// lintUnanchoredHotspots reports code no record claims, where the neighbours
// are claimed.
func lintUnanchoredHotspots(reg Registry, rep *Report, walked []SourceFile, isTest func(string) bool) {
	for _, h := range UnanchoredHotspots(reg.Root, walked, isTest) {
		rep.add(Finding{
			Severity: SeverityWarning,
			Check:    "unanchored-hotspots",
			Path:     h.Path,
			Message: fmt.Sprintf(
				"%s (%d lines) carries no @facet: anchor, in a directory that is otherwise %d%% anchored by line count",
				h.Path, h.Loc, h.DirAnchoredPercent),
			Hint: "anchor it to the record that describes it, or write the record it needs — a file this size beside documented neighbours is usually a feature nobody wrote down",
		})
	}
}
