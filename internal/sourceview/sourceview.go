// Package sourceview resolves portable repository identities to exactly one
// local checkout per repository for one command invocation.
package sourceview

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

const LocalName = ".facet/sources.local.yaml"
const EnvName = "FACET_SOURCE_VIEW"

type Repository struct {
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}
type Selection struct {
	Binding string `yaml:"binding" json:"binding"`
	Ref     string `yaml:"ref,omitempty" json:"ref,omitempty"`
}
type View struct {
	Sources map[string]Selection `yaml:"sources" json:"sources"`
}
type Config struct {
	Repositories       map[string]Repository `yaml:"repositories" json:"repositories"`
	RegistryRepository string                `yaml:"registry_repository,omitempty" json:"registry_repository,omitempty"`
	DefaultView        string                `yaml:"default_view,omitempty" json:"default_view,omitempty"`
	Views              map[string]View       `yaml:"views" json:"views"`
}
type Binding struct {
	Repository string `yaml:"repository" json:"repository"`
	Checkout   string `yaml:"checkout" json:"-"`
}
type LocalConfig struct {
	Bindings map[string]Binding `yaml:"bindings" json:"-"`
}

type Source struct {
	Repository   string `json:"repository"`
	Binding      string `json:"binding"`
	Root         string `json:"-"`
	Ref          string `json:"ref,omitempty"`
	Revision     string `json:"revision"`
	Dirty        bool   `json:"dirty"`
	ContentHash  string `json:"content_hash"`
	fingerprints map[string]string
}
type ResolvedView struct {
	Name               string                `json:"name"`
	RegistryRepository string                `json:"registry_repository,omitempty"`
	Sources            map[string]Source     `json:"sources"`
	Repositories       map[string]Repository `json:"repositories"`
	failure            error
}

type resolutionError struct{ message string }

func (e *resolutionError) Error() string          { return e.message }
func errorf(format string, args ...any) error     { return &resolutionError{fmt.Sprintf(format, args...)} }
func SelectionError(message string) error         { return errorf("%s", message) }
func IsError(err error) bool                      { var target *resolutionError; return errors.As(err, &target) }
func Failed(name string, err error) *ResolvedView { return &ResolvedView{Name: name, failure: err} }

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func (c Config) Validate() error {
	if len(c.Repositories) == 0 {
		return errorf("source_views.repositories must declare stable repository identities")
	}
	for id := range c.Repositories {
		if !nameRE.MatchString(id) {
			return errorf("invalid repository identity %q", id)
		}
	}
	if c.RegistryRepository != "" {
		if _, ok := c.Repositories[c.RegistryRepository]; !ok {
			return errorf("registry_repository %q is not a declared repository", c.RegistryRepository)
		}
	}
	if len(c.Views) == 0 {
		return errorf("source_views.views must declare at least one view")
	}
	if c.DefaultView != "" {
		if _, ok := c.Views[c.DefaultView]; !ok {
			return errorf("default source view %q is not declared", c.DefaultView)
		}
	}
	for name, view := range c.Views {
		if !nameRE.MatchString(name) {
			return errorf("invalid source view name %q", name)
		}
		if len(view.Sources) == 0 {
			return errorf("source view %q has no sources", name)
		}
		bindings := map[string]string{}
		for id, selection := range view.Sources {
			if _, ok := c.Repositories[id]; !ok {
				return errorf("view %q names unknown repository %q", name, id)
			}
			if !nameRE.MatchString(selection.Binding) {
				return errorf("view %q repository %q needs a valid local binding", name, id)
			}
			if previous, ok := bindings[selection.Binding]; ok {
				return errorf("view %q assigns binding %q to both %q and %q", name, selection.Binding, previous, id)
			}
			bindings[selection.Binding] = id
			if strings.ContainsAny(selection.Ref, "\x00\r\n") || strings.HasPrefix(selection.Ref, "-") {
				return errorf("invalid ref for repository %q", id)
			}
		}
	}
	return nil
}

func Resolve(root string, cfg Config, selected string) (*ResolvedView, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if _, err := git(root, "ls-files", "--error-unmatch", "--", LocalName); err == nil {
		return nil, errorf("%s contains personal checkout bindings and must be ignored, not tracked", LocalName)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(LocalName)))
	if err != nil {
		return nil, errorf("source view needs local bindings in %s: %v", LocalName, err)
	}
	var local LocalConfig
	if err := yaml.Unmarshal(data, &local); err != nil {
		return nil, errorf("invalid %s: %v", LocalName, err)
	}
	return ResolveWithBindings(root, cfg, local, selected)
}

// LoadLocalSpecSources reads the checkout overrides used by spec update.
func LoadLocalSpecSources(root string) (map[string]string, error) {
	var local struct {
		SpecSources map[string]string `yaml:"spec_sources"`
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(LocalName)))
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, &local); err != nil {
		return nil, errorf("invalid %s: %v", LocalName, err)
	}
	return local.SpecSources, nil
}

// @facet: registry/layout/source-views
// @facet-role: primary
func ResolveWithBindings(root string, cfg Config, local LocalConfig, selected string) (*ResolvedView, error) {
	return resolveBindings(root, cfg, local, selected, nil)
}

// ResolveCommittedBindings validates identities and refs without reading working
// tree content. The calling revision verifier materializes the returned commits.
func ResolveCommittedBindings(root string, cfg Config, local LocalConfig, selected string, revisions map[string]string) (*ResolvedView, error) {
	if revisions == nil {
		revisions = map[string]string{}
	}
	return resolveBindings(root, cfg, local, selected, revisions)
}

func resolveBindings(root string, cfg Config, local LocalConfig, selected string, revisions map[string]string) (*ResolvedView, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if selected == "" {
		selected = os.Getenv(EnvName)
	}
	if selected == "" {
		selected = cfg.DefaultView
	}
	if selected == "" {
		if len(cfg.Views) != 1 {
			return nil, errorf("select a source view explicitly; no default_view is declared")
		}
		for name := range cfg.Views {
			selected = name
		}
	}
	view, ok := cfg.Views[selected]
	if !ok {
		return nil, errorf("unknown source view %q", selected)
	}
	result := &ResolvedView{Name: selected, RegistryRepository: cfg.RegistryRepository, Sources: map[string]Source{}, Repositories: cfg.Repositories}
	roots := map[string]string{}
	commonRepositories := map[string]string{}
	for _, id := range sortedKeys(view.Sources) {
		selection := view.Sources[id]
		binding, ok := local.Bindings[selection.Binding]
		if !ok {
			return nil, errorf("view %q requires unavailable local binding %q", selected, selection.Binding)
		}
		if binding.Repository != id {
			return nil, errorf("binding %q belongs to repository %q, not %q", selection.Binding, binding.Repository, id)
		}
		if binding.Checkout == "" {
			return nil, errorf("binding %q has no checkout", selection.Binding)
		}
		checkout := binding.Checkout
		if !filepath.IsAbs(checkout) {
			checkout = filepath.Join(root, checkout)
		}
		checkout, err := filepath.Abs(checkout)
		if err != nil {
			return nil, errorf("binding %q: %v", selection.Binding, err)
		}
		checkout, err = filepath.EvalSymlinks(checkout)
		if err != nil {
			return nil, errorf("binding %q checkout is unavailable: %v", selection.Binding, err)
		}
		top, err := git(checkout, "rev-parse", "--show-toplevel")
		if err != nil {
			return nil, errorf("binding %q is not a Git checkout", selection.Binding)
		}
		topRoot, err := filepath.EvalSymlinks(strings.TrimSpace(string(top)))
		if err != nil || filepath.Clean(topRoot) != checkout {
			return nil, errorf("binding %q must name the checkout root", selection.Binding)
		}
		if previous, exists := roots[checkout]; exists {
			return nil, errorf("checkout is bound to both %q and %q; repository ownership is ambiguous", previous, id)
		}
		roots[checkout] = id
		commonRaw, err := git(checkout, "rev-parse", "--git-common-dir")
		if err != nil {
			return nil, errorf("binding %q has no repository metadata", selection.Binding)
		}
		common := strings.TrimSpace(string(commonRaw))
		if !filepath.IsAbs(common) {
			common = filepath.Join(checkout, common)
		}
		common, err = filepath.EvalSymlinks(common)
		if err != nil {
			return nil, errorf("binding %q repository metadata is unavailable", selection.Binding)
		}
		if previous, exists := commonRepositories[common]; exists {
			return nil, errorf("view %q binds multiple worktrees of one repository as %q and %q", selected, previous, id)
		}
		commonRepositories[common] = id

		revision, captured := revisions[checkout]
		if !captured {
			revision, err = head(checkout)
		} else {
			err = nil
		}
		if err != nil {
			original, _ := filepath.EvalSymlinks(root)
			if revisions == nil || checkout != original || selection.Ref != "" {
				return nil, errorf("binding %q has no available HEAD commit", selection.Binding)
			}
		}
		if selection.Ref != "" {
			pinned, err := git(checkout, "rev-parse", "--verify", "--end-of-options", selection.Ref+"^{commit}")
			if err != nil {
				return nil, errorf("repository %q ref %q is unavailable", id, selection.Ref)
			}
			if strings.TrimSpace(string(pinned)) != revision {
				return nil, errorf("repository %q ref %q is not checked out; bind a worktree at that revision", id, selection.Ref)
			}
		}
		if revisions != nil {
			revisions[checkout] = revision
			result.Sources[id] = Source{Repository: id, Binding: selection.Binding, Root: checkout, Ref: selection.Ref, Revision: revision}
			continue
		}
		hash, dirty, fingerprints, err := capture(checkout)
		if err != nil {
			return nil, errorf("repository %q snapshot: %v", id, err)
		}
		result.Sources[id] = Source{Repository: id, Binding: selection.Binding, Root: checkout, Ref: selection.Ref, Revision: revision, ContentHash: hash, Dirty: dirty, fingerprints: fingerprints}
	}
	return result, nil
}
func (v *ResolvedView) Validate() error {
	if v == nil {
		return nil
	}
	if v.failure != nil {
		return v.failure
	}
	if v.Name == "" || len(v.Sources) == 0 {
		return errorf("source view is unresolved")
	}
	for _, source := range v.Ordered() {
		if err := source.ValidateWorkingTree(); err != nil {
			return err
		}
	}
	return nil
}
func (v *ResolvedView) Ordered() []Source {
	if v == nil {
		return nil
	}
	out := make([]Source, 0, len(v.Sources))
	for _, id := range sortedKeys(v.Sources) {
		out = append(out, v.Sources[id])
	}
	return out
}
func (v *ResolvedView) Source(repository string) (Source, error) {
	if v == nil {
		return Source{}, errorf("no source view is selected")
	}
	if v.failure != nil {
		return Source{}, v.failure
	}
	if repository == "" {
		if len(v.Sources) != 1 {
			return Source{}, errorf("repository identity is required in a multi-repository view")
		}
		for _, source := range v.Sources {
			return source, nil
		}
	}
	source, ok := v.Sources[repository]
	if !ok {
		return Source{}, errorf("repository %q is unavailable in view %q", repository, v.Name)
	}
	return source, nil
}
func (v *ResolvedView) ResolveReference(ref string) (Source, string, error) {
	repository, relative := splitReference(ref)
	source, err := v.Source(repository)
	if err != nil {
		return Source{}, "", err
	}
	if err := validReferencePath(relative); err != nil {
		return Source{}, "", err
	}
	return source, relative, nil
}
func (v *ResolvedView) Qualify(repository, relative string) (string, error) {
	source, err := v.Source(repository)
	if err != nil {
		return "", err
	}
	if err := validReferencePath(relative); err != nil {
		return "", err
	}
	return source.Repository + ":" + relative, nil
}
func splitReference(ref string) (string, string) {
	if i := strings.IndexByte(ref, ':'); i > 0 && i+1 < len(ref) && ref[i+1] != ':' {
		return ref[:i], ref[i+1:]
	}
	return "", ref
}
func validReferencePath(ref string) error {
	path := strings.SplitN(ref, "::", 2)[0]
	if path == "" || strings.ContainsAny(path, "\\\x00") || filepath.IsAbs(path) || strings.HasPrefix(path, "/") || strings.Contains(path, ":") {
		return errorf("source reference %q must be repository-relative", ref)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if clean != path || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return errorf("source reference %q escapes or does not canonically name its repository", ref)
	}
	return nil
}
func (s Source) ValidateWorkingTree() error {
	revision, err := head(s.Root)
	if err != nil || revision != s.Revision {
		return errorf("repository %q HEAD changed after view resolution", s.Repository)
	}
	hash, dirty, err := snapshot(s.Root)
	if err != nil || hash != s.ContentHash || dirty != s.Dirty {
		return errorf("repository %q content changed after view resolution", s.Repository)
	}
	return nil
}
func (s Source) Files() ([]string, error) {
	if err := s.ValidateWorkingTree(); err != nil {
		return nil, err
	}
	return files(s.Root)
}

// Read validates the requested file against the invocation snapshot. Callers
// publishing an aggregate result must also Validate the complete view before
// and after their operation; this avoids rehashing every repository per file.
func (s Source) Read(relative string) ([]byte, error) {
	if err := validReferencePath(relative); err != nil {
		return nil, err
	}
	if strings.Contains(relative, "::") {
		return nil, errorf("Read requires a file path without a test selector")
	}
	if revision, err := head(s.Root); err != nil || revision != s.Revision {
		return nil, errorf("repository %q HEAD changed after view resolution", s.Repository)
	}
	if s.fingerprints == nil {
		if err := s.ValidateWorkingTree(); err != nil {
			return nil, err
		}
	}
	if s.fingerprints != nil {
		if expected, ok := s.fingerprints[relative]; !ok {
			return nil, errorf("file %q was not captured in repository %q", relative, s.Repository)
		} else if actual, err := fingerprint(s.Root, relative); err != nil || actual != expected {
			return nil, errorf("file %q changed after view resolution", relative)
		}
	}
	physical, err := filepath.EvalSymlinks(filepath.Join(s.Root, filepath.FromSlash(relative)))
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(s.Root, physical)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, errorf("source file %q leaves repository %q through a symlink", relative, s.Repository)
	}
	rel = filepath.ToSlash(rel)
	if s.fingerprints != nil {
		if _, ok := s.fingerprints[rel]; !ok {
			return nil, errorf("symlink target of %q was not captured in the selected repository", relative)
		}
	}
	raw, err := os.ReadFile(physical)
	if err != nil {
		return nil, err
	}
	if s.fingerprints != nil {
		info, err := os.Stat(physical)
		if err != nil {
			return nil, err
		}
		digest := sha256.New()
		fmt.Fprintf(digest, "regular:%t\x00", info.Mode().Perm()&0111 != 0)
		digest.Write(raw)
		if hex.EncodeToString(digest.Sum(nil)) != s.fingerprints[rel] {
			return nil, errorf("file %q did not match the selected content", relative)
		}
	}
	if s.fingerprints != nil {
		if actual, err := fingerprint(s.Root, rel); err != nil || actual != s.fingerprints[rel] {
			return nil, errorf("file %q changed while being read", relative)
		}
	} else if err := s.ValidateWorkingTree(); err != nil {
		return nil, err
	}
	if revision, err := head(s.Root); err != nil || revision != s.Revision {
		return nil, errorf("repository %q HEAD changed while reading", s.Repository)
	}
	return raw, nil
}

func git(root string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	return cmd.Output()
}
func head(root string) (string, error) {
	raw, err := git(root, "rev-parse", "--verify", "HEAD^{commit}")
	return strings.TrimSpace(string(raw)), err
}
func files(root string) ([]string, error) {
	raw, err := git(root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, p := range strings.Split(string(raw), "\x00") {
		if p != "" {
			seen[p] = true
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}
func snapshot(root string) (string, bool, error) {
	hash, dirty, _, err := capture(root)
	return hash, dirty, err
}
func capture(root string) (string, bool, map[string]string, error) {
	paths, err := files(root)
	if err != nil {
		return "", false, nil, err
	}
	digest := sha256.New()
	fingerprints := map[string]string{}
	for _, relative := range paths {
		fp, err := fingerprint(root, relative)
		if err != nil {
			return "", false, nil, err
		}
		fingerprints[relative] = fp
		fmt.Fprintf(digest, "%s\x00%s\x00", relative, fp)
	}
	status, err := git(root, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return "", false, nil, err
	}
	return hex.EncodeToString(digest.Sum(nil)), len(bytes.TrimSpace(status)) > 0, fingerprints, nil
}
func fingerprint(root, relative string) (string, error) {
	name := filepath.Join(root, filepath.FromSlash(relative))
	info, err := os.Lstat(name)
	if os.IsNotExist(err) {
		return "deleted", nil
	}
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(name)
		if err != nil {
			return "", err
		}
		digest.Write([]byte("link\x00" + target))
	case info.Mode().IsRegular():
		physical, err := filepath.EvalSymlinks(name)
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(root, physical)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", errorf("source file %q leaves its repository through a parent symlink", relative)
		}
		fmt.Fprintf(digest, "regular:%t\x00", info.Mode().Perm()&0111 != 0)
		f, err := os.Open(name)
		if err != nil {
			return "", err
		}
		_, readErr := io.Copy(digest, f)
		closeErr := f.Close()
		if readErr != nil {
			return "", readErr
		}
		if closeErr != nil {
			return "", closeErr
		}
	case info.IsDir():
		// A gitlink records the parent's declared dependency revision; its
		// checkout (or an untracked nested repository) is another source scope.
		// Never substitute the parent's HEAD or aggregate a child's files.
		entry, err := git(root, "ls-files", "--stage", "-z", "--", relative)
		if err != nil {
			return "", err
		}
		if fields := strings.Fields(string(entry)); len(fields) >= 2 && fields[0] == "160000" {
			digest.Write([]byte("gitlink\x00" + fields[1]))
		} else {
			digest.Write([]byte("nested-repository-boundary"))
		}

	default:
		return "", errorf("unsupported source file type %q", relative)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
