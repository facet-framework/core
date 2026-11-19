// @facet: registry/integrity/unanchored-hotspots
// @facet-role: primary
package facet

import (
	"path/filepath"
	"sort"
)

// UnanchoredFile is one file no record claims, in a neighbourhood that is
// otherwise documented. (Hotspot is taken: defects.go ranks features by
// fragility, which is a different question about a different thing.)
type UnanchoredFile struct {
	// Path is relative to the registry root.
	Path string
	// Loc is the file's line count.
	Loc int
	// Dir is the directory the judgement was made in, relative to the root.
	Dir string
	// DirAnchoredPercent is how much of that directory, by line count, does
	// carry an anchor. The number is the whole argument: it is what makes this
	// file's silence stand out rather than be the local norm.
	DirAnchoredPercent int
}

// Thresholds for the hotspot check.
const (
	hotspotDirAnchoredPercent = 60
	hotspotMinLines           = 100
)

// UnanchoredHotspots reports unanchored files that sit in well-anchored
// directories.
func UnanchoredHotspots(root string, files []SourceFile, isTest func(string) bool) []UnanchoredFile {
	if isTest == nil {
		isTest = IsTestPath
	}
	// Neither candidate nor evidence: prose, empty files, anything that
	// declared itself out of the scan, and test code.

	counts := func(f SourceFile) bool {
		return f.Loc > 0 && !f.OptedOut && !isTest(relTo(root, f.Path))
	}

	type bucket struct{ anchored, total int }
	dirs := map[string]*bucket{}

	for _, f := range files {
		if !counts(f) {
			continue
		}
		dir := filepath.Dir(f.Path)
		b := dirs[dir]
		if b == nil {
			b = &bucket{}
			dirs[dir] = b
		}
		b.total += f.Loc
		if f.Anchored {
			b.anchored += f.Loc
		}
	}

	var out []UnanchoredFile
	for _, f := range files {
		if f.Anchored || !counts(f) || f.Loc < hotspotMinLines {
			continue
		}
		b := dirs[filepath.Dir(f.Path)]
		if b == nil || b.total == 0 {
			continue
		}
		share := b.anchored * 100 / b.total
		if share < hotspotDirAnchoredPercent {
			continue
		}
		out = append(out, UnanchoredFile{
			Path:               relTo(root, f.Path),
			Loc:                f.Loc,
			Dir:                relTo(root, filepath.Dir(f.Path)),
			DirAnchoredPercent: share,
		})
	}

	// Largest first: the biggest undocumented file in a documented directory is
	// the one worth a question, and a reader who acts on one line acts on that.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Loc != out[j].Loc {
			return out[i].Loc > out[j].Loc
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// relTo renders an absolute path against the root, falling back to the path as
// given when it lies outside — a scan aimed at a directory above the registry
// can produce one.
func relTo(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}
