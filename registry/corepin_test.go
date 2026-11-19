package facet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, ConfigName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCorePinIsReadAndNormalised(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{`"0.1.0"`, "0.1.0"},
		{`"v0.1.0"`, "0.1.0"},
		{`0.1.0`, "0.1.0"},
		{`'0.1.0-rc.1+build.7'`, "0.1.0-rc.1+build.7"},
	} {
		root := t.TempDir()
		writeConfig(t, root, "requires:\n  core: "+tc.value+"\n")
		pin, err := ReadCorePin(root)
		if err != nil || pin == nil || pin.Core != tc.want || pin.File != ConfigPath(root) {
			t.Fatalf("%s: pin %+v, err %v", tc.value, pin, err)
		}
	}
}

func TestPinSatisfactionIsExactEquality(t *testing.T) {
	pin := CorePin{Core: "0.1.0"}
	for running, want := range map[string]bool{"0.1.0": true, "0.1.0-dev.1794528000+gabc": false, "0.1.1": false, "v0.1.0": false, "0.1": false} {
		if pin.Satisfied(running) != want {
			t.Errorf("Satisfied(%q) = %v, want %v", running, !want, want)
		}
	}
}

// A release build reports its commit as build metadata. That is not part of
// which version it is, so the pin a project writes has to accept it; a pin that
// names a build asks for that build only.
func TestBuildMetadataIsNotPartOfThePinnedVersion(t *testing.T) {
	for _, c := range []struct {
		pin, running string
		want         bool
	}{
		{"0.1.0", "0.1.0+g8ff6ff5d1234", true},
		{"0.1.0-rc.1", "0.1.0-rc.1+g8ff6ff5d1234", true},
		{"0.1.0", "0.1.0-rc.1+g8ff6ff5d1234", false},
		{"0.1.0", "0.1.1+g8ff6ff5d1234", false},
		{"0.1.0", "0.1.0-dev.20261114.2+g8ff6ff5d1234", false},
		{"0.1.0+g8ff6ff5d1234", "0.1.0+g8ff6ff5d1234", true},
		{"0.1.0+g8ff6ff5d1234", "0.1.0+g000000000000", false},
		{"0.1.0+g8ff6ff5d1234", "0.1.0", false},
	} {
		if got := (CorePin{Core: c.pin}).Satisfied(c.running); got != c.want {
			t.Errorf("pin %q, running %q: Satisfied = %v, want %v", c.pin, c.running, got, c.want)
		}
	}
}

// Every way of writing a pin that is not one exact version is refused with the
// file and what the author wrote, never read as "no pin". The project schema
// must refuse exactly the same declarations.
func TestEveryMalformedPinIsRefusedByLoaderAndSchema(t *testing.T) {
	root := project(t, "")
	copySchemas(t, root)
	compiled, err := compileSchema(filepath.Join(root, "docs/facets/schema/facetrc.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		yaml     string
		requires any    // the same declaration as the schema sees it
		mention  string // empty: valid
	}{
		{"exact version", "requires:\n  core: \"0.1.0\"\n", map[string]any{"core": "0.1.0"}, ""},
		{"prerelease with v", "requires:\n  core: v0.1.0-rc.1\n", map[string]any{"core": "v0.1.0-rc.1"}, ""},
		{"caret range", "requires:\n  core: \"^0.1.0\"\n", map[string]any{"core": "^0.1.0"}, `requires.core "^0.1.0" is not a SemVer version`},
		{"two parts", "requires:\n  core: \"0.1\"\n", map[string]any{"core": "0.1"}, `requires.core "0.1" is not a SemVer version`},
		{"empty string", "requires:\n  core: \"\"\n", map[string]any{"core": ""}, `requires.core "" is not a SemVer version`},
		{"word", "requires:\n  core: latest\n", map[string]any{"core": "latest"}, `requires.core "latest" is not a SemVer version`},
		{"typo key", "requires:\n  cor: \"0.1.0\"\n", map[string]any{"cor": "0.1.0"}, "requires.cor is not a known key"},
		{"requires is a string", "requires: \"9.9.9\"\n", "9.9.9", `requires must be a mapping such as 'requires: {core: "0.1.0"}', not "9.9.9"`},
		{"requires is empty", "requires:\n", nil, "requires must be a mapping such as 'requires: {core: \"0.1.0\"}', not an empty value"},
		{"core is a list", "requires:\n  core: [9.9.9]\n", map[string]any{"core": []any{"9.9.9"}}, "requires.core must be one version written as a string such as \"0.1.0\", not [9.9.9]"},
		{"core is empty", "requires:\n  core:\n", map[string]any{"core": nil}, "not an empty value"},
		{"core is a number", "requires:\n  core: 1.0\n", map[string]any{"core": 1.0}, "not 1.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeConfig(t, dir, tc.yaml)
			pin, loadErr := ReadCorePin(dir)
			schemaErr := compiled.Validate(map[string]any{"requires": tc.requires})
			if tc.mention == "" {
				if loadErr != nil || pin == nil || schemaErr != nil {
					t.Fatalf("valid pin refused: loader %v, schema %v", loadErr, schemaErr)
				}
				return
			}
			if loadErr == nil || !strings.Contains(loadErr.Error(), ConfigPath(dir)) || !strings.Contains(loadErr.Error(), tc.mention) {
				t.Errorf("loader = %v, want an error naming the file and %q", loadErr, tc.mention)
			}
			if schemaErr == nil {
				t.Errorf("the schema accepts what the loader refuses")
			}
		})
	}
}

// The pin must never break configuration loading: every hook depends on it to
// find a project's registries, and a broken pin would otherwise silence them.
func TestLoadConfigIgnoresTheCorePin(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "products/shop/docs/facets"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, requires := range []string{"requires:\n  cor: \"9.9.9\"\n", "requires:\n  core: latest\n", "requires: 9\n"} {
		writeConfig(t, root, requires+"registries:\n  shop:\n    records: products/shop/docs/facets\n")
		if _, err := ReadCorePin(root); err == nil {
			t.Fatalf("premise: %q is not refused by the pin check", requires)
		}
		cfg, err := LoadConfig(root)
		if err != nil || len(cfg.All()) != 1 || cfg.All()[0].Name != "shop" {
			t.Fatalf("%q broke configuration loading: %v", requires, err)
		}
	}
}

func TestNoPinMeansNoPin(t *testing.T) {
	root := t.TempDir()
	if pin, err := ReadCorePin(root); pin != nil || err != nil {
		t.Fatalf("no file: %+v %v", pin, err)
	}
	writeConfig(t, root, "conventions: CONVENTIONS.md\n")
	if pin, err := ReadCorePin(root); pin != nil || err != nil {
		t.Fatalf("no requires: %+v %v", pin, err)
	}
}
