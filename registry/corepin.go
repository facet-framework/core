// @facet: delivery/distribution/core-pin
// @facet-role: primary
package facet

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

// CorePin is a validated project pin on the Core version, read from
// requires.core in .facetrc.yaml. It is deliberately not part of Config:
// LoadConfig never reads it, so a broken pin can never break configuration
// loading — and with it every hook, which would fall back to the default
// layout and go silent in a project whose records live elsewhere. Only the
// pin check reads and refuses it.
type CorePin struct {
	// File is the configuration the pin was read from.
	File string
	// Core is the required version without a leading "v".
	Core string
}

// Satisfied reports whether a running product version is the pinned one. There
// are no ranges: 0.1.0-dev.… is not 0.1.0. Build metadata ("+…") is compared
// only when the pin names it, so a pin of 0.1.0 accepts 0.1.0+g<commit>.
func (p CorePin) Satisfied(running string) bool {
	if strings.Contains(p.Core, "+") {
		return running == p.Core
	}
	version, _, _ := strings.Cut(running, "+")
	return version == p.Core
}

// semverPattern is the SemVer 2.0.0 grammar from semver.org, without a prefix.
var semverPattern = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`)

// ReadCorePin reads only the 'requires:' section of a project's configuration.
func ReadCorePin(root string) (*CorePin, error) {
	path := ConfigPath(root)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	return corePinFromYAML(path, data)
}

// corePinFromYAML validates the 'requires:' section of raw configuration. It
// reads the syntax tree rather than decoded values, so an error can show what
// the author wrote: 'core: 1.0' decodes to the number 1, but was written 1.0.
func corePinFromYAML(path string, data []byte) (*CorePin, error) {
	file, err := parser.ParseBytes(data, 0)
	if err != nil || len(file.Docs) == 0 || file.Docs[0] == nil {
		return nil, nil // The full loader reports a YAML error.
	}
	top, ok := mappingEntries(file.Docs[0].Body)
	if !ok {
		return nil, nil
	}
	requires := entry(top, "requires")
	if requires == nil {
		return nil, nil
	}
	fields, ok := mappingEntries(requires.Value)
	if !ok {
		return nil, fmt.Errorf("%s: requires must be a mapping such as 'requires: {core: \"0.1.0\"}', not %s", path, written(requires.Value))
	}
	for _, field := range fields {
		if key := keyName(field); key != "core" {
			return nil, fmt.Errorf("%s: requires.%s is not a known key; only requires.core is read", path, key)
		}
	}
	core := entry(fields, "core")
	if core == nil {
		return nil, nil
	}
	value, ok := core.Value.(*ast.StringNode)
	if !ok {
		return nil, fmt.Errorf("%s: requires.core must be one version written as a string such as \"0.1.0\", not %s", path, written(core.Value))
	}
	normalized := strings.TrimPrefix(value.Value, "v")
	if !semverPattern.MatchString(normalized) {
		return nil, fmt.Errorf("%s: requires.core %q is not a SemVer version; pin one exact Core version such as \"0.1.0\" (no ranges or prefixes)", path, value.Value)
	}
	return &CorePin{File: path, Core: normalized}, nil
}

// mappingEntries returns the entries of a YAML mapping, false for any other node.
func mappingEntries(node ast.Node) ([]*ast.MappingValueNode, bool) {
	switch n := node.(type) {
	case *ast.MappingNode:
		return n.Values, true
	case *ast.MappingValueNode:
		return []*ast.MappingValueNode{n}, true
	}
	return nil, false
}

func entry(entries []*ast.MappingValueNode, key string) *ast.MappingValueNode {
	for _, e := range entries {
		if keyName(e) == key {
			return e
		}
	}
	return nil
}

func keyName(e *ast.MappingValueNode) string {
	if s, ok := e.Key.(*ast.StringNode); ok {
		return s.Value
	}
	return strings.TrimSpace(e.Key.String())
}

// written renders a value as the author wrote it, naming an empty value.
func written(node ast.Node) string {
	if node == nil {
		return "an empty value"
	}
	if _, ok := node.(*ast.NullNode); ok {
		if text := strings.TrimSpace(node.String()); text != "" && text != "null" {
			return text
		}
		return "an empty value (null)"
	}
	return strings.TrimSpace(node.String())
}
