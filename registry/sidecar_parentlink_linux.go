//go:build linux

package facet

import (
	"os/exec"
	"syscall"
)

// parentLink asks the kernel to kill the sidecar when this process dies.
func parentLink(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}
