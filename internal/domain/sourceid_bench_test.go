package domain

import "testing"

func BenchmarkDeriveSourceID(b *testing.B) {
	const path = "data/eu/de/bavaria/regions-2026.gpkg"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = DeriveSourceID(path)
	}
}
