// @facet: registry/layout/glossary-tools
// @facet-role: related
package facet

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrDefaultBranch is the refusal that the target is never the branch the
// project merges into. A proposal a human merges is the one rule that makes
// everything else here bearable.
var ErrDefaultBranch = errors.New("the default branch is never a write target")

// ErrNoRepository is a write asked of a directory git does not know about.
var ErrNoRepository = errors.New("this is not a git repository")

// ErrNothingToCommit is a commit that named nothing, or whose named paths hold
// nothing to record.
var ErrNothingToCommit = errors.New("a commit must name what it commits")

// ErrBranchName is a branch or remote name outside BranchNamePattern.
var ErrBranchName = errors.New("name is outside the branch name grammar")

// BranchNamePattern is what may be a branch or remote name.
var BranchNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,100}$`)

// defaultBranchNames are the names never written to.
var defaultBranchNames = []string{"main", "master", "trunk", "develop", "HEAD"}

// CommitResult is what a commit produced.
type CommitResult struct {
	SHA   string   `json:"sha"`
	Files []string `json:"files"`
	// Skipped names the paths git had nothing to record about — the source half
	// of a rename of a file this branch created and never committed, most
	// often. Reported rather than dropped quietly, because "committed" and
	// "committed all but one of the things you named" are different answers.
	Skipped []string `json:"skipped,omitempty"`
}

// BranchSession is one write session: a branch and a worktree to write it in.
type BranchSession struct {
	// Dir is the worktree root.
	Dir string
	// Branch is the branch this session writes on.
	Branch string
	// Root is the repository the worktree belongs to.
	Root string
}

// OpenBranchSession creates a branch and a worktree to write it in, for the
// repository whose working tree contains root.
func OpenBranchSession(root, branch string) (*BranchSession, error) {
	branch = strings.TrimSpace(branch)
	if !BranchNamePattern.MatchString(branch) {
		return nil, fmt.Errorf("%w: %q is not a branch name", ErrBranchName, branch)
	}
	// '..' would be refused by the grammar above as a name, but not inside one;
	// a branch called 'glossary/../main' is the default branch wearing a costume.
	if strings.Contains(branch, "..") {
		return nil, fmt.Errorf("%w: %q", ErrBranchName, branch)
	}
	if IsDefaultBranch(root, branch) {
		return nil, fmt.Errorf("%w: %q", ErrDefaultBranch, branch)
	}
	if !isGitRepository(root) {
		return nil, ErrNoRepository
	}

	// Inside .git/ so that it is never repository content: a scan walks the
	// whole tree, and a second checkout of the repository inside the repository
	// would be read as a second copy of every file in it.
	gitDir, err := GitCommonDir(root)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(gitDir, "facet-branches", filepath.FromSlash(branch))
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, err
	}

	// A branch that already exists is checked out rather than recreated, so a
	// session resumed later continues where it left off instead of failing on
	// the second call.
	args := []string{"worktree", "add"}
	if branchExists(root, branch) {
		args = append(args, dir, branch)
	} else {
		args = append(args, "-b", branch, dir)
	}
	if out, err := runGitIn(root, args...); err != nil {
		return nil, fmt.Errorf("could not create the worktree: %v: %s", err, out)
	}
	return &BranchSession{Dir: dir, Branch: branch, Root: root}, nil
}

// Push sends the session's branch to the remote.
func (s *BranchSession) Push(remote string) error {
	if remote == "" {
		remote = "origin"
	}
	if !BranchNamePattern.MatchString(remote) {
		return fmt.Errorf("%w: %q is not a remote name", ErrBranchName, remote)
	}
	if out, err := runGitIn(s.Dir, "push", "--set-upstream", remote, s.Branch); err != nil {
		// Reported rather than rolled back. The commit is the work; undoing it
		// because the network refused would throw away what somebody wrote, and
		// the two commands to finish by hand are worth more than a clean slate.
		return fmt.Errorf("the push was refused: %v: %s", err, out)
	}
	return nil
}

// Close removes the worktree and leaves the branch.
func (s *BranchSession) Close() error {
	if s == nil || s.Dir == "" {
		return nil
	}
	if out, err := runGitIn(s.Root, "worktree", "remove", "--force", s.Dir); err != nil {
		return fmt.Errorf("could not remove the worktree at %s: %v: %s", s.Dir, err, out)
	}
	return nil
}

// IsDefaultBranch reports whether a name is one this host refuses to write to.
func IsDefaultBranch(root, branch string) bool {
	for _, name := range defaultBranchNames {
		if strings.EqualFold(branch, name) {
			return true
		}
	}
	if out, err := runGitIn(root, "symbolic-ref", "--short", "HEAD"); err == nil {
		if strings.EqualFold(strings.TrimSpace(out), branch) {
			return true
		}
	}
	if out, err := runGitIn(root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		head := strings.TrimSpace(out)
		if i := strings.LastIndex(head, "/"); i >= 0 {
			head = head[i+1:]
		}
		if head != "" && strings.EqualFold(head, branch) {
			return true
		}
	}
	return false
}

func branchExists(root, branch string) bool {
	_, err := runGitIn(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

func isGitRepository(root string) bool {
	out, err := runGitIn(root, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// runGitIn runs one git command in a directory.
func runGitIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
