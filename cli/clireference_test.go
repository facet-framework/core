package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// findCommand returns the command at path in a reference, or nil.
func findCommand(c *cliCommand, path string) *cliCommand {
	if c.Path == path {
		return c
	}
	for i := range c.Commands {
		if found := findCommand(&c.Commands[i], path); found != nil {
			return found
		}
	}
	return nil
}

// findFlag returns the flag named name among flags, or nil.
func findFlag(flags []cliFlag, name string) *cliFlag {
	for i := range flags {
		if flags[i].Name == name {
			return &flags[i]
		}
	}
	return nil
}

// allPaths lists every command path in a reference, depth first.
func allPaths(c cliCommand) []string {
	paths := []string{c.Path}
	for _, sub := range c.Commands {
		paths = append(paths, allPaths(sub)...)
	}
	return paths
}

// The guard that keeps the generated reference worth reading: a command or a
// flag without help text would reach the website as an empty row. It walks
// the cobra tree itself rather than cliReference's output, so a bug that
// drops entries from the reference cannot also hide them from this check.
func TestEveryVisibleCommandAndFlagHasHelpText(t *testing.T) {
	root := newRootCmd()
	var commands, flags int
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		commands++
		if strings.TrimSpace(c.Short) == "" {
			t.Errorf("%q has no Short; the CLI reference would show an empty summary", c.CommandPath())
		}
		for _, f := range collectFlags(c.NonInheritedFlags()) {
			flags++
			if strings.TrimSpace(f.Usage) == "" {
				t.Errorf("%q has no usage text; the CLI reference would show an empty row", c.CommandPath()+" --"+f.Name)
			}
		}
		for _, sub := range c.Commands() {
			if documentedCommand(sub) {
				walk(sub)
			}
		}
	}
	walk(root)

	// The fixture's own precondition: a walk that visited nothing would pass.
	if commands < 20 || flags < 50 {
		t.Fatalf("walked only %d commands and %d flags; the guard did not see the real tree", commands, flags)
	}
}

// The reference describes the real tree: a known command carries its real
// flag with cobra's type and default, hidden and cobra-owned commands are
// absent, and --root is listed once at the top instead of under every command.
func TestTheReferenceDescribesTheRealCommandTree(t *testing.T) {
	cmd := newRootCmd()
	ref := cliReference(cmd)

	if ref.SchemaVersion != cliReferenceSchemaVersion || ref.FacetVersion != cmd.Version {
		t.Errorf("header = (%d, %q), want (%d, %q)", ref.SchemaVersion, ref.FacetVersion, cliReferenceSchemaVersion, cmd.Version)
	}

	check := findCommand(&ref.Root, "facet docs check")
	if check == nil {
		t.Fatalf("facet docs check is missing; paths: %v", allPaths(ref.Root))
	}
	if check.Short != "Report generated artifacts whose inputs have changed" {
		t.Errorf("docs check Short = %q", check.Short)
	}
	jsonFlag := findFlag(check.Flags, "json")
	if jsonFlag == nil {
		t.Fatalf("docs check --json is missing; flags: %+v", check.Flags)
	}
	if *jsonFlag != (cliFlag{Name: "json", Type: "bool", Default: "false", Usage: "emit statuses as JSON"}) {
		t.Errorf("docs check --json = %+v", *jsonFlag)
	}
	if strict := findFlag(check.Flags, "strict"); strict == nil || strict.Default != "false" || strict.Type != "bool" {
		t.Errorf("docs check --strict = %+v, want a bool defaulting to false", strict)
	}

	if drift := findCommand(&ref.Root, "facet drift"); drift == nil || len(drift.Commands) == 0 || !drift.Runnable {
		t.Errorf("facet drift = %+v, want runnable with subcommands", drift)
	}
	if adr := findCommand(&ref.Root, "facet adr"); adr == nil || len(adr.Commands) == 0 || adr.Runnable {
		t.Errorf("facet adr = %+v, want a non-runnable group with subcommands", adr)
	}

	for _, absent := range []string{"facet hook", "facet docs cli", "facet help", "facet completion"} {
		if findCommand(&ref.Root, absent) != nil {
			t.Errorf("%q is in the reference; hidden and cobra-owned commands stay out", absent)
		}
	}

	if f := findFlag(ref.PersistentFlags, "root"); f == nil || f.Type != "string" || f.Default != "" {
		t.Errorf("persistent --root = %+v, want a string flag with an empty default", f)
	}
	if findFlag(ref.Root.Flags, "root") != nil {
		t.Error("--root is listed under the root command too; persistent flags appear once")
	}
	if findFlag(check.Flags, "root") != nil || findFlag(check.Flags, "help") != nil {
		t.Errorf("docs check lists an inherited or cobra flag: %+v", check.Flags)
	}
}

// What makes the reference generated rather than hand-listed: a flag added to
// a command appears, with what was declared about it, and a hidden one does
// not. The tree here is built in the test, so nothing in the code under test
// can know its names.
func TestAFlagAddedToACommandAppearsInTheReference(t *testing.T) {
	root := &cobra.Command{Use: "tool", Short: "a tool", Version: "1.2.3"}
	sub := &cobra.Command{
		Use:        "deploy <env>",
		Short:      "deploy somewhere",
		Long:       "Deploys to an environment.",
		Aliases:    []string{"ship"},
		Example:    "tool deploy prod",
		Deprecated: "use release",
		RunE:       func(*cobra.Command, []string) error { return nil },
	}
	root.AddCommand(sub)

	initial := cliReference(root)
	before := findCommand(&initial.Root, "tool deploy")
	if before == nil || len(before.Flags) != 0 {
		t.Fatalf("precondition: deploy should start with no flags, got %+v", before)
	}

	sub.Flags().IntP("replicas", "r", 3, "number of replicas")
	sub.Flags().String("target", "", "where to deploy")
	sub.Flags().StringSlice("skip", nil, "checks to skip")
	sub.Flags().String("config", "", "read settings from `file`")
	if err := sub.MarkFlagRequired("target"); err != nil {
		t.Fatal(err)
	}
	sub.Flags().Bool("secret", false, "not for users")
	if err := sub.Flags().MarkHidden("secret"); err != nil {
		t.Fatal(err)
	}

	// cobra's own flags, added the way Execute adds them, stay out; a command
	// that declares a flag of the same name with its own text keeps it.
	info := &cobra.Command{Use: "info", Short: "show info", RunE: func(*cobra.Command, []string) error { return nil }}
	info.Flags().String("version", "", "which release to describe")
	root.AddCommand(info)
	root.InitDefaultHelpFlag()
	root.InitDefaultVersionFlag()
	sub.InitDefaultHelpFlag()

	ref := cliReference(root)
	if len(ref.Root.Flags) != 0 {
		t.Errorf("root flags = %+v, want cobra's --help and --version left out", ref.Root.Flags)
	}
	if gotInfo := findCommand(&ref.Root, "tool info"); gotInfo == nil || findFlag(gotInfo.Flags, "version") == nil {
		t.Errorf("tool info lost its own --version flag: %+v", gotInfo)
	}
	got := findCommand(&ref.Root, "tool deploy")
	if got == nil {
		t.Fatal("tool deploy is missing")
	}
	if got.Use != "deploy <env>" || got.Long != "Deploys to an environment." || got.Example != "tool deploy prod" ||
		got.Deprecated != "use release" || len(got.Aliases) != 1 || got.Aliases[0] != "ship" {
		t.Errorf("command fields = %+v", *got)
	}
	want := []cliFlag{
		// The value names are what --help prints: pflag's UnquoteUsage.
		{Name: "config", Type: "string", ValueName: "file", Usage: "read settings from file"},
		{Name: "replicas", Shorthand: "r", Type: "int", ValueName: "int", Default: "3", Usage: "number of replicas"},
		{Name: "skip", Type: "stringSlice", ValueName: "strings", Default: "[]", Usage: "checks to skip"},
		{Name: "target", Type: "string", ValueName: "string", Usage: "where to deploy", Required: true},
	}
	if len(got.Flags) != len(want) {
		t.Fatalf("flags = %+v, want %+v", got.Flags, want)
	}
	for i := range want {
		if got.Flags[i] != want[i] {
			t.Errorf("flag %d = %+v, want %+v", i, got.Flags[i], want[i])
		}
	}
	if ref.FacetVersion != "1.2.3" {
		t.Errorf("version = %q, want the root's", ref.FacetVersion)
	}
}

// `facet docs cli` prints exactly what cliReference returns for a fresh tree,
// although running it makes cobra add its help and completion commands to the
// tree it walks. Run in-process through Execute; no process is started.
func TestDocsCLIPrintsTheReferenceAsJSON(t *testing.T) {
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"docs", "cli"})
	if err := root.Execute(); err != nil {
		t.Fatalf("facet docs cli: %v\n%s", err, out.String())
	}

	want, err := json.MarshalIndent(cliReference(newRootCmd()), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != string(want) {
		t.Errorf("facet docs cli output differs from cliReference of a fresh tree\n got: %.400s\nwant: %.400s", out.String(), want)
	}

	var decoded cliReferenceDocument
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if findCommand(&decoded.Root, "facet docs check") == nil {
		t.Error("decoded output lacks facet docs check")
	}
}
