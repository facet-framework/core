// @facet: navigation/interfaces/product-services
// @facet-role: primary
package cli

import (
	"fmt"
	"slices"
	"sync"

	"github.com/facet-framework/core/registry"
)

// ProductBinding is what a build knows about one optional product before, or
// without, its installation. Core holds no product names of its own beyond
// the bindings registered into it: a build that links a product's binding
// keeps that product's placeholder command, opt-in check and compatibility
// names; a build that does not link it has none of them, and an installed
// package of that product still works through its manifest alone.
type ProductBinding struct {
	// ID is the manifest id of the product the binding describes.
	ID string
	// Command is the top-level command the product contributes. While the
	// product is not installed the command stays visible and says how to
	// install it, instead of being an unknown command. Empty means none.
	Command string
	// CompatibilityTools are tool names the product serves over MCP that keep
	// their unprefixed name instead of '<id>__<name>'.
	CompatibilityTools []string
	// DriftHelp is a paragraph appended to 'facet drift --help'.
	DriftHelp string
	// Requested reports whether the project explicitly asked for the named
	// service from this product. config loads the project configuration once,
	// on demand; an error from it is reported as a configuration failure.
	Requested func(service string, config func() (*facet.Config, error)) (bool, error)
	// Missing is the error when Requested is true and no installed product
	// with this id provides the service.
	Missing string
}

var productBindingRegistry struct {
	sync.Mutex
	bindings []ProductBinding
}

// RegisterProduct adds a binding. Builds register from init, so bindings are
// in place before the command tree is built. It panics on an empty or repeated
// id: two bindings for one product would make its placeholder and its opt-in
// answer twice.
func RegisterProduct(binding ProductBinding) {
	if binding.ID == "" {
		panic("cli: RegisterProduct needs a product id")
	}
	productBindingRegistry.Lock()
	defer productBindingRegistry.Unlock()
	for _, existing := range productBindingRegistry.bindings {
		if existing.ID == binding.ID {
			panic(fmt.Sprintf("cli: product %q is registered twice", binding.ID))
		}
	}
	productBindingRegistry.bindings = append(productBindingRegistry.bindings, binding)
}

// productBindings returns the bindings in registration order.
func productBindings() []ProductBinding {
	productBindingRegistry.Lock()
	defer productBindingRegistry.Unlock()
	return slices.Clone(productBindingRegistry.bindings)
}

// ProductToolName is the MCP name a product's tool is served under: the
// product namespace, unless the product's binding keeps the name for
// compatibility.
func ProductToolName(id, tool string) string {
	for _, binding := range productBindings() {
		if binding.ID == id && slices.Contains(binding.CompatibilityTools, tool) {
			return tool
		}
	}
	return id + "__" + tool
}

// productDriftHelp collects the drift help paragraphs of the bound products.
func productDriftHelp() string {
	text := ""
	for _, binding := range productBindings() {
		if binding.DriftHelp != "" {
			text += "\n\n" + binding.DriftHelp
		}
	}
	return text
}
