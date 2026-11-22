//go:build !windows

package atlaslauncher

import "os"

func openCacheLock(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
}

func detachCacheLock(path string) (func() error, error) { return nil, os.Remove(path) }
