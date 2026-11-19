// @facet: registry/integrity/runner-test-execution
// @facet-role: related
package facet

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

type junitCase struct {
	File     string     `xml:"file,attr"`
	Name     string     `xml:"name,attr"`
	Failures []struct{} `xml:"failure"`
	Errors   []struct{} `xml:"error"`
	Skipped  []struct{} `xml:"skipped"`
}
type junitSuite struct {
	XMLName  xml.Name
	File     string       `xml:"file,attr"`
	Tests    string       `xml:"tests,attr"`
	Failures string       `xml:"failures,attr"`
	Errors   string       `xml:"errors,attr"`
	Skipped  string       `xml:"skipped,attr"`
	Cases    []junitCase  `xml:"testcase"`
	Suites   []junitSuite `xml:"testsuite"`
}

func mergeFileState(results map[string]string, file, state string) {
	old := results[file]
	if old == TestFailed {
		return
	}
	if state == TestFailed || old == "" || state == TestUnmeasured {
		results[file] = state
	}
}
func parseJUnitResults(root, cwd string, data []byte, files []string) (map[string]string, error) {
	var suite junitSuite
	decoder := xml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&suite); err != nil {
		return nil, err
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch token := token.(type) {
		case xml.CharData:
			if strings.TrimSpace(string(token)) != "" {
				return nil, fmt.Errorf("unexpected data after JUnit root")
			}
		case xml.Comment, xml.ProcInst:
		default:
			return nil, fmt.Errorf("multiple roots or unexpected content after JUnit root")
		}
	}

	if suite.XMLName.Local != "testsuite" && suite.XMLName.Local != "testsuites" {
		return nil, fmt.Errorf("expected JUnit testsuite or testsuites")
	}
	allowed := map[string]bool{}
	for _, p := range files {
		allowed[p] = true
	}
	results := map[string]string{}
	var walk func(junitSuite, string, bool)
	walk = func(s junitSuite, inherited string, uncertain bool) {
		if s.File != "" {
			inherited = s.File
		}
		var counts func(junitSuite) (int, int, int)
		counts = func(current junitSuite) (int, int, int) {
			tests, failures, errors := len(current.Cases), 0, 0
			for _, c := range current.Cases {
				if len(c.Failures) > 0 {
					failures++
				}
				if len(c.Errors) > 0 {
					errors++
				}
			}
			for _, child := range current.Suites {
				t, f, e := counts(child)
				tests += t
				failures += f
				errors += e
			}
			return tests, failures, errors
		}
		tests, failures, errors := counts(s)
		var skippedCount func(junitSuite) int
		skippedCount = func(current junitSuite) int {
			n := 0
			for _, c := range current.Cases {
				if len(c.Skipped) > 0 {
					n++
				}
			}
			for _, child := range current.Suites {
				n += skippedCount(child)
			}
			return n
		}
		for _, pair := range []struct {
			raw    string
			actual int
		}{{s.Tests, tests}, {s.Failures, failures}, {s.Errors, errors}, {s.Skipped, skippedCount(s)}} {
			if pair.raw != "" {
				n, e := strconv.Atoi(pair.raw)
				if e != nil || n < 0 || n != pair.actual {
					uncertain = true
				}
			}
		}
		for _, c := range s.Cases {
			if c.File == "" && inherited == "" {
				uncertain = true
			}
		}
		for _, c := range s.Cases {
			path := c.File
			if path == "" {
				path = inherited
			}
			if path == "" {
				continue
			}
			file, err := normalizedTestPath(root, cwd, path)
			if err != nil || !allowed[file] {
				continue
			}
			state := TestPassed
			if uncertain || len(c.Skipped) > 0 {
				state = TestUnmeasured
			}
			if len(c.Failures) > 0 || len(c.Errors) > 0 {
				state = TestFailed
			}
			mergeFileState(results, file, state)
		}
		for _, child := range s.Suites {
			walk(child, inherited, uncertain)
		}
	}
	walk(suite, "", false)
	return results, nil
}

type goTestEvent struct {
	Action  string
	Package string
	Test    string
}

func goPackageForFile(root, path string) (string, error) {
	dir := filepath.Dir(filepath.Join(root, filepath.FromSlash(path)))
	for current := dir; ; current = filepath.Dir(current) {
		data, err := os.ReadFile(filepath.Join(current, "go.mod"))
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 2 && fields[0] == "module" {
					module := strings.Trim(fields[1], `"`)
					rel, e := filepath.Rel(current, dir)
					if e != nil {
						return "", e
					}
					if rel != "." {
						module += "/" + filepath.ToSlash(rel)
					}
					return module, nil
				}
			}
			return "", fmt.Errorf("go.mod has no module declaration")
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if current == root || filepath.Dir(current) == current {
			break
		}
	}
	return "", fmt.Errorf("no local go.mod for %s", path)
}
func declaredGoTests(root, path string) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, filepath.FromSlash(path)), nil, 0)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}
		name := fn.Name.Name
		for _, prefix := range []string{"Test", "Example", "Fuzz", "Benchmark"} {
			if strings.HasPrefix(name, prefix) && name != "TestMain" && (len(name) == len(prefix) || !unicode.IsLower(rune(name[len(prefix)]))) {
				names = append(names, name)
				break
			}
		}
	}
	return names, nil
}
func parseGoJSONResults(root string, data []byte, files []string) (map[string]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	events := map[string]map[string]string{}
	packages := map[string]string{}
	seen := false
	for {
		var event goTestEvent
		err := decoder.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if event.Action == "" || event.Package == "" {
			return nil, fmt.Errorf("invalid Go test JSON event")
		}
		seen = true
		if event.Action != "pass" && event.Action != "fail" && event.Action != "skip" {
			continue
		}
		state := TestPassed
		if event.Action == "fail" {
			state = TestFailed
		} else if event.Action == "skip" {
			state = TestUnmeasured
		}
		if event.Test == "" {
			if packages[event.Package] != TestFailed {
				packages[event.Package] = state
			}
			continue
		}
		if strings.Contains(event.Test, "/") {
			continue
		}
		if events[event.Package] == nil {
			events[event.Package] = map[string]string{}
		}
		mergeFileState(events[event.Package], event.Test, state)
	}
	if !seen {
		return nil, fmt.Errorf("empty Go test JSON report")
	}
	results := map[string]string{}
	for _, file := range files {
		if filepath.Ext(file) != ".go" {
			continue
		}
		pkg, err := goPackageForFile(root, file)
		if err != nil {
			continue
		}
		names, err := declaredGoTests(root, file)
		if err != nil || len(names) == 0 {
			continue
		}
		state := TestPassed
		for _, name := range names {
			observed := events[pkg][name]
			if observed == TestFailed {
				state = TestFailed
				break
			}
			if observed != TestPassed {
				state = TestUnmeasured
			}
		}
		if state != TestFailed && packages[pkg] != TestPassed && packages[pkg] != TestFailed {
			state = TestUnmeasured
		}
		results[file] = state
	}
	return results, nil
}
