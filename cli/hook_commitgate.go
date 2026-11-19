// @facet: authoring/guardrails/commit-gate
// @facet-role: related
package cli

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/facet-framework/core/registry"
)

// commitLint judges the commit a Bash command is about to make: it resolves
// which repository the command targets, then asks stagedLint what the index
// introduces. Only the selection is the hook's business; the comparison itself
// is shared with 'facet lint --staged', so the plugin gate, a local pre-commit
// hook and CI run one check and not three similar ones.
func commitLint(root, command string) (facet.Report, error) {
	return commitLintWithSourceView(root, command, "")
}

func commitLintWithSourceView(root, command, selected string) (facet.Report, error) {
	selectedRoot, err := commitSelection(root, command)
	if err != nil {
		return facet.Report{}, err
	}

	if commitGateOffAt(selectedRoot, "HEAD:") {
		return facet.Report{}, nil
	}

	if commitGateOffAt(selectedRoot, ":") {
		return facet.Report{}, fmt.Errorf("this commit sets hooks.commit_gate: off in %s; switching the gate off is a decision for the person running the session, not for the agent. It can be committed with FACET_SKIP_COMMIT_GATE=1 set in the environment Claude Code was started with, or from a terminal", facet.ConfigName)
	}
	return stagedLintWithSourceView(selectedRoot, selected)
}

// commitGateOffAt reports whether a revision of .facetrc.yaml declares
// 'hooks: {commit_gate: off}'. prefix is "HEAD:" for the committed file or ":"
// for the staged one.
func commitGateOffAt(root, prefix string) bool {
	data, err := exec.Command("git", "-C", root, "show", prefix+facet.ConfigName).Output()
	if err != nil {
		return false
	}
	scratch, err := os.MkdirTemp("", "facet-gate-switch-")
	if err != nil {
		return false
	}
	defer os.RemoveAll(scratch)
	if err = os.WriteFile(facet.ConfigPath(scratch), data, 0600); err != nil {
		return false
	}
	cfg, err := facet.LoadConfigUnresolved(scratch)
	return err == nil && cfg.CommitGateOff()
}

// stagedLint compares the index with HEAD and returns the errors the index
// introduces. Neither snapshot includes unstaged repairs: those cannot make a
// defective staged blob safe to commit. root must be a repository's top level.
func stagedLint(root string) (facet.Report, error) { return stagedLintWithSourceView(root, "") }

func stagedLintWithSourceView(root, selected string) (facet.Report, error) {
	if _, err := os.Stat(facet.ConfigPath(root)); err == nil {
		tracked, e := commitGit(root, "ls-files", "--", facet.ConfigName)
		if e != nil || strings.TrimSpace(tracked) == "" {
			return facet.Report{}, fmt.Errorf("private or untracked configuration cannot be compared to HEAD")
		}
	}
	tree, err := commitGit(root, "write-tree")
	if err != nil {
		return facet.Report{}, fmt.Errorf("cannot read commit index: %w", err)
	}
	context := &commitViewContext{root: root, selected: selected, external: map[string]string{}}
	defer context.close()
	head, err := commitGit(root, "rev-parse", "--verify", "HEAD^{commit}")
	var baseline facet.Report
	if err != nil {
		// An unborn branch is the only permitted absent baseline. A damaged HEAD
		// or missing object is a failure, never an empty clean report.
		ref, e := commitGit(root, "symbolic-ref", "-q", "HEAD")
		if e != nil {
			return baseline, fmt.Errorf("cannot resolve HEAD baseline: %w", err)
		}
		// show-ref alone is not enough: for a ref file holding garbage it
		// may report "no such ref", which would read as an unborn branch. A
		// ref that exists in any form, valid or not, means HEAD is damaged.
		if refStored(root, strings.TrimSpace(ref)) {
			return baseline, fmt.Errorf("cannot resolve HEAD baseline: %w", err)
		}
		probe := exec.Command("git", "-C", root, "show-ref", "--verify", "--quiet", strings.TrimSpace(ref))
		if e = probe.Run(); e == nil {
			return baseline, fmt.Errorf("cannot resolve HEAD baseline: %w", err)
		}
		if status, ok := e.(*exec.ExitError); !ok || status.ExitCode() != 1 {
			return baseline, fmt.Errorf("cannot resolve HEAD baseline: cannot establish that the branch is unborn: %w", e)
		}
	} else {
		baseline, err = lintCommitTreeContext(root, strings.TrimSpace(head), context)
		if err != nil {
			return baseline, fmt.Errorf("cannot lint HEAD baseline: %w", err)
		}
	}
	candidate, err := lintCommitTreeContext(root, strings.TrimSpace(tree), context)
	if err != nil {
		return candidate, fmt.Errorf("cannot lint commit index: %w", err)
	}
	counts := map[string]int{}
	for _, f := range baseline.Findings {
		if f.Severity == facet.SeverityError {
			counts[commitFindingKey(f)]++
		}
	}
	// Checked comes from the index, which is what was examined. Findings starts
	// non-nil so --json prints [] as 'facet lint --json' does, not null.
	out := facet.Report{Findings: []facet.Finding{}}
	out.Checked = candidate.Checked
	for _, f := range candidate.Findings {
		if f.Severity != facet.SeverityError {
			continue
		}
		key := commitFindingKey(f)
		if counts[key] > 0 {
			counts[key]--
			continue
		}
		out.Findings = append(out.Findings, f)
	}
	return out, nil
}

// refStored reports whether a ref has an entry in the repository at all, as a
// loose file or a line in packed-refs, without judging whether the entry is
// valid. An unborn branch has neither.
func refStored(root, ref string) bool {
	if loose, err := commitGit(root, "rev-parse", "--git-path", ref); err == nil {
		p := strings.TrimSpace(loose)
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		if _, err := os.Lstat(p); err == nil {
			return true
		}
	}
	if packed, err := commitGit(root, "rev-parse", "--git-path", "packed-refs"); err == nil {
		p := strings.TrimSpace(packed)
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		if data, err := os.ReadFile(p); err == nil {
			for _, l := range strings.Split(string(data), "\n") {
				if strings.HasSuffix(l, " "+ref) {
					return true
				}
			}
		}
	}
	return false
}

func commitFindingKey(f facet.Finding) string {
	return strings.Join([]string{f.Registry, f.Check, f.Path, f.ID, f.Message}, "\x00")
}

func commitGit(root string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func lintCommitTree(root, tree string) (facet.Report, error) {
	context := &commitViewContext{root: root, external: map[string]string{}}
	defer context.close()
	return lintCommitTreeContext(root, tree, context)
}

func lintCommitTreeContext(root, tree string, context *commitViewContext) (facet.Report, error) {
	snapshot, err := os.MkdirTemp("", "facet-commit-")
	if err != nil {
		return facet.Report{}, err
	}
	defer os.RemoveAll(snapshot)
	if err = materializeCommitTree(root, tree, snapshot); err != nil {
		return facet.Report{}, err
	}
	snapshotConfig, err := facet.LoadConfigUnresolved(snapshot)
	if err != nil {
		return facet.Report{}, err
	}
	for _, reg := range snapshotConfig.All() {
		rel, e := filepath.Rel(snapshot, reg.RecordsDir)
		if e == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
			ignored, e := commitGit(root, "ls-files", "--others", "--ignored", "--exclude-standard", "--", rel)
			if e != nil {
				return facet.Report{}, e
			}
			if strings.TrimSpace(ignored) != "" {
				return facet.Report{}, fmt.Errorf("private ignored records cannot be compared to HEAD")
			}
		}

		for _, path := range append([]string{reg.RecordsDir, reg.SchemaDir}, reg.Sources...) {
			if !filepath.IsAbs(path) {
				continue
			}
			rel, e := filepath.Rel(snapshot, path)
			if e != nil || rel == ".." || strings.HasPrefix(rel, "../") {
				return facet.Report{}, fmt.Errorf("external registry paths in revision cannot be compared to HEAD")
			}
		}
	}
	scan := facet.ScanOptionsFromEnv()
	if snapshotConfig.SourceViews == nil && context.selected != "" {
		return facet.Report{}, fmt.Errorf("no source_views configuration is declared")
	}
	if snapshotConfig.SourceViews != nil {
		scan.View, err = context.resolve(snapshot, tree, *snapshotConfig.SourceViews)
		if err != nil {
			return facet.Report{}, err
		}
	}
	report, err := facet.LintAll(snapshot, facet.LintOptions{Scan: scan})
	for i := range report.Findings {
		f := &report.Findings[i]
		for _, scratch := range context.scratch {
			f.Path = strings.ReplaceAll(f.Path, scratch+string(filepath.Separator), "")
			f.Message = strings.ReplaceAll(f.Message, scratch+string(filepath.Separator), "")
		}
		f.Path = strings.ReplaceAll(f.Path, snapshot+string(filepath.Separator), "")
		f.Message = strings.ReplaceAll(f.Message, snapshot+string(filepath.Separator), "")
	}
	return report, err
}

// Read blobs directly: git archive honours export-ignore and checkout-index
// runs smudge filters, both of which change the tree being verified.
func materializeCommitTree(root, tree, dest string) error {
	raw, err := exec.Command("git", "-C", root, "ls-tree", "-r", "-z", tree).Output()
	if err != nil {
		return err
	}
	type blob struct{ mode, oid, path string }
	var blobs []blob
	var request bytes.Buffer
	for _, entry := range bytes.Split(raw, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		parts := bytes.SplitN(entry, []byte{'\t'}, 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid tree entry")
		}
		meta := strings.Fields(string(parts[0]))
		rel := string(parts[1])
		target := filepath.Join(dest, rel)
		if !strings.HasPrefix(target, dest+string(filepath.Separator)) {
			return fmt.Errorf("unsafe tree path")
		}
		if meta[1] == "commit" {
			if err := materializeCommitTree(filepath.Join(root, rel), meta[2], target); err != nil {
				return fmt.Errorf("submodule %s: %w", rel, err)
			}
			continue
		}
		blobs = append(blobs, blob{meta[0], meta[2], target})
		fmt.Fprintln(&request, meta[2])
	}
	cmd := exec.Command("git", "-C", root, "cat-file", "--batch")
	cmd.Stdin = &request
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	reader := bufio.NewReader(bytes.NewReader(out))
	for _, b := range blobs {
		header, e := reader.ReadString('\n')
		if e != nil {
			return e
		}
		h := strings.Fields(header)
		if len(h) != 3 || h[1] != "blob" {
			return fmt.Errorf("cannot read tree blob %s", b.oid)
		}
		size, e := strconv.Atoi(h[2])
		if e != nil {
			return e
		}
		data := make([]byte, size)
		if _, e = io.ReadFull(reader, data); e != nil {
			return e
		}
		if _, e = reader.ReadByte(); e != nil {
			return e
		}
		if e = os.MkdirAll(filepath.Dir(b.path), 0700); e != nil {
			return e
		}
		// Symlinks outside the snapshot would make both reports read mutable files.
		if b.mode == "120000" {
			return fmt.Errorf("cannot verify symlink %s", filepath.Base(b.path))
		}
		mode := os.FileMode(0600)
		if b.mode == "100755" {
			mode = 0700
		}
		if e = os.WriteFile(b.path, data, mode); e != nil {
			return e
		}
	}
	return nil
}

// commitSelection accepts an ordinary staged commit with options whose values
// do not change the candidate tree. Ambiguous selections are explicit failures.
func commitSelection(root, command string) (string, error) {
	words, err := commitWords(command)
	if err != nil {
		return "", err
	}
	fail := func() (string, error) {
		return "", fmt.Errorf("cannot verify commit selection; stage changes separately and run a plain git commit")
	}
	i := 0
	for i < len(words) && strings.Contains(words[i], "=") {
		key := strings.SplitN(words[i], "=", 2)[0]
		if key == "GIT_INDEX_FILE" || key == "GIT_DIR" || key == "GIT_WORK_TREE" || key == "GIT_COMMON_DIR" || key == "GIT_OBJECT_DIRECTORY" || key == "GIT_ALTERNATE_OBJECT_DIRECTORIES" || strings.HasPrefix(key, "FACET_") {
			return fail()
		}
		i++
	}
	if i >= len(words) || words[i] != "git" {
		return fail()
	}
	i++
	for i < len(words) && words[i] != "commit" {
		if words[i] == "-C" && i+1 < len(words) {
			path := words[i+1]
			if !filepath.IsAbs(path) {
				path = filepath.Join(root, path)
			}
			root = path
			i += 2
			continue
		}
		return fail()
	}
	if i >= len(words) {
		return fail()
	}
	i++
	for i < len(words) {
		f := words[i]
		i++
		switch f {
		case "-m", "--message", "-F", "--file", "--author", "--date", "-S", "--gpg-sign", "--cleanup", "--trailer":
			if i >= len(words) {
				return fail()
			}
			i++
		case "--amend", "--no-edit", "--allow-empty", "--allow-empty-message", "--no-verify", "-n", "--signoff", "-s", "--no-gpg-sign", "--quiet", "-q", "--verbose", "-v", "--dry-run":
		default:
			if strings.HasPrefix(f, "--message=") || strings.HasPrefix(f, "--file=") || strings.HasPrefix(f, "--author=") || strings.HasPrefix(f, "--date=") {
				continue
			}
			if strings.HasPrefix(f, "-m") && len(f) > 2 {
				continue
			}
			return fail()
		}
	}
	top := gitToplevel(root)
	if top == "" {
		return "", fmt.Errorf("cannot locate commit repository")
	}
	return top, nil
}

// This lexer deliberately rejects shell expansion and command composition.
// It never evaluates shell input. Quoted messages and paths remain one word.
func commitWords(command string) ([]string, error) { return lexCommitWords(command, false) }

func lexCommitWords(command string, detect bool) ([]string, error) {
	var out []string
	var word strings.Builder
	var quote rune
	started := false
	escaped := false
	flush := func() {
		if started {
			out = append(out, word.String())
			word.Reset()
			started = false
		}
	}
	for _, r := range command {
		if escaped {
			word.WriteRune(r)
			started = true
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			started = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				if !detect && quote == '"' && (r == '$' || r == '`') {
					return nil, fmt.Errorf("cannot verify shell expansion in commit command")
				}
				word.WriteRune(r)
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
			started = true
		case ' ', '\t':
			flush()
		case ';', '&', '|', '\n', '(', ')':
			if detect {
				flush()
				if r == '\n' {
					r = ';'
				}
				out = append(out, string(r))
				continue
			}
			return nil, fmt.Errorf("cannot verify compound or expanded commit command; stage changes separately")
		case '<', '>', '$', '`':
			if detect {
				word.WriteRune(r)
				started = true
				continue
			}
			return nil, fmt.Errorf("cannot verify compound or expanded commit command; stage changes separately")
		default:
			word.WriteRune(r)
			started = true
		}
	}
	if quote != 0 || escaped {
		return nil, fmt.Errorf("cannot parse commit command")
	}
	flush()
	return out, nil
}

// Identify a commit repository before validating its selection. Unsupported
// options must still reach the gate, including from an unadopted cwd.
func commitRootCandidates(base, command string) []string {
	words, err := lexCommitWords(command, true)
	if err != nil {
		return nil
	}
	var roots []string
	// A 'cd <dir>' moves every later command in the line, so the commit that
	// follows it runs in that directory and not in the payload's cwd.
	dir := base
	for i, w := range words {
		if w == "cd" && startsCommand(words, i) && i+1 < len(words) && !shellSeparators[words[i+1]] {
			target := words[i+1]
			if !filepath.IsAbs(target) {
				target = filepath.Join(dir, target)
			}
			dir = target
			continue
		}
		if w != "git" || !startsCommand(words, i) {
			continue
		}
		root := dir
		for j := i + 1; j < len(words); j++ {
			f := words[j]
			if f == "commit" {
				if top := gitToplevel(root); top != "" {
					roots = append(roots, top)
				}
				break
			}
			if gitValueOptions[f] {
				if j+1 >= len(words) {
					break
				}
				j++
				if f == "-C" {
					path := words[j]
					if !filepath.IsAbs(path) {
						path = filepath.Join(root, path)
					}
					root = path
				}
				continue
			}
			if strings.HasPrefix(f, "-") {
				continue
			}
			break
		}
	}
	return roots
}

// Commit adoption is revisioned too: deleting the last working-tree record
// must not disable the guard over the staged orphan anchors left behind.
func commitRevisionAdopted(root string) bool {
	trees := []string{"HEAD"}
	if index, err := commitGit(root, "write-tree"); err == nil {
		trees = append(trees, strings.TrimSpace(index))
	}
	for _, tree := range trees {
		scratch, err := os.MkdirTemp("", "facet-adoption-")
		if err != nil {
			continue
		}
		data, err := exec.Command("git", "-C", root, "show", tree+":"+facet.ConfigName).Output()
		if err == nil {
			if err = os.WriteFile(facet.ConfigPath(scratch), data, 0600); err != nil {
				os.RemoveAll(scratch)
				continue
			}
		}
		cfg, err := facet.LoadConfigUnresolved(scratch)
		if err == nil {
			for _, reg := range cfg.All() {
				rel, e := filepath.Rel(scratch, reg.RecordsDir)
				if e != nil || rel == ".." || strings.HasPrefix(rel, "../") {
					continue
				}
				if _, e := commitGit(root, "cat-file", "-e", tree+":"+filepath.ToSlash(filepath.Join(rel, "features"))); e == nil {
					os.RemoveAll(scratch)
					return true
				}
			}
		}
		os.RemoveAll(scratch)
	}
	return false
}
