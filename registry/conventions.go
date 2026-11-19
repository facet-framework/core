// @facet: registry/layout/project-conventions
// @facet-role: primary
package facet

import (
	"fmt"
	"path/filepath"
	"strings"
)

// DefaultConventionsName is the project conventions document a repository may
// keep at its root without declaring anything.
const DefaultConventionsName = "FACET-CONVENTIONS.md"

// Where a registry's conventions document came from. Printed, so an agent and
// a person can tell an override from the shared document without reading the
// configuration themselves.
const (
	ConventionsFromRegistry = "registry" // the registry's own 'conventions' key
	ConventionsFromProject  = "project"  // the top-level 'conventions' key
	ConventionsFromDefault  = "default"  // FACET-CONVENTIONS.md at the root
)

// resolveConventions picks one registry's effective conventions document.
func resolveConventions(root, registryDeclared, projectDeclared string) (path, source string) {
	switch {
	case registryDeclared != "":
		return absUnder(root, registryDeclared), ConventionsFromRegistry
	case projectDeclared != "":
		return absUnder(root, projectDeclared), ConventionsFromProject
	case isFile(filepath.Join(root, DefaultConventionsName)):
		return filepath.Join(root, DefaultConventionsName), ConventionsFromDefault
	}
	return "", ""
}

// validConventionsPath reports whether a declared path stays inside the
// repository. A document outside it would make the effective conventions
// depend on the machine reading them, which is the failure .facetrc.yaml was
// introduced to remove.
func validConventionsPath(p string) bool {
	if p == "" || filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(p))
	return clean != ".." && !strings.HasPrefix(clean, "../")
}

// ConventionsLabel says, in words, where a registry's document came from.
func ConventionsLabel(source string) string {
	switch source {
	case ConventionsFromRegistry:
		return "declared for this registry in " + ConfigName
	case ConventionsFromProject:
		return "declared in " + ConfigName
	case ConventionsFromDefault:
		return "found at the repository root"
	}
	return ""
}

// lintConventions reports a declared conventions document that does not exist.
func lintConventions(rep *Report, cfg *Config, registries []Registry) {
	seen := map[string]bool{}
	for _, reg := range registries {
		if reg.ConventionsSource == ConventionsFromDefault || reg.ConventionsPath == "" {
			continue
		}
		if seen[reg.ConventionsPath] {
			continue
		}
		seen[reg.ConventionsPath] = true
		if isFile(reg.ConventionsPath) {
			continue
		}
		f := Finding{
			Severity: SeverityError,
			Check:    "conventions",
			Path:     reg.Rel(cfg.Path),
			Message: fmt.Sprintf("conventions document %q is declared but does not exist",
				reg.Rel(reg.ConventionsPath)),
			Hint: "create the document, correct the path, or remove the 'conventions' key",
		}
		if reg.ConventionsSource == ConventionsFromRegistry {
			f.Registry = reg.Name
		}
		rep.add(f)
	}
}
