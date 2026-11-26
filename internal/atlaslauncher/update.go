package atlaslauncher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const defaultAPIURL = "https://api.github.com/repos/facet-framework/atlas/releases?per_page=30"
const updateStateName = ".updates.json"
const metadataLimit = 1 << 20

type updateState struct {
	Active        string    `json:"active,omitempty"`
	Fallback      string    `json:"fallback,omitempty"`
	Started       bool      `json:"started,omitempty"`
	Checked       time.Time `json:"checked"`
	Available     string    `json:"available,omitempty"`
	AvailableCore string    `json:"available_core,omitempty"`
	Platform      string    `json:"platform,omitempty"`
}

func (i installation) state() updateState {
	var state updateState
	data, err := readMetadata(filepath.Join(filepath.Dir(i.dir), updateStateName))
	if err != nil || json.Unmarshal(data, &state) != nil {
		return updateState{}
	}
	return state
}

func (i installation) saveState(state updateState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	parent := filepath.Dir(i.dir)
	file, err := os.CreateTemp(parent, ".atlas-update-state-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), filepath.Join(parent, updateStateName))
}

func newer(a, b string) bool { return semver.Compare("v"+a, "v"+b) > 0 }

func (l Launcher) compatible(core string) bool {
	return l.projectPin == nil || l.projectPin.Satisfied(core)
}

func (l Launcher) signedInstall(base installation, version string) (pin, installation, bool) {
	if len(l.keys) == 0 || !validVersion(version) || !newer(version, l.pin.Version) {
		return pin{}, installation{}, false
	}
	dir := filepath.Join(filepath.Dir(base.dir), version)
	data, err := readMetadata(filepath.Join(dir, manifestName))
	if err != nil {
		return pin{}, installation{}, false
	}
	sig, err := readMetadata(filepath.Join(dir, signatureName))
	if err != nil {
		return pin{}, installation{}, false
	}
	p, err := verifyManifest(data, sig, version, l.keys)
	a, supported := p.Platforms[l.platform]
	target := installation{asset: a, dir: dir, binary: base.binary}
	return p, target, err == nil && supported && target.installed()
}

func (l Launcher) active(base installation, state updateState) (selected pin, target installation, updateFloor string) {
	p, signed, ok := l.signedInstall(base, state.Active)
	if !ok {
		return l.pin, base, l.pin.Version
	}
	if !l.compatible(p.Core) {
		return l.pin, base, p.Version
	}
	return p, signed, p.Version
}

func releaseAssetURL(version, name string) string {
	base := os.Getenv("FACET_ATLAS_BASE_URL")
	if base == "" {
		base = defaultBaseURL
	}
	return strings.TrimRight(base, "/") + "/v" + url.PathEscape(version) + "/" + url.PathEscape(name)
}

func readMetadata(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("Atlas metadata is not a regular file: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return boundedMetadata(f)
}

func boundedMetadata(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, metadataLimit+1))
	if len(data) > metadataLimit {
		return nil, fmt.Errorf("Atlas release metadata exceeds %d bytes", metadataLimit)
	}
	return data, err
}

func (l Launcher) fetchMetadata(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "facet/"+l.coreVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("fetch Atlas metadata from %s: %w", url, errNotPublished)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch Atlas metadata from %s: %s", url, resp.Status)
	}
	return boundedMetadata(resp.Body)
}

// errNotPublished marks a release without a manifest or signature, such as one
// published before Atlas signed its releases. Discovery passes over it.
var errNotPublished = errors.New("not published")

type candidate struct {
	pin
	data, sig []byte
}

// @facet: delivery/distribution/atlas-updates
// @facet-role: primary

func (l Launcher) discover(ctx context.Context, active string) (candidate, error) {
	if len(l.keys) == 0 {
		return candidate{}, nil
	}
	api := os.Getenv("FACET_ATLAS_API_URL")
	if api == "" {
		api = defaultAPIURL
	}
	body, err := l.fetchMetadata(ctx, api)
	if err != nil {
		return candidate{}, err
	}
	var releases []struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.Unmarshal(body, &releases); err != nil {
		return candidate{}, err
	}
	var stable, rc []string
	for _, r := range releases {
		version, hasPrefix := strings.CutPrefix(r.Tag, "v")
		if r.Draft || !hasPrefix || !validVersion(version) {
			continue
		}
		pre := semver.Prerelease(r.Tag)
		switch {
		case pre == "" && !r.Prerelease:
			stable = append(stable, version)
		case strings.HasPrefix(pre, "-rc."):
			rc = append(rc, version)
		}
	}
	versions := stable
	if len(versions) == 0 && semver.Prerelease("v"+l.pin.Version) != "" {
		versions = rc
	}
	slices.SortFunc(versions, func(a, b string) int { return semver.Compare("v"+b, "v"+a) })
	for _, version := range versions {
		if !newer(version, active) || !newer(version, l.pin.Version) {
			break
		}
		data, err := l.fetchMetadata(ctx, releaseAssetURL(version, manifestName))
		if errors.Is(err, errNotPublished) {
			continue
		}
		if err != nil {
			return candidate{}, err
		}
		sig, err := l.fetchMetadata(ctx, releaseAssetURL(version, signatureName))
		if errors.Is(err, errNotPublished) {
			continue
		}
		if err != nil {
			return candidate{}, err
		}
		p, err := verifyManifest(data, sig, version, l.keys)
		if err != nil {
			return candidate{}, err
		}
		if _, ok := p.Platforms[l.platform]; ok && l.compatible(p.Core) {
			return candidate{p, data, sig}, nil
		}
	}
	return candidate{}, nil
}

func (l Launcher) cachedAvailable(state updateState, active string) string {
	if len(l.keys) > 0 && state.Platform == l.platform && l.compatible(state.AvailableCore) &&
		validVersion(state.Available) && newer(state.Available, active) {
		return state.Available
	}
	return ""
}

func (l Launcher) check(ctx context.Context, base installation, active string) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var checked time.Time
	var available string
	if err := base.withLock(ctx, func() error {
		state := base.state()
		if time.Since(state.Checked) < 24*time.Hour {
			available = l.cachedAvailable(state, active)
			return nil
		}
		checked = time.Now()
		state.Checked = checked
		state.Available = ""
		return base.saveState(state)
	}); err != nil || checked.IsZero() {
		return available
	}
	found, err := l.discover(ctx, active)
	if err != nil || found.Version == "" {
		return ""
	}
	_ = base.withLock(ctx, func() error {
		state := base.state()
		if state.Checked.Equal(checked) {
			state.Available, state.AvailableCore, state.Platform = found.Version, found.Core, l.platform
			return base.saveState(state)
		}
		return nil
	})
	return found.Version
}

func (l Launcher) update(ctx context.Context, base installation, stdout io.Writer) error {
	_, _, active := l.active(base, base.state())
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	found, err := l.discover(checkCtx, active)
	cancel()
	if err != nil {
		return fmt.Errorf("check for Atlas updates: %w", err)
	}
	if found.Version == "" {
		_, err := fmt.Fprintln(stdout, "No FACET Atlas update available.")
		return err
	}

	target := installation{
		asset:    found.Platforms[l.platform],
		dir:      filepath.Join(filepath.Dir(base.dir), found.Version),
		binary:   base.binary,
		metadata: map[string][]byte{manifestName: found.data, signatureName: found.sig},
	}
	activated := false
	err = base.withLock(ctx, func() error {
		state := base.state()
		// Re-read under the lock so parallel updates cannot move the cache backwards.
		if _, _, ok := l.signedInstall(base, state.Active); ok && !newer(found.Version, state.Active) {
			return nil
		}
		if err := target.installLocked(ctx, releaseAssetURL(found.Version, target.Archive), l.coreVersion, l.idleTimeout); err != nil {
			return err
		}
		if _, _, ok := l.signedInstall(base, state.Active); ok && (state.Started || state.Fallback == "") {
			state.Fallback = state.Active
		}
		state.Active, state.Started = found.Version, false
		state.Checked, state.Available = time.Now(), ""
		activated = true
		return base.saveState(state)
	})
	if err != nil {
		return err
	}
	if activated {
		_, err = fmt.Fprintf(stdout, "Installed FACET Atlas %s. Run 'facet ui' to start it.\n", found.Version)
	}
	return err
}

func (l Launcher) finishStart(ctx context.Context, base installation, version string) {
	_ = base.withLock(ctx, func() error {
		state := base.state()
		if state.Active != version || state.Started {
			return nil
		}
		if _, _, ok := l.signedInstall(base, version); !ok {
			return nil
		}
		if _, _, ok := l.signedInstall(base, state.Fallback); base.installed() || !ok {
			state.Fallback = l.pin.Version
		}
		parent := filepath.Dir(base.dir)
		entries, err := os.ReadDir(parent)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			name := entry.Name()
			if !entry.IsDir() || name == version || name == state.Fallback || !validVersion(name) || !newer(version, name) {
				continue
			}
			// Only remove directories with the launcher completion marker.
			if _, err := os.Lstat(filepath.Join(parent, name, markerName)); err == nil {
				if err := os.RemoveAll(filepath.Join(parent, name)); err != nil {
					return err
				}
			}
		}
		state.Started = true
		return base.saveState(state)
	})
}
