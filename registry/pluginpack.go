package facet

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func Pack(dir, outPath string) (PackageManifest, error) {

	extra, err := sidecarBinariesIn(dir)
	if err != nil {
		return PackageManifest{}, err
	}
	manifest, err := VerifyPackage(dir, VerifyOptions{SkipSums: true, ExtraAllowed: extra})
	if err != nil {
		return PackageManifest{}, err
	}

	sums, err := measureSums(dir, manifest, extra)
	if err != nil {
		return PackageManifest{}, err
	}

	if manifest.Sidecar != nil {
		manifest.Sidecar.SHA256 = sumsForSidecar(sums, manifest.Sidecar.Bin)
	}

	manifestRaw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return PackageManifest{}, err
	}

	manifestSum := sha256.Sum256(manifestRaw)
	sums["plugin.json"] = hex.EncodeToString(manifestSum[:])

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return PackageManifest{}, err
	}
	out, err := os.Create(outPath)
	if err != nil {
		return PackageManifest{}, err
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	if err := writeZipEntry(zw, "plugin.json", manifestRaw, 0o644); err != nil {
		return PackageManifest{}, err
	}
	if err := writeZipEntry(zw, "SHA256SUMS", []byte(sumsFileBody(sums)), 0o644); err != nil {
		return PackageManifest{}, err
	}
	names := make([]string, 0, len(sums))
	for name := range sums {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if name == "plugin.json" {
			continue // written above, with the measured sidecar sums
		}
		content, mode, err := readPackedFile(dir, name)
		if err != nil {
			return PackageManifest{}, err
		}
		if err := writeZipEntry(zw, name, content, mode); err != nil {
			return PackageManifest{}, err
		}
	}
	if err := zw.Close(); err != nil {
		return PackageManifest{}, err
	}
	return manifest, nil
}

// PackageIDVersion reads a plugin folder's id and version from its
// plugin.json, for naming the package, without verifying anything — Pack
// verifies. Both must be present; their shape is Pack's to judge.
func PackageIDVersion(dir string) (string, string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil {
		return "", "", fmt.Errorf("plugin: %w", err)
	}
	var m struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", "", fmt.Errorf("plugin: plugin.json is not JSON: %w", err)
	}
	if m.ID == "" || m.Version == "" {
		return "", "", fmt.Errorf("plugin: plugin.json names no id or no version")
	}
	return m.ID, m.Version, nil
}

// sidecarBinariesIn names the files at dir's root that are the manifest's
// sidecar built for a platform the schema knows: "<bin>-<os>-<arch>", with
// ".exe" for windows. Only those — anything else at the root stays subject to
// the ordinary allow-list. A manifest without a sidecar names none.
func sidecarBinariesIn(dir string) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil {
		// VerifyPackage reports a missing or unreadable manifest itself.
		return nil, nil
	}
	var m struct {
		Sidecar *struct {
			Bin string `json:"bin"`
		} `json:"sidecar"`
	}
	if json.Unmarshal(raw, &m) != nil || m.Sidecar == nil || !validSidecarBin(m.Sidecar.Bin) {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		for _, platform := range sidecarPlatforms {
			if e.Name() == sidecarFileName(m.Sidecar.Bin, platform) {
				out = append(out, e.Name())
			}
		}
	}
	return out, nil
}

// sidecarPlatforms is every "<os>-<arch>" the manifest schema accepts as a
// key of sidecar.sha256 (schema/plugin-manifest.schema.json).
var sidecarPlatforms = []string{
	"linux-amd64", "linux-arm64",
	"darwin-amd64", "darwin-arm64",
	"windows-amd64", "windows-arm64",
}

// sumsForSidecar narrows a full sums map down to the platform => hex map a
// manifest's sidecar.sha256 wants, by reversing sidecarFileName back to the
// platform it was built for.
func sumsForSidecar(sums map[string]string, bin string) map[string]string {
	out := map[string]string{}
	prefix := bin + "-"
	for name, sum := range sums {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		platform := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".exe")
		out[platform] = sum
	}
	return out
}

func measureSums(dir string, m PackageManifest, extra []string) (map[string]string, error) {
	allow := allowedNames(m)
	for _, name := range extra {
		allow[name] = true
	}
	sums := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := filepath.ToSlash(mustRel(dir, p))
		if rel == "SHA256SUMS" {
			return nil
		}
		if !nameAllowed(rel, allow) {
			return fmt.Errorf("%w: %s", ErrPluginEntryOutsideAllowList, rel)
		}
		sum, err := fileSHA256(p)
		if err != nil {
			return err
		}
		sums[rel] = sum
		return nil
	})
	return sums, err
}

func sumsFileBody(sums map[string]string) string {
	names := make([]string, 0, len(sums))
	for name := range sums {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "%s  %s\n", sums[name], name)
	}
	return b.String()
}

func readPackedFile(dir, rel string) ([]byte, os.FileMode, error) {
	p := filepath.Join(dir, filepath.FromSlash(rel))
	content, err := os.ReadFile(p)
	if err != nil {
		return nil, 0, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, 0, err
	}
	return content, info.Mode(), nil
}

func writeZipEntry(zw *zip.Writer, name string, content []byte, mode os.FileMode) error {
	hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
	hdr.SetMode(mode)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = w.Write(content)
	return err
}
