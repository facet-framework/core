package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/facet-framework/core/registry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The glossary's agent surface.
//
// @facet: registry/layout/glossary-tools
// @facet-role: primary

// glossaryToolState owns authoring sessions for one MCP server. A branch is
// scoped to its repository and records directory, never shared across registries.
type glossaryToolState struct {
	mu sync.Mutex
	by map[glossarySessionKey]*glossaryAgentSession
}

// glossarySessionKey names one branch of one registry.
type glossarySessionKey struct {
	root    string
	records string
	branch  string
}

func newGlossaryToolState() *glossaryToolState {
	return &glossaryToolState{by: make(map[glossarySessionKey]*glossaryAgentSession)}
}

// glossaryAgentSession is one open branch.
type glossaryAgentSession struct {
	mu      sync.Mutex
	session *facet.GlossarySession
}

func (state *glossaryToolState) glossarySessionFor(reg facet.Registry, branch string) (*glossaryAgentSession, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	key := glossarySessionKey{root: filepath.Clean(reg.Root), records: filepath.Clean(reg.RecordsDir), branch: strings.TrimSpace(branch)}
	if s, ok := state.by[key]; ok {
		return s, nil
	}
	session, err := facet.OpenGlossaryBranch(reg, branch)
	if err != nil {
		return nil, err
	}
	s := &glossaryAgentSession{session: session}
	state.by[key] = s
	return s, nil
}

// --- tool inputs and outputs -------------------------------------------------

type glossaryListInput struct {
	Registry string `json:"registry,omitempty" jsonschema:"which registry's glossary, in a repository declaring several"`
}

// glossaryGroup is one heading as an agent receives it.
type glossaryGroup struct {
	ID       string               `json:"id"`
	Title    string               `json:"title,omitempty"`
	Purpose  string               `json:"purpose,omitempty"`
	Declared bool                 `json:"declared"`
	Terms    []facet.GlossaryTerm `json:"terms"`
}

type glossaryListOutput struct {
	// Groups is the glossary's grouping, the one Grouped() defines: declared categories in file
	// order including the empty ones, then undeclared ids sorted, then the
	// uncategorised terms, last and unnamed.
	Groups []glossaryGroup `json:"groups"`
	// DeclaredCategories lists the ids glossary_set accepts, in file order,
	// including declared categories that currently have no terms.
	DeclaredCategories []string `json:"declaredCategories"`
	// Terms is every term, in file order, for a caller that wants the list
	// rather than the grouping.
	Terms []facet.GlossaryTerm `json:"terms"`
	Total int                  `json:"total"`
}

type glossaryGetInput struct {
	Term     string `json:"term" jsonschema:"the term exactly as the glossary defines it; matching is case-sensitive"`
	Registry string `json:"registry,omitempty"`
}

// glossaryGetOutput is one term as an agent receives it.
type glossaryGetOutput struct {
	Term             string            `json:"term"`
	Category         string            `json:"category,omitempty"`
	CategoryDeclared bool              `json:"categoryDeclared"`
	CategoryTitle    string            `json:"categoryTitle,omitempty"`
	Definition       string            `json:"definition"`
	Renderings       map[string]string `json:"renderings,omitempty"`
}

type glossarySetInput struct {
	Branch string `json:"branch" jsonschema:"the branch to write on; it is created with its own worktree, and may never be the default branch"`
	Term   string `json:"term" jsonschema:"the term as it is written in the records, exactly; setting one that already exists changes it"`
	// Definition is required on every set call, including changes, so the
	// meaning accompanies category and term edits.
	Definition string `json:"definition" jsonschema:"what the word means in this product, in the product's own language"`
	Category   string `json:"category,omitempty" jsonschema:"the id of a category the glossary already declares; an undeclared one is refused, and leaving it empty means nobody has classified the term"`
	Registry   string `json:"registry,omitempty"`
}

type glossaryWriteOutput struct {
	Term string `json:"term"`
	// Diff contains the proposed change required by FACET §6.4 for caller review.
	Diff   string `json:"diff"`
	Branch string `json:"branch"`
}

type glossaryRenameInput struct {
	Branch   string `json:"branch"`
	From     string `json:"from" jsonschema:"the term as it is defined now"`
	To       string `json:"to" jsonschema:"the new spelling; one the glossary already defines is refused"`
	Registry string `json:"registry,omitempty"`
}

type glossaryRenameOutput struct {
	glossaryWriteOutput
	From string `json:"from"`
	To   string `json:"to"`
	// StillSaying names what still writes the old spelling — record ids and
	// the paths of other project prose a linked product reports, because a
	// consumer may link a term in both. Nothing was
	// done to any of them: the glossary is joined to the prose by content, so
	// the rename did not move those occurrences — it stopped them being
	// occurrences.
	StillSaying []string `json:"stillSaying"`
	Note        string   `json:"note"`
}

type glossaryRemoveInput struct {
	Branch   string `json:"branch"`
	Term     string `json:"term" jsonschema:"the term to undefine; one the glossary does not define is an error"`
	Registry string `json:"registry,omitempty"`
}

// glossaryCommitInput carries no path, as none of these inputs does. The file
// a commit records is the registry's glossary, decided in OpenGlossaryBranch.
type glossaryCommitInput struct {
	Branch   string `json:"branch"`
	Message  string `json:"message" jsonschema:"the commit message"`
	Registry string `json:"registry,omitempty"`
}

type glossaryProposeInput struct {
	Branch   string `json:"branch"`
	Title    string `json:"title" jsonschema:"the pull request's title"`
	Body     string `json:"body,omitempty"`
	Registry string `json:"registry,omitempty"`
}

// --- registration ------------------------------------------------------------

// addGlossaryTools registers the glossary surface on an MCP server.
func addGlossaryTools(server *mcp.Server, set facet.RegistrySet) *glossaryToolState {
	state := newGlossaryToolState()
	mcp.AddTool(server, &mcp.Tool{
		Name:        "glossary_list",
		Description: "List the project's terms of art with the groups they fall into. A term is NOT a record: a definition says what a word means, a record says what the product does and is backed by tests — if you are about to write down a claim about behaviour, it belongs in a record and not here. The grouping comes back in display order: declared categories in file order including empty ones, then any category id nobody declared, then the uncategorised terms, last and unnamed. There is no 'Other' and none is invented.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in glossaryListInput) (*mcp.CallToolResult, glossaryListOutput, error) {
		out, err := glossaryListTool(set, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "glossary_get",
		Description: "Read one term by its exact spelling — matching is case-sensitive, because 'Consignee' and 'consignee' are two different strings and a project may define both. The answer says whether the term's category is one the glossary declares: an undeclared one is a defect in the glossary, and it is a different thing from a term nobody has categorised.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in glossaryGetInput) (*mcp.CallToolResult, glossaryGetOutput, error) {
		out, err := glossaryGetTool(set, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "glossary_set",
		Description: "Define a term, or change the one that is already defined, on a branch — and return the diff rather than a confirmation. Setting a term that exists CHANGES it: two entries of one name never appear, because a word with two definitions has none. REFUSES a category the glossary does not declare, rather than creating it: a typo in a grouping key would otherwise split a category in two with nothing to say so — declare it first. Leaving the category empty means nobody has classified the term, which is a real answer and not a gap to fill. Do NOT put a claim about product behaviour in a definition; that is a record's job.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in glossarySetInput) (*mcp.CallToolResult, glossaryWriteOutput, error) {
		out, err := state.glossarySetTool(set, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "glossary_rename",
		Description: "Change a term's spelling. This is the delicate one: nothing in a record references the glossary — a term is matched by every exact occurrence of its spelling in the prose — so a rename moves no reference. It stops every occurrence of the old spelling being a term. The answer therefore names what still says the old word — records AND any other project prose this build reads, because a term is matched in both; correcting them is separate work and nothing is touched here. A new spelling the glossary already defines is refused rather than merged.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in glossaryRenameInput) (*mcp.CallToolResult, glossaryRenameOutput, error) {
		out, err := state.glossaryRenameTool(set, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "glossary_remove",
		Description: "Undefine a term on a branch, returning the diff. A term the glossary does not define is an ERROR rather than a silent success: told the removal worked, you would report a word as undefined that is still being read as a term everywhere it appears. The category the term named stays declared — a category is a decision about how the project groups its words, not a container that empties.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in glossaryRemoveInput) (*mcp.CallToolResult, glossaryWriteOutput, error) {
		out, err := state.glossaryRemoveTool(set, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "glossary_commit",
		Description: "Commit the glossary change written on this branch. There is no parameter naming what to commit and there cannot be one: the file is the registry's own glossary, so nothing else in the worktree can be staged. A branch nothing was written on is an ERROR rather than an empty commit — you believe you wrote something, and an empty commit would agree with you. The working tree you are coding in is never touched: all of this happens in a separate worktree under .git/.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in glossaryCommitInput) (*mcp.CallToolResult, facet.CommitResult, error) {
		out, err := state.glossaryCommitTool(set, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "glossary_propose",
		Description: "Push the branch and open a pull request for it. This is the only way a glossary change reaches anybody: no tool pushes to the default branch and none merges — a person merges, which is what makes a naming decision something agreed rather than something done. If no forge is declared for the remote's host, or a forge CLI is missing, the branch is still pushed and the answer says what is left to do by hand.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in glossaryProposeInput) (*mcp.CallToolResult, glossaryProposeOutput, error) {
		out, err := state.glossaryProposeTool(set, in)
		return nil, out, err
	})
	return state
}

// --- implementations ---------------------------------------------------------

// glossaryRegistryFor picks which registry's glossary a call means.
func glossaryRegistryFor(set facet.RegistrySet, name string) (facet.Registry, error) {
	if name == "" {
		return set.Primary(), nil
	}
	for _, reg := range set {
		if reg.Name == name {
			return reg, nil
		}
	}
	return facet.Registry{}, fmt.Errorf("no registry named %q", name)
}

// glossaryProposeOutput is what a proposal did.
type glossaryProposeOutput struct {
	Branch      string `json:"branch"`
	Pushed      bool   `json:"pushed"`
	PullRequest bool   `json:"pullRequest"`
	URL         string `json:"url,omitempty"`
	// Message says what happened when something did not. A forge this host
	// cannot open a pull request on, or a push the remote refused, loses the
	// last step and nothing else — and is said rather than swallowed.
	Message string `json:"message,omitempty"`
}

// loadGlossaryFor reads the glossary as the working tree has it, for the
// reading tools. The writing tools read the copy on their branch instead.
func loadGlossaryFor(set facet.RegistrySet, registry string) (*facet.Glossary, facet.Registry, error) {
	reg, err := glossaryRegistryFor(set, registry)
	if err != nil {
		return nil, facet.Registry{}, err
	}
	g, err := facet.LoadGlossary(reg.GlossaryPath())
	if err != nil {
		return nil, facet.Registry{}, err
	}
	return g, reg, nil
}

func glossaryListTool(set facet.RegistrySet, in glossaryListInput) (glossaryListOutput, error) {
	g, _, err := loadGlossaryFor(set, in.Registry)
	if err != nil {
		return glossaryListOutput{}, err
	}

	out := glossaryListOutput{
		Groups:             []glossaryGroup{},
		DeclaredCategories: g.CategoryIDs(),
		Terms:              g.Terms,
		Total:              len(g.Terms),
	}
	if out.DeclaredCategories == nil {
		out.DeclaredCategories = []string{}
	}
	if out.Terms == nil {
		out.Terms = []facet.GlossaryTerm{}
	}
	// Reuse Grouped() so tool output follows the canonical glossary grouping rule.
	for _, group := range g.Grouped() {
		terms := group.Terms
		if terms == nil {
			terms = []facet.GlossaryTerm{}
		}
		out.Groups = append(out.Groups, glossaryGroup{
			ID:       group.ID,
			Title:    group.Title,
			Purpose:  group.Purpose,
			Declared: group.Declared,
			Terms:    terms,
		})
	}
	return out, nil
}

func glossaryGetTool(set facet.RegistrySet, in glossaryGetInput) (glossaryGetOutput, error) {
	g, _, err := loadGlossaryFor(set, in.Registry)
	if err != nil {
		return glossaryGetOutput{}, err
	}
	term := strings.TrimSpace(in.Term)
	for _, entry := range g.Terms {
		if strings.TrimSpace(entry.Term) != term {
			continue
		}
		out := glossaryGetOutput{
			Term:       entry.Term,
			Category:   entry.Category,
			Definition: entry.Definition,
			Renderings: entry.Renderings,
		}
		if entry.Category != "" {
			out.CategoryDeclared = g.CategoryDeclared(entry.Category)
			for _, c := range g.Categories {
				if c.ID == entry.Category {
					out.CategoryTitle = c.Title
					break
				}
			}
		}
		return out, nil
	}
	// Exact matching preserves defined terminology; near misses return an error
	// rather than selecting a different term.
	return glossaryGetOutput{}, fmt.Errorf("the glossary does not define %q — use glossary_list to see what it defines", in.Term)
}

func (state *glossaryToolState) glossarySetTool(set facet.RegistrySet, in glossarySetInput) (glossaryWriteOutput, error) {
	reg, err := glossaryRegistryFor(set, in.Registry)
	if err != nil {
		return glossaryWriteOutput{}, err
	}
	session, err := state.glossarySessionFor(reg, in.Branch)
	if err != nil {
		return glossaryWriteOutput{}, err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	edited, err := facet.SetGlossaryTerm(session.session.GlossaryPath, in.Term, in.Definition, in.Category)
	if err != nil {
		return glossaryWriteOutput{}, err
	}
	return glossaryWriteOutput{
		Term:   strings.TrimSpace(in.Term),
		Diff:   edited.Diff,
		Branch: session.session.Branch,
	}, nil
}

func (state *glossaryToolState) glossaryRenameTool(set facet.RegistrySet, in glossaryRenameInput) (glossaryRenameOutput, error) {
	reg, err := glossaryRegistryFor(set, in.Registry)
	if err != nil {
		return glossaryRenameOutput{}, err
	}
	session, err := state.glossarySessionFor(reg, in.Branch)
	if err != nil {
		return glossaryRenameOutput{}, err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	// The registry rather than the session: the records are read where they
	// live, and the branch holds no change to them — nor will it.
	renamed, err := facet.RenameGlossaryTerm(reg, session.session.GlossaryPath, in.From, in.To)
	if err != nil {
		return glossaryRenameOutput{}, err
	}

	still := renamed.StillSaying
	if still == nil {
		still = []string{}
	}
	out := glossaryRenameOutput{
		glossaryWriteOutput: glossaryWriteOutput{
			Term:   renamed.To,
			Diff:   renamed.Diff,
			Branch: session.session.Branch,
		},
		From:        renamed.From,
		To:          renamed.To,
		StillSaying: still,
	}
	if len(still) > 0 {
		out.Note = fmt.Sprintf("%d record(s) and page(s) still write %q in their prose; those occurrences are no longer a defined term and nothing here changed them",
			len(still), renamed.From)
	} else {
		// An explicit note confirms that the prose search completed without matches;
		// an empty array alone does not report whether a search ran.
		out.Note = fmt.Sprintf("no record's and no page's prose writes %q", renamed.From)
	}
	return out, nil
}

func (state *glossaryToolState) glossaryRemoveTool(set facet.RegistrySet, in glossaryRemoveInput) (glossaryWriteOutput, error) {
	reg, err := glossaryRegistryFor(set, in.Registry)
	if err != nil {
		return glossaryWriteOutput{}, err
	}
	session, err := state.glossarySessionFor(reg, in.Branch)
	if err != nil {
		return glossaryWriteOutput{}, err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	edited, err := facet.RemoveGlossaryTerm(session.session.GlossaryPath, in.Term)
	if err != nil {
		return glossaryWriteOutput{}, err
	}
	return glossaryWriteOutput{
		Term:   strings.TrimSpace(in.Term),
		Diff:   edited.Diff,
		Branch: session.session.Branch,
	}, nil
}

func (state *glossaryToolState) glossaryCommitTool(set facet.RegistrySet, in glossaryCommitInput) (facet.CommitResult, error) {
	reg, err := glossaryRegistryFor(set, in.Registry)
	if err != nil {
		return facet.CommitResult{}, err
	}
	session, err := state.glossarySessionFor(reg, in.Branch)
	if err != nil {
		return facet.CommitResult{}, err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	// No path passed, because there is none to pass. The session knows the one
	// file it may touch, and that is the same fact the write tools rest on.
	return session.session.Commit(in.Message)
}

func (state *glossaryToolState) glossaryProposeTool(set facet.RegistrySet, in glossaryProposeInput) (glossaryProposeOutput, error) {
	reg, err := glossaryRegistryFor(set, in.Registry)
	if err != nil {
		return glossaryProposeOutput{}, err
	}
	session, err := state.glossarySessionFor(reg, in.Branch)
	if err != nil {
		return glossaryProposeOutput{}, err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	// A push that the remote refused and a forge this host cannot place are
	// the two outcomes every proposal reports, in the same words.
	out := glossaryProposeOutput{Branch: session.session.Branch}
	if err := session.session.Push(""); err != nil {
		// Never rolled back. The commit is somebody's naming decision, and
		// undoing it because a remote refused would throw that decision away.
		out.Message = err.Error() + " — the branch and the commit are here; push them by hand"
		return out, nil
	}
	out.Pushed = true

	forge := facet.ForgeForPush(session.session.Root, reg.Root)
	url, err := facet.OpenPullRequest(session.session.Dir, session.session.Branch, forge, in.Title, in.Body)
	if err != nil {
		out.Message = err.Error()
		return out, nil
	}
	out.PullRequest, out.URL = true, url
	return out, nil
}
