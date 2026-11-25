package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/facet-framework/core/rag"
	"github.com/facet-framework/core/registry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

// @facet: navigation/interfaces/mcp-server
// @facet-role: primary
func newMCPCmd(version string) *cobra.Command {
	var approvedOrigin string
	var keyEnv string
	// The server starts under an unmet pin and answers every tool call with
	// it instead (corePinMCPMiddleware), so the agent sees why, not "failed".
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve the registry over MCP on stdio",
		Long: `Exposes the FACET registry to an agent as MCP tools and resources.

Tools: feature_find, feature_get, read_facet, anchor_resolve, anchor_list, rag_query,
registry_validate, source_view_get. Installed products add tools under their own
namespace (<product>__<tool>). rag_query needs an index from 'facet rag index'.
Every feature record is also exposed as a readable resource.` + mcpToolsetHelp(),
		RunE: func(cmd *cobra.Command, args []string) error {
			authority := embeddingRemoteOptions(rag.QueryEgress, approvedOrigin, keyEnv)
			server, stop := newMCPServerWithPluginsAndRAG(registrySetForCommand(cmd), PluginsDir(), authority, version)
			defer stop()
			server.AddReceivingMiddleware(corePinMCPMiddleware(ResolveRoot(cmd), version))

			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			return server.Run(ctx, &mcp.StdioTransport{})
		},
	}
	cmd.Flags().StringVar(&approvedOrigin, "allow-query-egress", "", "permit sending MCP query text to this exact HTTPS origin")
	cmd.Flags().StringVar(&keyEnv, "embed-key-env", "", "invocation-selected environment variable holding the embedding API key")
	return markPinExempt(cmd, pinExemptReports)
}

type findInput struct {
	Query string `json:"query" jsonschema:"text matched against feature id and intent summary"`
	Mode  string `json:"mode,omitempty" jsonschema:"lexical (default), by_owner, by_lifecycle, or by_anchor_path"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum results, default 25"`
}

type findResult struct {
	Headline string `json:"headline"`
	ID       string `json:"id"`
}

type findOutput struct {
	Matches []findResult `json:"matches"`
	Total   int          `json:"total"`
}

type getInput struct {
	ID     string   `json:"id" jsonschema:"exact feature id, three segments: domain/capability/feature. Ids are global across a monorepo's registries: the bare form returns every registry's record for that feature, and a registry:domain/capability/feature prefix narrows it to one"`
	Facets []string `json:"facets,omitempty" jsonschema:"limit the response to these top-level facets"`
}

// recordView is one registry's answer for a feature id.
type recordView struct {
	Registry      string         `json:"registry,omitempty"`
	Path          string         `json:"path"`
	SchemaVersion string         `json:"schema_version"`
	Lifecycle     string         `json:"lifecycle"`
	Record        map[string]any `json:"record"`
	KnowledgeGaps []string       `json:"knowledge_gaps,omitempty"`
	// Tickets lists the tracker issues the record names, each resolved
	// against the project's 'trackers:' so an agent can follow it to the why
	// a specification does not carry (FACET §3.7.1). Unresolved keys keep
	// their key and get no invented link.
	Tickets []ticketView `json:"tickets,omitempty"`
}

// ticketView is one ticket as an agent receives it: which field named it,
// for which scenario, in which relation, and where it lives.
type ticketView struct {
	Key       string `json:"key"`
	Source    string `json:"source"`
	Scenario  string `json:"scenario,omitempty"`
	Relation  string `json:"relation"`
	Tracker   string `json:"tracker,omitempty"`
	URL       string `json:"url,omitempty"`
	ByDefault bool   `json:"by_default,omitempty"`
}

// recordTickets collects the tickets a record names on lineage and on its
// scenarios. Read from the full record, so a caller narrowing the facets
// still gets them.
func recordTickets(fields map[string]any, trackers []facet.Tracker) []ticketView {
	var out []ticketView
	add := func(raw any, source, scenario string) {
		list, _ := raw.([]any)
		for _, item := range list {
			key, relation, named := "", "relates", ""
			switch v := item.(type) {
			case string:
				key = v
			case map[string]any:
				key, _ = v["key"].(string)
				if r, ok := v["relation"].(string); ok && r != "" {
					relation = r
				}
				named, _ = v["tracker"].(string)
			}
			if key == "" {
				continue
			}
			r := facet.ResolveTicket(trackers, key, named)
			out = append(out, ticketView{Key: key, Source: source, Scenario: scenario, Relation: relation,
				Tracker: r.Tracker, URL: r.URL, ByDefault: r.ByDefault})
		}
	}
	if lineage, ok := fields["lineage"].(map[string]any); ok {
		add(lineage["tickets"], "lineage.tickets", "")
	}
	if contract, ok := fields["contract"].(map[string]any); ok {
		scenarios, _ := contract["scenarios"].([]any)
		for _, raw := range scenarios {
			sc, _ := raw.(map[string]any)
			id, _ := sc["id"].(string)
			add(sc["tickets"], "contract.scenarios.tickets", id)
		}
	}
	return out
}

// getOutput always carries a list, even for the single record a project with
// one registry can ever return.
type getOutput struct {
	ID      string       `json:"id"`
	Records []recordView `json:"records"`
}

type readFacetInput struct {
	ID       string   `json:"id" jsonschema:"exact feature id, optionally prefixed with its registry as registry:domain/capability/feature"`
	Sections []string `json:"sections,omitempty" jsonschema:"section names to read, e.g. contract, verification, knowledge or a child such as contract.scenarios; omit to list the sections with their sizes"`
}

type readFacetSection struct {
	Name  string `json:"name"`
	Bytes int    `json:"bytes"`
	Lines int    `json:"lines"`
}

type readFacetOutput struct {
	ID       string             `json:"id"`
	Path     string             `json:"path"`
	Bytes    int                `json:"bytes"`
	Sections []readFacetSection `json:"sections"`
	Text     string             `json:"text,omitempty"`
}

type resolveInput struct {
	Path string `json:"path" jsonschema:"repository-relative path to a source file"`
}

type resolveOutput struct {
	Features []string `json:"features"`
}

type anchorListInput struct {
	ID string `json:"id" jsonschema:"exact feature id, optionally prefixed with its registry as registry:domain/capability/feature"`
}

type anchorRef struct {
	Registry string `json:"registry,omitempty"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Role     string `json:"role,omitempty"`
}

type anchorListOutput struct {
	Anchors []anchorRef `json:"anchors"`
}

type validateInput struct{}

type validateOutput struct {
	Findings []facet.Finding `json:"findings"`
	Passed   bool            `json:"passed"`
}

func buildMCPServer(set facet.RegistrySet, versions ...string) *mcp.Server {
	return buildMCPServerWithRAG(set, rag.RemoteOptions{Egress: rag.QueryEgress}, versions...)
}

func buildMCPServerWithRAG(set facet.RegistrySet, authority rag.RemoteOptions, versions ...string) *mcp.Server {
	version := "dev"
	if len(versions) > 0 && versions[0] != "" {
		version = versions[0]
	}
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "facet",
		Title:   "FACET registry",
		Version: version,
	}, nil)

	addSourceViewTool(server, set)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "feature_find",
		Description: "Search the feature registry. Returns ranked {id, headline} pairs; use feature_get to retrieve a record. Prefix the query with 'registry:' to search one registry of a monorepo.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in findInput) (*mcp.CallToolResult, findOutput, error) {
		out, err := featureFind(set, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "feature_get",
		Description: "Retrieve a feature by exact id. Always returns a list of records: ids are global, so a feature documented independently by two registries of a monorepo returns one entry each, which is how you compare them. Prefix the id with 'registry:' for just one. Unknown ids are an error — use feature_find to discover them.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getInput) (*mcp.CallToolResult, getOutput, error) {
		out, err := featureGet(set, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "read_facet",
		Description: "Read part of a large feature record instead of the whole file. Without sections it returns the list of sections with their sizes; with sections it returns exactly those, as the file holds them, comments included. Use it when a record is too large to read whole — the Read hook points here — and feature_get when you want parsed fields.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in readFacetInput) (*mcp.CallToolResult, readFacetOutput, error) {
		out, err := readFacet(set, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "anchor_resolve",
		Description: "Resolve a source file to the feature ids anchored in it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in resolveInput) (*mcp.CallToolResult, resolveOutput, error) {
		out, err := anchorResolve(set, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "anchor_list",
		Description: "List every source anchor for one feature, with file, line, and role.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in anchorListInput) (*mcp.CallToolResult, anchorListOutput, error) {
		out, err := anchorList(set, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "rag_query",
		Description: "Search the registry by meaning rather than by id. Returns the record facets and generated documentation closest to a natural-language question, each with the file it came from and a similarity score. Use it when the words in the question match no id and no path — a convention, a relationship between features, anything whose answer is in prose. Use feature_get when you already know the id. Requires an index built by 'facet rag index'.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ragQueryInput) (*mcp.CallToolResult, ragQueryOutput, error) {
		out, err := ragQuery(ctx, set, in, authority)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "registry_validate",
		Description: "Run the registry's integrity checks and return the findings.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ validateInput) (*mcp.CallToolResult, validateOutput, error) {
		scan := facet.ScanOptionsFromEnv()
		scan.View = set.Primary().SourceView
		report, err := facet.LintAll(set.Primary().Root, facet.LintOptions{Scan: scan})
		if err != nil {
			return nil, validateOutput{}, err
		}
		return nil, validateOutput{Findings: report.Findings, Passed: !report.Failed()}, nil
	})

	addRegisteredMCPTools(server, set)
	addGlossaryTools(server, set)
	addRecordResources(server, set)
	addProductMCPTools(server, set)
	return server
}

// addRecordResources exposes each record as an MCP resource, so a client can
// browse the registry rather than only query it through tools.
func addRecordResources(server *mcp.Server, set facet.RegistrySet) {
	for _, reg := range set {
		addRegistryResources(server, set, reg)
	}
}

func addRegistryResources(server *mcp.Server, set facet.RegistrySet, reg facet.Registry) {
	records, err := reg.LoadFeatures()
	if err != nil {
		return
	}
	for _, rec := range records {
		path := rec.Path
		headline := rec.ID
		if summary := firstLine(rec.Intent()); summary != "" {
			headline = summary
		}
		// The URI carries the registry so two registries holding the same id
		// are two resources rather than one silently shadowing the other.
		name := set.Qualify(reg, rec.ID)
		server.AddResource(&mcp.Resource{
			URI:         "facet://features/" + name,
			Name:        name,
			Description: headline,
			MIMEType:    "application/yaml",
		}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			data, err := readFileString(path)
			if err != nil {
				return nil, err
			}
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{
					URI:      req.Params.URI,
					MIMEType: "application/yaml",
					Text:     data,
				}},
			}, nil
		})
	}
}

func featureFind(set facet.RegistrySet, in findInput) (findOutput, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 25
	}
	q := strings.ToLower(strings.TrimSpace(in.Query))

	// A query may name the registry it means; in a monorepo that is how a
	// caller asks "what does the admin app do" without wading through the rest.
	wanted, q := facet.SplitID(q)

	out := findOutput{Matches: []findResult{}}
	for _, reg := range set {
		if wanted != "" && reg.Name != wanted {
			continue
		}
		records, err := reg.LoadFeatures()
		if err != nil {
			return findOutput{}, err
		}

		var anchorsByID map[string][]facet.Anchor
		if in.Mode == "by_anchor_path" {
			scan := facet.ScanOptionsFromEnv()
			if len(reg.Sources) > 0 || reg.SourceView != nil {
				scan = reg.ScanOptions()
			}
			anchors, err := facet.ScanAnchors(reg.Root, scan)
			if err != nil {
				return findOutput{}, err
			}
			anchorsByID = facet.AnchorsByID(anchors)
		}

		for _, rec := range records {
			if !matchesQuery(reg, rec, q, in.Mode, anchorsByID) {
				continue
			}
			out.Total++
			if len(out.Matches) < limit {
				out.Matches = append(out.Matches, findResult{
					ID:       set.Qualify(reg, rec.ID),
					Headline: fmt.Sprintf("[%s] %s", rec.Lifecycle, firstLine(rec.Intent())),
				})
			}
		}
	}
	return out, nil
}

func matchesQuery(reg facet.Registry, rec facet.Record, q, mode string, anchors map[string][]facet.Anchor) bool {
	if q == "" {
		return true
	}
	switch mode {
	case "by_owner":
		return strings.Contains(strings.ToLower(ownerOf(rec)), q)
	case "by_lifecycle":
		return strings.EqualFold(rec.Lifecycle, q)
	case "by_anchor_path":
		for _, a := range anchors[rec.ID] {
			if strings.Contains(strings.ToLower(mcpAnchorPath(reg, a)), q) {
				return true
			}
		}
		return false
	default:
		return strings.Contains(strings.ToLower(rec.ID), q) ||
			strings.Contains(strings.ToLower(rec.Intent()), q)
	}
}

func ownerOf(rec facet.Record) string {
	lineage, ok := rec.Fields["lineage"].(map[string]any)
	if !ok {
		return ""
	}
	owner, ok := lineage["owner"].(map[string]any)
	if !ok {
		return ""
	}
	team, _ := owner["team"].(string)
	return team
}

func featureGet(set facet.RegistrySet, in getInput) (getOutput, error) {
	// §6.3: exact matching only. A near-miss is an error with a pointer to
	// feature_find, never a guess at what the caller meant.
	owners := set.Owners(in.ID)
	_, id := facet.SplitID(in.ID)
	if len(owners) == 0 {
		expected := set.Primary()
		return getOutput{}, fmt.Errorf("unknown feature id %q: no record at %s — use feature_find to discover ids",
			in.ID, expected.Rel(expected.PathForID(id)))
	}

	out := getOutput{ID: id, Records: []recordView{}}
	// Trackers are project-wide; a configuration that cannot be read leaves
	// tickets unresolved rather than failing the read of the record.
	var trackers []facet.Tracker
	if cfg, err := facet.LoadConfig(set.Primary().Root); err == nil {
		trackers = cfg.Trackers()
	}
	for _, reg := range owners {
		rec, err := facet.LoadRecord(reg.PathForID(id), "features")
		if err != nil {
			return getOutput{}, err
		}
		fields := rec.Fields
		tickets := recordTickets(fields, trackers)

		if fields == nil {
			fields = map[string]any{}
		}
		if len(in.Facets) > 0 {
			filtered := make(map[string]any, len(in.Facets))
			for _, f := range in.Facets {
				if v, ok := fields[f]; ok {
					filtered[f] = v
				}
			}
			fields = filtered
		}
		out.Records = append(out.Records, recordView{
			Registry:      reg.Name,
			Path:          reg.Rel(rec.Path),
			SchemaVersion: rec.SchemaVersion,
			Lifecycle:     rec.Lifecycle,
			Record:        fields,
			KnowledgeGaps: rec.KnowledgeGaps(),
			Tickets:       tickets,
		})
	}
	return out, nil
}

func readFacet(set facet.RegistrySet, in readFacetInput) (readFacetOutput, error) {
	owners := set.Owners(in.ID)
	_, id := facet.SplitID(in.ID)
	if len(owners) == 0 {
		expected := set.Primary()
		return readFacetOutput{}, fmt.Errorf("unknown feature id %q: no record at %s — use feature_find to discover ids",
			in.ID, expected.Rel(expected.PathForID(id)))
	}
	// Ids are global, so a bare id held by two registries is ambiguous for a
	// tool that returns text; answering with the first would be a quiet guess.
	if len(owners) > 1 {
		return readFacetOutput{}, fmt.Errorf("%q exists in %d registries — prefix it with one, as registry:%s", in.ID, len(owners), id)
	}
	reg := owners[0]
	path := reg.PathForID(id)
	raw, err := readFileString(path)
	if err != nil {
		return readFacetOutput{}, err
	}
	out := readFacetOutput{ID: id, Path: reg.Rel(path), Bytes: len(raw), Sections: []readFacetSection{}}
	for _, s := range facet.SectionOutline(raw) {
		out.Sections = append(out.Sections, readFacetSection{Name: s.Name, Bytes: s.Bytes, Lines: s.Lines})
	}
	if len(in.Sections) > 0 {
		if out.Text, err = facet.ReadSections(raw, in.Sections); err != nil {
			return readFacetOutput{}, err
		}
	}
	return out, nil
}

func anchorResolve(set facet.RegistrySet, in resolveInput) (resolveOutput, error) {
	root := set.Primary().Root
	path := in.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	var anchors []facet.Anchor
	var err error
	if view := set.Primary().SourceView; view != nil {
		if err = view.Validate(); err != nil {
			return resolveOutput{}, err
		}
		src, relative, resolveErr := view.ResolveReference(in.Path)
		if resolveErr != nil {
			return resolveOutput{}, resolveErr
		}
		raw, readErr := src.Read(relative)
		if readErr != nil {
			return resolveOutput{}, readErr
		}
		anchors = facet.ScanContent(filepath.Join(src.Root, filepath.FromSlash(relative)), raw)
		if err = view.Validate(); err != nil {
			return resolveOutput{}, err
		}
	} else {
		anchors, err = facet.ScanFile(path)
	}
	if err != nil {
		return resolveOutput{}, fmt.Errorf("cannot read %s: %w", in.Path, err)
	}
	seen := map[string]bool{}
	out := resolveOutput{Features: []string{}}
	for _, a := range anchors {
		// The id is reported as the caller must address it: a tag that names
		// its registry keeps the name, and an unqualified tag in a monorepo
		// gains the name of whichever registry turns out to own it.
		name := a.Qualified()
		if a.Registry == "" {
			if reg, id, ok := set.Resolve(a.ID); ok {
				name = set.Qualify(reg, id)
			}
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out.Features = append(out.Features, name)
	}
	sort.Strings(out.Features)
	return out, nil
}

func anchorList(set facet.RegistrySet, in anchorListInput) (anchorListOutput, error) {
	owners := set.Owners(in.ID)
	_, id := facet.SplitID(in.ID)
	if len(owners) == 0 {
		return anchorListOutput{}, fmt.Errorf("unknown feature id %q — use feature_find to discover ids", in.ID)
	}
	// One id documented by two registries has anchors in both, and which
	// registry each sits in is the answer to "where does this feature live over
	// there" — so they are labelled rather than merged.
	out := anchorListOutput{Anchors: []anchorRef{}}
	seen := map[string]bool{}
	for _, reg := range owners {
		scan := facet.ScanOptionsFromEnv()
		if len(reg.Sources) > 0 || reg.SourceView != nil {
			scan = reg.ScanOptions()
		}
		anchors, err := facet.ScanAnchors(reg.Root, scan)
		if err != nil {
			return anchorListOutput{}, err
		}
		for _, a := range facet.AnchorsByID(anchors)[id] {
			if !a.BelongsTo(reg.Name) {
				continue
			}
			rel := mcpAnchorPath(reg, a)
			key := reg.Name + "\x00" + rel + "\x00" + fmt.Sprint(a.Line)
			if seen[key] {
				continue
			}
			seen[key] = true
			out.Anchors = append(out.Anchors, anchorRef{
				Registry: reg.Name, Path: rel, Line: a.Line, Role: a.Role,
			})
		}
	}
	return out, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func readFileString(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func mcpAnchorPath(reg facet.Registry, a facet.Anchor) string {
	if a.Reference != "" {
		return a.Reference
	}
	return reg.Rel(a.Path)
}
