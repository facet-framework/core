package spec

import (
	"context"
	"fmt"
)

const (
	SpecMoved  = "spec-moved"
	Orphaned   = "orphaned"
	Unclaimed  = "unclaimed"
	Unmeasured = "unmeasured"
	Deprecated = "deprecated"

	ChangedSpecAdvice = "A changed specification is not an instruction to change code. Report it and recommend implementing it in an existing or new work item."
)

// RefreshCache fetches the configured branches without reading or changing the lock.
func RefreshCache(ctx context.Context, root string, sources []Source) {
	for _, source := range sources {
		if ctx.Err() != nil {
			return
		}
		_, _ = fetchFollow(ctx, sourceCache(root, source), source, true)
	}
}

// ReadFollow reads the committed specification files at the fetched branch tip.
func ReadFollow(ctx context.Context, root string, source Source, cached bool) (LockedSource, error) {
	checkout := sourceCache(root, source)
	var commit string
	var err error
	if cached {
		commit, err = resolveCachedFollow(ctx, checkout, cachedFollowRef(source))
		if err != nil {
			return LockedSource{}, fmt.Errorf("cannot read cached branch %q: %w", source.Follow, err)
		}
	} else {
		commit, err = fetchFollow(ctx, checkout, source, false)
		if err != nil {
			return LockedSource{}, err
		}
	}
	return readSnapshot(ctx, checkout, true, source, commit, true)
}

// FollowDiff shows changes to the normalised scenario sequence at follow.
func FollowDiff(pinned, current Sequence) string {
	return sequenceDiff(pinned, current, "follow")
}
