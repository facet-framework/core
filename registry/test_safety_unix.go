//go:build !windows

// @facet: registry/integrity/runner-test-discovery
// @facet-role: related
package facet

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"

	"github.com/facet-framework/core/pkg/filelock"
)

func acquireTestLock(ctx context.Context, file *os.File) error { return filelock.Acquire(ctx, file) }
func releaseTestLock(file *os.File)                            { filelock.Release(file) }
func configureTestProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
