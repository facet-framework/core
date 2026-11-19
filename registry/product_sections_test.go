// @facet: navigation/interfaces/product-services
// @facet-role: test
package facet

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// Registered once for the whole test binary, the way a build registers a
// product section from init. It refuses only a section that sets 'refuse'.
func init() {
	RegisterProductSection("sectiontest", func(cfg *Config) error {
		var section struct {
			Refuse bool `yaml:"refuse"`
		}
		if _, err := cfg.ProductSection("sectiontest", &section); err != nil {
			return err
		}
		if section.Refuse {
			return errors.New(ConfigName + ": sectiontest.refuse is set")
		}
		return nil
	})
}

// A section no linked product owns loads, unread and unrefused, and the
// shipped configuration schema accepts it as a mapping.
func TestASectionNoProductOwnsIsTolerated(t *testing.T) {
	config := "records: docs/facets\nunowned:\n  drift: true\n  links:\n    base: https://git.example.com/x/y\n"
	cfg, err := LoadConfig(project(t, config))
	if err != nil {
		t.Fatalf("an unowned product section failed the load: %v", err)
	}
	var section struct {
		Drift bool `yaml:"drift"`
	}
	if ok, err := cfg.ProductSection("unowned", &section); !ok || err != nil || !section.Drift {
		t.Fatalf("the section is not readable for its product: ok=%v err=%v %+v", ok, err, section)
	}
	if ok, err := cfg.ProductSection("absent", &section); ok || err != nil {
		t.Fatalf("an absent section: ok=%v err=%v", ok, err)
	}
	var nilCfg *Config
	if ok, err := nilCfg.ProductSection("unowned", &section); ok || err != nil {
		t.Fatalf("a nil config: ok=%v err=%v", ok, err)
	}

	root := project(t, "")
	copySchemas(t, root)
	compiled, err := compileSchema(filepath.Join(root, "docs/facets/schema/facetrc.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(map[string]any{"unowned": map[string]any{"drift": true}}); err != nil {
		t.Fatalf("the schema refused a product section: %v", err)
	}
	if err := compiled.Validate(map[string]any{"unowned": "scalar"}); err == nil {
		t.Fatal("the schema accepted a top-level scalar Core does not define")
	}
}

// A registered section's validator runs on every load and its error refuses
// the configuration, as Core's own keys are refused.
func TestARegisteredSectionIsValidatedOnLoad(t *testing.T) {
	if _, err := LoadConfig(project(t, "sectiontest:\n  refuse: false\n")); err != nil {
		t.Fatalf("an accepted section failed the load: %v", err)
	}
	_, err := LoadConfig(project(t, "sectiontest:\n  refuse: true\n"))
	if err == nil || !strings.Contains(err.Error(), "sectiontest.refuse is set") {
		t.Fatalf("the validator's refusal did not reach the load: %v", err)
	}
	_, err = LoadConfig(project(t, "sectiontest:\n  refuse: [\n"))
	if err == nil {
		t.Fatal("an unparseable section was accepted")
	}
}

// A Core key or a second owner cannot be registered.
func TestASectionHasOneOwner(t *testing.T) {
	for _, name := range []string{"records", "requires", "forges", "sectiontest", ""} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("section %q was registered", name)
				}
			}()
			RegisterProductSection(name, func(*Config) error { return nil })
		}()
	}
}
