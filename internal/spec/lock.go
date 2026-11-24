package spec

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/facet-framework/core/schema"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

const LockName = "docs/facets/spec-lock.yaml"

type Lock map[string]LockedSource

type LockedSource struct {
	Repo      string                    `yaml:"repo"`
	Follow    string                    `yaml:"follow"`
	Format    Format                    `yaml:"format"`
	Commit    string                    `yaml:"commit,omitempty"`
	Files     map[string]LockedFile     `yaml:"files"`
	Scenarios map[string]LockedScenario `yaml:"scenarios"`
}

type LockedFile struct {
	Status   string `yaml:"status"`
	Language string `yaml:"language"`
}

// @facet: registry/integrity/spec-adapters
// @facet-role: data

type LockedScenario struct {
	Commit   string          `yaml:"commit"`
	Binding  BindingStrength `yaml:"binding,omitempty"`
	Steps    string          `yaml:"steps"`
	Context  string          `yaml:"context,omitempty"`
	Sequence *Sequence       `yaml:"sequence,omitempty"`
	Text     string          `yaml:"text,omitempty"`
}

func (s LockedScenario) Strength() BindingStrength {
	if s.Binding == "" {
		return Exact
	}
	return s.Binding
}

func (s LockedScenario) FollowDiff(current LockedScenario) string {
	if s.Strength() == Provenance {
		return textDiff(textLines(s.Text), textLines(current.Text), "follow")
	}
	var after Sequence
	if current.Sequence != nil {
		after = *current.Sequence
	}
	return FollowDiff(*s.Sequence, after)
}

var lockSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	const name = "spec-lock.schema.json"
	file, err := schema.Files.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := jsonschema.UnmarshalJSON(file)
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(name, raw); err != nil {
		return nil, err
	}
	return compiler.Compile(name)
})

func ReadLock(root string) (Lock, error) {
	data, err := os.ReadFile(filepath.Join(root, LockName))
	if err != nil {
		return nil, err
	}
	return decodeLock(data)
}

func decodeLock(data []byte) (Lock, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("expected a single YAML document")
	}
	schema, err := lockSchema()
	if err != nil {
		return nil, err
	}
	if err := schema.Validate(raw); err != nil {
		return nil, err
	}
	var lock Lock
	if err := yaml.Unmarshal(data, &lock); err != nil {
		return nil, err
	}
	for _, id := range sortedKeys(lock) {
		source := lock[id]
		for _, file := range sortedKeys(source.Files) {
			if dialectFor(source.Files[file].Language) == nil {
				return nil, fmt.Errorf("%s:%s: unsupported Gherkin language", id, file)
			}
		}
		for _, address := range sortedKeys(source.Scenarios) {
			scenario := source.Scenarios[address]
			file, _, _ := strings.Cut(address, "#")
			if _, ok := source.Files[file]; !ok {
				return nil, fmt.Errorf("%s:%s: file metadata is missing", id, address)
			}
			if scenario.Strength() != source.Format.Strength() {
				return nil, fmt.Errorf("%s:%s: %s binding does not match source format %q", id, address, scenario.Strength(), source.Format)
			}
			if scenario.Strength() == Provenance {
				normalised := normaliseText(scenario.Text) == scenario.Text
				if source.Format == Gauge {
					normalised = normalisedGaugeSteps(scenario.Text)
				}
				if !normalised || digest(scenario.Text) != scenario.Steps {
					return nil, fmt.Errorf("%s:%s: provenance digest does not match the normalised text", id, address)
				}
				continue
			}
			if scenario.Sequence.Digest() != scenario.Steps {
				return nil, fmt.Errorf("%s:%s: step digest does not match the sequence", id, address)
			}
		}
	}
	return lock, nil
}

func encodeLock(lock Lock) ([]byte, error) {
	var node yaml.Node
	if err := node.Encode(lock); err != nil {
		return nil, err
	}
	sortMappings(&node)
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&node); err != nil {
		return nil, err
	}
	return out.Bytes(), encoder.Close()
}

func sortMappings(node *yaml.Node) {
	for _, child := range node.Content {
		sortMappings(child)
	}
	if node.Kind != yaml.MappingNode {
		return
	}
	pairs := make([][2]*yaml.Node, 0, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		pairs = append(pairs, [2]*yaml.Node{node.Content[i], node.Content[i+1]})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i][0].Value < pairs[j][0].Value })
	for i, pair := range pairs {
		node.Content[2*i], node.Content[2*i+1] = pair[0], pair[1]
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
