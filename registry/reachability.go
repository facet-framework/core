// @facet: registry/integrity/record-validation
// @facet-role: related

package facet

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// declarationLookahead bounds the lines inspected after an anchor comment.
// Package clauses, imports and long documentation may precede a declaration;
// the bound prevents a misplaced anchor from scanning the rest of the file.
const declarationLookahead = 300

// packageDeclRe matches a bare `package name` line, which a doc comment sits
// above but is never itself the symbol the comment documents.
var packageDeclRe = regexp.MustCompile(`^package\s+[A-Za-z_][A-Za-z0-9_]*$`)

// funcLikeRe matches a function/method declaration in the languages this
// naive heuristic knows about, receiver included.
var funcLikeRe = regexp.MustCompile(`\b(?:func|fn|function\*?|def)\s+(?:\([^)]*\)\s*)?([A-Za-z_$][A-Za-z0-9_$]*)`)

// typeLikeRe matches a type-shaped declaration: class, interface, struct,
// enum, trait, or a Go/TS `type` alias.
var typeLikeRe = regexp.MustCompile(`\b(?:class|interface|struct|enum|trait|type)\s+([A-Za-z_$][A-Za-z0-9_$]*)`)

// varLikeRe matches a top-level binding: Go's const/var, or JS/TS's
// const/let/var (which is how an arrow function is usually declared).
var varLikeRe = regexp.MustCompile(`\b(?:const|var|let)\s+([A-Za-z_$][A-Za-z0-9_$]*)`)

// declTokenRe extracts a bare identifier-like token for the fallback path,
// used when none of the keyword-shaped patterns above match.
var declTokenRe = regexp.MustCompile(`[A-Za-z_$][A-Za-z0-9_$]*`)

// declModifiers are tokens that can lead a declaration line without being
// its name — the fallback path skips them rather than reporting a modifier
// as the identifier.
var declModifiers = map[string]bool{
	"export": true, "default": true, "async": true, "public": true,
	"private": true, "protected": true, "static": true, "pub": true,
	"abstract": true, "final": true, "readonly": true, "override": true,
	"virtual": true, "declare": true, "internal": true, "sealed": true,
}

// isCommentLine reports whether a trimmed line is (the continuation of) a
// comment in one of the common line-comment or block-comment styles. It is
// intentionally loose — a false positive here only means the search keeps
// looking a little further for the real declaration, where a false negative
// would make the search stop on prose and misreport it as code.
func isCommentLine(text string) bool {
	for _, prefix := range [...]string{"//", "#", "/*", "*", "--"} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

// extractDeclaredIdentifier reads the symbol a single declaration-shaped
// line names. It tries the keyword-shaped patterns first, and falls back to
// the first identifier-like token that is not a common modifier keyword —
// the naive search, kept as the last resort rather than the first.
func extractDeclaredIdentifier(text string) (string, bool) {
	for _, re := range [...]*regexp.Regexp{funcLikeRe, typeLikeRe, varLikeRe} {
		if m := re.FindStringSubmatch(text); m != nil {
			return m[1], true
		}
	}
	for _, tok := range declTokenRe.FindAllString(text, -1) {
		if declModifiers[tok] {
			continue
		}
		return tok, true
	}
	return "", false
}

// anchoredIdentifier returns the name and line of the first declaration within
// declarationLookahead lines after anchorLine (1-indexed), or ok=false. It skips
// comments, decorators, package clauses, imports and blanks without a full
// language parser; file-header anchors must reach the declaration below them.
func anchoredIdentifier(lines []string, anchorLine int) (name string, line int, ok bool) {
	inImportBlock := false
	end := anchorLine + declarationLookahead
	if end > len(lines) {
		end = len(lines)
	}
	for i := anchorLine; i < end; i++ {
		text := strings.TrimSpace(lines[i])
		switch {
		case inImportBlock:
			if text == ")" {
				inImportBlock = false
			}
			continue
		case text == "":
			continue
		case isCommentLine(text):
			continue
		case strings.HasPrefix(text, "@"):
			continue
		case packageDeclRe.MatchString(text):
			continue
		case text == "import (":
			inImportBlock = true
			continue
		case strings.HasPrefix(text, "import "):
			continue
		}
		id, ok := extractDeclaredIdentifier(text)
		if !ok {
			// A real, non-skippable line that still names nothing — give up
			// rather than guess from something further away.
			return "", 0, false
		}
		return id, i + 1, true
	}
	return "", 0, false
}

// underDir reports whether path sits inside dir.
func underDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// testPathDirs are directory segments the default heuristic treats as
// exclusively test code, wherever in the tree they occur.
var testPathDirs = map[string]bool{
	"test": true, "tests": true, "spec": true, "__tests__": true, "testdata": true,
}

// testBaseGlobs recognises test filenames regardless of directory,
// including PHPUnit and JUnit FooTest files beside production sources.
var testBaseGlobs = [...]string{"*_test.*", "*.test.*", "*.spec.*", "*Test.*"}

// IsTestPath is the default "outside tests" classifier for the
// test-only-anchors check: a repository-relative path is test code when its
// filename matches a common test-file convention, or one of its directory
// segments is a common test directory. Both lists are deliberately small and
// unsurprising; a project whose test layout does not match them supplies
// LintOptions.IsTestPath instead of asking this default to grow a config
// surface of its own.
func IsTestPath(rel string) bool {
	segments := strings.Split(filepath.ToSlash(rel), "/")
	base := segments[len(segments)-1]
	for _, seg := range segments[:len(segments)-1] {
		if testPathDirs[seg] {
			return true
		}
	}
	for _, g := range testBaseGlobs {
		if ok, _ := filepath.Match(g, base); ok {
			return true
		}
	}
	return false
}

// occurrence is one line elsewhere in the tree that mentions a candidate
// identifier — the evidence the test-only-anchors check weighs against each
// candidate's own declaration site.
type occurrence struct {
	Path string
	Line int
	Test bool
}

// occurrencesFor walks the registry's scanned source once and records, for
// every identifier in idents, each line that mentions it as a whole word,
// tagged with whether the file it is in classifies as test code.
func occurrencesFor(root, recordsDir string, opts ScanOptions, idents map[string]bool, isTest func(string) bool) map[string][]occurrence {
	if opts.View != nil {
		out := map[string][]occurrence{}
		for _, src := range opts.View.Ordered() {
			paths, e := src.Files()
			if e != nil {
				continue
			}
			allowed := map[string]bool{}
			for _, rel := range paths {
				allowed[filepath.Join(src.Root, filepath.FromSlash(rel))] = true
			}
			part := occurrencesFor(src.Root, recordsDir, ScanOptions{Dirs: opts.Dirs, files: allowed}, idents, isTest)
			for key, values := range part {
				out[key] = append(out[key], values...)
			}
		}
		return out
	}
	if len(idents) == 0 {
		return nil
	}
	matchers := make(map[string]*regexp.Regexp, len(idents))
	needles := make(map[string][]byte, len(idents))
	for id := range idents {
		matchers[id] = regexp.MustCompile(`\b` + regexp.QuoteMeta(id) + `\b`)
		needles[id] = []byte(id)
	}

	roots := []string{root}
	if len(opts.Dirs) > 0 {
		roots = roots[:0]
		for _, d := range opts.Dirs {
			p := absUnder(root, d)
			if isDir(p) {
				roots = append(roots, p)
			}
		}
	}

	out := make(map[string][]occurrence, len(idents))
	for _, r := range roots {
		// Match the anchor scan's file selection, including dot-directories,
		// or reachable uses can be misreported as absent.
		walker, files := NewSourceWalker(r)
		gitFiles, useGit := GitRelevantFiles(r)
		go func() { _ = walker.Start() }()

		for f := range files {
			if opts.files != nil && !opts.files[filepath.Clean(f.Location)] {
				continue
			}
			if useGit && !gitFiles.Contains(f.Location) {
				continue
			}
			if proseExtensions[strings.ToLower(filepath.Ext(f.Location))] {
				continue
			}
			// A record is a description, never a caller.

			if recordsDir != "" && underDir(recordsDir, f.Location) {
				continue
			}
			data, err := os.ReadFile(f.Location)
			if err != nil {
				continue
			}
			var present []string
			for id, needle := range needles {
				if bytes.Contains(data, needle) {
					present = append(present, id)
				}
			}
			if len(present) == 0 {
				continue
			}
			test := isTest(relSlash(root, f.Location))
			for i, line := range strings.Split(string(data), "\n") {
				for _, id := range present {
					if matchers[id].MatchString(line) {
						out[id] = append(out[id], occurrence{Path: f.Location, Line: i + 1, Test: test})
					}
				}
			}
		}
	}
	return out
}

// relSlash renders path relative to root with forward slashes, for the
// classifier functions that judge a project-relative path rather than an
// absolute, OS-specific one.
func relSlash(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
