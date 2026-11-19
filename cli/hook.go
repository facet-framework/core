// @facet: authoring/session/hook-dispatch
// @facet-role: primary

package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/facet-framework/core/internal/sourceview"
	"github.com/facet-framework/core/registry"
	"github.com/spf13/cobra"
)

// hookInput is the subset of the Claude Code hook payload the handlers use.
type hookInput struct {
	StopHookActive bool   `json:"stop_hook_active"`
	SessionID      string `json:"session_id"`
	CWD            string `json:"cwd"`
	Event          string `json:"hook_event_name"`
	Source         string `json:"source"`
	ToolName       string `json:"tool_name"`
	ToolInput      struct {
		FilePath string `json:"file_path"`
		Command  string `json:"command"`
		Cmd      string `json:"cmd"`
		Offset   *int   `json:"offset"`
		Limit    *int   `json:"limit"`
	} `json:"tool_input"`
}

// hookOutput is the response envelope Claude Code understands.
type hookOutput struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		AdditionalContext        string `json:"additionalContext,omitempty"`
		PermissionDecision       string `json:"permissionDecision,omitempty"`
		PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	} `json:"hookSpecificOutput"`
}

func (ctx hookContext) emit(event, context string) {
	var out hookOutput
	out.HookSpecificOutput.HookEventName = event
	out.HookSpecificOutput.AdditionalContext = context
	ctx.writeOutput(out)
}

func (ctx hookContext) deny(event, reason string) {
	var out hookOutput
	out.HookSpecificOutput.HookEventName = event
	out.HookSpecificOutput.PermissionDecision = "deny"
	out.HookSpecificOutput.PermissionDecisionReason = reason
	ctx.writeOutput(out)
}

func newHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "hook <event>",
		Short:  "Handle a Claude Code hook event (reads the payload on stdin)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return err
			}
			var in hookInput
			// A payload we cannot parse must never break the session.
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil
			}

			in = normalizeHookInput(in)
			if in.ToolName == "apply_patch" {
				dispatchPatchHook(cmd, args[0], in)
				return nil
			}

			ctx, ok := resolveHookContext(cmd, in)
			if !ok {
				// Inert outside FACET projects, with one exception: a git
				// repository that has not met FACET is told once that
				// registry setup is available.
				if args[0] == "session-start" {
					hookSetupHint(hookContext{output: cmd.OutOrStdout()}, in)
				}
				return nil
			}

			ctx.output = cmd.OutOrStdout()
			// A project pin this binary does not meet is reported in the
			// response, at most once per invocation, while the guards still
			// run: failing the hook would switch them off without the agent
			// seeing why. Tool-use events repeat it only once per session.
			if event := hookContextEvent(args[0]); event != "" {
				if notice := hookCorePinNoticeOnce(cmd, ctx.root, args[0], in.SessionID); notice != "" {
					var outputs []hookOutput
					ctx.send = func(out hookOutput) { outputs = append(outputs, out) }
					defer func() {
						hookContext{output: cmd.OutOrStdout()}.writeOutput(mergeHookOutputs(event, []string{notice}, outputs))
					}()
				}
			}
			switch args[0] {
			case "session-start":
				clearNotices(in.SessionID, "read-context:")
				if in.Source == "compact" {
					hookCompactSessionStart(ctx, in.SessionID)
				} else {
					recordProductSession(ctx, in.SessionID, "session_start")
					hookSessionStart(ctx)
				}
			case "session-end":
				// SessionEnd closes the session; Stop ends only the current turn.
				recordProductSession(ctx, in.SessionID, "session_end")
			case "post-tool-use":
				if in.ToolName == "Read" {
					hookReadContext(ctx, in)
					break
				}
				// Noted before the handler runs, so the Stop hook can ask this
				// repository about drift even when the session's cwd is elsewhere.
				rememberRoot(in.SessionID, ctx.root)
				hookPostToolUse(ctx, in)
			case "pre-tool-use":
				hookPreToolUse(ctx, in)
			case "stop":
				hookStop(ctx, in)
			case "pre-compact":
				clearNotices(in.SessionID, "read-context:")
				hookPreCompact(ctx, in.SessionID)
			}
			return nil
		},
	}
	// A hook never fails over the pin; it reports it in its response.
	return markPinExempt(cmd, pinExemptReports)
}

// resolveHookContext finds the repository the work is actually happening in.
func resolveHookContext(cmd *cobra.Command, in hookInput) (hookContext, bool) {
	for _, root := range hookRootCandidates(cmd, in) {
		if ctx := newHookContextForCommand(cmd, root); len(ctx.registries) > 0 {
			return ctx, true
		}
		if looksLikeCommit(in.ToolInput.Command) && commitRevisionAdopted(root) {
			return hookContext{root: root, selectedSourceView: sourceViewFlag(cmd), registries: hookRegistriesForCommand(cmd, root)}, true
		}
	}
	return hookContext{}, false
}

// The host can choose a view independently of its environment. Candidate roots
// still follow worktree detection, while each candidate gets that same explicit
// selection; the invocation registry reuses its already frozen config.
func hookRegistriesForCommand(cmd *cobra.Command, root string) []facet.Registry {
	var cfg *facet.Config
	var err error
	if filepath.Clean(root) == filepath.Clean(ResolveRoot(cmd)) {
		cfg, err = configForCommand(cmd)
	} else {
		cfg, err = facet.LoadConfigWithSourceView(root, sourceViewFlag(cmd))
	}
	if cfg != nil {
		return cfg.All()
	}
	reg := facet.Open(root)
	if sourceViewFlag(cmd) != "" {
		reg.SourceView = sourceview.Failed(sourceViewFlag(cmd), err)
	}
	return []facet.Registry{reg}
}
func newHookContextForCommand(cmd *cobra.Command, root string) hookContext {
	ctx := hookContext{root: root, selectedSourceView: sourceViewFlag(cmd)}
	for _, reg := range hookRegistriesForCommand(cmd, root) {
		if reg.Adopted() {
			ctx.registries = append(ctx.registries, reg)
		}
	}
	return ctx
}

func hookRootCandidates(cmd *cobra.Command, in hookInput) []string {
	var out []string
	add := func(paths ...string) {
		for _, p := range paths {
			if p == "" {
				continue
			}
			if abs, err := filepath.Abs(p); err == nil {
				p = abs
			}
			for _, seen := range out {
				if seen == p {
					return
				}
			}
			out = append(out, p)
		}
	}

	if looksLikeCommit(in.ToolInput.Command) {
		base := in.CWD
		if base == "" {
			base = os.Getenv("CLAUDE_PROJECT_DIR")
		}
		if base == "" {
			base = ResolveRoot(cmd)
		}

		if named := commitRootCandidates(base, in.ToolInput.Command); len(named) > 0 {
			add(named...)
			return out
		}
	}

	if path := in.ToolInput.FilePath; path != "" {
		if !filepath.IsAbs(path) && in.CWD != "" {
			path = filepath.Join(in.CWD, path)
		}
		dir := filepath.Dir(path)
		for !isDirectoryOnDisk(dir) && filepath.Dir(dir) != dir {
			dir = filepath.Dir(dir)
		}
		add(gitToplevel(dir))
	}
	add(gitToplevel(in.CWD), in.CWD)
	add(os.Getenv("CLAUDE_PROJECT_DIR"))
	add(ResolveRoot(cmd))
	return out
}

// gitToplevel names the working tree a directory belongs to, which inside a
// worktree is the worktree rather than the checkout it was created from.
func gitToplevel(dir string) string {
	if dir == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// hookContext is the project as a hook sees it: one registry in almost every
// project, several in a monorepo that declares them.
type hookContext struct {
	selectedSourceView string
	root               string
	registries         []facet.Registry
	output             io.Writer
	send               func(hookOutput)
}

func newHookContext(root string) hookContext {
	all, err := facet.OpenAll(root)
	if err != nil {
		all = []facet.Registry{facet.Open(root)}
	}
	ctx := hookContext{root: root}
	for _, reg := range all {
		if reg.Adopted() {
			ctx.registries = append(ctx.registries, reg)
		}
	}
	return ctx
}

// primary is the registry to speak for when a message is about the project
// rather than about one file.
func (h hookContext) primary() facet.Registry { return h.registries[0] }

// multi reports whether messages need to say which registry they mean.
func (h hookContext) multi() bool { return len(h.registries) > 1 }

// tag prefixes a message fragment with the registry name, and returns it
// unchanged in a project that declares none.
func (h hookContext) tag(reg facet.Registry, s string) string {
	if !h.multi() || reg.Name == "" {
		return s
	}
	return "[" + reg.Name + "] " + s
}

// recordRegistry finds the registry whose records directory holds a path.
func (h hookContext) recordRegistry(path string) (facet.Registry, bool) {
	for _, reg := range h.registries {
		if strings.HasPrefix(path, reg.RecordsDir+string(filepath.Separator)) {
			return reg, true
		}
	}
	return facet.Registry{}, false
}

// sourceRegistries lists every registry that scans a source file. Shared code
// in a monorepo legitimately belongs to more than one.
func (h hookContext) sourceRegistries(path string) []facet.Registry {
	rel, err := filepath.Rel(h.root, path)
	if err != nil {
		return h.registries
	}
	var out []facet.Registry
	for _, reg := range h.registries {
		if reg.Claims(filepath.ToSlash(rel)) {
			out = append(out, reg)
		}
	}
	return out
}

// resolves reports whether an anchor has a record in any registry entitled to
// judge it, and names the registries that were entitled to.
func (h hookContext) resolves(a facet.Anchor, claiming []facet.Registry) bool {
	for _, reg := range claiming {
		if a.BelongsTo(reg.Name) && isFileOnDisk(reg.PathForID(a.ID)) {
			return true
		}
	}
	return false
}

// hookSessionStart tells the session what the registry looks like and which
// rules apply, replacing the CLAUDE.md boilerplate a consumer would maintain.
// @facet: authoring/session/registry-context
// @facet-role: primary
func hookSessionStart(ctx hookContext) {
	expireNotices(30 * 24 * time.Hour)
	var b strings.Builder
	var total int
	for _, reg := range ctx.registries {
		total += summariseRegistry(ctx, reg, &b)
	}
	if total == 0 {
		return
	}

	b.WriteString(`
Working rules:
- A change to user-visible behaviour updates the matching record in the same
  change, not in a follow-up (FACET spec 12.1).
- 'lifecycle: active' requires at least one '@facet:' anchor in source. Use
  'lifecycle: planned' for records whose code does not exist yet.
- Ground all five Knowledge slots in implementation, contract, documentation
  and real entry points. Draft supported answers; identify inferred answers in
  the handover for human review. Leave an answer absent only when evidence
  cannot answer it, and flag the missing owner input outside the YAML.
  Never invent owner facts, permissions, entry points or usage history.`)

	if ctx.multi() {
		b.WriteString(`
- Ids are global across registries: the same id in two of them is the SAME
  feature, documented independently by each product, with one record each.
  Reuse the id rather than inventing a suffixed one, and never link the two in
  'lineage' — the shared id already says they are the same feature.
- A record belongs to exactly one registry. Write it under that registry's
  records directory, never the default.
- Anchor a shared file with '@facet: <registry>:<id>' when the id exists in
  more than one. Shared code still proves no presence: 'active' in a registry
  needs an anchor in code that registry alone claims.`)
	}

	b.WriteString("\n\nUse the FACET specification for record structure and 'facet lint --help' for validation.")

	// The project's own authoring conventions, if it keeps any — named here so
	// the agent reads the same document a reviewer and 'facet conventions' do.
	appendConventions(&b, ctx)

	// The roster itself, so the first question about a feature does not begin
	// with a search for the file that answers it.
	appendIndex(&b, ctx)

	ctx.emit("SessionStart", b.String())
}

// summariseRegistry appends one registry's shape to the session summary and
// returns how many records it holds.
func summariseRegistry(ctx hookContext, reg facet.Registry, b *strings.Builder) int {
	features, err := reg.LoadFeatures()
	if err != nil || len(features) == 0 {
		return 0
	}

	counts := map[string]int{}
	perVariant := map[string]map[string]int{}
	schema := ""
	var needKnowledge int
	for _, r := range features {
		if schema == "" {
			schema = r.SchemaVersion
		}
		if variants := r.Variants(); variants != nil {
			strongest := "planned"
			for name, state := range variants {
				if perVariant[name] == nil {
					perVariant[name] = map[string]int{}
				}
				perVariant[name][state.Lifecycle]++
				if state.Lifecycle == "active" {
					strongest = "active"
				}
			}
			counts[strongest]++
		} else {
			counts[r.Lifecycle]++
		}
		if r.ActiveAnywhere() && len(r.KnowledgeGaps()) > 0 {
			needKnowledge++
		}
	}

	if b.Len() == 0 {
		fmt.Fprintf(b, "This project documents its features with FACET")
		if schema != "" {
			fmt.Fprintf(b, " (schema %s)", schema)
		}
		b.WriteString(".\n")
	}

	name := "Registry"
	if ctx.multi() && reg.Name != "" {
		name = "Registry " + reg.Name
		if desc := registryDescription(ctx.root, reg.Name); desc != "" {
			name += " (" + desc + ")"
		}
	}
	fmt.Fprintf(b, "\n%s: %d feature record(s) — %d active, %d planned, %d deprecated.\n",
		name, len(features), counts["active"], counts["planned"], counts["deprecated"])
	fmt.Fprintf(b, "Records live in %s/features/<domain>/<capability>/<feature>.yaml.\n", reg.Rel(reg.RecordsDir))
	if len(reg.Sources) > 0 {
		fmt.Fprintf(b, "It owns %s.\n", strings.Join(reg.Sources, ", "))
	}
	b.WriteString("Source files link back to them with '@facet: <id>' comments.\n")
	if needKnowledge > 0 {
		fmt.Fprintf(b, "%d active record(s) have Knowledge slots still awaiting their owner.\n", needKnowledge)
	}
	if vm, err := facet.LoadVariantMap(reg.VariantMapPath()); err == nil && vm.Configured() {
		fmt.Fprintf(b, "\nVariants (declared in %s):\n", reg.Rel(reg.VariantMapPath()))
		for _, name := range vm.Names() {
			c := perVariant[name]
			fmt.Fprintf(b, "  %s: %d active, %d planned\n", name, c["active"], c["planned"])
		}
		b.WriteString(`- A feature existing on several variants is ONE record with a per-variant
  lifecycle. Never split a record on a variant boundary.
- A record with variants has no top-level 'lifecycle'; the two are exclusive.
- 'active' on a variant needs an anchor in a path exclusive to it. Shared code
  is worth anchoring but proves no variant.
`)
	}
	return len(features)
}

// registryDescription reads the one-line description a registry declared, so a
// monorepo summary says what each registry is rather than only naming it.
func registryDescription(root, name string) string {
	cfg, err := facet.LoadConfig(root)
	if err != nil {
		return ""
	}
	return cfg.Description(name)
}

// hookPreCompact re-states the working rules before the context is compacted.
// @facet: authoring/session/registry-context
// @facet-role: primary
func hookPreCompact(ctx hookContext, session string) {
	var b strings.Builder
	b.WriteString(`Carrying forward: this project documents its features with FACET.

- A change to user-visible behaviour updates the matching record in the same
  change, not in a follow-up (spec 12.1).
- 'lifecycle: active' requires an '@facet:' anchor in source.
- Ground all five Knowledge slots in implementation, contract, documentation
  and real entry points. Draft supported answers; identify inferred answers in
  the handover for human review. Leave an answer absent only when evidence
  cannot answer it, and flag the missing owner input outside the YAML.
  Never invent owner facts, permissions, entry points or usage history.
- Run 'facet lint' before committing; the commit gate runs it anyway.`)

	if ctx.multi() {
		var names []string
		for _, r := range ctx.registries {
			names = append(names, r.Name)
		}
		fmt.Fprintf(&b, "\n\nRegistries: %s. Ids are global — the same id in two of them is the SAME\nfeature with a record in each, so reuse it rather than inventing one. Shared\ncode proves no presence; qualify its anchor as '<registry>:<id>'.",
			strings.Join(names, ", "))
	}

	for _, reg := range ctx.registries {
		if vm, err := facet.LoadVariantMap(reg.VariantMapPath()); err == nil && vm.Configured() {
			fmt.Fprintf(&b, "\n\n%sVariants: %s. One record per feature with a per-variant\nlifecycle — never one record per variant. Shared code proves no variant.",
				ctx.tag(reg, ""), strings.Join(vm.Names(), ", "))
		}
	}

	// Compaction is when the pointer to the project's conventions is most
	// likely to be summarised away, so it is said again here.
	appendConventions(&b, ctx)

	// Every repository this session edited, for the same reason the Stop hook
	// asks them all: the work may sit in a worktree the cwd never pointed at.
	touched, pending := preCompactChanges(sessionContexts(ctx, session), facet.ChangedFiles)
	sort.Strings(touched)
	sort.Strings(pending)
	touched = dedupe(touched)
	pending = dedupe(pending)

	if len(touched) > 0 {
		fmt.Fprintf(&b, "\n\nRecords changed so far in this working tree:\n")
		for _, id := range touched {
			fmt.Fprintf(&b, "- %s\n", id)
		}
		b.WriteString("Their contract scenarios are the reference for what the code must do.")
	}

	if len(pending) > 0 {
		fmt.Fprintf(&b, "\n\nAnchored source changed without its record following:\n")
		for _, id := range pending {
			fmt.Fprintf(&b, "- %s\n", id)
		}
		b.WriteString("Decide whether each is behaviour-preserving before finishing.")
	}

	// Emit the full record index before compaction so the compacted context
	// contains the complete roster.
	appendIndex(&b, ctx)

	ctx.emit("PreCompact", b.String())
}

func preCompactChanges(contexts []hookContext, changedFiles func(string) []string) (touched, pending []string) {
	for _, c := range contexts {

		changed := changedFiles(c.root)
		for _, reg := range c.registries {
			for _, id := range touchedRecords(reg, changed) {
				touched = append(touched, c.tag(reg, id))
			}
			for _, id := range unanchoredChanges(reg, changed) {
				pending = append(pending, c.tag(reg, id))
			}
		}
	}
	return touched, pending
}

// appendIndex writes the record roster onto a hook's message.
func appendIndex(b *strings.Builder, ctx hookContext) {
	if index := largeRegistryIndex(ctx); index != "" {
		b.WriteString("\n\n")
		b.WriteString(index)
		return
	}
	index := facet.RecordIndex(ctx.registries, facet.IndexOptions{Summaries: true})
	if index == "" {
		return
	}
	b.WriteString("\n\n")
	b.WriteString(index)
}

// relRecordsDir is the records directory as the agent should write it: a
// repository-relative path, never absolute.
func relRecordsDir(reg facet.Registry) string {
	rel, err := filepath.Rel(reg.Root, reg.RecordsDir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(reg.RecordsDir)
	}
	return filepath.ToSlash(rel)
}

// largeRegistryIndex takes the same deliberately plain fast path as the shell
// fallback. Above 400 records, parsing every YAML document just to rebuild the
// roster dominates PreCompact; the id is already encoded in the path. An empty
// result means the normal, richer index should be used.
func largeRegistryIndex(ctx hookContext) string {
	type entry struct{ registry, id string }
	var entries []entry
	for _, reg := range ctx.registries {
		base := filepath.Join(reg.RecordsDir, "features")
		_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || filepath.Ext(path) != ".yaml" {
				return nil
			}
			rel, err := filepath.Rel(base, path)
			if err == nil {
				entries = append(entries, entry{reg.Name, strings.TrimSuffix(filepath.ToSlash(rel), ".yaml")})
			}
			return nil
		})
	}
	if len(entries) <= 400 {
		return ""
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].registry != entries[j].registry {
			return entries[i].registry < entries[j].registry
		}
		return entries[i].id < entries[j].id
	})

	var b strings.Builder
	fmt.Fprintf(&b, "FACET record index — all %d record(s) in this project, so you do not have to search for them.\n", len(entries))
	// Where the files are, said as repository-relative in as many words.

	if !ctx.multi() && len(ctx.registries) == 1 {
		rel := relRecordsDir(ctx.registries[0])
		fmt.Fprintf(&b, "A record's file is %s/features/<id>.yaml — for example %s/features/%s.yaml.\nThese paths are relative to the repository root. Never prefix one with '/'.\n",
			rel, rel, entries[0].id)
	}
	b.WriteString("This registry is large, so the lines carry ids only. Open a record for its state and its summary.\n")
	current := "\x00"
	for i, e := range entries {
		line := "- " + e.id + "\n"
		if ctx.multi() && e.registry != current {
			current = e.registry
			line = "\nRegistry " + e.registry + ":\n" + line
		}
		footer := fmt.Sprintf("\n%d further record(s) are not listed here: the index did not fit. Search the records directory for those.\n", len(entries)-i)
		if b.Len()+len(line)+len(footer) > facet.DefaultIndexBytes {
			b.WriteString(footer)
			break
		}
		b.WriteString(line)
	}
	return b.String()
}

// touchedRecords lists the feature ids whose records changed in the working tree.
func touchedRecords(reg facet.Registry, changed []string) []string {
	prefix := reg.Rel(reg.RecordsDir) + "/features/"
	var ids []string
	for _, rel := range changed {
		if strings.HasPrefix(rel, prefix) && strings.HasSuffix(rel, ".yaml") {
			ids = append(ids, strings.TrimSuffix(strings.TrimPrefix(rel, prefix), ".yaml"))
		}
	}
	sort.Strings(ids)
	return ids
}

// unanchoredChanges lists features whose anchored source moved while their
// record did not — the same condition the Stop hook reports, recomputed here
// because the Stop hook's once-per-session notice may already have scrolled
// out of the context being compacted.
func unanchoredChanges(reg facet.Registry, changed []string) []string {
	touched := map[string]bool{}
	for _, id := range touchedRecords(reg, changed) {
		touched[id] = true
	}
	seen := map[string]bool{}
	var ids []string
	recordsPrefix := reg.Rel(reg.RecordsDir) + "/"
	for _, rel := range changed {
		if strings.HasPrefix(rel, recordsPrefix) {
			continue
		}
		anchors, err := facet.ScanFile(filepath.Join(reg.Root, rel))
		if err != nil {
			continue
		}
		for _, a := range anchors {
			if !a.BelongsTo(reg.Name) || touched[a.ID] || seen[a.ID] || !isFileOnDisk(reg.PathForID(a.ID)) {
				continue
			}
			seen[a.ID] = true
			ids = append(ids, a.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// hookPostToolUse validates a record as it is written, and verifies that the
// anchors in an edited source file still resolve.
func hookPostToolUse(ctx hookContext, in hookInput) {
	if in.ToolName == "Bash" {
		hookBashPostToolUse(ctx, in)
		return
	}
	path := in.ToolInput.FilePath
	if path != "" && !filepath.IsAbs(path) {
		base := in.CWD
		if base == "" {
			base = ctx.root
		}
		path = filepath.Join(base, path)
	}
	if path == "" {
		return
	}
	if !isFileOnDisk(path) {
		return
	}

	// A record is validated by the registry that holds it, and by no other:
	// running the other registries' schemas over it would report every field
	// they do not share as an error.
	if reg, ok := ctx.recordRegistry(path); ok {
		// The record just moved, so the obligation its anchored files carried is
		// discharged and their reminders start again from nothing.
		if id := reg.IDForPath(path); id != "" {
			clearNotices(in.SessionID, anchorNotice(reg, id, ""))
		}
		hookValidateRecord(ctx, reg, path, in.SessionID)
		return
	}
	hookRefreshPrivateAnchors(path)
	hookCheckAnchors(ctx, in, path)
}

// bulkMaxFiles caps the anchored files one Bash event reminds about.
const bulkMaxFiles = 40

// hookBashPostToolUse reminds about the anchored files in the dirty tree after
// a Bash command, which may have changed any of them.
func hookBashPostToolUse(ctx hookContext, in hookInput) {
	var contexts, candidates []string
	// Bulk reminders inspect the dirty tree, not earlier committed changes
	// retained by an explicit human review baseline.
	for _, rel := range facet.LoadChangeSet(ctx.root, "HEAD").Changed() {
		path := filepath.Join(ctx.root, rel)
		// Bulk snapshots are for source anchors. Validating every dirty
		// record here would reset reminders on every read-only command.
		if _, record := ctx.recordRegistry(path); record || !isFileOnDisk(path) {
			continue
		}
		candidates = append(candidates, path)
	}
	hookRefreshPrivateAnchorsBatch(ctx.root, candidates)

	inspected := 0
	capped := false
	for _, path := range candidates {
		if !mayCarryAnchor(path) {
			continue
		}
		if inspected == bulkMaxFiles {
			capped = true
			break
		}
		inspected++
		if context := hookAnchorContext(ctx, in, path); context != "" {
			contexts = append(contexts, context)
		}
	}
	if capped {
		contexts = append(contexts, fmt.Sprintf("More than %d changed files carry FACET anchors; only the first %d were\nchecked after this command. Run 'facet lint' for the rest.", bulkMaxFiles, bulkMaxFiles))
	}
	if len(contexts) > 0 {
		ctx.emit("PostToolUse", strings.Join(contexts, "\n\n"))
	}
}

// mayCarryAnchor is the cheap test that keeps a dirty file without an anchor
// from costing a full scan. Files too large to read whole are judged by their
// head, which is where an anchor sits.
func mayCarryAnchor(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 256*1024)
	n, _ := io.ReadFull(f, head)
	return bytes.Contains(head[:n], []byte("@facet:"))
}

// hookRefreshPrivateAnchorsBatch is hookRefreshPrivateAnchors for a whole
// dirty tree: one git call in place of one per file.
func hookRefreshPrivateAnchorsBatch(root string, paths []string) {
	if len(paths) == 0 || !privateAnchorFilterInstalled(root) {
		return
	}
	rels := make([]string, 0, len(paths))
	for _, p := range paths {
		if rel, err := filepath.Rel(root, p); err == nil {
			rels = append(rels, filepath.ToSlash(rel))
		}
	}
	_ = refreshIndex(root, rels)
}

// privateAnchorFilterInstalled reports whether the clone keeps anchors out of
// git, which is the only case an index refresh is needed in.
func privateAnchorFilterInstalled(root string) bool {
	// The config lives in the directory every worktree shares; a linked
	// worktree's own git dir has none, and reading there would switch the
	// refresh off in exactly the checkouts that need it.
	gitDir, err := facet.GitCommonDir(root)
	if err != nil {
		return false
	}
	config, err := os.ReadFile(filepath.Join(gitDir, "config"))
	return err == nil && strings.Contains(string(config), `[filter "facet"]`)
}

// hookRefreshPrivateAnchors keeps 'git status' honest in a clone that keeps
// anchors out of git. Git judges a file modified by size before it consults
// the clean filter, so a file that just gained an anchor line reads as
// modified while 'git diff' is empty — and 'git pull' refuses to run. The
// index refresh is what clears it, and the write hook is the one place that
// sees every edit an agent makes, so it clears it here rather than leaving a
// command for the reader to know about. Skipped, at the cost of one file
// read, wherever the filter is not installed.
//
// @facet: registry/layout/private-anchors
// @facet-role: related
func hookRefreshPrivateAnchors(path string) {
	root := repoRootOf(path)
	if root == "" || !privateAnchorFilterInstalled(root) {
		return
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return
	}
	_ = refreshIndex(root, []string{filepath.ToSlash(rel)})
}

// repoRootOf walks up from a file to the working tree that contains it,
// without a subprocess: the hook runs on every write.
func repoRootOf(path string) string {
	dir := filepath.Dir(path)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// recordSaveSkips lists the checks a record save does not run. Each either
// reports on something other than the record being saved or has its findings
// thrown away by the caller, and each costs a walk of the tree or of git.
func recordSaveSkips(errorsOnly bool) map[string]bool {
	skip := map[string]bool{"orphan-ids": true, "orphan-records": true, "registry-refs": true, "test-only-anchors": true, "lost-anchors": true}
	if errorsOnly {
		for _, c := range []string{"created-at-history", "unclaimed-tests", "unanchored-hotspots", "undocumented-directories"} {
			skip[c] = true
		}
	}
	return skip
}

// @facet: authoring/guardrails/write-validation
// @facet-role: primary
func hookValidateRecord(ctx hookContext, reg facet.Registry, path string, session string) {
	if filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml" {
		return
	}
	report, err := facet.ValidateRecord(reg, path)
	if err != nil {
		return
	}
	var msgs []string
	var sizing []facet.Finding
	for _, f := range report.Findings {
		if filepath.Join(reg.Root, f.Path) != path {
			continue
		}
		if f.Severity == facet.SeverityWarning {

			if f.Check == "sizing" {
				sizing = append(sizing, f)
			}
			continue
		}
		if f.Severity != facet.SeverityError {
			continue
		}
		msg := "- " + f.Message
		if f.Hint != "" {
			msg += "\n  " + f.Hint
		}
		msgs = append(msgs, msg)
	}

	if len(msgs) > 0 {
		ctx.emit("PostToolUse", fmt.Sprintf("FACET record %s has problems that need fixing now:\n%s",
			ctx.tag(reg, reg.Rel(path)), strings.Join(msgs, "\n")))
		return
	}

	// Emit sizing feedback once per record and session, avoiding repeated
	// notices for successive saves of the same record.
	for _, f := range sizing {
		if !noticeIsNew(session, "sizing:"+f.ID) {
			continue
		}
		ctx.emit("PostToolUse", fmt.Sprintf("FACET record %s: %s\n%s\n\nThis is a question, not a defect — a record may exceed the band with reason. If it is mis-sized, the fix is cheapest now, before the id is anchored and depended on.",
			ctx.tag(reg, reg.Rel(path)), f.Message, f.Hint))
	}
}

// @facet: authoring/guardrails/anchor-reminders
// @facet-role: primary
func hookCheckAnchors(ctx hookContext, in hookInput, path string) {
	if context := hookAnchorContext(ctx, in, path); context != "" {
		ctx.emit("PostToolUse", context)
	}
}

// hookAnchorContext returns one file's reminder so bulk edits can emit a
// single hook response while sharing the normal feature-and-file markers.
func hookAnchorContext(ctx hookContext, in hookInput, path string) string {
	anchors, err := facet.ScanFile(path)
	if err != nil || len(anchors) == 0 {
		return ""
	}
	// In a monorepo a shared file is scanned by several registries, and its
	// anchor resolves as long as one of them owns the id. Reporting it against
	// each registry separately would call every shared anchor broken.
	claiming := ctx.sourceRegistries(path)
	if len(claiming) == 0 {
		return ""
	}
	reg := claiming[0]

	var missing, known []string
	var resolved []facet.Anchor
	seen := map[string]bool{}
	for _, a := range anchors {
		if seen[a.Qualified()] {
			continue
		}
		seen[a.Qualified()] = true
		if ctx.resolves(a, claiming) {
			known = append(known, a.Qualified())
			resolved = append(resolved, a)
			continue
		}
		owner := reg
		if a.Registry != "" {
			for _, r := range ctx.registries {
				if r.Name == a.Registry {
					owner = r
				}
			}
		}
		missing = append(missing, fmt.Sprintf("- %q has no record at %s",
			a.Qualified(), owner.Rel(owner.PathForID(a.ID))))
	}
	sort.Strings(missing)
	sort.Strings(known)

	if len(missing) > 0 {
		return fmt.Sprintf("%s carries @facet: anchors that do not resolve:\n%s\n\nCreate the missing record(s) or correct the tag id before committing.",
			reg.Rel(path), strings.Join(missing, "\n"))
	}
	if len(known) == 0 {
		return ""
	}

	fresh := false
	for _, a := range resolved {
		for _, owner := range claiming {
			if !a.BelongsTo(owner.Name) || !isFileOnDisk(owner.PathForID(a.ID)) {
				continue
			}
			if noticeIsNew(in.SessionID, anchorNotice(owner, a.ID, owner.Rel(path))) {
				fresh = true
			}
		}
	}
	if !fresh {
		return ""
	}

	// Related anchors receive a shorter notice than primary implementation
	// anchors, preserving the distinction between their roles.
	if !anyPrimary(resolved) {
		return fmt.Sprintf("%s is related to FACET feature(s): %s. A contract rarely breaks here, but the record is the place to check if it did.",
			reg.Rel(path), strings.Join(known, ", "))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s implements FACET feature(s): %s.\n", reg.Rel(path), strings.Join(known, ", "))
	sort.Slice(resolved, func(i, j int) bool { return resolved[i].Qualified() < resolved[j].Qualified() })
	for _, a := range resolved {
		if digest := recordDigest(claiming, a); digest != "" {
			b.WriteString("\n" + digest)
		}
	}
	b.WriteString(`
If this edit changes observable behaviour, update the matching record in this
same change — intent, contract scenarios, and any Knowledge slot it invalidates
(FACET spec 12.1). If the change is behaviour-preserving, no record edit is needed.`)
	return b.String()
}

// anchorNotice names the marker for one feature seen in one file.
func anchorNotice(reg facet.Registry, id, rel string) string {
	// Hash each component separately: marker filenames stay bounded and the
	// feature prefix can clear exactly this registry's files after a record edit.
	scope := sha256.Sum256([]byte(reg.Root + "\x00" + reg.Name + "\x00" + reg.RecordsDir))
	feature := sha256.Sum256([]byte(id))
	prefix := fmt.Sprintf("anchor:%x:%x:", scope, feature)
	if rel == "" {
		return prefix
	}
	file := sha256.Sum256([]byte(rel))
	return fmt.Sprintf("%s%x", prefix, file)
}

// anyPrimary reports whether the file implements a feature rather than merely
// touching one. An anchor written without a role is treated as primary, which is
// what every anchor predating '@facet-role:' meant.
func anyPrimary(anchors []facet.Anchor) bool {
	for _, a := range anchors {
		if a.Role == "" || a.Role == "primary" {
			return true
		}
	}
	return false
}

// recordDigest is what the record claims, short enough to travel with the
// reminder that mentions it.
func recordDigest(claiming []facet.Registry, a facet.Anchor) string {
	if os.Getenv("FACET_HOOK_REMINDERS") == "brief" {
		return ""
	}
	for _, reg := range claiming {
		if !a.BelongsTo(reg.Name) {
			continue
		}
		path := reg.PathForID(a.ID)
		if !isFileOnDisk(path) {
			continue
		}
		rec, err := facet.LoadRecord(path, "features")
		if err != nil {
			return ""
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s\n", a.Qualified())
		if intent := condense(rec.Intent(), 280); intent != "" {
			fmt.Fprintf(&b, "  promises: %s\n", intent)
		}
		if scenarios := rec.ContractScenarios(); len(scenarios) > 0 {
			shown := scenarios
			extra := 0
			if len(shown) > 12 {
				extra = len(shown) - 12
				shown = shown[:12]
			}
			fmt.Fprintf(&b, "  contract: %s", strings.Join(shown, ", "))
			if extra > 0 {
				fmt.Fprintf(&b, ", and %d more", extra)
			}
			b.WriteString("\n")
		}
		return b.String()
	}
	return ""
}

// condense folds a block of prose onto one line and cuts it at a word boundary,
// because the point is to recognise the feature, not to reread it.
func condense(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	cut := strings.LastIndex(s[:max], " ")
	if cut < max/2 {
		cut = max
	}
	return strings.TrimRight(s[:cut], " .,;:") + " ..."
}

// hookPreToolUse gates commits on new errors compared with HEAD.
// @facet: authoring/guardrails/commit-gate
// @facet-role: primary
func hookPreToolUse(ctx hookContext, in hookInput) {
	if in.ToolName == "Read" {
		hookReadGate(ctx, in)
		return
	}
	if os.Getenv("FACET_SKIP_COMMIT_GATE") == "1" {
		return
	}
	if !looksLikeCommit(in.ToolInput.Command) {
		return
	}
	// The cwd is where relative 'git -C' paths start, but only while it is in
	// the repository the command was resolved to. After a 'cd' into another
	// repository the cwd is the one the commit left behind.
	commitDir := ctx.root
	if in.CWD != "" && gitToplevel(in.CWD) == ctx.root {
		commitDir = in.CWD
	}
	report, err := commitLintWithSourceView(commitDir, in.ToolInput.Command, ctx.selectedSourceView)
	if err != nil {
		ctx.deny("PreToolUse", "FACET commit gate could not verify this commit: "+err.Error()+". Run a plain staged commit after fixing this.\n\n"+commitGateEscape)
		return
	}
	if !report.Failed() {
		return
	}
	var lines []string
	for _, f := range report.Findings {
		if f.Severity != facet.SeverityError {
			continue
		}
		loc := f.Path
		if f.Registry != "" {
			loc = "[" + f.Registry + "] " + loc
		}
		lines = append(lines, fmt.Sprintf("  %s: %s", loc, f.Message))
		if len(lines) >= 25 {
			break
		}
	}
	ctx.deny("PreToolUse", fmt.Sprintf(`FACET lint found new errors compared with HEAD in the staged commit:

%s

Fix the records or the anchors, then commit again. Run 'facet lint' for the full
report.

%s`, strings.Join(lines, "\n"), commitGateEscape))
}

// commitGateEscape describes the host environment required for the bypass.
// The gate inherits the environment in which Claude Code was started; a
// FACET_SKIP_COMMIT_GATE=1 prefix on a git command cannot reach the hook and
// is refused as an unverifiable selection.
const commitGateEscape = `To commit anyway, FACET_SKIP_COMMIT_GATE=1 must be set in the
environment Claude Code was started with. Prefixing it to the git command does
not reach this gate. An agent cannot set it: ask the person running the session.`

// gitValueOptions lists git options that consume the next argument. Their
// values must be skipped to find the subcommand: in git -C /path commit,
// /path is an option value and commit is the subcommand.
var gitValueOptions = map[string]bool{
	"-C": true, "-c": true, "--git-dir": true, "--work-tree": true,
	"--namespace": true, "--exec-path": true, "--super-prefix": true,
	"--config-env": true,
}

// shellSeparators end one command and begin the next, which is what makes the
// following word a command name rather than an argument. Without this, the word
// "git" inside 'echo git commit' reads as an invocation.
var shellSeparators = map[string]bool{
	"&&": true, "||": true, ";": true, "|": true, "&": true, "(": true, "{": true,
}

func looksLikeCommit(cmd string) bool {
	fields := strings.Fields(cmd)
	if words, err := lexCommitWords(cmd, true); err == nil {
		fields = words
	}
	for i, f := range fields {
		if f != "git" || !startsCommand(fields, i) {
			continue
		}
		// A line may invoke git more than once ('git add -A && git commit'), so a
		// non-commit invocation moves on to the next rather than deciding the line.
		if gitSubcommand(fields[i+1:]) == "commit" {
			return true
		}
	}
	return false
}

// gitSubcommand returns the subcommand from the arguments following 'git', or ""
// when there is none.
func gitSubcommand(rest []string) string {
	for j := 0; j < len(rest); j++ {
		tok := rest[j]
		if gitValueOptions[tok] {
			j++ // the value belongs to the option, not to git
			continue
		}
		// '--git-dir=x' carries its value inline and consumes nothing.
		if strings.HasPrefix(tok, "-") {
			continue
		}
		return tok
	}
	return ""
}

// startsCommand reports whether the token at i is in command position: at the
// start of the line, after a separator, or after environment assignments such as
// 'GIT_AUTHOR_NAME=x git commit'.
func startsCommand(fields []string, i int) bool {
	for j := i - 1; j >= 0; j-- {
		prev := fields[j]
		if shellSeparators[prev] {
			return true
		}
		if strings.Contains(prev, "=") && !strings.HasPrefix(prev, "-") {
			continue // environment assignment, keep looking left
		}
		return false
	}
	return true
}

// emitStopReview uses Stop's top-level decision envelope, not a tool permission.
func (ctx hookContext) emitStopReview(reason string) {
	json.NewEncoder(ctx.outputWriter()).Encode(struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}{"block", reason})
}

// hookStop reports anchored source that changed without its record following.
// Deterministic on purpose: it runs at every turn end, so it must be free and
// must not loop. Each record is reported once per session.
func hookStop(ctx hookContext, in hookInput) {
	var findings, stale, removed, unknown, reviewRoots, productNotices []string
	for _, c := range sessionContexts(ctx, in.SessionID) {
		reviewRoots = append(reviewRoots, c.root)
		report, err := facet.ComputeDrift(c.root, c.registries, "")
		if err != nil {
			unknown = append(unknown, fmt.Sprintf("- %s: the anchor scan failed (%v)", c.root, err))
			continue
		}
		findings = append(findings, reportDriftFindings(c, in, report)...)
		stale = append(stale, reportStaleReferences(c, in, report)...)
		removed = append(removed, referenceFindings(c, in, report)...)
		unknown = append(unknown, baselineFindings(c, in, report)...)
		productNotices = append(productNotices, productStopNotices(c, in.SessionID, report.Baseline)...)
	}
	if len(findings) == 0 && len(stale) == 0 && len(removed) == 0 && len(unknown) == 0 && len(productNotices) == 0 {
		return
	}

	var b strings.Builder
	b.WriteString("FACET: Review the following source/reference changes before finishing. A changed file is a review signal, not proof of a semantic defect. Check surviving implementation, verification references, Knowledge and lifecycle; repair the record when needed, or explain why the existing record remains correct.\n\n")
	if in.StopHookActive {
		// Deduplication already discarded delivered notices. A blanket early
		// return here would lose new records changed during the continuation.
		b.WriteString("These are new notices during the Stop-hook continuation. Previously delivered notices do not block again.\n\n")
	}
	if len(findings) > 0 {
		sort.Strings(findings)
		fmt.Fprintf(&b, `FACET: source implementing these features changed, but their records did not:

%s

If any of these changes observable behaviour, the record belongs in the same
change (FACET spec 12.1). If they are behaviour-preserving, explain that review outcome.
Once that review is complete, 'facet drift accept' records the outcome.

To have this judged rather than guessed, hand the diff to the drift-auditor
subagent. Each record is reported here only once per session.`, strings.Join(findings, "\n"))
	}
	if len(removed) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		sort.Strings(removed)
		fmt.Fprintf(&b, `FACET: these features lost an implementation reference — a file was deleted,
or an anchor was removed from it:

%s

Losing a reference does not retire the feature, and nothing here changes a record.
Deleting one source file does not establish that the feature is gone. Review
surviving implementation and references before deciding whether the record
needs reference repair or a lifecycle change.
If the code moved rather than went, put the anchor on its new home and check
verification entries that still name the removed code.

Check what else only that code used: a hook or a helper the deleted file was
the last caller of keeps its own anchor and will not be reported by anything.`, strings.Join(dedupe(removed), "\n"))
	}
	if len(unknown) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		sort.Strings(unknown)
		fmt.Fprintf(&b, `FACET: drift could not be determined here, so the silence above is not a
clean result:

%s`, strings.Join(dedupe(unknown), "\n"))
	}
	if len(stale) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		sort.Strings(stale)
		fmt.Fprintf(&b, `FACET: a file moved and these records still name where it was:

%s

A renamed file leaves the record wrong without changing a line of it, so
nothing else will report this. Update the reference, or confirm it was prose
that reads fine either way.`, strings.Join(dedupe(stale), "\n"))
	}
	if len(productNotices) > 0 {
		b.WriteString("\n\n" + strings.Join(productNotices, "\n\n"))
	}
	fmt.Fprintf(&b, "\n\nFACET review locations:\n- %s", strings.Join(dedupe(reviewRoots), "\n- "))
	b.WriteString("\n\nAfter reviewing, check the current FACET status with 'facet drift --json' in the affected repository or worktree. If the facet binary is unavailable, report that the status check could not run and base the scoped review on the listed files and records; do not claim a clean status. A deduplicated notice is not evidence that drift is resolved. Then send a complete, self-contained final response to the user's original task again: lead with the task result and retain relevant deliverables, links, verification results and remaining limitations. Update any earlier claims invalidated by the review or subsequent edits; do not claim checks passed unless they ran. Include the FACET review outcome briefly after the task result, including unresolved or unrelated changes and any failed status check. Do not finish with only a drift acknowledgement or refer the user to an earlier response. Review unrelated changes without modifying them or accepting a baseline outside the authorized scope.")
	ctx.emitStopReview(b.String())
}

// referenceFindings reports implementation associations a deletion or a
// removed anchor lost. A rename that carried its anchor along is not a loss;
// reportStaleReferences covers what it leaves wrong.
func referenceFindings(ctx hookContext, in hookInput, report facet.DriftReport) []string {
	var out []string
	for _, c := range report.ReferenceChanges {
		if !c.Lost() {
			continue
		}
		id := hookQualified(ctx, c.Registry, c.ID)
		if !noticeIsNew(in.SessionID, "reference:"+c.Kind+":"+id+":"+c.Path) {
			continue
		}
		out = append(out, "- "+id+" — "+describeReference(c))
	}
	for _, p := range report.Unreadable {
		if noticeIsNew(in.SessionID, "unreadable:"+p) {
			out = append(out, "- "+p+" — its content at the baseline could not be read, so what it anchored is unknown")
		}
	}
	return out
}

// baselineFindings says, once per session, that a repository could not be
// compared at all — so an empty Stop message there is not read as "no drift".
func baselineFindings(ctx hookContext, in hookInput, report facet.DriftReport) []string {
	if len(report.Repositories) > 0 {
		var out []string
		for _, repo := range report.Repositories {
			local := ctx
			local.root = filepath.Join(ctx.root, filepath.FromSlash(repo.Path))
			out = append(out, baselineFindings(local, in, facet.DriftReport{Baseline: repo.Baseline})...)
		}
		return out
	}
	b := report.Baseline
	// A registry outside git has no history to compare, which is a property
	// of the project rather than news for this turn; the hook stays as quiet
	// there as it always was. A repository with no first commit is the same.
	if b.Status == facet.BaselineOK || b.Status == facet.BaselineNoRepository ||
		(b.Status == facet.BaselineMissing && b.Kind == facet.BaselineHead) {
		return nil
	}
	if !noticeIsNew(in.SessionID, "baseline:"+ctx.root+":"+b.Status) {
		return nil
	}
	return []string{"- " + ctx.root + ": " + baselineLine(b)}
}

// reportStaleReferences reports records naming a path that has been renamed
// away.
func reportStaleReferences(ctx hookContext, in hookInput, report facet.DriftReport) []string {
	var out []string
	for _, s := range report.StaleReferences {
		id := hookQualified(ctx, s.Registry, s.ID)
		if !noticeIsNew(in.SessionID, "moved:"+id+":"+s.Path) {
			continue
		}
		out = append(out, fmt.Sprintf("- %s still names %s, now at %s", id, s.Path, s.RenamedTo))
	}
	return out
}

// hookQualified names a feature the way the hook's other messages do: with
// its registry only where a project declares more than one.
func hookQualified(ctx hookContext, registry, id string) string {
	if !ctx.multi() || registry == "" {
		return id
	}
	return registry + ":" + id
}

// driftFindings reports anchored source that changed while its record did not,
// computed fresh for one repository.
//
// @facet: authoring/guardrails/drift-notice
// @facet-role: primary
func driftFindings(ctx hookContext, in hookInput) []string {
	report, err := facet.ComputeDrift(ctx.root, ctx.registries, "")
	if err != nil {
		return nil
	}
	return reportDriftFindings(ctx, in, report)
}

func reportDriftFindings(ctx hookContext, in hookInput, report facet.DriftReport) []string {
	var findings []string
	var eligible []facet.Drift
	seen := map[string]bool{}
	for _, d := range report.Drift {
		id := hookQualified(ctx, d.Registry, d.ID)
		// One line per feature, as before: the hook asks for a decision about
		// a record, and the CLI lists the files for whoever wants them.
		if seen[id] {
			continue
		}
		seen[id] = true
		if isPlannedIn(ctx, d.Registry, d.ID) {
			continue
		}
		if !noticeIsNew(in.SessionID, "drift:"+id) {
			continue
		}
		eligible = append(eligible, d)
	}
	notes := recordProductWarnings(ctx.root, ctx.registries, eligible, report.Baseline.Commit)
	for i, d := range eligible {
		findings = append(findings, fmt.Sprintf("- %s — changed: %s%s", hookQualified(ctx, d.Registry, d.ID), d.Path, notes[i]))
	}
	return findings
}

// isPlannedIn is isPlanned for an id a registry has already resolved.
func isPlannedIn(ctx hookContext, registry, id string) bool {
	for _, reg := range ctx.registries {
		if reg.Name != registry {
			continue
		}
		return isPlanned(ctx, facet.Anchor{ID: id, Registry: registry}, []facet.Registry{reg})
	}
	return false
}

// isPlanned reports whether the anchor's record is still a promise rather than
// a description.
func isPlanned(ctx hookContext, a facet.Anchor, claiming []facet.Registry) bool {
	for _, reg := range claiming {
		if !a.BelongsTo(reg.Name) {
			continue
		}
		path := reg.PathForID(a.ID)
		if !isFileOnDisk(path) {
			continue
		}
		rec, err := facet.LoadRecord(path, "features")
		if err != nil {
			// Unreadable is not planned. resolves() already established the
			// file is there, so this is a broken record, and the lint suite
			// says so in words this hook has no business repeating.
			return false
		}
		return rec.Lifecycle == "planned"
	}
	return false
}

// dedupe removes repeats from a sorted slice. Two repositories in one session
// can name the same record, and saying it twice would read as two problems.
func dedupe(in []string) []string {
	var out []string
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// sessionContexts lists the project as seen from every repository this session
// has edited: the one the hook was called against first, then any other the
// PostToolUse hook has recorded.
func sessionContexts(ctx hookContext, session string) []hookContext {
	out := []hookContext{ctx}
	for _, root := range rememberedRoots(session) {
		if root == ctx.root {
			continue
		}
		if other := newHookContext(root); len(other.registries) > 0 {
			other.output, other.send = ctx.output, ctx.send
			out = append(out, other)
		}
	}
	return out
}

// rememberRoot notes that this session has acted on a repository, so the Stop
// and PreCompact hooks can ask it about drift even though the session's cwd
// points somewhere else entirely.
func rememberRoot(session, root string) {
	if root == "" {
		return
	}
	dir := sessionDir(session)
	if dir == "" {
		return
	}
	for _, known := range rememberedRoots(session) {
		if known == root {
			return
		}
	}
	f, err := os.OpenFile(filepath.Join(dir, "roots"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, root)
}

func rememberedRoots(session string) []string {
	dir := sessionDir(session)
	if dir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "roots"))
	if err != nil {
		return nil
	}
	var roots []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			roots = append(roots, line)
		}
	}
	return roots
}

// sessionDir is where a session's markers live, created on demand.
func sessionDir(session string) string {
	base := os.Getenv("CLAUDE_PLUGIN_DATA")
	if base == "" {
		base = os.TempDir()
	}
	if session == "" {
		session = "nosession"
	}
	dir := filepath.Join(base, "notices", session)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	return dir
}

// noticeIsNew keeps a once-per-session marker so a reminder does not repeat on
// every edit, and a Stop hook cannot argue with itself indefinitely.
func noticeIsNew(session, key string) bool {
	dir := sessionDir(session)
	if dir == "" {
		return true
	}
	marker := filepath.Join(dir, sanitise(key))
	// Claim delivery atomically: concurrent Stop invocations must not both
	// observe an absent marker and emit the same notice.
	file, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if os.IsExist(err) {
		return false
	}
	if err != nil {
		return true // Persistence failures must not suppress a review.
	}
	_ = file.Close()
	return true
}

// expireNotices removes the markers of sessions that ended long ago.
func expireNotices(maxAge time.Duration) {
	base := os.Getenv("CLAUDE_PLUGIN_DATA")
	if base == "" {
		return // the temp directory is the system's to clean
	}
	root := filepath.Join(base, "notices")
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) > maxAge {
			os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}

// clearNotices forgets every marker whose key begins with prefix, so a reminder
// that has been acted on can be given again if the same ground is covered twice.
func clearNotices(session, prefix string) {
	dir := sessionDir(session)
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	want := sanitise(prefix)
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), want) {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func sanitise(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func isFileOnDisk(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// setupHintText is the whole hint: two lines, naming the command and saying
// what it is for. It starts nothing; a plugin that launched an assistant
// unasked would be switched off.
const setupHintText = `FACET is installed but this repository has not adopted it yet.
Add a FACET registry and records following the specification or the minimal example, then run facet setup status (or ignore this; it is shown once per repository).`

// hookSetupHint tells a session in an un-adopted git repository that
// registry setup is available, once per repository and never anywhere else.
// @facet: authoring/session/registry-context
// @facet-role: related
func hookSetupHint(ctx hookContext, in hookInput) {
	root := gitToplevel(in.CWD)
	if root == "" {
		root = gitToplevel(os.Getenv("CLAUDE_PROJECT_DIR"))
	}
	if root == "" {
		return
	}
	// A dotfiles repository at $HOME makes every directory below it "inside
	// git". That is not a project that meant to adopt anything.
	if home, err := os.UserHomeDir(); err == nil && sameDir(root, home) {
		return
	}
	for _, rel := range []string{"docs/facets", facet.ConfigName} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			return
		}
	}
	if !setupHintIsNew(root) {
		return
	}
	ctx.emit("SessionStart", setupHintText)
}

func sameDir(a, b string) bool {
	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// setupHintIsNew keeps a marker per repository, not per session: a session id
// changes every time, and the hint is about the repository. The marker is a
// plain file beside the per-session directories, which expireNotices leaves
// alone because it only removes directories.
func setupHintIsNew(root string) bool {
	base := os.Getenv("CLAUDE_PLUGIN_DATA")
	if base == "" {
		// A shared temp directory is writable by every user on the machine:
		// another user could create the marker first and silence the hint, or
		// plant a symlink. A per-user directory only this user can enter avoids
		// both.
		base = filepath.Join(os.TempDir(), fmt.Sprintf("facet-%d", os.Getuid()))
	}
	dir := filepath.Join(base, "notices")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false // cannot remember it, so do not risk repeating it
	}
	sum := sha256.Sum256([]byte(root))
	marker := filepath.Join(dir, fmt.Sprintf("setup-hint-%x", sum[:8]))
	f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	f.Close()
	return true
}
