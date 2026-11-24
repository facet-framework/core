package spec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/facet-framework/core/pkg/filelock"
)

const cacheName = ".facet/spec-cache"

type Change struct {
	Source  string
	Address string
	Kind    string
	Binding BindingStrength
}

// @facet: registry/integrity/spec-pin
// @facet-role: primary

// Update reads all selected sources before publishing a new lock.
func Update(ctx context.Context, root string, sources []Source, overrides map[string]string, only []string) ([]Change, error) {
	requested := make(map[string]bool, len(only))
	for _, address := range only {
		if !addressPattern.MatchString(address) {
			return nil, fmt.Errorf("invalid scenario address %q; expected <spec-id>#<case>", address)
		}
		requested[address] = false
	}
	cache := filepath.Join(root, cacheName)
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return nil, err
	}
	guard, err := os.OpenFile(filepath.Join(cache, ".update.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	defer guard.Close()
	if err := filelock.Acquire(ctx, guard); err != nil {
		return nil, err
	}
	defer filelock.Release(guard)

	lock, err := ReadLock(root)
	if os.IsNotExist(err) {
		lock = make(Lock)
	} else if err != nil {
		return nil, fmt.Errorf("%s: %w", LockName, err)
	}
	var changes []Change
	for _, source := range sources {
		current, err := readSource(ctx, root, source, overrides[source.ID])
		if err != nil {
			return nil, fmt.Errorf("spec source %q: %w", source.ID, err)
		}
		before := lock[source.ID]
		after := current
		if len(requested) > 0 {
			after, err = pinSelected(before, current, requested)
			if err != nil {
				return nil, fmt.Errorf("spec source %q: %w", source.ID, err)
			}
			if after.Repo == "" {
				continue
			}
		}
		changes = append(changes, snapshotChanges(source.ID, before, after)...)
		lock[source.ID] = after
	}
	for _, address := range sortedKeys(requested) {
		if !requested[address] {
			return nil, fmt.Errorf("scenario %q is absent from the selected upstream sources", address)
		}
	}
	data, err := encodeLock(lock)
	if err != nil {
		return nil, err
	}
	if _, err := decodeLock(data); err != nil {
		return nil, fmt.Errorf("%s: %w", LockName, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := writeLock(filepath.Join(root, LockName), data); err != nil {
		return nil, err
	}
	return changes, nil
}

func pinSelected(before, current LockedSource, requested map[string]bool) (LockedSource, error) {
	var selected []string
	for _, address := range sortedKeys(requested) {
		if _, ok := current.Scenarios[address]; ok {
			requested[address] = true
			selected = append(selected, address)
		}
	}
	if len(selected) == 0 {
		return before, nil
	}
	if before.Repo == "" {
		before = LockedSource{
			Repo: current.Repo, Follow: current.Follow, Format: current.Format,
			Files: make(map[string]LockedFile), Scenarios: make(map[string]LockedScenario),
		}
	}
	if before.Repo != current.Repo || before.Format != current.Format || before.Follow != current.Follow {
		return LockedSource{}, fmt.Errorf("repository, follow branch or format changed; a full update is required")
	}

	for _, address := range selected {
		file, _, _ := strings.Cut(address, "#")
		metadata := current.Files[file]
		if pinned, exists := before.Files[file]; !exists {
			before.Files[file] = metadata
		} else if pinned.Language != metadata.Language {
			return LockedSource{}, fmt.Errorf("language changed for %q; a full update is required", file)
		}
	}
	// Clone the scenario map so the previous entries remain available for reporting.
	scenarios := make(map[string]LockedScenario, len(before.Scenarios)+len(selected))
	for address, entry := range before.Scenarios {
		scenarios[address] = entry
	}
	for _, address := range selected {
		scenarios[address] = current.Scenarios[address]
	}
	before.Scenarios = scenarios
	return before, nil
}

func snapshotChanges(source string, before, after LockedSource) []Change {
	var changes []Change
	for _, address := range sortedKeys(after.Scenarios) {
		entry := after.Scenarios[address]
		old, exists := before.Scenarios[address]
		switch {
		case !exists:
			changes = append(changes, Change{source, address, "added", entry.Strength()})
		case old.Commit != entry.Commit || old.Steps != entry.Steps || old.Context != entry.Context || old.Strength() != entry.Strength():
			changes = append(changes, Change{source, address, "changed", entry.Strength()})
		}
	}
	for _, address := range sortedKeys(before.Scenarios) {
		if _, exists := after.Scenarios[address]; !exists {
			changes = append(changes, Change{source, address, "removed", before.Scenarios[address].Strength()})
		}
	}
	return changes
}

func readSource(ctx context.Context, root string, source Source, checkout string) (LockedSource, error) {
	ref := "refs/heads/" + source.Follow
	if checkout == "" {
		checkout = sourceCache(root, source)
		commit, err := fetchFollow(ctx, checkout, source, false)
		if err != nil {
			return LockedSource{}, err
		}
		return readSnapshot(ctx, checkout, true, source, commit, false)
	}
	if !filepath.IsAbs(checkout) {
		checkout = filepath.Join(root, checkout)
	}
	top, err := gitOutput(ctx, checkout, false, "rev-parse", "--show-toplevel")
	if err != nil {
		return LockedSource{}, fmt.Errorf("local override is not a Git checkout: %w", err)
	}
	info, err := os.Stat(checkout)
	topInfo, topErr := os.Stat(strings.TrimSuffix(string(top), "\n"))
	if err != nil || topErr != nil || !os.SameFile(info, topInfo) {
		return LockedSource{}, fmt.Errorf("local override must name the root of a Git checkout")
	}
	output, err := gitOutput(ctx, checkout, false, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return LockedSource{}, fmt.Errorf("cannot resolve branch %q: %w", source.Follow, err)
	}
	commit := strings.TrimSpace(string(output))
	return readSnapshot(ctx, checkout, false, source, commit, false)
}

func sourceCache(root string, source Source) string {
	return filepath.Join(root, cacheName, source.ID)
}

func cachedFollowRef(source Source) string {
	identity := sha256.Sum256([]byte(source.Repo + "\x00" + source.Follow))
	return fmt.Sprintf("refs/facet/fetched/%x", identity)
}

func fetchFollow(ctx context.Context, checkout string, source Source, noninteractive bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		return "", err
	}
	guard, err := os.OpenFile(filepath.Join(checkout, ".fetch.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", err
	}
	defer guard.Close()
	if err := filelock.Acquire(ctx, guard); err != nil {
		return "", err
	}
	defer filelock.Release(guard)

	if _, err := os.Stat(filepath.Join(checkout, "HEAD")); os.IsNotExist(err) {
		if _, err := gitOutput(ctx, checkout, false, "init", "--bare", "."); err != nil {
			return "", fmt.Errorf("initialise Git cache: %w", err)
		}
	}
	ref := cachedFollowRef(source)
	if _, err := runGit(ctx, checkout, true, noninteractive, "fetch", "--no-tags", "--no-recurse-submodules", "--",
		source.gitURL(), "+refs/heads/"+source.Follow+":"+ref); err != nil {
		return "", fmt.Errorf("cannot fetch branch %q: %w", source.Follow, err)
	}
	commit, err := resolveCachedFollow(ctx, checkout, ref)
	if err != nil {
		return "", fmt.Errorf("cannot resolve fetched branch %q: %w", source.Follow, err)
	}
	return commit, nil
}

func resolveCachedFollow(ctx context.Context, checkout, ref string) (string, error) {
	output, err := gitOutput(ctx, checkout, true, "rev-parse", "--verify", ref+"^{commit}")
	return strings.TrimSpace(string(output)), err
}

func readSnapshot(ctx context.Context, checkout string, bare bool, source Source, commit string, allowMissingPath bool) (LockedSource, error) {
	result := LockedSource{
		Repo: source.Repo, Follow: source.Follow, Format: source.Format, Commit: commit,
		Files: make(map[string]LockedFile), Scenarios: make(map[string]LockedScenario),
	}
	var findings []error
	err := visitSpecDocuments(ctx, checkout, bare, source, commit, allowMissingPath, func(name string, doc Document, issues []Finding) {
		for _, finding := range issues {
			findings = append(findings, finding)
		}
		if doc.Skipped || len(issues) > 0 {
			return
		}
		if !filePattern.MatchString(doc.ID) {
			findings = append(findings, Finding{name, 1, fmt.Sprintf("invalid spec ID %q", doc.ID), source.Format.Strength()})
			return
		}
		if _, exists := result.Files[doc.ID]; exists {
			findings = append(findings, Finding{name, 1, fmt.Sprintf("duplicate spec ID %q", doc.ID), source.Format.Strength()})
			return
		}
		result.Files[doc.ID] = LockedFile{Status: doc.Status, Language: doc.Language}
		for _, unit := range doc.Units {
			// Exact is the default and is never written, so exact-only locks carry no binding field.
			binding := unit.Strength()
			if binding == Exact {
				binding = ""
			}
			result.Scenarios[unit.Address] = LockedScenario{
				Commit: commit, Binding: binding, Steps: unit.Digest,
				Context: unit.ContextDigest, Sequence: unit.Sequence, Text: unit.Text,
			}
		}
	})
	return result, errors.Join(err, errors.Join(findings...))
}

func initSpecCache(ctx context.Context, checkout string) error {
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(checkout, "HEAD")); os.IsNotExist(err) {
		if _, err := gitOutput(ctx, checkout, false, "init", "--bare", "."); err != nil {
			return fmt.Errorf("initialise Git cache: %w", err)
		}
	}
	return nil
}

func visitSpecDocuments(ctx context.Context, checkout string, bare bool, source Source, commit string, allowMissingPath bool, visit func(string, Document, []Finding)) error {
	adapter, err := AdapterFor(source.Format)
	if err != nil {
		return err
	}
	dir := source.directory()
	tree := commit
	if dir != "" {
		tree += ":" + dir
		kind, err := gitOutput(ctx, checkout, bare, "cat-file", "-t", tree)
		if err != nil && allowMissingPath && ctx.Err() == nil {
			entry, listErr := gitOutput(ctx, checkout, bare, "ls-tree", "-z", commit, "--", ":(literal)"+dir)
			if listErr == nil && len(entry) == 0 {
				return nil
			}
		}
		if err != nil || string(kind) != "tree\n" {
			return fmt.Errorf("path %q is not a directory at %s", dir, commit)
		}
	}
	entries, err := gitOutput(ctx, checkout, bare, "ls-tree", "-rz", tree)
	if err != nil {
		return fmt.Errorf("list specification files: %w", err)
	}
	for entry := range strings.SplitSeq(string(entries), "\x00") {
		mode, name, ok := strings.Cut(entry, "\t")
		if !ok || !strings.HasPrefix(mode, "100") || !adapter.Matches(name) ||
			(source.Format == OpenSpec && InOpenSpecChanges(path.Join(dir, name))) {
			continue
		}
		blob, err := gitOutput(ctx, checkout, bare, "show", commit+":"+path.Join(dir, name))
		if err != nil {
			return fmt.Errorf("read spec file %q: %w", name, err)
		}
		doc, issues := adapter.Read(".", filepath.FromSlash(name), blob)
		visit(name, doc, issues)
	}
	return nil
}

func gitOutput(ctx context.Context, dir string, bare bool, args ...string) ([]byte, error) {
	return runGit(ctx, dir, bare, false, args...)
}

func runGit(ctx context.Context, dir string, bare, noninteractive bool, args ...string) ([]byte, error) {
	options := []string{"-C", dir, "-c", "protocol.ext.allow=never", "--no-replace-objects"}
	if bare {
		options = append(options, "--git-dir=.")
	}
	cmd := exec.CommandContext(ctx, "git", append(options, args...)...)
	if noninteractive {
		cancelGitProcessTree(cmd)
	}
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Env = slices.DeleteFunc(os.Environ(), func(entry string) bool {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE",
			"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE",
			"GIT_QUARANTINE_PATH", "GIT_PREFIX", "GIT_INTERNAL_SUPER_PREFIX", "GIT_SHALLOW_FILE":
			return true
		default:
			return false
		}
	})
	if bare {
		cmd.Env = append(cmd.Env, "GIT_NO_LAZY_FETCH=1")
	}
	if noninteractive {
		cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0")
		if os.Getenv("GIT_SSH_COMMAND") == "" && os.Getenv("GIT_SSH") == "" {
			// ssh asks for passwords and host keys on its own terminal, outside
			// Git's prompt setting; batch mode makes it fail instead.
			cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
		}
	}
	// Git diagnostics can contain credential-bearing URLs from Git configuration.
	// Keep the operation and exit status, and discard the subprocess stderr.
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("git: %v", err)
	}
	return output, nil
}

func writeLock(path string, data []byte) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".spec-lock-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	_, writeErr := temp.Write(data)
	chmodErr := temp.Chmod(0o644)
	if err := errors.Join(writeErr, chmodErr, temp.Close()); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
