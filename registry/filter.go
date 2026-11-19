// @facet: registry/layout/private-anchors
// @facet-role: primary
package facet

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// AnchorBlock is one run of anchor lines as they stand in a working copy,
// together with where they stand.
type AnchorBlock struct {
	Repository        string `yaml:"repository,omitempty"`
	OriginRevision    string `yaml:"origin_revision,omitempty"`
	OriginContentHash string `yaml:"origin_content_hash,omitempty"`
	// OriginWorktree records where this block was first saved. Empty means a
	// legacy sidecar whose provenance is unknown, never a proven local orphan.
	OriginWorktree string `yaml:"origin_worktree,omitempty"`
	// Lines are the anchor lines verbatim, indentation included, so a block
	// goes back exactly as it was written.
	Lines []string `yaml:"lines"`
	// Before is the line the block sat directly above in the cleaned file,
	// and Occurrence which of that line's repetitions — a file's package
	// clause appears once, an empty line does not.
	Before     string `yaml:"before"`
	Occurrence int    `yaml:"occurrence"`
	// AtEnd marks a block that closed the file, which has no line to sit
	// above.
	AtEnd bool `yaml:"at_end,omitempty"`
}

// Placeable reports whether the block's place exists in the given cleaned
// lines. Not stored: whether a block is orphaned is a fact about one checkout
// at one moment, and the sidecar is shared by every worktree of the clone.
func (b AnchorBlock) Placeable(lines []string) bool {
	if b.AtEnd {
		return true
	}
	for _, l := range lines {
		if l == b.Before {
			return true
		}
	}
	return false
}

// PrivateAnchors is the sidecar record for one file: the anchors that live in
// its working copy and nowhere in git.
type PrivateAnchors struct {
	Path   string        `yaml:"path"`
	Blocks []AnchorBlock `yaml:"blocks"`
}

// SidecarDirName is where a clone keeps its private anchors, under the git
// directory every worktree of the clone shares — outside the tree, outside
// every commit, and gone with the clone rather than with a checkout. Shared
// on purpose: the filter's configuration and attributes are shared the same
// way, and a worktree that inherited the filter but not the anchors would
// read as a clone where they had all been lost.
const SidecarDirName = "facet/anchors"

// isAnchorLine reports whether a line is an anchor tag standing alone: a
// comment, and nothing but the tag in it.
func isAnchorLine(text string) bool {
	loc := -1
	for _, re := range [...]string{"@facet:", "@facet-role:", "@facet-proves:"} {
		if i := strings.Index(text, re); i >= 0 && (loc < 0 || i < loc) {
			loc = i
		}
	}
	if loc < 0 {
		return false
	}
	// Whatever precedes the tag may only be comment punctuation. This is the
	// same tolerance the scanner has for languages it has never heard of:
	// nothing here knows what a comment is in Lua, and it does not need to.
	prefix := strings.TrimSpace(text[:loc])
	for _, r := range prefix {
		if !strings.ContainsRune("/#*-;!<%'\"|", r) {
			return false
		}
	}
	return true
}

// filterApplies reports whether the filter touches a file at all. Anchor lines
// are recognised through the same rules the scanner applies, so the filter
// never strips a line the scanner would not read as an anchor — including every
// line in a file that opted out, where an '@facet:' is a fixture and not a
// claim.
func filterApplies(path string, data []byte) bool {
	if proseExtensions[strings.ToLower(filepath.Ext(path))] {
		return false
	}
	if !bytes.Contains(data, anchorMarker) {
		return false
	}
	return !optsOut(data)
}

// splitLines splits content into lines while remembering how to put it back:
// the line ending in use, and whether the file closed with one.
func splitLines(data []byte) (lines []string, crlf bool, trailing bool) {
	s := string(data)
	crlf = strings.Contains(s, "\r\n")
	if crlf {
		s = strings.ReplaceAll(s, "\r\n", "\n")
	}
	trailing = strings.HasSuffix(s, "\n")
	if trailing {
		s = strings.TrimSuffix(s, "\n")
	}
	if s == "" {
		return nil, crlf, trailing
	}
	return strings.Split(s, "\n"), crlf, trailing
}

func joinLines(lines []string, crlf, trailing bool) []byte {
	sep := "\n"
	if crlf {
		sep = "\r\n"
	}
	out := strings.Join(lines, sep)
	if trailing && len(lines) > 0 {
		out += sep
	}
	return []byte(out)
}

// blockKey identifies a block by what it says, so a committed block and a
// working-copy block that read the same are the same block.
func blockKey(lines []string) string {
	trimmed := make([]string, len(lines))
	for i, l := range lines {
		trimmed[i] = strings.TrimSpace(l)
	}
	return strings.Join(trimmed, "\n")
}

// isEmptyCommentLine reports a comment line that carries nothing: ' *' in a
// docblock, '//' in Go, '#', '--'. Openers and closers are not empty — '/**'
// and '*/' hold a comment together.
func isEmptyCommentLine(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" || len(t) > 3 || strings.Contains(t, "/*") || strings.Contains(t, "*/") || strings.ContainsAny(t, "<>") {
		return false
	}
	for _, r := range t {
		if !strings.ContainsRune("/#*-;!%|", r) {
			return false
		}
	}
	return true
}

// isCommentPrefixed reports a line that begins like a comment, opener and
// docblock continuation included.
func isCommentPrefixed(text string) bool {
	t := strings.TrimSpace(text)
	return t != "" && strings.ContainsRune("/#*-;!<%'", rune(t[0]))
}

// anchorRuns finds every maximal run of consecutive anchor lines, as index
// ranges into lines — each widened by the one separator line that came with
// it.
func anchorRuns(lines []string) [][2]int {
	var runs [][2]int
	start := -1
	for i, l := range lines {
		if isAnchorLine(l) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			runs = append(runs, [2]int{start, i})
			start = -1
		}
	}
	if start >= 0 {
		runs = append(runs, [2]int{start, len(lines)})
	}

	prevEnd := 0
	for i := range runs {
		s, e := runs[i][0], runs[i][1]
		switch {
		case s-1 >= prevEnd && s >= 2 && isEmptyCommentLine(lines[s-1]) && isCommentPrefixed(lines[s-2]):
			s--
		case e < len(lines) && isEmptyCommentLine(lines[e]) && (s == 0 || isCommentPrefixed(lines[s-1])):
			// Only when no later run wants this line as its leading
			// separator: the run after ours, if it starts right past it, has
			// the stronger claim, being the one the separator sets off.
			if !(i+1 < len(runs) && runs[i+1][0] == e+1) {
				e++
			}
		}
		runs[i] = [2]int{s, e}
		prevEnd = e
	}
	return runs
}

// StripPrivateAnchors is the clean half.
func StripPrivateAnchors(path string, content, committed []byte) ([]byte, []AnchorBlock) {
	if !filterApplies(path, content) {
		return content, nil
	}
	lines, crlf, trailing := splitLines(content)

	known := map[string]bool{}
	if committed != nil {
		cl, _, _ := splitLines(committed)
		for _, r := range anchorRuns(cl) {
			known[blockKey(cl[r[0]:r[1]])] = true
		}
	}

	var out []string
	var blocks []AnchorBlock
	// Compute placement and occurrence counts against the cleaned output that
	// smudge receives. Track open blocks by index because growing the slice
	// can invalidate pointers to its elements.
	var open []int

	settle := func(next string, atEnd bool) {
		for _, idx := range open {
			b := &blocks[idx]
			if atEnd {
				b.AtEnd = true
				continue
			}
			b.Before = next
			n := 0
			for _, l := range out {
				if l == next {
					n++
				}
			}
			b.Occurrence = n + 1
		}
		open = nil
	}

	runs := anchorRuns(lines)
	ri := 0
	for i := 0; i < len(lines); {
		if ri < len(runs) && runs[ri][0] == i {
			r := runs[ri]
			ri++
			run := lines[r[0]:r[1]]
			if known[blockKey(run)] {
				// Committed: it stays, and it is the line any open private
				// block sits above.
				settle(run[0], false)
				out = append(out, run...)
			} else {
				blocks = append(blocks, AnchorBlock{Lines: append([]string(nil), run...)})
				open = append(open, len(blocks)-1)
			}
			i = r[1]
			continue
		}
		settle(lines[i], false)
		out = append(out, lines[i])
		i++
	}
	settle("", true)

	if len(blocks) == 0 {
		return content, nil
	}
	return joinLines(out, crlf, trailing), blocks
}

// InjectAnchors is the smudge half.
func InjectAnchors(clean []byte, blocks []AnchorBlock) ([]byte, []AnchorBlock) {
	if len(blocks) == 0 {
		return clean, nil
	}
	lines, crlf, trailing := splitLines(clean)
	if len(lines) == 0 && !trailing && len(clean) == 0 {
		trailing = true
	}

	type insertion struct {
		at    int
		lines []string
	}
	var inserts []insertion
	var orphans []AnchorBlock
	var atEnd []string

	for _, b := range blocks {
		if b.AtEnd {
			atEnd = append(atEnd, b.Lines...)
			continue
		}
		want := b.Occurrence
		if want < 1 {
			want = 1
		}
		seen, at := 0, -1
		for i, l := range lines {
			if l == b.Before {
				seen++
				if seen == want {
					at = i
					break
				}
			}
		}
		if at < 0 {
			orphans = append(orphans, b)
			continue
		}
		inserts = append(inserts, insertion{at: at, lines: b.Lines})
	}

	// Earlier insertions first, stable, so two blocks above the same line come
	// back in the order they were written.
	sort.SliceStable(inserts, func(i, j int) bool { return inserts[i].at < inserts[j].at })
	out := make([]string, 0, len(lines)+len(blocks)*2)
	next := 0
	for i, l := range lines {
		for next < len(inserts) && inserts[next].at == i {
			out = append(out, inserts[next].lines...)
			next++
		}
		out = append(out, l)
	}
	out = append(out, atEnd...)
	return joinLines(out, crlf, trailing), orphans
}

// GitDir resolves the directory git keeps a checkout's state in: '.git' when
// it is a directory, or wherever a '.git' file points for a linked worktree
// or a submodule. Resolved by reading rather than by asking git, because the
// filter runs once per file and a subprocess per file is what makes a large
// 'git status' crawl.
func GitDir(root string) (string, error) {
	dot := filepath.Join(root, ".git")
	info, err := os.Stat(dot)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return dot, nil
	}
	data, err := os.ReadFile(dot)
	if err != nil {
		return "", err
	}
	line := strings.TrimRight(string(data), "\r\n")
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return "", errors.New(".git is neither a directory nor a gitdir pointer")
	}
	target := strings.TrimPrefix(strings.TrimPrefix(line, prefix), " ")
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	return filepath.Clean(target), nil
}

// GitCommonDir resolves the directory a clone's worktrees share: the git
// directory itself for the main worktree, and wherever a linked worktree's
// 'commondir' file points otherwise. Config, attributes and the sidecar all
// live there, so one install serves every worktree of the clone.
func GitCommonDir(root string) (string, error) {
	gitDir, err := GitDir(root)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if errors.Is(err, os.ErrNotExist) {
		return gitDir, nil
	}
	if err != nil {
		return "", err
	}
	target := strings.TrimRight(string(data), "\r\n")
	if !filepath.IsAbs(target) {
		target = filepath.Join(gitDir, target)
	}
	return filepath.Clean(target), nil
}

// SidecarPath is where one file's private anchors are kept: the file's own
// path mirrored under the shared git directory, so a reader who wonders what
// is private in a file can open the answer by name.
func SidecarPath(gitDir, rel string) string {
	return filepath.Join(gitDir, SidecarDirName, filepath.FromSlash(rel)+".yaml")
}

// LoadPrivateAnchors reads one file's sidecar; a file without one has none.
func LoadPrivateAnchors(gitDir, rel string) (PrivateAnchors, error) {
	data, err := os.ReadFile(SidecarPath(gitDir, rel))
	if errors.Is(err, os.ErrNotExist) {
		return PrivateAnchors{Path: rel}, nil
	}
	if err != nil {
		return PrivateAnchors{}, err
	}
	var pa PrivateAnchors
	if err := yaml.Unmarshal(data, &pa); err != nil {
		return PrivateAnchors{}, err
	}
	if pa.Path == "" {
		pa.Path = rel
	}
	return pa, nil
}

// SavePrivateAnchors writes one file's sidecar, atomically, or removes it when
// there is nothing left to keep. Atomic because git may run the filter for
// several files at once, and a half-written sidecar is a lost anchor.
func SavePrivateAnchors(gitDir string, pa PrivateAnchors) error {
	path := SidecarPath(gitDir, pa.Path)
	if len(pa.Blocks) == 0 {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(pa)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".anchors-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// MergeSidecar decides what the sidecar holds after a clean: the blocks the
// working copy carries now, plus any previous block whose lines are absent
// but whose place is absent too.
func MergeSidecar(previous PrivateAnchors, found []AnchorBlock, clean []byte) PrivateAnchors {
	present := map[string]bool{}
	for _, b := range found {
		present[blockKey(b.Lines)] = true
	}
	lines, _, _ := splitLines(clean)
	next := PrivateAnchors{Path: previous.Path, Blocks: append([]AnchorBlock(nil), found...)}
	for _, b := range previous.Blocks {
		if present[blockKey(b.Lines)] || b.Placeable(lines) {
			continue
		}
		next.Blocks = append(next.Blocks, b)
	}
	return next
}

// AttributePrivateAnchors preserves existing provenance across shared smudges
// and records the origin only for new blocks. Legacy blocks remain unknown.
func AttributePrivateAnchors(previous PrivateAnchors, found []AnchorBlock, worktree string) {
	for i := range found {
		found[i].OriginWorktree = worktree
		for _, old := range previous.Blocks {
			if blockKey(old.Lines) == blockKey(found[i].Lines) {
				found[i].OriginWorktree = old.OriginWorktree
				found[i].Repository = old.Repository
				found[i].OriginRevision = old.OriginRevision
				found[i].OriginContentHash = old.OriginContentHash
				break
			}
		}
	}
}

// PrivateAnchorWorktree identifies a worktree relative to its shared git
// directory, so relocating a clone does not turn its local anchors foreign.
func PrivateAnchorWorktree(root string) (string, error) {
	common, err := GitCommonDir(root)
	if err != nil {
		return "", err
	}
	dir, err := gitOutput(root, nil, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	id, err := filepath.Rel(common, strings.TrimSpace(string(dir)))
	return filepath.ToSlash(id), err
}

// Orphans reports which of a file's private blocks are not standing in the
// given working-copy content: the ones no smudge could place.
func (pa PrivateAnchors) Orphans(working []byte) []AnchorBlock {
	lines, _, _ := splitLines(working)
	standing := map[string]bool{}
	for _, r := range anchorRuns(lines) {
		standing[blockKey(lines[r[0]:r[1]])] = true
	}
	var out []AnchorBlock
	for _, b := range pa.Blocks {
		if !standing[blockKey(b.Lines)] {
			out = append(out, b)
		}
	}
	return out
}

// ListPrivateAnchors walks every sidecar under a git directory.
func ListPrivateAnchors(gitDir string) ([]PrivateAnchors, error) {
	base := filepath.Join(gitDir, SidecarDirName)
	var out []PrivateAnchors
	err := filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && p == base {
				return nil
			}
			return err
		}
		if info.IsDir() || !strings.HasSuffix(p, ".yaml") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var pa PrivateAnchors
		if err := yaml.Unmarshal(data, &pa); err != nil {
			return err
		}
		if pa.Path == "" {
			rel, _ := filepath.Rel(base, strings.TrimSuffix(p, ".yaml"))
			pa.Path = filepath.ToSlash(rel)
		}
		out = append(out, pa)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// AttributePrivateAnchorsInView records provenance only for new associations.
// Identical tag text at a different declaration is a distinct placement.
//
// @facet: registry/layout/source-views
// @facet-role: related
func AttributePrivateAnchorsInView(previous PrivateAnchors, found []AnchorBlock, worktree, repository, revision, contentHash string) {
	for i := range found {
		found[i].OriginWorktree = worktree
		found[i].Repository = repository
		found[i].OriginRevision = revision
		found[i].OriginContentHash = contentHash
		for _, old := range previous.Blocks {
			if samePrivatePlacement(old, found[i]) {
				found[i].OriginWorktree = old.OriginWorktree
				found[i].Repository = old.Repository
				found[i].OriginRevision = old.OriginRevision
				found[i].OriginContentHash = old.OriginContentHash
				break
			}
		}
	}
}
func samePrivatePlacement(a, b AnchorBlock) bool {
	return blockKey(a.Lines) == blockKey(b.Lines) && a.Before == b.Before && a.Occurrence == b.Occurrence && a.AtEnd == b.AtEnd
}

// MergeSidecarInView preserves other-version associations, including unknown
// legacy origins; only a known association from this exact context is removable.
func MergeSidecarInView(previous PrivateAnchors, found []AnchorBlock, clean []byte, worktree, repository, revision string) PrivateAnchors {
	next := PrivateAnchors{Path: previous.Path, Blocks: append([]AnchorBlock(nil), found...)}
	lines, _, _ := splitLines(clean)
	for _, old := range previous.Blocks {
		present := false
		for _, b := range found {
			present = present || samePrivatePlacement(old, b)
		}
		if present {
			continue
		}
		if old.Repository == repository && old.OriginRevision == revision && old.OriginWorktree == worktree && old.Placeable(lines) {
			continue
		}
		next.Blocks = append(next.Blocks, old)
	}
	return next
}

// InjectAnchorsInView never applies an association explicitly owned by another
// logical repository. Existing exact placement checks handle version changes.
func InjectAnchorsInView(content []byte, blocks []AnchorBlock, repository string) ([]byte, []AnchorBlock) {
	var applicable, unresolved []AnchorBlock
	for _, b := range blocks {
		if b.Repository != "" && b.Repository != repository {
			unresolved = append(unresolved, b)
		} else {
			applicable = append(applicable, b)
		}
	}
	out, orphans := InjectAnchors(content, applicable)
	return out, append(unresolved, orphans...)
}
