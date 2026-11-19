// @facet: delivery/distribution/product-runtime
// @facet-role: primary
package productplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/facet-framework/core/pkg/filelock"
)

type Store struct{ Dir string }
type Installed struct {
	Manifest        Manifest `json:"manifest"`
	ManifestSHA256  string   `json:"manifest_sha256"`
	NativeExecution bool     `json:"native_execution"`
}

func (s Store) locked(ctx context.Context, f func() error) error {
	if s.Dir == "" {
		return fmt.Errorf("product store directory is empty")
	}
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(s.Dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = filelock.Acquire(ctx, lock); err != nil {
		return err
	}
	defer filelock.Release(lock)
	return f()
}
func (s Store) activePath(id string) string { return filepath.Join(s.Dir, id+".json") }
func (s Store) packagePath(id, hash string) string {
	return filepath.Join(s.Dir, "packages", id+"-"+hash)
}

// Install copies a verified staged package to an immutable generation, then
// atomically selects it. Every install requires an explicit native-code grant.
// The caller stages downloads through the existing Core package loader.
func (s Store) Install(ctx context.Context, staged string, grant, replace bool) (Installed, error) {
	var installed Installed
	if !grant {
		return installed, fmt.Errorf("product installation requires --grant-native: executes with your OS user permissions")
	}
	m, hash, err := Verify(staged)
	if err != nil {
		return installed, err
	}
	installed = Installed{m, hash, true}
	err = s.locked(ctx, func() error {
		if _, e := os.Lstat(s.activePath(m.ID)); e == nil && !replace {
			return fmt.Errorf("product %s is installed; use --replace", m.ID)
		} else if e != nil && !os.IsNotExist(e) {
			return e
		}
		entries, e := os.ReadDir(s.Dir)
		if e != nil {
			return e
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			otherID := strings.TrimSuffix(entry.Name(), ".json")
			if otherID == m.ID {
				continue
			}
			other, _, e := s.Resolve(otherID)
			if e != nil {
				return fmt.Errorf("cannot check installed product %s for command conflicts: %w", otherID, e)
			}
			for _, name := range m.Commands {
				if slices.Contains(other.Manifest.Commands, name) {
					return fmt.Errorf("product command %s conflicts with product %s", name, otherID)
				}
			}
		}
		packages := filepath.Join(s.Dir, "packages")
		if e := os.MkdirAll(packages, 0700); e != nil {
			return e
		}
		work, e := os.MkdirTemp(packages, ".stage-")
		if e != nil {
			return e
		}
		defer os.RemoveAll(work)
		for _, name := range append(m.FileNames(), "plugin.json") {
			target := filepath.Join(work, filepath.FromSlash(name))
			if e := os.MkdirAll(filepath.Dir(target), 0700); e != nil {
				return e
			}
			if e := copyFile(filepath.Join(staged, filepath.FromSlash(name)), target); e != nil {
				return e
			}
		}
		copied, got, e := Verify(work)
		if e != nil {
			return e
		}
		if got != hash || copied.ID != m.ID {
			return fmt.Errorf("product changed during installation")
		}
		if e := os.Chmod(filepath.Join(work, filepath.FromSlash(m.Executable)), 0700); e != nil {
			return e
		}
		dest := s.packagePath(m.ID, hash)
		if _, e := os.Lstat(dest); os.IsNotExist(e) {
			if e := os.Rename(work, dest); e != nil {
				return e
			}
		} else if e != nil {
			return e
		} else {
			_, existing, e := Verify(dest)
			if e != nil {
				return e
			}
			if existing != hash {
				return fmt.Errorf("installed generation is corrupt")
			}
		}
		raw, e := json.Marshal(installed)
		if e != nil {
			return e
		}
		file, e := os.CreateTemp(s.Dir, ".active-")
		if e != nil {
			return e
		}
		name := file.Name()
		defer os.Remove(name)
		if _, e = file.Write(raw); e == nil {
			e = file.Sync()
		}
		closeErr := file.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		return os.Rename(name, s.activePath(m.ID))
	})
	return installed, err
}
func copyFile(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("product source is not regular: %s", src)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(in, MaxFileBytes+1))
	closeErr := out.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if n > MaxFileBytes {
		return fmt.Errorf("product source exceeds size limit")
	}
	return nil
}

// Resolve rechecks payload hashes and the granted manifest binding on every
// invocation. Store files belong to the installing user, not to a checkout.
func (s Store) Resolve(id string) (Installed, string, error) {
	var in Installed
	if !ValidID(id) {
		return in, "", fmt.Errorf("invalid product id")
	}
	p := s.activePath(id)
	info, err := os.Lstat(p)
	if err != nil {
		return in, "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 2*MaxManifestBytes {
		return in, "", fmt.Errorf("invalid installed product metadata")
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return in, "", err
	}
	if err = json.Unmarshal(raw, &in); err != nil {
		return in, "", err
	}
	if len(in.ManifestSHA256) != 64 || strings.Trim(in.ManifestSHA256, "0123456789abcdef") != "" || !in.NativeExecution || in.Manifest.ID != id {
		return in, "", fmt.Errorf("invalid product grant or manifest identity")
	}
	dir := s.packagePath(id, in.ManifestSHA256)
	m, hash, err := Verify(dir)
	if err != nil {
		return in, "", err
	}
	if hash != in.ManifestSHA256 || m.ID != id {
		return in, "", fmt.Errorf("product manifest changed after approval")
	}
	in.Manifest = m
	return in, dir, nil
}

func (s Store) List() ([]Installed, error) {
	out := []Installed{}
	entries, err := os.ReadDir(s.Dir)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		in, _, err := s.Resolve(id)
		if err != nil {
			return nil, fmt.Errorf("product %s: %w", id, err)
		}
		out = append(out, in)
	}
	return out, nil
}

// Remove revokes discovery and removes installed generations without touching
// repository data. A running executable may prevent file removal on Windows;
// the error is reported and removal can be retried after the process exits.
func (s Store) Remove(ctx context.Context, id string) error {
	if !ValidID(id) {
		return fmt.Errorf("invalid product id")
	}
	return s.locked(ctx, func() error {
		if err := os.Remove(s.activePath(id)); err != nil && !os.IsNotExist(err) {
			return err
		}
		packages := filepath.Join(s.Dir, "packages")
		entries, err := os.ReadDir(packages)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			suffix, ok := strings.CutPrefix(entry.Name(), id+"-")
			if !ok || len(suffix) != 64 || strings.Trim(suffix, "0123456789abcdef") != "" {
				continue
			}
			if err := os.RemoveAll(filepath.Join(packages, entry.Name())); err != nil {
				return fmt.Errorf("product unregistered; generation cleanup failed: %w", err)
			}
		}
		return nil
	})
}

// DefaultStore is shared by every host command (CLI and MCP) so installation
// has one scope.
func DefaultStore() Store {
	dir := os.Getenv("FACET_PRODUCTS_DIR")
	if dir == "" {
		base := os.Getenv("XDG_DATA_HOME")
		if base == "" {
			if home, err := os.UserHomeDir(); err == nil {
				base = filepath.Join(home, ".local", "share")
			}
		}
		if base != "" {
			dir = filepath.Join(base, "facet", "products")
		}
	}
	return Store{Dir: dir}
}
