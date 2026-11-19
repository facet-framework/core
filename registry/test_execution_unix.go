//go:build !windows

// @facet: registry/integrity/runner-test-execution
// @facet-role: related
package facet

import (
	"os/exec"
	"syscall"
)

func runnerExitCode(exit *exec.ExitError) int {
	if code := exit.ExitCode(); code >= 0 {
		return code
	}
	if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return 1
}
