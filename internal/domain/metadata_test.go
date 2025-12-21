package domain

import (
	"testing"
	"time"
)

func TestMetadataHasKeyword(t *testing.T) {
	meta := Metadata{
		Keywords: []string{"geo", "data", "germany"},
	}

	tests := []struct {
		keyword string
		want    bool
	}{
		{"geo", true},
		{"data", true},
		{"germany", true},
		{"france", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.keyword, func(t *testing.T) {
			if got := meta.HasKeyword(tt.keyword); got != tt.want {
				t.Errorf("HasKeyword(%q) = %v, want %v", tt.keyword, got, tt.want)
			}
		})
	}
}

func TestMetadataHasKeywordEmpty(t *testing.T) {
	meta := Metadata{}

	if meta.HasKeyword("anything") {
		t.Error("HasKeyword on empty keywords should return false")
	}
}

func TestMetadataGetCustom(t *testing.T) {
	meta := Metadata{
		Custom: map[string]string{
			"source": "OpenStreetMap",
			"year":   "2024",
		},
	}

	tests := []struct {
		key    string
		want   string
		wantOK bool
	}{
		{"source", "OpenStreetMap", true},
		{"year", "2024", true},
		{"missing", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got, ok := meta.GetCustom(tt.key)
			if ok != tt.wantOK {
				t.Errorf("GetCustom(%q) ok = %v, want %v", tt.key, ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("GetCustom(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestMetadataGetCustomNilMap(t *testing.T) {
	meta := Metadata{}

	got, ok := meta.GetCustom("anything")
	if ok {
		t.Error("GetCustom on nil map should return false")
	}
	if got != "" {
		t.Error("GetCustom on nil map should return empty string")
	}
}

func TestLicenseIsEmpty(t *testing.T) {
	tests := []struct {
		name    string
		license License
		want    bool
	}{
		{
			name:    "completely empty",
			license: License{},
			want:    true,
		},
		{
			name:    "with name",
			license: License{Name: "CC BY 4.0"},
			want:    false,
		},
		{
			name:    "with URL",
			license: License{URL: "https://example.com"},
			want:    false,
		},
		{
			name:    "with attribution",
			license: License{Attribution: "Test Attribution"},
			want:    false,
		},
		{
			name: "fully populated",
			license: License{
				Name:        "CC BY 4.0",
				URL:         "https://creativecommons.org/licenses/by/4.0/",
				Attribution: "Data by Example Corp",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.license.IsEmpty(); got != tt.want {
				t.Errorf("IsEmpty() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLicenseString(t *testing.T) {
	tests := []struct {
		name    string
		license License
		want    string
	}{
		{
			name:    "prefer attribution",
			license: License{Name: "CC BY 4.0", Attribution: "Data by Example"},
			want:    "Data by Example",
		},
		{
			name:    "fallback to name",
			license: License{Name: "CC BY 4.0"},
			want:    "CC BY 4.0",
		},
		{
			name:    "empty license",
			license: License{},
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.license.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestQueryResultFeatureCount(t *testing.T) {
	tests := []struct {
		name   string
		result QueryResult
		want   int
	}{
		{
			name:   "no features",
			result: QueryResult{},
			want:   0,
		},
		{
			name: "with features",
			result: QueryResult{
				Features: []Feature{{ID: 1}, {ID: 2}, {ID: 3}},
			},
			want: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.result.FeatureCount(); got != tt.want {
				t.Errorf("FeatureCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestQueryResultHasFeatures(t *testing.T) {
	tests := []struct {
		name   string
		result QueryResult
		want   bool
	}{
		{
			name:   "no features",
			result: QueryResult{},
			want:   false,
		},
		{
			name: "with features",
			result: QueryResult{
				Features: []Feature{{ID: 1}},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.result.HasFeatures(); got != tt.want {
				t.Errorf("HasFeatures() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQueryResponseAddResult(t *testing.T) {
	response := &QueryResponse{}

	result1 := QueryResult{
		PackageID: "pkg1",
		Features:  []Feature{{ID: 1}, {ID: 2}},
	}
	result2 := QueryResult{
		PackageID: "pkg2",
		Features:  []Feature{{ID: 3}},
	}

	response.AddResult(result1)

	if len(response.Results) != 1 {
		t.Errorf("After first AddResult, len(Results) = %d, want 1", len(response.Results))
	}
	if response.TotalFeatures != 2 {
		t.Errorf("After first AddResult, TotalFeatures = %d, want 2", response.TotalFeatures)
	}

	response.AddResult(result2)

	if len(response.Results) != 2 {
		t.Errorf("After second AddResult, len(Results) = %d, want 2", len(response.Results))
	}
	if response.TotalFeatures != 3 {
		t.Errorf("After second AddResult, TotalFeatures = %d, want 3", response.TotalFeatures)
	}
}

func TestQueryResultWithQueryTime(t *testing.T) {
	result := QueryResult{
		PackageID: "test-pkg",
		Features:  []Feature{{ID: 1}},
		QueryTime: 100 * time.Millisecond,
	}

	if result.QueryTime != 100*time.Millisecond {
		t.Errorf("QueryTime = %v, want 100ms", result.QueryTime)
	}
}
