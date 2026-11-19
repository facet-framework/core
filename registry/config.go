// @facet: registry/layout/multi-registry
// @facet-role: primary

package facet

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/facet-framework/core/internal/sourceview"
	"github.com/goccy/go-yaml"
)

// ConfigName is the project's committed FACET configuration.
const ConfigName = ".facetrc.yaml"

// DefaultRecordsDir is where records live when nothing says otherwise.
const DefaultRecordsDir = "docs/facets"

// DefaultRegistryName is the implicit registry of a project that declares none.
// It is never printed for single-registry projects, where naming a registry the
// author never chose would be noise.
const DefaultRegistryName = "default"

type registryEntry struct {
	Runners     map[string]Runner `yaml:"runners"`
	Records     string            `yaml:"records"`
	Schema      string            `yaml:"schema"`
	Sources     []string          `yaml:"sources"`
	Description string            `yaml:"description"`
	// Conventions overrides the project's conventions document for this
	// registry alone. It replaces the shared one; the two are never merged.
	Conventions string `yaml:"conventions"`
}

// Config is the parsed .facetrc.yaml.
type Config struct {
	SourceViews *sourceview.Config `yaml:"source_views,omitempty"`
	sourceView  *sourceview.ResolvedView

	// TestFeaturePrefix explicitly limits test evidence to one product area.
	TestFeaturePrefix   string   `yaml:"test_feature_prefix"`
	TestFeaturePrefixes []string `yaml:"test_feature_prefixes"`

	// ADR configures the project-wide directory read by the ADR plugin.
	ADR  ADRConfig `yaml:"adr"`
	BPMN ADRConfig `yaml:"bpmn"`

	SchemaVersion string            `yaml:"schema_version"`
	Runners       map[string]Runner `yaml:"runners"`

	// Single-registry shorthand.
	Records string   `yaml:"records"`
	Schema  string   `yaml:"schema"`
	Sources []string `yaml:"sources"`

	// Multi-registry form.
	Registries map[string]registryEntry `yaml:"registries"`

	// Conventions names the project's authoring conventions document, shared
	// by every registry that does not declare its own. Absent means
	// FACET-CONVENTIONS.md at the root if it exists, and nothing otherwise.
	// See conventions.go for what the document is, and what it cannot do.
	Conventions string `yaml:"conventions"`

	// Embedding configures one retrieval index shared by all project registries.
	// Each indexed chunk retains its originating registry.
	Embedding EmbeddingConfig `yaml:"embedding"`

	// ErrorCodes configures the identifier pattern 'facet errors' and the
	// error-codes lint check scan for. It applies to the whole project, not
	// to one registry: a company-wide numbering convention spans every
	// registry the same way it spans every team.
	ErrorCodes errorCodesEntry `yaml:"error_codes"`

	// Forges names the forge behind a git host this tooling cannot recognise.
	ForgeHosts map[string]string `yaml:"forges"`

	// Hooks holds project-wide guard switches. Absent means all guards are on.
	// The binary pre-tool-use gate and shell fallback read it from HEAD;
	// commitGateOffAt implements the binary check in the CLI package.
	Hooks HooksConfig `yaml:"hooks"`

	// TrackerList is the 'trackers:' section: where the project keeps its
	// issues and how a ticket key maps to one of them. Read through Trackers().
	TrackerList []Tracker `yaml:"trackers"`

	// Path is where the config was read from, empty when none exists.
	Path string `yaml:"-"`
	root string
	// data is the file as read, kept for the product sections (product_sections.go).
	data []byte
}

// ADRConfig is the optional ADR reader configuration in .facetrc.yaml.
type ADRConfig struct {
	Directory string `yaml:"directory"`
}

// ADRDirectory returns the project ADR directory, including for a nil Config.
func (c *Config) ADRDirectory() string {
	if c != nil && c.ADR.Directory != "" {
		return c.ADR.Directory
	}
	return ADRReadDirectory
}

func (c *Config) BPMNDirectory() string {
	if c != nil && c.BPMN.Directory != "" {
		return c.BPMN.Directory
	}
	return "docs/bpmn"
}

// HooksConfig is the optional 'hooks:' section of .facetrc.yaml.
//
// @facet: authoring/guardrails/commit-gate
// @facet-role: config
type HooksConfig struct {
	// CommitGate is "on" (the default when empty) or "off". Off stops the
	// plugin's commit gate from judging this project's commits. The environment
	// variable FACET_SKIP_COMMIT_GATE=1 remains the per-session escape hatch and
	// wins over this. Only "on" and "off" are accepted: a typo such as "of"
	// must not read as either.
	CommitGate string `yaml:"commit_gate"`
}

// CommitGateOff reports whether the configuration switches the commit gate
// off. It answers false for a nil Config.
func (c *Config) CommitGateOff() bool {
	return c != nil && c.Hooks.CommitGate == "off"
}

// EmbeddingConfig says how to turn text into vectors, and where to keep them.
//
// @facet: navigation/retrieval/embedding-configuration
// @facet-role: config
type EmbeddingConfig struct {
	// Provider is "openai" for any OpenAI-compatible /embeddings endpoint —
	// which is nearly all of them, hosted and local alike — or "local" for the
	// lexical embedder built into this binary.
	Provider string `yaml:"provider"`
	// BaseURL is the API root, without the trailing /embeddings.
	BaseURL string `yaml:"base_url"`
	// Model is the embedding model's name at that endpoint.
	Model string `yaml:"model"`
	// APIKeyEnv names the environment variable holding the bearer token. Empty
	// is valid: a local Ollama or LM Studio wants no key.
	APIKeyEnv string `yaml:"api_key_env"`
	// Dimensions shortens the vector where the provider supports it. Zero
	// leaves the model's own width.
	Dimensions int `yaml:"dimensions"`
	// Batch is how many chunks go in one request. Zero uses the default.
	Batch int `yaml:"batch"`
	// Index is where the vectors are written, relative to the repository root.
	// Empty puts them beside the generation manifest.
	Index string `yaml:"index"`
}

// RAGIndexName is the index's file name inside the records directory.
const RAGIndexName = "generated.rag.bin"

// RAGIndexPath returns where a registry's vector index lives by default.
func (r Registry) RAGIndexPath() string {
	return filepath.Join(r.RecordsDir, RAGIndexName)
}

// EmbeddingSettings returns the embedding configuration with environment
// overrides applied.
func (c *Config) EmbeddingSettings() EmbeddingConfig {
	e := EmbeddingConfig{}
	if c != nil {
		e = c.Embedding
	}
	if v := os.Getenv("FACET_EMBEDDING_PROVIDER"); v != "" {
		e.Provider = v
	}
	if v := os.Getenv("FACET_EMBEDDING_BASE_URL"); v != "" {
		e.BaseURL = v
	}
	if v := os.Getenv("FACET_EMBEDDING_MODEL"); v != "" {
		e.Model = v
	}
	if v := os.Getenv("FACET_EMBEDDING_API_KEY_ENV"); v != "" {
		e.APIKeyEnv = v
	}
	if v := os.Getenv("FACET_RAG_INDEX"); v != "" {
		e.Index = v
	}
	return e
}

// IndexPath resolves where the vector index lives for a project.
func (c *Config) IndexPath(reg Registry) string {
	if e := c.EmbeddingSettings(); e.Index != "" {
		return absUnder(reg.Root, e.Index)
	}
	return reg.RAGIndexPath()
}

// errorCodesEntry configures identifier extraction. The pattern is a project
// convention (an E\d{4} band is the default), never a FACET concept,
// which is why it lives here rather than in the record schema.
type errorCodesEntry struct {
	Pattern string `yaml:"pattern"`
}

// ErrorCodePattern returns the project's configured identifier pattern, or
// DefaultErrorCodePattern when none is declared — including for a nil
// Config, so a caller that skipped LoadConfig still gets the default
// rather than an empty pattern.
func (c *Config) ErrorCodePattern() string {
	if c != nil && c.ErrorCodes.Pattern != "" {
		return c.ErrorCodes.Pattern
	}
	return DefaultErrorCodePattern
}

// ConfigPath returns where a project's configuration lives.
func ConfigPath(root string) string { return filepath.Join(root, ConfigName) }

// LoadConfig reads project configuration. A missing file is not an error;
// the project uses the default layout without requiring configuration.
func LoadConfig(root string) (*Config, error) { return LoadConfigWithSourceView(root, "") }

// LoadConfigWithSourceView resolves the invocation's explicit view without
// changing process environment or any checkout. Empty uses FACET_SOURCE_VIEW,
// then the shared default. Registry records remain rooted in the same project.
func LoadConfigWithSourceView(root, selected string) (*Config, error) {
	return loadConfig(root, selected, nil, false)
}

// LoadConfigWithResolvedSourceView keeps the command's already selected
// checkout bindings, without reading or resolving local bindings again.
func LoadConfigWithResolvedSourceView(root string, frozen *sourceview.ResolvedView) (*Config, error) {
	return loadConfig(root, "", frozen, false)
}

// LoadConfigUnresolved validates committed configuration without consulting local
// checkout bindings or invocation selection. Revision policy readers use this.
func LoadConfigUnresolved(root string) (*Config, error) {
	return loadConfig(root, "", nil, true)
}

func loadConfig(root, selected string, frozen *sourceview.ResolvedView, unresolved bool) (*Config, error) {
	path := ConfigPath(root)
	cfg := &Config{root: root}

	data, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		if !unresolved && (frozen != nil || selected != "" || os.Getenv(sourceview.EnvName) != "") {
			return cfg, sourceview.SelectionError("no source_views configuration is declared")
		}
		return cfg, nil
	case err != nil:
		return nil, err
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", ConfigName, err)
	}
	if err := validateRunnerFields(data); err != nil {
		return nil, fmt.Errorf("%s: %w", ConfigName, err)
	}
	if cfg.ADR.Directory != "" && !ValidReadDir(cfg.ADR.Directory) {
		return nil, fmt.Errorf("%s: adr.directory %q must be a directory inside the repository, relative to its root", ConfigName, cfg.ADR.Directory)
	}
	if cfg.BPMN.Directory != "" && !ValidReadDir(cfg.BPMN.Directory) {
		return nil, fmt.Errorf("invalid bpmn.directory")
	}
	cfg.Path = path
	cfg.root = root
	// 'requires:' is deliberately not read here (see CorePin in corepin.go).
	if err := compileTrackers(cfg.TrackerList); err != nil {
		return nil, fmt.Errorf("%s: %w", ConfigName, err)
	}

	if len(cfg.Registries) > 0 && (cfg.Records != "" || cfg.Schema != "" || len(cfg.Sources) > 0 || len(cfg.Runners) > 0) {
		return nil, fmt.Errorf(
			"%s declares both top-level records/schema/sources/runners and a registries block; "+
				"use one or the other, so there is no question which the linter means", ConfigName)
	}
	for name := range cfg.Registries {
		if !validRegistryName(name) {
			return nil, fmt.Errorf(
				"%s: registry name %q must be lowercase letters, digits and hyphens — "+
					"it appears in ids as a prefix", ConfigName, name)
		}
	}
	if cfg.Conventions != "" && !validConventionsPath(cfg.Conventions) {
		return nil, fmt.Errorf(
			"%s: conventions %q must be a path inside the repository, relative to its root",
			ConfigName, cfg.Conventions)
	}
	for name, entry := range cfg.Registries {
		if entry.Conventions != "" && !validConventionsPath(entry.Conventions) {
			return nil, fmt.Errorf(
				"%s: registry %q: conventions %q must be a path inside the repository, relative to its root",
				ConfigName, name, entry.Conventions)
		}
	}
	switch cfg.Hooks.CommitGate {
	case "", "on", "off":
	default:
		return nil, fmt.Errorf("%s: hooks.commit_gate %q must be on or off", ConfigName, cfg.Hooks.CommitGate)
	}
	for host, forge := range cfg.ForgeHosts {
		if !validForgeHost(host) {
			return nil, fmt.Errorf(
				"%s: %q is not a host — 'forges:' is keyed by the host of the git remote "+
					"(git.example.com), not by a URL", ConfigName, host)
		}
		// Refused here rather than ignored later: a project that wrote 'gitea'
		// said what it meant, and silently reading it as "unknown" would leave
		// the surface reporting no forge for a host somebody had declared.
		if forge != "github" && forge != "gitlab" {
			return nil, fmt.Errorf(
				"%s: forge %q for %q is not one this tool can open a pull request on — "+
					"github or gitlab, because those are the two CLIs it drives",
				ConfigName, forge, host)
		}
	}
	cfg.data = data
	if err := validateProductSections(cfg); err != nil {
		return nil, err
	}
	if err := validateRunners(cfg.Runners); err != nil {
		return nil, err
	}
	for _, entry := range cfg.Registries {
		if err := validateRunners(entry.Runners); err != nil {
			return nil, err
		}
	}
	if unresolved {
		if cfg.SourceViews != nil {
			if err := cfg.SourceViews.Validate(); err != nil {
				return cfg, err
			}
		}
		return cfg, nil
	}
	if frozen != nil {
		if cfg.SourceViews == nil {
			return cfg, sourceview.SelectionError("the frozen source view has no shared configuration")
		}
		if err := cfg.SourceViews.Validate(); err != nil {
			return cfg, err
		}
		declared, ok := cfg.SourceViews.Views[frozen.Name]
		if !ok || len(declared.Sources) != len(frozen.Sources) || cfg.SourceViews.RegistryRepository != frozen.RegistryRepository {
			return cfg, sourceview.SelectionError("shared source-view selection changed during this invocation")
		}
		for repository, selection := range declared.Sources {
			actual, err := frozen.Source(repository)
			if err != nil {
				return cfg, err
			}
			if selection.Binding != actual.Binding || selection.Ref != actual.Ref {
				return cfg, sourceview.SelectionError("shared source-view binding changed during this invocation")
			}
		}
		cfg.sourceView = frozen
		return cfg, nil
	}
	if cfg.SourceViews != nil || selected != "" || os.Getenv(sourceview.EnvName) != "" {
		view, err := cfg.ResolveSourceView(selected)
		if err != nil {
			cfg.sourceView = sourceview.Failed(cfg.sourceViewName(selected), err)
			return cfg, err
		}
		cfg.sourceView = view
	}
	return cfg, nil
}

func (c *Config) sourceViewName(selected string) string {
	if selected != "" {
		return selected
	}
	if value := os.Getenv(sourceview.EnvName); value != "" {
		return value
	}
	if c != nil && c.SourceViews != nil {
		if c.SourceViews.DefaultView != "" {
			return c.SourceViews.DefaultView
		}
		if len(c.SourceViews.Views) == 1 {
			for name := range c.SourceViews.Views {
				return name
			}
		}
	}
	return ""
}

// ResolveSourceView resolves one explicit selection. Configuration readers
// normally use the already frozen Registry.SourceView created by LoadConfig.
func (c *Config) ResolveSourceView(selected string) (*sourceview.ResolvedView, error) {
	if c == nil || c.SourceViews == nil {
		return nil, sourceview.SelectionError("no source_views configuration is declared")
	}
	root := c.root
	if root == "" {
		root = "."
	}
	return sourceview.Resolve(root, *c.SourceViews, selected)
}

// validForgeHost reports whether a key is a bare host.
func validForgeHost(host string) bool {
	if host == "" || strings.ContainsAny(host, "/:@ \t") {
		return false
	}
	return !strings.HasPrefix(host, ".") && !strings.HasSuffix(host, ".")
}

// Forges returns the project's forge declarations, empty when it makes none —
// including for a nil Config, so a caller that skipped LoadConfig gets the
// behaviour of a project that declares nothing rather than a panic.
func (c *Config) Forges() map[string]string {
	if c == nil {
		return nil
	}
	return c.ForgeHosts
}

// validRegistryName mirrors the id segment grammar. A registry name prefixes an
// id, so anything the id grammar rejects would produce an unparseable anchor.
func validRegistryName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9', r == '-':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// MultiRegistry reports whether the project declares more than the implicit one.
func (c *Config) MultiRegistry() bool { return c != nil && len(c.Registries) > 0 }

// Names lists the declared registries, sorted. Empty for a single-registry
// project — the implicit registry has no name its author would recognise.
func (c *Config) Names() []string {
	if !c.MultiRegistry() {
		return nil
	}
	names := make([]string, 0, len(c.Registries))
	for n := range c.Registries {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Description returns a registry's prose description, if it declared one.
func (c *Config) Description(name string) string {
	if c == nil {
		return ""
	}
	return c.Registries[name].Description
}

// All builds every registry the project declares.
func (c *Config) All() []Registry {
	if c == nil {
		return []Registry{Open(".")}
	}
	if !c.MultiRegistry() {
		return []Registry{c.single()}
	}
	out := make([]Registry, 0, len(c.Registries))
	for _, name := range c.Names() {
		entry := c.Registries[name]
		records := entry.Records
		if records == "" {
			records = DefaultRecordsDir
		}
		reg := c.build(name, records, entry.Schema, entry.Sources)
		reg.ConventionsPath, reg.ConventionsSource = resolveConventions(reg.Root, entry.Conventions, c.Conventions)
		out = append(out, reg)
	}
	return out
}

// Select returns one registry by name. An empty name means the first, which for
// a single-registry project is the only one.
func (c *Config) Select(name string) (Registry, error) {
	all := c.All()
	if name == "" {
		return all[0], nil
	}
	for _, r := range all {
		if r.Name == name {
			return r, nil
		}
	}
	if !c.MultiRegistry() {
		return Registry{}, fmt.Errorf("this project declares no registries, so %q cannot be selected", name)
	}
	return Registry{}, fmt.Errorf("no registry named %q — declared: %s", name, strings.Join(c.Names(), ", "))
}

// single builds the implicit registry, letting the environment override the
// file. The overrides survive because CI jobs and migration scripts set them to
// point the linter at a scratch tree; a committed file cannot express that.
func (c *Config) single() Registry {
	records := c.Records
	if v := os.Getenv("FACET_RECORDS_DIR"); v != "" {
		records = v
	}
	if records == "" {
		records = DefaultRecordsDir
	}
	schema := c.Schema
	if v := os.Getenv("FACET_SCHEMA_DIR"); v != "" {
		schema = v
	}
	sources := c.Sources
	if v := strings.Fields(os.Getenv("FACET_SOURCE_DIRS")); len(v) > 0 {
		sources = v
	}
	reg := c.build("", records, schema, sources)
	reg.ConventionsPath, reg.ConventionsSource = resolveConventions(reg.Root, "", c.Conventions)
	return reg
}

func (c *Config) build(name, records, schema string, sources []string) Registry {
	root := c.root
	if root == "" {
		root = "."
	}
	recordsAbs := absUnder(root, records)
	return Registry{
		Name:       name,
		Root:       root,
		RecordsDir: recordsAbs,
		SchemaDir:  resolveSchemaDir(root, recordsAbs, schema),
		Sources:    append([]string(nil), sources...),
		SourceView: c.sourceView,
	}
}

// resolveSchemaDir picks where the JSON schemas live, preferring a copy the
// project vendored itself so local schema edits win over the bundled ones.
func resolveSchemaDir(root, records, declared string) string {
	switch {
	case declared != "":
		return absUnder(root, declared)
	case isDir(filepath.Join(records, "schema")):
		return filepath.Join(records, "schema")
	case isFile(filepath.Join(root, "schema", "facet.schema.json")):
		// The framework repository keeps canonical schemas at the root rather
		// than a vendored copy under the records directory.
		return filepath.Join(root, "schema")
	default:
		return filepath.Join(records, "schema")
	}
}

// OpenAll resolves every registry a project declares.
func OpenAll(root string) ([]Registry, error) {
	cfg, err := LoadConfig(root)
	if err != nil {
		return nil, err
	}
	return cfg.All(), nil
}

// RegistrySet is a project's registries addressed as one thing, for the
// interfaces that answer questions about ids without knowing which registry a
// caller means: the MCP tools and the language server.
type RegistrySet []Registry

// OpenSet resolves the project registries, using the default registry when
// configuration cannot be read so the interface can still answer. Invalid
// source views remain failed selections on the fallback and cannot scan.
func OpenSet(root string) RegistrySet {
	all, err := OpenAll(root)
	if err != nil || len(all) == 0 {
		return RegistrySet{Open(root)}
	}
	return all
}

// Multi reports whether ids need qualifying to be unambiguous.
func (s RegistrySet) Multi() bool { return len(s) > 1 }

// Primary is the registry to answer for when nothing narrows the question.
func (s RegistrySet) Primary() Registry {
	if len(s) == 0 {
		return Registry{}
	}
	return s[0]
}

// SplitID separates an optional registry prefix from a feature id.
func SplitID(s string) (registry, id string) {
	if i := strings.IndexByte(s, ':'); i > 0 {
		return s[:i], s[i+1:]
	}
	return "", s
}

// Owners lists registries holding an id in RegistrySet order. Config.All builds
// its set in alphabetical name order.
func (s RegistrySet) Owners(qualified string) []Registry {
	name, id := SplitID(qualified)
	var out []Registry
	for _, reg := range s {
		if name != "" && reg.Name != name {
			continue
		}
		if isFile(reg.PathForID(id)) {
			out = append(out, reg)
		}
	}
	return out
}

// Resolve returns the first registry holding an id, for the callers that need
// one answer — rendering a path, jumping to a definition. Callers that can show
// more than one should use Owners.
func (s RegistrySet) Resolve(qualified string) (Registry, string, bool) {
	_, id := SplitID(qualified)
	owners := s.Owners(qualified)
	if len(owners) == 0 {
		return Registry{}, id, false
	}
	return owners[0], id, true
}

// Claimants lists every registry whose sources cover a repository-relative path.
// A path claimed by more than one is shared code.
func (s RegistrySet) Claimants(rel string) []Registry {
	var out []Registry
	for _, reg := range s {
		if reg.Claims(rel) {
			out = append(out, reg)
		}
	}
	return out
}

// Qualify renders an id the way a caller should address it: bare in a project
// with one registry, prefixed where the name is needed to be unambiguous.
func (s RegistrySet) Qualify(reg Registry, id string) string {
	if !s.Multi() || reg.Name == "" {
		return id
	}
	return reg.Name + ":" + id
}
