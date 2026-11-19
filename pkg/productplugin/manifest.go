// Package productplugin defines the portable executable product protocol.
// It has no dependency on FACET's registry or any product implementation.
//
// @facet: delivery/distribution/product-runtime
// @facet-role: primary
package productplugin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

const Protocol = 1
const MaxFileBytes = 64 << 20
const MaxPackageBytes = 256 << 20
const MaxManifestBytes = 1 << 20

// Manifest is plugin.json for kind=product. Core compatibility is determined
// by protocol, independently of the product's release version. Files binds every
// payload to a digest; the installer separately binds the manifest itself.
// Native execution grants ordinary user-level OS access, not a sandbox.
type Manifest struct {
	Kind       string            `json:"kind"`
	ID         string            `json:"id"`
	Version    string            `json:"version"`
	Protocol   int               `json:"protocol"`
	Platform   string            `json:"platform"`
	Executable string            `json:"executable"`
	Commands   []string          `json:"commands"`
	Services   []string          `json:"services,omitempty"`
	Requires   []string          `json:"requires,omitempty"`
	Env        []string          `json:"env,omitempty"`
	Files      map[string]string `json:"files"`
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
var envPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func ValidID(id string) bool { return len(id) <= 80 && namePattern.MatchString(id) }

func Decode(raw []byte) (Manifest, error) {
	var m Manifest
	if len(raw) > MaxManifestBytes {
		return m, fmt.Errorf("product manifest exceeds size limit")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return m, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return m, fmt.Errorf("expected exactly one product manifest")
	}
	return m, m.Validate()
}

func (m Manifest) Validate() error {
	if m.Kind != "product" || !ValidID(m.ID) || !versionPattern.MatchString(m.Version) {
		return fmt.Errorf("invalid product kind, id or version")
	}
	if m.Protocol != Protocol {
		return fmt.Errorf("product %s requires protocol %d; host offers %d", m.ID, m.Protocol, Protocol)
	}
	requirements := map[string]bool{}
	for _, required := range m.Requires {
		switch required {
		case "product-comparisons-v1", "product-mcp-v1", "observers-v1", "large-service-input-v1":
		default:
			return fmt.Errorf("product %s requires unsupported host capability %q", m.ID, required)
		}
		if requirements[required] {
			return fmt.Errorf("duplicate host requirement %q", required)
		}
		requirements[required] = true
	}
	if m.Platform != runtime.GOOS+"-"+runtime.GOARCH {
		return fmt.Errorf("product %s targets %s, host is %s-%s", m.ID, m.Platform, runtime.GOOS, runtime.GOARCH)
	}
	if len(m.Commands) == 0 || len(m.Commands) > 32 || len(m.Services) > 32 || len(m.Requires) > 32 || len(m.Env) > 64 || len(m.Files) == 0 || len(m.Files) > 1000 {
		return fmt.Errorf("invalid product declaration size")
	}
	for _, names := range [][]string{m.Commands, m.Services} {
		seen := map[string]bool{}
		for _, n := range names {
			if !ValidID(n) || seen[n] {
				return fmt.Errorf("invalid or duplicate product capability %q", n)
			}
			seen[n] = true
		}
	}
	seenEnv := map[string]bool{}
	for _, n := range m.Env {
		if !envPattern.MatchString(n) || seenEnv[n] || strings.HasPrefix(n, "FACET_PRODUCT_") {
			return fmt.Errorf("invalid or reserved product environment %q", n)
		}
		seenEnv[n] = true
	}
	if _, ok := m.Files[m.Executable]; !ok {
		return fmt.Errorf("product executable is not hash-bound")
	}
	for name, sum := range m.Files {
		if !safePath(name) || name == "plugin.json" {
			return fmt.Errorf("invalid product payload path %q", name)
		}
		digest, err := hex.DecodeString(sum)
		if err != nil || len(digest) != sha256.Size || sum != strings.ToLower(sum) {
			return fmt.Errorf("invalid product digest for %s", name)
		}
	}
	return nil
}
func safePath(s string) bool {
	if !fs.ValidPath(s) || s == "." || strings.ContainsAny(s, "\\:") {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
	}
	return path.Clean(s) == s
}

// Verify accepts regular, declared files only and checks all bytes before any
// executable is started. Symlinks and special files are never payloads.
func Verify(dir string) (Manifest, string, error) {
	var empty Manifest
	rootInfo, rootErr := os.Lstat(dir)
	if rootErr != nil {
		return empty, "", rootErr
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return empty, "", fmt.Errorf("product package must be a real directory")
	}
	p := filepath.Join(dir, "plugin.json")
	info, err := os.Lstat(p)
	if err != nil {
		return empty, "", err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxManifestBytes {
		return empty, "", fmt.Errorf("invalid product manifest file")
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return empty, "", err
	}
	m, err := Decode(raw)
	if err != nil {
		return empty, "", err
	}
	found := map[string]bool{}
	var total int64
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(dir, p)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular product file %s", rel)
		}
		if rel == "plugin.json" {
			return nil
		}
		want, ok := m.Files[rel]
		if !ok {
			return fmt.Errorf("undeclared product file %s", rel)
		}
		if info.Size() > MaxFileBytes {
			return fmt.Errorf("product file exceeds size limit: %s", rel)
		}
		total += info.Size()
		if total > MaxPackageBytes {
			return fmt.Errorf("product package exceeds size limit")
		}
		f, e := os.Open(p)
		if e != nil {
			return e
		}
		h := sha256.New()
		n, e := io.Copy(h, io.LimitReader(f, MaxFileBytes+1))
		closeErr := f.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		if n > MaxFileBytes || hex.EncodeToString(h.Sum(nil)) != want {
			return fmt.Errorf("product checksum mismatch: %s", rel)
		}
		found[rel] = true
		return nil
	})
	if err != nil {
		return empty, "", err
	}
	for name := range m.Files {
		if !found[name] {
			return empty, "", fmt.Errorf("missing product file %s", name)
		}
	}
	sum := sha256.Sum256(raw)
	return m, hex.EncodeToString(sum[:]), nil
}

func (m Manifest) FileNames() []string {
	names := make([]string, 0, len(m.Files))
	for name := range m.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
