//go:build windows

// @facet: registry/integrity/runner-test-execution
// @facet-role: related
package facet

import "os/exec"

func runnerExitCode(exit *exec.ExitError) int {
	if code := exit.ExitCode(); code >= 0 {
		return code
	}
	return 1
}
