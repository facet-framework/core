// @facet: registry/layout/product-taxonomy
// @facet-role: primary

package facet

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// TaxonomyName is the deployment's product decomposition inside the records dir.
const TaxonomyName = "taxonomy.yaml"

// Classification is how much of the product's reason to exist lives in a node:
// Evans' core / supporting / generic subdomain. Strategic weight only — it is
// orthogonal to depth, which is already the id.
type Classification string

const (
	ClassificationCore       Classification = "core"
	ClassificationSupporting Classification = "supporting"
	ClassificationGeneric    Classification = "generic"
)

type taxonomyOwner struct {
	Team string `yaml:"team"`
}

// CapabilityNode is one capability: the second segment of an id.
type CapabilityNode struct {
	Title          string         `yaml:"title"`
	Purpose        string         `yaml:"purpose"`
	Classification Classification `yaml:"classification"`
}

// DomainNode is one product domain: the first segment of an id.
type DomainNode struct {
	Title          string                    `yaml:"title"`
	Purpose        string                    `yaml:"purpose"`
	Classification Classification            `yaml:"classification"`
	Owner          *taxonomyOwner            `yaml:"owner"`
	Capabilities   map[string]CapabilityNode `yaml:"capabilities"`
}

// Taxonomy declares the product decomposition ids are drawn from.
type Taxonomy struct {
	SchemaVersion string                `yaml:"schema_version"`
	Domains       map[string]DomainNode `yaml:"domains"`
}

// Configured reports whether the project declares a taxonomy at all. A project
// without one has no taxonomy constraints — the same rule that governs
// the variant map, and the reason adoption is a decision rather than a
// migration.
func (t *Taxonomy) Configured() bool { return t != nil && len(t.Domains) > 0 }

// TaxonomyPath returns where the taxonomy lives for a registry.
func (r Registry) TaxonomyPath() string {
	return filepath.Join(r.RecordsDir, TaxonomyName)
}

// LoadTaxonomy reads the deployment's product decomposition. A missing file is
// not an error — it means the project has not declared one.
func LoadTaxonomy(path string) (*Taxonomy, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Taxonomy{}, nil
	}
	if err != nil {
		return nil, err
	}
	var t Taxonomy
	if err := yaml.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// DomainNames lists the declared domains, sorted.
func (t *Taxonomy) DomainNames() []string {
	if t == nil {
		return nil
	}
	names := make([]string, 0, len(t.Domains))
	for n := range t.Domains {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// CapabilityNames lists the capabilities declared under a domain, sorted.
func (t *Taxonomy) CapabilityNames(domain string) []string {
	if t == nil {
		return nil
	}
	d, ok := t.Domains[domain]
	if !ok {
		return nil
	}
	names := make([]string, 0, len(d.Capabilities))
	for n := range d.Capabilities {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// DomainDeclared reports whether a domain segment exists in the taxonomy.
func (t *Taxonomy) DomainDeclared(domain string) bool {
	if t == nil {
		return false
	}
	_, ok := t.Domains[domain]
	return ok
}

// CapabilityDeclared reports whether a capability exists under a domain.
func (t *Taxonomy) CapabilityDeclared(domain, capability string) bool {
	if t == nil {
		return false
	}
	d, ok := t.Domains[domain]
	if !ok {
		return false
	}
	_, ok = d.Capabilities[capability]
	return ok
}

// ClassificationOf reports a node's strategic weight, inheriting the domain's
// value when the capability declares none.
func (t *Taxonomy) ClassificationOf(domain, capability string) Classification {
	if t == nil {
		return ""
	}
	d, ok := t.Domains[domain]
	if !ok {
		return ""
	}
	if capability != "" {
		if c, ok := d.Capabilities[capability]; ok && c.Classification != "" {
			return c.Classification
		}
	}
	return d.Classification
}

// OwnerTeam reports the team answering for a domain by default, if declared.
// It informs a record's lineage.owner and never replaces it.
func (t *Taxonomy) OwnerTeam(domain string) string {
	if t == nil {
		return ""
	}
	d, ok := t.Domains[domain]
	if !ok || d.Owner == nil {
		return ""
	}
	return d.Owner.Team
}

// SplitSegments breaks a canonical id into its three segments. The last return
// value reports whether the id had exactly three non-empty segments; a caller
// holding a schema-valid record can ignore it, and a caller reading an anchor
// cannot. Named apart from SplitID, which separates a registry prefix.
func SplitSegments(id string) (domain, capability, feature string, ok bool) {
	parts := strings.Split(id, "/")
	if len(parts) != 3 {
		return "", "", "", false
	}
	for _, p := range parts {
		if p == "" {
			return "", "", "", false
		}
	}
	return parts[0], parts[1], parts[2], true
}
