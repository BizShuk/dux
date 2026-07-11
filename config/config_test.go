package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSize(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
		wantErr  bool
	}{
		{"200MB", 200 * 1000 * 1000, false},
		{"100MB", 100 * 1000 * 1000, false},
		{"1GB", 1000 * 1000 * 1000, false},
		{"1.5GB", 0, true}, // not supported (only integer)
		{"100B", 100, false},
		{"500KB", 500 * 1000, false},
		{"", 0, true},
		{"abc", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseSize(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseSize(%q) err=%v, wantErr=%v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.expected {
				t.Errorf("parseSize(%q) = %d, want %d", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSettings_Defaults(t *testing.T) {
	s := &Settings{}
	if err := s.validate(); err != nil {
		// validate needs gosdk paths; it will fail outside Default().
		// So this is just testing the field-level fallbacks.
	}
	if s.OverviewRootDepth <= 0 {
		t.Errorf("expected OverviewRootDepth to be defaulted, got %d", s.OverviewRootDepth)
	}
	if s.TopN <= 0 {
		t.Errorf("expected TopN to be defaulted, got %d", s.TopN)
	}
}

// TestParseSize_Public exercises the exported ParseSize used by cmd/scan.go
// to parse --min-size overrides. Same semantics as the package-private parseSize
// but must stay in sync — duplicate the SI unit table here.
func TestParseSize_Public(t *testing.T) {
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"200MB", 200 * 1000 * 1000, false},
		{"1GB", 1000 * 1000 * 1000, false},
		{"500KB", 500 * 1000, false},
		{"100B", 100, false},
		{"1.5GB", 0, true},
		{"", 0, true},
		{"xyz", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseSize(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseSize(%q) err=%v, wantErr=%v", tt.in, err, err)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ParseSize(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// TestSettings_ExcludeFields verifies that the excludes / exclude_from JSON
// keys round-trip into the Settings struct, and that validate() expands
// "~" / "$HOME" inside ExcludeFrom (so users can write "~/excludes.txt"
// instead of the absolute path).
func TestSettings_ExcludeFields(t *testing.T) {
	raw := `{"excludes": ["node_modules", "*.tmp"], "exclude_from": "~/excludes.txt"}`
	var s Settings
	require.NoError(t, json.Unmarshal([]byte(raw), &s))
	assert.Equal(t, []string{"node_modules", "*.tmp"}, s.Excludes)
	assert.Equal(t, "~/excludes.txt", s.ExcludeFrom)

	// validate() expands "~" in ExcludeFrom. validate() also touches
	// gosdk config paths and may fail without Default() having run, so
	// we only assert the expansion side-effect, which happens before
	// the gosdk-derived section.
	if err := s.validate(); err != nil {
		// Expected when gosdk config isn't initialized; only the
		// expansion happened. Fall through and check the home bit.
		t.Logf("validate() returned (likely missing gosdk init): %v", err)
	}
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "excludes.txt"), s.ExcludeFrom,
		"ExcludeFrom should be expanded to absolute path under $HOME")
}

// TestExpandHomeOrEnv covers both forms used by validate() — "~" and
// "$HOME" / "${HOME}". Plain paths without either marker pass through.
func TestExpandHomeOrEnv(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(home, "foo"), expandHomeOrEnv("~/foo"))
	assert.Equal(t, filepath.Join(home, "bar"), expandHomeOrEnv("$HOME/bar"))
	assert.Equal(t, filepath.Join(home, "baz"), expandHomeOrEnv("${HOME}/baz"))
	assert.Equal(t, "plain/path", expandHomeOrEnv("plain/path"))
}