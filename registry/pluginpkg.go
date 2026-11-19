// Package facet — what a plugin package is, and the one check every source of
// one goes through before it is trusted.
package facet

import (
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dlclark/regexp2"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const HostPluginAPI = 0

const (
	pluginPkgMaxEntries    = 1000
	pluginPkgMaxTotalBytes = 256 << 20
	pluginPkgMaxFileBytes  = 64 << 20
)

//go:embed schema/plugin-manifest.schema.json
var pluginManifestSchemaFS embed.FS

// pluginManifestSchemaGuard fails the build's tests if the embedded schema and
// the one under schema/ at the repository root ever drift apart — a file
// copied instead of shared has no other way to notice that somebody changed
// one and not the other. See pluginpkg_schema_sync_test.go.
const pluginManifestSchemaEmbedPath = "schema/plugin-manifest.schema.json"

// PackageManifest is plugin.json in full, as read from a package under
// verification.
type PackageManifest struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Version     string           `json:"version"`
	API         int              `json:"api"`
	Entry       string           `json:"entry"`
	Description string           `json:"description,omitempty"`
	Author      string           `json:"author,omitempty"`
	License     string           `json:"license,omitempty"`
	Homepage    string           `json:"homepage,omitempty"`
	Areas       []string         `json:"areas,omitempty"`
	Slots       []string         `json:"slots,omitempty"`
	Tools       []string         `json:"tools,omitempty"`
	AITools     []ManifestAITool `json:"ai_tools,omitempty"`
	Reads       []ReadDecl       `json:"reads,omitempty"`
	Events      *ManifestEvents  `json:"events,omitempty"`
	Sidecar     *ManifestSidecar `json:"sidecar,omitempty"`
}

// ManifestEvents is the manifest's "events" block.
type ManifestEvents struct {
	Listens []string `json:"listens,omitempty"`
	Emits   []string `json:"emits,omitempty"`
}

func (m PackageManifest) AddedRights(other PackageManifest) bool {
	if hasNew(m.Tools, other.Tools) {
		return true
	}
	if hasNew(readRights(m.Reads), readRights(other.Reads)) {
		return true
	}
	oldEnv, newEnv := manifestEnv(m), manifestEnv(other)
	if hasNew(oldEnv, newEnv) {
		return true
	}
	// ai_env is a right of its own: an update that offers the agent a
	// variable the old manifest only gave the surface widens what a granted
	// ai_env would let through, so the grant must be answered again.
	if hasNew(manifestAIEnv(m), manifestAIEnv(other)) {
		return true
	}
	oldAI := make([]string, 0, len(m.AITools))
	for _, t := range m.AITools {
		oldAI = append(oldAI, t.Tool)
	}
	newAI := make([]string, 0, len(other.AITools))
	for _, t := range other.AITools {
		newAI = append(newAI, t.Tool)
	}
	return hasNew(oldAI, newAI)
}

func manifestEnv(m PackageManifest) []string {
	if m.Sidecar == nil {
		return nil
	}
	return m.Sidecar.Env
}

func manifestAIEnv(m PackageManifest) []string {
	if m.Sidecar == nil {
		return nil
	}
	return m.Sidecar.AIEnv
}

// readRights flattens "reads" into one right per directory and extension, so
// widening the extensions of a directory counts as a new right just like a
// new directory, and the string form "docs/adr" equals the object form
// {"dir": "docs/adr", "ext": [".md"]}.
func readRights(reads []ReadDecl) []string {
	var out []string
	for _, d := range reads {
		for _, e := range d.Exts() {
			out = append(out, d.Dir+"\x00"+e)
		}
	}
	return out
}

// hasNew reports whether next names something base does not.
func hasNew(base, next []string) bool {
	have := map[string]bool{}
	for _, b := range base {
		have[b] = true
	}
	for _, n := range next {
		if !have[n] {
			return true
		}
	}
	return false
}

// ErrPluginIsBundled means the id belongs to a plugin the binary already
// ships. Installing a second one under the same name would shadow whichever
// the broker reads first, and bundledManifests would then describe rights for
// a manifest nobody checked.
var ErrPluginIsBundled = errors.New("plugin: this id is bundled with the binary")

// ErrPluginAPIMismatch means the manifest names an API this reader does not
// offer.
var ErrPluginAPIMismatch = errors.New("plugin: this reader does not offer the requested API")

var ErrPluginEntryOutsideAllowList = errors.New("plugin: entry is not on the allow-list")

// ErrPluginChecksumMismatch means a file's content does not match what
// SHA256SUMS — or the manifest's own sidecar.sha256 — says it should be.
var ErrPluginChecksumMismatch = errors.New("plugin: checksum does not match")

// VerifyOptions changes which of the seven rules VerifyPackage enforces.
type VerifyOptions struct {
	SkipSums bool

	// ExtraAllowed names files the allow-list (rule 3) must accept in addition
	// to what the manifest itself earns. A raw, just-extracted package must
	// never carry these — that would let a malicious package smuggle in a
	// forged `.source.json` or forged `grants.json` under a name the host
	// treats as its own. But an already-installed plugin directory legitimately
	// carries them, because Install wrote them itself after the package's own
	// files had already passed this same check. InstalledPlugins passes them
	// here rather than weakening allowedNames for everyone.
	ExtraAllowed []string
}

func VerifyPackage(dir string, opts VerifyOptions) (PackageManifest, error) {
	manifest, err := readAndValidateManifest(dir)
	if err != nil {
		return PackageManifest{}, err
	}
	if err := checkAllowList(dir, manifest, opts.ExtraAllowed); err != nil {
		return PackageManifest{}, err
	}
	if !opts.SkipSums {
		if err := checkSums(dir, manifest, opts.ExtraAllowed); err != nil {
			return PackageManifest{}, err
		}
	}
	if manifest.API != HostPluginAPI {
		return PackageManifest{}, fmt.Errorf("%w: package asks for %d, this reader offers %d",
			ErrPluginAPIMismatch, manifest.API, HostPluginAPI)
	}
	if err := markSidecarsExecutable(dir, manifest); err != nil {
		return PackageManifest{}, err
	}
	return manifest, nil
}

func readAndValidateManifest(dir string) (PackageManifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if errors.Is(err, os.ErrNotExist) {
		return PackageManifest{}, fmt.Errorf("plugin: package carries no plugin.json")
	}
	if err != nil {
		return PackageManifest{}, err
	}
	schema, err := compilePluginManifestSchema()
	if err != nil {
		return PackageManifest{}, fmt.Errorf("plugin: could not load the manifest schema: %w", err)
	}
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		return PackageManifest{}, fmt.Errorf("plugin: plugin.json is not JSON: %w", err)
	}
	if err := schema.Validate(doc); err != nil {
		var ve *jsonschema.ValidationError
		if errors.As(err, &ve) {
			return PackageManifest{}, fmt.Errorf("plugin: plugin.json does not match the schema: %s", firstLeaf(ve))
		}
		return PackageManifest{}, fmt.Errorf("plugin: plugin.json does not match the schema: %w", err)
	}
	var m PackageManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return PackageManifest{}, fmt.Errorf("plugin: plugin.json: %w", err)
	}
	if !validPluginID(m.ID) {
		return PackageManifest{}, fmt.Errorf("plugin: %q is not a plugin id", m.ID)
	}
	if _, bundled := bundledManifest(m.ID); bundled {
		return PackageManifest{}, fmt.Errorf("%w: %s", ErrPluginIsBundled, m.ID)
	}
	if m.Sidecar != nil && m.Sidecar.Bin != "" && !validSidecarBin(m.Sidecar.Bin) {
		return PackageManifest{}, fmt.Errorf("plugin: %q is not a binary name", m.Sidecar.Bin)
	}
	return m, nil
}

func firstLeaf(e *jsonschema.ValidationError) string {
	leaves := leafCauses(e)
	if len(leaves) == 0 {
		return e.Error()
	}
	loc := strings.Join(leaves[0].InstanceLocation, "/")
	if loc == "" {
		loc = "(document root)"
	}
	return fmt.Sprintf("%s: %s", loc, leaves[0].ErrorKind.LocalizedString(errorPrinter))
}

func compilePluginManifestSchema() (*jsonschema.Schema, error) {
	raw, err := pluginManifestSchemaFS.ReadFile(pluginManifestSchemaEmbedPath)
	if err != nil {
		return nil, err
	}
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()

	c.UseRegexpEngine(dlclarkRegexpCompile)
	const resourceID = "embedded://plugin-manifest.schema.json"
	if err := c.AddResource(resourceID, doc); err != nil {
		return nil, err
	}
	return c.Compile(resourceID)
}

// dlclarkRegexpRegexp adapts regexp2.Regexp to jsonschema.Regexp's two-method
// interface (MatchString, String) — copied from the library's own
// example_regexp_test.go, which is the wiring santhosh-tekuri/jsonschema
// itself documents for a pattern package.json needs and RE2 cannot parse.
type dlclarkRegexpRegexp regexp2.Regexp

func (re *dlclarkRegexpRegexp) MatchString(s string) bool {
	matched, err := (*regexp2.Regexp)(re).MatchString(s)
	return err == nil && matched
}

func (re *dlclarkRegexpRegexp) String() string {
	return (*regexp2.Regexp)(re).String()
}

func dlclarkRegexpCompile(s string) (jsonschema.Regexp, error) {
	re, err := regexp2.Compile(s, regexp2.ECMAScript)
	if err != nil {
		return nil, err
	}
	return (*dlclarkRegexpRegexp)(re), nil
}

func sidecarFileName(bin, platform string) string {
	name := bin + "-" + platform
	if strings.HasPrefix(platform, "windows-") {
		name += ".exe"
	}
	return name
}

func allowedNames(m PackageManifest) map[string]bool {
	allow := map[string]bool{
		"plugin.json": true,
		"SHA256SUMS":  true,
	}
	if m.Entry != "" {
		allow[m.Entry] = true
	}
	if m.Sidecar != nil && m.Sidecar.Bin != "" {
		platforms := make([]string, 0, len(m.Sidecar.SHA256))
		for platform := range m.Sidecar.SHA256 {
			platforms = append(platforms, platform)
		}
		sort.Strings(platforms)
		for _, platform := range platforms {
			allow[sidecarFileName(m.Sidecar.Bin, platform)] = true
		}
	}
	return allow
}

func nameAllowed(rel string, allow map[string]bool) bool {
	if allow[rel] {
		return true
	}
	if strings.HasPrefix(rel, "assets/") {
		return true
	}
	if !strings.Contains(rel, "/") {
		base := rel
		if strings.HasPrefix(base, "LICENSE") || strings.HasPrefix(base, "README") {
			return true
		}
	}
	return false
}

func checkAllowList(dir string, m PackageManifest, extraAllowed []string) error {
	allow := allowedNames(m)
	for _, name := range extraAllowed {
		allow[name] = true
	}
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := filepath.ToSlash(mustRel(dir, p))
		if !nameAllowed(rel, allow) {
			return fmt.Errorf("%w: %s", ErrPluginEntryOutsideAllowList, rel)
		}
		return nil
	})
}

func mustRel(base, target string) string {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return target
	}
	return rel
}

func checkSums(dir string, m PackageManifest, extraAllowed []string) error {
	skip := make(map[string]bool, len(extraAllowed))
	for _, name := range extraAllowed {
		skip[name] = true
	}
	raw, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("plugin: package carries no SHA256SUMS")
	}
	if err != nil {
		return err
	}
	listed, err := ParseSHA256Sums(raw)
	if err != nil {
		return err
	}

	present := map[string]string{}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := filepath.ToSlash(mustRel(dir, p))
		if rel == "SHA256SUMS" || skip[rel] {
			return nil
		}
		sum, err := fileSHA256(p)
		if err != nil {
			return err
		}
		present[rel] = sum
		return nil
	})
	if err != nil {
		return err
	}

	names := make([]string, 0, len(listed))
	for name := range listed {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		actual, ok := present[name]
		if !ok {
			return fmt.Errorf("%w: SHA256SUMS names %s, which is not in the package", ErrPluginChecksumMismatch, name)
		}
		if actual != listed[name] {
			return fmt.Errorf("%w: %s", ErrPluginChecksumMismatch, name)
		}
	}
	for name := range present {
		if _, ok := listed[name]; !ok {
			return fmt.Errorf("%w: %s is in the package but not in SHA256SUMS", ErrPluginChecksumMismatch, name)
		}
	}

	if m.Sidecar != nil {
		platforms := make([]string, 0, len(m.Sidecar.SHA256))
		for platform := range m.Sidecar.SHA256 {
			platforms = append(platforms, platform)
		}
		sort.Strings(platforms)
		for _, platform := range platforms {
			name := sidecarFileName(m.Sidecar.Bin, platform)
			actual, ok := present[name]
			if !ok {
				return fmt.Errorf("%w: manifest names a sidecar for %s that is not in the package", ErrPluginChecksumMismatch, platform)
			}
			if !strings.EqualFold(actual, m.Sidecar.SHA256[platform]) {
				return fmt.Errorf("%w: sidecar for %s disagrees with plugin.json's sidecar.sha256", ErrPluginChecksumMismatch, platform)
			}
		}
	}
	return nil
}

// ParseSHA256Sums parses a whole SHA256SUMS file into name → lowercase hex
// digest. Exported so a caller outside this package that needs to check one
// file against the same list (a server checking an entry file at serve time,
// rather than VerifyPackage's whole-directory pass at install) reads it through the
// one parser this package already trusts, instead of a second one that could
// read the format differently.
func ParseSHA256Sums(raw []byte) (map[string]string, error) {
	listed := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		sum, name, ok := splitSumLine(line)
		if !ok {
			return nil, fmt.Errorf("plugin: SHA256SUMS: malformed line %q", line)
		}
		listed[name] = strings.ToLower(sum)
	}
	return listed, nil
}

// splitSumLine parses one `sha256sum`-format line: <hex>  <path>, with one or
// more spaces between the two fields (sha256sum itself writes two, one of
// them marking text vs. binary mode; a single space is accepted too, since
// nothing here writes in binary mode).
func splitSumLine(line string) (sum, name string, ok bool) {
	i := strings.IndexAny(line, " \t")
	if i < 0 {
		return "", "", false
	}
	sum = line[:i]
	name = strings.TrimLeft(line[i:], " \t*")
	if sum == "" || name == "" {
		return "", "", false
	}
	if _, err := hex.DecodeString(sum); err != nil || len(sum) != 64 {
		return "", "", false
	}
	return sum, filepath.ToSlash(name), true
}

func markSidecarsExecutable(dir string, m PackageManifest) error {
	if m.Sidecar == nil {
		return nil
	}
	for platform := range m.Sidecar.SHA256 {
		p := filepath.Join(dir, sidecarFileName(m.Sidecar.Bin, platform))
		if err := os.Chmod(p, 0o755); err != nil {
			return err
		}
	}
	return nil
}

type sourceEntry struct {
	// name is the entry's path within the package, forward slashes, before
	// any single-top-folder stripping.
	name string
	// mode carries the entry's own bits, used only to refuse a symlink,
	// device, or pipe — never to set permissions on the copy.
	mode fs.FileMode
	open func() (io.ReadCloser, error)
}

func stagePackage(entries []sourceEntry, dest string) error {
	if len(entries) > pluginPkgMaxEntries {
		return fmt.Errorf("plugin: package carries more than %d entries", pluginPkgMaxEntries)
	}
	seenCaseFold := map[string]string{}
	var total int64
	for _, e := range entries {
		name := e.name
		if err := checkEntryPath(name); err != nil {
			return err
		}
		if e.mode&(fs.ModeSymlink|fs.ModeDevice|fs.ModeNamedPipe|fs.ModeSocket|fs.ModeCharDevice) != 0 {
			return fmt.Errorf("plugin: %s is not a regular file", name)
		}
		fold := strings.ToLower(name)
		if prior, ok := seenCaseFold[fold]; ok && prior != name {
			return fmt.Errorf("plugin: %s and %s differ only in case", prior, name)
		}
		seenCaseFold[fold] = name

		dstPath := filepath.Join(dest, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return err
		}
		n, err := copyEntry(e, dstPath)
		if err != nil {
			return err
		}
		total += n
		if total > pluginPkgMaxTotalBytes {
			return fmt.Errorf("plugin: package exceeds %d bytes unpacked", pluginPkgMaxTotalBytes)
		}
	}
	return nil
}

func checkEntryPath(name string) error {
	if name == "" {
		return errors.New("plugin: package carries an entry with no name")
	}
	if strings.Contains(name, "\\") {
		return fmt.Errorf("plugin: %s carries a backslash", name)
	}
	if path.IsAbs(name) || (len(name) > 1 && name[1] == ':') {
		return fmt.Errorf("plugin: %s is an absolute path", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return fmt.Errorf("plugin: %s walks outside the package", name)
		}
	}
	return nil
}

func copyEntry(e sourceEntry, dstPath string) (int64, error) {
	src, err := e.open()
	if err != nil {
		return 0, err
	}
	defer src.Close()
	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	defer dst.Close()

	n, err := io.Copy(dst, io.LimitReader(src, pluginPkgMaxFileBytes+1))
	if err != nil {
		return n, err
	}
	if n > pluginPkgMaxFileBytes {
		return n, fmt.Errorf("plugin: %s exceeds %d bytes", e.name, pluginPkgMaxFileBytes)
	}
	return n, nil
}

func flattenSingleTopFolder(entries []sourceEntry) ([]sourceEntry, error) {
	rootHasManifest := false
	firstSegments := map[string]bool{}
	for _, e := range entries {
		first, rest, has := strings.Cut(e.name, "/")
		if !has {
			if e.name == "plugin.json" {
				rootHasManifest = true
			}
			firstSegments["."] = true
			continue
		}
		_ = rest
		firstSegments[first] = true
	}
	if rootHasManifest {
		return entries, nil
	}
	if len(firstSegments) != 1 {
		return nil, errors.New("plugin: package has no plugin.json at its root, and its entries do not share a single top-level folder")
	}
	var prefix string
	for k := range firstSegments {
		prefix = k
	}
	if prefix == "." {
		return nil, errors.New("plugin: package carries no plugin.json")
	}
	stripped := make([]sourceEntry, 0, len(entries))
	foundManifest := false
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.name, prefix+"/")
		if !ok {
			return nil, fmt.Errorf("plugin: %s does not share the package's single top-level folder %q", e.name, prefix)
		}
		if rest == "plugin.json" {
			foundManifest = true
		}
		ne := e
		ne.name = rest
		stripped = append(stripped, ne)
	}
	if !foundManifest {
		return nil, fmt.Errorf("plugin: %q is not a single top-level folder containing plugin.json", prefix)
	}
	return stripped, nil
}
