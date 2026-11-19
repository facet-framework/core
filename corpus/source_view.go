// @facet: registry/layout/source-views
// @facet-role: related
package corpus

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/facet-framework/core/registry"
	"os"
	"os/exec"
	"sort"
	"strings"
)

type SourceViewRepository struct {
	Repository  string `json:"repository"`
	Revision    string `json:"revision"`
	ContentHash string `json:"contentHash"`
	Dirty       bool   `json:"dirty"`
}
type SourceViewSnapshot struct {
	Name               string                 `json:"name"`
	RegistryRepository string                 `json:"registryRepository,omitempty"`
	RegistryRevision   string                 `json:"registryRevision,omitempty"`
	RecordContentHash  string                 `json:"recordContentHash"`
	Sources            []SourceViewRepository `json:"sources"`
}

func sourceViewSnapshot(reg facet.Registry, records map[string]string) *SourceViewSnapshot {
	if reg.SourceView == nil {
		return nil
	}
	out := &SourceViewSnapshot{Name: reg.SourceView.Name, RegistryRepository: reg.SourceView.RegistryRepository, Sources: []SourceViewRepository{}}
	if raw, err := exec.Command("git", "-C", reg.Root, "rev-parse", "HEAD").Output(); err == nil {
		out.RegistryRevision = strings.TrimSpace(string(raw))
	}
	keys := make([]string, 0, len(records))
	for key := range records {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, key := range keys {
		h.Write([]byte(key))
		h.Write([]byte{0})
		h.Write([]byte(records[key]))
		h.Write([]byte{0})
	}
	out.RecordContentHash = hex.EncodeToString(h.Sum(nil))
	for _, s := range reg.SourceView.Ordered() {
		out.Sources = append(out.Sources, SourceViewRepository{Repository: s.Repository, Revision: s.Revision, ContentHash: s.ContentHash, Dirty: s.Dirty})
	}
	return out
}
func enumerateSources(reg facet.Registry, found func(int, string)) ([]string, map[string]facet.SourceFile, error) {
	if reg.SourceView == nil {
		paths, err := sourceFiles(reg, found)
		return paths, nil, err
	}
	files, err := facet.SelectedSourceFiles(reg)
	if err != nil {
		return nil, nil, err
	}
	paths := make([]string, 0, len(files))
	identities := map[string]facet.SourceFile{}
	for _, file := range files {
		paths = append(paths, file.Path)
		identities[file.Path] = file
		if found != nil {
			found(len(paths), file.Path)
		}
	}
	sort.Strings(paths)
	return paths, identities, nil
}
func corpusSourcePath(reg facet.Registry, path string, identities map[string]facet.SourceFile) string {
	if source, ok := identities[path]; ok {
		return source.Reference
	}
	return reg.Rel(path)
}

// DescribeSourceView exposes the same portable identity as corpus.Build without
// scanning implementation anchors. Unknown identity stays unknown.
func DescribeSourceView(reg facet.Registry) (*SourceViewSnapshot, error) {
	if err := reg.ValidateSourceView(); err != nil {
		return nil, err
	}
	if reg.SourceView == nil {
		return nil, nil
	}
	paths, err := recordPaths(reg)
	if err != nil {
		return nil, err
	}
	records := map[string]string{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		records[reg.Rel(path)] = string(raw)
	}
	result := sourceViewSnapshot(reg, records)
	if err := reg.ValidateSourceView(); err != nil {
		return nil, err
	}
	return result, nil
}
