package facet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// errorPrinter renders validation-error kinds. A nil printer panics for some
// error kinds, so one is constructed explicitly rather than passing nil.
var errorPrinter = message.NewPrinter(language.English)

// lintSchema validates every record against the schema for its category.
//
// @facet: registry/integrity/record-validation
// @facet-role: related
func lintSchema(reg Registry, rep *Report, opts LintOptions) error {
	if !isDir(reg.SchemaDir) {
		rep.add(Finding{
			Severity: SeverityWarning,
			Check:    "schema",
			Message:  fmt.Sprintf("no schema directory at %s — schema validation skipped", reg.Rel(reg.SchemaDir)),
		})
		return nil
	}

	// The generation manifest is a committed document like any other, so it is
	// held to its schema too.
	if manifestSchema := filepath.Join(reg.SchemaDir, "generation-manifest.schema.json"); isFile(manifestSchema) {
		if isFile(reg.ManifestPath()) {
			if compiled, err := compileSchema(manifestSchema); err == nil {
				validateRecord(reg, rep, compiled, Record{
					Path:   reg.ManifestPath(),
					Fields: loadYAMLFields(reg.ManifestPath()),
				})
			}
		}
	}

	if taxonomySchema := filepath.Join(reg.SchemaDir, "taxonomy.schema.json"); isFile(taxonomySchema) {
		if isFile(reg.TaxonomyPath()) {
			if compiled, err := compileSchema(taxonomySchema); err == nil {
				validateRecord(reg, rep, compiled, Record{
					Path:   reg.TaxonomyPath(),
					Fields: loadYAMLFields(reg.TaxonomyPath()),
				})
			}
		}
	}

	if glossarySchema := filepath.Join(reg.SchemaDir, "glossary.schema.json"); isFile(glossarySchema) {
		if isFile(reg.GlossaryPath()) {
			if compiled, err := compileSchema(glossarySchema); err == nil {
				validateRecord(reg, rep, compiled, Record{
					Path:   reg.GlossaryPath(),
					Fields: loadYAMLFields(reg.GlossaryPath()),
				})
			}
		}
	}

	// A product's declaration inside the registry is a committed document like
	// the three above, held to its schema where the project carries one.
	for _, document := range registeredProductDocuments() {
		path := document.Path(reg)
		if productSchema := filepath.Join(reg.SchemaDir, document.Schema); isFile(productSchema) {
			if isFile(path) {
				if compiled, err := compileSchema(productSchema); err == nil {
					validateRecord(reg, rep, compiled, Record{
						Path:   path,
						Fields: loadYAMLFields(path),
					})
				}
			}
		}
	}

	for _, cat := range Categories {
		schemaPath := filepath.Join(reg.SchemaDir, SchemaFor(cat))
		if !isFile(schemaPath) {
			continue
		}
		records, err := reg.LoadCategory(cat)
		if err != nil || len(records) == 0 {
			continue
		}
		compiled, err := compileSchema(schemaPath)
		if err != nil {
			rep.add(Finding{
				Severity: SeverityError,
				Check:    "schema",
				Path:     reg.Rel(schemaPath),
				Message:  fmt.Sprintf("schema could not be compiled: %v", err),
			})
			continue
		}
		for _, rec := range records {
			validateRecord(reg, rep, compiled, rec)
		}
	}
	return nil
}

func compileSchema(path string) (*jsonschema.Schema, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	doc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(path, doc); err != nil {
		return nil, err
	}
	return c.Compile(path)
}

func validateRecord(reg Registry, rep *Report, schema *jsonschema.Schema, rec Record) {
	// The validator works on JSON-shaped values, so YAML types are normalised
	// before validation rather than after a failure.
	value := toJSONValue(rec.Fields)
	err := schema.Validate(value)
	if err == nil {
		return
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		rep.add(Finding{
			Severity: SeverityError, Check: "schema",
			Path: reg.Rel(rec.Path), ID: rec.ID, Message: err.Error(),
		})
		return
	}
	// Report leaf causes: they name the offending field, where the root error
	// only says the document is invalid.
	for _, leaf := range leafCauses(ve) {
		loc := strings.Join(leaf.InstanceLocation, "/")
		if loc == "" {
			loc = "(document root)"
		}
		rep.add(Finding{
			Severity: SeverityError,
			Check:    "schema",
			Path:     reg.Rel(rec.Path),
			ID:       rec.ID,
			Message:  fmt.Sprintf("%s: %s", loc, leaf.ErrorKind.LocalizedString(errorPrinter)),
		})
	}
}

func leafCauses(e *jsonschema.ValidationError) []*jsonschema.ValidationError {
	if len(e.Causes) == 0 {
		return []*jsonschema.ValidationError{e}
	}
	var out []*jsonschema.ValidationError
	for _, c := range e.Causes {
		out = append(out, leafCauses(c)...)
	}
	return out
}

// toJSONValue converts decoded YAML into the shapes the JSON Schema validator
// expects: string keys and float64 numbers.
func toJSONValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = toJSONValue(val)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[fmt.Sprint(k)] = toJSONValue(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = toJSONValue(val)
		}
		return out
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case uint64:
		return float64(t)
	default:
		return v
	}
}
