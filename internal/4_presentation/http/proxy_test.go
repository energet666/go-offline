package httphandlers

import (
	"context"
	"testing"
)

func TestSplitModuleQuery(t *testing.T) {
	tests := []struct {
		name        string
		module      string
		version     string
		wantPath    string
		wantVersion string
		wantErr     bool
	}{
		{name: "plain path", module: "fyne.io/fyne/v2", version: "", wantPath: "fyne.io/fyne/v2"},
		{name: "plain path with version", module: "fyne.io/fyne/v2", version: "v2.4.0", wantPath: "fyne.io/fyne/v2", wantVersion: "v2.4.0"},
		{name: "pasted query", module: "fyne.io/fyne/v2@latest", version: "", wantPath: "fyne.io/fyne/v2", wantVersion: "latest"},
		{name: "pasted query matching field", module: "fyne.io/fyne/v2@v2.4.0", version: "v2.4.0", wantPath: "fyne.io/fyne/v2", wantVersion: "v2.4.0"},
		{name: "conflicting versions", module: "fyne.io/fyne/v2@v2.4.0", version: "v2.3.0", wantErr: true},
		{name: "empty version after at", module: "fyne.io/fyne/v2@", wantErr: true},
		{name: "empty path before at", module: "@latest", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, version, err := splitModuleQuery(tt.module, tt.version)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("splitModuleQuery(%q, %q) = %q, %q, nil; want an error", tt.module, tt.version, path, version)
				}
				return
			}
			if err != nil {
				t.Fatalf("splitModuleQuery(%q, %q): %v", tt.module, tt.version, err)
			}
			if path != tt.wantPath || version != tt.wantVersion {
				t.Errorf("splitModuleQuery(%q, %q) = %q, %q; want %q, %q",
					tt.module, tt.version, path, version, tt.wantPath, tt.wantVersion)
			}
		})
	}
}

// A malformed path must fail instead of being shortened until some prefix
// happens to resolve: "fyne.io/fyne/v2@latest" used to resolve to fyne.io/fyne
// and download v1.4.3.
func TestResolveModulePathRejectsMalformedPath(t *testing.T) {
	s := &Server{}
	if _, err := s.resolveModulePath(context.Background(), "fyne.io/fyne/v2@latest", ""); err == nil {
		t.Fatal("resolveModulePath accepted a path containing '@'")
	}
}
