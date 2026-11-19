// @facet-ignore-file

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const internalTagVersion = "0.1.0-dev.20261114.7"
const internalTag = "internal/core/" + internalTagVersion

// Stubs that log every argv to $FACET_TEST_LOG, one invocation per line.
// curl serves a fixture only when the URL is exactly the browser download URL
// under FACET_TEST_SERVE_PREFIX; everything else fails. gh always fails.
func recordingShims(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("uname", `#!/bin/sh
case "$1" in
  -m) echo x86_64 ;;
  *)  echo Linux ;;
esac
`)
	write("curl", `#!/bin/sh
printf 'curl' >> "$FACET_TEST_LOG"
for a in "$@"; do printf ' [%s]' "$a" >> "$FACET_TEST_LOG"; done
printf '\n' >> "$FACET_TEST_LOG"
out=
url=
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -H) shift 2 ;;
    -*) shift ;;
    *)  url="$1"; shift ;;
  esac
done
[ -n "$FACET_TEST_SERVE_PREFIX" ] || exit 22
case "$url" in
  "$FACET_TEST_SERVE_PREFIX"*) ;;
  *) exit 22 ;;
esac
src="$FACET_TEST_FIXTURES/${url#"$FACET_TEST_SERVE_PREFIX"}"
[ -f "$src" ] || exit 22
cp "$src" "$out"
`)
	write("gh", `#!/bin/sh
printf 'gh' >> "$FACET_TEST_LOG"
for a in "$@"; do printf ' [%s]' "$a" >> "$FACET_TEST_LOG"; done
printf '\n' >> "$FACET_TEST_LOG"
exit 1
`)
	return dir
}

func runInternalTag(t *testing.T, shims, cache, fixtures, log, servePrefix string) (string, error) {
	t.Helper()
	dispatcher, err := filepath.Abs(filepath.Join(cliModuleRoot(t), "bin/facet"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", dispatcher, "--version")
	cmd.Env = append(cmd.Environ(),
		"CLAUDE_PLUGIN_DATA="+cache,
		"FACET_VERSION="+internalTagVersion,
		"FACET_RELEASE_TAG="+internalTag,
		"FACET_REPOS=Owner-Org/Core.Repo",
		"FACET_REPO=", "FACET_BINARY=", "FACET_DEV_BUILD=", "GITHUB_TOKEN=",
		// A token, so the API path is reached once curl and gh have failed.
		"GH_TOKEN=test-token",
		"FACET_TEST_LOG="+log,
		"FACET_TEST_FIXTURES="+fixtures,
		"FACET_TEST_SERVE_PREFIX="+servePrefix,
		"PATH="+shims+":"+sandboxPath(t),
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestAnInternalTagWithSlashesReachesEveryDownloadPathUnescaped(t *testing.T) {
	skipUnlessPOSIX(t)
	shims := recordingShims(t)
	log := filepath.Join(t.TempDir(), "argv.log")
	out, err := runInternalTag(t, shims, t.TempDir(), t.TempDir(), log, "")
	if err == nil {
		t.Fatalf("the dispatcher succeeded with no release on offer:\n%s", out)
	}
	raw, readErr := os.ReadFile(log)
	if readErr != nil {
		t.Fatalf("no stub was called, so nothing below measures the URLs: %v\n%s", readErr, out)
	}
	calls := string(raw)
	archive := "facet_" + internalTagVersion + "_linux_amd64.tar.gz"
	for _, want := range []string{
		"curl [-fsL] [https://github.com/Owner-Org/Core.Repo/releases/download/" + internalTag + "/" + archive + "]",
		"gh [release] [download] [" + internalTag + "] [--repo] [Owner-Org/Core.Repo] [--pattern] [" + archive + "]",
		"curl [-fsL] [-H] [Authorization: Bearer test-token] [-H] [Accept: application/vnd.github+json] [https://api.github.com/repos/Owner-Org/Core.Repo/releases/tags/" + internalTag + "]",
	} {
		if !strings.Contains(calls, want) {
			t.Errorf("no call starting with\n  %s\nrecorded calls:\n%s", want, calls)
		}
	}
	// Nothing was percent-encoded or cut at the first slash.
	if strings.Contains(calls, "%2F") || strings.Contains(calls, "/download/internal/"+archive) {
		t.Errorf("the tag reached a URL encoded or truncated:\n%s", calls)
	}
	if !strings.Contains(out, "release tag "+internalTag) {
		t.Errorf("the failure does not name the tag it looked for:\n%s", out)
	}
}

func TestAnInternalTagInstallsUnderTheRepositorysDistribution(t *testing.T) {
	skipUnlessPOSIX(t)
	shims := recordingShims(t)
	fixtures := t.TempDir()
	cache := t.TempDir()
	log := filepath.Join(t.TempDir(), "argv.log")
	archive := "facet_" + internalTagVersion + "_linux_amd64.tar.gz"
	body := makeTarGz(t, "facet")
	writeFixture(t, filepath.Join(fixtures, archive), body)
	writeFixture(t, filepath.Join(fixtures, "checksums.txt"), []byte(fmt.Sprintf("%s  %s\n", sha256Hex(body), archive)))
	prefix := "https://github.com/Owner-Org/Core.Repo/releases/download/" + internalTag + "/"

	out, err := runInternalTag(t, shims, cache, fixtures, log, prefix)
	if err != nil {
		t.Fatalf("the dispatcher could not install from the internal tag: %v\n%s", err, out)
	}
	if !strings.Contains(out, archiveStubMarker) {
		t.Errorf("the installed binary did not run:\n%s", out)
	}
	dir := filepath.Join(cache, "dist", "core", "github-owner-org-core.repo", "linux_amd64", internalTagVersion)
	raw, err := os.ReadFile(filepath.Join(dir, "install.json"))
	if err != nil {
		t.Fatalf("no install under the repository's distribution: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("install.json is not JSON: %v\n%s", err, raw)
	}
	for key, want := range map[string]string{
		"distribution": "github-owner-org-core.repo",
		"version":      internalTagVersion,
		"release_tag":  internalTag,
		"source_repo":  "Owner-Org/Core.Repo",
	} {
		if meta[key] != want {
			t.Errorf("install.json %s = %v, want %s", key, meta[key], want)
		}
	}
}
