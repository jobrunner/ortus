package storage

import "testing"

func BenchmarkSafeJoin(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = safeJoin("/srv/data", "eu/de/regions.gpkg")
	}
}
