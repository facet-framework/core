package cli

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/facet-framework/core/internal/spec"
	"github.com/facet-framework/core/registry"
)

// @facet: authoring/session/registry-context
// @facet-role: related

func refreshHookSpecCache(ctx context.Context, root string) {
	cfg := hookSpecConfig(root)
	if cfg == nil {
		return
	}
	timeout := 10 * time.Second
	if value, err := time.ParseDuration(os.Getenv("FACET_SPEC_REFRESH_TIMEOUT")); err == nil && value > 0 && value < timeout {
		timeout = value
	}
	// Allow the Git worker to terminate its subprocesses before the hook returns.
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	work, stop := context.WithTimeout(deadline, timeout-min(100*time.Millisecond, timeout/10))
	defer stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		spec.RefreshCache(work, root, cfg.SpecSources)
	}()
	select {
	case <-done:
	case <-deadline.Done():
	}
}

// @facet: authoring/guardrails/drift-notice
// @facet-role: related

func hookSpecDrift(ctx hookContext, session string) []string {
	cfg := hookSpecConfig(ctx.root)
	if cfg == nil {
		return nil
	}
	report, err := facet.CompareSpecDrift(context.Background(), ctx.root, cfg.All(), cfg.SpecSources, true, false)
	if err != nil {
		return nil
	}

	var notices []string
	seen := make(map[string]bool)
	for _, finding := range report.Findings {
		switch finding.State {
		case spec.SpecMoved, spec.Orphaned, spec.Unclaimed:
		default:
			continue
		}
		address := finding.Address
		deliver, checked := seen[address]
		if !checked {
			key := sha256.Sum256([]byte(ctx.root + "\x00" + address))
			deliver = noticeIsNew(session, fmt.Sprintf("spec-drift:%x", key))
			seen[address] = deliver
		}
		if deliver {
			notices = append(notices, "- "+formatSpecDriftFinding(finding))
		}
	}
	return notices
}

func formatHookSpecNotices(notices []string) string {
	return "FACET specification drift from cached sources:\n" + strings.Join(notices, "\n")
}

func hookSpecConfig(root string) *facet.Config {
	cfg, err := facet.LoadConfigUnresolved(root)
	if err != nil || len(cfg.SpecSources) == 0 {
		return nil
	}
	return cfg
}
