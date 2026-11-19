package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/facet-framework/core/registry"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newPluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Install, list, remove and pack plugins",
	}
	cmd.AddCommand(newPluginAddCmd(), newPluginListCmd(), newPluginRemoveCmd(),
		newPluginUpdateCmd(), newPluginPackCmd(), newProductPluginCmd())
	return cmd
}

// pluginsDirForCmd is the same directory every host command points its plugin
// broker at — a command that installed somewhere else would install a plugin
// no host ever finds.
func pluginsDirForCmd() (string, error) {
	dir := PluginsDir()
	if dir == "" {
		return "", errors.New("plugin: no plugins directory — set FACET_PLUGINS_DIR or XDG_DATA_HOME, or have a home directory")
	}
	return dir, nil
}

func newPluginAddCmd() *cobra.Command {
	var sha256Flag string
	var replace bool
	var dev bool
	var grants []string

	cmd := &cobra.Command{
		Use:   "add <source>",
		Short: "Install a plugin package from a file, folder, https URL, or github:owner/repo@tag",
		Long: `Plugin packages are checked for manifest compatibility, allowed files and
checksums before installation. An https:// source requires --sha256; a
github:owner/repo@tag source verifies against that release's own checksums.txt.

With --dev, a working folder is linked instead of copied. Manifest and file
checks still apply, but package checksums are not required for the working folder.

Without a terminal, nothing is granted unless --grant names it explicitly.
--grant may only name a right the manifest itself declares.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPluginAdd(cmd, args[0], pluginAddOptions{
				sha256:  sha256Flag,
				replace: replace,
				dev:     dev,
				grants:  grants,
			})
		},
	}
	cmd.Flags().StringVar(&sha256Flag, "sha256", "", "expected SHA256 of the package (required for an https:// source)")
	cmd.Flags().BoolVar(&replace, "replace", false, "replace an already-installed plugin of the same id")
	cmd.Flags().BoolVar(&dev, "dev", false, "link a working folder instead of copying it")
	cmd.Flags().StringArrayVar(&grants, "grant", nil, grantFlagUsage)
	return cmd
}

// grantFlagUsage names the four groups --grant knows, once for add and update.
const grantFlagUsage = "grant a right without asking: tools=a,b / env=X,Y / ai=a,b / ai-env=X,Y"

type pluginAddOptions struct {
	sha256  string
	replace bool
	dev     bool
	grants  []string
}

func runPluginAdd(cmd *cobra.Command, source string, opts pluginAddOptions) error {
	dir, err := pluginsDirForCmd()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()

	if opts.dev {
		return addDevPlugin(cmd, dir, source, opts)
	}

	work, err := os.MkdirTemp("", "facet-plugin-stage-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	packageSum, err := facet.LoadPackage(source, work, facet.LoadOptions{SHA256: opts.sha256})
	if err != nil {
		return err
	}

	manifest, err := facet.VerifyPackage(work, facet.VerifyOptions{})
	if err != nil {
		return err
	}

	previous := installedManifest(dir, manifest.ID, false)
	kept := keptGrant(dir, manifest, previous, opts.replace)

	kind := facet.SourceKindFile
	switch {
	case strings.HasPrefix(source, "github:"):
		kind = facet.SourceKindGitHub
	case strings.HasPrefix(source, "https://"):
		kind = facet.SourceKindHTTPS
	default:
		if info, statErr := os.Stat(source); statErr == nil && info.IsDir() {
			kind = facet.SourceKindFolder
		}
	}

	grant, err := decideGrant(cmd, manifest, kept, opts.grants)
	if err != nil {
		return err
	}

	if err := facet.Install(dir, manifest.ID, work, manifest, previous, facet.InstallOptions{
		Replace: opts.replace,
		Source: facet.SourceInfo{
			Kind:        kind,
			Where:       source,
			PackageSHA:  packageSum,
			InstalledAt: time.Now().UTC(),
		},
	}); err != nil {
		return err
	}

	if err := writeGrant(dir, manifest.ID, grant); err != nil {
		return err
	}

	fmt.Fprintf(out, "installed %s %s from %s\n", manifest.ID, manifest.Version, source)
	return nil
}

func addDevPlugin(cmd *cobra.Command, dir, source string, opts pluginAddOptions) error {
	out := cmd.OutOrStdout()
	abs, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	manifest, err := facet.VerifyPackage(abs, facet.VerifyOptions{SkipSums: true})
	if err != nil {
		return err
	}

	previous := installedManifest(dir, manifest.ID, true)
	kept := keptGrant(dir, manifest, previous, opts.replace)

	grant, err := decideGrant(cmd, manifest, kept, opts.grants)
	if err != nil {
		return err
	}

	if err := facet.Install(dir, manifest.ID, "", manifest, previous, facet.InstallOptions{
		Replace:       opts.replace,
		DevLinkTarget: abs,
		Source: facet.SourceInfo{
			Kind:        facet.SourceKindFolder,
			Where:       abs,
			InstalledAt: time.Now().UTC(),
			Dev:         true,
		},
	}); err != nil {
		return err
	}

	if err := writeGrant(dir, manifest.ID, grant); err != nil {
		return err
	}

	fmt.Fprintf(out, "linked %s %s from %s (dev)\n", manifest.ID, manifest.Version, abs)
	return nil
}

// installedManifest is the manifest of what is already installed under id,
// or nil when nothing verifiable is.
func installedManifest(dir, id string, skipSums bool) *facet.PackageManifest {
	existing, err := facet.VerifyPackage(filepath.Join(dir, id), facet.VerifyOptions{
		SkipSums:     skipSums,
		ExtraAllowed: []string{".source.json", "grants.json"},
	})
	if err != nil {
		return nil
	}
	return &existing
}

// keptGrant reads the grant Install is about to preserve on --replace, so
// decideGrant can build on it instead of over it.
func keptGrant(dir string, manifest facet.PackageManifest, previous *facet.PackageManifest, replace bool) facet.Grant {
	if !replace || previous == nil || previous.AddedRights(manifest) {
		return facet.Grant{}
	}
	raw, err := os.ReadFile(filepath.Join(dir, manifest.ID, "grants.json"))
	if err != nil {
		return facet.Grant{}
	}
	var g facet.Grant
	if json.Unmarshal(raw, &g) != nil {
		return facet.Grant{}
	}
	return g
}

func decideGrant(cmd *cobra.Command, manifest facet.PackageManifest, kept facet.Grant, grantFlags []string) (facet.Grant, error) {
	explicit, err := parseGrantFlags(grantFlags, manifest)
	if err != nil {
		return facet.Grant{}, err
	}

	out := cmd.OutOrStdout()
	in := cmd.InOrStdin()
	interactive := isInteractive(in)

	fmt.Fprintf(out, "%s %s — from %s (local, unsigned)\n", manifest.Name, manifest.Version, manifest.ID)

	// Start from what a replace kept; each group below overwrites its own
	// field only when this run answers it.
	grant := kept
	env := manifestEnvNames(manifest)
	aiTools := manifestAITools(manifest)

	if explicit.tools != nil {
		grant.Tools = explicit.tools
	} else if interactive && len(manifest.Tools) > 0 {
		// The directories under "reads" are shown in the same question
		// because they are not a separate grant: repo.list/repo.read granted
		// here reach exactly these directories, so a reviewer who is not
		// shown them is asked to approve a scope they cannot see.
		tools := strings.Join(manifest.Tools, ", ")
		if len(manifest.Reads) > 0 {
			reads := make([]string, 0, len(manifest.Reads))
			for _, r := range manifest.Reads {
				reads = append(reads, r.String())
			}
			tools += " — reads " + strings.Join(reads, ", ")
		}
		if askYesNo(out, in, "  Tools        "+tools+"                        grant?") {
			grant.Tools = manifest.Tools
		} else {
			grant.Tools = nil
		}
	}

	if explicit.env != nil {
		grant.Env = explicit.env
	} else if interactive && len(env) > 0 {
		if askYesNo(out, in, "  Environment  "+strings.Join(env, ", ")+"                            grant?") {
			grant.Env = env
		} else {
			grant.Env = nil
		}
	}

	offeredAI := both(aiTools, grant.Tools)
	if explicit.ai != nil {
		grant.AITools = explicit.ai
	} else if interactive && len(offeredAI) > 0 {
		if askYesNo(out, in, "  For the agent: "+strings.Join(offeredAI, ", ")+"                         grant?") {
			grant.AITools = offeredAI
		} else {
			grant.AITools = nil
		}
	}

	offeredAIEnv := both(manifestAIEnvNames(manifest), grant.Env)
	if explicit.aiEnv != nil {
		grant.AIEnv = explicit.aiEnv
	} else if interactive && len(offeredAIEnv) > 0 && len(grant.AITools) > 0 {
		if askYesNo(out, in, "  For the agent: environment "+strings.Join(offeredAIEnv, ", ")+"           grant?") {
			grant.AIEnv = offeredAIEnv
		} else {
			grant.AIEnv = nil
		}
	}

	rightsDeclared := len(manifest.Tools) > 0 || len(env) > 0 || len(aiTools) > 0 || len(manifestAIEnvNames(manifest)) > 0
	grantedSomething := len(grant.Tools) > 0 || len(grant.Env) > 0 || len(grant.AITools) > 0 || len(grant.AIEnv) > 0
	if !interactive && rightsDeclared && !grantedSomething {
		fmt.Fprintf(out, "nothing granted (no terminal to ask on) — use --grant tools=…/env=…/ai=…/ai-env=… to grant explicitly\n")
	}

	return grant, nil
}

// writeGrant persists a non-empty grant next to the plugin's other
// host-written state. An empty grant leaves no grants.json — not even one a
// replace kept, because the grant here already carries the kept groups
// (decideGrant), so empty means the user withdrew the last of them. No
// grants.json at all is how Install and InstalledPlugins both already
// recognise "nothing was granted".
func writeGrant(dir, id string, grant facet.Grant) error {

	if len(grant.Tools) == 0 && len(grant.Env) == 0 && len(grant.AITools) == 0 && len(grant.AIEnv) == 0 {
		err := os.Remove(filepath.Join(dir, id, "grants.json"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	// Install may have preserved this exact host-written file. If no answer
	// changed its grant, keep its bytes (including future metadata and formatting).
	if raw, err := os.ReadFile(filepath.Join(dir, id, "grants.json")); err == nil {
		var existing facet.Grant
		if json.Unmarshal(raw, &existing) == nil && reflect.DeepEqual(existing, grant) {
			return nil
		}
	}

	raw, err := json.MarshalIndent(grant, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, id, "grants.json"), raw, 0o644)
}

type explicitGrants struct {
	tools, env, ai, aiEnv []string
}

// parseGrantFlags turns --grant tools=a,b --grant env=X --grant ai=a
// --grant ai-env=X into the four lists, refusing anything the manifest itself
// did not declare — a --grant naming an undeclared right would grant
// something nobody asked for and nobody could have reviewed against the
// manifest shown above it. For ai-env "declared" means the manifest's ai_env
// narrowed to env (manifestAIEnvNames): the schema holds each list's grammar,
// not the subset relation, so the install path holds it here.
func parseGrantFlags(flags []string, manifest facet.PackageManifest) (explicitGrants, error) {
	var out explicitGrants
	declaredTools := manifest.Tools
	declaredEnv := manifestEnvNames(manifest)
	declaredAI := manifestAITools(manifest)
	declaredAIEnv := manifestAIEnvNames(manifest)

	for _, f := range flags {
		key, value, ok := strings.Cut(f, "=")
		if !ok {
			return explicitGrants{}, fmt.Errorf("plugin: --grant %q is not key=value", f)
		}
		names := strings.Split(value, ",")
		switch key {
		case "tools":
			if err := requireDeclared(names, declaredTools, "tool"); err != nil {
				return explicitGrants{}, err
			}
			out.tools = append(out.tools, names...)
		case "env":
			if err := requireDeclared(names, declaredEnv, "environment variable"); err != nil {
				return explicitGrants{}, err
			}
			out.env = append(out.env, names...)
		case "ai":
			if err := requireDeclared(names, declaredAI, "ai_tool"); err != nil {
				return explicitGrants{}, err
			}
			out.ai = append(out.ai, names...)
		case "ai-env":
			if err := requireDeclared(names, declaredAIEnv, "ai_env variable"); err != nil {
				return explicitGrants{}, err
			}
			out.aiEnv = append(out.aiEnv, names...)
		default:
			return explicitGrants{}, fmt.Errorf("plugin: --grant %q names an unknown group (want tools, env, ai, or ai-env)", key)
		}
	}
	return out, nil
}

func requireDeclared(names, declared []string, what string) error {
	have := map[string]bool{}
	for _, d := range declared {
		have[d] = true
	}
	for _, n := range names {
		if !have[n] {
			return fmt.Errorf("plugin: the manifest does not declare the %s %q", what, n)
		}
	}
	return nil
}

func manifestEnvNames(m facet.PackageManifest) []string {
	if m.Sidecar == nil {
		return nil
	}
	return m.Sidecar.Env
}

// manifestAIEnvNames is the manifest's ai_env, narrowed to what env also
// names: a variable the manifest offers the agent without declaring it at all
// is not on offer (the same rule ai_tools has against tools).
func manifestAIEnvNames(m facet.PackageManifest) []string {
	if m.Sidecar == nil {
		return nil
	}
	out := make([]string, 0, len(m.Sidecar.AIEnv))
	for _, name := range m.Sidecar.AIEnv {
		for _, declared := range m.Sidecar.Env {
			if declared == name {
				out = append(out, name)
				break
			}
		}
	}
	return out
}

// both is the intersection in the first list's order — the same shape the
// broker uses, kept local because this package only needs it here.
func both(asked, granted []string) []string {
	out := make([]string, 0, len(asked))
	for _, a := range asked {
		for _, g := range granted {
			if a == g {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

func manifestAITools(m facet.PackageManifest) []string {
	out := make([]string, 0, len(m.AITools))
	for _, t := range m.AITools {
		out = append(out, t.Tool)
	}
	return out
}

// isInteractive reports whether in looks like a real terminal a human can
// answer a prompt on. Anything else — a pipe, a file, /dev/null in a test or
// a script — grants nothing unless --grant said so explicitly.
func isInteractive(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// askYesNo prints prompt with [y/N] and reads one line. Explicit y/yes or
// the German compatibility aliases j/ja grant. Every other answer, an empty
// line or an interrupted read denies the grant.
func askYesNo(out io.Writer, in io.Reader, prompt string) bool {
	fmt.Fprint(out, prompt+" [y/N] ")
	var line string
	buf := make([]byte, 1)
	for {
		n, err := in.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				break
			}
			line += string(buf[0])
		}
		if err != nil {
			// A disconnected terminal did not confirm with Enter, even if
			// the partial buffer already spells a valid affirmative answer.
			return false
		}
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	// Accept the German compatibility aliases j/ja alongside y/yes.
	return answer == "y" || answer == "yes" || answer == "j" || answer == "ja"
}

func newPluginListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List installed plugins",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := pluginsDirForCmd()
			if err != nil {
				return err
			}
			plugins, err := facet.InstalledPlugins(dir)
			if err != nil {
				return err
			}
			sort.Slice(plugins, func(i, j int) bool { return plugins[i].ID < plugins[j].ID })
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(plugins)
			}
			for _, p := range plugins {
				fmt.Fprintf(out, "%s\t%s\t%s\n", p.ID, p.Manifest.Version, p.State)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}

func newPluginRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <id>",
		Short: "Remove an installed plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := pluginsDirForCmd()
			if err != nil {
				return err
			}
			id := args[0]
			if id == "" || strings.ContainsAny(id, "/\\") {
				return fmt.Errorf("plugin: %q is not a plugin id", id)
			}
			target := filepath.Join(dir, id)
			if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("plugin: %s is not installed", id)
			}
			// The dev-mode sidecar files this command's Install wrote beside
			// a symlinked target, undone the same way they were made.
			os.Remove(filepath.Join(dir, id+".source.json"))
			os.Remove(filepath.Join(dir, id+".grants.json"))
			if err := os.RemoveAll(target); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", id)
			return nil
		},
	}
}

func newPluginUpdateCmd() *cobra.Command {
	var grants []string
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Re-install a plugin from the source it was installed from",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := pluginsDirForCmd()
			if err != nil {
				return err
			}
			id := args[0]
			sourcePath := filepath.Join(dir, id, ".source.json")
			raw, err := os.ReadFile(sourcePath)
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("plugin: %s is not installed", id)
			}
			if err != nil {
				return err
			}
			var src facet.SourceInfo
			if err := json.Unmarshal(raw, &src); err != nil {
				return err
			}
			if src.Dev {
				return fmt.Errorf("plugin: %s was installed with --dev; a symlinked plugin follows its working folder without update", id)
			}
			return runPluginAdd(cmd, src.Where, pluginAddOptions{replace: true, grants: grants})
		},
	}
	cmd.Flags().StringArrayVar(&grants, "grant", nil, grantFlagUsage)
	return cmd
}

func newPluginPackCmd() *cobra.Command {
	var outPath string
	cmd := &cobra.Command{
		Use:   "pack <dir>",
		Short: "Pack a plugin folder into a .facetplugin, writing SHA256SUMS from the actual files",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := mustAbs(args[0])
			out := outPath
			if out == "" {

				id, version, err := facet.PackageIDVersion(dir)
				if err != nil {
					return err
				}
				out = id + "-" + version + ".facetplugin"
			}
			if _, err := facet.Pack(dir, out); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", out)
			return nil
		},
	}
	cmd.Flags().StringVarP(&outPath, "out", "o", "", "output path (default <id>-<version>.facetplugin)")
	return cmd
}

func mustAbs(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}
