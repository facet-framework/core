//go:build race

package facet

// raceEnabled reports whether the test binary runs under the race detector.
// The detector drops a random quarter of sync.Pool puts, so code that reuses
// pooled state (regexp's backtracker among it) allocates afresh on many calls,
// and an allocation bound measures the detector rather than the code.
const raceEnabled = true
