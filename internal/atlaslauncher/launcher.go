package atlaslauncher

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

const defaultBaseURL = "https://github.com/facet-framework/atlas/releases/download"
const markerName = ".complete"
const pendingMarker = ".complete.pending"

type asset struct {
	Archive string `json:"archive"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

type pin struct {
	Version   string           `json:"version"`
	Core      string           `json:"core"`
	Platforms map[string]asset `json:"platforms"`
}

//go:embed atlas.json
var pinJSON []byte

var embeddedPin = func() pin {
	var p pin
	if err := json.Unmarshal(pinJSON, &p); err != nil {
		panic("invalid embedded Atlas pin: " + err.Error())
	}
	return p
}()

// Help describes the launcher flags and where they can appear.
const Help = `Download and start FACET Atlas, the browser reader (AGPL-3.0).
The first run asks before downloading the pinned release.

Usage: facet ui [launcher flags] [Atlas flags]

Launcher flags (place them before any Atlas flags):
  --yes            Download without asking
  --atlas-version  Print the pinned Atlas and contained Core versions and local install status
  --remove         Remove the pinned version from the cache
  -h, --help       Show this help

The remaining arguments go to Atlas. Launcher flags after '--' are passed
through. Use 'facet ui --no-open' to start the reader without opening a browser.
`

// Launcher installs and starts the Atlas release pinned to this Core build.
type Launcher struct {
	pin         pin
	coreVersion string
	platform    string
	isTerminal  func() bool
	idleTimeout time.Duration
}

// New creates a launcher for the current platform.
func New(coreVersion string) Launcher {
	return Launcher{pin: embeddedPin, coreVersion: coreVersion, platform: runtime.GOOS + "/" + runtime.GOARCH}
}

// Version identifies the Atlas release selected at build time.
func (l Launcher) Version() string { return l.pin.Version }

// AtlasCoreVersion is the Core version built into the pinned Atlas release.
func (l Launcher) AtlasCoreVersion() string { return l.pin.Core }

// Status returns the cache directory and whether it holds the pinned release.
func (l Launcher) Status() (dir string, installed bool, err error) {
	target, err := l.target()
	if err != nil {
		return "", false, err
	}
	return target.dir, target.installed(), nil
}

type installation struct {
	asset
	dir    string
	binary string
}

func (l Launcher) target() (installation, error) {
	a, ok := l.pin.Platforms[l.platform]
	if !ok {
		return installation{}, fmt.Errorf("FACET Atlas is unavailable on %s (supported platforms: %s)",
			l.platform, strings.Join(slices.Sorted(maps.Keys(l.pin.Platforms)), ", "))
	}
	base := os.Getenv("FACET_ATLAS_CACHE")
	if base == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return installation{}, fmt.Errorf("locate Atlas cache: %w", err)
		}
		base = filepath.Join(cache, "facet", "atlas")
	}
	dir, err := filepath.Abs(filepath.Join(base, l.pin.Version))
	if err != nil {
		return installation{}, fmt.Errorf("locate Atlas cache: %w", err)
	}
	binary := "facet"
	if strings.HasPrefix(l.platform, "windows/") {
		binary += ".exe"
	}
	return installation{asset: a, dir: dir, binary: binary}, nil
}

func (i installation) installed() bool {
	marker := filepath.Join(i.dir, markerName)
	info, err := os.Lstat(marker)
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(i.SHA256)) {
		return false
	}
	content, err := os.ReadFile(marker)
	return err == nil && string(content) == i.SHA256 && i.hasBinary(i.dir)
}

func (i installation) hasBinary(dir string) bool {
	info, err := os.Lstat(filepath.Join(dir, i.binary))
	return err == nil && info.Mode().IsRegular() && (strings.HasSuffix(i.binary, ".exe") || info.Mode()&0o111 != 0)
}

type options struct {
	yes, help, version, remove bool
	atlasArgs                  []string
	projectArgs                []string
}

type usageError string

func (e usageError) Error() string { return string(e) }
func (e usageError) ExitCode() int { return 2 }

func parseOptions(args []string) (options, error) {
	var opts options
	for index, arg := range args {
		switch arg {
		case "--yes":
			opts.yes = true
		case "-h", "--help":
			opts.help = true
		case "--atlas-version":
			opts.version = true
		case "--remove":
			opts.remove = true
		default:
			opts.atlasArgs = args[index:]
			err := opts.scanAtlasArgs()
			return opts, err
		}
	}
	return opts, nil
}

func (o *options) scanAtlasArgs() error {
	args := o.atlasArgs
	for i := 0; i < len(args); i++ {
		start := i
		arg := args[i]
		if arg == "--" {
			break
		}
		flag, _, hasValue := strings.Cut(arg, "=")
		switch flag {
		case "--yes", "--atlas-version", "--remove":
			rest := append(append([]string{}, args[:i]...), args[i+1:]...)
			return usageError(fmt.Sprintf("launcher flag %s must come before Atlas flags: facet ui %s %s", flag, arg, strings.Join(rest, " ")))
		case "--root", "--source-view", "--port", "--registry", "--record", "--companion-model", "--companion-bin", "--test-report":
			// Atlas accepts dash-prefixed values for its value-taking flags.
			if !hasValue && i+1 < len(args) {
				i++
			}
			if flag == "--root" || flag == "--source-view" {
				o.projectArgs = append(o.projectArgs, args[start:i+1]...)
			}
		}
	}
	return nil
}

// StartSelection validates flag order and returns the project flags for preflight.
func StartSelection(args []string) (selection []string, start bool, err error) {
	opts, err := parseOptions(args)
	return opts.projectArgs, !opts.help && !opts.version && !opts.remove, err
}

// Run handles launcher flags and forwards the remaining arguments and streams.
func (l Launcher) Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}
	if opts.help {
		_, err := fmt.Fprint(stdout, Help)
		return err
	}
	if opts.version && opts.remove {
		return fmt.Errorf("--atlas-version and --remove cannot be used together")
	}
	target, err := l.target()
	if err != nil {
		return err
	}
	switch {
	case opts.version:
		release := fmt.Sprintf("FACET Atlas %s (Core %s)", l.Version(), l.AtlasCoreVersion())
		if target.installed() {
			_, err = fmt.Fprintf(stdout, "%s is installed at %s\n", release, target.dir)
		} else {
			_, err = fmt.Fprintf(stdout, "%s is not installed\n", release)
		}
		return err
	case opts.remove:
		removed, err := target.remove(ctx)
		if err != nil {
			return err
		}
		if removed {
			_, err = fmt.Fprintf(stdout, "Removed FACET Atlas %s cache at %s\n", l.Version(), target.dir)
		} else {
			_, err = fmt.Fprintf(stdout, "FACET Atlas %s is not installed\n", l.Version())
		}
		return err
	}
	if !target.installed() {
		baseURL := os.Getenv("FACET_ATLAS_BASE_URL")
		if baseURL == "" {
			baseURL = defaultBaseURL
		}
		url := strings.TrimRight(baseURL, "/") + "/v" + l.Version() + "/" + target.Archive
		if !opts.yes {
			if err := l.confirm(target, url, stdin, stderr); err != nil {
				return err
			}
		}
		if err := l.install(ctx, target, url); err != nil {
			return err
		}
	}
	return runAtlas(ctx, filepath.Join(target.dir, target.binary), opts.atlasArgs, stdin, stdout, stderr)
}

func (l Launcher) install(ctx context.Context, target installation, url string) error {
	interrupt, stopInterrupt := signal.NotifyContext(ctx, os.Interrupt)
	defer stopInterrupt()
	install, stopInstall := signal.NotifyContext(interrupt, syscall.SIGTERM)
	defer stopInstall()
	err := target.install(install, url, l.coreVersion, l.idleTimeout)
	if interrupt.Err() != nil && ctx.Err() == nil {
		return downloadInterrupted{fmt.Errorf("Atlas download interrupted: %w", interrupt.Err())}
	}
	return err
}

type downloadInterrupted struct{ error }

func (e downloadInterrupted) Unwrap() error { return e.error }
func (e downloadInterrupted) ExitCode() int { return 130 }

func (l Launcher) confirm(target installation, url string, stdin io.Reader, stderr io.Writer) error {
	isTerminal := l.isTerminal
	if isTerminal == nil {
		isTerminal = func() bool {
			file, ok := stdin.(*os.File)
			return ok && term.IsTerminal(int(file.Fd()))
		}
	}
	if !isTerminal() {
		return fmt.Errorf("FACET Atlas %s is not installed; run 'facet ui --yes' to allow the download without a terminal", l.Version())
	}
	_, err := fmt.Fprintf(stderr, "FACET Atlas %s (AGPL-3.0)\nArchive: %s (%.1f MB)\nSource: %s\nCache: %s\nDownload and start? [y/N] ",
		l.Version(), target.Archive, float64(target.Size)/1e6, url, target.dir)
	if err != nil {
		return err
	}
	// Read only the consent line so Atlas receives any remaining input.
	var answer strings.Builder
	var b [1]byte
	for answer.Len() < 128 {
		_, err := io.ReadFull(stdin, b[:])
		if err != nil && err != io.EOF {
			return fmt.Errorf("read Atlas download consent: %w", err)
		}
		if err == io.EOF || b[0] == '\n' {
			switch strings.ToLower(strings.TrimSpace(answer.String())) {
			case "y", "yes":
				return nil
			}
			break
		}
		answer.WriteByte(b[0])
	}
	return fmt.Errorf("Atlas download declined")
}
