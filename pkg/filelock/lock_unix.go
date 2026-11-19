//go:build !windows

// @facet: registry/integrity/runner-test-discovery
// @facet-role: related
package filelock

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

// Acquire waits for an exclusive advisory lock until the context is cancelled.
func Acquire(ctx context.Context, file *os.File) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Release relinquishes the advisory lock held by file.
func Release(file *os.File) { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }
