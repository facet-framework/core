// @facet: delivery/distribution/product-runtime
// @facet-role: primary
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/facet-framework/core/pkg/productplugin"
	"github.com/facet-framework/core/registry"
	"github.com/spf13/cobra"
)

func productStore() productplugin.Store { return productplugin.DefaultStore() }
func newProductPluginCmd() *cobra.Command {
	group := &cobra.Command{Use: "product", Short: "Install and manage native product executables"}
	var sha string
	var grant, replace bool
	add := &cobra.Command{Use: "add <source>", Short: "Verify and install a product package", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !grant {
			return fmt.Errorf("native products execute with your OS user permissions; installation requires --grant-native")
		}
		work, err := os.MkdirTemp("", "facet-product-stage-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(work)
		if _, err = facet.LoadPackage(args[0], work, facet.LoadOptions{SHA256: sha, AssetSuffix: "-" + runtime.GOOS + "-" + runtime.GOARCH + ".facetplugin", GitHub: productGitHubSource(args[0])}); err != nil {
			return err
		}
		manifest, _, err := productplugin.Verify(work)
		if err != nil {
			return err
		}
		for _, name := range manifest.Commands {
			for _, existing := range cmd.Root().Commands() {
				matches := existing.Name() == name
				for _, alias := range existing.Aliases {
					matches = matches || alias == name
				}
				if matches && existing.Annotations["facet.product.id"] != manifest.ID {
					return fmt.Errorf("product command %s conflicts with an existing command", name)
				}
			}
		}
		installed, err := productStore().Install(cmd.Context(), work, grant, replace)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Installed product %s@%s (protocol %d)\n", installed.Manifest.ID, installed.Manifest.Version, installed.Manifest.Protocol)
		return err
	}}
	add.Flags().StringVar(&sha, "sha256", "", "expected package SHA256 (required for HTTPS)")
	add.Flags().BoolVar(&grant, "grant-native", false, "allow native code with your OS user permissions (not a sandbox)")
	add.Flags().BoolVar(&replace, "replace", false, "replace the selected version; requires a fresh native grant")
	list := &cobra.Command{Use: "list", Short: "List verified installed products and grants", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		installed, err := productStore().List()
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(installed)
	}}
	remove := &cobra.Command{Use: "remove <id>", Short: "Revoke an installed product's command registration", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error { return productStore().Remove(cmd.Context(), args[0]) }}
	group.AddCommand(add, list, remove)
	return group
}

// registerProductCommands does not invoke package executables during discovery.
// A broken product must not make Core commands unavailable. Diagnostics remain
// visible through `facet plugin product list` and on attempted product use.
func registerProductCommands(root *cobra.Command, store productplugin.Store) {
	installed, err := store.List()
	if err != nil {
		root.SetHelpTemplate(root.HelpTemplate() + "\nProduct discovery failed; run 'facet plugin product list' for details.\n")
		return
	}
	claimed := map[string]bool{}
	for _, cmd := range root.Commands() {
		claimed[cmd.Name()] = true
		for _, alias := range cmd.Aliases {
			claimed[alias] = true
		}
	}
	for _, in := range installed {
		id := in.Manifest.ID
		for _, name := range in.Manifest.Commands {
			if claimed[name] {
				continue
			} // Core and earlier declarations cannot be shadowed.
			claimed[name] = true
			root.AddCommand(&cobra.Command{Use: name, Short: "Provided by product " + id + "@" + in.Manifest.Version, DisableFlagParsing: true, Annotations: map[string]string{"facet.product.id": id},
				PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
					if _, err := selectProductRoot(cmd, args); err != nil {
						return err
					}
					if root.PersistentPreRunE != nil {
						return root.PersistentPreRunE(cmd, args)
					}
					return nil
				}, RunE: func(cmd *cobra.Command, args []string) error {
					forwarded, err := selectProductRoot(cmd, args)
					if err != nil {
						return err
					}
					host, err := os.Executable()
					if err != nil {
						return err
					}
					selection := sourceViewFlag(cmd)
					if cfg, e := configForCommand(cmd); e == nil {
						if view := facet.RegistrySet(cfg.All()).Primary().SourceView; view != nil {
							selection = view.Name
						}
					}
					ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
					defer cancel()
					hostCommands := []string{}
					for _, offered := range root.Commands() {
						if offered.Annotations["facet.product.missing"] != "true" {
							hostCommands = append(hostCommands, offered.Name())
						}
					}
					return store.Run(ctx, id, cmd.Name(), forwarded, productplugin.Invocation{Root: ResolveRoot(cmd), SourceView: selection, HostExecutable: host, HostCommands: hostCommands}, productplugin.Streams{In: cmd.InOrStdin(), Out: cmd.OutOrStdout(), Err: cmd.ErrOrStderr()})
				}})
		}
	}
}

// Preserve all product arguments except the host's explicit selection flags.
// An argument after -- belongs to the product, even if it resembles a host flag.
func selectProductRoot(cmd *cobra.Command, args []string) ([]string, error) {
	out := []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return append(out, args[i:]...), nil
		}
		key, value, hasValue := strings.Cut(arg, "=")
		if key != "--root" && key != "--source-view" {
			out = append(out, arg)
			continue
		}
		if !hasValue {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("%s needs a value", key)
			}
			i++
			value = args[i]
		}
		if err := cmd.Flags().Set(strings.TrimPrefix(key, "--"), value); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func productGitHubSource(source string) *facet.GitHubSource {
	if !strings.HasPrefix(source, "github:") {
		return nil
	}
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		if raw, err := exec.Command("gh", "auth", "token").Output(); err == nil {
			token = strings.TrimSpace(string(raw))
		}
	}
	return &facet.GitHubSource{Token: token}
}

func registerMissingProductCommands(root *cobra.Command) {
	for _, binding := range productBindings() {
		if binding.Command == "" {
			continue
		}
		found := false
		for _, existing := range root.Commands() {
			if existing.Name() == binding.Command {
				found = true
				break
			}
		}
		if found {
			continue
		}
		message := fmt.Sprintf("Command %s requires the %s product. Install an authorized package with 'facet plugin product add <source> --grant-native'.", binding.Command, binding.ID)
		root.AddCommand(&cobra.Command{Use: binding.Command, Short: "Optional " + binding.ID + " product (not installed)", Long: message, DisableFlagParsing: true,
			Annotations:       map[string]string{"facet.product.id": binding.ID, "facet.product.missing": "true"},
			PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
			RunE: func(cmd *cobra.Command, args []string) error {
				if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
					return cmd.Help()
				}
				return fmt.Errorf("%s", message)
			},
		})
	}
}
