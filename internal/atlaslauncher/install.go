package atlaslauncher

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/facet-framework/core/pkg/filelock"
)

func (i installation) lockPath() string { return filepath.Join(filepath.Dir(i.dir), ".lock") }

func (i installation) withLock(ctx context.Context, update func() error) error {
	parent := filepath.Dir(i.dir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create Atlas cache: %w", err)
	}
	lock, err := acquireCacheLock(ctx, i.lockPath())
	if err != nil {
		return fmt.Errorf("lock Atlas cache: %w", err)
	}
	defer lock.Close()
	defer filelock.Release(lock)
	if err := sweepStaleTemps(parent, time.Now().Add(-time.Hour)); err != nil {
		return fmt.Errorf("clean up Atlas cache: %w", err)
	}
	return update()
}

func acquireCacheLock(ctx context.Context, path string) (*os.File, error) {
	for {
		lock, err := openCacheLock(path)
		if err != nil {
			return nil, err
		}
		if err := filelock.Acquire(ctx, lock); err != nil {
			lock.Close()
			return nil, err
		}
		// A remover can detach the lock while another process is waiting on
		// it. Only the handle still named by path protects the cache.
		opened, openErr := lock.Stat()
		current, statErr := os.Stat(path)
		if openErr == nil && statErr == nil && os.SameFile(opened, current) {
			return lock, nil
		}
		filelock.Release(lock)
		lock.Close()
		if openErr != nil {
			return nil, openErr
		}
		if statErr != nil && !os.IsNotExist(statErr) {
			return nil, statErr
		}
	}
}

func sweepStaleTemps(parent string, cutoff time.Time) error {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, ".atlas-download-") && !strings.HasPrefix(name, ".atlas-extract-") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().Before(cutoff) {
			if err := os.RemoveAll(filepath.Join(parent, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (i installation) remove(ctx context.Context) (bool, error) {
	var removed bool
	var cleanup func() error
	err := i.withLock(ctx, func() error {
		_, err := os.Lstat(i.dir)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("inspect Atlas cache: %w", err)
		}
		removed = err == nil
		if err := os.RemoveAll(i.dir); err != nil {
			return fmt.Errorf("remove Atlas cache: %w", err)
		}
		cleanup, err = detachCacheLock(i.lockPath())
		if err != nil {
			return fmt.Errorf("remove Atlas cache lock: %w", err)
		}
		return nil
	})
	if cleanup != nil {
		if err := cleanup(); err != nil {
			return removed, fmt.Errorf("clean up detached Atlas cache lock: %w", err)
		}
	}
	return removed, err
}

func (i installation) install(ctx context.Context, url, coreVersion string, idleTimeout time.Duration) error {
	return i.withLock(ctx, func() error {
		if i.installed() {
			return nil
		}
		parent := filepath.Dir(i.dir)
		download, err := os.CreateTemp(parent, ".atlas-download-")
		if err != nil {
			return fmt.Errorf("create Atlas download file: %w", err)
		}
		defer os.Remove(download.Name())
		err = i.download(ctx, download, url, coreVersion, idleTimeout)
		closeErr := download.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return fmt.Errorf("close Atlas download: %w", closeErr)
		}

		stage, err := os.MkdirTemp(parent, ".atlas-extract-")
		if err != nil {
			return fmt.Errorf("create Atlas extraction directory: %w", err)
		}
		defer os.RemoveAll(stage)
		if err := extract(ctx, download.Name(), i.Archive, stage); err != nil {
			return fmt.Errorf("extract Atlas archive: %w", err)
		}
		if !i.hasBinary(stage) {
			return fmt.Errorf("Atlas archive is missing an executable %s", i.binary)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return i.publish(stage)
	})
}

func (i installation) publish(stage string) error {
	// Stage the marker contents before publishing, then commit the marker
	// with a rename after the extracted directory is in place.
	if err := os.WriteFile(filepath.Join(stage, pendingMarker), []byte(i.SHA256), 0o600); err != nil {
		return fmt.Errorf("prepare Atlas cache marker: %w", err)
	}
	if err := os.RemoveAll(i.dir); err != nil {
		return fmt.Errorf("clear incomplete Atlas cache: %w", err)
	}
	if err := os.Rename(stage, i.dir); err != nil {
		return fmt.Errorf("publish Atlas cache: %w", err)
	}
	if err := os.Rename(filepath.Join(i.dir, pendingMarker), filepath.Join(i.dir, markerName)); err != nil {
		_ = os.RemoveAll(i.dir)
		return fmt.Errorf("complete Atlas cache: %w", err)
	}
	return nil
}

func (i installation) download(ctx context.Context, file *os.File, url, coreVersion string, idleTimeout time.Duration) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("prepare Atlas download: %w", err)
	}
	req.Header.Set("User-Agent", "facet/"+coreVersion)
	client := &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ForceAttemptHTTP2:     true,
	}}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download Atlas: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download Atlas from %s: %s", url, resp.Status)
	}
	if idleTimeout == 0 {
		idleTimeout = 60 * time.Second
	}
	body := idleReader{resp.Body, idleTimeout, cancel, ctx}
	hash := sha256.New()
	// Bound disk use by the pinned size. Hash any excess so the mismatch
	// reports the digest of the full response.
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(body, i.Size))
	if err != nil {
		return fmt.Errorf("write Atlas download: %w", err)
	}
	extra, err := io.Copy(hash, body)
	if err != nil {
		return fmt.Errorf("finish Atlas download: %w", err)
	}
	actual := fmt.Sprintf("%x", hash.Sum(nil))
	if actual != i.SHA256 {
		return fmt.Errorf("Atlas archive SHA-256 mismatch: expected %s, got %s", i.SHA256, actual)
	}
	if written != i.Size || extra != 0 {
		return fmt.Errorf("Atlas archive size mismatch: expected %d bytes, got %d", i.Size, written+extra)
	}
	return nil
}

var errIdleDownload = errors.New("Atlas download stalled waiting for data")

type idleReader struct {
	body    io.Reader
	timeout time.Duration
	cancel  context.CancelCauseFunc
	ctx     context.Context
}

func (r idleReader) Read(p []byte) (int, error) {
	timer := time.AfterFunc(r.timeout, func() { r.cancel(errIdleDownload) })
	n, err := r.body.Read(p)
	if !timer.Stop() {
		r.cancel(errIdleDownload)
	}
	if cause := context.Cause(r.ctx); cause != nil {
		return n, cause
	}
	return n, err
}
