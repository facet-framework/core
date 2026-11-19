//go:build !windows

// The reuse check compares the inode of the development build, which only
// Unix-like systems report; the other development-build cases are in
// dispatcher_devbuild_test.go.
// @facet-ignore-file
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A clean tree at the same commit is not rebuilt; a dirty one is.
func TestADevelopmentBuildIsReusedOnlyForTheSameCleanCommit(t *testing.T) {
	root := devFixture(t, nil)
	data := t.TempDir()
	stamp := func() (uint64, int64, string) {
		t.Helper()
		st, err := os.Stat(filepath.Join(devDir(data), "facet"))
		if err != nil {
			t.Fatalf("no development build: %v", err)
		}
		meta, err := os.ReadFile(filepath.Join(devDir(data), "install.json"))
		if err != nil {
			t.Fatal(err)
		}
		return st.Sys().(*syscall.Stat_t).Ino, st.ModTime().UnixNano(), string(meta)
	}

	if out, err := runDevBuild(t, root, data); err != nil || !strings.Contains(out, "fixture-facet-ran") {
		t.Fatalf("first build did not run (err %v):\n%s", err, out)
	}
	ino1, mod1, meta1 := stamp()
	if !strings.Contains(meta1, `"dirty": false`) {
		t.Fatalf("the fixture tree is not clean, so reuse cannot be measured:\n%s", meta1)
	}

	if out, err := runDevBuild(t, root, data); err != nil || !strings.Contains(out, "fixture-facet-ran") {
		t.Fatalf("second run failed (err %v):\n%s", err, out)
	}
	ino2, mod2, meta2 := stamp()
	if ino1 != ino2 || mod1 != mod2 || meta1 != meta2 {
		t.Errorf("a clean tree at the same commit was rebuilt (inode %d→%d, mtime %d→%d)", ino1, ino2, mod1, mod2)
	}

	f, err := os.OpenFile(filepath.Join(root, "cmd", "facet", "main.go"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n// edited\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if out, err := runDevBuild(t, root, data); err != nil || !strings.Contains(out, "fixture-facet-ran") {
		t.Fatalf("run on a dirty tree failed (err %v):\n%s", err, out)
	}
	ino3, _, meta3 := stamp()
	if ino3 == ino2 || !strings.Contains(meta3, `"dirty": true`) {
		t.Errorf("a dirty tree was not rebuilt (inode %d→%d):\n%s", ino2, ino3, meta3)
	}
}
