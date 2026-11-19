package release

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/facet-framework/core/pkg/productplugin"
)

func TestDevelopmentIdentity(t *testing.T) {
	got := Resolve("dev", "internal", "", "abcdef1234567890", "true", "2026-11-13T00:00:00Z")
	if got.Version != "0.1.0-dev.1794528000+gabcdef123456.dirty" || got.Channel != "dev" || got.Distribution != "internal" || got.SourceCommit != "abcdef1234567890" || got.Dirty == nil || !*got.Dirty {
		t.Fatalf("%+v", got)
	}
}
func TestBuildTimeIsReportedInUTCOrLeftOut(t *testing.T) {
	got := Resolve("0.1.0-rc.1+gabcdef123456", "github-o-r", "rc", "abcdef1234567890", "false", "2026-11-14T03:20:30+01:00")
	if got.BuildTime != "2026-11-14T02:20:30Z" {
		t.Fatalf("build time = %q, want the stamp in UTC", got.BuildTime)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"build_time":"2026-11-14T02:20:30Z"`) {
		t.Fatalf("the JSON identity does not carry build_time: %s", raw)
	}
	for _, stamp := range []string{"", "yesterday", "2026-11-14"} {
		unknown := Resolve("0.1.0", "", "", "", "", stamp)
		raw, err := json.Marshal(unknown)
		if err != nil {
			t.Fatal(err)
		}
		if unknown.BuildTime != "" || strings.Contains(string(raw), "build_time") {
			t.Fatalf("stamp %q: an unknown build time must stay absent, got %s", stamp, raw)
		}
	}
}

func TestUnknownProvenanceAndExplicitVersion(t *testing.T) {
	for _, version := range []string{"dev", "0.1.0-rc.1", "1.21.0"} {
		got := Resolve(version, "", "", "", "", "")
		want := version
		if version == "dev" {
			want = "0.1.0-dev.unknown"
		}
		if got.Version != want || got.Distribution != "unidentified" || got.Dirty != nil || got.SourceCommit != "" {
			t.Fatalf("%+v", got)
		}
	}
	got := Resolve("0.1.0", "public", "stable", "abc", "false", "")
	if got.Version != "0.1.0" || got.Distribution != "public" || got.Channel != "stable" || got.Dirty == nil || *got.Dirty {
		t.Fatalf("%+v", got)
	}
}
func TestCompatibilityMatchesFormats(t *testing.T) {
	raw, err := os.ReadFile("../schema/facet.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct{ Enum []string }
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	c := Supported()
	if !reflect.DeepEqual(c.RecordSchemaVersions, schema.Properties["schema_version"].Enum) {
		t.Fatalf("schema declarations differ: %v / %v", c.RecordSchemaVersions, schema.Properties["schema_version"].Enum)
	}
	if c.ProductProtocol != productplugin.Protocol {
		t.Fatal("product protocol declaration differs")
	}
}
