package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// setupGOOS is the operating system the setup commands behave for. A variable
// only so the Windows branch, which writes a wrapper instead of a symlink, can
// be exercised on the machine the tests run on.
var setupGOOS = runtime.GOOS

// shellProbeTimeout bounds every question put to the user's login shell. A
// profile that waits for input must cost a probe, never hang the report.
const shellProbeTimeout = 10 * time.Second

// cmdWrapperMarker is the second line of the wrapper written on Windows. It is
// how a later run recognises a wrapper it wrote itself, and so may replace.
const cmdWrapperMarker = "REM facet setup link"

// facetCacheBase is the dispatcher's (bin/facet) base directory. The
// expression is the dispatcher's own,
// `${CLAUDE_PLUGIN_DATA:-${XDG_CACHE_HOME:-$HOME/.cache}/facet}`, and has to
// stay equal to it — a link made against a different directory would point at
// nothing the dispatcher ever updates.
func facetCacheBase() string {
	if data := os.Getenv("CLAUDE_PLUGIN_DATA"); data != "" {
		return data
	}
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		cache = filepath.Join(home, ".cache")
	}
	return filepath.Join(cache, "facet")
}

// facetDistDir is where the dispatcher installs FACET Core:
// <distribution>/<platform>/<version> directories, and the version-free
// `selected` pointer beside them.
func facetDistDir() string {
	base := facetCacheBase()
	if base == "" {
		return ""
	}
	return filepath.Join(base, "dist", "core")
}

// currentPointerBinary names the binary behind the version-free `selected`
// pointer. The returned path goes THROUGH `selected` and is never resolved: the
// whole point of a link to it is that it survives the next update.
func currentPointerBinary() (string, error) {
	dist := facetDistDir()
	if dist == "" {
		return "", errors.New("no cache directory: set CLAUDE_PLUGIN_DATA or XDG_CACHE_HOME, or have a home directory")
	}
	for _, name := range []string{"facet", "facet.exe"} {
		candidate := filepath.Join(dist, "selected", name)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no binary behind %s — run facet once through the plugin's dispatcher (bin/facet) so it creates the pointer", filepath.Join(dist, "selected"))
}

// loginShell is the shell the person using this machine works in, as far as
// $SHELL tells.
type loginShell struct {
	Path string // $SHELL, possibly empty
	Name string // bash, zsh, fish, pwsh, ... — empty when unknown
}

func detectLoginShell() loginShell {
	path := os.Getenv("SHELL")
	if path == "" {
		if setupGOOS == "windows" {
			return loginShell{Name: "powershell"}
		}
		return loginShell{}
	}
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(path)), ".exe")
	return loginShell{Path: path, Name: name}
}

func (s loginShell) String() string {
	switch {
	case s.Path != "" && s.Name != "":
		return fmt.Sprintf("%s (%s)", s.Name, s.Path)
	case s.Name != "":
		return s.Name
	}
	return "unknown ($SHELL is not set)"
}

// runShell runs one script in a login shell and reports whether the timeout,
// rather than the script, ended it.
func runLoginShell(shell, script string, timeout time.Duration) (out string, timedOut bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-lc", script)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	// A profile may leave a child holding the pipe open after the shell is
	// killed; without a delay Run would wait for it past the deadline.
	cmd.WaitDelay = 2 * time.Second
	err = cmd.Run()
	return stdout.String(), ctx.Err() == context.DeadlineExceeded, err
}

// lastLine is the last non-empty line of a shell's output. A profile may print
// a banner before the answer; the answer is what the script printed last.
func lastOutputLine(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

func splitPathList(s, sep string) []string {
	var out []string
	for _, part := range strings.Split(s, sep) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// pathEntries reads the PATH the login shell would give a new terminal — not
// this process's, which inside Claude Code is a different one — and says how
// it was read, so the report can name what was actually run.
func (s loginShell) pathEntries() (entries []string, how string, err error) {
	if setupGOOS == "windows" {
		return splitPathList(os.Getenv("PATH"), ";"), "the PATH of this process (Windows has no login shell to ask)", nil
	}
	if s.Path == "" {
		return nil, "", errors.New("$SHELL is not set, so the login shell's PATH cannot be read")
	}
	script := "echo $PATH"
	if s.Name == "fish" {
		// fish keeps PATH as a list and `echo` would join it with spaces.
		script = "string join : $PATH"
	}
	how = fmt.Sprintf("%s -lc '%s'", s.Path, script)
	out, timedOut, err := runLoginShell(s.Path, script, shellProbeTimeout)
	if timedOut {
		return nil, how, fmt.Errorf("%s did not answer within %s", how, shellProbeTimeout)
	}
	if err != nil {
		return nil, how, fmt.Errorf("%s failed: %w", how, err)
	}
	line := lastOutputLine(out)
	if line == "" {
		return nil, how, fmt.Errorf("%s printed nothing", how)
	}
	return splitPathList(line, ":"), how, nil
}

// underHome reports whether dir lies strictly below home.
func underHome(dir, home string) bool {
	if home == "" {
		return false
	}
	rel, err := filepath.Rel(home, dir)
	if err != nil || rel == "." || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// dirWritable asks the filesystem rather than reading mode bits, which say
// nothing about ACLs, read-only mounts or a directory owned by someone else.
// The probe file is removed again at once.
func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".facet-writable-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

func isExistingDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// linkCandidates picks, from the login shell's PATH, the directories the user
// could put the link in without touching anything outside their home: they
// exist, are writable, and lie under $HOME — or, on macOS, are Homebrew's
// prefix. A system directory is never offered, even when it happens to be
// writable.
func linkCandidates(entries []string, home string) []string {
	var out []string
	seen := map[string]bool{}
	for _, entry := range entries {
		dir := filepath.Clean(entry)
		if seen[dir] || !filepath.IsAbs(dir) {
			continue
		}
		seen[dir] = true
		if !underHome(dir, home) && !(setupGOOS == "darwin" && dir == "/opt/homebrew/bin") {
			continue
		}
		if isExistingDir(dir) && dirWritable(dir) {
			out = append(out, dir)
		}
	}
	return out
}

// dirOnPath compares directories as the filesystem sees them, so a PATH entry
// spelled through a symlink or with a trailing slash still counts.
func dirOnPath(dir string, entries []string) bool {
	same := func(a, b string) bool {
		if setupGOOS == "windows" {
			return strings.EqualFold(a, b)
		}
		return a == b
	}
	want := filepath.Clean(dir)
	wantReal, _ := filepath.EvalSymlinks(want)
	for _, entry := range entries {
		got := filepath.Clean(entry)
		if same(got, want) {
			return true
		}
		if gotReal, err := filepath.EvalSymlinks(got); err == nil && wantReal != "" && same(gotReal, wantReal) {
			return true
		}
	}
	return false
}

// rcInstruction is the line that would put dir on the PATH of a new shell, and
// the file it belongs in. It is only ever printed; nothing here writes it.
func rcInstruction(shell loginShell, dir, home string) (file, line string) {
	shown := dir
	if underHome(dir, home) {
		if rel, err := filepath.Rel(home, dir); err == nil {
			shown = "$HOME/" + filepath.ToSlash(rel)
		}
	}
	switch shell.Name {

	case "zsh":
		return "~/.zprofile", fmt.Sprintf(`export PATH="%s:$PATH"`, shown)
	case "bash":
		for _, name := range []string{".bash_profile", ".bash_login"} {
			if home != "" {
				if _, err := os.Stat(filepath.Join(home, name)); err == nil {
					return "~/" + name, fmt.Sprintf(`export PATH="%s:$PATH"`, shown)
				}
			}
		}
		return "~/.profile", fmt.Sprintf(`export PATH="%s:$PATH"`, shown)
	case "fish":
		return "~/.config/fish/config.fish", fmt.Sprintf("fish_add_path %s", shown)
	case "pwsh", "powershell":
		return "your PowerShell profile ($PROFILE)", fmt.Sprintf(`$env:Path = "%s;$env:Path"`, dir)
	}
	return "your shell's startup file (~/.profile for POSIX shells)", fmt.Sprintf(`export PATH="%s:$PATH"`, shown)
}

// facetCacheLinks match a link into a facet binary cache wherever the cache
// lives: the plugin's data directory and ~/.cache/facet both end in
// `facet-ish-dir/dist/core/(selected|<distribution>/<platform>/<version or current>)/facet`.
// The older `facet-ish-dir/bin/<version or current>/facet` shape is still
// recognised only so that a link this command made before can be replaced; the
// dispatcher no longer reads that cache.
var facetCacheLinks = []*regexp.Regexp{
	regexp.MustCompile(`^.*facet.*/dist/core/(selected|[^/]+/[^/]+/[^/]+)/facet(\.exe)?$`),
	regexp.MustCompile(`^.*facet.*/bin/(current|[0-9][^/]*)/facet(\.exe)?$`),
}

// pointsIntoFacetCache reports whether an existing link was plausibly made by
// this command or by the dispatcher's cache, and so may be replaced. A link to
// some other `facet` is somebody's choice and is left alone.
func pointsIntoFacetCache(target string) bool {
	target = filepath.Clean(target)
	if base := facetCacheBase(); base != "" {
		for _, dir := range []string{filepath.Join(base, "dist", "core"), filepath.Join(base, "bin")} {
			if strings.HasPrefix(target, dir+string(filepath.Separator)) {
				return true
			}
		}
	}
	for _, re := range facetCacheLinks {
		if re.MatchString(filepath.ToSlash(target)) {
			return true
		}
	}
	return false
}

// @facet: delivery/distribution/binary-link
// @facet-role: primary
func newSetupLinkCmd() *cobra.Command {
	var candidates, force bool
	var dir string
	cmd := &cobra.Command{
		Use:   "link",
		Short: "Make facet reachable from your shell with a link to the current binary",
		Long: `Links a directory on your login shell's PATH to the binary behind
<cache>/dist/core/selected/facet, so a terminal, an editor and a git hook find
'facet' and not only the processes Claude Code starts.

  --candidates   list the directories you could use: on the PATH your login
                 shell ($SHELL -lc 'echo $PATH') gives a new terminal, under
                 your home directory, existing and writable (on macOS also
                 /opt/homebrew/bin when writable). Names the shell it asked.
  --dir <path>   create <path>/facet. The link goes to the version-free
                 'selected' pointer, never to a versioned directory, so it
                 survives an update. On Windows a facet.cmd wrapper is
                 written instead, because symlinks there need a privilege.

Refused, so that nothing of yours is overwritten silently:
  - a <path> that is not on that PATH (--force links anyway)
  - a <path>/facet that is a regular file or directory
  - a <path>/facet that is a symlink pointing anywhere but into a facet cache

This command never edits a shell startup file. When <path> is not on the PATH
it prints the line to add and the file it belongs in, and you decide.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if candidates == (dir != "") {
				return errors.New("give exactly one of --candidates and --dir <path>")
			}
			shell := detectLoginShell()
			entries, how, perr := shell.pathEntries()
			if candidates {
				if perr != nil {
					return perr
				}
				return printLinkCandidates(cmd.OutOrStdout(), shell, how, entries)
			}
			return linkInto(cmd.OutOrStdout(), shell, entries, perr, dir, force)
		},
	}
	cmd.Flags().BoolVar(&candidates, "candidates", false, "list user-writable directories on your login shell's PATH")
	cmd.Flags().StringVar(&dir, "dir", "", "directory to put the facet link in")
	cmd.Flags().BoolVar(&force, "force", false, "link even when the directory is not on your login shell's PATH")
	return cmd
}

func printLinkCandidates(w io.Writer, shell loginShell, how string, entries []string) error {
	home, _ := os.UserHomeDir()
	found := linkCandidates(entries, home)
	fmt.Fprintf(w, "shell: %s\n", shell)
	fmt.Fprintf(w, "PATH read from: %s\n", how)
	if len(found) == 0 {
		fmt.Fprintln(w, "candidates: none — no existing, writable directory under your home is on that PATH")
		fmt.Fprintln(w, "Pick a directory yourself and run: facet setup link --dir <path> --force")
		fmt.Fprintln(w, "It prints the line your shell's startup file needs; nothing is written there for you.")
		return nil
	}
	fmt.Fprintln(w, "candidates:")
	for _, dir := range found {
		fmt.Fprintf(w, "  %s\n", dir)
	}
	fmt.Fprintln(w, "Choose one and run: facet setup link --dir <path>")
	return nil
}

func linkInto(w io.Writer, shell loginShell, entries []string, pathErr error, dir string, force bool) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if !isExistingDir(dir) {
		return fmt.Errorf("%s is not an existing directory — create it first; this command does not", dir)
	}
	pointer, err := currentPointerBinary()
	if err != nil {
		return err
	}

	home, _ := os.UserHomeDir()
	file, line := rcInstruction(shell, dir, home)
	onPath := pathErr == nil && dirOnPath(dir, entries)
	if !onPath && !force {
		reason := fmt.Sprintf("%s is not on the PATH of your login shell (%s)", dir, shell)
		if pathErr != nil {
			reason = fmt.Sprintf("cannot tell whether %s is on your login shell's PATH: %v", dir, pathErr)
		}
		return fmt.Errorf("%s.\nA link there would not be found. Add the directory to the PATH first — add this line to %s:\n  %s\nor pass --force to link anyway", reason, file, line)
	}

	var linkPath string
	if setupGOOS == "windows" {
		linkPath, err = writeCmdWrapper(dir, pointer)
	} else {
		linkPath, err = writeSymlink(dir, pointer)
	}
	if err != nil {
		return err
	}

	resolved, err := filepath.EvalSymlinks(linkPath)
	if setupGOOS != "windows" {
		if err != nil {
			return fmt.Errorf("linked %s but it does not resolve: %w", linkPath, err)
		}
		fmt.Fprintf(w, "linked %s -> %s\n", linkPath, pointer)
		fmt.Fprintf(w, "it resolves to %s today; after an update it follows the pointer\n", resolved)
	} else {
		fmt.Fprintf(w, "wrote %s calling %s\n", linkPath, pointer)
	}
	if !onPath {
		fmt.Fprintf(w, "\n%s is not on your login shell's PATH, so a new terminal will not find facet yet.\n", dir)
		fmt.Fprintf(w, "Add this line to %s, then open a new shell:\n  %s\n", file, line)
		fmt.Fprintln(w, "facet setup link does not edit startup files.")
	}
	return nil
}

// writeSymlink creates dir/facet pointing at the current pointer, replacing
// only a link that already points into a facet cache.
func writeSymlink(dir, pointer string) (string, error) {
	linkPath := filepath.Join(dir, "facet")
	st, err := os.Lstat(linkPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return "", err
	case st.Mode()&os.ModeSymlink == 0:
		return "", fmt.Errorf("%s exists and is not a symlink — it is left alone; move it away yourself if it should be replaced", linkPath)
	default:
		existing, err := os.Readlink(linkPath)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(existing) {
			existing = filepath.Join(dir, existing)
		}
		if !pointsIntoFacetCache(existing) {
			return "", fmt.Errorf("%s is a symlink to %s, which is not a facet cache — it is left alone", linkPath, existing)
		}
	}

	// Made under a temporary name and renamed over the old one, so there is no
	// moment at which `facet` does not exist in a directory on the PATH.
	tmp := filepath.Join(dir, ".facet-link-"+strconv.Itoa(os.Getpid()))
	os.Remove(tmp)
	if err := os.Symlink(pointer, tmp); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, linkPath); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return linkPath, nil
}

// writeCmdWrapper is the Windows form: a facet.cmd that calls the pointer.
// Only a wrapper carrying this command's marker is ever replaced.
func writeCmdWrapper(dir, pointer string) (string, error) {
	path := filepath.Join(dir, "facet.cmd")
	if existing, err := os.ReadFile(path); err == nil {
		if !strings.Contains(string(existing), cmdWrapperMarker) {
			return "", fmt.Errorf("%s exists and was not written by facet setup link — it is left alone", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	body := "@echo off\r\n" + cmdWrapperMarker + "\r\n\"" + pointer + "\" %*\r\n"
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return path, nil
}
