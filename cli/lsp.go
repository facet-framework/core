// @facet: navigation/interfaces/language-server
// @facet-role: primary
package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/facet-framework/core/registry"
	"github.com/spf13/cobra"
)

// The language server makes the registry navigable in an editor: an anchor
// resolves to its record on hover and on go-to-definition, an unresolvable one
// is underlined as you type rather than at commit time, and completion offers
// the ids that actually exist.

const lspName = "facet"

// LSP diagnostic severities.
const (
	sevError       = 1
	sevWarning     = 2
	sevInformation = 3
)

// anchorLineRe matches an anchor tag and captures the id's position on the line,
// registry prefix included, so hover and go-to-definition land on the whole
// reference rather than on the part after the colon.
var anchorLineRe = regexp.MustCompile(`@facet:[ \t]*((?:[a-z][a-z0-9-]*:)?[a-z][a-z0-9-]*/[a-z][a-z0-9-]*/[a-z][a-z0-9-]+)`)

// lspRoleRe mirrors the scanner's role tag.
var lspRoleRe = regexp.MustCompile(`@facet-role:[ \t]*(primary|related|config|data)`)

// completionTriggerRe matches a partially typed anchor, so completion fires
// while the id is still incomplete and therefore unmatched by anchorLineRe.
var completionTriggerRe = regexp.MustCompile(`@facet:[ \t]*([a-z0-9/:-]*)$`)

// anchorBareIDRe mirrors anchorLineRe, but keeps the registry prefix and the
// bare id as separate capture groups so a rename can replace just the id and
// leave a written qualifier untouched.
var anchorBareIDRe = regexp.MustCompile(`@facet:[ \t]*(?:([a-z][a-z0-9-]*):)?([a-z][a-z0-9-]*/[a-z][a-z0-9-]*/[a-z][a-z0-9-]+)`)

// bareIDPattern is what a rename's newName must look like - the same shape
// ids are required to have everywhere else, so a malformed target fails the
// rename outright rather than leaving anchors and a record that no longer
// parse as one.
var bareIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*/[a-z][a-z0-9-]*/[a-z][a-z0-9-]+$`)

// recordIDLineRe matches a record's own top-level `id:` field, quoted with
// either mark or bare, with or without a trailing YAML comment - all valid
// and lint-clean, so a rename must recognize each rather than silently
// missing the field and moving the file without it.
var recordIDLineRe = regexp.MustCompile(`^id:\s*['"]?([a-z][a-z0-9-]*/[a-z][a-z0-9-]*/[a-z][a-z0-9-]+)['"]?\s*(#.*)?$`)

func newLSPCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "lsp",
		Short: "Serve FACET code intelligence over LSP on stdio",
		Long: `Provides hover, go-to-definition, completion, and live diagnostics for
'@facet:' anchors, so the registry is navigable from an editor instead of being
a directory of YAML nobody opens.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return newLSPServer(registrySetForCommand(cmd), version).serve(os.Stdin, os.Stdout)
		},
	}
}

// --- protocol types (only the fields this server uses) ---

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
}

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type location struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

type textDocumentIdentifier struct {
	URI string `json:"uri"`
}

type didOpenParams struct {
	TextDocument struct {
		URI  string `json:"uri"`
		Text string `json:"text"`
	} `json:"textDocument"`
}

type didChangeParams struct {
	TextDocument   textDocumentIdentifier `json:"textDocument"`
	ContentChanges []struct {
		Text string `json:"text"`
	} `json:"contentChanges"`
}

type didSaveParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Text         *string                `json:"text"`
}

type didCloseParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

type positionParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     position               `json:"position"`
}

type initializeParams struct {
	RootURI          *string `json:"rootUri"`
	WorkspaceFolders []struct {
		URI string `json:"uri"`
	} `json:"workspaceFolders"`
}

type markupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type hoverResult struct {
	Contents markupContent `json:"contents"`
}

type diagnostic struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity"`
	Source   string   `json:"source"`
	Message  string   `json:"message"`
}

type publishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []diagnostic `json:"diagnostics"`
}

type completionItem struct {
	Label         string `json:"label"`
	Kind          int    `json:"kind"`
	Detail        string `json:"detail,omitempty"`
	Documentation string `json:"documentation,omitempty"`
}

type referenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

type referenceParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     position               `json:"position"`
	Context      referenceContext       `json:"context"`
}

type renameParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     position               `json:"position"`
	NewName      string                 `json:"newName"`
}

type textEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

// versionedTextDocumentIdentifier's Version is always null: this server does
// not track document versions, and null tells the client not to check one.
type versionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version *int   `json:"version"`
}

type textDocumentEdit struct {
	TextDocument versionedTextDocumentIdentifier `json:"textDocument"`
	Edits        []textEdit                      `json:"edits"`
}

// renameFileOp is a workspace/applyEdit resource operation. Its Kind field is
// what lets a client tell it apart from a textDocumentEdit in the same
// documentChanges list.
type renameFileOp struct {
	Kind   string `json:"kind"`
	OldURI string `json:"oldUri"`
	NewURI string `json:"newUri"`
}

type workspaceEdit struct {
	DocumentChanges []any `json:"documentChanges"`
}

// --- server ---

type lspServer struct {
	version string
	set     facet.RegistrySet

	mu   sync.RWMutex
	docs map[string]string // uri -> current text

	outMu sync.Mutex
	out   io.Writer
}

func newLSPServer(set facet.RegistrySet, versions ...string) *lspServer {
	version := "dev"
	if len(versions) > 0 && versions[0] != "" {
		version = versions[0]
	}
	return &lspServer{set: set, docs: map[string]string{}, version: version}
}

// reg is the registry to speak for when the question is about the project
// rather than about one id: rendering a path, finding the repository root.
func (s *lspServer) reg() facet.Registry { return s.set.Primary() }

// recordPathFor resolves a possibly-qualified id to the record that defines it,
// and to where it would live if it does not exist yet.
func (s *lspServer) recordPathFor(id string) (path string, exists bool) {
	if reg, bare, ok := s.set.Resolve(id); ok {
		return reg.PathForID(bare), true
	}
	name, bare := facet.SplitID(id)
	expected := s.reg()
	for _, r := range s.set {
		if r.Name == name {
			expected = r
		}
	}
	return expected.PathForID(bare), false
}

func (s *lspServer) serve(in io.Reader, out io.Writer) error {
	s.out = out
	r := bufio.NewReader(in)
	for {
		body, err := readMessage(r)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		var msg rpcMessage
		if json.Unmarshal(body, &msg) != nil {
			continue // a malformed frame must not kill the server
		}
		if msg.Method == "exit" {
			return nil
		}
		s.dispatch(msg)
	}
}

func readMessage(r *bufio.Reader) ([]byte, error) {
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
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, fmt.Errorf("bad Content-Length: %w", err)
			}
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("message without Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (s *lspServer) write(msg rpcMessage) {
	msg.JSONRPC = "2.0"
	body, err := json.Marshal(msg)
	if err != nil {
		return
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	fmt.Fprintf(s.out, "Content-Length: %d\r\n\r\n", len(body))
	s.out.Write(body)
}

func (s *lspServer) respond(id json.RawMessage, result any) {
	if id == nil {
		return // a notification takes no response
	}
	s.write(rpcMessage{ID: id, Result: result})
}

func (s *lspServer) notify(method string, params any) {
	raw, err := json.Marshal(params)
	if err != nil {
		return
	}
	s.write(rpcMessage{Method: method, Params: raw})
}

func (s *lspServer) dispatch(msg rpcMessage) {
	switch msg.Method {
	case "initialize":
		s.respond(msg.ID, s.initialize(msg.Params))
	case "initialized", "$/setTrace", "$/cancelRequest":
		// nothing to do
	case "shutdown":
		s.respond(msg.ID, nil)

	case "textDocument/didOpen":
		var p didOpenParams
		if json.Unmarshal(msg.Params, &p) == nil {
			s.setDoc(p.TextDocument.URI, p.TextDocument.Text)
			s.publish(p.TextDocument.URI)
		}
	case "textDocument/didChange":
		var p didChangeParams
		if json.Unmarshal(msg.Params, &p) == nil {
			if n := len(p.ContentChanges); n > 0 {
				s.setDoc(p.TextDocument.URI, p.ContentChanges[n-1].Text)
			}
			s.publish(p.TextDocument.URI)
		}
	case "textDocument/didSave":
		var p didSaveParams
		if json.Unmarshal(msg.Params, &p) == nil {
			if p.Text != nil {
				s.setDoc(p.TextDocument.URI, *p.Text)
			}
			s.publish(p.TextDocument.URI)
		}
	case "textDocument/didClose":
		var p didCloseParams
		if json.Unmarshal(msg.Params, &p) == nil {
			s.mu.Lock()
			delete(s.docs, p.TextDocument.URI)
			s.mu.Unlock()
		}

	case "textDocument/hover":
		s.respond(msg.ID, s.hover(msg.Params))
	case "textDocument/definition":
		s.respond(msg.ID, s.definition(msg.Params))
	case "textDocument/completion":
		s.respond(msg.ID, s.completion(msg.Params))
	case "textDocument/references":
		s.respond(msg.ID, s.references(msg.Params))
	case "textDocument/prepareRename":
		s.respond(msg.ID, s.prepareRename(msg.Params))
	case "textDocument/rename":
		s.respond(msg.ID, s.rename(msg.Params))

	default:
		// An unimplemented request must still be answered, or the client waits.
		s.respond(msg.ID, nil)
	}
}

func (s *lspServer) initialize(raw json.RawMessage) any {
	var p initializeParams
	if json.Unmarshal(raw, &p) == nil {
		// An editor may open the workspace somewhere other than where the
		// binary was started, so prefer the root the client reports.
		switch {
		case len(p.WorkspaceFolders) > 0:
			if path := uriToPath(p.WorkspaceFolders[0].URI); path != "" {
				s.set = facet.OpenSet(path)
			}
		case p.RootURI != nil:
			if path := uriToPath(*p.RootURI); path != "" {
				s.set = facet.OpenSet(path)
			}
		}
	}
	return map[string]any{
		"serverInfo": map[string]any{"name": lspName, "version": s.version},
		"capabilities": map[string]any{
			"textDocumentSync": map[string]any{
				"openClose": true,
				"change":    1, // full document
				"save":      map[string]any{"includeText": true},
			},
			"hoverProvider":      true,
			"definitionProvider": true,
			"completionProvider": map[string]any{
				"triggerCharacters": []string{":", "/", " "},
			},
			"referencesProvider": true,
			"renameProvider":     map[string]any{"prepareProvider": true},
		},
	}
}

func (s *lspServer) setDoc(uri, text string) {
	s.mu.Lock()
	s.docs[uri] = text
	s.mu.Unlock()
}

func (s *lspServer) doc(uri string) (string, bool) {
	s.mu.RLock()
	text, ok := s.docs[uri]
	s.mu.RUnlock()
	if ok {
		return text, true
	}
	// Fall back to disk: hover can be requested for a file the client never sent.
	if p := uriToPath(uri); p != "" {
		if data, err := os.ReadFile(p); err == nil {
			return string(data), true
		}
	}
	return "", false
}

// idAt returns the anchor id on the given line, if the position falls on it.
func (s *lspServer) idAt(uri string, pos position) (string, bool) {
	text, ok := s.doc(uri)
	if !ok {
		return "", false
	}
	lines := strings.Split(text, "\n")
	if pos.Line < 0 || pos.Line >= len(lines) {
		return "", false
	}
	loc := anchorLineRe.FindStringSubmatchIndex(lines[pos.Line])
	if loc == nil {
		return "", false
	}
	// Anywhere in the tag counts, not just the id — being generous here spares
	// the reader precise cursor placement and costs nothing.
	if pos.Character < loc[0] || pos.Character > loc[1] {
		return "", false
	}
	return lines[pos.Line][loc[2]:loc[3]], true
}

func (s *lspServer) hover(raw json.RawMessage) any {
	var p positionParams
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	id, ok := s.idAt(p.TextDocument.URI, p.Position)
	if !ok {
		return nil
	}
	owners := s.set.Owners(id)
	if len(owners) == 0 {
		path, _ := s.recordPathFor(id)
		return hoverResult{Contents: markupContent{
			Kind:  "markdown",
			Value: fmt.Sprintf("**%s** — no record\n\nExpected at `%s`.", id, s.reg().Rel(path)),
		}}
	}

	// Ids are global, so a feature documented by two products has two records.
	// Hovering shows both: seeing "active here, planned there" without leaving
	// the file is the whole reason the id is shared.
	_, bare := facet.SplitID(id)
	var b strings.Builder
	for i, reg := range owners {
		rec, err := facet.LoadRecord(reg.PathForID(bare), "features")
		if err != nil {
			continue
		}
		if i > 0 {
			b.WriteString("\n\n---\n\n")
		}
		label := bare
		if len(s.set) > 1 && reg.Name != "" {
			label = reg.Name + " · " + bare
		}
		fmt.Fprintf(&b, "**%s** · `%s`\n\n", label, rec.Lifecycle)
		if summary := strings.TrimSpace(rec.Intent()); summary != "" {
			b.WriteString(summary)
			b.WriteString("\n\n")
		}
		if n := scenarioCount(rec); n > 0 {
			fmt.Fprintf(&b, "%d contract scenario(s).", n)
		}
		if gaps := rec.KnowledgeGaps(); len(gaps) > 0 {
			fmt.Fprintf(&b, " Knowledge awaiting the owner: %s.", strings.Join(gaps, ", "))
		}
		fmt.Fprintf(&b, "\n\n`%s`", reg.Rel(rec.Path))
	}
	if b.Len() == 0 {
		return nil
	}
	return hoverResult{Contents: markupContent{Kind: "markdown", Value: b.String()}}
}

func (s *lspServer) definition(raw json.RawMessage) any {
	var p positionParams
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	id, ok := s.idAt(p.TextDocument.URI, p.Position)
	if !ok {
		return nil
	}
	// One id, one record per registry documenting it. Returning them all lets a
	// client offer the choice instead of the server picking silently.
	_, bare := facet.SplitID(id)
	var out []location
	for _, reg := range s.set.Owners(id) {
		out = append(out, location{URI: pathToURI(reg.PathForID(bare))}) // top of the record
	}
	if len(out) == 0 {
		return nil
	}
	if len(out) == 1 {
		return out[0]
	}
	return out
}

func (s *lspServer) completion(raw json.RawMessage) any {
	var p positionParams
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	text, ok := s.doc(p.TextDocument.URI)
	if !ok {
		return nil
	}
	lines := strings.Split(text, "\n")
	if p.Position.Line < 0 || p.Position.Line >= len(lines) {
		return nil
	}
	line := lines[p.Position.Line]
	col := p.Position.Character
	if col > len(line) {
		col = len(line)
	}
	if col < 0 || completionTriggerRe.FindStringSubmatch(line[:col]) == nil {
		return nil
	}

	// Every registry contributes, qualified where the name is what makes the
	// suggestion unambiguous — offering a bare id that resolves elsewhere would
	// complete to the wrong feature.
	var items []completionItem
	for _, reg := range s.set {
		records, err := reg.LoadFeatures()
		if err != nil {
			continue
		}
		for _, rec := range records {
			detail := rec.Lifecycle
			if reg.Name != "" {
				detail = reg.Name + " · " + detail
			}
			items = append(items, completionItem{
				Label:         s.set.Qualify(reg, rec.ID),
				Kind:          18, // Reference
				Detail:        detail,
				Documentation: firstLine(rec.Intent()),
			})
		}

		tax, err := facet.LoadTaxonomy(reg.TaxonomyPath())
		if err != nil || !tax.Configured() {
			continue
		}
		for _, domain := range tax.DomainNames() {
			for _, capability := range tax.CapabilityNames(domain) {
				node := tax.Domains[domain].Capabilities[capability]
				detail := "declared capability"
				if reg.Name != "" {
					detail = reg.Name + " · " + detail
				}
				if class := tax.ClassificationOf(domain, capability); class != "" {
					detail += " · " + string(class)
				}
				items = append(items, completionItem{
					Label:         s.set.Qualify(reg, domain+"/"+capability+"/"),
					Kind:          19, // Folder
					Detail:        detail,
					Documentation: firstLine(node.Purpose),
				})
			}
		}
	}
	if items == nil {
		items = []completionItem{}
	}
	return items
}

// anchorOccurrences finds every @facet: anchor across the repository that
// names the given (possibly qualified) id, each with the precise range of
// just the id text (registry prefix, if any, excluded) so a rename can
// replace exactly that.
//
// A qualified id names one registry unambiguously - Owners() would return
// only that registry's record - so only that registry's own qualified
// anchors are the same rename target; touching an unqualified anchor, or one
// qualified to a different registry, would rewrite text that still names a
// distinct record. An unqualified id is deliberately ambiguous (Owners()
// returns every registry documenting it, per the "same id, same feature,
// documented once per product" rule hover() already relies on), so every
// anchor sharing the bare id is in scope regardless of its own qualifier.
//
// It scans the whole repository root rather than any one registry's Sources,
// deliberately: an anchor a rename cannot see is an anchor it would leave
// pointing at an id that no longer exists.
//
// ScanAnchors is used only to find which files are candidates, not for its
// line numbers: those come from disk, and an open buffer with unsaved edits
// that add or remove a line shifts every anchor after it, silently pointing
// a trusted line number at the wrong text. Each candidate file is re-scanned
// against s.doc() - the buffer if the client has one open, disk otherwise -
// so the position returned always matches the content a client-applied edit
// would actually land in.
func (s *lspServer) anchorOccurrences(id string) []location {
	regName, bareID := facet.SplitID(id)
	anchors, err := facet.ScanAnchors(s.reg().Root, facet.ScanOptions{})
	if err != nil {
		return nil
	}
	paths := map[string]bool{}
	for _, a := range anchors {
		if a.ID == bareID {
			paths[a.Path] = true
		}
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)

	var out []location
	for _, path := range sorted {
		text, ok := s.doc(pathToURI(path))
		if !ok {
			continue
		}
		for i, line := range strings.Split(text, "\n") {
			idx := anchorBareIDRe.FindStringSubmatchIndex(line)
			if idx == nil || idx[4] < 0 || line[idx[4]:idx[5]] != bareID {
				continue
			}
			lineReg := ""
			if idx[2] >= 0 {
				lineReg = line[idx[2]:idx[3]]
			}
			if regName != "" && lineReg != regName {
				continue
			}
			out = append(out, location{
				URI: pathToURI(path),
				Range: lspRange{
					Start: position{Line: i, Character: idx[4]},
					End:   position{Line: i, Character: idx[5]},
				},
			})
		}
	}
	return out
}

func (s *lspServer) references(raw json.RawMessage) any {
	var p referenceParams
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	id, ok := s.idAt(p.TextDocument.URI, p.Position)
	if !ok {
		return nil
	}
	out := s.anchorOccurrences(id)
	if p.Context.IncludeDeclaration {
		_, bare := facet.SplitID(id)
		for _, reg := range s.set.Owners(id) {
			out = append(out, location{URI: pathToURI(reg.PathForID(bare))}) // top of the record
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// prepareRename answers whether the cursor sits on a renameable id and, if
// so, exactly which span it covers - the id contains '/' and ':', which a
// client's own word-boundary heuristic would otherwise cut short.
func (s *lspServer) prepareRename(raw json.RawMessage) any {
	var p positionParams
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	text, ok := s.doc(p.TextDocument.URI)
	if !ok {
		return nil
	}
	lines := strings.Split(text, "\n")
	if p.Position.Line < 0 || p.Position.Line >= len(lines) {
		return nil
	}
	idx := anchorBareIDRe.FindStringSubmatchIndex(lines[p.Position.Line])
	if idx == nil || idx[4] < 0 {
		return nil
	}
	start, end := idx[4], idx[5]
	if p.Position.Character < start || p.Position.Character > end {
		return nil // cursor is on the tag but not the id itself - refuse rather than guess
	}
	return map[string]any{
		"range":       lspRange{Start: position{Line: p.Position.Line, Character: start}, End: position{Line: p.Position.Line, Character: end}},
		"placeholder": lines[p.Position.Line][start:end],
	}
}

// rename renames an id everywhere the anchor scan can see it: every anchor
// occurrence, and each owning registry's own declaration - both its `id:`
// field and, since the two are required to agree (the id-path lint check
// exists for exactly this), the file path implied by the new id.
func (s *lspServer) rename(raw json.RawMessage) any {
	var p renameParams
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	if !bareIDPattern.MatchString(p.NewName) {
		return nil
	}
	id, ok := s.idAt(p.TextDocument.URI, p.Position)
	if !ok {
		return nil
	}
	_, bare := facet.SplitID(id)
	if bare == p.NewName {
		return nil
	}

	// Pre-flight, before building a single edit: every owner's id: field must
	// be found, and its new path must be free, or the whole rename is
	// refused. Half a rename - some records moved and fixed, others left
	// with their anchors now pointing at an id they never declared - is
	// worse than doing nothing.
	type ownerChange struct {
		oldPath, newPath string
		idEdit           textEdit
	}
	var ownerChanges []ownerChange
	for _, reg := range s.set.Owners(id) {
		oldPath, newPath := reg.PathForID(bare), reg.PathForID(p.NewName)
		idEdit, found := s.recordIDEdit(oldPath, p.NewName)
		if !found {
			return nil
		}
		if newPath != oldPath {
			if _, err := os.Stat(newPath); err == nil {
				return nil // the new id already has a record - refuse rather than overwrite it
			}
		}
		ownerChanges = append(ownerChanges, ownerChange{oldPath, newPath, idEdit})
	}

	// A WorkspaceEdit may carry at most one TextDocumentEdit per URI, so every
	// edit to the same file - an anchor and the record's own id: field could
	// both live in one, however unusual that is - collects here first.
	editsByURI := map[string][]textEdit{}
	var order []string
	addEdit := func(uri string, e textEdit) {
		if _, seen := editsByURI[uri]; !seen {
			order = append(order, uri)
		}
		editsByURI[uri] = append(editsByURI[uri], e)
	}

	for _, loc := range s.anchorOccurrences(id) {
		addEdit(loc.URI, textEdit{Range: loc.Range, NewText: p.NewName})
	}

	var renames []renameFileOp
	for _, oc := range ownerChanges {
		addEdit(pathToURI(oc.oldPath), oc.idEdit)
		if oc.newPath != oc.oldPath {
			renames = append(renames, renameFileOp{
				Kind: "rename", OldURI: pathToURI(oc.oldPath), NewURI: pathToURI(oc.newPath),
			})
		}
	}
	if len(order) == 0 {
		return nil
	}

	// Edits before renames throughout: every edit above targets a path that
	// has not moved yet, so all content changes must land before any file
	// move does.
	changes := make([]any, 0, len(order)+len(renames))
	for _, uri := range order {
		changes = append(changes, textDocumentEdit{
			TextDocument: versionedTextDocumentIdentifier{URI: uri},
			Edits:        editsByURI[uri],
		})
	}
	for _, r := range renames {
		changes = append(changes, r)
	}
	return workspaceEdit{DocumentChanges: changes}
}

// recordIDEdit finds a record's own top-level `id:` line and returns the edit
// that updates its value, and whether one was found.
func (s *lspServer) recordIDEdit(path, newID string) (textEdit, bool) {
	text, ok := s.doc(pathToURI(path))
	if !ok {
		return textEdit{}, false
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		loc := recordIDLineRe.FindStringSubmatchIndex(line)
		if loc == nil {
			continue
		}
		return textEdit{
			Range: lspRange{
				Start: position{Line: i, Character: loc[2]},
				End:   position{Line: i, Character: loc[3]},
			},
			NewText: newID,
		}, true
	}
	return textEdit{}, false
}

// publish sends diagnostics for one document. Anchors are checked on every
// keystroke, which is the point: an unresolvable id is underlined while it is
// being typed rather than at commit time.
func (s *lspServer) publish(uri string) {
	text, ok := s.doc(uri)
	if !ok {
		return
	}
	path := uriToPath(uri)

	// Never nil: publishing null leaves stale diagnostics on screen in clients
	// that distinguish it from an empty list, so a fixed anchor would stay
	// underlined until the file was closed.
	diagnostics := []diagnostic{}
	switch owner, isRecord := s.registryHolding(path); {
	case isRecord:
		diagnostics = s.recordDiagnostics(owner, path)
	case !isIllustrative(path, text):
		diagnostics = s.anchorDiagnostics(text)
	}

	s.notify("textDocument/publishDiagnostics", publishDiagnosticsParams{
		URI: uri, Diagnostics: diagnostics,
	})
}

func (s *lspServer) anchorDiagnostics(text string) []diagnostic {
	out := []diagnostic{}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		loc := anchorLineRe.FindStringSubmatchIndex(line)
		if loc == nil {
			continue
		}
		id := line[loc[2]:loc[3]]
		rng := lspRange{
			Start: position{Line: i, Character: loc[2]},
			End:   position{Line: i, Character: loc[3]},
		}
		if path, exists := s.recordPathFor(id); !exists {
			out = append(out, diagnostic{
				Range: rng, Severity: sevError, Source: lspName,
				Message: fmt.Sprintf("no record for %q — expected %s", id, s.reg().Rel(path)),
			})
			continue
		}
		// The role tag is documented as the line immediately after the anchor.
		if i+1 >= len(lines) || !lspRoleRe.MatchString(lines[i+1]) {
			out = append(out, diagnostic{
				Range: rng, Severity: sevInformation, Source: lspName,
				Message: "no '@facet-role:' on the following line (primary | related | config | data)",
			})
		}
	}
	return out
}

// registryHolding reports which registry's records directory contains a path.
func (s *lspServer) registryHolding(path string) (facet.Registry, bool) {
	if path == "" {
		return facet.Registry{}, false
	}
	for _, reg := range s.set {
		if strings.HasPrefix(path, reg.RecordsDir+string(filepath.Separator)) {
			return reg, true
		}
	}
	return facet.Registry{}, false
}

// recordDiagnostics reports a record's problems, judged by the registry that
// holds it: another registry's schema would fault every field they do not share.
func (s *lspServer) recordDiagnostics(reg facet.Registry, path string) []diagnostic {
	out := []diagnostic{}
	report, err := facet.Lint(reg, facet.LintOptions{Skip: recordSaveSkips(false)})
	if err != nil {
		return out
	}
	for _, f := range report.Findings {
		if filepath.Join(reg.Root, f.Path) != path {
			continue
		}
		sev := sevError
		switch f.Severity {
		case facet.SeverityWarning:
			sev = sevWarning
		case facet.SeverityInfo:
			sev = sevInformation
		}
		msg := f.Message
		if f.Hint != "" {
			msg += " — " + f.Hint
		}
		out = append(out, diagnostic{Severity: sev, Source: lspName, Message: msg})
	}
	return out
}

// isIllustrative mirrors the scanner: prose and opted-out files show the tag
// syntax rather than using it, so their anchors must not be diagnosed.
func isIllustrative(path, text string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".mdx", ".markdown", ".rst", ".adoc", ".asciidoc", ".txt", ".rtf", ".org":
		return true
	}
	// Literal split so this file does not opt itself out by mentioning it.
	return strings.Contains(text, "@facet-ignore"+"-file")
}

func scenarioCount(rec facet.Record) int {
	contract, ok := rec.Fields["contract"].(map[string]any)
	if !ok {
		return 0
	}
	scenarios, ok := contract["scenarios"].([]any)
	if !ok {
		return 0
	}
	return len(scenarios)
}

func uriToPath(uri string) string {
	if !strings.HasPrefix(uri, "file://") {
		return ""
	}
	p := strings.TrimPrefix(uri, "file://")
	if decoded, err := url.PathUnescape(p); err == nil {
		p = decoded
	}
	return filepath.FromSlash(p)
}

func pathToURI(path string) string {
	return "file://" + filepath.ToSlash(path)
}
