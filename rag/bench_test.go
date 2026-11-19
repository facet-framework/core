package rag

import (
	"fmt"
	"math/rand"
	"runtime"
	"sort"
	"testing"
	"time"
)

// synthetic builds a store of n random unit vectors.
func synthetic(tb testing.TB, n, dim int) *Store {
	tb.Helper()
	rng := rand.New(rand.NewSource(1))

	chunks := make([]Chunk, n)
	vectors := make([][]float32, n)
	for i := 0; i < n; i++ {
		chunks[i] = Chunk{ID: fmt.Sprintf("f/%d#intent", i), Feature: fmt.Sprintf("f/%d", i), Kind: "record", Facet: "intent"}
		v := make([]float32, dim)
		for d := range v {
			v[d] = float32(rng.NormFloat64())
		}
		vectors[i] = v
	}
	store, err := NewStore("bench", "synthetic", "", chunks, vectors)
	if err != nil {
		tb.Fatal(err)
	}
	return store
}

// BenchmarkSearch is the evidence for the claim this package makes: that brute
// force is fast enough at the scale a documentation registry reaches, and that
// no approximate index is needed to answer in milliseconds.
func BenchmarkSearch(b *testing.B) {
	for _, size := range []int{1_000, 10_000, 100_000} {
		for _, dim := range []int{384, 768, 1536} {
			b.Run(fmt.Sprintf("n=%d/dim=%d", size, dim), func(b *testing.B) {
				store := synthetic(b, size, dim)
				query := make([]float32, dim)
				for i := range query {
					query[i] = float32(rand.NormFloat64())
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := store.Search(query, 10); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// Nine paired work blocks tolerate up to four isolated scheduling outliers.
const samples = 9

// medianRatio measures comparable work blocks and takes the median paired ratio.
// Repeats normalize the result back to one call. Scaling compares one large scan
// with ten small scans, so both sides span similar scheduling windows instead of
// comparing one long window with a short one. Alternating AB/BA order balances
// drift between adjacent blocks. Separate duration medians are diagnostic only;
// dividing them would discard the pairing that controls shared-runner noise.
func medianRatio(a, b func(), repeatsA, repeatsB int) (ratioAB float64, medA, medB time.Duration) {
	da := make([]time.Duration, samples)
	db := make([]time.Duration, samples)
	ratios := make([]float64, samples)
	measure := func(run func(), repeats int) time.Duration {
		start := time.Now()
		for range repeats {
			run()
		}
		return time.Since(start) / time.Duration(repeats)
	}
	for i := range samples {
		if i%2 == 0 {
			da[i] = measure(a, repeatsA)
			db[i] = measure(b, repeatsB)
		} else {
			db[i] = measure(b, repeatsB)
			da[i] = measure(a, repeatsA)
		}
		ratios[i] = float64(da[i]) / float64(db[i])
	}
	sort.Slice(da, func(i, j int) bool { return da[i] < da[j] })
	sort.Slice(db, func(i, j int) bool { return db[i] < db[j] })
	sort.Float64s(ratios)
	return ratios[samples/2], da[samples/2], db[samples/2]
}

// TestSearchScalesLinearlyAndAddsNoOverheadPerDocument defends the cost model
// the package's brute-force choice rests on, without measuring the machine.
func TestSearchScalesLinearlyAndAddsNoOverheadPerDocument(t *testing.T) {
	if testing.Short() {
		// 350 MB of flat vectors across the three stores, and synthetic builds
		// each as a [][]float32 that NewStore then copies, so the peak is about
		// twice that before the per-store garbage is collected.
		t.Skip("allocates ~700 MB at peak")
	}
	const dim = 768

	query := make([]float32, dim)
	for i := range query {
		query[i] = float32(rand.NormFloat64())
	}

	const (
		small      = 10_000
		large      = 100_000
		minScaling = 4.0
		maxScaling = 12.0
	)
	if small < shardThreshold {
		t.Fatalf("both scaling sizes must exercise the sharded path: small=%d is below "+
			"shardThreshold=%d, so this would compare a serial scan against a sharded "+
			"one and measure the core count", small, shardThreshold)
	}
	smallStore := synthetic(t, small, dim)
	largeStore := synthetic(t, large, dim)

	// Warm-ups, so neither measurement is the first touch of its memory.
	for _, s := range []*Store{smallStore, largeStore} {
		if _, err := s.Search(query, 10); err != nil {
			t.Fatal(err)
		}
	}

	var hits []Hit
	var searchErr error
	keep := func(h []Hit, err error) {
		if err != nil {
			searchErr = err
		}
		hits = h
	}
	scaling, largeTime, smallTime := medianRatio(
		func() { keep(largeStore.Search(query, 10)) },
		func() { keep(smallStore.Search(query, 10)) },
		1, large/small,
	)
	if searchErr != nil {
		t.Fatal(searchErr)
	}
	if len(hits) != 10 {
		t.Fatalf("want 10 hits, got %d", len(hits))
	}

	t.Logf("scaling on %d core(s): %d docs %v, %d docs %v, paired median ratio %.2f× for 10× the documents",
		runtime.GOMAXPROCS(0), small, smallTime, large, largeTime, scaling)
	switch {
	case scaling > maxScaling:
		t.Errorf("%d× the documents cost %.2f× the time, past the %.0f× ceiling — "+
			"the cost per document grew with the size of the index, so the scan is "+
			"doing more than a linear pass (an n·log n scan also stays under this "+
			"ceiling, so passing it does not prove the scan linear)",
			large/small, scaling, maxScaling)
	case scaling < minScaling:
		t.Errorf("%d× the documents cost only %.2f× the time, under the %.0f× floor — "+
			"the small query got disproportionately slower, which means a cost "+
			"was added that does not scale with the number of documents",
			large/small, scaling, minScaling)
	}

	// Overhead: the serial scan may cost at most 1.25x a bare dot-product loop.
	// This axis detects per-document costs that cancel in the scaling ratio.
	// Keep the reference shaped like dot(): re-slice then range, so extra
	// bounds checks in the reference do not dilute the regression signal.
	// The 1.25 ceiling allows timing noise; costs below roughly a fifth of the
	// scan can remain invisible and require BenchmarkSearch for investigation.
	// Stay below shardThreshold: comparing parallel work to a serial reference
	// would measure core count instead of per-document overhead.
	const serial = 4_000
	if serial >= shardThreshold {
		t.Fatalf("the overhead reference is serial: serial=%d must stay below "+
			"shardThreshold=%d, or this compares a sharded scan against a "+
			"single-threaded loop and measures the core count", serial, shardThreshold)
	}
	serialStore := synthetic(t, serial, dim)
	if _, err := serialStore.Search(query, 10); err != nil {
		t.Fatal(err)
	}

	// The reference reads the store's own normalised vectors and a normalised
	// copy of the query, so it does the identical arithmetic on identical
	// memory — otherwise the ratio would include normalisation, or a cache miss
	// the real scan does not take.
	normalisedQuery := make([]float32, dim)
	copy(normalisedQuery, query)
	normaliseRows(normalisedQuery, dim)
	vectors := serialStore.vectors

	var sink float32
	overhead, scanTime, referenceTime := medianRatio(
		func() { keep(serialStore.Search(query, 10)) },
		func() {
			for i := 0; i < serial; i++ {
				row := vectors[i*dim : i*dim+dim]
				row = row[:len(normalisedQuery)]
				var sum float32
				for j, v := range normalisedQuery {
					sum += v * row[j]
				}
				sink += sum
			}
		},
		8, 8,
	)
	if searchErr != nil {
		t.Fatal(searchErr)
	}

	// Allocation counts do not depend on scheduling. Both stores take the
	// serial path with the same dimensions and k, so per-document allocations
	// must not hide behind a noisy timing ratio.
	smallSerialStore := synthetic(t, 100, dim)
	smallAllocations := testing.AllocsPerRun(3, func() { keep(smallSerialStore.Search(query, 10)) })
	largeAllocations := testing.AllocsPerRun(3, func() { keep(serialStore.Search(query, 10)) })
	if searchErr != nil {
		t.Fatal(searchErr)
	}
	t.Logf("serial allocations per query: 100 docs %.0f, %d docs %.0f", smallAllocations, serial, largeAllocations)
	if largeAllocations > smallAllocations+1 {
		t.Errorf("allocations grew with document count: 100 docs %.0f, %d docs %.0f", smallAllocations, serial, largeAllocations)
	}

	const maxOverhead = 1.25
	t.Logf("overhead over %d docs: scan %v, bare dot-product loop %v, paired median ratio %.2f×",
		serial, scanTime, referenceTime, overhead)
	if overhead > maxOverhead {
		t.Errorf("the scan cost %.2f× a bare dot-product loop over the same vectors, "+
			"past the %.2f× ceiling — something per-document was added to the hot loop",
			overhead, maxOverhead)
	}
	_ = sink
}
