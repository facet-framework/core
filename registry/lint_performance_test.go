// @facet-ignore-file
package facet

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// BenchmarkLintAllLargeRegistry measures a synthetic registry with fixed record,
// anchor and source counts. It does not read another project's records.
func BenchmarkLintAllLargeRegistry(b *testing.B) {
	root := b.TempDir()
	template, err := os.ReadFile(PackageTestPath(b, "testdata", "representative-record.yaml"))
	if err != nil {
		b.Fatal(err)
	}
	put := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			b.Fatal(err)
		}
	}
	for i := 0; i < 912; i++ {
		id := fmt.Sprintf("sample/source/feature-%04d", i)
		body := strings.ReplaceAll(string(template), "sample/source/item", id)
		put("docs/facets/features/"+id+".yaml", body)
	}
	for i := 0; i < 5639; i++ {
		body := "package source\n" + strings.Repeat("// representative source line\n", 99)
		if i < 3359 {
			body = fmt.Sprintf("// @facet: sample/source/feature-%04d\n// @facet-role: primary\n", i%912) + body
		}
		put(fmt.Sprintf("src/file-%04d.go", i), body)
	}
	entries, err := os.ReadDir(SourceTestPath(b, "schema"))
	if err != nil {
		b.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(SourceTestPath(b, "schema", e.Name()))
		if err != nil {
			b.Fatal(err)
		}
		put("schema/"+e.Name(), string(data))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rep, err := LintAll(root, LintOptions{Today: "2026-10-06"})
		if err != nil {
			b.Fatal(err)
		}
		if rep.Checked.Records != 912 || rep.Checked.Anchors != 3359 {
			b.Fatalf("fixture counts: %+v", rep.Checked)
		}
	}
}

// Mutation: cache a successful category load with nil records; repeated
// checks then lose the policy dependency finding and the record count.
func TestLintSnapshotPreservesCategoryFindings(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(fmt.Sprint(broken), func(t *testing.T) {
			root := project(t, "")
			record(t, root, "docs/facets", "sample/source/item", "planned")
			writeFile(t, filepath.Join(root, "docs/facets/policies/rules.yaml"), "id: sample/policy/rule\nlineage:\n  depends_on: [absent/source/item]\n")
			if broken {
				writeFile(t, filepath.Join(root, "docs/facets/assumptions/broken.yaml"), "[: invalid")
			}
			writeFile(t, filepath.Join(root, "schema/facet.schema.json"), `{"type":"object","required":["intent"]}`)
			writeFile(t, filepath.Join(root, "schema/policy.schema.json"), `{"type":"object","required":["contract"]}`)
			opts := LintOptions{Today: "2026-10-06"}
			reg := Open(root)
			want, wantErr := lint(reg, opts)
			got, gotErr := Lint(reg, opts)
			if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || !reflect.DeepEqual(got, want) {
				t.Fatalf("snapshot changed report: got %+v (%v), want %+v (%v)", got, gotErr, want, wantErr)
			}
			if got.Checked.Records != 2 || len(findings(got, "dependencies")) != 1 || len(findings(got, "schema")) != 2 {
				t.Fatalf("fixture prerequisites not observed: %+v", got)
			}
		})
	}
}

// Mutation: reuse a category snapshot across LintAll invocations; the second
// report keeps the deleted record and misses the new dependency finding.
func TestLintInvocationsObserveRecordChanges(t *testing.T) {
	root := project(t, "")
	record(t, root, "docs/facets", "sample/source/item", "planned")
	opts := LintOptions{Skip: map[string]bool{"schema": true}, Today: "2026-10-06"}
	first, err := LintAll(root, opts)
	if err != nil || first.Checked.Records != 1 {
		t.Fatalf("first record was not checked: %+v, %v", first, err)
	}
	if err := os.Remove(Open(root).PathForID("sample/source/item")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "docs/facets/policies/rules.yaml"), "id: sample/policy/rule\nlineage:\n  depends_on: [sample/source/item]\n")
	second, err := LintAll(root, opts)
	if err != nil || second.Checked.Records != 1 || len(findings(second, "dependencies")) != 1 {
		t.Fatalf("second invocation missed disk changes: %+v, %v", second, err)
	}
}
