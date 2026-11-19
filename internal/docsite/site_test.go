// @facet-ignore-file

package docsite

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) (string, Config) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "docs/facets/features/demo/start")
	if e := os.MkdirAll(dir, 0755); e != nil {
		t.Fatal(e)
	}
	put(t, filepath.Join(dir, "first.yaml"), `schema_version: "0.1"
id: demo/start/first
lifecycle: active
intent:
  summary: Open the workspace to begin.
knowledge:
  prerequisite_knowledge:
    question: What do I need?
    answer: A workspace.
  discoverability:
    question: Where do I start?
    answer: Open Settings.
  success_signal:
    question: How do I know it worked?
    answer: The workspace name appears.
contract:
  scenarios:
    - id: workspace-visible
      statement: Given a workspace, When opened, Then its name appears.
lineage:
  owner:
    team: docs
  created_at: "2026-10-29"
`)
	put(t, filepath.Join(root, "app.go"), "// @facet: demo/start/first\n// @facet-role: primary\npackage app\n")
	c := Config{Visibility: "internal", Version: 1, Title: "Product guide", Description: "Learn your next task.", Audiences: []Audience{{ID: "newcomer", Title: "New colleagues", Kind: "creator", Description: "Start here", Features: []string{"demo/start/first"}}}, Articles: []Article{}}
	inv, err := Collect(root)
	if err != nil {
		t.Fatal("invalid fixture inventory", err)
	}
	if _, err := MakePlan(inv, c); err != nil {
		t.Fatal("invalid fixture plan", err)
	}
	return root, c
}
func put(t *testing.T, path, body string) {
	t.Helper()
	if e := os.WriteFile(path, []byte(body), 0644); e != nil {
		t.Fatal(e)
	}
}
func inventory(t *testing.T, root string) Inventory {
	t.Helper()
	v, e := Collect(root)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func composed(t *testing.T, root string, c Config) Config {
	t.Helper()
	v, e := Compose(inventory(t, root), c)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := MakePlan(inventory(t, root), v); e != nil {
		t.Fatal("composed control invalid", e)
	}
	return v
}
func build(t *testing.T, root string, c Config) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "site")
	if e := Build(root, out, c); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(read(t, filepath.Join(out, "index.html")), "<h1>") {
		t.Fatal("baseline page missing")
	}
	if e := Check(root, out, c); e != nil {
		t.Fatal("baseline freshness failed", e)
	}
	return out
}
func read(t *testing.T, path string) string {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func TestInventoryEvidence(t *testing.T) {
	r, _ := fixture(t)
	v := inventory(t, r)
	if len(v.Records) != 1 || len(v.Records[0].Sources) != 2 || len(v.Records[0].Anchors) != 1 {
		t.Fatalf("missing evidence: %+v", v)
	}
	rec := v.Records[0]
	if rec.ID != "demo/start/first" || rec.Lifecycle != "active" || rec.Category != "features" {
		t.Fatal(rec)
	}
	if rec.Anchors[0].ID != rec.ID || rec.Anchors[0].Path != "app.go" || rec.Anchors[0].Role != "primary" {
		t.Fatal(rec.Anchors)
	}
	if rec.Sources[0].Path != "app.go" || rec.Sources[1].Path != "docs/facets/features/demo/start/first.yaml" || len(rec.Sources[0].Digest) != 64 {
		t.Fatal(rec.Sources)
	}

	for _, source := range rec.Sources {
		expected := sha256.Sum256([]byte(read(t, filepath.Join(r, source.Path))))
		if source.Digest != hex.EncodeToString(expected[:]) {
			t.Fatalf("source %s: digest %s does not match fixture content", source.Path, source.Digest)
		}
	}
}
func TestKnowledgeGaps(t *testing.T) {
	r, _ := fixture(t)
	v := inventory(t, r)
	if strings.Join(v.Records[0].Gaps, ",") != "not_for,common_pitfalls" {
		t.Fatal(v.Records[0].Gaps)
	}
}
func TestDeterministicInventory(t *testing.T) {
	r, _ := fixture(t)
	path := filepath.Join(r, "docs/facets/features/demo/start/first.yaml")
	for filename, id := range map[string]string{"a.yaml": "third", "z.yaml": "second"} {
		put(t, filepath.Join(filepath.Dir(path), filename), strings.Replace(read(t, path), "demo/start/first", "demo/start/"+id, 1))
	}
	v := inventory(t, r)
	if len(v.Records) != 3 || v.Records[0].ID != "demo/start/first" || v.Records[1].ID != "demo/start/second" || v.Records[2].ID != "demo/start/third" {
		t.Fatal(v)
	}
	a, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(inventory(t, r))
	if e != nil {
		t.Fatal(e)
	}
	if string(a) != string(b) {
		t.Fatal("inventory changed")
	}
}

func TestUnknownAudienceRecord(t *testing.T) {
	r, c := fixture(t)
	c.Audiences[0].Features = []string{"missing"}
	if _, e := MakePlan(inventory(t, r), c); e == nil || !strings.Contains(e.Error(), "unknown record missing") {
		t.Fatal("accepted unknown record")
	}
}
func TestDuplicateAudience(t *testing.T) {
	r, c := fixture(t)
	c.Audiences = append(c.Audiences, c.Audiences[0])
	if _, e := MakePlan(inventory(t, r), c); e == nil || !strings.Contains(e.Error(), "invalid or duplicate audience") {
		t.Fatal("accepted duplicate")
	}
}
func TestUnsafeAudienceID(t *testing.T) {
	r, c := fixture(t)
	c.Audiences[0].ID = "../escape"
	if _, e := MakePlan(inventory(t, r), c); e == nil || !strings.Contains(e.Error(), "invalid or duplicate audience") {
		t.Fatal("accepted traversal")
	}
}
func TestAudienceKinds(t *testing.T) {
	r, c := fixture(t)
	c.Audiences[0].Kind = "premium"
	if _, e := MakePlan(inventory(t, r), c); e == nil || !strings.Contains(e.Error(), "invalid or duplicate audience") {
		t.Fatal("accepted unknown audience kind")
	}
}
func TestPlanUnassigned(t *testing.T) {
	r, c := fixture(t)
	c.Audiences[0].Features = nil
	p, e := MakePlan(inventory(t, r), c)
	if e != nil || len(p.Unassigned) != 1 || p.Unassigned[0] != "demo/start/first" || len(p.Clusters) != 0 {
		t.Fatal(p, e)
	}
}
func TestPlanClusters(t *testing.T) {
	r, c := fixture(t)
	p, e := MakePlan(inventory(t, r), c)
	if e != nil || len(p.Clusters) != 1 || p.Clusters[0].Topic != "demo/start" || p.Clusters[0].Audience != "newcomer" || strings.Join(p.Clusters[0].Features, ",") != "demo/start/first" {
		t.Fatal(p, e)
	}
}
func TestComposeGrounded(t *testing.T) {
	r, c := fixture(t)
	c = composed(t, r, c)
	if len(c.Articles) != 2 || len(c.Articles[0].Sections) != 4 {
		t.Fatal(c.Articles)
	}
	for _, s := range c.Articles[0].Sections {
		if len(s.Sources) != 1 || s.Sources[0] != "demo/start/first" {
			t.Fatal("uncited")
		}
		if s.Title == "Avoid common mistakes" {
			t.Fatal("invented missing answer")
		}
	}
	want := []string{"Open the workspace to begin.", "A workspace.", "Open Settings.", "The workspace name appears."}
	for i, section := range c.Articles[0].Sections {
		if strings.TrimSpace(section.Body) != want[i] {
			t.Fatal("ungrounded body", section.Body)
		}
	}
	if c.Articles[0].Kind != "feature" || c.Articles[1].Kind != "faq" || len(c.Articles[1].Sections) != 3 || c.Articles[1].Sections[0].Title != "What do I need?" {
		t.Fatal(c.Articles)
	}

}
func TestComposePreservesEditorial(t *testing.T) {
	r, c := fixture(t)
	c = composed(t, r, c)
	if _, e := Compose(inventory(t, r), c); e == nil || !strings.Contains(e.Error(), "requires an empty articles list") {
		t.Fatal("overwrites articles")
	}
}
func TestComposeSkipsPlanned(t *testing.T) {
	r, c := fixture(t)
	if len(composed(t, r, c).Articles) == 0 {
		t.Fatal("active control has no articles")
	}
	p := filepath.Join(r, "docs/facets/features/demo/start/first.yaml")
	put(t, p, strings.Replace(read(t, p), "lifecycle: active", "lifecycle: planned", 1))
	c = composed(t, r, c)
	if len(c.Articles) != 0 {
		t.Fatal("planned advertised as active")
	}
}
func TestUncitedSection(t *testing.T) {
	r, c := fixture(t)
	c = composed(t, r, c)
	c.Articles[0].Sections[0].Sources = nil
	if _, e := MakePlan(inventory(t, r), c); e == nil || !strings.Contains(e.Error(), "section needs title, prose and sources") {
		t.Fatal("accepted uncited prose")
	}
}
func TestCrossAudienceArticle(t *testing.T) {
	r, c := fixture(t)
	c = composed(t, r, c)
	c.Audiences[0].Features = nil
	if _, e := MakePlan(inventory(t, r), c); e == nil || !strings.Contains(e.Error(), "outside audience") {
		t.Fatal("accepted cross audience evidence")
	}
}
func TestBuildStandalone(t *testing.T) {
	r, c := fixture(t)
	c = composed(t, r, c)
	out := build(t, r, c)
	for _, p := range []string{"index.html", "audience-newcomer.html", "features.html", "sources.html", "style.css", "search.js", "theme.js", "search-index.js", "inventory.json", "plan.json", "search.json"} {
		if _, e := os.Stat(filepath.Join(out, p)); e != nil {
			t.Fatal(e)
		}
	}
	if !strings.Contains(read(t, filepath.Join(out, "index.html")), "features.html") {
		t.Fatal("missing complete feature catalog link")
	}
	if !strings.Contains(read(t, filepath.Join(out, "index.html")), "audience-newcomer.html") {
		t.Fatal("legacy audience page unreachable")
	}
}
func TestBuildExistingDestination(t *testing.T) {
	r, c := fixture(t)
	out := t.TempDir()
	put(t, filepath.Join(out, "keep"), "precious")
	if e := Build(r, out, c); e == nil || !strings.Contains(e.Error(), "destination exists") {
		t.Fatal("overwrites destination")
	}
	if read(t, filepath.Join(out, "keep")) != "precious" {
		t.Fatal("lost file")
	}
}
func TestEscapedContent(t *testing.T) {
	r, c := fixture(t)
	c.Title = "<script>alert(1)</script>"
	out := build(t, r, c)
	s := read(t, filepath.Join(out, "index.html"))
	if strings.Contains(s, "<script>alert(1)</script>") || !strings.Contains(s, "&lt;script&gt;") {
		t.Fatal("unsafe HTML")
	}
}
func TestDiagramAccessibility(t *testing.T) {
	r, c := fixture(t)
	c = composed(t, r, c)
	c.Articles[0].Diagram = &Diagram{Title: "Product relationship", Description: "Records provide source evidence.", Sources: []string{"demo/start/first"}, Nodes: []Node{{ID: "record", Label: "Record", X: 8, Y: 8}, {ID: "evidence", Label: "Evidence", X: 8, Y: 108}}, Edges: []Edge{{From: "record", To: "evidence", Label: "provides"}}}
	out := build(t, r, c)
	s := read(t, filepath.Join(out, "article-"+c.Articles[0].ID+".html"))
	for _, want := range []string{`role="img" aria-labelledby="product-title product-desc"`, `<title id="product-title">Product relationship</title>`, `<desc id="product-desc">Records provide source evidence.</desc>`, "Record → Evidence", "provides"} {
		if !strings.Contains(s, want) {
			t.Fatal("missing", want)
		}
	}
}
func TestNoInventedReadingSequence(t *testing.T) {
	r, c := fixture(t)
	c = composed(t, r, c)
	out := build(t, r, c)
	if strings.Contains(read(t, filepath.Join(out, "article-"+c.Articles[0].ID+".html")), `role="img"`) {
		t.Fatal("invented diagram without authored product relationship")
	}
}
func TestReferenceCatalogWithoutAudienceGates(t *testing.T) {
	r, c := fixture(t)
	c = composed(t, r, c)
	c.Mode = "reference"
	c.Catalog = true
	c.Audiences[0].Features = nil
	c.Topics = []Topic{{ID: "basics", Title: "Basics", Prefixes: []string{"demo/"}}}
	c.Articles = nil
	out := build(t, r, c)
	if _, err := os.Stat(filepath.Join(out, "audience-newcomer.html")); !os.IsNotExist(err) {
		t.Fatal("reference mode generated audience gate")
	}
	catalog := read(t, filepath.Join(out, "features.html"))
	if !strings.Contains(catalog, featureAnchor("demo/start/first")) {
		t.Fatal("catalog omitted feature outside audience selection")
	}
	if !strings.Contains(read(t, filepath.Join(out, "index.html")), `id="perspective"`) {
		t.Fatal("missing perspective selector")
	}
	if !strings.Contains(read(t, filepath.Join(out, "search-index.js")), "demo/start/first") {
		t.Fatal("global search omitted feature")
	}
}
func TestFreshBuild(t *testing.T) {
	r, c := fixture(t)
	out := build(t, r, c)
	if e := Check(r, out, c); e != nil {
		t.Fatal(e)
	}
}
func TestChangedAnchor(t *testing.T) {
	r, c := fixture(t)
	out := build(t, r, c)
	put(t, filepath.Join(r, "app.go"), "// @facet: demo/start/first\npackage changed\n")
	if e := Check(r, out, c); e == nil || !strings.Contains(e.Error(), "record or anchor inventory changed") {
		t.Fatal("missed drift")
	}
}
func TestChangedConfig(t *testing.T) {
	r, c := fixture(t)
	out := build(t, r, c)
	c.Title = "New title"
	if e := Check(r, out, c); e == nil || !strings.Contains(e.Error(), "editorial configuration changed") {
		t.Fatal("missed config drift")
	}
}
func TestChangedArtifact(t *testing.T) {
	r, c := fixture(t)
	out := build(t, r, c)
	put(t, filepath.Join(out, "index.html"), "altered")
	if e := Check(r, out, c); e == nil || !strings.Contains(e.Error(), "documentation modified: index.html") {
		t.Fatal("missed tampering")
	}
}
func TestDeletedRecord(t *testing.T) {
	r, c := fixture(t)
	out := build(t, r, c)
	if e := os.Remove(filepath.Join(r, "docs/facets/features/demo/start/first.yaml")); e != nil {
		t.Fatal(e)
	}
	if e := Check(r, out, c); e == nil || !strings.Contains(e.Error(), "record or anchor inventory changed") {
		t.Fatal("missed deletion")
	}
}
func TestUnknownConfigField(t *testing.T) {
	r, _ := fixture(t)
	p := filepath.Join(r, "config.json")
	put(t, p, `{"version":1,"invented":true}`)
	if _, e := ReadConfig(p); e == nil || !strings.Contains(e.Error(), "unknown field") {
		t.Fatal("accepted typo")
	}
}
func TestDependencyOrdering(t *testing.T) {
	rs := map[string]Evidence{"a": {Fields: map[string]any{"lineage": map[string]any{"depends_on": []any{"b"}}}}, "b": {}}
	got, e := dependencyOrder([]string{"a", "b"}, rs)
	if e != nil || strings.Join(got, ",") != "b,a" {
		t.Fatal(got, e)
	}
}
func TestDependencyCycle(t *testing.T) {
	rs := map[string]Evidence{"a": {Fields: map[string]any{"lineage": map[string]any{"depends_on": []any{"b"}}}}, "b": {Fields: map[string]any{"lineage": map[string]any{"depends_on": []any{"a"}}}}}
	if _, e := dependencyOrder([]string{"a", "b"}, rs); e == nil || !strings.Contains(e.Error(), "dependency cycle") {
		t.Fatal("accepted cycle")
	}
}
func TestDiagramSources(t *testing.T) {
	r, c := fixture(t)
	c = composed(t, r, c)
	c.Articles[0].Diagram = &Diagram{Title: "Flow", Description: "Start here", Sources: []string{"missing"}, Nodes: []Node{{ID: "start", Label: "Start", X: 8, Y: 8}}}
	if _, e := MakePlan(inventory(t, r), c); e == nil || !strings.Contains(e.Error(), "diagram source outside article") {
		t.Fatal("accepted uncited diagram")
	}
}
func TestDiagramEdges(t *testing.T) {
	r, c := fixture(t)
	c = composed(t, r, c)
	c.Articles[0].Diagram = &Diagram{Title: "Flow", Description: "Start here", Sources: []string{"demo/start/first"}, Nodes: []Node{{ID: "start", Label: "Start", X: 8, Y: 8}}, Edges: []Edge{{From: "start", To: "missing"}}}
	if _, e := MakePlan(inventory(t, r), c); e == nil || !strings.Contains(e.Error(), "invalid diagram edge") {
		t.Fatal("accepted unknown node")
	}
}
func TestProductDiagram(t *testing.T) {
	r, c := fixture(t)
	c = composed(t, r, c)
	c.Articles[0].Diagram = &Diagram{Title: "Workspace flow", Description: "Open settings, then check the workspace name.", Sources: []string{"demo/start/first"}, Nodes: []Node{{ID: "settings", Label: "Open settings", X: 8, Y: 8}, {ID: "check", Label: "Check workspace name", X: 8, Y: 108}}, Edges: []Edge{{From: "settings", To: "check", Label: "then"}}}
	out := build(t, r, c)
	s := read(t, filepath.Join(out, "article-"+c.Articles[0].ID+".html"))
	if !strings.Contains(s, "Workspace flow") || !strings.Contains(s, "Open settings → Check workspace name") || !strings.Contains(s, `<desc id="product-desc">Open settings, then check the workspace name.</desc>`) {
		t.Fatal("diagram missing accessible data")
	}
}
func TestConfigTrailingDocument(t *testing.T) {
	r, _ := fixture(t)
	p := filepath.Join(r, "c.json")
	put(t, p, `{"version":1} {"version":1}`)
	if _, e := ReadConfig(p); e == nil || !strings.Contains(e.Error(), "exactly one JSON document") {
		t.Fatal("accepted trailing document")
	}
}
func TestSourceSymlinkEscape(t *testing.T) {
	r, _ := fixture(t)
	inside := filepath.Join(r, "docs/facets/features/demo/start/first.yaml")
	body := read(t, inside)
	outside := filepath.Join(t.TempDir(), "outside.yaml")
	put(t, outside, body)
	if e := os.Remove(inside); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, inside); e != nil {
		t.Skip(e)
	}
	if read(t, inside) != body {
		t.Fatal("symlink fixture unreadable")
	}
	if _, e := Collect(r); e == nil || !strings.Contains(e.Error(), "source symlink outside root") {
		t.Fatal("external record not rejected", e)
	}
}

func TestDeterministicBuild(t *testing.T) {
	r, c := fixture(t)
	c = composed(t, r, c)
	a := build(t, r, c)
	b := build(t, r, c)
	files, e := os.ReadDir(a)
	if e != nil {
		t.Fatal(e)
	}
	other, e := os.ReadDir(b)
	if e != nil || len(files) == 0 || len(files) != len(other) {
		t.Fatal("file set differs", e)
	}
	for _, f := range files {
		if read(t, filepath.Join(a, f.Name())) != read(t, filepath.Join(b, f.Name())) {
			t.Fatal("non deterministic", f.Name())
		}
	}
}
func TestRendererDrift(t *testing.T) {
	r, c := fixture(t)
	out := build(t, r, c)
	p := out + ".lock.json"
	var lock Lock
	if e := json.Unmarshal([]byte(read(t, p)), &lock); e != nil {
		t.Fatal(e)
	}
	lock.Engine = "old"
	if e := writeJSON(p, lock); e != nil {
		t.Fatal(e)
	}
	if e := Check(r, out, c); e == nil || !strings.Contains(e.Error(), "renderer changed") {
		t.Fatal("missed renderer drift")
	}
}
func TestLockTraversal(t *testing.T) {
	r, c := fixture(t)
	out := build(t, r, c)
	p := out + ".lock.json"
	var lock Lock
	if e := json.Unmarshal([]byte(read(t, p)), &lock); e != nil {
		t.Fatal(e)
	}
	outside := filepath.Join(filepath.Dir(out), "outside")
	put(t, outside, "outside")
	sum, e := digest(outside)
	if e != nil {
		t.Fatal(e)
	}
	lock.Files[0] = Source{Path: "../outside", Digest: sum}
	if e := writeJSON(p, lock); e != nil {
		t.Fatal(e)
	}
	if e := Check(r, out, c); e == nil || !strings.Contains(e.Error(), "unsafe lock path") {
		t.Fatal("accepted unsafe lock")
	}
}
func TestPublicEvidenceIsolation(t *testing.T) {
	r, c := fixture(t)
	c = composed(t, r, c)
	internal := build(t, r, c)
	for _, token := range []string{"app.go", "workspace-visible", "prerequisite_knowledge"} {
		if !strings.Contains(read(t, filepath.Join(internal, "sources.html")), token) {
			t.Fatal("isolation fixture lacks internal field", token)
		}
	}
	c.Visibility = "public"
	out := build(t, r, c)
	files, e := os.ReadDir(out)
	if e != nil {
		t.Fatal(e)
	}
	if len(files) != 11+len(c.Articles) {
		t.Fatal("unexpected public file set", len(files))
	}

	for _, name := range []string{"sources.html", "inventory.json", "features.html", "search-index.js"} {
		s := read(t, filepath.Join(out, name))
		if strings.Contains(s, "app.go") || strings.Contains(s, "workspace-visible") || strings.Contains(s, "prerequisite_knowledge") {
			t.Fatal("internal evidence leaked", name)
		}
	}
	locks, e := filepath.Glob(filepath.Join(out, "*.lock.json"))
	if e != nil || len(locks) != 0 {
		t.Fatal("private lock published", e)
	}
	if _, e := os.Stat(out + ".lock.json"); e != nil {
		t.Fatal("private external lock missing", e)
	}
	if e := Check(r, out, c); e != nil {
		t.Fatal(e)
	}
}
func TestSitePermissions(t *testing.T) {
	r, c := fixture(t)
	out := build(t, r, c)
	info, e := os.Stat(out)
	if e != nil {
		t.Fatal(e)
	}
	if info.Mode().Perm() != 0755 {
		t.Fatal(info.Mode())
	}
	info, e = os.Stat(out + ".lock.json")
	if e != nil {
		t.Fatal(e)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
}
func TestAddedArtifact(t *testing.T) {
	r, c := fixture(t)
	out := build(t, r, c)
	put(t, filepath.Join(out, "extra.html"), "extra")
	if e := Check(r, out, c); e == nil || !strings.Contains(e.Error(), "file set changed") {
		t.Fatal(e)
	}
}
func TestBuildInsideRepository(t *testing.T) {
	r, c := fixture(t)
	c = composed(t, r, c)
	out := filepath.Join(r, "docs-site")
	if e := Build(r, out, c); e != nil {
		t.Fatal(e)
	}
	if e := Check(r, out, c); e != nil {
		t.Fatal(e)
	}
}
func TestPublicPlannedArticle(t *testing.T) {
	r, c := fixture(t)
	c = composed(t, r, c)
	c.Visibility = "public"
	inv := inventory(t, r)
	inv.Records[0].Lifecycle = "planned"
	if _, e := MakePlan(inv, c); e == nil || !strings.Contains(e.Error(), "not active") {
		t.Fatal(e)
	}
}
func TestGeneratedAnchorsNotScanned(t *testing.T) {
	r, c := fixture(t)
	path := filepath.Join(r, "docs/facets/features/demo/start/first.yaml")
	put(t, path, strings.Replace(read(t, path), "Open the workspace to begin.", `"Example @facet: demo/start/first in prose."`, 1))
	c = composed(t, r, c)
	out := filepath.Join(r, "docs-site")
	if e := Build(r, out, c); e != nil {
		t.Fatal(e)
	}
	if e := Check(r, out, c); e != nil {
		t.Fatal(e)
	}
}
func TestMultiRegistryInventory(t *testing.T) {
	r, c := fixture(t)
	put(t, filepath.Join(r, ".facetrc.yaml"), "registries:\n  first:\n    records: docs/facets\n    sources: [app.go]\n  second:\n    records: docs/facets\n    sources: [app.go]\n")
	inv := inventory(t, r)
	if len(inv.Records) != 2 || inv.Records[0].ID != "first:demo/start/first" || inv.Records[1].ID != "second:demo/start/first" {
		t.Fatal(inv)
	}
	c.Audiences[0].Features = []string{"second:demo/start/first"}
	if _, e := MakePlan(inv, c); e != nil {
		t.Fatal(e)
	}
}
func TestCheckTrailingSlash(t *testing.T) {
	r, c := fixture(t)
	out := build(t, r, c)
	if e := Check(r, out+string(filepath.Separator), c); e != nil {
		t.Fatal(e)
	}
}
func TestPrivateLockContainsOnlyDigests(t *testing.T) {
	r, c := fixture(t)
	out := build(t, r, c)
	s := read(t, out+".lock.json")
	for _, token := range []string{"Open the workspace", "app.go", "prerequisite_knowledge"} {
		if strings.Contains(s, token) {
			t.Fatal("source contents in lock", token)
		}
	}
	if !strings.Contains(s, "inventory_digest") {
		t.Fatal("missing digest")
	}
}
func TestPublicAudiencePlannedWithoutArticle(t *testing.T) {
	r, c := fixture(t)
	c.Visibility = "public"
	inv := inventory(t, r)
	inv.Records[0].Lifecycle = "planned"
	if _, e := MakePlan(inv, c); e == nil || !strings.Contains(e.Error(), "not active") {
		t.Fatal(e)
	}
}
func TestDefaultPublicFreshness(t *testing.T) {
	for _, visibility := range []string{"", "internal"} {
		t.Run(visibility, func(t *testing.T) {
			r, c := fixture(t)
			c.Visibility = visibility
			out := build(t, r, c)
			if e := Check(r, out, c); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestPublicPlanDoesNotExposeGaps(t *testing.T) {
	r, c := fixture(t)
	path := filepath.Join(r, "docs/facets/features/demo/start/first.yaml")
	put(t, filepath.Join(filepath.Dir(path), "internal.yaml"), strings.Replace(read(t, path), "demo/start/first", "demo/start/internal", 1))
	internal := build(t, r, c)
	before := read(t, filepath.Join(internal, "plan.json"))
	for _, token := range []string{"demo/start/internal", "not_for"} {
		if !strings.Contains(before, token) {
			t.Fatal("plan fixture lacks internal token", token)
		}
	}
	c.Visibility = "public"
	out := build(t, r, c)
	after := read(t, filepath.Join(out, "plan.json"))
	for _, token := range []string{"demo/start/internal", "not_for"} {
		if strings.Contains(after, token) {
			t.Fatal("plan leaked token", token)
		}
	}
}
func TestIndependentRenderComparison(t *testing.T) {
	r, c := fixture(t)
	out := build(t, r, c)
	put(t, filepath.Join(out, "index.html"), "altered")
	p := out + ".lock.json"
	var lock Lock
	if e := json.Unmarshal([]byte(read(t, p)), &lock); e != nil {
		t.Fatal(e)
	}
	for i, f := range lock.Files {
		if f.Path == "index.html" {
			sum, e := digest(filepath.Join(out, f.Path))
			if e != nil {
				t.Fatal(e)
			}
			lock.Files[i].Digest = sum
		}
	}
	if e := writeJSON(p, lock); e != nil {
		t.Fatal(e)
	}
	if e := Check(r, out, c); e == nil || !strings.Contains(e.Error(), "needs rendering") {
		t.Fatal(e)
	}
}

func TestReferenceInvalidPerspectiveSource(t *testing.T) {
	r, c := fixture(t)
	c = composed(t, r, c)
	c.Mode = "reference"
	c.Catalog = true
	c.Articles[0].Notes = []PerspectiveNote{{Perspective: "newcomer", Title: "Context", Body: "Explanation", Sources: []string{"unknown/source/record"}}}
	if _, err := MakePlan(inventory(t, r), c); err == nil || !strings.Contains(err.Error(), "perspective source") {
		t.Fatal("accepted unrelated note evidence", err)
	}
}
func TestReferenceMalformedTable(t *testing.T) {
	r, c := fixture(t)
	c = composed(t, r, c)
	c.Mode = "reference"
	c.Catalog = true
	c.Articles[0].Sections[0].Table = &Table{Columns: []string{"Name", "Meaning"}, Rows: [][]string{{"one"}}}
	if _, err := MakePlan(inventory(t, r), c); err == nil || !strings.Contains(err.Error(), "row width") {
		t.Fatal("accepted malformed table", err)
	}
}
func TestReferenceCatalogFallbackTopic(t *testing.T) {
	r, c := fixture(t)
	c.Mode = "reference"
	c.Catalog = true
	c.Audiences[0].Features = nil
	views := topicViews(c, featureCatalog(inventory(t, r), c))
	if len(views) != 1 || views[0].Topic.ID != "other" || len(views[0].Features) != 1 {
		t.Fatalf("lost unmatched features: %#v", views)
	}
}

func TestPublicReferenceExcludesUnselectedPlannedFeature(t *testing.T) {
	r, c := fixture(t)
	c.Mode = "reference"
	c.Catalog = true
	c.Audiences[0].Features = nil
	recordsDir := filepath.Join(r, "docs/facets/features/demo/start")
	// A real unselected record is visible internally, proving the export control.
	put(t, filepath.Join(recordsDir, "hidden.yaml"), `schema_version: "0.1"
id: demo/start/hidden
lifecycle: planned
intent:
  summary: Confidential planned capability.
`)
	internal := build(t, r, c)
	for _, name := range []string{"features.html", "search-index.js"} {
		if !strings.Contains(read(t, filepath.Join(internal, name)), "Confidential planned capability") {
			t.Fatal("missing internal control", name)
		}
		if !strings.Contains(read(t, filepath.Join(internal, name)), "Open Settings.") {
			t.Fatal("missing internal Knowledge control", name)
		}
	}
	c.Visibility = "public"
	out := build(t, r, c)
	for _, name := range []string{"features.html", "search-index.js", "inventory.json", "sources.html"} {
		text := read(t, filepath.Join(out, name))
		for _, token := range []string{"Confidential planned capability", "demo/start/hidden", "app.go", "workspace-visible", "Open Settings."} {
			if strings.Contains(text, token) {
				t.Fatal("public reference leak", name, token)
			}
		}
	}
	if !strings.Contains(read(t, filepath.Join(out, "features.html")), featureAnchor("demo/start/first")) {
		t.Fatal("public catalog lost unselected active feature")
	}
}

func TestSearchEntryIdentity(t *testing.T) {
	r, c := fixture(t)
	c = composed(t, r, c)
	entries := buildSearch(c, nil, featureCatalog(inventory(t, r), c))
	if len(entries) != len(c.Articles)+1 {
		t.Fatal("missing search entries")
	}
	a := entries[0]
	if a.Title != c.Articles[0].Title || a.Kind != "article" || a.URL != "article-"+c.Articles[0].ID+".html" || a.Lifecycle != "" {
		t.Fatalf("article identity corrupted: %#v", a)
	}
	f := entries[len(entries)-1]
	if f.Kind != "feature" || f.Lifecycle != "active" || !strings.HasPrefix(f.URL, "features.html#feature-") {
		t.Fatalf("feature identity corrupted: %#v", f)
	}
}

// English is the default site language; "de" renders the German table, and the
// German table names every key the English one has, so no German site falls
// back to English in the middle of a page.
func TestSiteTextIsEnglishByDefaultAndGermanUnderDe(t *testing.T) {
	for key := range siteTextEN {
		if _, ok := siteTextDE[key]; !ok {
			t.Errorf("German site text lacks %q", key)
		}
	}
	for key := range siteTextDE {
		if _, ok := siteTextEN[key]; !ok {
			t.Errorf("German site text has %q, which English does not", key)
		}
	}
	cases := []struct{ language, key, want string }{
		{"", "close", "Close"},
		{"en", "deprecated", "Deprecated"},
		{"fr", "otherTitle", "Other features"},
		{"de", "close", "Schließen"},
		{"de", "deprecated", "Abgekündigt"},
		{"de", "otherTitle", "Weitere Funktionen"},
		{"de", "no-such-key", "no-such-key"},
	}
	for _, c := range cases {
		if got := translate(c.language, c.key); got != c.want {
			t.Errorf("translate(%q, %q) = %q, want %q", c.language, c.key, got, c.want)
		}
	}
	topics := topicViews(Config{Language: "de"}, []FeatureView{{Topic: "other"}})
	if len(topics) != 1 || topics[0].Topic.Title != "Weitere Funktionen" || topics[0].Topic.Description != "Weitere dokumentierte Funktionen des Produkts." {
		t.Fatalf("German 'other' topic = %+v", topics)
	}
	topics = topicViews(Config{}, []FeatureView{{Topic: "other"}})
	if len(topics) != 1 || topics[0].Topic.Title != "Other features" {
		t.Fatalf("default 'other' topic = %+v", topics)
	}
}
