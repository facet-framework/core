// The fixture below carries an @facet: tag for a feature that exists only
// inside a temporary directory, so this file opts itself out of anchor scanning:
// @facet-ignore-file
package corpus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/facet-framework/core/registry"
)

// registerForTest registers a section for one test and removes it afterwards,
// so the package-wide table holds no invented section once the test is done.
func registerForTest(t *testing.T, name string, build SectionBuilder) {
	t.Helper()
	RegisterSection(name, build)
	t.Cleanup(func() {
		sectionBuilders.Lock()
		defer sectionBuilders.Unlock()
		delete(sectionBuilders.builders, name)
	})
}

// topLevelKeys reads the object keys of a JSON document in the order they
// were written.
func topLevelKeys(t *testing.T, data []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("not a JSON object: %v %v", tok, err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

// A product linked into the build adds its own top-level section: the corpus
// carries it under its name after Core's fields, sections in name order, and
// a builder with nothing to say leaves its key out entirely.
func TestRegisteredSectionsFollowCoreFieldsInNameOrder(t *testing.T) {
	reg := project(t, map[string]string{
		"docs/facets/features/tasks/create/quick-add.yaml": quickAdd,
		"src/quick_add.ts": "// @facet: tasks/create/quick-add\nexport function quickAdd() {}\n",
	})
	// Each builder answers only for this test's registry, so a test elsewhere
	// that builds a corpus while these are registered sees no section.
	mine := func(r facet.Registry) bool { return r.Root == reg.Root }
	registerForTest(t, "example-notes", func(r facet.Registry) (any, bool) {
		return map[string]int{"notes": 2}, mine(r)
	})
	registerForTest(t, "example-alpha", func(r facet.Registry) (any, bool) {
		return []string{"first"}, mine(r)
	})
	registerForTest(t, "example-empty", func(r facet.Registry) (any, bool) {
		return "never shipped", false
	})

	corpus, err := Build(reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := corpus.Section("example-notes"); fmt.Sprint(got) != "map[notes:2]" {
		t.Fatalf("Section(example-notes) = %v, want the builder's value", got)
	}
	if got := corpus.Section("example-empty"); got != nil {
		t.Fatalf("a builder that reported nothing must leave no section, got %v", got)
	}

	data, err := json.Marshal(corpus)
	if err != nil {
		t.Fatal(err)
	}
	core, err := json.Marshal(plainCorpus(*corpus))
	if err != nil {
		t.Fatal(err)
	}
	coreKeys := topLevelKeys(t, core)
	// Premise: Core's own fields are there to be followed.
	if !slices.Contains(coreKeys, "records") || !slices.Contains(coreKeys, "scan") {
		t.Fatalf("premise: the Core corpus has no records or scan key: %v", coreKeys)
	}
	want := append(slices.Clone(coreKeys), "example-alpha", "example-notes")
	if got := topLevelKeys(t, data); !slices.Equal(got, want) {
		t.Fatalf("keys = %v, want Core's fields as declared and then the sections in name order %v", got, want)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("the marshalled corpus is not valid JSON: %v", err)
	}
	if string(decoded["example-notes"]) != `{"notes":2}` || string(decoded["example-alpha"]) != `["first"]` {
		t.Fatalf("section values = %s, %s", decoded["example-notes"], decoded["example-alpha"])
	}
}

// A corpus without sections marshals exactly as its plain Core fields: a build
// that links no product ships nothing extra.
func TestACorpusWithoutSectionsMarshalsCoreFieldsAlone(t *testing.T) {
	reg := project(t, map[string]string{
		"docs/facets/features/tasks/create/quick-add.yaml": quickAdd,
	})
	corpus, err := Build(reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(corpus)
	if err != nil {
		t.Fatal(err)
	}
	core, err := json.Marshal(plainCorpus(*corpus))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, core) {
		t.Fatalf("a corpus without sections differs from its Core fields:\n%s\n%s", data, core)
	}
}

// RegisterSection refuses what would make one key ambiguous: no name, no
// builder, a name Core's own fields use, or a name registered twice.
func TestRegisterSectionRefusesAmbiguousNames(t *testing.T) {
	build := func(facet.Registry) (any, bool) { return nil, false }
	registerForTest(t, "example-taken", build)
	cases := []struct {
		name    string
		section string
		build   SectionBuilder
		want    string
	}{
		{"empty name", "", build, "needs a name and a builder"},
		{"no builder", "example-unbuilt", nil, "needs a name and a builder"},
		{"Core field", "records", build, `section "records" is a Core corpus field`},
		{"registered twice", "example-taken", build, `section "example-taken" is registered twice`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				got := fmt.Sprint(recover())
				if !strings.Contains(got, c.want) {
					t.Fatalf("panic = %q, want one containing %q", got, c.want)
				}
			}()
			RegisterSection(c.section, c.build)
		})
	}
	sectionBuilders.Lock()
	_, leaked := sectionBuilders.builders["example-unbuilt"]
	sectionBuilders.Unlock()
	if leaked {
		t.Fatal("a refused registration was kept")
	}
}
