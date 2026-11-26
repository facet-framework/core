//go:build linux

// Fixtures below contain example @facet: tags; this file demonstrates the
// syntax rather than implementing a feature.
// @facet-ignore-file

package facet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// The shared-claims pass reads declarations in Go sources only, so it opens no
// other source again: not a small one, not one too large for the scanner to
// read whole, and not for another registry's scan of the same files. The
// kernel reports every open and read of the watched files, so the test
// counts what happened rather than what the code meant to do.
func TestNonGoSourcesAreNotOpenedAgain(t *testing.T) {
	root := t.TempDir()
	head := "// @facet: a/b/one\n// @facet: a/b/two\n"
	var paths []string
	var anchors []Anchor
	for _, ext := range []string{".js", ".mjs", ".tsx", ".cjs", ".py"} {
		for _, large := range []bool{false, true} {
			name := "small" + ext
			body := head
			if large {
				name = "large" + ext
				body += strings.Repeat("// filler\n", 240000)
			}
			if ext == ".py" {
				body = strings.ReplaceAll(body, "//", "#")
			}
			path := filepath.Join(root, "src", name)
			writeFile(t, path, body)
			found, err := ScanFile(path)
			if err != nil || len(found) != 2 {
				t.Fatalf("premise: the scanner reads both anchors of %s, got %+v %v", name, found, err)
			}
			if info, _ := os.Stat(path); large != (info.Size() > scanWholeFileLimit) {
				t.Fatalf("premise: %s is large %v, size %d", name, large, info.Size())
			}
			paths = append(paths, path)
			anchors = append(anchors, found...)
		}
	}

	fd, err := syscall.InotifyInit1(syscall.IN_NONBLOCK | syscall.IN_CLOEXEC)
	if err != nil {
		t.Skipf("inotify unavailable: %v", err)
	}
	defer syscall.Close(fd)
	watched := map[int32]string{}
	for _, path := range paths {
		wd, err := syscall.InotifyAddWatch(fd, path, syscall.IN_OPEN|syscall.IN_ACCESS)
		if err != nil {
			t.Fatal(err)
		}
		watched[int32(wd)] = filepath.Base(path)
	}
	events := func() map[string]int {
		seen := map[string]int{}
		buf := make([]byte, 64*1024)
		for {
			n, err := syscall.Read(fd, buf)
			if errors.Is(err, syscall.EAGAIN) || n <= 0 {
				return seen
			}
			if err != nil {
				t.Fatal(err)
			}
			for off := 0; off+syscall.SizeofInotifyEvent <= n; {
				ev := (*syscall.InotifyEvent)(unsafe.Pointer(&buf[off]))
				seen[watched[ev.Wd]]++
				off += syscall.SizeofInotifyEvent + int(ev.Len)
			}
		}
	}
	// The watch itself is live: reading a file is seen.
	if _, err := os.ReadFile(paths[0]); err != nil {
		t.Fatal(err)
	}
	if got := events(); got[filepath.Base(paths[0])] == 0 {
		t.Fatalf("premise: the watch reports a read, got %v", got)
	}

	for name, inputs := range map[string][]SharedClaimsInput{
		"one registry": {{Registry: Registry{Name: "aa", Root: root}, Anchors: anchors}},
		"a second registry over the same sources": {
			{Registry: Registry{Name: "aa", Root: root}, Anchors: anchors},
			{Registry: Registry{Name: "bb", Root: root}, Foreign: true},
		},
	} {
		claims := ComputeSharedClaims(inputs)
		if got := events(); len(got) != 0 {
			t.Errorf("%s: the pass opened non-Go sources again: %v", name, got)
		}
		if len(claims.Symbols)+len(claims.Primaries)+len(claims.Incomplete) != 0 {
			t.Errorf("%s: non-Go sources were measured: %+v", name, claims)
		}
	}
}
