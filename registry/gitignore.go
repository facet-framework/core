// @facet: registry/layout/source-scanning
// @facet-role: related
package facet

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// GitRelevantSet reports whether a walked path is one git itself considers
// relevant under the root GitRelevantFiles was called with.
type GitRelevantSet struct {
	files    map[string]bool
	prefixes []string // absolute directory paths (embedded git repos), no trailing separator
}

// Contains reports whether path (as gocodewalker's own File.Location - an
// absolute path built the same way this set's entries were) is one git
// itself would show.
func (s *GitRelevantSet) Contains(path string) bool {
	if s.files[path] {
		return true
	}
	for _, p := range s.prefixes {
		if path == p || strings.HasPrefix(path, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// GitRelevantFiles selects tracked and non-ignored untracked files using git ls-files
// --exclude-standard, including local, global and XDG exclusions. Embedded repository
// directory entries are accepted as path prefixes. Returns unavailable outside a Git
// worktree or when Git fails; callers retain their ordinary walker filtering.
// Delegate ignore semantics to Git: walker-local patterns re-scope root rules
// in subdirectories. Exact path matching alone also loses files beneath the
// trailing-slash entries Git uses for embedded repositories.
func GitRelevantFiles(root string) (*GitRelevantSet, bool) {
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		return nil, false
	}
	set := &GitRelevantSet{files: make(map[string]bool)}
	for _, rel := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		if rel == "" {
			continue
		}

		if dir, ok := strings.CutSuffix(rel, "/"); ok {
			set.prefixes = append(set.prefixes, filepath.Join(root, filepath.FromSlash(dir)))
			continue
		}
		set.files[filepath.Join(root, filepath.FromSlash(rel))] = true
	}
	return set, true
}
