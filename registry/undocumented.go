// @facet: registry/integrity/undocumented-directories
// @facet-role: primary
package facet

import (
	"path/filepath"
	"sort"
	"strings"
)

// UndocumentedDir is a directory in which no file carries an anchor at all.
type UndocumentedDir struct {
	// Dir is relative to the registry root.
	Dir string
	// Loc is the directory's line count, tests and opted-out files excluded.
	Loc int
	// Files is how many files that line count is spread over.
	Files int
	// Largest is the biggest file in it, relative to the root. "2655 lines
	// somewhere" is not something a reader can start on; a file name is.
	Largest string
}

// Thresholds for the undocumented-directory check.
const (
	undocumentedRepoAnchoredPercent = 25
	undocumentedMinLines            = 300
)

// UndocumentedDirectories reports directories in which nothing is anchored, in
// a repository that anchors a reasonable share of its code.
func UndocumentedDirectories(root string, files []SourceFile, isTest func(string) bool) []UndocumentedDir {
	if isTest == nil {
		isTest = IsTestPath
	}

	counts := func(f SourceFile) bool {
		return f.Loc > 0 && !f.OptedOut && !isTest(relTo(root, f.Path))
	}

	anchoredExt := map[string]bool{}
	for _, f := range files {
		if f.Anchored && counts(f) {
			anchoredExt[strings.ToLower(filepath.Ext(f.Path))] = true
		}
	}
	carries := func(f SourceFile) bool {
		return anchoredExt[strings.ToLower(filepath.Ext(f.Path))]
	}

	type bucket struct {
		anchored int
		loc      int
		files    int
		largest  string
		biggest  int
	}
	dirs := map[string]*bucket{}
	var repoLoc, repoAnchored int

	for _, f := range files {
		if !counts(f) {
			continue
		}
		repoLoc += f.Loc
		if f.Anchored {
			repoAnchored += f.Loc
		}
		if !carries(f) {
			continue
		}
		dir := filepath.Dir(f.Path)
		b := dirs[dir]
		if b == nil {
			b = &bucket{}
			dirs[dir] = b
		}
		b.loc += f.Loc
		b.files++
		if f.Anchored {
			b.anchored += f.Loc
		}
		if f.Loc > b.biggest {
			b.biggest = f.Loc
			b.largest = relTo(root, f.Path)
		}
	}

	if repoLoc == 0 || repoAnchored*100/repoLoc < undocumentedRepoAnchoredPercent {
		return nil
	}

	var out []UndocumentedDir
	for dir, b := range dirs {
		if b.anchored > 0 || b.loc < undocumentedMinLines {
			continue
		}
		out = append(out, UndocumentedDir{
			Dir:     relTo(root, dir),
			Loc:     b.loc,
			Files:   b.files,
			Largest: b.largest,
		})
	}

	// Largest first: the biggest hole is the one worth a question, and a
	// reader who acts on one line acts on that.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Loc != out[j].Loc {
			return out[i].Loc > out[j].Loc
		}
		return out[i].Dir < out[j].Dir
	})
	return out
}
