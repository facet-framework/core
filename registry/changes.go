package facet

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Shared git change parsing preserves paths and reads deleted files from the baseline.
// @facet: registry/history/reference-changes
// @facet-role: primary

// Baseline statuses. A status is never folded into an empty result: "no drift"
// and "drift could not be determined" are different statements, and a consumer
// handed an empty list for both would report a repository without commits, or
// a typo in a baseline, as a clean one.
const (
	// BaselineOK means the baseline resolved and the comparison ran.
	BaselineOK = "ok"
	// BaselineMissing means the baseline does not resolve to a commit — a
	// repository with no commit yet, or a revision that does not exist.
	BaselineMissing = "no-baseline"
	// BaselineUnavailable means git could not answer inside a repository: a
	// command that failed part way.
	BaselineUnavailable = "unavailable"
	// BaselineNoRepository means there is no git repository to ask, or no git.
	// Kept apart from BaselineUnavailable because the two call for different
	// responses: a project outside git has no history to lose, while a failed
	// command inside one has hidden whatever it would have found.
	BaselineNoRepository = "no-repository"
)

// Baseline kinds: where the baseline came from, which is what decides how much
// a clean result is worth.
const (
	// BaselineHead is the implicit default: the checkout's own HEAD. Nobody
	// recorded a review; a clean result means only that the working tree
	// matches the last commit.
	BaselineHead = "head"
	// BaselineAccepted is a snapshot 'facet drift accept' recorded in this
	// worktree. Everything after it — committed or not — is unreviewed.
	BaselineAccepted = "accepted"
	// BaselineExplicit is a revision the caller named.
	BaselineExplicit = "explicit"
)

// ScopeWorkingTree names what every change set compares: the working tree,
// with staged, unstaged and untracked changes, against one baseline commit.
// History before the baseline is not examined, and a result saying nothing
// changed is a statement about that window only — never proof that the
// registry was ever reviewed against older commits.
const ScopeWorkingTree = "working-tree-against-baseline"

// AcceptedBaselineRef is where 'facet drift accept' keeps its snapshot.
const AcceptedBaselineRef = "refs/worktree/facet/drift-baseline"

// Review outcomes 'facet drift accept' can record. They describe what the
// reviewer concluded about the changes since the previous baseline; none of
// them is a record field, and none changes a record's lifecycle.
var ReviewOutcomes = []string{"behavior-unchanged", "record-updated", "references-repaired"}

// Baseline is the revision a change set was measured against, as resolved.
type Baseline struct {
	// Ref is what was compared against: the caller's revision, HEAD, or
	// AcceptedBaselineRef.
	Ref string `json:"ref"`
	// Kind is one of BaselineHead, BaselineAccepted, BaselineExplicit.
	Kind string `json:"kind"`
	// Commit is the resolved commit, empty when it did not resolve.
	Commit string `json:"commit,omitempty"`
	// Scope is always ScopeWorkingTree; it travels with the result so a
	// reader never has to assume what window it covers.
	Scope string `json:"scope"`
	// Status is one of BaselineOK, BaselineMissing, BaselineUnavailable.
	Status string `json:"status"`
	// Error carries git's own words when the status is not ok.
	Error string `json:"error,omitempty"`
	// AcceptedAt and Outcome describe the last 'facet drift accept', when
	// the baseline is one.
	AcceptedAt string `json:"acceptedAt,omitempty"`
	Outcome    string `json:"outcome,omitempty"`
}

// Reviewed reports whether anybody recorded a review: an accepted snapshot,
// or a revision the caller chose on purpose. HEAD is neither.
func (b Baseline) Reviewed() bool { return b.Kind != BaselineHead }

// FileChange is one path git reports between the baseline and the working
// tree.
type FileChange struct {
	// Status is git's letter: A, M, D, R, T, or '?' for an untracked file.
	Status byte
	// Path is where the file is now; for a deletion, where it was.
	Path string
	// OldPath is the baseline path of a rename, empty otherwise.
	OldPath string
}

// ChangeSet is everything that moved between a baseline and the working tree.
type ChangeSet struct {
	sources  []sourceChangeSet
	Root     string
	Baseline Baseline
	Files    []FileChange
	// Unreadable lists baseline paths whose previous content git could not
	// produce, so their lost associations cannot be named. Reported, never
	// dropped: an unreadable before-state is missing evidence, not an absence
	// of change.
	Unreadable []string

	// anchorEdits holds the paths whose diff adds or removes an anchor line.
	// Every other modified file kept its anchors, and is not read twice.
	anchorEdits map[string]bool
	before      map[string][]Anchor
}

// LoadChangeSet compares the working tree under root with ref. An empty ref
// means this worktree's accepted baseline when it has one, and HEAD otherwise.
func LoadChangeSet(root, ref string) ChangeSet {
	cs := ChangeSet{Root: root, Baseline: Baseline{Ref: ref, Kind: BaselineExplicit, Scope: ScopeWorkingTree}}

	if _, err := gitOutput(root, nil, "rev-parse", "--git-dir"); err != nil {
		cs.Baseline.Status = BaselineNoRepository
		cs.Baseline.Error = err.Error()
		if ref == "" {
			cs.Baseline.Ref, cs.Baseline.Kind = "HEAD", BaselineHead
		}
		return cs
	}
	if ref == "" {
		cs.Baseline.Ref, cs.Baseline.Kind = "HEAD", BaselineHead
		// The ref's existence decides, not whether it resolves: an accepted
		// baseline that has gone bad must be reported as missing, never
		// silently replaced by HEAD — that would present the uncommitted work
		// alone as everything since the last review.
		if _, err := gitOutput(root, nil, "rev-parse", "--verify", "--quiet", AcceptedBaselineRef); err == nil {
			cs.Baseline.Ref, cs.Baseline.Kind = AcceptedBaselineRef, BaselineAccepted
		}
	}

	commit := resolveCommit(root, cs.Baseline.Ref)
	if commit == "" {
		cs.Baseline.Status = BaselineMissing
		cs.Baseline.Error = "baseline " + cs.Baseline.Ref + " does not resolve to a commit"
		// Nothing to compare with, but the files are still there. A repository
		// before its first commit holds only additions, and the callers that
		// look for new anchored files must still see them.
		if out, err := gitOutput(root, nil, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames"); err == nil {
			for _, entry := range splitNUL(out) {
				if len(entry) > 3 {
					cs.Files = append(cs.Files, FileChange{Status: 'A', Path: entry[3:]})
				}
			}
		}
		return cs
	}
	cs.Baseline.Commit = commit
	if cs.Baseline.Kind == BaselineAccepted {
		cs.Baseline.AcceptedAt, cs.Baseline.Outcome = acceptedMeta(root, commit)
	}

	diff, err := gitOutput(root, nil, "diff", "--no-color", "--no-ext-diff", "-M", "-z", "--name-status", commit, "--")
	if err != nil {
		cs.Baseline.Status = BaselineUnavailable
		cs.Baseline.Error = err.Error()
		return cs
	}
	cs.Files = parseNameStatusZ(diff)

	// Every untracked file by its own name (--untracked-files=all). A wholly
	// new directory reported as one entry is a path nothing downstream can
	// scan, and new features tend to arrive in new directories.

	status, err := gitOutput(root, nil, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames")
	if err != nil {
		cs.Baseline.Status = BaselineUnavailable
		cs.Baseline.Error = err.Error()
		return cs
	}
	untracked := untrackedFromStatusZ(status)
	if cs.Baseline.Reviewed() && len(untracked) > 0 {
		cs.Files, err = reconcileReviewedUntracked(root, commit, cs.Files, untracked)
		if err != nil {
			cs.Baseline.Status, cs.Baseline.Error = BaselineUnavailable, err.Error()
			return cs
		}
	} else {
		cs.Files = append(cs.Files, untracked...)
	}
	cs.Baseline.Status = BaselineOK
	return cs
}

// A review snapshot can contain files absent from the real index, because
// acceptance deliberately never stages them. Compare those files through a
// temporary index seeded with the snapshot; the real index would call them
// deleted and git status would independently call them new, even unchanged.
func reconcileReviewedUntracked(root, commit string, files, untracked []FileChange) ([]FileChange, error) {
	args := []string{"ls-tree", "-r", "-z", "--name-only", commit, "--"}
	for _, f := range untracked {
		args = append(args, f.Path)
	}
	listed, err := gitOutput(root, nil, args...)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, path := range splitNUL(listed) {
		known[path] = true
	}
	if len(known) == 0 {
		return append(files, untracked...), nil
	}
	var out []FileChange
	for _, f := range files {
		if !known[f.Path] {
			out = append(out, f)
		}
	}
	var reviewedPaths []string
	for _, f := range untracked {
		if known[f.Path] {
			reviewedPaths = append(reviewedPaths, f.Path)
		} else {
			out = append(out, f)
		}
	}
	tmp, err := os.MkdirTemp("", "facet-reviewed-untracked-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}
	if _, err := gitOutput(root, env, "read-tree", commit); err != nil {
		return nil, err
	}
	diffArgs := []string{"diff", "--no-color", "--no-ext-diff", "-z", "--name-status", commit, "--"}
	diff, err := gitOutput(root, env, append(diffArgs, reviewedPaths...)...)
	if err != nil {
		return nil, err
	}
	return append(out, parseNameStatusZ(diff)...), nil
}

// untrackedFromStatusZ picks the '??' entries out of 'git status
// --porcelain=v1 -z --no-renames': each entry is "XY path", NUL-terminated,
// and without renames no entry carries a second path.
func untrackedFromStatusZ(out []byte) []FileChange {
	var files []FileChange
	for _, entry := range splitNUL(out) {
		if len(entry) > 3 && entry[0] == '?' && entry[1] == '?' {
			files = append(files, FileChange{Status: '?', Path: entry[3:]})
		}
	}
	return files
}

// anchorPickaxe is what -G looks for. Deliberately looser than the scanner's
// grammar: a file it selects is then scanned properly, and a file it misses
// would be a missed association.
const anchorPickaxe = "@facet:"

// OK reports whether the comparison ran.
func (c ChangeSet) OK() bool { return c.Baseline.Status == BaselineOK }

// Changed lists every path that moved, by its current name; a deleted path by
// the name it had.
func (c ChangeSet) Changed() []string {
	out := make([]string, 0, len(c.Files))
	for _, f := range c.Files {
		out = append(out, f.Path)
	}
	return out
}

// Deleted lists the paths removed since the baseline. A rename's old path is
// not a deletion.
func (c ChangeSet) Deleted() []string {
	var out []string
	for _, f := range c.Files {
		if f.Status == 'D' {
			out = append(out, f.Path)
		}
	}
	return out
}

// Renamed maps each renamed path's baseline name to its current one.
func (c ChangeSet) Renamed() map[string]string {
	out := map[string]string{}
	for _, f := range c.Files {
		if f.Status == 'R' {
			out[f.OldPath] = f.Path
		}
	}
	return out
}

// FollowedRecords is the set of record ids whose files changed — the
// "the record moved with its code" heuristic — or nil when the baseline is
// one somebody chose.
func (c ChangeSet) FollowedRecords(registries []Registry) map[string]bool {
	// A composite comparison supplies candidate touched records; RegistryDrift
	// applies each repository's own review policy to those candidates.
	if len(c.sources) > 0 {
		return TouchedRecords(registries, c.Changed())
	}
	if c.Baseline.Reviewed() {
		return map[string]bool{}
	}
	return TouchedRecords(registries, c.Changed())
}

// parseNameStatusZ reads 'git diff --name-status -z': a status field, then one
// path, or two for a rename or copy (source first, then destination).
func parseNameStatusZ(out []byte) []FileChange {
	fields := splitNUL(out)
	var files []FileChange
	for i := 0; i < len(fields); {
		status := fields[i]
		i++
		if status == "" || i >= len(fields) {
			break
		}
		switch status[0] {
		case 'R', 'C':
			if i+1 >= len(fields) {
				return files
			}
			from, to := fields[i], fields[i+1]
			i += 2
			if status[0] == 'C' {
				// A copy leaves its source where it was; only the destination
				// is new.
				files = append(files, FileChange{Status: 'A', Path: to})
				continue
			}
			files = append(files, FileChange{Status: 'R', Path: to, OldPath: from})
		default:
			files = append(files, FileChange{Status: status[0], Path: fields[i]})
			i++
		}
	}
	return files
}

// splitNUL splits NUL-terminated output, dropping the empty tail.
func splitNUL(out []byte) []string {
	var parts []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

// resolveCommit returns the commit a revision names, or "" when it names none.
func resolveCommit(root, ref string) string {
	out, err := gitOutput(root, nil, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitOutput runs git in root and returns stdout, folding stderr into the error
// so a failure says why. env, when given, is added to the inherited
// environment.
func gitOutput(root string, env []string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, err
		}
		return nil, errors.New(msg)
	}
	return out, nil
}

// Reference-change kinds. They differ in what is left behind, which is what a
// reader has to decide about.
const (
	// RefDeleted: the file carrying the anchor is gone.
	RefDeleted = "deleted"
	// RefRenamed: the file moved and the anchor moved with it. The
	// association survives; what goes stale is any record text that names
	// the old path.
	RefRenamed = "renamed"
	// RefAnchorRemoved: the file is still there and no longer carries the
	// anchor.
	RefAnchorRemoved = "anchor-removed"
)

// Evidence names where a baseline association was read from.
const (
	// EvidenceBaseline: the baseline commit's own content.
	EvidenceBaseline = "baseline"
	// EvidencePrivateSidecar: the clone's private-anchor sidecar. The clean
	// filter keeps such anchors out of every commit, so the baseline cannot
	// show them; the sidecar is the only place they are written down.
	EvidencePrivateSidecar = "private-sidecar"
)

// ReferenceChange is one implementation association that moved or was lost
// between the baseline and the working tree.
type ReferenceChange struct {
	Kind     string `json:"kind"`
	Registry string `json:"registry,omitempty"`
	ID       string `json:"id"`
	Role     string `json:"role,omitempty"`
	// Path is where the anchor stood at the baseline.
	Path string `json:"path"`
	// NewPath is where the file is now, for a rename or for an anchor
	// removed from a file that was also renamed.
	NewPath  string `json:"newPath,omitempty"`
	Evidence string `json:"evidence"`
	// Surviving counts this id's anchors still standing in the working tree.
	// A lost association is reported whatever this says: losing one
	// implementation is worth a look even when another remains. Zero is not
	// upgraded to an error here — the orphan-records check owns that.
	Surviving int `json:"surviving"`
}

// Lost reports whether the change leaves the record without an association it
// had. A rename that carried its anchor along does not.
func (r ReferenceChange) Lost() bool { return r.Kind != RefRenamed }

// ReferenceChanges names, for one registry, every anchor association the
// working tree has moved or lost since the baseline.
func (c *ChangeSet) ReferenceChanges(reg Registry, current []Anchor, known map[string]bool) []ReferenceChange {
	if len(c.sources) > 0 {
		return c.sourceReferenceChanges(reg, current, known)
	}
	if !c.OK() {
		return nil
	}
	if c.anchorEdits == nil {

		c.anchorEdits = map[string]bool{}
		edits, err := gitOutput(c.Root, nil, "diff", "--no-color", "--no-ext-diff", "-M", "-z", "--name-only", "-G"+anchorPickaxe, c.Baseline.Commit, "--")
		if err != nil {
			c.Baseline.Status = BaselineUnavailable
			c.Baseline.Error = err.Error()
			return nil
		}
		for _, p := range splitNUL(edits) {
			c.anchorEdits[p] = true
		}
	}
	surviving := map[string]int{}
	for _, a := range current {
		if a.BelongsTo(reg.Name) {
			surviving[a.ID]++
		}
	}
	recordsPrefix := reg.Rel(reg.RecordsDir) + "/"

	var out []ReferenceChange
	for _, f := range c.Files {
		if f.Status != 'D' && f.Status != 'M' && f.Status != 'R' && f.Status != 'T' {
			continue
		}
		oldPath := f.Path
		if f.Status == 'R' {
			oldPath = f.OldPath
		}
		if strings.HasPrefix(oldPath, recordsPrefix) || !reg.Claims(oldPath) {
			continue
		}
		// A modified or renamed file whose diff touches no anchor line kept
		// every anchor it had; only a rename has something to say about it.
		if f.Status != 'D' && !c.anchorEdits[f.Path] && f.Status != 'R' {
			continue
		}

		var after []Anchor
		if f.Status != 'D' {
			after, _ = ScanFile(filepath.Join(c.Root, filepath.FromSlash(f.Path)))
		}
		// Provenance belongs to the old association, including on a rename.
		// Current anchors carry filesystem paths, not an evidence source.
		before := c.anchorsBefore(oldPath, f.Status == 'D' || f.Status == 'R')
		if len(before) == 0 {
			continue
		}
		standing := map[string]bool{}
		for _, a := range after {
			if a.BelongsTo(reg.Name) {
				standing[a.ID] = true
			}
		}

		seen := map[string]bool{}
		for _, a := range before {
			if !a.BelongsTo(reg.Name) || !known[a.ID] || seen[a.ID] {
				continue
			}
			seen[a.ID] = true
			rc := ReferenceChange{
				Registry:  reg.Name,
				ID:        a.ID,
				Role:      roleFor(before, a.ID),
				Path:      oldPath,
				Evidence:  evidenceFor(before, a.ID),
				Surviving: surviving[a.ID],
			}
			switch {
			case f.Status == 'D':
				rc.Kind = RefDeleted
			case standing[a.ID] && f.Status == 'R':
				rc.Kind = RefRenamed
				rc.NewPath = f.Path
			case standing[a.ID]:
				continue
			default:
				rc.Kind = RefAnchorRemoved
				if f.Status == 'R' {
					rc.NewPath = f.Path
				}
			}
			out = append(out, rc)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// anchorsBefore reads a path's anchors as the baseline had them and, for a
// deleted or renamed file, the private anchors the clone's sidecar holds for
// its old path. Cached: every registry of a project asks about the same files.
func (c *ChangeSet) anchorsBefore(rel string, deleted bool) []Anchor {
	if c.before == nil {
		c.before = map[string][]Anchor{}
	}
	if got, ok := c.before[rel]; ok {
		return got
	}
	abs := filepath.Join(c.Root, filepath.FromSlash(rel))

	var anchors []Anchor
	data, err := gitOutput(c.Root, nil, "show", c.Baseline.Commit+":"+rel)
	if err != nil {
		c.Unreadable = append(c.Unreadable, rel)
	} else {
		for _, a := range ScanContent(abs, data) {
			a.Path = EvidenceBaseline
			anchors = append(anchors, a)
		}
	}

	if deleted {
		if gitDir, err := GitCommonDir(c.Root); err == nil {
			if pa, err := LoadPrivateAnchors(gitDir, rel); err == nil {
				for _, b := range pa.Blocks {
					block := []byte(strings.Join(b.Lines, "\n") + "\n")
					for _, a := range ScanContent(abs, block) {
						a.Path = EvidencePrivateSidecar
						anchors = append(anchors, a)
					}
				}
			}
		}
	}
	c.before[rel] = anchors
	return anchors
}

// roleFor picks the role an id had in a file, preferring primary: a file that
// held both a primary and a related tag for one feature was its
// implementation.
func roleFor(before []Anchor, id string) string {
	role := ""
	for _, a := range before {
		if a.ID != id {
			continue
		}
		if a.Role == "primary" {
			return a.Role
		}
		if role == "" {
			role = a.Role
		}
	}
	return role
}

// evidenceFor names the baseline as the evidence whenever it holds the id, and
// the sidecar only when the sidecar alone does.
func evidenceFor(before []Anchor, id string) string {
	ev := ""
	for _, a := range before {
		if a.ID != id {
			continue
		}
		if a.Path == EvidenceBaseline {
			return EvidenceBaseline
		}
		ev = a.Path
	}
	return ev
}

// acceptedMeta reads the date and outcome an accepted snapshot recorded.
func acceptedMeta(root, commit string) (at, outcome string) {
	out, err := gitOutput(root, nil, "log", "-1", "--format=%cI%x00%B", commit)
	if err != nil {
		return "", ""
	}
	at, body, _ := strings.Cut(string(out), "\x00")
	for _, line := range strings.Split(body, "\n") {
		if v, ok := strings.CutPrefix(line, "Outcome: "); ok {
			outcome = strings.TrimSpace(v)
		}
	}
	return strings.TrimSpace(at), outcome
}

// AcceptReview records that the changes to the given paths — every change in
// the working tree when none are given — have been reviewed, by moving this
// worktree's accepted baseline forward to include them.
func AcceptReview(root string, paths []string, outcome string) (Baseline, error) {
	return AcceptReviewFromBaseline(root, paths, outcome, "")
}

// AcceptReviewFromBaseline starts a partial review at an explicitly confirmed
// revision, so incoming commits outside the reviewed paths remain unreviewed
// even when no accepted snapshot existed before the pull.
func AcceptReviewFromBaseline(root string, paths []string, outcome, ref string) (Baseline, error) {
	if outcome == "" {
		outcome = ReviewOutcomes[0]
	}
	valid := false
	for _, o := range ReviewOutcomes {
		valid = valid || o == outcome
	}
	if !valid {
		return Baseline{}, fmt.Errorf("unknown outcome %q (want one of %s)", outcome, strings.Join(ReviewOutcomes, ", "))
	}

	current := LoadChangeSet(root, ref)
	if ref != "" && !current.OK() {
		return current.Baseline, fmt.Errorf("cannot record a review: %s", current.Baseline.Error)
	}
	if current.Baseline.Status == BaselineUnavailable || current.Baseline.Status == BaselineNoRepository {
		return current.Baseline, fmt.Errorf("cannot record a review: %s", current.Baseline.Error)
	}
	if current.Baseline.Kind == BaselineAccepted && current.Baseline.Commit == "" {
		// Building on nothing would accept every file in the tree at once.
		return current.Baseline, fmt.Errorf("the accepted baseline %s does not resolve; run 'facet drift reset' first", AcceptedBaselineRef)
	}

	tmp, err := os.MkdirTemp("", "facet-accept-")
	if err != nil {
		return Baseline{}, err
	}
	defer os.RemoveAll(tmp)
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}

	parent := current.Baseline.Commit
	readTree := []string{"read-tree", "--empty"}
	if parent != "" {
		readTree = []string{"read-tree", parent}
	}
	if _, err := gitOutput(root, env, readTree...); err != nil {
		return Baseline{}, err
	}
	add := []string{"add", "-A", "--"}
	if len(paths) == 0 {
		add = append(add, ".")
	} else {
		add = append(add, paths...)
	}
	if _, err := gitOutput(root, env, add...); err != nil {
		return Baseline{}, err
	}
	tree, err := gitOutput(root, env, "write-tree")
	if err != nil {
		return Baseline{}, err
	}

	var msg strings.Builder
	msg.WriteString("facet drift accept\n\nOutcome: " + outcome + "\n")
	if len(paths) > 0 {
		msg.WriteString("Paths:\n")
		for _, p := range paths {
			msg.WriteString("- " + p + "\n")
		}
	}
	commitArgs := []string{"commit-tree", strings.TrimSpace(string(tree)), "-m", msg.String()}
	if parent != "" {
		commitArgs = append(commitArgs, "-p", parent)
	}
	// A local marker, not a commit anybody will read in a log: a fixed identity
	// keeps it from failing in a clone with no user configured.
	identity := []string{
		"GIT_AUTHOR_NAME=facet drift", "GIT_AUTHOR_EMAIL=hello@facet-framework.com",
		"GIT_COMMITTER_NAME=facet drift", "GIT_COMMITTER_EMAIL=hello@facet-framework.com",
	}
	commit, err := gitOutput(root, identity, commitArgs...)
	if err != nil {
		return Baseline{}, err
	}
	if _, err := gitOutput(root, nil, "update-ref", AcceptedBaselineRef, strings.TrimSpace(string(commit))); err != nil {
		return Baseline{}, err
	}
	return LoadChangeSet(root, "").Baseline, nil
}

// ClearReview forgets this worktree's accepted baseline, so comparisons fall
// back to HEAD.
func ClearReview(root string) error {
	if _, err := gitOutput(root, nil, "rev-parse", "--verify", "--quiet", AcceptedBaselineRef); err != nil {
		return nil
	}
	_, err := gitOutput(root, nil, "update-ref", "-d", AcceptedBaselineRef)
	return err
}
