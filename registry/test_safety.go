// @facet: registry/integrity/runner-test-discovery
// @facet-role: related

package facet

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Lock names are shared between processes but kept outside the registry.
func lockTestBaseline(ctx context.Context, path string) (func(), error) {
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if os.IsNotExist(err) {
		return func() {}, nil
	}
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(filepath.Join(parent, filepath.Base(path))))
	file, err := os.OpenFile(filepath.Join(os.TempDir(), fmt.Sprintf("facet-test-baseline-%x.lock", key)), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = acquireTestLock(ctx, file); err != nil {
		file.Close()
		return nil, err
	}
	return func() { releaseTestLock(file); file.Close() }, nil
}

// A failed write never truncates the last reviewed baseline. Rename publishes
// only a complete, synced replacement with the original permissions.
func writeTestBaseline(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".facet-test-baseline-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err = file.Chmod(info.Mode().Perm()); err != nil {
		file.Close()
		return err
	}
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func testListCommand(ctx context.Context, command string, files ...string) *exec.Cmd {
	args := []string{"-c", command}
	if len(files) > 0 {
		args = append(append(args, "sh"), files...)
	}
	cmd := exec.CommandContext(ctx, "sh", args...)
	// Bound the pipe drain too; a detached child must not delay cancellation.
	cmd.WaitDelay = 100 * time.Millisecond
	configureTestProcess(cmd)
	return cmd
}
