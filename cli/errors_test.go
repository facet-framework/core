// Fixtures below anchor features that exist only in a temporary directory,
// so this file opts itself out of anchor scanning: @facet-ignore-file

package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/facet-framework/core/registry"
)

// runErrors executes the real 'facet errors' command through the same cobra
// wiring the binary uses, and returns what it wrote to stdout.
func runErrors(t *testing.T, args ...string) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	root := newRootCmd()
	root.SetArgs(append([]string{"errors"}, args...))
	runErr := root.Execute()

	w.Close()
	os.Stdout = old
	out, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if runErr != nil {
		t.Fatalf("facet errors: %v\n%s", runErr, out)
	}
	return string(out)
}

func errorsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "docs/facets/features/platform/keys/rotate.yaml"), `schema_version: "0.1"
id: platform/keys/rotate
lifecycle: active
intent:
  summary: |
    Rotates encryption keys on a schedule; fails with E1001 when the key
    store is locked and with E1003 when the schedule is malformed.
contract:
  scenarios:
    - id: locked-store
      statement: |
        Given a locked key store
        When rotation is attempted
        Then it fails with E1001
`)
	return root
}

func TestErrorsJSONShape(t *testing.T) {
	root := errorsFixture(t)

	out := runErrors(t, "--json", "--root", root)

	var register facet.ErrorCodeRegister
	if err := json.Unmarshal([]byte(out), &register); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	if len(register.Codes) != 2 {
		t.Fatalf("want 2 codes, got %+v", register.Codes)
	}
	byCode := map[string]facet.ErrorCode{}
	for _, ec := range register.Codes {
		byCode[ec.Code] = ec
	}
	e1001, ok := byCode["E1001"]
	if !ok {
		t.Fatalf("want E1001 found, got %+v", register.Codes)
	}
	if len(e1001.Mentions) != 2 {
		t.Fatalf("E1001 appears in both intent.summary and the scenario — want 2 mentions, got %+v", e1001.Mentions)
	}
	for _, m := range e1001.Mentions {
		if m.Feature != "platform/keys/rotate" {
			t.Errorf("mention lost its record: %+v", m)
		}
		if m.Path != filepath.ToSlash(filepath.Join("docs/facets/features/platform/keys/rotate.yaml")) {
			t.Errorf("mention path must be repository-relative, got %q", m.Path)
		}
	}

	if len(register.Bands) != 1 || register.Bands[0].Band != "E1" {
		t.Fatalf("want one E1 band, got %+v", register.Bands)
	}
	b := register.Bands[0]
	if len(b.Gaps) != 1 || b.Gaps[0] != "E1002" {
		t.Errorf("want the gap E1002 between E1001 and E1003, got %+v", b.Gaps)
	}
	if b.Next != "E1004" {
		t.Errorf("next = %q, want E1004", b.Next)
	}
}

func TestErrorsHumanReadableOutput(t *testing.T) {
	root := errorsFixture(t)

	out := runErrors(t, "--root", root)

	if !strings.Contains(out, "E1001") || !strings.Contains(out, "E1003") {
		t.Errorf("output must name every code found, got %q", out)
	}
	if !strings.Contains(out, "platform/keys/rotate") {
		t.Errorf("output must name the record that mentions each code, got %q", out)
	}
	if !strings.Contains(out, "gaps: E1002") {
		t.Errorf("output must show the band's gap, got %q", out)
	}
	if !strings.Contains(out, "next free: E1004") {
		t.Errorf("output must show the band's next free code, got %q", out)
	}
	if !strings.Contains(out, "2 identifier(s) found across 1 band(s)") {
		t.Errorf("want a summary count, got %q", out)
	}
}

func TestErrorsUsesTheConfiguredPattern(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".facetrc.yaml"), "schema_version: \"0.1\"\nerror_codes:\n  pattern: 'ERR-\\d{3}'\n")
	write(t, filepath.Join(root, "docs/facets/features/shop/billing/webhook.yaml"), `schema_version: "0.1"
id: shop/billing/webhook
lifecycle: active
intent:
  summary: |
    Verifies a webhook signature; fails with ERR-042 on mismatch. E9999 is not
    this project's configured pattern and must not appear.
`)

	out := runErrors(t, "--json", "--root", root)

	var register facet.ErrorCodeRegister
	if err := json.Unmarshal([]byte(out), &register); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	if len(register.Codes) != 1 || register.Codes[0].Code != "ERR-042" {
		t.Fatalf("want only ERR-042 found under the configured pattern, got %+v", register.Codes)
	}
}

func TestErrorsWithNoIdentifiersIsQuiet(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "docs/facets/features/platform/keys/rotate.yaml"), `schema_version: "0.1"
id: platform/keys/rotate
lifecycle: active
intent:
  summary: |
    Rotates encryption keys on a schedule, quietly.
`)

	out := runErrors(t, "--root", root)
	if !strings.Contains(out, "No identifiers found.") {
		t.Errorf("want the quiet message, got %q", out)
	}
}
