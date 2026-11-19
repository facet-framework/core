//go:build windows

// @facet: registry/integrity/runner-test-discovery
// @facet-role: related
package facet

import (
	"context"
	"os"
	"os/exec"
	"strconv"

	"github.com/facet-framework/core/pkg/filelock"
)

func acquireTestLock(ctx context.Context, file *os.File) error { return filelock.Acquire(ctx, file) }
func releaseTestLock(file *os.File)                            { filelock.Release(file) }
func configureTestProcess(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
