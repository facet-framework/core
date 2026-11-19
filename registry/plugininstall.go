package facet

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SourceKind names where an installed plugin's bytes came from, written into
// .source.json so a later `update` knows how to fetch the same thing again.
type SourceKind string

const (
	SourceKindFile   SourceKind = "file"
	SourceKindFolder SourceKind = "folder"
	SourceKindHTTPS  SourceKind = "https"
	SourceKindGitHub SourceKind = "github"
)

type SourceInfo struct {
	Kind        SourceKind `json:"kind"`
	Where       string     `json:"where"`
	PackageSHA  string     `json:"package_sha256"`
	InstalledAt time.Time  `json:"installed_at"`

	Dev bool `json:"dev"`
}

// ErrPluginAlreadyInstalled means the id already has a directory, and
// --replace was not given.
var ErrPluginAlreadyInstalled = errors.New("plugin: already installed — use --replace")

// InstallOptions carries what Install needs beyond the staged, verified
// package itself.
type InstallOptions struct {
	Replace bool
	Source  SourceInfo

	DevLinkTarget string
}

// Install moves a staged, already-verified package (see VerifyPackage) into
// dir/<id>, atomically: stage first, verify (the caller's job, before
// calling Install), then rename — so a check that fails never leaves a
// half-written plugin directory where the broker could find it.
func Install(dir string, id string, staged string, manifest PackageManifest, previous *PackageManifest, opts InstallOptions) error {
	if !validPluginID(id) {
		return fmt.Errorf("plugin: %q is not a plugin id", id)
	}
	dest := filepath.Join(dir, id)
	_, err := os.Stat(dest)
	exists := err == nil
	if exists && !opts.Replace {
		return fmt.Errorf("%w: %s", ErrPluginAlreadyInstalled, id)
	}

	var savedGrants []byte
	if exists {
		keep := previous != nil && !previous.AddedRights(manifest)
		if keep {
			savedGrants, _ = os.ReadFile(filepath.Join(dest, "grants.json"))
		}
		if err := os.RemoveAll(dest); err != nil {
			return err
		}
	}

	if opts.DevLinkTarget != "" {
		if err := os.Symlink(opts.DevLinkTarget, dest); err != nil {
			return err
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := os.Rename(staged, dest); err != nil {
			return err
		}
	}

	sourceRaw, err := json.MarshalIndent(opts.Source, "", "  ")
	if err != nil {
		return err
	}
	if err := writeIntoPluginDir(dest, ".source.json", sourceRaw, opts.DevLinkTarget != ""); err != nil {
		return err
	}
	if len(savedGrants) > 0 {
		if err := writeIntoPluginDir(dest, "grants.json", savedGrants, opts.DevLinkTarget != ""); err != nil {
			return err
		}
	}
	return nil
}

// writeIntoPluginDir writes a file that is not part of the package proper
// (.source.json, grants.json) into the plugin's directory.
func writeIntoPluginDir(dest, name string, data []byte, dev bool) error {
	target := filepath.Join(dest, name)
	if dev {
		target = filepath.Join(filepath.Dir(dest), filepath.Base(dest)+"."+name)
	}
	return os.WriteFile(target, data, 0o644)
}

// SourceInfoPath is the counterpart writeIntoPluginDir uses for reading:
// where .source.json (or grants.json) lives for a given plugin directory,
// given whether it is a --dev symlink. Exported so a caller outside this
// package (a server handing out an installed plugin's files, which has to
// know whether a served plugin is a --dev install before deciding whether to
// check it against SHA256SUMS) reads .source.json from the same place this package writes
// it, rather than assuming it always lives inside the plugin directory
// itself — which is false for exactly the --dev case that matters most.
func SourceInfoPath(dest, name string) (string, bool) {
	if info, err := os.Lstat(dest); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return filepath.Join(filepath.Dir(dest), filepath.Base(dest)+"."+name), true
	}
	return filepath.Join(dest, name), false
}

type InstalledPlugin struct {
	ID       string
	Manifest PackageManifest
	Source   SourceInfo
	// VerifyError is set when the installed content no longer verifies —
	// most relevant for a --dev install, whose bytes can change between
	// `add --dev` and the next time this is read.
	VerifyError error
	// State is the one column a caller should read to ask "is this plugin
	// loadable": "ok", or "invalid: <reason>". VerifyError already carries
	// the same fact, but json.Marshal turns an error into "{}" — a plugin
	// list served as JSON needs a plain string, not a value only Go's error
	// interface can print, so State is computed once, here, and is what both
	// `facet plugin list`'s text and --json output show.
	State string
}

const pluginStateOK = "ok"

func pluginState(verr error) string {
	if verr == nil {
		return pluginStateOK
	}
	return "invalid: " + verr.Error()
}

// InstalledPlugins lists every plugin installed under dir, sorted by id. A
// directory that does not exist yields an empty list, not an error — an
// unconfigured or fresh plugins directory is the ordinary state, the one
// pluginsDir returns when no plugin was ever added.
func InstalledPlugins(dir string) ([]InstalledPlugin, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []InstalledPlugin
	for _, e := range entries {
		if !e.IsDir() && e.Type()&os.ModeSymlink == 0 {
			continue
		}
		id := e.Name()
		if !validPluginID(id) {
			continue
		}
		dest := filepath.Join(dir, id)
		if _, err := os.Stat(filepath.Join(dest, "plugin.json")); err != nil {
			continue
		}
		ip := InstalledPlugin{ID: id}

		sourcePath, _ := SourceInfoPath(dest, ".source.json")
		if raw, err := os.ReadFile(sourcePath); err == nil {
			_ = json.Unmarshal(raw, &ip.Source)
		}

		verifyRoot := dest
		if resolved, err := filepath.EvalSymlinks(dest); err == nil {
			verifyRoot = resolved
		}

		m, verr := VerifyPackage(verifyRoot, VerifyOptions{
			SkipSums:     ip.Source.Dev,
			ExtraAllowed: []string{".source.json", "grants.json"},
		})

		if verr == nil && m.ID != id {
			verr = fmt.Errorf("plugin: directory %q carries a manifest declaring the id %q", id, m.ID)
		}
		ip.Manifest = m
		ip.VerifyError = verr
		ip.State = pluginState(verr)
		out = append(out, ip)
	}
	return out, nil
}
