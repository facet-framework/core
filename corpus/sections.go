// @facet: delivery/distribution/registry-snapshot
// @facet-role: related
package corpus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/facet-framework/core/registry"
)

// SectionBuilder builds one product section for a registry. The second value
// is false when the project has nothing for this section, which leaves the key
// out rather than shipping an empty value.
type SectionBuilder func(reg facet.Registry) (any, bool)

var sectionBuilders = struct {
	sync.Mutex
	builders map[string]SectionBuilder
}{builders: map[string]SectionBuilder{}}

// RegisterSection adds a product section under name. Builds register from
// init. It panics on an empty name, a name Core's own fields already use, or
// a name registered twice: two writers of one key would make the value a
// consumer reads depend on which ran last.
func RegisterSection(name string, build SectionBuilder) {
	if name == "" || build == nil {
		panic("corpus: RegisterSection needs a name and a builder")
	}
	if coreField(name) {
		panic(fmt.Sprintf("corpus: section %q is a Core corpus field", name))
	}
	sectionBuilders.Lock()
	defer sectionBuilders.Unlock()
	if _, taken := sectionBuilders.builders[name]; taken {
		panic(fmt.Sprintf("corpus: section %q is registered twice", name))
	}
	sectionBuilders.builders[name] = build
}

// buildSections runs every registered builder for one registry.
func buildSections(reg facet.Registry) map[string]any {
	sectionBuilders.Lock()
	builders := make(map[string]SectionBuilder, len(sectionBuilders.builders))
	for name, build := range sectionBuilders.builders {
		builders[name] = build
	}
	sectionBuilders.Unlock()
	var out map[string]any
	for name, build := range builders {
		if value, ok := build(reg); ok {
			if out == nil {
				out = map[string]any{}
			}
			out[name] = value
		}
	}
	return out
}

// Section returns the value a product contributed under name, or nil.
func (c *Corpus) Section(name string) any {
	if c == nil {
		return nil
	}
	return c.sections[name]
}

// coreField reports whether name is the JSON key of one of Corpus's fields.
func coreField(name string) bool {
	data, err := json.Marshal(plainCorpus{})
	if err != nil {
		return false
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return false
	}
	_, ok := keys[name]
	return ok
}

// plainCorpus is Corpus without its MarshalJSON, so the method can encode
// Core's fields the ordinary way.
type plainCorpus Corpus

// MarshalJSON writes Core's fields as they are declared, then each product
// section in name order.
func (c Corpus) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(plainCorpus(c))
	if err != nil || len(c.sections) == 0 {
		return data, err
	}
	names := make([]string, 0, len(c.sections))
	for name := range c.sections {
		names = append(names, name)
	}
	sort.Strings(names)
	var b bytes.Buffer
	b.Write(bytes.TrimSuffix(bytes.TrimSpace(data), []byte("}")))
	for _, name := range names {
		key, err := json.Marshal(name)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(c.sections[name])
		if err != nil {
			return nil, err
		}
		b.WriteByte(',')
		b.Write(key)
		b.WriteByte(':')
		b.Write(value)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}
