// Fixtures below contain example @facet: tags; this file demonstrates the
// syntax rather than implementing a feature.
// @facet-ignore-file

package facet

import (
	"fmt"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// validGo fails the test unless file is valid Go, so a case never rests on a
// file the compiler would reject.
func validGo(t *testing.T, file string) {
	t.Helper()
	if _, err := parser.ParseFile(token.NewFileSet(), "s.go", file, 0); err != nil {
		t.Fatalf("premise: the file is valid Go: %v\n%s", err, file)
	}
}

// A tag counts only where it sits: inside a comment, by its byte offset, not
// by the line it shares with one. Both files hold one tag outside any
// comment, so no symbol is shared.
func TestATagOutsideAnyCommentBindsNothing(t *testing.T) {
	cases := map[string]string{
		"a string tag beside a block comment": "package s\nvar Sample = \"@facet: a/b/one\" /* ordinary\n@facet: a/b/two */\nfunc Next() {}\n",
		"a raw string under a line directive": "package s\nconst Sample = `\n// @facet: a/b/one\n// @facet: a/b/two\n`\n\n//line generated.go:2\nvar N int\n// Ordinary doc.\n// Ordinary second line.\nfunc Other() {}\n",
	}
	for name, file := range cases {
		t.Run(name, func(t *testing.T) {
			validGo(t, file)
			if got := sharedSymbolsIn(t, "s.go", file); len(got) != 0 {
				t.Fatalf("want no symbol, got %v\n%s", got, file)
			}
		})
	}
}

// A comment group ending on the line above a declaration binds it only when
// nothing but white space lies between them: a closing brace after the
// comment ends the binding.
func TestAClosingBraceEndsTheBinding(t *testing.T) {
	const real = "package s\nfunc Outer() {\n}\n/* @facet: a/b/one\n@facet: a/b/two */\nfunc Real() {}\n"
	validGo(t, real)
	if got := strings.Join(sharedSymbolsIn(t, "s.go", real), ","); got != "Real" {
		t.Fatalf("premise: a group directly above a func binds it, got %q", got)
	}
	const body = "package s\nfunc Outer() {\n/* @facet: a/b/one\n@facet: a/b/two */ }\nfunc Next() {}\n"
	validGo(t, body)
	if got := sharedSymbolsIn(t, "s.go", body); len(got) != 0 {
		t.Fatalf("a comment closed by a brace bound %v", got)
	}
}

// A //line directive changes neither which comment holds a tag nor the line
// reported: the place is the physical line of the file.
func TestALineDirectiveKeepsThePhysicalLine(t *testing.T) {
	const file = "package s\n\n//line generated.go:100\n// @facet: a/b/one\n// @facet: a/b/two\nfunc Real() {}\n"
	validGo(t, file)
	got := lintSharedSource(t, "s.go", file).SharedClaims.Symbols
	if len(got) != 1 || got[0].Symbol != "Real" || got[0].Line != 6 {
		t.Fatalf("want Real on physical line 6, got %+v", got)
	}
}

// The blank identifier binds nothing, so it is neither a shared symbol nor a
// site of a repeated primary.
func TestTheBlankIdentifierIsNoSymbol(t *testing.T) {
	const shared = "package s\n\n// @facet: a/b/one\n// @facet: a/b/two\nvar _ = 1\n"
	validGo(t, shared)
	if got := sharedSymbolsIn(t, "s.go", shared); len(got) != 0 {
		t.Fatalf("the blank identifier was shared: %v", got)
	}

	root := project(t, "")
	sharedUseRecord(t, root, "docs/facets", "a/b/one", [3]string{"s", "Given one", ""})
	for _, name := range []string{"p.go", "q.go"} {
		writeFile(t, filepath.Join(root, "src", name), "package s\n\n// @facet: a/b/one\n// @facet-role: primary\nvar _ = 1\n")
	}
	rep := lintShared(t, root)
	if rep.Checked.Anchors != 2 {
		t.Fatalf("premise: both anchors are read, checked %d", rep.Checked.Anchors)
	}
	if got := rep.SharedClaims.Primaries; len(got) != 0 {
		t.Fatalf("the blank identifier was a primary site: %+v", got)
	}
	for _, name := range []string{"p.go", "q.go"} {
		writeFile(t, filepath.Join(root, "src", name), "package s\n\n// @facet: a/b/one\n// @facet-role: primary\nvar "+strings.ToUpper(name[:1])+" = 1\n")
	}
	if got := lintShared(t, root).SharedClaims.Primaries; len(got) != 1 {
		t.Fatalf("premise: named vars are a repeated primary, got %+v", got)
	}
}

// The data carries the whole parsed name: no cut at a byte count, so neither
// a long name nor one with a multi-byte letter at the edge is changed.
func TestASymbolNameIsKeptWhole(t *testing.T) {
	for _, name := range []string{strings.Repeat("A", 81), strings.Repeat("A", 79) + "Öx"} {
		file := "package s\n\n// @facet: a/b/one\n// @facet: a/b/two\nfunc " + name + "() {}\n"
		validGo(t, file)
		if got := strings.Join(sharedSymbolsIn(t, "s.go", file), ","); got != name {
			t.Fatalf("want %q, got %q", name, got)
		}
	}
}

// A parenthesised declaration of one spec with one name is documented by the
// declaration's comment; a group of two names is not, and the comment of one
// spec inside a group still binds that spec.
func TestAGroupOfOneSpecIsDocumentedByItsComment(t *testing.T) {
	const anchors = "// @facet: a/b/one\n// @facet: a/b/two\n"
	cases := []struct{ name, file, want string }{
		{"one spec", "package s\n\n" + anchors + "var (\n\tOnly int\n)\n", "Only"},
		{"one type spec", "package s\n\n" + anchors + "type (\n\tOnly struct{}\n)\n", "Only"},
		{"two specs", "package s\n\n" + anchors + "var (\n\tA int\n\tB int\n)\n", ""},
		{"one spec of two names", "package s\n\n" + anchors + "var (\n\tA, B int\n)\n", ""},
		{"a spec's own comment", "package s\n\nvar (\n\tA int\n\t// @facet: a/b/one\n\t// @facet: a/b/two\n\tB int\n)\n", "B"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			validGo(t, tc.file)
			if got := strings.Join(sharedSymbolsIn(t, "s.go", tc.file), ","); got != tc.want {
				t.Fatalf("want %q, got %q\n%s", tc.want, got, tc.file)
			}
		})
	}
}

// A role tag counts only inside a comment, like the id tag: the scanner joins
// a role read from the code line after an anchor, and the pass treats that
// anchor as related.
func TestARoleTagInCodeIsNoRole(t *testing.T) {
	const one = "package s\n// @facet: a/b/one\n// @facet-role: primary\n// @facet: a/b/two\nconst Role = \"@facet-role: primary\"\n"
	t.Run("one file", func(t *testing.T) {
		validGo(t, one)
		got := lintSharedSource(t, "s.go", one).SharedClaims.Symbols
		if len(got) != 1 || got[0].Symbol != "Role" {
			t.Fatalf("premise: Role is shared, got %+v", got)
		}
		if got[0].DoublePrimary || got[0].Anchors[0].Role != "primary" || got[0].Anchors[1].Role != "related" {
			t.Fatalf("a role in a string made a second primary: %+v", got[0])
		}
	})
	t.Run("two files", func(t *testing.T) { roleInTwoFiles(t) })
}

func roleInTwoFiles(t *testing.T) {
	root := project(t, "")
	sharedUseRecord(t, root, "docs/facets", "a/b/one", [3]string{"s", "Given one", ""})
	write := func(first, second string) {
		validGo(t, first)
		validGo(t, second)
		writeFile(t, filepath.Join(root, "src/p.go"), first)
		writeFile(t, filepath.Join(root, "src/q.go"), second)
	}
	write("package s\n// @facet: a/b/one\nconst First = \"@facet-role: primary\"\n",
		"package s\n// @facet: a/b/one\nconst Second = \"@facet-role: primary\"\n")
	rep := lintShared(t, root)
	if rep.Checked.Anchors != 2 {
		t.Fatalf("premise: both anchors are read, checked %d", rep.Checked.Anchors)
	}
	if got := rep.SharedClaims.Primaries; len(got) != 0 {
		t.Fatalf("roles in strings made a repeated primary: %+v", got)
	}
	write("package s\n// @facet: a/b/one\n// @facet-role: primary\nconst First = \"ordinary text\"\n",
		"package s\n// @facet: a/b/one\n// @facet-role: primary\nconst Second = \"ordinary text\"\n")
	primaries := lintShared(t, root).SharedClaims.Primaries
	if len(primaries) != 1 || len(primaries[0].Sites) != 2 || primaries[0].Sites[0].Line != 4 || primaries[0].Sites[1].Line != 4 {
		t.Fatalf("real role comments must give one primary group on line 4, got %+v", primaries)
	}
}

// An anchor's role comes only from the comment group that binds it: role
// text in a body comment neither adds a role, and role text in a string
// neither clears one the group states.
func TestARoleComesOnlyFromTheBindingGroup(t *testing.T) {
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }
	roles := func(t *testing.T, file string) (string, bool) {
		t.Helper()
		validGo(t, file)
		got := lintSharedSource(t, "s.go", file).SharedClaims.Symbols
		if len(got) != 1 || len(got[0].Anchors) != 2 {
			t.Fatalf("premise: one symbol with both anchors, got %+v", got)
		}
		return got[0].Anchors[0].Role + "/" + got[0].Anchors[1].Role, got[0].DoublePrimary
	}
	const doc = "package s\n// @facet: a/b/one\n// @facet-role: primary\n// @facet: a/b/two\n"
	for name, file := range map[string]string{
		"a block comment in the body":     doc + "func Real() { /* @facet-role: primary */ }\n",
		"a CRLF line comment in the body": crlf(doc + "func Real() { // @facet-role: primary\n}\n"),
	} {
		t.Run(name, func(t *testing.T) {
			if got, double := roles(t, file); got != "primary/related" || double {
				t.Fatalf("want primary/related without the mark, got %s %v", got, double)
			}
		})
	}
	const both = "package s\n// @facet: a/b/one\n// @facet-role: primary\n// @facet: a/b/two\n// @facet-role: primary\n"
	for name, value := range map[string]string{
		"a raw string stating primary": "`@facet-role: primary`",
		"a raw string stating related": "`@facet-role: related`",
		"ordinary text":                "`ordinary text`",
	} {
		t.Run(name, func(t *testing.T) {
			if got, double := roles(t, both+"const Raw = "+value+"\n"); got != "primary/primary" || !double {
				t.Fatalf("want primary/primary with the mark, got %s %v", got, double)
			}
		})
	}
	t.Run("body roles in two files", func(t *testing.T) {
		root := project(t, "")
		sharedUseRecord(t, root, "docs/facets", "a/b/one", [3]string{"s", "Given one", ""})
		for _, name := range []string{"First", "Second"} {
			file := "package s\n// @facet: a/b/one\nfunc " + name + "() { /* @facet-role: primary */ }\n"
			validGo(t, file)
			writeFile(t, filepath.Join(root, "src", strings.ToLower(name)+".go"), file)
		}
		rep := lintShared(t, root)
		if rep.Checked.Anchors != 2 {
			t.Fatalf("premise: both anchors are read, checked %d", rep.Checked.Anchors)
		}
		if got := rep.SharedClaims.Primaries; len(got) != 0 {
			t.Fatalf("body roles made a repeated primary: %+v", got)
		}
	})
}

// The files read for a repeated primary are chosen before the comment group
// binds each role, so the scanner's provisional role must not choose them: a
// body comment or a raw string stating related after a primary doc comment
// keeps both primary sites.
func TestARoleTextAfterTheDocKeepsThePrimarySite(t *testing.T) {
	const fn = "package s\n// @facet: a/b/one\n// @facet-role: primary\nfunc %s() { %s }\n"
	const raw = "package s\n// @facet: a/b/one\n// @facet-role: primary\nconst %s = `%s`\n"
	cases := []struct{ name, first, second string }{
		{"empty bodies", fmt.Sprintf(fn, "First", ""), fmt.Sprintf(fn, "Second", "")},
		{"a related body comment in First", fmt.Sprintf(fn, "First", "/* @facet-role: related */"), fmt.Sprintf(fn, "Second", "")},
		{"related body comments in both", fmt.Sprintf(fn, "First", "/* @facet-role: related */"), fmt.Sprintf(fn, "Second", "/* @facet-role: related */")},
		{"raw strings stating related", fmt.Sprintf(raw, "First", "@facet-role: related"), fmt.Sprintf(raw, "Second", "@facet-role: related")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := project(t, "")
			sharedUseRecord(t, root, "docs/facets", "a/b/one", [3]string{"s", "Given one", ""})
			sharedUseRecord(t, root, "docs/facets", "a/b/two", [3]string{"s", "Given two", ""})
			for name, file := range map[string]string{"p.go": tc.first, "q.go": tc.second, "z.go": "package s\n// @facet: a/b/two\nfunc Other() {}\n"} {
				validGo(t, file)
				writeFile(t, filepath.Join(root, "src", name), file)
			}
			rep := lintShared(t, root)
			if rep.Checked.Anchors != 3 || len(rep.SharedClaims.Incomplete) != 0 {
				t.Fatalf("premise: three anchors read and no gap, checked %d, gaps %+v", rep.Checked.Anchors, rep.SharedClaims.Incomplete)
			}
			got := rep.SharedClaims.Primaries
			want := []PrimarySite{{Path: "src/p.go", Line: 4, Symbol: "First"}, {Path: "src/q.go", Line: 4, Symbol: "Second"}}
			if len(got) != 1 || got[0].Record != "a/b/one" || !slices.Equal(got[0].Sites, want) {
				t.Fatalf("want one repeated primary for a/b/one at %+v, got %+v", want, got)
			}
		})
	}
}

// Comment spans are physical: the parser drops carriage returns from comment
// text and ends a CRLF comment early, so a tag near the end of a long CRLF
// block would fall outside it. LF and CRLF files give the same result.
func TestACRLFCommentKeepsItsAnchors(t *testing.T) {
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }
	block := "package s\n/*\n" + strings.Repeat(" *\n", 28) + " * @facet: a/b/one\n * @facet: a/b/two\n */\nfunc Real() {}\n"
	for name, file := range map[string]string{"LF": block, "CRLF": crlf(block)} {
		t.Run("a 30-line block doc in "+name, func(t *testing.T) {
			validGo(t, file)
			got := lintSharedSource(t, "s.go", file).SharedClaims.Symbols
			if len(got) != 1 || got[0].Symbol != "Real" || got[0].Line != 34 || len(distinctRecords(got[0].Anchors)) != 2 {
				t.Fatalf("want Real on line 34 with both records, got %+v", got)
			}
		})
	}
	t.Run("line comments in CRLF", func(t *testing.T) {
		file := crlf("package s\n\n// @facet: a/b/one\n// @facet: a/b/two\nfunc Real() {}\n")
		validGo(t, file)
		if got := strings.Join(sharedSymbolsIn(t, "s.go", file), ","); got != "Real" {
			t.Fatalf("want Real, got %q", got)
		}
	})
	t.Run("role tags in CRLF", func(t *testing.T) {
		file := crlf("package s\n\n// @facet: a/b/one\n// @facet-role: primary\n// @facet: a/b/two\n// @facet-role: primary\nfunc Real() {}\n")
		validGo(t, file)
		got := lintSharedSource(t, "s.go", file).SharedClaims.Symbols
		if len(got) != 1 || !got[0].DoublePrimary {
			t.Fatalf("want Real marked doubly primary, got %+v", got)
		}
	})
	t.Run("role tags at the end of a CRLF block", func(t *testing.T) {
		file := crlf("package s\n/*\n" + strings.Repeat(" *\n", 28) + " * @facet: a/b/one\n * @facet-role: primary\n * @facet: a/b/two\n * @facet-role: primary\n */\nfunc Real() {}\n")
		validGo(t, file)
		got := lintSharedSource(t, "s.go", file).SharedClaims.Symbols
		if len(got) != 1 || !got[0].DoublePrimary {
			t.Fatalf("want Real marked doubly primary, got %+v", got)
		}
	})
}
