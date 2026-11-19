package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/facet-framework/core/release"
)

func TestVersionIgnoresBrokenProjectConfiguration(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".facetrc.yaml"), []byte("registries: [broken"), 0600); err != nil {
		t.Fatal(err)
	}
	identity := release.Resolve("dev", "", "", "", "", "")
	for _, asJSON := range []bool{false, true} {
		cmd := NewCommand(Options{Identity: &identity})
		var out bytes.Buffer
		cmd.SetOut(&out)
		args := []string{"--root", root, "version"}
		if asJSON {
			args = append(args, "--json")
		}
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if asJSON {
			var got release.Identity
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Version != identity.Version || got.Distribution != "unidentified" || got.Dirty != nil || got.Compatibility.ProductProtocol != 1 {
				t.Fatalf("%+v", got)
			}
		} else if !strings.Contains(out.String(), identity.Version) || !strings.Contains(out.String(), "source: unknown; dirty: unknown; built: unknown") {
			t.Fatal(out.String())
		}
	}
}

func TestVersionReportsTheStampedBuildTime(t *testing.T) {
	identity := release.Resolve("0.1.0-rc.1+gabcdef123456", "github-o-r", "rc", "abcdef1234567890", "false", "2026-11-14T02:20:30Z")
	for _, asJSON := range []bool{false, true} {
		cmd := NewCommand(Options{Identity: &identity})
		var out bytes.Buffer
		cmd.SetOut(&out)
		args := []string{"--root", t.TempDir(), "version"}
		if asJSON {
			args = append(args, "--json")
		}
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		want := "dirty: false; built: 2026-11-14T02:20:30Z"
		if asJSON {
			want = `"build_time":"2026-11-14T02:20:30Z"`
		}
		if !strings.Contains(out.String(), want) {
			t.Fatalf("json=%v: %q not in\n%s", asJSON, want, out.String())
		}
	}
}
