//go:build !windows

package remote

import (
	"os"
	"syscall"
)

// Nonblocking open prevents a FIFO swapped after Lstat from stalling publication.
const readFlags = os.O_RDONLY | syscall.O_NONBLOCK | syscall.O_NOFOLLOW
