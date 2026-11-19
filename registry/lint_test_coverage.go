package facet

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// lintUnclaimedTests reports discovered test files absent from every record's
// verification entries, grouped and sorted by directory. Snapshots and Go files
// with no runnable test declarations are excluded. A warning asks for review,
// never for adding proof associations merely to clear the finding.
//
// @facet: registry/integrity/unclaimed-tests
// @facet-role: primary
func lintUnclaimedTests(reg Registry, rep *Report, features []Record, walked []SourceFile, isTest func(string) bool) {
	if isTest == nil {
		isTest = IsTestPath
	}

	// Every path a record claims, in the form verification ids are written.
	// verificationPath is the same reading the verification-targets check
	// does, so a project whose ids carry a test name after the path is
	// understood the same way by both.
	claimed := map[string]bool{}
	for _, rec := range features {
		for _, prov := range rec.Provenances() {
			if rel, ok := verificationPath(prov.TestID); ok {
				claimed[filepath.ToSlash(rel)] = true
			}
		}
	}

	byDir := map[string][]string{}
	for _, f := range walked {
		rel := filepath.ToSlash(reg.Rel(f.Path))
		if rel == "" || !isTest(rel) || claimed[rel] {
			continue
		}

		if path.Ext(rel) == ".snap" || hasSegment(rel, "__snapshots__") {
			continue
		}

		if path.Ext(rel) == ".go" && !declaresGoTest(f.Path) {
			continue
		}
		dir := path.Dir(rel)
		byDir[dir] = append(byDir[dir], path.Base(rel))
	}

	dirs := make([]string, 0, len(byDir))
	for dir := range byDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	for _, dir := range dirs {
		names := byDir[dir]
		sort.Strings(names)
		rep.add(Finding{
			Severity: SeverityWarning,
			Check:    "unclaimed-tests",
			Path:     dir,
			Message:  fmt.Sprintf("%d test file(s) in %s are named by no record: %s", len(names), dir, strings.Join(names, ", ")),
			Hint:     "if one of them proves a contract scenario, name it under that record's verification — after reading the test body, because lintProves only checks that the scenario id exists. A test that checks a helper or builds a fixture proves no promise and belongs here unclaimed",
		})
	}
}

// goTestFuncRe matches the declaration of anything `go test` will run.
var goTestFuncRe = regexp.MustCompile(`(?m)^func (Test|Benchmark|Fuzz|Example)`)

// declaresGoTest reports whether a Go file declares anything `go test` runs.
func declaresGoTest(abs string) bool {
	data, err := os.ReadFile(abs)
	if err != nil {
		return true
	}
	return goTestFuncRe.Match(data)
}

// hasSegment reports whether a slash-separated path has the given directory
// segment.
func hasSegment(rel, segment string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if seg == segment {
			return true
		}
	}
	return false
}
