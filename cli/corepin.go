// @facet: delivery/distribution/core-pin
// @facet-role: primary
package cli

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/facet-framework/core/registry"
	"github.com/facet-framework/core/release"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

// pinExemptAnnotation opts a command out of the pre-run pin check. Nothing is
// exempt by its name alone, so a product or application command called 'init'
// is checked like any other.
const pinExemptAnnotation = "facet.pin-exempt"

const (
	// pinExemptSelf exempts the annotated command only.
	pinExemptSelf = "self"
	// pinExemptSubtree exempts the command and every command below it.
	pinExemptSubtree = "subtree"
	// pinExemptReports marks a command that runs under a mismatch but reports
	// it in its own responses instead of failing: hooks and the MCP server.
	pinExemptReports = "reports"
)

// markPinExempt sets the opt-in annotation on a command.
func markPinExempt(cmd *cobra.Command, scope string) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[pinExemptAnnotation] = scope
	return cmd
}

// cobraBuiltIns are the commands cobra itself adds while executing, after
// NewCommand has returned, so they cannot carry an annotation. Their names are
// reserved by cobra at the top level; a nested command of the same name is not
// one of them.
var cobraBuiltIns = map[string]bool{
	"help":                          true,
	"completion":                    true,
	cobra.ShellCompRequestCmd:       true,
	cobra.ShellCompNoDescRequestCmd: true,
}

// corePinExempt is the one place that decides which commands a pin mismatch
// does not stop: commands annotated with pinExemptAnnotation (version, the
// setup subtree, the git filters 'filter clean|smudge', hook, mcp) and cobra's
// own help and completion commands. --help and --version never reach a pre-run
// at all; cobra answers them first.
func corePinExempt(cmd *cobra.Command) bool {
	if cmd.Annotations[pinExemptAnnotation] != "" {
		return true
	}
	for parent := cmd.Parent(); parent != nil; parent = parent.Parent() {
		if parent.Annotations[pinExemptAnnotation] == pinExemptSubtree {
			return true
		}
	}
	top := topLevelCommand(cmd)
	return top == nil || cobraBuiltIns[top.Name()] && len(top.Annotations) == 0
}

// topLevelCommand is the root's direct child on the path to cmd, nil for the root.
func topLevelCommand(cmd *cobra.Command) *cobra.Command {
	if cmd.Parent() == nil {
		return nil
	}
	for cmd.Parent().Parent() != nil {
		cmd = cmd.Parent()
	}
	return cmd
}

// enforceCorePin fails a command before it does any work when the project
// pins a Core version other than the running one, or pins an invalid value.
func enforceCorePin(cmd *cobra.Command, running string) error {
	if corePinExempt(cmd) {
		return nil
	}
	return checkCorePin(ResolveRoot(cmd), running)
}

// invalidCorePin is a pin that no Core version can satisfy because it cannot
// be read as one exact version.
type invalidCorePin struct{ err error }

func (e invalidCorePin) Error() string { return e.err.Error() }
func (e invalidCorePin) Unwrap() error { return e.err }

// checkCorePin returns nil when root's project declares no pin or the running
// version satisfies it; an invalidCorePin when the declaration is malformed.
func checkCorePin(root, running string) error {
	pin, err := facet.ReadCorePin(root)
	if err != nil {
		return invalidCorePin{err}
	}
	if pin == nil || pin.Satisfied(running) {
		return nil
	}
	return corePinMismatch(*pin, running, release.CurrentInstall())
}

// corePinMismatch names the file, both versions, the install origin and the
// remedy that fits how this binary was installed.
func corePinMismatch(pin facet.CorePin, running string, install release.Install) error {
	var remedy string
	switch install.Origin {
	case release.OriginNPM:
		remedy = fmt.Sprintf("run it as 'npx facet-framework@%s …', or install facet-framework@%s in this project", pin.Core, pin.Core)
	case release.OriginFacetCache:
		remedy = fmt.Sprintf("set FACET_VERSION=%s so the facet dispatcher selects that Core", pin.Core)
	default:
		remedy = fmt.Sprintf("install FACET Core %s, or point FACET_BINARY at a Core %s build", pin.Core, pin.Core)
	}
	return fmt.Errorf("%s requires FACET Core %s, but this is Core %s (install origin: %s, %s); %s",
		pin.File, pin.Core, running, install.Origin, install.Path, remedy)
}

// corePinReport turns a pin check failure into the sentence a hook or an MCP
// tool result carries. An invalid pin is said to be invalid: "does not match"
// would send the reader looking for another binary that does not exist.
func corePinReport(err error) string {
	var invalid invalidCorePin
	if errors.As(err, &invalid) {
		return "FACET: this project's Core pin is invalid, so no Core version satisfies it. " + err.Error() + "."
	}
	return "FACET: the running Core does not match this project's pin. " + err.Error() + "."
}

func hookCorePinNotice(cmd *cobra.Command, root string) string {
	if err := checkCorePin(root, cmd.Root().Version); err != nil {
		return corePinReport(err)
	}
	return ""
}

// hookCorePinNoticeOnce applies the notice frequency per event. SessionStart
// always carries it, since a new session has seen nothing yet. Tool-use events
// carry it once per session; the marker is keyed by the notice text, which
// names file, required and running version, so a changed mismatch is new.
func hookCorePinNoticeOnce(cmd *cobra.Command, root, event, session string) string {
	notice := hookCorePinNotice(cmd, root)
	if notice == "" || event == "session-start" {
		return notice
	}
	sum := sha256.Sum256([]byte(notice))
	if !noticeIsNew(session, fmt.Sprintf("core-pin:%x", sum[:12])) {
		return ""
	}
	return notice
}

// corePinMCPMiddleware is the MCP server's single choke point for tool calls.
// Under an invalid or unmet pin every tools/call is answered with that error
// as the tool's error result and no tool runs. The pin is read again on each
// call, so correcting .facetrc.yaml takes effect without restarting the server.
func corePinMCPMiddleware(root, running string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/call" {
				if err := checkCorePin(root, running); err != nil {
					return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: corePinReport(err)}}}, nil
				}
			}
			return next(ctx, method, req)
		}
	}
}

// projectPinReport is the pin as 'facet version --json' reports it. A broken
// declaration is reported with its error, so "no pin" (absent) and "broken
// pin" differ.
type projectPinReport struct {
	File      string `json:"file"`
	Core      string `json:"core,omitempty"`
	Satisfied *bool  `json:"satisfied,omitempty"`
	Error     string `json:"error,omitempty"`
}

func projectPinFor(root, running string) *projectPinReport {
	pin, err := facet.ReadCorePin(root)
	if err != nil {
		return &projectPinReport{File: facet.ConfigPath(root), Error: err.Error()}
	}
	if pin == nil {
		return nil
	}
	satisfied := pin.Satisfied(running)
	return &projectPinReport{File: pin.File, Core: pin.Core, Satisfied: &satisfied}
}
