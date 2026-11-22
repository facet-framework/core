package atlaslauncher

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func openCacheLock(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

func detachCacheLock(path string) (func() error, error) {
	// Detach the name before marking the old file for deletion, so a new
	// invocation can open the lock path before the old handles close.
	dir, err := os.MkdirTemp(filepath.Dir(path), ".atlas-extract-")
	if err != nil {
		return nil, err
	}
	if err := os.Rename(path, filepath.Join(dir, ".lock")); err != nil {
		_ = os.Remove(dir)
		return nil, err
	}
	return func() error { return os.RemoveAll(dir) }, nil
}
