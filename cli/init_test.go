package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/facet-framework/core/cli/scaffold"
	"github.com/facet-framework/core/release"
)

func runInit(t *testing.T, identity release.Identity, args ...string) (int, string, string) {
	t.Helper()
	cmd := NewCommand(Options{Identity: &identity})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(append([]string{"init"}, args...))
	var code int
	stderr := captureStream(t, &os.Stderr, func() { code = Run(cmd) })
	return code, out.String(), stderr
}

func dispatcherPin(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "tools", "facet-lint", "facet"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, `FACET_VERSION="${FACET_VERSION:-`) {
			return line
		}
	}
	t.Fatal("the scaffolded dispatcher has no FACET_VERSION default")
	return ""
}

func TestInitScaffoldsTheNamedDirectoryAndARunAgainKeepsEverything(t *testing.T) {
	dir := t.TempDir()
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal("fixture: the target is not empty")
	}
	identity := release.Resolve("dev", "", "", "", "", "")
	code, out, stderr := runInit(t, identity, dir)
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, stderr)
	}
	files, err := scaffold.Payload()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f.Path))); err != nil {
			t.Errorf("%s was not written", f.Path)
		}
	}
	code, out, _ = runInit(t, identity, dir)
	if code != 0 || !strings.Contains(out, "0 file(s) written") {
		t.Fatalf("second run: exit %d\n%s", code, out)
	}
}

func TestADevelopmentBuildKeepsThePayloadsPinAndAReleaseBuildPinsItself(t *testing.T) {
	var payloadPin string
	files, err := scaffold.Payload()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Path == scaffold.DispatcherCopy {
			for _, line := range strings.Split(string(f.Data), "\n") {
				if strings.HasPrefix(line, `FACET_VERSION="${FACET_VERSION:-`) {
					payloadPin = line
				}
			}
		}
	}
	if payloadPin == "" {
		t.Fatal("fixture: the payload's dispatcher carries no pin")
	}

	dev := t.TempDir()
	if code, out, stderr := runInit(t, release.Resolve("dev", "", "", "", "", ""), dev); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, stderr)
	}
	if got := dispatcherPin(t, dev); got != payloadPin {
		t.Errorf("a development build pinned %s, want the payload's %s", got, payloadPin)
	}

	for _, channel := range []string{"rc", "stable"} {
		rel := t.TempDir()
		version := "0.4.2-rc.3"
		if channel == "stable" {
			version = "0.4.2"
		}
		identity := release.Resolve(version+"+gabcdef123456", "github-facet-framework-core", channel, "", "", "")
		if code, out, stderr := runInit(t, identity, rel); code != 0 {
			t.Fatalf("exit %d\n%s%s", code, out, stderr)
		}
		if got, want := dispatcherPin(t, rel), `FACET_VERSION="${FACET_VERSION:-`+version+`}"`; got != want {
			t.Errorf("a %s build pinned %s, want %s", channel, got, want)
		}
	}
}

func TestInitRefusesATargetThatIsNotADirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(file, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := runInit(t, release.Resolve("dev", "", "", "", "", ""), file)
	if code == 0 {
		t.Fatalf("exit 0 for a file target\n%s", out)
	}
	if !strings.Contains(stderr, "facet: init: "+file+" is not a directory") {
		t.Fatalf("stderr does not name the reason: %q", stderr)
	}
}

func TestInitRefusesAnUnknownOptionWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	code, _, stderr := runInit(t, release.Resolve("dev", "", "", "", "", ""), "--dry", dir)
	if code == 0 || !strings.Contains(stderr, "unknown flag") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal("an unknown option still wrote into the target")
	}
}

func TestInitDryRunFlagWritesNothing(t *testing.T) {
	dir := t.TempDir()
	code, out, _ := runInit(t, release.Resolve("dev", "", "", "", "", ""), "-n", dir)
	if code != 0 || !strings.Contains(out, "[dry-run] write       .facetignore") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal("the dry run wrote into the target")
	}
}
