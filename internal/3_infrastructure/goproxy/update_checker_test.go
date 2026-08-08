package goproxy

import "testing"

func TestSplitMajor(t *testing.T) {
	tests := []struct {
		name      string
		modPath   string
		version   string
		wantBase  string
		wantMajor int
	}{
		{"v1 без суффикса", "github.com/pkg/errors", "v0.9.1", "github.com/pkg/errors", 1},
		{"v0 без суффикса", "go.bug.st/serial", "v1.6.4", "go.bug.st/serial", 1},
		{"суффикс v3", "go.yaml.in/yaml/v3", "v3.0.4", "go.yaml.in/yaml", 3},
		{"суффикс v10", "example.com/mod/v10", "v10.1.0", "example.com/mod", 10},
		{"incompatible без суффикса", "github.com/foo/bar", "v2.1.0+incompatible", "github.com/foo/bar", 2},
		{"путь заканчивается на /v1 — не суффикс мажора", "example.com/mod/v1", "v1.2.0", "example.com/mod/v1", 1},
		{"путь заканчивается на /video — не суффикс мажора", "example.com/video", "v1.2.0", "example.com/video", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, major := splitMajor(tt.modPath, tt.version)
			if base != tt.wantBase || major != tt.wantMajor {
				t.Errorf("splitMajor(%q, %q) = (%q, %d), want (%q, %d)",
					tt.modPath, tt.version, base, major, tt.wantBase, tt.wantMajor)
			}
		})
	}
}
