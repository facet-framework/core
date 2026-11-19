package remote

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) (string, Publication) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{"record.yaml": "schema_version: '0.1'\nid: navigation/interfaces/example\nintent:\n  summary: Example record\ncontract:\n  scenarios: []\n", "source.go": "package example\n// @" + "facet: navigation/interfaces/example\nfunc Example() {}\n"}
	pub := Publication{SchemaVersion: 1}
	for _, name := range []string{"record.yaml", "source.go"} {
		data := []byte(files[name])
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(data)
		kind := "source"
		if name == "record.yaml" {
			kind = "features"
		}
		pub.Files = append(pub.Files, PublishedFile{Path: name, SHA256: hex.EncodeToString(hash[:]), Kind: kind})
	}
	return root, pub
}
func TestPublicationIsExplicitAndImmutable(t *testing.T) {
	root, pub := fixture(t)
	c, err := LoadCorpus(root, pub)
	if err != nil {
		t.Fatal(err)
	}
	r := NewReader(c)
	if err := os.WriteFile(filepath.Join(root, "source.go"), []byte("secret replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	src, err := r.source(Input{Path: "source.go", Start: 1, End: 3})
	if err != nil || strings.Contains(src.Text, "secret") {
		t.Fatalf("snapshot changed: %+v %v", src, err)
	}
	for _, p := range []string{"../source.go", "/etc/passwd", ".env", "unpublished.go"} {
		if _, err := r.source(Input{Path: p, Start: 1, End: 1}); err == nil {
			t.Fatalf("read unpublished path %q", p)
		}
	}
	if _, err := LoadCorpus(root, pub); err == nil {
		t.Fatal("accepted hash mismatch")
	}
}
func TestPublicationRejectsSymlinksAndPrivatePaths(t *testing.T) {
	root, pub := fixture(t)
	if err := os.Symlink("record.yaml", filepath.Join(root, "link.yaml")); err != nil {
		t.Fatal(err)
	}
	pub.Files[0].Path = "link.yaml"
	if _, err := LoadCorpus(root, pub); err == nil {
		t.Fatal("accepted symlink")
	}
	for _, p := range []string{".env", "a/../record.yaml", ".git/config", "a/key.pem", "a\\b", "a/%2e%2e/b"} {
		if validPath(p) {
			t.Fatalf("accepted private/ambiguous path %q", p)
		}
	}
}
func TestRecordSelectionAndSections(t *testing.T) {
	root, pub := fixture(t)
	c, err := LoadCorpus(root, pub)
	if err != nil {
		t.Fatal(err)
	}
	r := NewReader(c)
	for _, id := range []string{"../../etc/passwd", "missing/record/id"} {
		if _, err := c.record(id, "features"); err == nil {
			t.Fatal("accepted invalid or missing id")
		}
	}
	out, err := r.sections(Input{ID: "navigation/interfaces/example", Sections: []string{"intent"}})
	if err != nil || len(out.Sections) != 1 || out.Sections[0].Text == nil || !strings.Contains(*out.Sections[0].Text, "Example record") || strings.Contains(*out.Sections[0].Text, "contract:") {
		t.Fatalf("wrong section: %+v %v", out, err)
	}
	if _, err := r.sections(Input{ID: "navigation/interfaces/example", Sections: []string{"unknown"}}); err == nil {
		t.Fatal("unknown section accepted")
	}
	if _, err := r.sections(Input{ID: "navigation/interfaces/example", Sections: []string{"intent", "intent"}}); err == nil {
		t.Fatal("duplicate section accepted")
	}
	anchors, err := r.anchors(Input{ID: "navigation/interfaces/example"})
	if err != nil || len(anchors.Anchors) != 1 || anchors.Anchors[0].Path != "source.go" {
		t.Fatalf("wrong anchors: %+v %v", anchors, err)
	}
	for _, in := range []Input{{Path: "source.go", Start: 0, End: 1}, {Path: "source.go", Start: 2, End: 1}, {Path: "source.go", Start: 1, End: 4}} {
		if _, err := r.source(in); err == nil {
			t.Fatal("invalid range accepted")
		}
	}
}
func TestYAMLRejectsAliasesTagsAndDuplicateKeys(t *testing.T) {
	for _, raw := range []string{"id: a/b/c\nid: d/e/f\n", "id: &x a/b/c\nother: *x\n", "id: !custom a/b/c\n", "id: a/b/c\n---\nid: a/b/c\n", "123: value\nid: a/b/c\n"} {
		if _, err := recordFields([]byte(raw)); err == nil {
			t.Fatalf("accepted unsafe YAML: %s", raw)
		}
	}
}
func TestCursorBindsSelection(t *testing.T) {
	root, pub := fixture(t)
	c, err := LoadCorpus(root, pub)
	if err != nil {
		t.Fatal(err)
	}
	other := c.records[0]
	other.ID = "navigation/interfaces/second"
	c.records = append(c.records, other)
	r := NewReader(c)
	first, err := r.find(Input{Limit: 1})
	if err != nil || first.NextCursor == nil {
		t.Fatal("missing cursor", err)
	}
	next, err := r.find(Input{Limit: 1, Cursor: *first.NextCursor})
	if err != nil || len(next.Records) != 1 || next.Records[0].ID != other.ID {
		t.Fatalf("wrong continuation %+v %v", next, err)
	}
	for _, in := range []Input{{Limit: 2, Cursor: *first.NextCursor}, {Limit: 1, Cursor: *first.NextCursor, Query: "different"}, {Limit: 1, Cursor: *first.NextCursor + "x"}} {
		if _, err := r.find(in); err == nil {
			t.Fatal("accepted mismatched/tampered cursor")
		}
	}
}
