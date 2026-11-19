package scaffold

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// RecordsDir is where the scaffold puts the record layout unless the caller
// names another directory. Payload paths below it are rewritten to the chosen
// directory when the files are written.
const RecordsDir = "docs/facets"

// RecordSubdirs are the record directories every scaffolded project gets,
// created even when they stay empty, in the order they are reported.
var RecordSubdirs = []string{"features", "policies", "assumptions", "data-entities", "schema", "framework"}

// Source is one file of the payload: where it lands in a project, and where
// its bytes come from in the FACET Core source tree.
type Source struct {
	// Path is the destination relative to the project root, with the record
	// layout under RecordsDir.
	Path string
	// From is the canonical file relative to the module root. Empty for a
	// file whose content is Literal.
	From string
	// Strip removes the anchor declarations that bind the canonical file to a
	// record of the FACET Core registry. A scaffolded project holds no such
	// record, so a copied anchor would fail its very first lint.
	Strip stripMode
	// Literal is the content of a file that has no canonical source because
	// its content is a decision about the project rather than a copy.
	Literal []byte
}

type stripMode int

const (
	keepAnchors stripMode = iota
	// stripColumnZero removes '# @facet:' and '# @facet-role:' comment lines
	// that start at column 0. The lint scripts never indent an anchor.
	stripColumnZero
	// stripIndented also removes indented ones; the dispatcher declares one
	// inside a condition.
	stripIndented
)

// facetIgnore is written empty on purpose: what a scan may skip is a judgement
// about one repository, and a scaffold that guessed would either miss what
// matters or hide something that carries anchors.
const facetIgnore = `# Files FACET never needs to read.
#
# Every scan for @facet: anchors opens each file this does not exclude, and a
# repository usually holds a lot that can never carry one: rendered audio,
# images, fonts, compiled output, generated bundles.
#
# This file ships empty on purpose. What is irrelevant is a judgement about
# this repository, and a scaffold that guessed would either miss what matters
# here or hide something that does carry anchors. Fill it in once, from what is
# actually in the tree. Media and documents are the usual answer.
#
# Syntax is .gitignore's, one pattern per line, applying to this directory and
# everything below. This is not .gitignore: excluding a file here says nothing
# about whether it is versioned.
#
# Example, once you know the tree:
#   *.mp3
#   *.png
`

// The published project workflow. A source tree that keeps the files it
// publishes in an overlay directory holds it there, and then its own
// .github/workflows/facet-lint.yml is that tree's CI rather than the one a
// project gets; a published tree holds it at the usual place.
const (
	overlayDir     = "facet-framework/core"
	workflowPath   = ".github/workflows/facet-lint.yml"
	dispatcherPath = "bin/facet"
	specPath       = "docs/framework/FACET.md"
	schemaDir      = "schema"
	lintSuiteDir   = "tools/facet-lint"
	DispatcherCopy = "tools/facet-lint/facet"
)

// Sources lists the payload in the order the scaffold reports it, read from
// the source tree rooted at root (the directory holding go.mod).
func Sources(root string) ([]Source, error) {
	var out []Source

	schemas, err := filepath.Glob(filepath.Join(root, schemaDir, "*.json"))
	if err != nil {
		return nil, err
	}
	if len(schemas) == 0 {
		return nil, fmt.Errorf("scaffold: no JSON schemas under %s", filepath.Join(root, schemaDir))
	}
	sort.Strings(schemas)
	for _, s := range schemas {
		name := filepath.Base(s)
		out = append(out, Source{Path: path.Join(RecordsDir, "schema", name), From: path.Join(schemaDir, name)})
	}
	out = append(out, Source{Path: path.Join(RecordsDir, "framework", "FACET.md"), From: specPath})

	tools, err := filepath.Glob(filepath.Join(root, lintSuiteDir, "*.sh"))
	if err != nil {
		return nil, err
	}
	if len(tools) == 0 {
		return nil, fmt.Errorf("scaffold: no lint scripts under %s", filepath.Join(root, lintSuiteDir))
	}
	sort.Strings(tools)
	for _, t := range tools {
		name := filepath.Base(t)
		out = append(out, Source{Path: path.Join(lintSuiteDir, name), From: path.Join(lintSuiteDir, name), Strip: stripColumnZero})
	}
	out = append(out, Source{Path: DispatcherCopy, From: dispatcherPath, Strip: stripIndented})

	out = append(out, Source{Path: ".facetignore", Literal: []byte(facetIgnore)})

	workflow := workflowPath
	if info, err := os.Stat(filepath.Join(root, overlayDir)); err == nil && info.IsDir() {
		// With an overlay present, the tree's own workflow is not the
		// published one; a missing overlay copy is an error, never a fallback.
		workflow = path.Join(overlayDir, workflowPath)
	}
	out = append(out, Source{Path: workflowPath, From: workflow})
	return out, nil
}

// Generate builds the payload bundle from the source tree at root. The bundle
// is a txtar archive: a comment, then one '-- path --' line per file followed
// by its exact bytes.
func Generate(root string) ([]byte, error) {
	sources, err := Sources(root)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString(bundleHeader)
	for _, s := range sources {
		data := s.Literal
		if s.From != "" {
			data, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(s.From)))
			if err != nil {
				return nil, fmt.Errorf("scaffold: %w", err)
			}
			data = stripAnchors(data, s.Strip)
		}
		if err := checkBundleable(s.Path, data); err != nil {
			return nil, err
		}
		fmt.Fprintf(&buf, "-- %s --\n", s.Path)
		buf.Write(data)
	}
	return buf.Bytes(), nil
}

// The marker in the third line keeps every scanner for anchors out of the
// bundle: the specification and the lint suite inside it write anchors out as
// examples and patterns, and none of them is a claim of this package.
const bundleHeader = `Generated by 'go generate' in this package from the canonical files of the
source tree it is built in. Do not edit; regenerate.
@facet-ignore-file
`

// FindModuleRoot walks up from dir to the directory holding go.mod.
func FindModuleRoot(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("scaffold: no go.mod above the working directory")
		}
		dir = parent
	}
}

// stripAnchors drops whole comment lines that declare an anchor, with the
// same rule the shell scaffold applies. Only a comment whose content begins
// with the tag counts: these files are the linter, so the tag also appears in
// prose, in grep patterns and in fixtures, and those lines must stay.
func stripAnchors(data []byte, mode stripMode) []byte {
	if mode == keepAnchors {
		return data
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	var out bytes.Buffer
	for _, line := range lines {
		if isAnchorLine(strings.TrimSuffix(string(line), "\n"), mode == stripIndented) {
			continue
		}
		out.Write(line)
	}
	return out.Bytes()
}

func isAnchorLine(line string, indented bool) bool {
	if indented {
		line = strings.TrimLeft(line, " \t\v\f\r")
	}
	if !strings.HasPrefix(line, "#") {
		return false
	}
	rest := strings.TrimLeft(line[1:], " \t\v\f\r")
	for _, tag := range []string{"@facet:", "@facet-role:"} {
		if strings.HasPrefix(rest, tag) && len(rest) > len(tag) && strings.ContainsRune(" \t\v\f\r", rune(rest[len(tag)])) {
			return true
		}
	}
	return false
}

// checkBundleable refuses content the txtar format cannot carry exactly: a
// file must end in a newline, and no line may look like a file marker.
func checkBundleable(name string, data []byte) error {
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return fmt.Errorf("scaffold: %s does not end in a newline and cannot be bundled exactly", name)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if isMarker(line) {
			return fmt.Errorf("scaffold: %s holds a line that reads as a bundle marker: %q", name, line)
		}
	}
	return nil
}

func isMarker(line string) bool {
	return strings.HasPrefix(line, "-- ") && strings.HasSuffix(line, " --") && len(line) > len("--  --")
}
