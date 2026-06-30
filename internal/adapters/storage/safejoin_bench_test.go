package storage

import "testing"

var (
	sinkSafePath string
	sinkSafeErr  error
)

func BenchmarkSafeJoin(b *testing.B) {
	var (
		p   string
		err error
	)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p, err = safeJoin("/srv/data", "eu/de/regions.gpkg")
	}
	sinkSafePath, sinkSafeErr = p, err
}
