package geopackage

import "testing"

var sinkGeomType string

func BenchmarkExtractGeometryType(b *testing.B) {
	const wkt = "MULTIPOLYGON (((11.5 48.1, 11.6 48.2, 11.7 48.1, 11.5 48.1)))"
	var r string
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r = extractGeometryType(wkt)
	}
	sinkGeomType = r
}
