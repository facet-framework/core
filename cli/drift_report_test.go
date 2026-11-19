package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/facet-framework/core/registry"
)

func TestDriftReportAdaptersPreserveCoreReport(t *testing.T) {
	for _, broken := range []bool{false, true} {
		name := "resolved baseline"
		if broken {
			name = "unresolved baseline"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("FACET_PRODUCTS_DIR", t.TempDir())
			root := driftFixture(t, map[string]string{".facetrc.yaml": "records: docs/facets\n", "README.md": "initial"}, map[string]string{"README.md": "changed"})
			regs, err := driftRegistries(root, "")
			if err != nil {
				t.Fatal(err)
			}
			report, err := facet.ComputeDriftInRepository(root, regs, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if broken {
				report.Baseline.Status = "no-repository"
				report.Baseline.Commit = ""
				report.Baseline.Error = "comparison unavailable"
			}
			for _, adapter := range []struct {
				name  string
				write driftReportWriter
			}{{"core", writeCoreDriftReport}, {"products", writeProductDriftReport}} {
				var out bytes.Buffer
				if err := adapter.write(&out, root, regs, report, true); err != nil {
					t.Fatalf("%s: %v", adapter.name, err)
				}
				var got facet.DriftReport
				if err := json.Unmarshal(out.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				// Compare against a JSON round trip: omitted zero values can
				// normalize nil slices independently of the adapter.
				encoded, err := json.Marshal(report)
				if err != nil {
					t.Fatal(err)
				}
				var want facet.DriftReport
				if err := json.Unmarshal(encoded, &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%s changed Core report: got %+v, want %+v", adapter.name, got, want)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(out.Bytes(), &fields); err != nil {
					t.Fatal(err)
				}
				if adapter.name == "core" {
					// Every top-level field of the Core reporter is a field of
					// the Core report itself; anything else came from a product.
					var own map[string]json.RawMessage
					if err := json.Unmarshal(encoded, &own); err != nil {
						t.Fatal(err)
					}
					for key := range fields {
						if _, ok := own[key]; !ok {
							t.Fatalf("Core reporter emitted product field %q", key)
						}
					}
				}
				if err := adapter.write(driftFailingWriter{}, root, regs, report, true); !errors.Is(err, io.ErrClosedPipe) {
					t.Fatalf("%s lost output error: %v", adapter.name, err)
				}
			}
		})
	}
}

type driftFailingWriter struct{}

func (driftFailingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
