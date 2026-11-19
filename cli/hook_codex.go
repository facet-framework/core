// @facet: authoring/session/hook-dispatch
// @facet-role: primary
// @facet: authoring/session/read-context
// @facet-role: related
// @facet: authoring/guardrails/write-validation
// @facet-role: related
package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// Each hook invocation owns its response sink. A copied context can collect or
// transform responses without intercepting another invocation's output.
func (ctx hookContext) outputWriter() io.Writer {
	if ctx.output != nil {
		return ctx.output
	}
	return os.Stdout
}

func (ctx hookContext) writeOutput(out hookOutput) {
	if ctx.send != nil {
		ctx.send(out)
		return
	}
	_ = json.NewEncoder(ctx.outputWriter()).Encode(out)
}

func normalizeHookInput(in hookInput) hookInput {
	if in.ToolName == "exec_command" {
		in.ToolName = "Bash"
	}
	if in.ToolName == "Bash" && in.ToolInput.Command == "" {
		in.ToolInput.Command = in.ToolInput.Cmd
	}
	return in
}

// Parse only the patch envelope's file headers, never shell syntax or hunk
// content. Deleted paths still identify their repository after the edit.
func patchHookPaths(patch string) []string {
	lines := strings.Split(strings.TrimSpace(patch), "\n")
	if len(lines) < 2 || strings.TrimSuffix(lines[0], "\r") != "*** Begin Patch" || strings.TrimSuffix(lines[len(lines)-1], "\r") != "*** End Patch" {
		return nil
	}
	var paths []string
	seen := map[string]bool{}
	for _, line := range lines[1 : len(lines)-1] {
		for _, prefix := range []string{"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "} {
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			path := strings.TrimSuffix(strings.TrimPrefix(line, prefix), "\r")
			if path == "" || strings.ContainsRune(path, 0) {
				continue
			}
			path = filepath.Clean(path)
			if !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
			break
		}
	}
	return paths
}

func dispatchPatchHook(cmd *cobra.Command, event string, in hookInput) {
	if event != "post-tool-use" && event != "pre-tool-use" {
		return
	}
	var outputs []hookOutput
	var notices []string
	seen, seenNotice := map[string]bool{}, map[string]bool{}
	for _, path := range patchHookPaths(in.ToolInput.Command) {
		base := in.CWD
		if base == "" {
			base = ResolveRoot(cmd)
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(base, path)
		}
		path = filepath.Clean(path)
		if seen[path] {
			continue
		}
		seen[path] = true
		fileIn := in
		fileIn.ToolName = "Edit"
		fileIn.ToolInput.FilePath = path
		fileIn.ToolInput.Command = ""
		fileIn.ToolInput.Cmd = ""
		ctx, ok := resolveHookContext(cmd, fileIn)
		if !ok {
			continue
		}
		ctx.send = func(out hookOutput) { outputs = append(outputs, out) }
		if notice := hookCorePinNoticeOnce(cmd, ctx.root, event, in.SessionID); notice != "" && !seenNotice[notice] {
			seenNotice[notice] = true
			notices = append(notices, notice)
		}
		// Never route an outside path into cwd's unrelated registry.
		rel, err := filepath.Rel(ctx.root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if event == "pre-tool-use" {
			hookReadContext(ctx, fileIn)
		} else {
			rememberRoot(in.SessionID, ctx.root)
			hookPostToolUse(ctx, fileIn)
		}
	}
	if len(outputs) == 0 && len(notices) == 0 {
		return
	}
	event = hookContextEvent(event)
	hookContext{output: cmd.OutOrStdout()}.writeOutput(mergeHookOutputs(event, notices, outputs))
}

// hookContextEvent names the response envelope of the events whose answer can
// carry additional context, empty for every other event. Stop, SessionEnd and
// PreCompact have no such field, so a notice is never attached to them.
func hookContextEvent(event string) string {
	switch event {
	case "session-start":
		return "SessionStart"
	case "pre-tool-use":
		return "PreToolUse"
	case "post-tool-use":
		return "PostToolUse"
	}
	return ""
}

// mergeHookOutputs folds several handler responses into the one response a
// hook invocation may print. Notices lead the context; a deny from any handler
// remains a deny with every reason kept.
func mergeHookOutputs(event string, notices []string, outputs []hookOutput) hookOutput {
	var merged hookOutput
	merged.HookSpecificOutput.HookEventName = event
	contexts := append([]string(nil), notices...)
	var reasons []string
	for _, out := range outputs {
		h := out.HookSpecificOutput
		if h.AdditionalContext != "" {
			contexts = append(contexts, h.AdditionalContext)
		}
		if h.PermissionDecision == "deny" {
			merged.HookSpecificOutput.PermissionDecision = "deny"
			reasons = append(reasons, h.PermissionDecisionReason)
		}
	}
	merged.HookSpecificOutput.AdditionalContext = strings.Join(contexts, "\n\n")
	merged.HookSpecificOutput.PermissionDecisionReason = strings.Join(reasons, "\n\n")
	return merged
}

func isDirectoryOnDisk(path string) bool { st, err := os.Stat(path); return err == nil && st.IsDir() }

// Codex reconstructs persistent instructions after compaction through
// SessionStart(source=compact); PreCompact context alone is not delivered.
func hookCompactSessionStart(ctx hookContext, session string) {
	parent := ctx
	ctx.send = func(out hookOutput) {
		out.HookSpecificOutput.HookEventName = "SessionStart"
		parent.writeOutput(out)
	}
	hookPreCompact(ctx, session)
}
