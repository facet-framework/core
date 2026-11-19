// Fixtures here contain example @facet: tags; the file exercises the filter
// rather than implementing a feature, so it opts out of scanning.
// @facet-ignore-file

package facet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const filterGoFile = `// @facet: registry/layout/source-scanning
// @facet-role: primary
package facet

import "os"

// Listen opens a loopback listener.
//
// @facet: host/local/local-only-access
// @facet-role: primary
// @facet-proves: the-port-is-not-on-the-network
func Listen() {}

func helper() {}
`

// The separator '//' between the description and the tags goes with the
// tags: the clean file reads as it did before the anchor was added.
const filterGoClean = `package facet

import "os"

// Listen opens a loopback listener.
func Listen() {}

func helper() {}
`

// The property everything else rests on: what the clean removes, the smudge
// puts back, byte for byte, and the file in between carries no anchor at all.
func TestPrivateAnchorsRoundTrip(t *testing.T) {
	clean, blocks := StripPrivateAnchors("src/scan.go", []byte(filterGoFile), nil)
	if string(clean) != filterGoClean {
		t.Fatalf("clean content differs:\n%s", clean)
	}
	if strings.Contains(string(clean), "@facet") {
		t.Fatal("a private anchor reached the cleaned content")
	}
	if len(blocks) != 2 {
		t.Fatalf("want two blocks, got %d", len(blocks))
	}
	if blocks[0].Before != "package facet" || blocks[0].Occurrence != 1 {
		t.Errorf("first block placed at %q #%d, want the package clause", blocks[0].Before, blocks[0].Occurrence)
	}
	if blocks[1].Before != "func Listen() {}" || len(blocks[1].Lines) != 4 || blocks[1].Lines[0] != "//" {
		t.Errorf("second block must carry its separator, got %+v", blocks[1])
	}

	back, orphans := InjectAnchors(clean, blocks)
	if len(orphans) != 0 {
		t.Fatalf("nothing moved, nothing may be orphaned: %+v", orphans)
	}
	if string(back) != filterGoFile {
		t.Fatalf("round trip lost something:\n%s", back)
	}
}

// Installing the filter on a repository that already commits anchors must
// change nothing about them: a block that stands in HEAD is not private and
// passes through, or the first 'git add' after installing would delete every
// anchor the project had.
func TestACommittedAnchorPassesThrough(t *testing.T) {
	committed := "// @facet: registry/layout/source-scanning\n// @facet-role: primary\npackage facet\n\nfunc a() {}\n"
	working := committed + "\n// @facet: registry/integrity/private\n// @facet-role: related\nfunc b() {}\n"

	clean, blocks := StripPrivateAnchors("x.go", []byte(working), []byte(committed))
	if !strings.Contains(string(clean), "@facet: registry/layout/source-scanning") {
		t.Fatal("the committed anchor was stripped")
	}
	if strings.Contains(string(clean), "registry/integrity/private") {
		t.Fatal("the private anchor was not stripped")
	}
	if len(blocks) != 1 || blocks[0].Before != "func b() {}" {
		t.Fatalf("want the one private block above b, got %+v", blocks)
	}
}

// A placement key is content, so a block follows its declaration wherever an
// upstream edit moves it. This is what makes a pull a non-event instead of a
// conflict.
func TestAnAnchorFollowsItsDeclarationWhenLinesMove(t *testing.T) {
	_, blocks := StripPrivateAnchors("x.go", []byte(filterGoFile), nil)

	// Upstream added twenty lines above Listen and dropped helper.
	moved := "package facet\n\nimport \"os\"\n" + strings.Repeat("\n// padding\n", 20) +
		"// Listen opens a loopback listener.\nfunc Listen() {}\n"
	back, orphans := InjectAnchors([]byte(moved), blocks)
	if len(orphans) != 0 {
		t.Fatalf("both places still exist, got orphans %+v", orphans)
	}
	lines := strings.Split(string(back), "\n")
	for i, l := range lines {
		if l == "func Listen() {}" {
			if lines[i-1] != "// @facet-proves: the-port-is-not-on-the-network" {
				t.Fatalf("the block did not follow Listen; line above is %q", lines[i-1])
			}
			return
		}
	}
	t.Fatal("Listen vanished")
}

// A block whose place is gone is orphaned: left out rather than guessed into a
// nearby spot, returned rather than dropped. The clean that follows keeps it,
// because its absence from the working copy is not the author's decision — the
// author never saw it.
func TestAnAnchorWhosePlaceIsGoneIsKeptAndReported(t *testing.T) {
	_, blocks := StripPrivateAnchors("x.go", []byte(filterGoFile), nil)

	renamed := strings.ReplaceAll(filterGoClean, "func Listen() {}", "func Serve() {}")
	back, orphans := InjectAnchors([]byte(renamed), blocks)
	if len(orphans) != 1 || orphans[0].Before != "func Listen() {}" {
		t.Fatalf("want the Listen block orphaned, got %+v", orphans)
	}
	if strings.Contains(string(back), "local-only-access") {
		t.Fatal("an orphaned block must not be guessed into the file")
	}
	if !strings.Contains(string(back), "registry/layout/source-scanning") {
		t.Fatal("the block whose place survived must still be placed")
	}

	// The working copy now lacks the orphaned block, and lacks its place. A
	// clean sees only the placed one; the merge keeps the orphan, decided
	// from the content rather than from any stored flag.
	clean, found := StripPrivateAnchors("x.go", back, nil)
	previous := PrivateAnchors{Path: "x.go", Blocks: blocks}
	merged := MergeSidecar(previous, found, clean)
	if len(merged.Blocks) != 2 {
		t.Fatalf("the orphan must survive the clean, got %+v", merged.Blocks)
	}
	if got := merged.Orphans(back); len(got) != 1 || got[0].Before != "func Listen() {}" {
		t.Fatalf("Orphans must name the block not standing in the file, got %+v", got)
	}
}

// The other half of the same rule: a block the author deleted from the working
// copy is gone. Only orphans are exempt from "absent means removed".
func TestADeletedAnchorIsDroppedByTheNextClean(t *testing.T) {
	_, blocks := StripPrivateAnchors("x.go", []byte(filterGoFile), nil)
	previous := PrivateAnchors{Path: "x.go", Blocks: blocks}

	// The author removed the Listen block by hand.
	edited := strings.Replace(filterGoFile,
		"// @facet: host/local/local-only-access\n// @facet-role: primary\n// @facet-proves: the-port-is-not-on-the-network\n", "", 1)
	clean, found := StripPrivateAnchors("x.go", []byte(edited), nil)
	merged := MergeSidecar(previous, found, clean)
	if len(merged.Blocks) != 1 || merged.Blocks[0].Before != "package facet" {
		t.Fatalf("a deliberately deleted block must not come back, got %+v", merged.Blocks)
	}
}

func TestPrivateAnchorsPreserveCRLF(t *testing.T) {
	crlf := strings.ReplaceAll(filterGoFile, "\n", "\r\n")
	clean, blocks := StripPrivateAnchors("x.go", []byte(crlf), nil)
	if strings.Contains(string(clean), "\n") && !strings.Contains(string(clean), "\r\n") {
		t.Fatal("line endings were normalised away")
	}
	back, _ := InjectAnchors(clean, blocks)
	if string(back) != crlf {
		t.Fatal("CRLF round trip lost bytes")
	}
}

// The filter strips exactly what the scanner would read as an anchor. A file
// that opted out, and prose, carry examples rather than claims — stripping
// them would break the very tests and documents that show the syntax.
func TestFilterLeavesFixturesAndProseAlone(t *testing.T) {
	fixture := "// @facet-ignore-file\npackage x\n\nconst example = `\n// @facet: a/b/c\n// @facet-role: primary\n`\n"
	if clean, blocks := StripPrivateAnchors("x_test.go", []byte(fixture), nil); string(clean) != fixture || len(blocks) != 0 {
		t.Fatal("an opted-out file must pass through untouched")
	}
	prose := "# Guide\n\n// @facet: a/b/c\n"
	if clean, blocks := StripPrivateAnchors("GUIDE.md", []byte(prose), nil); string(clean) != prose || len(blocks) != 0 {
		t.Fatal("prose must pass through untouched")
	}
}

// A tag trailing a statement is not a line the filter may remove. It stays,
// and it is committed — private anchors stand on their own line.
func TestATrailingTagIsNotStripped(t *testing.T) {
	src := "package x\n\nfunc a() {} // @facet: a/b/c\n"
	clean, blocks := StripPrivateAnchors("x.go", []byte(src), nil)
	if string(clean) != src || len(blocks) != 0 {
		t.Fatalf("a trailing tag is code's line, got %q", clean)
	}
}

// The clean side knows nothing about comment syntax: a tag preceded by nothing
// but punctuation is an anchor line in any language.
func TestPrivateAnchorsAcrossLanguages(t *testing.T) {
	cases := map[string]string{
		"a.py":   "# @facet: a/b/c\n# @facet-role: primary\ndef run():\n    pass\n",
		"a.lua":  "-- @facet: a/b/c\n-- @facet-role: primary\nlocal function run() end\n",
		"a.html": "<!-- @facet: a/b/c -->\n<!-- @facet-role: primary -->\n<main></main>\n",
		"a.css":  "/* @facet: a/b/c */\n/* @facet-role: primary */\n.x {}\n",
		"a.sql":  "-- @facet: a/b/c\n-- @facet-role: primary\nCREATE TABLE x ();\n",
		"a.rs":   "    // @facet: a/b/c\n    // @facet-role: primary\n    fn run() {}\n",
	}
	for name, src := range cases {
		clean, blocks := StripPrivateAnchors(name, []byte(src), nil)
		if strings.Contains(string(clean), "@facet") {
			t.Errorf("%s: anchor survived the clean: %q", name, clean)
			continue
		}
		if len(blocks) != 1 {
			t.Errorf("%s: want one block, got %d", name, len(blocks))
			continue
		}
		if back, _ := InjectAnchors(clean, blocks); string(back) != src {
			t.Errorf("%s: round trip differs:\n%s", name, back)
		}
	}
}

func TestABlockAtTheEndOfAFileComesBackAtTheEnd(t *testing.T) {
	src := "package x\n\nfunc a() {}\n// @facet: a/b/c\n// @facet-role: related\n"
	clean, blocks := StripPrivateAnchors("x.go", []byte(src), nil)
	if len(blocks) != 1 || !blocks[0].AtEnd {
		t.Fatalf("want an at-end block, got %+v", blocks)
	}
	if back, _ := InjectAnchors(clean, blocks); string(back) != src {
		t.Fatalf("round trip differs:\n%s", back)
	}
}

// Occurrence distinguishes repeated identical content lines: the two anchor
// blocks attach to the second and third occurrences of x = 1, preserving order.
func TestOccurrenceDisambiguatesRepeatedLines(t *testing.T) {
	src := "x = 1\n// @facet: a/b/c\n// @facet-role: primary\nx = 1\n// @facet: d/e/f\n// @facet-role: related\nx = 1\n"
	clean, blocks := StripPrivateAnchors("x.js", []byte(src), nil)
	if len(blocks) != 2 || blocks[0].Occurrence != 2 || blocks[1].Occurrence != 3 {
		t.Fatalf("occurrences must count repetitions in the cleaned file, got %+v", blocks)
	}
	if back, _ := InjectAnchors(clean, blocks); string(back) != src {
		t.Fatalf("round trip differs:\n%s", back)
	}
}

// A linked worktree and a submodule have a '.git' file pointing elsewhere; the
// sidecar has to land where that points, or one worktree's private anchors
// would be another's.
func TestGitDirFollowsAGitdirPointer(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "elsewhere", "worktrees", "wt")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+real+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := GitDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != real {
		t.Fatalf("GitDir = %q, want %q", got, real)
	}

	plain := t.TempDir()
	os.Mkdir(filepath.Join(plain, ".git"), 0o755)
	if got, _ := GitDir(plain); got != filepath.Join(plain, ".git") {
		t.Fatalf("a plain .git directory must resolve to itself, got %q", got)
	}
	if got, _ := GitCommonDir(plain); got != filepath.Join(plain, ".git") {
		t.Fatalf("the main worktree's common dir is its git dir, got %q", got)
	}

	// A linked worktree's git dir names the shared one in 'commondir',
	// relative to itself — the sidecar has to land there, or one worktree's
	// private anchors would be invisible to another.
	os.WriteFile(filepath.Join(real, "commondir"), []byte("../..\n"), 0o644)
	if got, _ := GitCommonDir(root); got != filepath.Join(root, "elsewhere") {
		t.Fatalf("GitCommonDir must follow commondir, got %q", got)
	}
}

func TestSidecarSavesLoadsAndRemovesWhenEmpty(t *testing.T) {
	gitDir := t.TempDir()
	pa := PrivateAnchors{Path: "internal/x/y.go", Blocks: []AnchorBlock{{
		Lines: []string{"// @facet: a/b/c", "// @facet-role: primary"}, Before: "package x", Occurrence: 1,
	}}}
	if err := SavePrivateAnchors(gitDir, pa); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SidecarPath(gitDir, "internal/x/y.go")); err != nil {
		t.Fatalf("the sidecar must mirror the file's path: %v", err)
	}
	got, err := LoadPrivateAnchors(gitDir, "internal/x/y.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Blocks) != 1 || got.Blocks[0].Before != "package x" {
		t.Fatalf("loaded %+v", got)
	}
	all, err := ListPrivateAnchors(gitDir)
	if err != nil || len(all) != 1 {
		t.Fatalf("list = %+v, %v", all, err)
	}

	if err := SavePrivateAnchors(gitDir, PrivateAnchors{Path: "internal/x/y.go"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SidecarPath(gitDir, "internal/x/y.go")); !os.IsNotExist(err) {
		t.Fatal("an empty sidecar must be removed, not left as a stub")
	}
	if none, err := LoadPrivateAnchors(gitDir, "never/seen.go"); err != nil || len(none.Blocks) != 0 {
		t.Fatalf("a file without a sidecar has no private anchors: %+v %v", none, err)
	}
}

// The separator inserted with private PHP docblock tags belongs to their
// block. Cleaning removes it with the tags to reproduce HEAD byte for byte;
// smudging restores the separator and tags together.
func TestADocblockSeparatorGoesWithItsBlock(t *testing.T) {
	head := "<?php\n/**\n * Creates the invoice.\n */\nfunction create() {}\n"
	work := "<?php\n/**\n * Creates the invoice.\n *\n * @facet: billing/invoice/create\n * @facet-role: primary\n */\nfunction create() {}\n"

	clean, blocks := StripPrivateAnchors("a.php", []byte(work), []byte(head))
	if string(clean) != head {
		t.Fatalf("the separator leaked:\n%s", clean)
	}
	if len(blocks) != 1 || len(blocks[0].Lines) != 3 || strings.TrimSpace(blocks[0].Lines[0]) != "*" {
		t.Fatalf("the block must carry the separator, got %+v", blocks)
	}
	if back, _ := InjectAnchors(clean, blocks); string(back) != work {
		t.Fatalf("round trip differs:\n%s", back)
	}
}

// Tags placed first, separator after: the separator still goes with the block.
func TestALeadingTagGroupTakesItsTrailingSeparator(t *testing.T) {
	head := "<?php\n/**\n * Creates the invoice.\n */\nfunction create() {}\n"
	work := "<?php\n/**\n * @facet: billing/invoice/create\n * @facet-role: primary\n *\n * Creates the invoice.\n */\nfunction create() {}\n"

	clean, blocks := StripPrivateAnchors("a.php", []byte(work), []byte(head))
	if string(clean) != head {
		t.Fatalf("the trailing separator leaked:\n%s", clean)
	}
	if back, _ := InjectAnchors(clean, blocks); string(back) != work {
		t.Fatalf("round trip differs:\n%s", back)
	}
}

// A separator the file already had is not the block's. Tags added between two
// paragraphs take the one that came with them and leave the original.
func TestAnExistingParagraphSeparatorStays(t *testing.T) {
	head := "// Listen opens a loopback listener.\n//\n// Falls back to a random port.\nfunc Listen() {}\n"
	work := "// Listen opens a loopback listener.\n//\n// @facet: a/b/c\n// @facet-role: primary\n//\n// Falls back to a random port.\nfunc Listen() {}\n"

	clean, blocks := StripPrivateAnchors("x.go", []byte(work), []byte(head))
	if string(clean) != head {
		t.Fatalf("the original separator must stay, and only one:\n%s", clean)
	}
	if back, _ := InjectAnchors(clean, blocks); string(back) != work {
		t.Fatalf("round trip differs:\n%s", back)
	}
}

// Two tag groups in one comment, one separator between them: each block takes
// one separator, none is taken twice, and nothing is left behind.
func TestAdjacentTagGroupsShareNoSeparator(t *testing.T) {
	head := "// Creates.\nfunc a() {}\n"
	work := "// Creates.\n//\n// @facet: a/b/c\n// @facet-role: primary\n//\n// @facet: d/e/f\n// @facet-role: related\nfunc a() {}\n"

	clean, blocks := StripPrivateAnchors("x.go", []byte(work), []byte(head))
	if string(clean) != head {
		t.Fatalf("clean differs from HEAD:\n%s", clean)
	}
	if len(blocks) != 2 {
		t.Fatalf("want two blocks, got %d", len(blocks))
	}
	if back, _ := InjectAnchors(clean, blocks); string(back) != work {
		t.Fatalf("round trip differs:\n%s", back)
	}
}

// A committed block is recognised with its separator too, so a project's own
// docblock anchors pass through the filter exactly as they stand.
func TestACommittedDocblockAnchorPassesThroughWithItsSeparator(t *testing.T) {
	head := "<?php\n/**\n * Creates the invoice.\n *\n * @facet: billing/invoice/create\n * @facet-role: primary\n */\nfunction create() {}\n"
	clean, blocks := StripPrivateAnchors("a.php", []byte(head), []byte(head))
	if string(clean) != head || len(blocks) != 0 {
		t.Fatalf("a committed docblock anchor must pass through untouched, got %d blocks:\n%s", len(blocks), clean)
	}
}
