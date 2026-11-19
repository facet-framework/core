// Package facet implements the FACET registry: loading records, resolving the
// source anchors that connect them to code, and the integrity checks that keep
// the two from drifting apart.
//
// @facet: registry/layout/multi-registry
// @facet-role: related
package facet

import (
	"github.com/facet-framework/core/internal/sourceview"
	"os"
	"path/filepath"
	"strings"
)

// Categories of record the registry holds, in the order checks report them.
var Categories = []string{"features", "policies", "assumptions", "data-entities"}

// SchemaFor maps a record category to the schema file that validates it.
func SchemaFor(category string) string {
	switch category {
	case "features":
		return "facet.schema.json"
	case "policies":
		return "policy.schema.json"
	case "assumptions":
		return "assumption.schema.json"
	case "data-entities":
		return "data-entity.schema.json"
	}
	return ""
}

// Registry describes where one project keeps its FACET data.
type Registry struct {
	// categorySnapshot is private to one synchronous lint invocation. Ordinary
	// registry readers continue to observe the filesystem on every call.
	categorySnapshot map[string]categoryLoad

	Name       string                   // declared name; empty for a project that declares none
	Root       string                   // repository root
	RecordsDir string                   // absolute path to the records directory
	SchemaDir  string                   // absolute path to the JSON schemas
	Sources    []string                 // legacy path filters; with SourceView, scoped to selected repositories
	SourceView *sourceview.ResolvedView // frozen coherent source selection; Root still owns the records

	// ConventionsPath is the registry's effective conventions document,
	// absolute, or empty when the project has none. ConventionsSource says
	// which step of the precedence chose it (see resolveConventions).
	ConventionsPath   string
	ConventionsSource string
}

// Open resolves a project's default registry: the only one in a project that
// declares none, and the first declared one otherwise.
func Open(root string) Registry {
	cfg, err := LoadConfig(root)
	if err != nil {
		if sourceview.IsError(err) {
			if cfg == nil {
				cfg = &Config{root: root}
			}
			if cfg.sourceView == nil {
				cfg.sourceView = sourceview.Failed(cfg.sourceViewName(""), err)
			}
		} else {
			cfg = &Config{root: root}
		}
	}
	return cfg.All()[0]
}

// Label names the registry for a message, or returns an empty string when the
// project has only the implicit one and a name would be noise.
func (r Registry) Label() string { return r.Name }

// ScanOptions narrows an anchor scan to this registry's declared sources.
func (r Registry) ScanOptions() ScanOptions { return ScanOptions{Dirs: r.Sources, View: r.SourceView} }

// ValidateSourceView reports resolution failures even for legacy Open/OpenSet
// callers whose return signature cannot carry a configuration error.
func (r Registry) ValidateSourceView() error { return r.SourceView.Validate() }

// Claims reports whether a root-relative path falls inside this registry's
// sources. A registry declaring no sources claims the whole repository.
func (r Registry) Claims(rel string) bool {
	if len(r.Sources) == 0 {
		return true
	}
	rel = filepath.ToSlash(rel)
	for _, dir := range r.Sources {
		dir = strings.Trim(filepath.ToSlash(dir), "/")
		if dir == "" || dir == "." {
			return true
		}
		if rel == dir || strings.HasPrefix(rel, dir+"/") {
			return true
		}
	}
	return false
}

// Adopted reports whether the project uses FACET at all. Every hook checks this
// first so the tooling stays inert in unrelated repositories.
func (r Registry) Adopted() bool {
	return isDir(filepath.Join(r.RecordsDir, "features"))
}

// CategoryDir returns the directory holding records of one category.
func (r Registry) CategoryDir(category string) string {
	return filepath.Join(r.RecordsDir, category)
}

// Rel renders a path relative to the repository root for display.
func (r Registry) Rel(path string) string {
	if rel, err := filepath.Rel(r.Root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

// PathForID returns where a feature record with the given id must live.
// The path is the id, so a mismatch between the two is itself a defect.
func (r Registry) PathForID(id string) string {
	return filepath.Join(r.CategoryDir("features"), filepath.FromSlash(id)+".yaml")
}

// IDForPath is the inverse of PathForID.
func (r Registry) IDForPath(path string) string {
	rel, err := filepath.Rel(r.CategoryDir("features"), path)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(filepath.ToSlash(rel), ".yaml")
}

func absUnder(root, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, p)
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
