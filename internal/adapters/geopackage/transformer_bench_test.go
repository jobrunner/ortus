package geopackage

import "testing"

func BenchmarkExtractGeometryType(b *testing.B) {
	const wkt = "MULTIPOLYGON (((11.5 48.1, 11.6 48.2, 11.7 48.1, 11.5 48.1)))"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = extractGeometryType(wkt)
	}
}
