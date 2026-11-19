package facet

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var ErrPluginHTTPRequiresChecksum = errors.New("plugin: an https:// source requires --sha256")

// ErrPluginPlainHTTPRefused means the source used http:// rather than
// https://. A checksum makes the download's integrity checkable but says
// nothing about who could read or replace it in flight before the check
// runs, which is exactly what TLS is for.
var ErrPluginPlainHTTPRefused = errors.New("plugin: only https:// sources are accepted")

// LoadOptions carries what a source needs beyond the source string itself.
type LoadOptions struct {
	// AssetSuffix selects a platform-specific GitHub release asset for native products.
	AssetSuffix string

	SHA256 string
	// HTTPClient is used for an https:// source. Nil means http.DefaultClient.
	HTTPClient *http.Client
	// GitHub carries what a github: source needs to reach an API standing in
	// for GitHub's — nil means the real one.
	GitHub *GitHubSource
}

// LoadPackage resolves src to a staged, unverified directory under work
// (which must exist and be empty) and reports whether the staged package's
// own checksum equals what was expected (for a source where one applies).
func LoadPackage(src string, work string, opts LoadOptions) (packageSHA256 string, err error) {
	switch {
	case strings.HasPrefix(src, "github:"):
		return loadFromGitHub(src, work, opts)
	case strings.HasPrefix(src, "https://"):
		return loadFromHTTPS(src, work, opts)
	case strings.HasPrefix(src, "http://"):
		return "", ErrPluginPlainHTTPRefused
	default:
		return loadFromLocal(src, work)
	}
}

func loadFromLocal(src, work string) (string, error) {
	info, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("plugin: %s: %w", src, err)
	}
	if info.IsDir() {
		return "", stageFromFolder(src, work)
	}
	return stageFromZipFile(src, work)
}

func stageFromFolder(dir, work string) error {
	var entries []sourceEntry
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		path := p
		entries = append(entries, sourceEntry{
			name: filepath.ToSlash(rel),
			mode: info.Mode(),
			open: func() (io.ReadCloser, error) { return os.Open(path) },
		})
		return nil
	})
	if err != nil {
		return err
	}
	flattened, err := flattenSingleTopFolder(entries)
	if err != nil {
		return err
	}
	return stagePackage(flattened, work)
}

// stageFromZipFile stages a zip already on local disk, and returns the
// package's own SHA256 — needed so an https:// or github: caller can compare
// it against what was expected before anything from it is trusted further.
func stageFromZipFile(zipPath, work string) (string, error) {
	sum, err := fileSHA256(zipPath)
	if err != nil {
		return "", err
	}
	if err := stageZipEntries(zipPath, work); err != nil {
		return "", err
	}
	return sum, nil
}

func stageZipEntries(zipPath, work string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("plugin: %s is not a zip: %w", zipPath, err)
	}
	defer r.Close()

	entries := make([]sourceEntry, 0, len(r.File))
	for _, f := range r.File {
		f := f
		if f.FileInfo().IsDir() {
			continue
		}
		entries = append(entries, sourceEntry{
			name: f.Name,
			mode: f.Mode(),
			open: func() (io.ReadCloser, error) { return f.Open() },
		})
	}
	flattened, err := flattenSingleTopFolder(entries)
	if err != nil {
		return err
	}
	return stagePackage(flattened, work)
}

func loadFromHTTPS(src, work string, opts LoadOptions) (string, error) {
	if opts.SHA256 == "" {
		return "", ErrPluginHTTPRequiresChecksum
	}
	tmp, err := os.CreateTemp("", "facet-plugin-dl-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	if err := downloadTo(client, src, tmp); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}

	sum, err := fileSHA256(tmpPath)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(sum, opts.SHA256) {
		return "", fmt.Errorf("%w: downloaded %s, expected %s", ErrPluginChecksumMismatch, sum, opts.SHA256)
	}
	if err := stageZipEntries(tmpPath, work); err != nil {
		return "", err
	}
	return sum, nil
}

func downloadTo(client *http.Client, url string, dst io.Writer) error {
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("plugin: fetching %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("plugin: fetching %s: server answered %s", url, resp.Status)
	}
	_, err = io.Copy(dst, resp.Body)
	return err
}
