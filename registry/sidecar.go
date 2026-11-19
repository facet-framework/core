// Running a plugin's own binary, and nothing of its business.

package facet

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SidecarSpec is what a plugin manifest declares about its binary.
type SidecarSpec struct {
	// Bin is the executable's path, already resolved for this OS and arch.
	Bin string
	// SHA256 is the checksum the binary must have, lowercase hex.
	SHA256 string
	// Env names the variables to pass through. Nothing else reaches the child.
	Env []string
	// Timeout bounds one call. Zero means SidecarDefaultTimeout.
	Timeout time.Duration
}

// SidecarDefaultTimeout bounds a call when the manifest names none.
const SidecarDefaultTimeout = 30 * time.Second

// SidecarMaxFrame bounds one reply.
const SidecarMaxFrame = 8 << 20 // 8 MiB

// sidecarMaxHeaderLines bounds the header block.
const sidecarMaxHeaderLines = 64

// ErrSidecarChecksum is returned when the binary is not the one that was
// verified. The caller aborts and names the source; it does not try another.
var ErrSidecarChecksum = errors.New("sidecar: checksum does not match")

// SidecarCall is one request to a plugin's binary.
type SidecarCall struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args,omitempty"`
}

// SidecarReply is what comes back. Exactly one of Result or Error is set.
type SidecarReply struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Sidecar is one plugin's running binary.
type Sidecar struct {
	spec SidecarSpec

	// sem admits one call at a time, and admission is a select rather than a
	// Lock.
	sem chan struct{}

	mu   sync.Mutex
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Reader
	dead bool
}

// NewSidecar checks the binary before anything runs it.
func NewSidecar(spec SidecarSpec) (*Sidecar, error) {
	if spec.Bin == "" {
		return nil, errors.New("sidecar: no binary named")
	}
	if spec.SHA256 == "" {
		return nil, fmt.Errorf("sidecar: %s has no checksum — refusing to run it", spec.Bin)
	}
	if err := verify(spec); err != nil {
		return nil, err
	}
	if spec.Timeout <= 0 {
		spec.Timeout = SidecarDefaultTimeout
	}
	return &Sidecar{spec: spec, sem: make(chan struct{}, 1)}, nil
}

// verify hashes what is at the path and compares it with the manifest.
func verify(spec SidecarSpec) error {
	sum, err := fileSHA256(spec.Bin)
	if err != nil {
		return fmt.Errorf("sidecar: %s: %w", spec.Bin, err)
	}
	if !strings.EqualFold(sum, spec.SHA256) {
		return fmt.Errorf("%w: %s has %s, manifest says %s",
			ErrSidecarChecksum, spec.Bin, sum, strings.ToLower(spec.SHA256))
	}
	return nil
}

// Call sends one request and waits for its reply.
func (s *Sidecar) Call(ctx context.Context, call SidecarCall) (SidecarReply, error) {
	// Admission before anything else, and cancellable. A caller waiting behind
	// another must be able to observe its own context — see the sem field.
	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return SidecarReply{}, ctx.Err()
	}
	defer func() { <-s.sem }()

	in, out, err := s.begin()
	if err != nil {
		return SidecarReply{}, err
	}

	body, err := json.Marshal(call)
	if err != nil {
		return SidecarReply{}, err
	}
	if _, err := fmt.Fprintf(in, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
		s.stop()
		return SidecarReply{}, fmt.Errorf("sidecar: writing the call: %w", err)
	}

	type outcome struct {
		reply SidecarReply
		err   error
	}
	done := make(chan outcome, 1)
	go func() {

		raw, err := readFrame(out)
		if err != nil {
			done <- outcome{err: fmt.Errorf("sidecar: reading the reply: %w", err)}
			return
		}
		var reply SidecarReply
		if err := json.Unmarshal(raw, &reply); err != nil {
			done <- outcome{err: fmt.Errorf("sidecar: reply is not JSON: %w", err)}
			return
		}
		done <- outcome{reply: reply}
	}()

	timeout := time.NewTimer(s.spec.Timeout)
	defer timeout.Stop()

	select {
	case o := <-done:
		if o.err != nil {
			s.stop()
		}
		return o.reply, o.err
	case <-timeout.C:
		// The refusal, not a wait. The process is stopped because a sidecar
		// that owes an answer cannot be asked a second question.
		s.stop()
		return SidecarReply{}, fmt.Errorf("sidecar: no answer within %s", s.spec.Timeout)
	case <-ctx.Done():
		s.stop()
		return SidecarReply{}, ctx.Err()
	}
}

// begin makes sure a process is up and hands this call the pipes to use.
func (s *Sidecar) begin() (io.Writer, *bufio.Reader, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead {
		return nil, nil, errors.New("sidecar: already stopped")
	}
	if s.cmd == nil {
		if err := s.start(); err != nil {
			return nil, nil, err
		}
	}
	return s.in, s.out, nil
}

func (s *Sidecar) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked()
}

// Stop ends the process. Safe to call more than once.
func (s *Sidecar) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked()
	s.dead = true
}

// Running reports whether a process is up. For the host's own reporting.
func (s *Sidecar) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmd != nil
}

func (s *Sidecar) start() error {
	// Verified before every exec, not once at construction.

	if err := verify(s.spec); err != nil {
		return err
	}
	cmd := exec.Command(s.spec.Bin)
	// Only the named variables. Not the host's environment minus something:
	// a list built by removing grows a hole the first time somebody adds a
	// variable and nobody remembers to remove it too.
	cmd.Env = passThrough(s.spec.Env)
	// The child's stderr is the host's, so a sidecar that complains is heard
	// rather than silently swallowed.
	cmd.Stderr = os.Stderr
	// Ask the OS to kill the child when this process dies, where the OS can
	// (Linux only).
	parentLink(cmd)

	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("sidecar: starting %s: %w", s.spec.Bin, err)
	}
	s.cmd, s.in, s.out = cmd, in, bufio.NewReader(out)
	return nil
}

func (s *Sidecar) stopLocked() {
	if s.cmd == nil {
		return
	}
	if s.in != nil {
		_ = s.in.Close()
	}
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.cmd.Wait()
	s.cmd, s.in, s.out = nil, nil, nil
}

// passThrough builds the child's environment from names alone.
func passThrough(names []string) []string {
	env := make([]string, 0, len(names))
	for _, name := range names {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// readFrame reads one Content-Length framed message.
func readFrame(r *bufio.Reader) ([]byte, error) {
	length := -1
	for lines := 0; ; lines++ {
		if lines >= sidecarMaxHeaderLines {
			return nil, fmt.Errorf("sidecar: more than %d header lines", sidecarMaxHeaderLines)
		}
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if name, value, ok := strings.Cut(line, ":"); ok &&
			strings.EqualFold(strings.TrimSpace(name), "content-length") {
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, fmt.Errorf("bad Content-Length: %w", err)
			}
		}
	}
	if length < 0 {
		return nil, errors.New("message without Content-Length")
	}
	// Before the allocation, not after: `make([]byte, length)` with a length
	// the child chose panics above the platform's limit, and the panic is in
	// a goroutine nobody recovers — it ends the whole process, not the call.
	if length > SidecarMaxFrame {
		return nil, fmt.Errorf("sidecar: reply of %d bytes exceeds %d", length, SidecarMaxFrame)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}
