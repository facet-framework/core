//go:build linux

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// openTestTerminal opens a real pseudo-terminal pair in canonical mode, so
// `plugin add` sees an interactive stdin and asks its questions. Input written
// to master arrives at slave as typed lines.
func openTestTerminal(t *testing.T) (master, slave *os.File) {
	t.Helper()
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("cannot build real terminal fixture: %v", err)
	}
	master = os.NewFile(uintptr(fd), "facet-prompt-master")
	t.Cleanup(func() { master.Close() })
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { slave.Close() })
	term, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	term.Lflag |= unix.ICANON
	term.Cc[unix.VEOF] = 4
	if err := unix.IoctlSetTermios(int(slave.Fd()), unix.TCSETS, term); err != nil {
		t.Fatal(err)
	}
	if !isInteractive(slave) {
		t.Fatal("fixture is not an interactive terminal")
	}
	return master, slave
}

// addOnTerminal types input into a fresh terminal and runs `plugin add source`
// on it against a fresh plugins directory. It returns that directory and the
// command's output.
func addOnTerminal(t *testing.T, source string, input []byte) (dir, output string) {
	t.Helper()
	master, slave := openTestTerminal(t)
	dir = t.TempDir()
	t.Setenv("FACET_PLUGINS_DIR", dir)
	if _, err := master.Write(input); err != nil {
		t.Fatal(err)
	}
	cmd := newPluginAddCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(slave)
	cmd.SetArgs([]string{source})
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("install on a terminal failed: %v: %s", err, out.String())
		}
	case <-time.After(3 * time.Second):
		master.Close()
		slave.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("terminal read did not unblock")
		}
		t.Fatal("the install on a terminal did not finish")
	}
	return dir, out.String()
}

func TestAnInterruptedInteractiveTTYStillInstallsWithoutAGrant(t *testing.T) {
	source := buildPackedPlugin(t, "acme-notes", []string{"notes.read"})
	// First EOF flushes the partial 'j' without a newline; the next EOF ends
	// the read. It must not be mistaken for confirmation.
	dir, output := addOnTerminal(t, source, []byte{'j', 4, 4})
	if !strings.Contains(output, "[y/N]") {
		t.Fatal("the interactive question was never asked")
	}
	if _, err := os.Stat(filepath.Join(dir, "acme-notes", "plugin.json")); err != nil {
		t.Fatalf("plugin not installed after interrupted answer: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "acme-notes", "grants.json")); !os.IsNotExist(err) {
		t.Fatalf("partial terminal answer granted a right: %v", err)
	}
}

// The English grant prompt offers [y/N]. It accepts y/yes and the German
// compatibility aliases j/ja as affirmative answers.
func TestATerminalAnswerGrantsInEitherSpelling(t *testing.T) {
	source := buildPackedPlugin(t, "acme-notes", []string{"notes.read"})
	for _, answer := range []string{"y", "yes", "j", "ja", "n"} {
		dir, output := addOnTerminal(t, source, []byte(answer+"\n"))
		for _, line := range []string{
			"Test Plugin 1.0.0 — from acme-notes (local, unsigned)",
			"  Tools        notes.read",
			"grant? [y/N] ",
		} {
			if !strings.Contains(output, line) {
				t.Fatalf("answer %q: output lacks %q:\n%s", answer, line, output)
			}
		}
		raw, err := os.ReadFile(filepath.Join(dir, "acme-notes", "grants.json"))
		if answer == "n" {
			if !os.IsNotExist(err) {
				t.Fatalf("answer %q granted a right: %v %s", answer, err, raw)
			}
			continue
		}
		if err != nil {
			t.Fatalf("answer %q granted nothing: %v\n%s", answer, err, output)
		}
		var grant struct {
			Tools []string `json:"tools"`
		}
		if err := json.Unmarshal(raw, &grant); err != nil || !slices.Equal(grant.Tools, []string{"notes.read"}) {
			t.Fatalf("answer %q: grants.json = %s (%v)", answer, raw, err)
		}
	}
}
