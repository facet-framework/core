// Brokering a plugin's call to its own binary — and refusing the rest.
package facet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// PluginManifest is the part of plugin.json the host reads.
type PluginManifest struct {
	ID    string   `json:"id"`
	Tools []string `json:"tools"`

	AITools []ManifestAITool `json:"ai_tools"`
	Sidecar *ManifestSidecar `json:"sidecar"`
	// Reads names the directories, relative to the registry root, this
	// plugin may read through repo.list/repo.read. A directory absent
	// from here is refused the same way an undeclared tool is: the manifest
	// asks, and reading a directory nobody named would be a right the plugin
	// granted itself. Empty for a plugin that never calls either tool.
	Reads []ReadDecl `json:"reads"`
}

// ManifestAITool is one tool as the agent sees it.
type ManifestAITool struct {
	Tool        string `json:"tool"`
	Description string `json:"description"`
	// Input is a JSON Schema object, passed to the model as written.
	Input json.RawMessage `json:"input"`
}

// ManifestSidecar declares the binary and what it may see.
type ManifestSidecar struct {
	// Bin is the base name; the host appends the OS and architecture.
	Bin string `json:"bin"`

	Env []string `json:"env,omitempty"`

	AIEnv []string `json:"ai_env,omitempty"`
	// SHA256 maps "<os>-<arch>" to the checksum of that build.
	SHA256 map[string]string `json:"sha256"`
}

// Grant is the user's answer, recorded in grants.json, and the host holds it —
// never the plugin's own surface.
type Grant struct {
	Tools []string `json:"tools"`

	AITools []string `json:"ai_tools"`
	// Env names the variables the user let through to this plugin's binary.
	Env []string `json:"env"`

	AIEnv []string `json:"ai_env"`
}

// ErrToolNotDeclared means the manifest does not carry this tool.
var ErrToolNotDeclared = errors.New("plugin: the manifest does not declare this tool")

// ErrToolNotGranted means the grant does not allow it.
var ErrToolNotGranted = errors.New("plugin: this tool has not been granted")

// ErrNoSidecar means the plugin declares tools but brings no binary.
var ErrNoSidecar = errors.New("plugin: no sidecar declared")

// ErrPluginNotInstalled means there is no manifest under this id.
var ErrPluginNotInstalled = errors.New("plugin: not installed")

// PluginBroker resolves a call against one plugin directory.
type PluginBroker struct {
	dir string

	// platform names the running host as "<os>-<arch>", the same shape a
	// manifest's sidecar.sha256 keys against. Set by NewPluginBroker to the
	// real runtime.GOOS/runtime.GOARCH; a test overrides it directly to
	// exercise the windows ".exe" naming rule without a windows machine.
	platform string

	mu sync.Mutex
	// sidecars is keyed by plugin AND audience, not by plugin alone.
	sidecars map[sidecarKey]*Sidecar
}

// sidecarAudience says on whose behalf a sidecar is started.
type sidecarAudience int

const (
	// forSurface is the plugin's own surface: a host's plugin-call endpoint,
	// a product's own commands, facet lint. The environment is env ∩ grants.env.
	forSurface sidecarAudience = iota
	// forAgent is an agent calling through `facet mcp`. The environment is
	// the four-way intersection agentEnv builds.
	forAgent
)

type sidecarKey struct {
	id       string
	audience sidecarAudience
}

// NewPluginBroker reads from one directory of installed plugins.
func NewPluginBroker(dir string) *PluginBroker {
	return &PluginBroker{dir: dir, sidecars: map[sidecarKey]*Sidecar{}, platform: runtime.GOOS + "-" + runtime.GOARCH}
}

// NewPluginBrokerForPlatform is NewPluginBroker with the platform string
// named explicitly, rather than read from runtime.GOOS/runtime.GOARCH.
func NewPluginBrokerForPlatform(dir, platform string) *PluginBroker {
	b := NewPluginBroker(dir)
	b.platform = platform
	return b
}

// Manifest reads and validates one plugin's manifest.
func (b *PluginBroker) Manifest(id string) (PluginManifest, error) {
	m, _, err := b.manifest(id)
	return m, err
}

// manifest is Manifest's own body, and also says whether the answer came from
// the embedded catalogue rather than a directory — Allowed needs to know
// which, and the public method does not.
func (b *PluginBroker) manifest(id string) (PluginManifest, bool, error) {
	var m PluginManifest
	if !validPluginID(id) {
		return m, false, fmt.Errorf("plugin: %q is not a plugin id", id)
	}
	raw, err := os.ReadFile(filepath.Join(b.dir, id, "plugin.json"))
	if errors.Is(err, os.ErrNotExist) {

		if bundled, ok := bundledManifest(id); ok {
			return bundled, true, nil
		}
		return m, false, fmt.Errorf("%w: %s", ErrPluginNotInstalled, id)
	}
	if err != nil {
		return m, false, fmt.Errorf("plugin: %s: %w", id, err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, false, fmt.Errorf("plugin: %s: manifest is not JSON: %w", id, err)
	}
	// The id is the directory's, not the file's. A manifest that could name
	// itself could claim another plugin's grants.
	if m.ID != id {
		return m, false, fmt.Errorf("plugin: %s declares the id %q", id, m.ID)
	}
	return m, false, nil
}

// Granted reads what grants.json allows. A missing file means nothing.
func (b *PluginBroker) Granted(id string) (Grant, error) {
	var g Grant
	if !validPluginID(id) {
		return g, fmt.Errorf("plugin: %q is not a plugin id", id)
	}
	raw, err := os.ReadFile(filepath.Join(b.dir, id, "grants.json"))
	if errors.Is(err, os.ErrNotExist) {
		return g, nil
	}
	if err != nil {
		return g, err
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		return g, fmt.Errorf("plugin: %s: grants are not JSON: %w", id, err)
	}
	return g, nil
}

// Allowed returns nil only for a declared and granted tool, or a tool declared
// by a bundled manifest. It returns the manifest, grant, or permission error
// otherwise. Keep declaration and grant separate: editing a plugin manifest
// must not widen the reader's grant. Shipping a bundled manifest grants its
// tools and declared environment without an installed grants.json.
func (b *PluginBroker) Allowed(id, tool string) error {
	m, bundled, err := b.manifest(id)
	if err != nil {
		return err
	}
	if !names(m.Tools, tool) {
		return fmt.Errorf("%w: %s", ErrToolNotDeclared, tool)
	}
	if bundled {
		return nil
	}
	g, err := b.Granted(id)
	if err != nil {
		return err
	}
	if !names(g.Tools, tool) {
		return fmt.Errorf("%w: %s", ErrToolNotGranted, tool)
	}
	return nil
}

// bundledManifest answers the manifest of a plugin the binary carries itself,
// when no plugins directory names it.
func bundledManifest(id string) (PluginManifest, bool) {
	bundledRegistry.Lock()
	defer bundledRegistry.Unlock()
	if m, ok := bundledRegistry.manifests[id]; ok {
		return m, true
	}
	m, ok := bundledManifests[id]
	return m, ok
}

var bundledRegistry struct {
	sync.Mutex
	manifests map[string]PluginManifest
}

// RegisterBundledPlugin adds the manifest of a plugin a linked product carries
// into the bundled catalogue. Builds register from init. It panics on an empty
// or repeated id: two manifests for one plugin would make its rights
// undecidable.
func RegisterBundledPlugin(manifest PluginManifest) {
	if manifest.ID == "" {
		panic("facet: RegisterBundledPlugin needs a plugin id")
	}
	bundledRegistry.Lock()
	defer bundledRegistry.Unlock()
	if _, taken := bundledManifests[manifest.ID]; taken {
		panic(fmt.Sprintf("facet: bundled plugin %q is registered twice", manifest.ID))
	}
	if _, taken := bundledRegistry.manifests[manifest.ID]; taken {
		panic(fmt.Sprintf("facet: bundled plugin %q is registered twice", manifest.ID))
	}
	if bundledRegistry.manifests == nil {
		bundledRegistry.manifests = map[string]PluginManifest{}
	}
	bundledRegistry.manifests[manifest.ID] = manifest
}

var bundledManifests = map[string]PluginManifest{

	"adr": {
		ID:    "adr",
		Tools: []string{"repo.list", "repo.read"},
		Reads: []ReadDecl{{Dir: "docs/adr"}},
	},

	"bpmn": {
		ID:    "bpmn",
		Tools: []string{"repo.list", "repo.read", "bpmn.diagram"},
		// The object form, so the host reads .bpmn here and nothing else —
		// not even .md, which the string form would have meant.
		Reads: []ReadDecl{{Dir: "docs/bpmn", Ext: []string{".bpmn"}, object: true}},
		Sidecar: &ManifestSidecar{
			Bin: "bpmn",
		},
	},
}

// Call brokers one call: check, then route, and never the other way round.
func (b *PluginBroker) Call(ctx context.Context, id, tool string, args json.RawMessage) (SidecarReply, error) {
	if err := b.Allowed(id, tool); err != nil {
		return SidecarReply{}, err
	}
	side, err := b.sidecarFor(id, forSurface)
	if err != nil {
		return SidecarReply{}, err
	}
	return side.Call(ctx, SidecarCall{Tool: tool, Args: args})
}

// StopAll ends every running sidecar. Called when the host exits.
func (b *PluginBroker) StopAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for key, s := range b.sidecars {
		s.Stop()
		delete(b.sidecars, key)
	}
}

func (b *PluginBroker) sidecarFor(id string, who sidecarAudience) (*Sidecar, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := sidecarKey{id: id, audience: who}
	if s, ok := b.sidecars[key]; ok {
		return s, nil
	}
	m, bundled, err := b.manifest(id)
	if err != nil {
		return nil, err
	}
	if m.Sidecar == nil || m.Sidecar.Bin == "" {
		return nil, fmt.Errorf("%w: %s", ErrNoSidecar, id)
	}
	if !validSidecarBin(m.Sidecar.Bin) {
		return nil, fmt.Errorf("plugin: %s names %q as its binary, which is not a name", id, m.Sidecar.Bin)
	}
	var spec SidecarSpec
	if bundled {
		spec, err = b.bundledSidecarSpec(m, who)
	} else {
		spec, err = b.installedSidecarSpec(id, m, who)
	}
	if err != nil {
		return nil, err
	}
	s, err := NewSidecar(spec)
	if err != nil {
		return nil, err
	}
	b.sidecars[key] = s
	return s, nil
}

func (b *PluginBroker) installedSidecarSpec(id string, m PluginManifest, who sidecarAudience) (SidecarSpec, error) {
	platform := b.platform
	sum, ok := m.Sidecar.SHA256[platform]
	if !ok {
		return SidecarSpec{}, fmt.Errorf("plugin: %s brings no binary for %s", id, platform)
	}
	g, err := b.Granted(id)
	if err != nil {
		return SidecarSpec{}, err
	}
	return SidecarSpec{

		Bin:    filepath.Join(b.dir, id, sidecarFileName(m.Sidecar.Bin, platform)),
		SHA256: sum,

		Env: sidecarEnv(m.Sidecar, g, who),
	}, nil
}

// sidecarEnv is the environment a sidecar started for this audience may see.
func sidecarEnv(m *ManifestSidecar, g Grant, who sidecarAudience) []string {
	if who == forAgent {
		return agentEnv(m, g)
	}
	return both(m.Env, g.Env)
}

func agentEnv(m *ManifestSidecar, g Grant) []string {
	return both(both(m.Env, m.AIEnv), both(g.Env, g.AIEnv))
}

// AgentEnv is agentEnv for callers outside this package: the variables a
// sidecar started for the agent may see under manifest m and grant g: those
// the manifest declares under both env and ai_env and the grant names under
// both env and ai_env.
func AgentEnv(m *ManifestSidecar, g Grant) []string {
	return agentEnv(m, g)
}

// bundledSidecarExecutable resolves this process's executable. Tests replace
// it temporarily to resolve a fixture beside a stand-in host executable.
var bundledSidecarExecutable = os.Executable

// bundledSidecarSpec returns a sidecar beside the host executable with the
// build-measured checksum injected by cmd/facet, never a manifest self-checksum.
// It returns an error for missing measurements or executable resolution failure;
// scripts/build-sidecars.sh supplies development measurements. Bundled Env is
// granted by shipping the manifest; agents receive only Env intersected with
// AIEnv, so they do not inherit the surface's full environment.
func (b *PluginBroker) bundledSidecarSpec(m PluginManifest, who sidecarAudience) (SidecarSpec, error) {
	sum := bundledSidecarSum(m.Sidecar.Bin)
	if sum == "" {
		return SidecarSpec{}, fmt.Errorf(
			"plugin: this build carries no measured checksum for its bundled sidecar %s — "+
				"run scripts/build-sidecars.sh, or install %s under FACET_PLUGINS_DIR instead", m.Sidecar.Bin, m.ID)
	}
	exe, err := bundledSidecarExecutable()
	if err != nil {
		return SidecarSpec{}, fmt.Errorf("plugin: could not find this binary's own directory: %w", err)
	}
	bin := m.Sidecar.Bin
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	env := m.Sidecar.Env
	if who == forAgent {
		env = both(m.Sidecar.Env, m.Sidecar.AIEnv)
	}
	return SidecarSpec{
		Bin:    filepath.Join(filepath.Dir(exe), bin),
		SHA256: sum,
		Env:    env,
	}, nil
}

// validSidecarBin keeps the binary inside the plugin's own directory.
func validSidecarBin(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// validPluginID keeps a call from walking out of the plugin directory.
func validPluginID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}
	return true
}

// names reports whether the list carries exactly this string.
func both(asked, granted []string) []string {
	out := make([]string, 0, len(asked))
	for _, name := range asked {
		if names(granted, name) {
			out = append(out, name)
		}
	}
	return out
}

func names(list []string, want string) bool {
	for _, got := range list {
		if got == want {
			return true
		}
	}
	return false
}
