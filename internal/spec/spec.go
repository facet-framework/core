// Package spec reads specification sources and pins their addressable units.
package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Format string

const (
	FacetSpec       Format = "facet-spec"
	Gherkin         Format = "gherkin"
	OpenSpec        Format = "openspec"
	SpecKit         Format = "speckit"
	GherkinMarkdown Format = "gherkin-markdown"
	Kiro            Format = "kiro"
	BMAD            Format = "bmad"
	Gauge           Format = "gauge"
)

type BindingStrength string

const (
	Exact          BindingStrength = "exact"
	Provenance     BindingStrength = "provenance"
	UnknownBinding BindingStrength = "unknown"
)

// Adapter reads one file within a source and returns its addressable units.
type Adapter interface {
	Read(root, path string, data []byte) (Document, []Finding)
	Matches(relativePath string) bool
}

type Document struct {
	ID       string
	Status   string
	Language string
	Skipped  bool
	Units    []Unit
	blocks   []FeatureBlock
}

type Unit struct {
	Address       string
	Line          int
	Digest        string
	ContextDigest string
	Binding       BindingStrength
	Text          string
	Sequence      *Sequence
	block         int
}

func (u Unit) Strength() BindingStrength {
	return u.Binding
}

func (f Format) Strength() BindingStrength {
	if f == SpecKit || f == Kiro || f == BMAD || f == Gauge {
		return Provenance
	}
	return Exact
}

type Finding struct {
	File    string          `json:"file"`
	Line    int             `json:"line"`
	Message string          `json:"message"`
	Binding BindingStrength `json:"binding"`
}

func (f Finding) Error() string {
	return fmt.Sprintf("%s:%d: [%s] %s", f.File, f.Line, f.Binding, f.Message)
}

func AdapterFor(format Format) (Adapter, error) {
	switch format {
	case FacetSpec:
		return markdownAdapter{}, nil
	case Gherkin:
		return featureAdapter{}, nil
	case OpenSpec:
		return openSpecAdapter{}, nil
	case SpecKit:
		return specKitAdapter{}, nil
	case GherkinMarkdown:
		return gherkinMarkdownAdapter{}, nil
	case Kiro:
		return kiroAdapter{}, nil
	case BMAD:
		return bmadAdapter{}, nil
	case Gauge:
		return gaugeAdapter{}, nil
	default:
		return nil, fmt.Errorf("unsupported spec format %q", format)
	}
}

var facetSpecMarker = regexp.MustCompile(`(?m)\A---\r?\n(?:.*\r?\n)*?schema:\s*["']?facet-spec/`)

// @facet: registry/integrity/spec-lint
// @facet-role: primary

// ReadFile recognises file extensions and default layouts for local linting.
func ReadFile(root, path string) (Document, []Finding) {
	format, givenRoot := FacetSpec, root
	var location string
	switch filepath.Ext(path) {
	case ".feature", ".md", ".spec":
		var err error
		location, err = filepath.Abs(path)
		if err != nil {
			return Document{}, []Finding{{path, 1, err.Error(), UnknownBinding}}
		}
		name := filepath.Base(location)
		dir := filepath.Dir(location)
		if InOpenSpecChanges(dir) {
			return Document{Skipped: true}, nil
		}
		collection := filepath.Dir(dir)
		switch {
		case strings.HasSuffix(name, gherkinMarkdownSuffix):
			format = GherkinMarkdown
		case filepath.Ext(path) == ".spec":
			// .spec is also used by RPM and JavaScript test files; only a
			// specs/ tree, Gauge's layout, makes it a Gauge specification.
			specs := gaugeSpecsRoot(dir)
			if specs == "" {
				return Document{Skipped: true}, nil
			}
			format, root = Gauge, specs
		case filepath.Ext(path) == ".feature":
			format = Gherkin
		case name == "requirements.md" && filepath.Base(collection) == "specs" && filepath.Base(filepath.Dir(collection)) == ".kiro":
			format, root = Kiro, collection
		case strings.Contains(filepath.ToSlash(location), "/_bmad-output/implementation-artifacts/"):
			format = BMAD
		case name == "spec.md" && filepath.Base(collection) == "specs" &&
			filepath.Base(filepath.Dir(collection)) == "openspec":
			format, root = OpenSpec, collection
		case name == "spec.md" && filepath.Base(collection) == "specs" && numberedSpecID.MatchString(filepath.Base(dir)):
			format, root = SpecKit, collection
		}
	default:
		return Document{Skipped: true}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Document{}, []Finding{{path, 1, err.Error(), format.Strength()}}
	}
	if filepath.Ext(path) == ".md" && format != GherkinMarkdown {
		// A FACET specification declares its schema; the layout is only a guess.
		if facetSpecMarker.Match(data) {
			format, root = FacetSpec, givenRoot
		} else if format == FacetSpec {
			// Gauge accepts Markdown specifications; its project manifest next
			// to specs/ distinguishes them from other Markdown under specs/.
			if specs := gaugeSpecsRoot(filepath.Dir(location)); specs != "" {
				if _, err := os.Stat(filepath.Join(filepath.Dir(specs), "manifest.json")); err == nil {
					format, root = Gauge, specs
				}
			}
		}
	}
	adapter, err := AdapterFor(format)
	if err != nil {
		return Document{}, []Finding{{path, 1, err.Error(), format.Strength()}}
	}
	if format == Gauge {
		absoluteRoot, err := filepath.Abs(root)
		if err != nil {
			return Document{}, []Finding{{path, 1, err.Error(), Provenance}}
		}
		return adapter.Read(absoluteRoot, location, data)
	}
	return adapter.Read(root, path, data)
}

func InOpenSpecChanges(path string) bool {
	return strings.Contains("/"+filepath.ToSlash(path)+"/", "/openspec/changes/")
}

type featureAdapter struct{}

func (featureAdapter) Matches(relativePath string) bool {
	return filepath.Ext(relativePath) == ".feature"
}

func (featureAdapter) Read(root, path string, data []byte) (Document, []Finding) {
	return readFeature(root, path, ".feature", string(data))
}

func readFeature(root, path, suffix, body string) (Document, []Finding) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Document{}, []Finding{{path, 1, err.Error(), Exact}}
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return Document{}, []Finding{{path, 1, err.Error(), Exact}}
	}
	rel, err := filepath.Rel(root, absolute)
	if err != nil {
		return Document{}, []Finding{{path, 1, err.Error(), Exact}}
	}
	doc := Document{ID: strings.TrimSuffix(filepath.ToSlash(rel), suffix), Status: "approved"}
	block := sourceBlock{body: body, line: 1}
	findings := doc.readBlocks(path, []sourceBlock{block}, defaults())
	return doc, findings
}
