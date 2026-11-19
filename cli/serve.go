package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/facet-framework/core/internal/remote"
	"github.com/spf13/cobra"
)

// @facet: navigation/interfaces/remote-read-access
// @facet-role: related
func newServeCmd() *cobra.Command {
	var configPath, manifestPath, listen string
	cmd := &cobra.Command{Use: "serve", Short: "Serve an explicitly published read-only corpus over REST and MCP", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := serveRoot(cmd)
		if err != nil {
			return err
		}
		if configPath == "" || manifestPath == "" {
			return errors.New("--config and --manifest are required")
		}
		if insideRoot(root, configPath) || insideRoot(root, manifestPath) {
			return errors.New("operator config and publication manifest must be outside the corpus root")
		}
		var cfg remote.Config
		if err := readServeJSON(configPath, &cfg); err != nil {
			return err
		}
		if cfg.OAuth != nil {
			cfg.OAuth.ClientSecret = os.Getenv("FACET_SERVE_OAUTH_CLIENT_SECRET")
		}
		if cfg.RAG != nil {
			cfg.RAG.APIKey = os.Getenv("FACET_SERVE_EMBEDDING_API_KEY")
		}
		corpus, err := loadPublication(root, manifestPath)
		if err != nil {
			return err
		}
		logger := slog.New(slog.NewJSONHandler(cmd.ErrOrStderr(), nil))
		if cfg.UI && remote.RegisteredViewer() == "" {
			logger.Warn("ui is enabled but this build carries no viewer; serving tools without an MCP Apps resource")
		}
		cfg.Audit = func(e remote.AuditEvent) {
			logger.Info("facet read request", "request_id", e.RequestID, "operation", e.Operation, "status", e.Status, "code", e.Code, "duration_ms", e.DurationMS, "principal", e.Principal, "snapshot", e.Snapshot)
		}
		handler, err := remote.NewServer(corpus, cfg)
		if err != nil {
			return err
		}
		server := &http.Server{Addr: listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- server.ListenAndServe() }()
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			drain, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			err := server.Shutdown(drain)
			if err != nil {
				server.Close()
			}
			return err
		}
	}}
	cmd.Flags().StringVar(&configPath, "config", "", "Operator-owned JSON configuration outside the corpus")
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "Reviewed publication manifest outside the corpus")
	cmd.Flags().StringVar(&listen, "listen", "127.0.0.1:8080", "HTTP listener behind trusted HTTPS ingress")
	var keyScopes, keyTransports []string
	var expires string
	keygen := &cobra.Command{Use: "keygen", Short: "Generate a scoped API key and its digest configuration", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		expiry, err := time.Parse(time.RFC3339, expires)
		if err != nil || !expiry.After(time.Now()) {
			return errors.New("--expires requires a future RFC3339 timestamp")
		}
		secret, key, err := remote.GenerateKey(keyScopes, keyTransports, expiry)
		if err != nil {
			return err
		}
		if err := (&remote.Authenticator{Keys: []remote.Key{key}}).Validate(); err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
			Secret string     `json:"secret"`
			Config remote.Key `json:"config"`
		}{secret, key})
	}}
	keygen.Flags().StringSliceVar(&keyScopes, "scopes", []string{remote.RecordsScope}, "Read permissions for this key")
	keygen.Flags().StringSliceVar(&keyTransports, "transports", []string{"rest"}, "Allowed transports: rest,mcp")
	keygen.Flags().StringVar(&expires, "expires", "", "Required expiration timestamp")
	var checkManifest string
	check := &cobra.Command{Use: "check-publication", Short: "Validate an explicit publication without publishing files", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if checkManifest == "" {
			return errors.New("--manifest is required")
		}
		root, err := serveRoot(cmd)
		if err != nil {
			return err
		}
		corpus, err := loadPublication(root, checkManifest)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), corpus.Snapshot)
		return err
	}}
	check.Flags().StringVar(&checkManifest, "manifest", "", "Reviewed manifest to validate")
	var indexManifest, indexConfig, indexOutput string
	var allowRecordEgress bool
	index := &cobra.Command{Use: "index-publication", Short: "Build an offline RAG index over an approved publication", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := serveRoot(cmd)
		if err != nil {
			return err
		}
		if indexManifest == "" || indexConfig == "" || indexOutput == "" || insideRoot(root, indexManifest) || insideRoot(root, indexConfig) || insideRoot(root, indexOutput) {
			return errors.New("manifest, config and new output directory must be outside corpus root")
		}
		var cfg remote.Config
		if err := readServeJSON(indexConfig, &cfg); err != nil {
			return err
		}
		if cfg.RAG == nil {
			return errors.New("configuration must select a RAG provider/model")
		}
		cfg.RAG.APIKey = os.Getenv("FACET_SERVE_EMBEDDING_API_KEY")
		corpus, err := loadPublication(root, indexManifest)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(indexOutput); !os.IsNotExist(err) {
			return errors.New("output directory must not exist")
		}
		store, proof, err := remote.BuildIndex(cmd.Context(), corpus, cfg.RAG, allowRecordEgress)
		if err != nil {
			return err
		}
		if err := os.Mkdir(indexOutput, 0700); err != nil {
			return err
		}
		complete := false
		defer func() {
			if !complete {
				os.RemoveAll(indexOutput)
			}
		}()
		indexPath := filepath.Join(indexOutput, "records.facetvec")
		if err := store.Save(indexPath); err != nil {
			return err
		}
		bytes, err := os.ReadFile(indexPath)
		if err != nil {
			return err
		}
		if len(bytes) > 64<<20 {
			return errors.New("index exceeds service size limit")
		}
		hash := sha256.Sum256(bytes)
		sidecar, err := json.MarshalIndent(proof, "", "  ")
		if err != nil {
			return err
		}
		if len(sidecar) > 4<<20 {
			return errors.New("provenance exceeds service size limit")
		}
		if err := os.WriteFile(filepath.Join(indexOutput, "provenance.json"), sidecar, 0600); err != nil {
			return err
		}
		complete = true
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"index_sha256": hex.EncodeToString(hash[:]), "snapshot": corpus.Snapshot})
	}}
	index.Flags().StringVar(&indexManifest, "manifest", "", "Approved publication manifest")
	index.Flags().StringVar(&indexConfig, "config", "", "Operator config selecting RAG provider/model")
	index.Flags().StringVar(&indexOutput, "output", "", "New private directory for reviewable index and provenance")
	index.Flags().BoolVar(&allowRecordEgress, "allow-record-egress", false, "Explicitly permit sending published record text to the configured embedding provider")
	cmd.AddCommand(keygen, check, index)
	return cmd
}
func insideRoot(root, p string) bool {
	a, err := filepath.Abs(root)
	if err != nil {
		return true
	}
	b, err := filepath.Abs(p)
	if err != nil {
		return true
	}
	if real, e := filepath.EvalSymlinks(a); e == nil {
		a = real
	}
	if real, e := filepath.EvalSymlinks(b); e == nil {
		b = real
	} else if os.IsNotExist(e) {
		parent, err := filepath.EvalSymlinks(filepath.Dir(b))
		if err != nil {
			return true
		}
		b = filepath.Join(parent, filepath.Base(b))
	} else {
		return true
	}
	rel, err := filepath.Rel(a, b)
	return err != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
func readServeJSON(path string, target any) error {
	f, err := os.Open(path)
	if err != nil {
		return errors.New("cannot open operator configuration")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil || len(raw) > 1<<20 {
		return errors.New("invalid or oversized operator configuration")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return errors.New("invalid operator configuration")
	}
	if dec.Decode(new(any)) != io.EOF {
		return errors.New("multiple configuration values")
	}
	return nil
}
func loadPublication(root, manifest string) (*remote.Corpus, error) {
	f, err := os.Open(manifest)
	if err != nil {
		return nil, errors.New("cannot open publication manifest")
	}
	defer f.Close()
	publication, err := remote.LoadPublication(f)
	if err != nil {
		return nil, err
	}
	return remote.LoadCorpus(root, publication)
}

func serveRoot(cmd *cobra.Command) (string, error) {
	root, _ := cmd.Flags().GetString("root")
	if root == "" {
		return "", errors.New("serve requires an explicit --root; no repository discovery is performed")
	}
	return filepath.Abs(root)
}
