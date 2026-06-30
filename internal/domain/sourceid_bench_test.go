package domain

import "testing"

// sink defeats dead-code elimination so the benchmarked call isn't optimized away.
var sinkSourceID string

func BenchmarkDeriveSourceID(b *testing.B) {
	const path = "data/eu/de/bavaria/regions-2026.gpkg"
	var r string
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r = DeriveSourceID(path)
	}
	sinkSourceID = r
}
