// @facet: navigation/interfaces/product-services
// @facet-role: primary
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"github.com/facet-framework/core/pkg/productplugin"
	"github.com/facet-framework/core/registry"
)

type productReply struct {
	ID      string
	Payload json.RawMessage
	Err     error
}

func invokeProducts(ctx context.Context, root, view, service string, input any) []productReply {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	store := productStore()
	installed, err := store.List()
	if err != nil {
		return []productReply{{ID: "discovery", Err: err}}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return []productReply{{ID: "request", Err: err}}
	}
	host, err := os.Executable()
	if err != nil {
		return []productReply{{ID: "host", Err: err}}
	}
	replies := []productReply{}
	var cfg *facet.Config
	var cfgErr error
	loaded, cfgReported := false, false
	config := func() (*facet.Config, error) {
		if !loaded {
			cfg, cfgErr = facet.LoadConfigWithSourceView(root, view)
			loaded = true
		}
		return cfg, cfgErr
	}
	// A product the project explicitly asked for must not be absent silently:
	// each binding says whether this service was requested from its product.
	for _, binding := range productBindings() {
		if binding.Requested == nil {
			continue
		}
		requested, err := binding.Requested(service, config)
		if err != nil {
			if !cfgReported {
				replies = append(replies, productReply{ID: "configuration", Err: err})
				cfgReported = true
			}
			continue
		}
		if !requested {
			continue
		}
		available := false
		for _, in := range installed {
			if in.Manifest.ID == binding.ID && slices.Contains(in.Manifest.Services, service) {
				available = true
			}
		}
		if !available {
			replies = append(replies, productReply{ID: binding.ID, Err: errors.New(binding.Missing)})
		}
	}
	for _, in := range installed {
		if !slices.Contains(in.Manifest.Services, service) {
			continue
		}
		body, err := store.Call(ctx, in.Manifest.ID, service, raw, productplugin.Invocation{Root: root, SourceView: view, HostExecutable: host})
		replies = append(replies, productReply{in.Manifest.ID, body, err})
	}
	return replies
}
func productView(registries []facet.Registry) string {
	for _, reg := range registries {
		if reg.SourceView != nil {
			return reg.SourceView.Name
		}
	}
	return ""
}
func recordProductWarnings(root string, registries []facet.Registry, drift []facet.Drift, baseline string) []string {
	notes := make([]string, len(drift))
	if len(drift) == 0 {
		return notes
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, reply := range invokeProducts(ctx, root, productView(registries), "observe-warnings", struct {
		Drift    []facet.Drift `json:"drift"`
		Baseline string        `json:"baseline"`
	}{drift, baseline}) {
		var result struct {
			Notes []string `json:"notes"`
		}
		err := reply.Err
		if err == nil {
			err = json.Unmarshal(reply.Payload, &result)
		}
		if err == nil && len(result.Notes) != len(drift) {
			err = fmt.Errorf("warning response count differs from request")
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "facet product %s: warning observation failed: %v\n", reply.ID, err)
			continue
		}
		for i, note := range result.Notes {
			notes[i] += note
		}
	}
	return notes
}
func recordProductSession(ctx hookContext, session, kind string) {
	if session == "" {
		return
	}
	ctxDeadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	names := []string{}
	for _, reg := range ctx.registries {
		names = append(names, reg.Name)
	}
	for _, reply := range invokeProducts(ctxDeadline, ctx.root, productView(ctx.registries), "observe-session", struct {
		Registries []string `json:"registries"`
		Session    string   `json:"session"`
		Kind       string   `json:"kind"`
	}{names, session, kind}) {
		if reply.Err != nil {
			fmt.Fprintf(os.Stderr, "facet product %s: session observation failed: %v\n", reply.ID, reply.Err)
		}
	}
}
