package facet_test

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
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/facet-framework/core/registry"
)

// The test binary doubles as the sidecar.
const helperEnv = "FACET_SIDECAR_HELPER"

// markerEnv names a file the helper writes as soon as it runs.
const markerEnv = "FACET_SIDECAR_MARKER"

func TestMain(m *testing.M) {
	// A child this binary was started as, with no helper mode, must not run
	// the suite.

	if len(os.Args) == 1 && os.Getenv(helperEnv) == "" {
		fmt.Fprintln(os.Stderr, "sidecar helper started without "+helperEnv+"; refusing to run the suite as a child")
		os.Exit(3)
	}
	switch os.Getenv(helperEnv) {
	case "echo":
		helperEcho()
		return
	case "silent":
		// Reads the call and never answers — the hanging sidecar.
		helperMark()
		_, _ = io.Copy(io.Discard, os.Stdin)
		select {}
	case "env":
		helperEnvDump()
		return
	case "lint":
		// The plugin-lint sidecar (pluginlint_test.go): answers a facet.lint
		// call the way FACET_LINT_HELPER_REPLY says.
		helperLint()
		return
	case "huge":
		// Announces a frame no machine can allocate, then says nothing.
		helperMark()
		_, _ = readTestFrame(bufio.NewReader(os.Stdin))
		fmt.Fprintf(os.Stdout, "Content-Length: 9223372036854775807\r\n\r\n")
		select {}
	case "headers":
		// Never sends the blank line that ends the header block.
		helperMark()
		_, _ = readTestFrame(bufio.NewReader(os.Stdin))
		for {
			fmt.Fprintf(os.Stdout, "X-Padding: %s\r\n", strings.Repeat("x", 64))
		}
	}
	// Fixtures must not inherit a repository from above the OS temporary root.
	ceiling := os.TempDir()
	if existing := os.Getenv("GIT_CEILING_DIRECTORIES"); existing != "" {
		ceiling += string(os.PathListSeparator) + existing
	}
	if err := os.Setenv("GIT_CEILING_DIRECTORIES", ceiling); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// helperMark records that this process ran at all.
func helperMark() {
	if path := os.Getenv(markerEnv); path != "" {
		_ = os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644)
	}
}

func helperEcho() {
	helperMark()
	r := bufio.NewReader(os.Stdin)
	for {
		body, err := readTestFrame(r)
		if err != nil {
			return
		}
		var call facet.SidecarCall
		if json.Unmarshal(body, &call) != nil {
			return
		}
		writeTestFrame(map[string]any{"result": map[string]any{"tool": call.Tool}})
	}
}

// lintReplyEnv tells helperLint how to answer; pluginlint_test.go sets it and
// grants it to the plugin, so it reaches the child the way any declared
// variable does.
const lintReplyEnv = "FACET_LINT_HELPER_REPLY"

// helperLint answers one facet.lint call.
func helperLint() {
	helperMark()
	r := bufio.NewReader(os.Stdin)
	body, err := readTestFrame(r)
	if err != nil {
		return
	}
	var call struct {
		Tool string `json:"tool"`
		Args struct {
			Records []any             `json:"records"`
			Files   map[string]string `json:"files"`
		} `json:"args"`
	}
	if json.Unmarshal(body, &call) != nil || call.Tool != "facet.lint" {
		writeTestFrame(map[string]any{"error": "not a facet.lint call"})
		return
	}
	switch os.Getenv(lintReplyEnv) {
	case "findings":
		writeTestFrame(map[string]any{"result": map[string]any{"findings": []map[string]any{
			{"fid": "a/b/c", "category": "gap", "severity": "Error", "message": "row 3 has no case"},
			{"fid": "a/b/c", "category": "overlap", "severity": "Warning", "message": "rows 1 and 2 overlap"},
		}}})
	case "warning":
		writeTestFrame(map[string]any{"result": map[string]any{"findings": []map[string]any{
			{"fid": "a/b/c", "category": "style", "severity": "Warning", "message": "only a warning"},
		}}})
	case "files":
		names := make([]string, 0, len(call.Args.Files))
		for n := range call.Args.Files {
			names = append(names, n)
		}
		sort.Strings(names)
		writeTestFrame(map[string]any{"result": map[string]any{"findings": []map[string]any{
			{"severity": "Warning", "category": "files", "message": "files=" + strings.Join(names, ",")},
			{"severity": "Warning", "category": "records", "message": "records=" + strconv.Itoa(len(call.Args.Records))},
		}}})
	case "error":
		writeTestFrame(map[string]any{"error": "the model could not be parsed"})
	case "nonsense":
		writeTestFrame(map[string]any{"result": "not an object"})
	case "nofind":
		writeTestFrame(map[string]any{"result": map[string]any{"rows": 3}})
	case "empty":
		writeTestFrame(map[string]any{"result": map[string]any{"findings": []any{}}})
	case "hang":
		select {}
	default:
		writeTestFrame(map[string]any{"error": "no " + lintReplyEnv + " given"})
	}
}

// helperEnvDump answers every call with the environment this process sees,
// and its pid beside it, so a test can tell two processes apart. The pid is
// its own field, never a line in env: a case that asserts "nothing beyond the
// manifest arrived" reads env as exactly the child's environment.
func helperEnvDump() {
	helperMark()
	r := bufio.NewReader(os.Stdin)
	for {
		if _, err := readTestFrame(r); err != nil {
			return
		}
		writeTestFrame(map[string]any{"result": map[string]any{"env": os.Environ(), "pid": os.Getpid()}})
	}
}

func readTestFrame(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
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
			length, _ = strconv.Atoi(strings.TrimSpace(value))
		}
	}
	if length < 0 {
		return nil, errors.New("no Content-Length")
	}
	body := make([]byte, length)
	_, err := io.ReadFull(r, body)
	return body, err
}

func writeTestFrame(v any) {
	body, _ := json.Marshal(v)
	fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(body), body)
}

func selfSum(t *testing.T) string {
	t.Helper()
	f, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatalf("opening the test binary: %v", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatalf("hashing the test binary: %v", err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func spec(t *testing.T, mode string) facet.SidecarSpec {
	t.Helper()
	t.Setenv(helperEnv, mode)
	// markerEnv travels with helperEnv: passThrough leaves out a name whose
	// variable is unset, so a case that does not use the marker is unaffected.
	return facet.SidecarSpec{Bin: os.Args[0], SHA256: selfSum(t), Env: []string{helperEnv, markerEnv}}
}

func TestASidecarWithTheWrongChecksumNeverRuns(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	t.Setenv(markerEnv, marker)
	s := spec(t, "echo")
	// The precondition: with the right checksum this binary does start, so
	// the refusal below is about the sum and not about a binary that cannot
	// run at all.
	allowed, err := facet.NewSidecar(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := allowed.Call(context.Background(), facet.SidecarCall{Tool: "confluence.pull"}); err != nil {
		t.Fatal(err)
	}
	allowed.Stop()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("valid fixture did not run: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	s.SHA256 = strings.Repeat("00", 32)
	_, err = facet.NewSidecar(s)

	// Drop the comparison in NewSidecar and this goes green while `go build`
	// stays green — the host would then run whatever is at that path.
	if !errors.Is(err, facet.ErrSidecarChecksum) {
		t.Fatalf("a mismatched checksum was let through: %v", err)
	}
	// The failure names the source rather than trying the next one, the same
	// rule bin/facet follows.
	if !strings.Contains(err.Error(), os.Args[0]) {
		t.Errorf("the refusal does not name the binary: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("checksum-refused binary ran: %v", err)
	}
}

func TestASidecarWithoutAChecksumIsRefused(t *testing.T) {
	s := spec(t, "echo")
	s.SHA256 = ""

	if _, err := facet.NewSidecar(s); err == nil {
		t.Fatal("a binary with no declared checksum was accepted")
	}
}

func TestACallReachesTheBinaryAndComesBack(t *testing.T) {
	s, err := facet.NewSidecar(spec(t, "echo"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	// Nothing runs before the first call: a plugin whose tools nobody calls
	// starts no process.
	if s.Running() {
		t.Fatal("the process started before it was called")
	}

	reply, err := s.Call(context.Background(), facet.SidecarCall{Tool: "confluence.pull"})
	if err != nil {
		t.Fatal(err)
	}
	if !s.Running() {
		t.Error("the process is not up after a call")
	}

	var got struct {
		Tool string `json:"tool"`
	}
	if err := json.Unmarshal(reply.Result, &got); err != nil {
		t.Fatalf("the reply is not what the sidecar sent: %v", err)
	}
	if got.Tool != "confluence.pull" {
		t.Errorf("the tool name did not survive the channel: %q", got.Tool)
	}
}

func TestOnlyTheNamedVariablesReachTheSidecar(t *testing.T) {
	t.Setenv("FACET_SIDECAR_SECRET", "secret")
	t.Setenv("FACET_SIDECAR_ALLOWED", "allowed")

	s := spec(t, "env")
	s.Env = []string{helperEnv, "FACET_SIDECAR_ALLOWED"}
	side, err := facet.NewSidecar(s)
	if err != nil {
		t.Fatal(err)
	}
	defer side.Stop()

	reply, err := side.Call(context.Background(), facet.SidecarCall{Tool: "env"})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Env []string `json:"env"`
	}
	if err := json.Unmarshal(reply.Result, &got); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got.Env, "\n")

	// The precondition: a named variable did arrive. Without it the assertion
	// below would pass for a child that got no environment at all.
	if !strings.Contains(joined, "FACET_SIDECAR_ALLOWED=allowed") {
		t.Fatalf("the named variable did not arrive: %v", got.Env)
	}
	// The promise. Replace passThrough with os.Environ() and this goes red
	// while every other case stays green — the sidecar would inherit the
	// host's whole environment, and with it every token in it.
	if strings.Contains(joined, "FACET_SIDECAR_SECRET") {
		t.Errorf("a variable the manifest never named reached the sidecar: %v", got.Env)
	}
}

func TestAHangingSidecarBecomesARefusalAndNotAWait(t *testing.T) {
	s := spec(t, "silent")
	s.Timeout = 200 * time.Millisecond
	side, err := facet.NewSidecar(s)
	if err != nil {
		t.Fatal(err)
	}
	defer side.Stop()

	start := time.Now()
	_, err = side.Call(context.Background(), facet.SidecarCall{Tool: "never"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a sidecar that never answered was reported as success")
	}
	// Remove the timeout branch from Call and this case hangs instead of
	// failing — which is the defect it exists against, and the reason the
	// bound is asserted rather than only the error.
	if elapsed > 3*time.Second {
		t.Errorf("the call waited %s on a sidecar that never answers", elapsed)
	}
	if !strings.Contains(err.Error(), "no answer within") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

func TestAnOversizedReplyIsRefusedRatherThanAllocated(t *testing.T) {
	s := spec(t, "huge")
	s.Timeout = 5 * time.Second
	side, err := facet.NewSidecar(s)
	if err != nil {
		t.Fatal(err)
	}
	defer side.Stop()

	_, err = side.Call(context.Background(), facet.SidecarCall{Tool: "x"})

	if err == nil {
		t.Fatal("a reply of 9223372036854775807 bytes was accepted")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("the refusal does not say the frame was too large: %v", err)
	}
}

func TestAHeaderBlockThatNeverEndsIsRefused(t *testing.T) {
	s := spec(t, "headers")
	// Well above the header bound and well below the wait this is about: the
	// case has to fail on the bound, not on the clock.
	s.Timeout = 10 * time.Second
	side, err := facet.NewSidecar(s)
	if err != nil {
		t.Fatal(err)
	}
	defer side.Stop()

	start := time.Now()
	_, err = side.Call(context.Background(), facet.SidecarCall{Tool: "x"})

	// Drop sidecarMaxHeaderLines and this waits for the full timeout while the
	// child's lines pile up in memory.
	if err == nil || !strings.Contains(err.Error(), "header lines") {
		t.Fatalf("an endless header block was not refused on the bound: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the bound did not bite before the timeout: %s", elapsed)
	}
}

func TestATimeoutDoesNotTakeTheHostDownWithIt(t *testing.T) {

	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))

	for i := 0; i < 200; i++ {
		s := spec(t, "silent")
		s.Timeout = time.Nanosecond
		side, err := facet.NewSidecar(s)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := side.Call(context.Background(), facet.SidecarCall{Tool: "never"}); err == nil {
			t.Fatal("a sidecar that never answers reported success")
		}
		side.Stop()
	}

}

func TestARestartVerifiesTheBinaryAgain(t *testing.T) {
	src, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir() + "/side"
	if err := os.WriteFile(bin, src, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(helperEnv, "silent")
	s := facet.SidecarSpec{
		Bin:     bin,
		SHA256:  selfSum(t),
		Env:     []string{helperEnv},
		Timeout: 200 * time.Millisecond,
	}
	side, err := facet.NewSidecar(s)
	if err != nil {
		t.Fatal(err)
	}
	defer side.Stop()

	// The precondition: a stop that leaves the object alive. Every stop path
	// does — a timeout, a broken pipe, a context that ended — and none of them
	// sets `dead`, so the next call starts the process again.
	if _, err := side.Call(context.Background(), facet.SidecarCall{Tool: "never"}); err == nil {
		t.Fatal("the silent helper answered")
	}

	t.Setenv(helperEnv, "echo")
	// Replace the executable inode rather than rewriting it while the OS
	// finishes releasing the timed-out helper.
	tampered := bin + ".tampered"
	if err := os.WriteFile(tampered, append(src, []byte("\n# tampered\n")...), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tampered, bin); err != nil {
		t.Fatal(err)
	}

	_, err = side.Call(context.Background(), facet.SidecarCall{Tool: "confluence.pull"})

	if !errors.Is(err, facet.ErrSidecarChecksum) {
		t.Fatalf("a tampered binary ran on the restart: %v", err)
	}
}

func TestAQueuedCallerCanStillObserveItsOwnContext(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	t.Setenv(markerEnv, marker)
	s := spec(t, "silent")
	s.Timeout = 30 * time.Second
	side, err := facet.NewSidecar(s)
	if err != nil {
		t.Fatal(err)
	}
	defer side.Stop()

	// The first caller occupies the sidecar for its whole timeout.
	go func() {
		_, _ = side.Call(context.Background(), facet.SidecarCall{Tool: "never"})
	}()
	// Wait until it is actually in, so the second caller queues rather than
	// racing the first. Without this the case would pass for the wrong reason.

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first caller never reached the sidecar")
		}
		time.Sleep(time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = side.Call(ctx, facet.SidecarCall{Tool: "second"})
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the queued caller did not end on its own deadline: %v", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("the queued caller waited %s for a deadline of 150ms", elapsed)
	}
}
