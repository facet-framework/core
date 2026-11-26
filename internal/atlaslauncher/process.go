package atlaslauncher

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
)

func runAtlas(ctx context.Context, binary string, args []string, stdin io.Reader, stdout, stderr io.Writer, afterStart func()) error {
	cmd := exec.CommandContext(ctx, binary, append([]string{"ui"}, args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr

	signals := make(chan os.Signal, 2)
	// The terminal sends interrupts to Atlas directly in the shared group.
	// Keep the launcher waiting so it can return the child's exit status.
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	if err := cmd.Start(); err != nil {
		return err
	}

	afterStart()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case sig := <-signals:
			// On Windows the child shares the console and receives its
			// control events directly. Process.Signal cannot send them.
			if sig == syscall.SIGTERM && runtime.GOOS != "windows" {
				_ = cmd.Process.Signal(sig)
			}
		case err := <-done:
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				return childExit{exit}
			}
			return err
		}
	}
}

type childExit struct{ *exec.ExitError }

func (e childExit) Silent() bool { return true }

func (e childExit) ExitCode() int {
	if status, ok := e.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return e.ProcessState.ExitCode()
}
