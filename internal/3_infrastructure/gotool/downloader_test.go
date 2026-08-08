package gotool

import "testing"

func TestIsFilesystemPath(t *testing.T) {
	local := []string{".", "..", "./lib", "../lib", "../../shared/lib", `.\lib`, `..\lib`}
	for _, p := range local {
		if !isFilesystemPath(p) {
			t.Errorf("isFilesystemPath(%q) = false, want true", p)
		}
	}

	remote := []string{
		"github.com/spf13/viper",
		"gopkg.in/yaml.v3",
		"example.com/internal/lib",
		"golang.org/x/mod",
	}
	for _, p := range remote {
		if isFilesystemPath(p) {
			t.Errorf("isFilesystemPath(%q) = true, want false", p)
		}
	}
}
