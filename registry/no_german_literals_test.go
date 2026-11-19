// @facet-ignore-file
package facet

import (
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The Core's Go code is English: CLI output, errors, and every other string a
// user reads. German lives only in a language table. This guard scans every
// non-test Go file under the trees below for string literals that look German
// and fails on any outside the allowlist.
var germanLiteralRoots = []string{"cmd", "internal", "pkg", "agents", "cli", "registry", "corpus", "rag", "release"}

// germanLiteralAllowlist names, per file, the literals that may stay German.
// "*" allows a whole file. Every entry says why. Keys are file names, not
// paths, so an entry holds in both layouts; each name is unique in the module.
var germanLiteralAllowlist = map[string][]string{
	// The German locale of the generated documentation site.
	"locale_de.go": {"*"},
	// The pre-rc.1 German ADR status, read as an alias of "superseded".
	"adr_proposal.go": {`"abgelöst"`},
}

var (
	germanUmlaut = regexp.MustCompile(`[äöüÄÖÜß]`)
	// Lower-case only: capitalised, several of these are English acronyms
	// ("MIT", "IST", "DES").
	germanFunctionWords = regexp.MustCompile(`\b(und|nicht|ist|eine|einen|einem|einer|mit|wird|werden|wurde|keine|keinen|kein|sich|oder|auch|nur|noch|wenn|dann|bei|den|dem|des|der|zum|zur|vom|nach|wie|sind|hat|haben|kann|muss|soll|dass|weil|deshalb|schon|aber|ohne|diese|dieser|dieses|hier|jetzt|gibt|steht|liegt|fehlt|nichts|beim|wieder|zwischen|aus|liest|bitte)\b`)
	germanWords         = regexp.MustCompile(`(?i)\b(freigeben|werkzeuge|umgebung|datei|dateien|seite|seiten|projekt|lokal|signiert|zeigt|erklärt|vorgaben|weitere)\b`)
)

func looksGerman(literal string) bool {
	if germanUmlaut.MatchString(literal) {
		return true
	}
	if !strings.ContainsAny(literal, " \t") {
		return false
	}
	return germanFunctionWords.MatchString(literal) || germanWords.MatchString(literal)
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the package directory")
		}
		dir = parent
	}
}

// germanLiterals returns "path:line: literal" for every German-looking string
// literal in src that the allowlist does not cover.
func germanLiterals(path string, src []byte) []string {
	allowed := map[string]bool{}
	for _, literal := range germanLiteralAllowlist[filepath.Base(path)] {
		allowed[literal] = true
	}
	if allowed["*"] {
		return nil
	}
	fset := token.NewFileSet()
	file := fset.AddFile(path, -1, len(src))
	var s scanner.Scanner
	s.Init(file, src, nil, 0)
	var found []string
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.STRING && looksGerman(lit) && !allowed[lit] {
			found = append(found, path+":"+strconv.Itoa(fset.Position(pos).Line)+": "+lit)
		}
	}
	return found
}

func TestNoGermanStringLiteralsInCoreGo(t *testing.T) {
	root := moduleRoot(t)
	var found []string
	scanned := 0
	for _, dir := range germanLiteralRoots {
		if _, err := os.Stat(filepath.Join(root, dir)); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if name := d.Name(); name == "testdata" || name == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			scanned++
			found = append(found, germanLiterals(filepath.ToSlash(rel), src)...)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Precondition: the walk reached the code. A guard that scanned nothing
	// would pass for a clean tree.
	if scanned < 100 {
		t.Fatalf("scanned only %d Go files under %v; the walk did not reach the code", scanned, germanLiteralRoots)
	}
	sort.Strings(found)
	for _, f := range found {
		t.Errorf("German string literal outside the allowlist: %s", f)
	}
}

// The detector itself: each shape that shipped before rc.1 is caught, and
// English that merely shares letters with German is not.
func TestTheGermanLiteralDetector(t *testing.T) {
	german := []string{
		`"kein Stand erklärt"`,
		`"%s %s — aus %s (lokal, nicht signiert)\n"`,
		`"  Werkzeuge    "`,
		`"                        freigeben?"`,
		`"# Dieses Projekt hat keine "`,
		`"Schließen"`,
	}
	english := []string{
		`"MIT License"`,
		`"no status stated"`,
		`".der"`,
		`"keine-antwort"`,
		`"the plugin was installed"`,
		`"die"`,
	}
	for _, lit := range german {
		if !looksGerman(lit) {
			t.Errorf("not recognised as German: %s", lit)
		}
	}
	for _, lit := range english {
		if looksGerman(lit) {
			t.Errorf("English or a wire value taken for German: %s", lit)
		}
	}
	src := []byte("package x\n\nvar a = \"abgelöst\"\nvar c = \"nicht erlaubt hier\"\n")
	got := germanLiterals("internal/facet/adr_proposal.go", src)
	if len(got) != 1 || !strings.Contains(got[0], `"nicht erlaubt hier"`) {
		t.Fatalf("allowlist must cover only its named literals, got %v", got)
	}
	if got := germanLiterals("internal/docsite/locale_de.go", src); len(got) != 0 {
		t.Fatalf("a whole-file allowlist entry still reported %v", got)
	}
	if got := germanLiterals("internal/cli/other.go", src); len(got) != 2 {
		t.Fatalf("a file outside the allowlist reported %v, want both literals", got)
	}
}
