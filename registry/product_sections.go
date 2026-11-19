// @facet: navigation/interfaces/product-services
// @facet-role: config
package facet

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/goccy/go-yaml"
)

// ProductSectionValidator checks one product section of a loaded
// configuration. It reads the section through Config.ProductSection and
// returns an error that names ConfigName and the offending key, the way Core's
// own validation does.
type ProductSectionValidator func(cfg *Config) error

var productSections = struct {
	sync.Mutex
	validators map[string]ProductSectionValidator
}{validators: map[string]ProductSectionValidator{}}

// coreKeys are the top-level keys Core itself reads: every yaml tag of Config
// and 'requires', which the Core pin reads on its own (corepin.go).
func coreKeys() map[string]bool {
	keys := map[string]bool{"requires": true}
	kind := reflect.TypeOf(Config{})
	for i := 0; i < kind.NumField(); i++ {
		tag := strings.Split(kind.Field(i).Tag.Get("yaml"), ",")[0]
		if tag != "" && tag != "-" {
			keys[tag] = true
		}
	}
	return keys
}

// RegisterProductSection makes the named top-level section the registering
// product's. It panics on a name Core already reads or another product has
// registered: both are programming errors of the build, and a silent second
// owner would make it undecidable which validator a project's file answers to.
func RegisterProductSection(name string, validate ProductSectionValidator) {
	if name == "" || validate == nil {
		panic("facet: RegisterProductSection needs a name and a validator")
	}
	if coreKeys()[name] {
		panic(fmt.Sprintf("facet: section %q is a Core configuration key", name))
	}
	productSections.Lock()
	defer productSections.Unlock()
	if _, taken := productSections.validators[name]; taken {
		panic(fmt.Sprintf("facet: section %q is registered twice", name))
	}
	productSections.validators[name] = validate
}

// validateProductSections runs the registered validators in name order, so the
// first error a project sees does not depend on registration order.
func validateProductSections(cfg *Config) error {
	productSections.Lock()
	names := make([]string, 0, len(productSections.validators))
	validators := make(map[string]ProductSectionValidator, len(productSections.validators))
	for name, validate := range productSections.validators {
		names = append(names, name)
		validators[name] = validate
	}
	productSections.Unlock()
	sort.Strings(names)
	for _, name := range names {
		if err := validators[name](cfg); err != nil {
			return err
		}
	}
	return nil
}

// ProductSection decodes the named top-level section into out. The first value
// is false when the configuration has no such key; out is then left untouched.
// It answers for a nil Config and for a project without .facetrc.yaml too.
func (c *Config) ProductSection(name string, out any) (bool, error) {
	if c == nil || len(c.data) == 0 {
		return false, nil
	}
	var top map[string]any
	if err := yaml.Unmarshal(c.data, &top); err != nil {
		return false, fmt.Errorf("%s: %w", ConfigName, err)
	}
	value, ok := top[name]
	if !ok {
		return false, nil
	}
	if value == nil {
		return true, nil
	}
	encoded, err := yaml.Marshal(value)
	if err != nil {
		return true, fmt.Errorf("%s: %s: %w", ConfigName, name, err)
	}
	if err := yaml.Unmarshal(encoded, out); err != nil {
		return true, fmt.Errorf("%s: %s: %w", ConfigName, name, err)
	}
	return true, nil
}
