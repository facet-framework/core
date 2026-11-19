package release

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Install origins of the running executable. They are facts about where this
// file was found, not about how it was built: the same build can be installed
// by the dispatcher, shipped in an npm package or copied onto PATH.
const (
	OriginFacetCache = "facet-cache"
	OriginNPM        = "npm"
	OriginUnmanaged  = "unmanaged"
)

// InstallManifest is the file the dispatcher bin/facet writes beside each Core
// binary it installs under $BASE/dist/core/<distribution>/<platform>/<version>/.
const InstallManifest = "install.json"

// Install says where the running executable lives and which installer owns it.
// It is kept apart from Identity on purpose: Identity is fixed at build time,
// while Install depends on the file the process was started from.
//
// @facet: delivery/distribution/install-origin
// @facet-role: primary
type Install struct {
	Origin string `json:"origin"`
	Path   string `json:"path"`
	// Distribution and Version are carried from install.json for facet-cache.
	Distribution string `json:"distribution,omitempty"`
	Version      string `json:"version,omitempty"`
	// Note explains why an origin was not established, e.g. an install.json
	// that exists but cannot be trusted. Absent when nothing needs explaining.
	Note string `json:"note,omitempty"`
}

// CurrentInstall classifies the executable of this process after resolving
// symlinks, so a link on PATH reports the install it points to.
func CurrentInstall() Install {
	exe, err := os.Executable()
	if err != nil {
		return Install{Origin: OriginUnmanaged, Note: "the running executable could not be located: " + err.Error()}
	}
	return installAt(exe)
}

// installAt classifies the file a possibly symlinked executable path resolves
// to: a link on PATH into node_modules is an npm install, not a PATH install.
func installAt(exe string) Install {
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return ClassifyInstall(exe)
}

// ClassifyInstall classifies an already resolved executable path.
func ClassifyInstall(path string) Install {
	if hasNodeModulesElement(path) {
		return Install{Origin: OriginNPM, Path: path}
	}
	manifestPath := filepath.Join(filepath.Dir(path), InstallManifest)
	raw, err := os.ReadFile(manifestPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Install{Origin: OriginUnmanaged, Path: path}
	case err != nil:
		return Install{Origin: OriginUnmanaged, Path: path, Note: fmt.Sprintf("%s exists but cannot be read: %v", manifestPath, err)}
	}
	var manifest struct {
		Product      string `json:"product"`
		Distribution string `json:"distribution"`
		Version      string `json:"version"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Install{Origin: OriginUnmanaged, Path: path, Note: fmt.Sprintf("%s is not valid JSON: %v", manifestPath, err)}
	}
	if manifest.Product != "core" {
		return Install{Origin: OriginUnmanaged, Path: path, Note: fmt.Sprintf("%s names product %q, not core", manifestPath, manifest.Product)}
	}
	return Install{Origin: OriginFacetCache, Path: path, Distribution: manifest.Distribution, Version: manifest.Version}
}

// hasNodeModulesElement reports whether any element of path is exactly
// node_modules. Both separators count, so a Windows path reads the same on
// every platform the classification is tested on.
func hasNodeModulesElement(path string) bool {
	for _, element := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if element == "node_modules" {
			return true
		}
	}
	return false
}
