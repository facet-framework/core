package facet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// uninitializedSubmodules uses Git's index rather than interpreting config
// text: only real gitlinks qualify, and NUL delimiters preserve quoted paths.
// Git failures leave the normal missing-file errors intact. A present .git
// marker, even a damaged one, is never treated as an unavailable checkout.
//
// @facet: registry/integrity/proof-references
// @facet-role: related
func uninitializedSubmodules(root string) []string {
	modules, _ := readUninitializedSubmodules(root)
	return modules
}

// readUninitializedSubmodules preserves real Git failures for callers whose
// next operation can write. Verification retains its existing best-effort wrapper.
func readUninitializedSubmodules(root string) ([]string, error) {
	topCmd := exec.Command("git", "-C", root, "rev-parse", "--show-toplevel")
	top, err := topCmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("could not read Git working tree: %w: %s", err, strings.TrimSuffix(string(top), "\n"))
	}
	repo := strings.TrimSuffix(string(top), "\n")
	cmd := exec.Command("git", "-C", repo, "ls-files", "--stage", "-z")
	index, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("could not read Git index: %w: %s", err, strings.TrimSuffix(string(index), "\n"))
	}
	var modules []string
	for _, entry := range strings.Split(string(index), "\x00") {
		metadata, name, ok := strings.Cut(entry, "\t")
		if !ok || !strings.HasPrefix(metadata, "160000 ") || !strings.HasSuffix(metadata, " 0") {
			continue
		}
		module := filepath.Join(repo, filepath.FromSlash(name))
		if _, err := os.Lstat(filepath.Join(module, ".git")); os.IsNotExist(err) {
			modules = append(modules, module)
		}
	}
	return modules, nil
}

func containingSubmodule(root, target string, modules []string) string {
	// Git resolves a symlinked working-tree root. Compare targets against that
	// same directory identity; the target itself may legitimately be absent.
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return ""
	}
	absolute, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(target)))
	if err != nil {
		return ""
	}
	for _, module := range modules {
		rel, err := filepath.Rel(module, absolute)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			name, err := filepath.Rel(root, module)
			if err == nil {
				return filepath.ToSlash(name)
			}
		}
	}
	return ""
}

// shellQuote makes the suggested command safe for paths containing quotes.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
