// @facet: delivery/distribution/product-runtime
// @facet-role: primary
package productplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"
)

// Invocation is supplied by the host, never read from package-controlled env.
// A product can call HostExecutable for Core data using the same selection.
type Invocation struct {
	Root           string   `json:"root"`
	SourceView     string   `json:"source_view,omitempty"`
	HostExecutable string   `json:"host_executable"`
	HostCommands   []string `json:"host_commands,omitempty"`
}
type Streams struct {
	In       io.Reader
	Out, Err io.Writer
}
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("product exited with status %d", e.Code) }
func (e *ExitError) ExitCode() int { return e.Code }
func (e *ExitError) Silent() bool  { return true }

func (s Store) command(ctx context.Context, id string, inv Invocation, args ...string) (*exec.Cmd, Manifest, error) {
	installed, dir, err := s.Resolve(id)
	if err != nil {
		return nil, Manifest{}, err
	}
	raw, err := json.Marshal(inv)
	if err != nil {
		return nil, Manifest{}, err
	}
	cmd := exec.CommandContext(ctx, filepath.Join(dir, filepath.FromSlash(installed.Manifest.Executable)), args...)
	cmd.Dir = inv.Root
	// OS runtime variables are required on Windows; everything else must be
	// declared. A native grant is still full OS access, not credential isolation.
	names := append([]string{"SYSTEMROOT", "WINDIR"}, installed.Manifest.Env...)
	cmd.Env = []string{}
	for _, name := range names {
		if value, ok := os.LookupEnv(name); ok {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}
	cmd.Env = append(cmd.Env, "FACET_PRODUCT_CONTEXT="+string(raw))
	configureProcess(cmd)
	return cmd, installed.Manifest, nil
}

// Run forwards arguments without a shell and preserves streams and exit code.
func (s Store) Run(ctx context.Context, id, command string, args []string, inv Invocation, streams Streams) error {
	argv := append([]string{"--facet-product-command", command, "--"}, args...)
	cmd, m, err := s.command(ctx, id, inv, argv...)
	if err != nil {
		return err
	}
	if !slices.Contains(m.Commands, command) {
		return fmt.Errorf("product %s does not offer command %s", id, command)
	}
	cmd.Stdin = streams.In
	cmd.Stdout = streams.Out
	cmd.Stderr = streams.Err
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code := exit.ExitCode()
			if code < 0 {
				code = 1
			}
			return &ExitError{code}
		}
		return err
	}
	return nil
}

type Request struct {
	Protocol int             `json:"protocol"`
	Payload  json.RawMessage `json:"payload"`
}
type Response struct {
	Protocol int             `json:"protocol"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
}
type boundedOutput struct {
	bytes.Buffer
	limit  int
	cancel context.CancelFunc
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		b.cancel()
		return 0, fmt.Errorf("product output exceeds limit")
	}
	return b.Buffer.Write(p)
}

// ServiceLimits bounds the bytes a service call may send and receive.
type ServiceLimits struct{ RequestBytes, ResponseBytes int }

// Call uses one process per service call, bounded input/output and a host-owned
// deadline. Plugin errors are explicit; an absent response is never success.
func (s Store) Call(ctx context.Context, id, service string, payload json.RawMessage, inv Invocation) (json.RawMessage, error) {
	return s.CallWithLimits(ctx, id, service, payload, inv, ServiceLimits{4 << 20, 4 << 20})
}

// CallWithLimits lets trusted host adapters accommodate existing bounded data
// contracts. Packages cannot raise these limits; the hard cap is 64 MiB.
func (s Store) CallWithLimits(ctx context.Context, id, service string, payload json.RawMessage, inv Invocation, limits ServiceLimits) (json.RawMessage, error) {
	if limits.RequestBytes <= 0 || limits.RequestBytes > 64<<20 || limits.ResponseBytes <= 0 || limits.ResponseBytes > 64<<20 {
		return nil, fmt.Errorf("invalid host service limits")
	}

	if len(payload) > limits.RequestBytes || !json.Valid(payload) {
		return nil, fmt.Errorf("invalid or oversized product request")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd, m, err := s.command(ctx, id, inv, "--facet-product-service", service)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(m.Services, service) {
		return nil, fmt.Errorf("product %s does not offer service %s", id, service)
	}
	raw, _ := json.Marshal(Request{Protocol: Protocol, Payload: payload})
	if len(raw) > limits.RequestBytes {
		return nil, fmt.Errorf("encoded product request exceeds size limit")
	}
	cmd.Stdin = bytes.NewReader(raw)
	stdout := &boundedOutput{limit: limits.ResponseBytes, cancel: cancel}
	stderr := &boundedOutput{limit: 64 << 10, cancel: cancel}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("product %s service %s: %w", id, service, ctx.Err())
		}
		return nil, fmt.Errorf("product %s service %s failed: %w", id, service, err)
	}
	var response Response
	d := json.NewDecoder(&stdout.Buffer)
	d.DisallowUnknownFields()
	if err = d.Decode(&response); err != nil {
		return nil, fmt.Errorf("invalid product response: %w", err)
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("product returned more than one response")
	}
	if response.Protocol != Protocol {
		return nil, fmt.Errorf("product response protocol mismatch")
	}
	if response.Error != "" {
		return nil, fmt.Errorf("product %s: %s", id, response.Error)
	}
	if !json.Valid(response.Result) {
		return nil, fmt.Errorf("product response has no result")
	}
	return response.Result, nil
}
