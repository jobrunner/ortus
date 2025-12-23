package watcher

import (
	"testing"

	"github.com/fsnotify/fsnotify"
)

func TestFsnotifyOpToOperation(t *testing.T) {
	tests := []struct {
		name     string
		op       fsnotify.Op
		expected Operation
	}{
		{
			name:     "Remove returns OpDelete",
			op:       fsnotify.Remove,
			expected: OpDelete,
		},
		{
			name:     "Rename returns OpDelete",
			op:       fsnotify.Rename,
			expected: OpDelete,
		},
		{
			name:     "Create returns OpCreate",
			op:       fsnotify.Create,
			expected: OpCreate,
		},
		{
			name:     "Write returns OpModify",
			op:       fsnotify.Write,
			expected: OpModify,
		},
		{
			name:     "Chmod returns OpModify",
			op:       fsnotify.Chmod,
			expected: OpModify,
		},
		{
			name:     "Remove takes precedence over Write",
			op:       fsnotify.Remove | fsnotify.Write,
			expected: OpDelete,
		},
		{
			name:     "Rename takes precedence over Create",
			op:       fsnotify.Rename | fsnotify.Create,
			expected: OpDelete,
		},
		{
			name:     "Create takes precedence over Write",
			op:       fsnotify.Create | fsnotify.Write,
			expected: OpCreate,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := fsnotifyOpToOperation(tt.op)
			if result != tt.expected {
				t.Errorf("fsnotifyOpToOperation(%v) = %v, want %v", tt.op, result, tt.expected)
			}
		})
	}
}

func TestOperationString(t *testing.T) {
	tests := []struct {
		op       Operation
		expected string
	}{
		{OpCreate, "create"},
		{OpModify, "modify"},
		{OpDelete, "delete"},
		{Operation(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.op.String(); got != tt.expected {
				t.Errorf("Operation.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsGeoPackageFile(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"test.gpkg", true},
		{"test.GPKG", true},
		{"test.GpKg", true},
		{"/path/to/file.gpkg", true},
		{"test.txt", false},
		{"test.gpkg.bak", false},
		{"gpkg", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := isGeoPackageFile(tt.path); got != tt.expected {
				t.Errorf("isGeoPackageFile(%q) = %v, want %v", tt.path, got, tt.expected)
			}
		})
	}
}
