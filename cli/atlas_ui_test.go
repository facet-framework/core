package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/facet-framework/core/internal/atlaslauncher"
)

func seedAtlasCache(t *testing.T, executable string) string {
	t.Helper()
	// atlas.json is found relative to either package layout.
	var data []byte
	var err error
	for _, path := range []string{"../atlaslauncher/atlas.json", "../internal/atlaslauncher/atlas.json"} {
		data, err = os.ReadFile(path)
		if !os.IsNotExist(err) {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	var pin struct {
		Version   string
		Platforms map[string]struct{ SHA256 string }
	}
	if err := json.Unmarshal(data, &pin); err != nil {
		t.Fatal(err)
	}
	asset, ok := pin.Platforms[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		t.Skip("Atlas is unavailable on this platform")
	}
	cache := filepath.Join(t.TempDir(), "cache")
	t.Setenv("FACET_ATLAS_CACHE", cache)
	target := filepath.Join(cache, pin.Version)
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := "facet"
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if err := os.WriteFile(filepath.Join(target, binary), []byte(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(target, ".complete"), asset.SHA256)
	return target
}

func TestAtlasLaunchUsesTheSelectedProjectsCorePin(t *testing.T) {
	skipOnWindows(t)
	const version = "0.1.0-rc.3"
	launcher := atlaslauncher.New(version)
	t.Setenv("FACET_PRODUCTS_DIR", t.TempDir())
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected download", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	t.Setenv("FACET_ATLAS_BASE_URL", server.URL)
	for _, tc := range []struct {
		name, pin string
		wantError bool
	}{
		{"atlas-core", launcher.AtlasCoreVersion(), false},
		{"launcher-core", version, true},
		{"no-pin", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := pinnedProject(t, tc.pin)
			argsFile := filepath.Join(t.TempDir(), "args")
			t.Setenv("ATLAS_TEST_ARGS", argsFile)
			args := []string{"--no-open", "--port", "1", "--root", root}
			if tc.wantError {
				t.Setenv("FACET_ATLAS_CACHE", filepath.Join(t.TempDir(), "cache"))
				args = append([]string{"--yes"}, args...)
			} else {
				seedAtlasCache(t, "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$ATLAS_TEST_ARGS\"\n")
			}
			// A contradictory cwd pin makes dropping --root observable.
			t.Chdir(pinnedProject(t, "9.9.9"))
			execute := func(args ...string) error {
				cmd := NewCommand(Options{Version: version})
				cmd.SetIn(strings.NewReader(""))
				cmd.SetOut(&bytes.Buffer{})
				cmd.SetErr(&bytes.Buffer{})
				cmd.SetArgs(append([]string{"ui"}, args...))
				return cmd.Execute()
			}
			if !tc.wantError {
				// The root preflight must reject a view before the cached script runs.
				err := execute(append(append([]string{}, args...), "--source-view", "missing")...)
				if err == nil || !strings.Contains(err.Error(), "no source_views configuration is declared") {
					t.Fatalf("root preflight did not reject the source view: %v", err)
				}
				if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
					t.Fatalf("Atlas started despite the source-view error: %v", err)
				}
			}
			err := execute(args...)
			if tc.wantError {
				want := fmt.Sprintf("this project requires FACET Core %s; facet ui starts FACET Atlas %s, which contains Core %s; facet ui opens this project once a FACET Atlas release contains Core %s",
					version, launcher.Version(), launcher.AtlasCoreVersion(), version)
				if err == nil || err.Error() != want {
					t.Fatalf("pin mismatch: got %v, want %q", err, want)
				}
				if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
					t.Fatalf("Atlas started despite the pin mismatch: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(argsFile)
				if want := "ui\n" + strings.Join(args, "\n") + "\n"; err != nil || string(got) != want {
					t.Fatalf("Atlas argv: %q, %v, want %q", got, err, want)
				}
			}
			if got := requests.Load(); got != 0 {
				t.Fatalf("unexpected Atlas requests: %d", got)
			}
		})
	}
}
