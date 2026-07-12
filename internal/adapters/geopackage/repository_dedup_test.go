package geopackage

import (
	"testing"

	"github.com/jobrunner/ortus/internal/domain"
)

// TestDedupFeaturesByProperties locks in the behavior relied on by boundary-inclusive
// (ST_Covers) point-in-polygon matching against ST_Subdivide fragments.
func TestDedupFeaturesByProperties(t *testing.T) {
	feat := func(id int64, props map[string]interface{}) domain.Feature {
		return domain.Feature{ID: id, LayerName: "regions", Properties: props}
	}

	t.Run("collapses same-property fragments, keeps smallest non-zero fid", func(t *testing.T) {
		in := []domain.Feature{
			feat(7, map[string]interface{}{"tzid": "Etc/GMT+10"}),
			feat(3, map[string]interface{}{"tzid": "Etc/GMT+10"}),
			feat(9, map[string]interface{}{"tzid": "Etc/GMT+10"}),
		}
		out := dedupFeaturesByProperties(in)
		if len(out) != 1 {
			t.Fatalf("got %d features, want 1", len(out))
		}
		if out[0].ID != 3 {
			t.Errorf("representative fid = %d, want 3 (smallest)", out[0].ID)
		}
	})

	t.Run("no fid: deterministic representative by smallest geometry WKT", func(t *testing.T) {
		// All duplicates have ID 0 (layer without an fid column). Regardless of input
		// order, the kept representative must be the smallest WKT.
		mk := func(wkt string) domain.Feature {
			f := feat(0, map[string]interface{}{"tzid": "Etc/GMT+10"})
			f.Geometry.WKT = wkt
			return f
		}
		for _, order := range [][]domain.Feature{
			{mk("POLYGON((2 0))"), mk("POLYGON((1 0))"), mk("POLYGON((3 0))")},
			{mk("POLYGON((3 0))"), mk("POLYGON((2 0))"), mk("POLYGON((1 0))")},
		} {
			out := dedupFeaturesByProperties(order)
			if len(out) != 1 {
				t.Fatalf("got %d features, want 1", len(out))
			}
			if out[0].Geometry.WKT != "POLYGON((1 0))" {
				t.Errorf("representative WKT = %q, want smallest POLYGON((1 0))", out[0].Geometry.WKT)
			}
		}
	})

	t.Run("keeps distinct features that differ in properties", func(t *testing.T) {
		in := []domain.Feature{
			feat(1, map[string]interface{}{"tzid": "Europe/Berlin"}),
			feat(2, map[string]interface{}{"tzid": "Europe/Paris"}),
		}
		if out := dedupFeaturesByProperties(in); len(out) != 2 {
			t.Fatalf("got %d features, want 2 (distinct props must both survive)", len(out))
		}
	})

	t.Run("does not collide across value types", func(t *testing.T) {
		// int64(1) and "1" stringify identically; the type-tagged key must keep them apart.
		in := []domain.Feature{
			feat(1, map[string]interface{}{"code": int64(1)}),
			feat(2, map[string]interface{}{"code": "1"}),
		}
		if out := dedupFeaturesByProperties(in); len(out) != 2 {
			t.Fatalf("got %d features, want 2 (int64(1) vs \"1\" must not collide)", len(out))
		}
	})

	t.Run("does not collide on embedded delimiter or NUL in TEXT", func(t *testing.T) {
		// Length-prefixed encoding must keep {a:"x", b:"y"} distinct from {a:"x\x00y", b:""} etc.
		in := []domain.Feature{
			feat(1, map[string]interface{}{"a": "x", "b": "y"}),
			feat(2, map[string]interface{}{"a": "x\x00y", "b": ""}),
		}
		if out := dedupFeaturesByProperties(in); len(out) != 2 {
			t.Fatalf("got %d features, want 2 (delimiter/NUL must not collide)", len(out))
		}
	})

	t.Run("property key order does not matter", func(t *testing.T) {
		in := []domain.Feature{
			{ID: 1, LayerName: "r", Properties: map[string]interface{}{"a": "1", "b": "2"}},
			{ID: 2, LayerName: "r", Properties: map[string]interface{}{"b": "2", "a": "1"}},
		}
		if out := dedupFeaturesByProperties(in); len(out) != 1 {
			t.Fatalf("got %d features, want 1 (same props in different insertion order)", len(out))
		}
	})
}
