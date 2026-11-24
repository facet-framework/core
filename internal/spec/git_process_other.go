//go:build !unix

package spec

import "os/exec"

func cancelGitProcessTree(_ *exec.Cmd) {}
