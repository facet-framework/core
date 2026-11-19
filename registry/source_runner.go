// @facet: registry/layout/source-views
// @facet-role: related
package facet

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// selectedRunnerRoot uses the same immutable source selection as scanning.
// Discovery commands may execute code too, so validation precedes both phases.
func selectedRunnerRoot(reg Registry, runner Runner) (string, string, error) {
	if reg.SourceView == nil {
		if runner.Repository != "" {
			return "", "", fmt.Errorf("runner repository %q requires source_views", runner.Repository)
		}
		return reg.Root, "", nil
	}
	src, err := reg.SourceView.Source(runner.Repository)
	if err != nil {
		return "", "", err
	}
	if err = src.ValidateWorkingTree(); err != nil {
		return "", "", err
	}
	return src.Root, src.Repository, nil
}
func selectedTestReference(reg Registry, path string) (string, error) {
	if reg.SourceView == nil {
		return normalizedTestPath(reg.Root, reg.Root, path)
	}
	src, rel, err := reg.SourceView.ResolveReference(path)
	if err != nil {
		return "", err
	}
	rel, err = normalizedTestPath(src.Root, src.Root, rel)
	if err != nil {
		return "", err
	}
	return reg.SourceView.Qualify(src.Repository, rel)
}
func readSelectedTest(reg Registry, path string) ([]byte, error) {
	if reg.SourceView == nil {
		return os.ReadFile(filepath.Join(reg.Root, filepath.FromSlash(path)))
	}
	src, rel, err := reg.SourceView.ResolveReference(path)
	if err != nil {
		return nil, err
	}
	return src.Read(rel)
}

// portableSourcePath projects internal workspace paths only at the reporting boundary.
func portableSourcePath(reg Registry, path string) string {
	if reg.SourceView == nil {
		return path
	}
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(reg.Root, filepath.FromSlash(path))
	}
	for _, src := range reg.SourceView.Ordered() {
		rel, e := filepath.Rel(src.Root, full)
		if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			qualified, e := reg.SourceView.Qualify(src.Repository, filepath.ToSlash(rel))
			if e == nil {
				return qualified
			}
		}
	}
	return path
}

func registryRecordHashes(reg Registry) (map[string]string, error) {
	hashes := map[string]string{}
	for _, category := range Categories {
		records, e := reg.LoadCategory(category)
		if e != nil {
			return nil, e
		}
		for _, record := range records {
			raw, e := os.ReadFile(record.Path)
			if e != nil {
				return nil, e
			}
			hashes[record.ID] = fmt.Sprintf("%x", sha256.Sum256(raw))
		}
	}
	return hashes, nil
}
