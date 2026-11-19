package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/facet-framework/core/rag"
	"github.com/facet-framework/core/registry"
	"github.com/spf13/cobra"
)

// newRAGCmd is retrieval over the registry: build an index, ask it a question.
//
// @facet: navigation/retrieval/registry-index
// @facet-role: primary
func newRAGCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rag",
		Short: "Index the registry for retrieval, and search it",
		Long: `Turns records and generated documentation into a vector index, and searches it.

'rag index' embeds every record facet and every generated article the manifest
claims, and writes one file next to generated.lock.yaml.
'rag query' embeds a question and returns the nearest chunks with their scores.

The embedding model is an HTTP call to an endpoint you configure — facet runs
no model and hosts no server. Any OpenAI-compatible /embeddings endpoint works,
including a local Ollama or LM Studio. With nothing configured, a lexical
embedder built into this binary answers, so both commands work offline.`,
	}
	cmd.AddCommand(newRAGIndexCmd(), newRAGQueryCmd())
	return cmd
}

// embedFlags are the "or via a parameter" half of the configuration.
type embedFlags struct {
	provider string
	baseURL  string
	model    string
	keyEnv   string
	dims     int
	batch    int
	index    string
}

func (f *embedFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.provider, "embed-provider", "", `"openai" for any OpenAI-compatible endpoint, or "local" (default: from `+facet.ConfigName+`)`)
	cmd.Flags().StringVar(&f.baseURL, "embed-url", "", "embeddings API root, without /embeddings")
	cmd.Flags().StringVar(&f.model, "embed-model", "", "embedding model name")
	cmd.Flags().StringVar(&f.keyEnv, "embed-key-env", "", "environment variable holding the API key (never the key itself)")
	cmd.Flags().IntVar(&f.dims, "embed-dimensions", 0, "shorten vectors, where the provider supports it")
	cmd.Flags().IntVar(&f.batch, "embed-batch", 0, fmt.Sprintf("chunks per request (default %d)", rag.DefaultBatch))
	cmd.Flags().StringVar(&f.index, "index-path", "", "where the index lives (default: "+facet.DefaultRecordsDir+"/"+facet.RAGIndexName+")")
}

// settings merges the three sources, in the order a reader would expect: the
// committed file, then the environment, then this run's flags.
func (f *embedFlags) settings(cfg *facet.Config) facet.EmbeddingConfig {
	e := cfg.EmbeddingSettings()
	if f.provider != "" {
		e.Provider = f.provider
	}
	if f.baseURL != "" {
		e.BaseURL = f.baseURL
	}
	if f.model != "" {
		e.Model = f.model
	}
	if f.keyEnv != "" {
		e.APIKeyEnv = f.keyEnv
	}
	if f.dims != 0 {
		e.Dimensions = f.dims
	}
	if f.batch != 0 {
		e.Batch = f.batch
	}
	if f.index != "" {
		e.Index = f.index
	}
	return e
}

// ragContext is everything both subcommands need to resolve first.
type ragContext struct {
	registries []facet.Registry
	embedder   rag.Embedder
	indexPath  string
}

func (f *embedFlags) resolve(cmd *cobra.Command) (*ragContext, error) {
	cfg, err := configForCommand(cmd)
	if err != nil {
		return nil, err
	}
	registries := cfg.All()

	settings := f.settings(cfg)
	embedder, err := rag.Resolve(settings)
	if err != nil {
		return nil, err
	}

	path := registries[0].RAGIndexPath()
	if settings.Index != "" {
		cfg.Embedding.Index = settings.Index
		path = cfg.IndexPath(registries[0])
	}
	return &ragContext{registries: registries, embedder: embedder, indexPath: path}, nil
}

func newRAGIndexCmd() *cobra.Command {
	var flags embedFlags
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "index",
		Short: "Embed the records and generated documents into a vector index",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := flags.resolve(cmd)
			if err != nil {
				return err
			}

			start := time.Now()
			// Progress on stderr, so `facet rag index --json > file` stays a
			// clean document and a person still sees the run move.
			report := func(phase string, done, total int) {
				if asJSON {
					return
				}
				if total > 0 {
					fmt.Fprintf(cmd.ErrOrStderr(), "\r%s %d/%d", phase, done, total)
					return
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "\r%s %d", phase, done)
			}

			store, err := rag.Build(cmd.Context(), ctx.registries, ctx.embedder, report)
			if !asJSON {
				fmt.Fprint(cmd.ErrOrStderr(), "\r\033[K")
			}
			if err != nil {
				return err
			}
			if err := store.Save(ctx.indexPath); err != nil {
				return err
			}

			rel := ctx.registries[0].Rel(ctx.indexPath)
			if asJSON {
				return encode(cmd.OutOrStdout(), map[string]any{
					"path":     rel,
					"chunks":   store.Len(),
					"dim":      store.Dim,
					"provider": store.Provider,
					"model":    store.Model,
					"builtAt":  store.BuiltAt,
					"tookMs":   time.Since(start).Milliseconds(),
				})
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Indexed %d chunk(s) at %d dimensions with %s in %s\n",
				store.Len(), store.Dim, rag.Describe(ctx.embedder), time.Since(start).Round(time.Millisecond))
			fmt.Fprintf(cmd.OutOrStdout(), "Written to %s\n", rel)
			if store.Provider == "local" {
				// Said plainly and every time. An index whose vectors carry no
				// semantics beyond word overlap gives answers that look exactly
				// like the other kind, and the difference matters more than the
				// noise of repeating it.
				fmt.Fprintln(cmd.OutOrStdout(),
					"\nThis index was built by the built-in lexical embedder: it matches words, not meaning.\n"+
						"Set embedding.base_url and embedding.model in "+facet.ConfigName+" to use a real embedding model.")
			}
			return nil
		},
	}
	flags.bind(cmd)
	cmd.Flags().BoolVar(&asJSON, "json", false, "report the build as JSON")
	return cmd
}

func newRAGQueryCmd() *cobra.Command {
	var flags embedFlags
	var asJSON bool
	var limit int
	var minScore float64
	var kind string

	cmd := &cobra.Command{
		Use:   "query <question>",
		Short: "Search the index and print the nearest chunks",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := flags.resolve(cmd)
			if err != nil {
				return err
			}
			question := strings.Join(args, " ")

			store, err := rag.LoadStore(ctx.indexPath)
			if os.IsNotExist(err) {
				return fmt.Errorf("no index at %s — build one with 'facet rag index'",
					ctx.registries[0].Rel(ctx.indexPath))
			}
			if err != nil {
				return err
			}

			start := time.Now()
			hits, err := rag.Query(cmd.Context(), store, ctx.embedder, question, limit)
			if err != nil {
				return err
			}
			took := time.Since(start)

			hits = filter(hits, kind, float32(minScore))

			if asJSON {
				return encode(cmd.OutOrStdout(), map[string]any{
					"question": question,
					"provider": store.Provider,
					"model":    store.Model,
					"chunks":   store.Len(),
					"tookMs":   float64(took.Microseconds()) / 1000,
					"hits":     hits,
				})
			}
			printHits(cmd, hits, question, store, took)
			return nil
		},
	}
	flags.bind(cmd)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit hits as JSON")
	cmd.Flags().IntVarP(&limit, "limit", "n", 5, "how many chunks to return")
	cmd.Flags().Float64Var(&minScore, "min-score", 0, "drop hits below this cosine similarity")
	cmd.Flags().StringVar(&kind, "kind", "", `only "record" chunks or only "doc" chunks`)
	return cmd
}

// filter narrows a result set after the search rather than during it.
func filter(hits []rag.Hit, kind string, min float32) []rag.Hit {
	out := hits[:0]
	for _, h := range hits {
		if kind != "" && h.Kind != kind {
			continue
		}
		if h.Score < min {
			continue
		}
		out = append(out, h)
	}
	return out
}

func printHits(cmd *cobra.Command, hits []rag.Hit, question string, store *rag.Store, took time.Duration) {
	w := cmd.OutOrStdout()
	if len(hits) == 0 {
		fmt.Fprintf(w, "Nothing in the index is close to %q.\n", question)
		return
	}
	for i, h := range hits {
		where := h.Path
		if h.Feature != "" {
			where = fmt.Sprintf("%s — %s", h.Feature, h.Path)
		}
		fmt.Fprintf(w, "%d. [%.3f] %s\n   %s\n", i+1, h.Score, h.Title, where)
		fmt.Fprintf(w, "%s\n\n", indent(excerptOf(h.Text), "   "))
	}
	fmt.Fprintf(w, "%d hit(s) from %d chunk(s) in %.1f ms · %s/%s\n",
		len(hits), store.Len(), float64(took.Microseconds())/1000, store.Provider, store.Model)
}

// excerptOf trims a chunk for a terminal without pretending to summarise it.
func excerptOf(text string) string {
	const max = 320
	body := strings.TrimSpace(text)
	if len(body) <= max {
		return body
	}
	cut := strings.LastIndexByte(body[:max], '\n')
	if cut < max/2 {
		cut = max
	}
	return strings.TrimSpace(body[:cut]) + "\n…"
}

func indent(text, prefix string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

func encode(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}
