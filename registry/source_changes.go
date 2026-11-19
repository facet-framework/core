package facet

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// RepositoryBaseline identifies each independently compared source repository.
type RepositoryBaseline struct {
	RecordHashes   map[string]string `json:"record_hashes,omitempty"`
	Repository     string            `json:"repository,omitempty"`
	RegistryInput  bool              `json:"registry_input,omitempty"`
	IdentityStatus string            `json:"identity_status,omitempty"`
	Path           string            `json:"path"`
	Baseline       Baseline          `json:"baseline"`
}

type sourceChangeSet struct {
	recordHashes  map[string]string
	repository    string
	registryInput bool
	recordsOnly   bool
	prefix        string
	changes       ChangeSet
	files         []FileChange // projected, exclusively owned workspace paths
}

// SourceRepositories includes the workspace repository and the repositories
// containing explicitly declared sources. It does not discover ignored clones
// by walking the workspace.
//
// @facet: registry/history/reference-changes
// @facet-role: related
func SourceRepositories(root string, registries []Registry) []string {
	root, _ = filepath.Abs(root)
	for _, reg := range registries {
		if reg.SourceView != nil {
			var roots []string
			seen := map[string]bool{}
			for _, src := range reg.SourceView.Ordered() {
				if !seen[src.Root] {
					roots = append(roots, src.Root)
					seen[src.Root] = true
				}
			}
			return roots
		}
	}
	seen := map[string]bool{}
	add := func(dir string) {
		out, err := gitOutput(dir, nil, "rev-parse", "--show-toplevel")
		if err != nil {
			return
		}
		repo := filepath.Clean(strings.TrimSpace(string(out)))
		if seen[repo] {
			return
		}
		rel, err := filepath.Rel(root, repo)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return
		}
		seen[repo] = true
	}
	add(root)
	for _, reg := range registries {
		for _, source := range reg.Sources {
			add(filepath.Join(root, source))
		}
	}
	var repos []string
	for repo := range seen {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	return repos
}

// LoadSourceChangeSet projects repository-local paths into the registry's
// workspace. Each repository keeps its own accepted review ref.
func LoadSourceChangeSet(root string, registries []Registry, ref string) ChangeSet {
	return loadSourceChangeSet(root, registries, ref, "")
}

func loadSourceChangeSet(root string, registries []Registry, ref, repository string) ChangeSet {
	root, _ = filepath.Abs(root)
	for _, reg := range registries {
		if reg.SourceView != nil {
			return loadSelectedSourceChanges(root, reg, ref, repository)
		}
	}
	declaredSources := false
	for _, reg := range registries {
		declaredSources = declaredSources || len(reg.Sources) > 0
	}
	if !declaredSources && repository == "" {
		return LoadChangeSet(root, ref)
	}
	repos := SourceRepositories(root, registries)
	if repository != "" {
		selected, _ := filepath.Abs(filepath.Join(root, repository))
		if filepath.IsAbs(repository) {
			selected = filepath.Clean(repository)
		}
		found := false
		for _, repo := range repos {
			found = found || repo == selected
		}
		if !found {
			return ChangeSet{Root: root, Baseline: Baseline{Status: BaselineUnavailable, Error: fmt.Sprintf("repository %q is not a declared source repository", repository)}}
		}
		repos = []string{selected}
	}
	if len(repos) == 0 {
		return LoadChangeSet(root, ref)
	}
	cs := ChangeSet{Root: root}
	for _, repo := range repos {
		local := LoadChangeSet(repo, ref)
		prefix, _ := filepath.Rel(root, repo)
		prefix = filepath.ToSlash(prefix)
		if prefix == "." {
			prefix = ""
		}
		cs.sources = append(cs.sources, sourceChangeSet{prefix: prefix, changes: local})
		for _, f := range local.Files {
			f.Path = sourcePath(prefix, f.Path)
			if f.OldPath != "" {
				f.OldPath = sourcePath(prefix, f.OldPath)
			}
			// An explicitly declared independent clone belongs to its own
			// change set, not the outer repo's untracked-directory entry.
			owned := false
			for _, other := range repos {
				if other == repo {
					continue
				}
				nested, _ := filepath.Rel(repo, other)
				if nested == ".." || strings.HasPrefix(nested, ".."+string(filepath.Separator)) {
					continue
				}
				abs := filepath.Join(root, filepath.FromSlash(f.Path))
				rel, _ := filepath.Rel(other, abs)
				owned = owned || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
			}
			if !owned {
				cs.Files = append(cs.Files, f)
				cs.sources[len(cs.sources)-1].files = append(cs.sources[len(cs.sources)-1].files, f)
			}
		}
	}
	cs.refreshSourceBaseline()
	return cs
}

func sourcePath(prefix, path string) string {
	if prefix == "" {
		return path
	}
	return prefix + "/" + path
}

func (c *ChangeSet) refreshSourceBaseline() {
	if len(c.sources) == 0 {
		return
	}
	c.Baseline = c.sources[0].changes.Baseline
	for _, source := range c.sources {
		if source.changes.Baseline.Status != BaselineOK {
			c.Baseline = source.changes.Baseline
			return
		}
		if !source.changes.Baseline.Reviewed() {
			c.Baseline = source.changes.Baseline
		}
	}
}

func (c ChangeSet) RepositoryBaselines() []RepositoryBaseline {
	if len(c.sources) < 2 && (len(c.sources) == 0 || c.sources[0].prefix == "" && c.sources[0].repository == "") {
		return nil
	}
	var out []RepositoryBaseline
	for _, s := range c.sources {
		path := s.prefix
		if path == "" {
			path = "."
		}
		if s.repository != "" {
			path = s.repository
		}
		identity := "known"
		if s.repository == "" && s.registryInput {
			identity = "unknown"
		}
		if s.repository == "" && !s.registryInput {
			identity = ""
		}
		out = append(out, RepositoryBaseline{RecordHashes: s.recordHashes, IdentityStatus: identity, Repository: s.repository, RegistryInput: s.registryInput, Path: path, Baseline: s.changes.Baseline})
	}
	return out
}

func (c *ChangeSet) sourceReferenceChanges(reg Registry, current []Anchor, known map[string]bool) []ReferenceChange {
	var out []ReferenceChange
	unreadable := map[string]bool{}
	for _, path := range c.Unreadable {
		unreadable[path] = true
	}
	for i := range c.sources {
		s := &c.sources[i]
		if s.recordsOnly {
			continue
		}
		localReg := reg
		localReg.Root = s.changes.Root
		if s.repository != "" {
			localReg.SourceView = nil
		}
		if s.repository == "" && s.prefix != "" && len(reg.Sources) > 0 {
			localReg.Sources = nil
			for _, dir := range reg.Sources {
				dir = filepath.ToSlash(filepath.Clean(dir))
				switch {
				case dir == "." || dir == s.prefix || strings.HasPrefix(s.prefix, dir+"/"):
					localReg.Sources = append(localReg.Sources, ".")
				case strings.HasPrefix(dir, s.prefix+"/"):
					localReg.Sources = append(localReg.Sources, strings.TrimPrefix(dir, s.prefix+"/"))
				}
			}
			if len(localReg.Sources) == 0 {
				continue
			}
		}
		refs := s.changes.ReferenceChanges(localReg, current, known)
		for _, ref := range refs {
			ref.Path = sourcePath(s.prefix, ref.Path)
			if ref.NewPath != "" {
				ref.NewPath = sourcePath(s.prefix, ref.NewPath)
			}
			if s.repository != "" || reg.Claims(ref.Path) {
				out = append(out, ref)
			}
		}
		for _, path := range s.changes.Unreadable {
			path = sourcePath(s.prefix, path)
			if !unreadable[path] {
				c.Unreadable = append(c.Unreadable, path)
				unreadable[path] = true
			}
		}
	}
	c.refreshSourceBaseline()
	return out
}

// ReviewSourceRepositories accepts or resets independently in each selected
// repository. Paths are workspace-relative; a clone's SHA must be selected
// with --repository rather than interpreted in the outer repository.
//
// @facet: registry/history/drift-review
// @facet-role: related
func ReviewSourceRepositories(root string, registries []Registry, repository string, paths []string, outcome string, reset bool) ([]RepositoryBaseline, error) {
	return ReviewSourceRepositoriesFromBaseline(root, registries, repository, paths, outcome, reset, "")
}

// ReviewSourceRepositoriesFromBaseline supports bootstrapping a partial
// catch-up review from the last reviewed revision instead of current HEAD.
func ReviewSourceRepositoriesFromBaseline(root string, registries []Registry, repository string, paths []string, outcome string, reset bool, ref string) ([]RepositoryBaseline, error) {
	cs := loadSourceChangeSet(root, registries, ref, repository)
	if repository != "" && len(cs.sources) == 0 {
		return nil, fmt.Errorf("%s", cs.Baseline.Error)
	}
	if len(cs.sources) == 0 {
		if reset {
			return nil, ClearReview(root)
		}
		b, err := AcceptReviewFromBaseline(root, paths, outcome, ref)
		return []RepositoryBaseline{{Path: ".", Baseline: b}}, err
	}
	if !reset {
		if outcome == "" {
			outcome = ReviewOutcomes[0]
		}
		for _, source := range cs.sources {
			b := source.changes.Baseline
			if b.Status != BaselineOK && !(b.Status == BaselineMissing && b.Kind == BaselineHead) {
				return nil, fmt.Errorf("cannot record a review: %s", b.Error)
			}
		}
	}
	selectedView := false
	for _, reg := range registries {
		selectedView = selectedView || reg.SourceView != nil
	}
	for i, p := range paths {
		if selectedView {
			valid := false
			for _, reg := range registries {
				if reg.SourceView != nil {
					src, rel, e := reg.SourceView.ResolveReference(p)
					if id, path, ok := strings.Cut(p, ":"); ok && id != "" && id == reg.SourceView.RegistryRepository && path != "" && !filepath.IsAbs(path) && filepath.ToSlash(filepath.Clean(path)) == path && path != ".." && !strings.HasPrefix(path, "../") {
						paths[i] = p
						valid = true
						continue
					}
					if e == nil {
						paths[i], e = reg.SourceView.Qualify(src.Repository, rel)
					}
					valid = valid || e == nil
				}
			}
			if !valid {
				return nil, fmt.Errorf("invalid selected source reference %q", p)
			}
			continue
		}
		if filepath.IsAbs(p) || filepath.Clean(p) == ".." || strings.HasPrefix(filepath.Clean(p), ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("review path %q must stay inside the workspace", p)
		}
	}
	if !reset {
		valid := false
		for _, value := range ReviewOutcomes {
			valid = valid || value == outcome
		}
		if !valid {
			return nil, fmt.Errorf("unknown outcome %q", outcome)
		}
	}
	var out []RepositoryBaseline
	for _, s := range cs.sources {
		var localPaths []string
		for _, p := range paths {
			if s.recordsOnly && s.repository == "" {
				continue
			}
			if s.repository != "" {
				if repo, rel, ok := strings.Cut(p, ":"); ok && repo == s.repository {
					localPaths = append(localPaths, rel)
				}
				continue
			}
			p = filepath.ToSlash(filepath.Clean(p))
			if p == "." || p == s.prefix || (s.prefix != "" && strings.HasPrefix(s.prefix, p+"/")) {
				localPaths = append(localPaths, ".")
			} else if s.prefix != "" && strings.HasPrefix(p, s.prefix+"/") {
				localPaths = append(localPaths, strings.TrimPrefix(p, s.prefix+"/"))
			} else if s.prefix == "" {
				inClone := false
				for _, other := range cs.sources {
					if other.prefix != "" && (p == other.prefix || strings.HasPrefix(p, other.prefix+"/")) {
						inClone = true
					}
				}
				if !inClone {
					localPaths = append(localPaths, p)
				}
			}
		}
		if !reset && len(paths) > 0 && len(localPaths) == 0 {
			continue
		}
		path := s.prefix
		if path == "" {
			path = "."
		}
		if reset {
			if err := ClearReview(s.changes.Root); err != nil {
				return out, fmt.Errorf("reset %s: %w", path, err)
			}
		} else {
			b, err := AcceptReviewFromBaseline(s.changes.Root, localPaths, outcome, ref)
			if err != nil {
				return out, fmt.Errorf("accept %s (earlier repositories may already be accepted): %w", path, err)
			}
			if s.repository != "" {
				path = s.repository
			}
			out = append(out, RepositoryBaseline{Repository: s.repository, Path: path, Baseline: b})
		}
	}
	if !reset && len(out) == 0 {
		return nil, fmt.Errorf("no review path belongs to the selected repositories")
	}
	return out, nil
}

// loadSelectedSourceChanges never discovers sibling clones or alternate worktrees.
func loadSelectedSourceChanges(root string, reg Registry, ref, repository string) ChangeSet {
	cs := ChangeSet{Root: root}
	if err := reg.SourceView.Validate(); err != nil {
		return ChangeSet{Root: root, Baseline: Baseline{Status: BaselineUnavailable, Error: err.Error()}}
	}
	for _, src := range reg.SourceView.Ordered() {
		if repository != "" && repository != src.Repository {
			continue
		}
		if err := src.ValidateWorkingTree(); err != nil {
			return ChangeSet{Root: root, Baseline: Baseline{Status: BaselineUnavailable, Error: err.Error()}}
		}
		local := LoadChangeSet(src.Root, ref)
		prefix, _ := filepath.Rel(root, src.Root)
		prefix = filepath.ToSlash(prefix)
		if prefix == "." {
			prefix = ""
		}
		entry := sourceChangeSet{repository: src.Repository, prefix: prefix, changes: local}
		for _, f := range local.Files {
			f.Path = sourcePath(prefix, f.Path)
			if f.OldPath != "" {
				f.OldPath = sourcePath(prefix, f.OldPath)
			}
			entry.files = append(entry.files, f)
			cs.Files = append(cs.Files, f)
		}
		cs.sources = append(cs.sources, entry)
	}
	// The record checkout is an independent comparison input, even when it is
	// deliberately absent from the selected implementation sources.
	registryID := reg.SourceView.RegistryRepository
	hashes, hashErr := registryRecordHashes(reg)
	if hashErr != nil {
		return ChangeSet{Root: root, Baseline: Baseline{Status: BaselineUnavailable, Error: hashErr.Error()}}
	}
	if repository == "" || repository == registryID {
		duplicate := false
		for i := range cs.sources {
			if filepath.Clean(cs.sources[i].changes.Root) == filepath.Clean(root) {
				if registryID != "" && cs.sources[i].repository != registryID {
					return ChangeSet{Root: root, Baseline: Baseline{Status: BaselineUnavailable, Error: "registry repository identity conflicts with selected source identity"}}
				}
				cs.sources[i].registryInput = true
				cs.sources[i].recordHashes = hashes
				duplicate = true
			}
		}
		if !duplicate {
			local := LoadChangeSet(root, ref)
			entry := sourceChangeSet{repository: registryID, registryInput: true, recordsOnly: true, changes: local, recordHashes: hashes}
			for _, f := range local.Files {
				full := filepath.Join(root, filepath.FromSlash(f.Path))
				rel, e := filepath.Rel(reg.RecordsDir, full)
				if e == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
					entry.files = append(entry.files, f)
					cs.Files = append(cs.Files, f)
				}
			}
			cs.sources = append(cs.sources, entry)
		}
	}
	if len(cs.sources) == 0 {
		cs.Baseline = Baseline{Status: BaselineUnavailable, Error: fmt.Sprintf("repository %q is not in the selected source view", repository)}
		return cs
	}
	cs.refreshSourceBaseline()
	return cs
}
