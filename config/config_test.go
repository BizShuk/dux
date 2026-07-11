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
	if len(s.Roots) != 2 {
		t.Fatalf("expected Roots to default to 2 entries, got %d", len(s.Roots))
	}
	if s.Roots[0] != "/" {
		t.Errorf("expected first root to be /, got %q", s.Roots[0])
	}
	// Second root should have ~ expanded to home.
	if s.Roots[1] == "$HOME" {
		t.Error("expected $HOME to be expanded in Roots[1]")
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