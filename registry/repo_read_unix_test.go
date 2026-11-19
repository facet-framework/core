//go:build !windows

// A FIFO exists only on Unix-like systems; the rest of the repository read
// tests are in repo_read_test.go.
// @facet-ignore-file
package facet

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Only regular files are listed: a symlink to a directory and a FIFO that
// carry the listed extension are left out of the listing, rather than listed
// and then refused when read. Mutation: drop the openBeneath check from
// ListRepoFiles → red.
func TestListRepoFilesLeavesOutWhatIsNotARegularFile(t *testing.T) {
	dir := adrDir(t, map[string]string{"0001-title.md": sampleADR})
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "sub"), filepath.Join(dir, "0002-folder.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "0003-fifo.md"), 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	names, err := ListRepoFiles(dir, ".md")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "0001-title.md" {
		t.Errorf("ListRepoFiles = %v, want only the regular file", names)
	}
}
