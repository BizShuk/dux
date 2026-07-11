package config

import (
	"testing"
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
	if s.Workers <= 0 {
		t.Errorf("expected Workers to be defaulted to positive, got %d", s.Workers)
	}
	if s.BreakdownThreshold != "200MB" && s.BreakdownThreshold != "" {
		t.Errorf("expected BreakdownThreshold=200MB or empty, got %q", s.BreakdownThreshold)
	}
	if s.ColdAgeDays <= 0 {
		t.Errorf("expected ColdAgeDays to be defaulted, got %d", s.ColdAgeDays)
	}
	if s.OverviewRootDepth <= 0 {
		t.Errorf("expected OverviewRootDepth to be defaulted, got %d", s.OverviewRootDepth)
	}
	if s.TopN <= 0 {
		t.Errorf("expected TopN to be defaulted, got %d", s.TopN)
	}
}

func TestSettings_DefaultRootsWhenEmpty(t *testing.T) {
	s := &Settings{}
	_ = s.validate()
	if len(s.Roots) != 1 {
		t.Fatalf("expected Roots to default to 1 entry, got %d", len(s.Roots))
	}
	// Single default root should have $HOME expanded to the user's home.
	if s.Roots[0] == "$HOME" {
		t.Error("expected $HOME to be expanded in Roots[0]")
	}
	if s.Roots[0] == "" || s.Roots[0] == "/" {
		t.Errorf("expected Roots[0] to be the user's home directory, got %q", s.Roots[0])
	}
}

func TestSettings_InvalidInteractiveMode(t *testing.T) {
	s := &Settings{
		Interactive: "yes",
	}
	if err := s.validate(); err == nil {
		t.Error("expected error for invalid Interactive mode, got nil")
	}
}

func TestSettings_InteractiveAutoAccepted(t *testing.T) {
	s := &Settings{Interactive: "auto"}
	_ = s.validate()
	if s.Interactive != "auto" {
		t.Errorf("expected Interactive=auto, got %q", s.Interactive)
	}
}

// TestParseSize_Public exercises the exported ParseSize used by handler/runner.go
// to parse --min-size overrides. Same semantics as the package-private parseSize
// but must stay in sync — duplicate the SI unit table here.
func TestParseSize_Public(t *testing.T) {
	tests := []struct {
		in       string
		want     int64
		wantErr  bool
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
				t.Fatalf("ParseSize(%q) err=%v, wantErr=%v", tt.in, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ParseSize(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}