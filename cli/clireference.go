package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// cliReferenceSchemaVersion is bumped whenever a field of cliReferenceDocument,
// cliCommand or cliFlag is renamed, removed or changes meaning. Adding a field
// does not bump it: a consumer that ignores unknown keys keeps working.
const cliReferenceSchemaVersion = 1

// cliReferenceDocument is the machine-readable description of the command tree, the
// source the website's CLI reference is generated from. It replaces scraping
// `facet <cmd> --help`, which broke on every change to cobra's help layout and
// only caught a new flag when somebody remembered to re-run the scraper.
type cliReferenceDocument struct {
	SchemaVersion int    `json:"schema_version"`
	FacetVersion  string `json:"facet_version"`
	// PersistentFlags are the root's persistent flags, listed once here
	// instead of under every command that inherits them.
	PersistentFlags []cliFlag  `json:"persistent_flags"`
	Root            cliCommand `json:"root"`
}

// cliCommand is one visible command. Slices are never nil, so every key is
// present in the JSON with the same type for every command.
type cliCommand struct {
	Path       string   `json:"path"`
	Use        string   `json:"use"`
	Short      string   `json:"short"`
	Long       string   `json:"long"`
	Aliases    []string `json:"aliases"`
	Example    string   `json:"example"`
	Deprecated string   `json:"deprecated"`
	// Runnable is cobra's Runnable(): the command does something itself
	// rather than only grouping subcommands. cobra's usage lines depend on it
	// ("facet drift [flags]" beside "facet drift [command]", but only
	// "facet adr [command]"), so a consumer needs it to reproduce them.
	Runnable bool         `json:"runnable"`
	Flags    []cliFlag    `json:"flags"`
	Commands []cliCommand `json:"commands"`
}

// cliFlag is one flag a command declares itself, not one it inherits.
type cliFlag struct {
	Name      string `json:"name"`
	Shorthand string `json:"shorthand"`
	// Type is pflag's Value.Type(): "bool", "string", "stringSlice", "int", …
	Type string `json:"type"`
	// ValueName is what --help prints after the flag ("strings", "float", a
	// back-quoted name from the usage), empty for a switch.
	ValueName string `json:"value_name"`
	Default   string `json:"default"`
	// Usage is the help text as --help prints it, back quotes removed.
	Usage    string `json:"usage"`
	Required bool   `json:"required"`
}

// cliReference walks root and describes every visible command and flag.
//
// @facet: delivery/distribution/cli-reference
// @facet-role: primary
func cliReference(root *cobra.Command) cliReferenceDocument {
	persistent := collectFlags(root.PersistentFlags())
	skip := map[string]bool{}
	for _, f := range persistent {
		skip[f.Name] = true
	}
	return cliReferenceDocument{
		SchemaVersion:   cliReferenceSchemaVersion,
		FacetVersion:    root.Version,
		PersistentFlags: persistent,
		Root:            describeCommand(root, skip),
	}
}

// describeCommand describes cmd and its visible subcommands. skip names flags
// already reported elsewhere (the root's persistent flags), and applies only
// to cmd itself: a subcommand never re-declares a flag it inherits.
func describeCommand(cmd *cobra.Command, skip map[string]bool) cliCommand {
	flags := []cliFlag{}
	for _, f := range collectFlags(cmd.NonInheritedFlags()) {
		if !skip[f.Name] {
			flags = append(flags, f)
		}
	}
	out := cliCommand{
		Path:       cmd.CommandPath(),
		Use:        cmd.Use,
		Short:      cmd.Short,
		Long:       cmd.Long,
		Aliases:    append([]string{}, cmd.Aliases...),
		Example:    cmd.Example,
		Deprecated: cmd.Deprecated,
		Runnable:   cmd.Runnable(),
		Flags:      flags,
		Commands:   []cliCommand{},
	}
	for _, sub := range cmd.Commands() {
		if !documentedCommand(sub) {
			continue
		}
		out.Commands = append(out.Commands, describeCommand(sub, nil))
	}
	return out
}

// documentedCommand reports whether a command belongs in the reference.
func documentedCommand(c *cobra.Command) bool {
	if c.Hidden {
		return false
	}
	switch c.Name() {
	case "help", "completion":
		return false
	}
	return true
}

// collectFlags lists the visible flags of a set, minus cobra's automatic ones.
func collectFlags(set *pflag.FlagSet) []cliFlag {
	flags := []cliFlag{}
	set.VisitAll(func(f *pflag.Flag) {
		if f.Hidden || isCobraDefaultFlag(f) {
			return
		}
		// The same call --help makes, so the reference reads like the terminal.
		valueName, usage := pflag.UnquoteUsage(f)
		flags = append(flags, cliFlag{
			Name:      f.Name,
			Shorthand: f.Shorthand,
			Type:      f.Value.Type(),
			ValueName: valueName,
			Default:   f.DefValue,
			Usage:     usage,
			Required:  isRequiredFlag(f),
		})
	})
	return flags
}

// isCobraDefaultFlag recognises the --help and --version flags cobra adds on
// its own, by the usage text cobra gives them, so that a command declaring a
// flag of the same name with its own text is still documented.
func isCobraDefaultFlag(f *pflag.Flag) bool {
	return (f.Name == "help" && strings.HasPrefix(f.Usage, "help for ")) ||
		(f.Name == "version" && strings.HasPrefix(f.Usage, "version for "))
}

// isRequiredFlag reads the annotation cobra's MarkFlagRequired writes.
func isRequiredFlag(f *pflag.Flag) bool {
	v, ok := f.Annotations[cobra.BashCompOneRequiredFlag]
	return ok && len(v) > 0 && v[0] == "true"
}

func newDocsCLICmd() *cobra.Command {
	return &cobra.Command{
		Use:    "cli",
		Short:  "Print the command tree as JSON, the source of the CLI reference",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			if err := enc.Encode(cliReference(cmd.Root())); err != nil {
				return fmt.Errorf("encode CLI reference: %w", err)
			}
			return nil
		},
	}
}
