package facet

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Path containment and diff rendering shared by every host write path: a write
// tool resolves the file it is about to write against the directory it may
// write in, and answers with the change it made.

// WithinResolved reports whether a path stays inside a directory once symlinks
// are resolved.
func WithinResolved(dir, path string) bool {

	root, target := ResolveExisting(dir), ResolveExisting(path)
	if root == "" || target == "" {
		return false
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ResolveExisting resolves the nearest ancestor of a path that exists, so a
// directory about to be created can still be compared against.
func ResolveExisting(path string) string {
	for probe := path; ; {
		if resolved, err := filepath.EvalSymlinks(probe); err == nil {
			// The unresolved tail is appended back on, so a caller comparing
			// against this gets the path it asked about rather than its parent.
			rest, relErr := filepath.Rel(probe, path)
			if relErr != nil {
				return resolved
			}
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return ""
		}
		probe = parent
	}
}

// UnifiedDiff renders the change between two versions of a file.
func UnifiedDiff(path, before, after string) string {
	if before == after {
		return ""
	}
	oldLines, newLines := strings.Split(before, "\n"), strings.Split(after, "\n")

	// The common head and tail are what make the result readable: a header edit
	// should show the header, not the whole page.
	head := 0
	for head < len(oldLines) && head < len(newLines) && oldLines[head] == newLines[head] {
		head++
	}
	tail := 0
	for tail < len(oldLines)-head && tail < len(newLines)-head &&
		oldLines[len(oldLines)-1-tail] == newLines[len(newLines)-1-tail] {
		tail++
	}

	const context = 3
	start := head - context
	if start < 0 {
		start = 0
	}
	oldEnd, newEnd := len(oldLines)-tail+context, len(newLines)-tail+context
	if oldEnd > len(oldLines) {
		oldEnd = len(oldLines)
	}
	if newEnd > len(newLines) {
		newEnd = len(newLines)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", path, path)
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", start+1, oldEnd-start, start+1, newEnd-start)
	for i := start; i < head; i++ {
		fmt.Fprintf(&b, " %s\n", oldLines[i])
	}
	for i := head; i < len(oldLines)-tail; i++ {
		fmt.Fprintf(&b, "-%s\n", oldLines[i])
	}
	for i := head; i < len(newLines)-tail; i++ {
		fmt.Fprintf(&b, "+%s\n", newLines[i])
	}
	for i := len(oldLines) - tail; i < oldEnd; i++ {
		fmt.Fprintf(&b, " %s\n", oldLines[i])
	}
	return b.String()
}
