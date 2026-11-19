// @facet: registry/history/generation-freshness
// @facet-role: primary

package facet

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/goccy/go-yaml"
)

// ManifestName is the generation manifest's location inside the records dir.
const ManifestName = "generated.lock.yaml"

// Digest identifies content. Content hashing is deliberate: modification times
// are not preserved by git, so a fresh checkout would mark every artifact stale.
type Digest struct {
	SHA256 string `yaml:"sha256"`
}

// Input is one thing that fed a generated artifact.
type Input struct {
	Role   string `yaml:"role"` // record | anchor
	URI    string `yaml:"uri"`  // repo-relative path
	Digest Digest `yaml:"digest"`
}

// Subject is the generated artifact itself.
type Subject struct {
	Path   string `yaml:"path"`
	Digest Digest `yaml:"digest"`
}

// Generator records the non-hermetic part of the pipeline. An LLM is not a
// deterministic compiler, so the model and prompt version are inputs: changing
// either invalidates the artifact exactly as a new compiler would.
type Generator struct {
	Pipeline      string `yaml:"pipeline"`
	Model         string `yaml:"model,omitempty"`
	PromptVersion string `yaml:"prompt_version,omitempty"`
}

// Artifact is one manifest entry.
type Artifact struct {
	Subject     Subject   `yaml:"subject"`
	Feature     string    `yaml:"feature"`
	Slots       []string  `yaml:"slots,omitempty"`
	Generator   Generator `yaml:"generator"`
	GeneratedAt string    `yaml:"generated_at,omitempty"`
	Inputs      []Input   `yaml:"inputs"`
}

// Manifest is the committed record of what produced what.
type Manifest struct {
	SchemaVersion string     `yaml:"schema_version"`
	Artifacts     []Artifact `yaml:"artifacts"`
}

// ManifestPath returns where the manifest lives for a registry.
func (r Registry) ManifestPath() string {
	return filepath.Join(r.RecordsDir, ManifestName)
}

// LoadManifest reads the generation manifest. A missing file is not an error —
// a project that has generated nothing yet has an empty manifest.
func LoadManifest(path string) (Manifest, error) {
	m := Manifest{SchemaVersion: "0.1"}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	if err := yaml.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// Save writes the manifest, sorted so its diff stays reviewable.
func (m Manifest) Save(path string) error {
	sort.Slice(m.Artifacts, func(i, j int) bool {
		return m.Artifacts[i].Subject.Path < m.Artifacts[j].Subject.Path
	})
	if m.SchemaVersion == "" {
		m.SchemaVersion = "0.1"
	}
	data, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

// Upsert replaces the entry for an artifact path, or appends a new one.
func (m *Manifest) Upsert(a Artifact) {
	for i := range m.Artifacts {
		if m.Artifacts[i].Subject.Path == a.Subject.Path {
			m.Artifacts[i] = a
			return
		}
	}
	m.Artifacts = append(m.Artifacts, a)
}

// State is the freshness verdict for one artifact.
type State string

const (
	StateCurrent  State = "current"
	StateStale    State = "stale"
	StateOrphaned State = "orphaned" // the feature record no longer exists
	StateMissing  State = "missing"  // the artifact itself is gone
)

// Status is a checked artifact and why it landed in its state.
type Status struct {
	Artifact Artifact `json:"-"`
	Path     string   `json:"path"`
	Feature  string   `json:"feature"`
	State    State    `json:"state"`
	Reasons  []string `json:"reasons,omitempty"`
}

// CheckManifest recomputes every artifact's inputs and reports which have moved.
func CheckManifest(reg Registry, m Manifest, anchors []Anchor) []Status {
	byID := AnchorsByID(anchors)
	statuses := make([]Status, 0, len(m.Artifacts))

	for _, a := range m.Artifacts {
		st := Status{Artifact: a, Path: a.Subject.Path, Feature: a.Feature, State: StateCurrent}

		if !isFile(filepath.Join(reg.Root, a.Subject.Path)) {
			st.State = StateMissing
			st.Reasons = append(st.Reasons, "the generated file no longer exists")
			statuses = append(statuses, st)
			continue
		}
		if !isFile(reg.PathForID(a.Feature)) {
			st.State = StateOrphaned
			st.Reasons = append(st.Reasons, fmt.Sprintf("feature record %q no longer exists", a.Feature))
			statuses = append(statuses, st)
			continue
		}

		recorded := make(map[string]string, len(a.Inputs))
		for _, in := range a.Inputs {
			recorded[in.URI] = in.Digest.SHA256
			actual, err := HashFile(filepath.Join(reg.Root, in.URI))
			switch {
			case err != nil:
				st.Reasons = append(st.Reasons, fmt.Sprintf("input %s can no longer be read", in.URI))
			case actual != in.Digest.SHA256:
				st.Reasons = append(st.Reasons, fmt.Sprintf("input %s changed since generation", in.URI))
			}
		}

		// Re-resolve the anchor set rather than trusting the recorded list.
		for _, p := range AnchorPaths(byID[a.Feature]) {
			rel := reg.Rel(p)
			if _, known := recorded[rel]; !known {
				st.Reasons = append(st.Reasons, fmt.Sprintf("anchor %s was added after generation", rel))
			}
		}
		for _, in := range a.Inputs {
			if in.Role != "anchor" {
				continue
			}
			if !anchorStillPresent(reg, byID[a.Feature], in.URI) {
				st.Reasons = append(st.Reasons, fmt.Sprintf("anchor %s no longer implements this feature", in.URI))
			}
		}

		if len(st.Reasons) > 0 {
			st.State = StateStale
		}
		statuses = append(statuses, st)
	}
	return statuses
}

func anchorStillPresent(reg Registry, anchors []Anchor, uri string) bool {
	for _, p := range AnchorPaths(anchors) {
		if reg.Rel(p) == uri {
			return true
		}
	}
	return false
}

// BuildArtifact assembles a manifest entry from the current state of a feature:
// the record plus every file that anchors it.
func BuildArtifact(reg Registry, feature, subjectPath string, gen Generator, slots []string, anchors []Anchor) (Artifact, error) {
	a := Artifact{Feature: feature, Generator: gen, Slots: slots}

	abs := filepath.Join(reg.Root, subjectPath)
	sum, err := HashFile(abs)
	if err != nil {
		return a, fmt.Errorf("generated artifact %s: %w", subjectPath, err)
	}
	a.Subject = Subject{Path: filepath.ToSlash(subjectPath), Digest: Digest{SHA256: sum}}

	recPath := reg.PathForID(feature)
	recSum, err := HashFile(recPath)
	if err != nil {
		return a, fmt.Errorf("record for %s: %w", feature, err)
	}
	a.Inputs = append(a.Inputs, Input{Role: "record", URI: reg.Rel(recPath), Digest: Digest{SHA256: recSum}})

	for _, p := range AnchorPaths(AnchorsByID(anchors)[feature]) {
		sum, err := HashFile(p)
		if err != nil {
			continue
		}
		a.Inputs = append(a.Inputs, Input{Role: "anchor", URI: reg.Rel(p), Digest: Digest{SHA256: sum}})
	}
	return a, nil
}

// HashFile returns the SHA-256 of a file's contents.
func HashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// writeFileAtomic writes via a temporary file and a rename, so an interrupted
// write cannot truncate a file a human authored.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".facet-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
