//go:build !linux

package facet

import "os/exec"

// parentLink does nothing here, and that absence is the honest report.
func parentLink(*exec.Cmd) {}
