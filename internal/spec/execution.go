package spec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/facet-framework/core/pkg/filelock"
)

type FeatureBlock struct {
	Body    string
	CaseTag string
}

// @facet: registry/integrity/spec-execution
// @facet-role: related

// PinnedFeatures reads blocks containing the requested pins at one commit.
func PinnedFeatures(ctx context.Context, root string, source Source, commit string, pins map[string]LockedScenario) (map[string]*FeatureBlock, error) {
	cache, err := pinnedCache(ctx, root, source, commit)
	if err != nil {
		return nil, err
	}
	result := make(map[string]*FeatureBlock, len(pins))
	seen := map[string]bool{}
	err = visitSpecDocuments(ctx, cache, true, source, commit, false, func(_ string, doc Document, issues []Finding) {
		if len(issues) != 0 || doc.Skipped {
			return
		}
		if seen[doc.ID] {
			for address := range pins {
				if strings.HasPrefix(address, doc.ID+"#") {
					delete(result, address)
				}
			}
			return
		}
		seen[doc.ID] = true
		for _, unit := range doc.Units {
			pin, ok := pins[unit.Address]
			if !ok || unit.Strength() != Exact || pin.Strength() != Exact || pin.Steps != unit.Digest || pin.Context != unit.ContextDigest {
				continue
			}
			result[unit.Address] = &doc.blocks[unit.block]
		}
	})
	return result, err
}

func pinnedCache(ctx context.Context, root string, source Source, commit string) (string, error) {
	cache := filepath.Join(root, cacheName)
	checkout := filepath.Join(cache, source.ID)
	hasCommit := func() bool {
		kind, err := gitOutput(ctx, checkout, true, "cat-file", "-t", commit)
		return err == nil && strings.TrimSpace(string(kind)) == "commit"
	}
	if hasCommit() {
		return checkout, nil
	}
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		return "", err
	}
	// The same lock as fetchFollow: both write into this source's cache.
	guard, err := os.OpenFile(filepath.Join(checkout, ".fetch.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", err
	}
	defer guard.Close()
	if err := filelock.Acquire(ctx, guard); err != nil {
		return "", err
	}
	defer filelock.Release(guard)
	if hasCommit() {
		return checkout, nil
	}
	if err := initSpecCache(ctx, checkout); err != nil {
		return "", err
	}
	if _, err := gitOutput(ctx, checkout, true, "fetch", "--no-tags", "--no-recurse-submodules", "--", source.gitURL(), commit); err != nil {
		return "", fmt.Errorf("cannot fetch pinned specification commit %s: %w", commit, err)
	}
	if !hasCommit() {
		return "", fmt.Errorf("pinned specification commit %s is unavailable", commit)
	}
	return checkout, nil
}
