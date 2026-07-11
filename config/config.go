package config

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/bizshuk/gosdk/config"
	"github.com/spf13/viper"
)

//go:embed default_settings.json
var defaultSettingsJSON string

// Settings is the runtime configuration for disk-analyzer.
// Fields tagged with `json:"-"` are populated at validate() time from
// gosdk paths and must not be set in the user-facing settings.json.
//
// Note: Roots is intentionally NOT exposed in settings.json — disk-analyzer
// always scans "$HOME" by default. Use the --roots CLI flag to override
// for ad-hoc testing.
type Settings struct {
	ExcludeList        []string `json:"exclude_list" mapstructure:"exclude_list"`
	PrivacyRedactPaths []string `json:"privacy_redact_paths" mapstructure:"privacy_redact_paths"`
	Workers            int      `json:"workers" mapstructure:"workers"`
	BreakdownThreshold string   `json:"breakdown_threshold" mapstructure:"breakdown_threshold"`
	ColdSizeThreshold  string   `json:"cold_size_threshold" mapstructure:"cold_size_threshold"`
	ColdAgeDays        int      `json:"cold_age_days" mapstructure:"cold_age_days"`
	OverviewRootDepth  int      `json:"overview_root_depth" mapstructure:"overview_root_depth"`
	OverviewHomeDepth  int      `json:"overview_home_depth" mapstructure:"overview_home_depth"`
	TopN               int      `json:"top_n" mapstructure:"top_n"`
	PrivacyRedact      bool     `json:"privacy_redact" mapstructure:"privacy_redact"`
	FollowSymlinks     bool     `json:"follow_symlinks" mapstructure:"follow_symlinks"`
	Interactive        string   `json:"interactive" mapstructure:"interactive"` // "auto" / "true" / "false"

	// Roots is filled by validate() to ["$HOME"] by default. Overridable
	// at runtime via the --roots CLI flag for testing.
	Roots []string `json:"-"`

	// Derived paths — populated by validate() from gosdk config paths.
	SQLitePath string `json:"-"`
	DataDir    string `json:"-"`
	CSVDir     string `json:"-"`
	LogDir     string `json:"-"`
}

// GlobalSettings is the package-level singleton holding the active Settings.
// Mirrors the file_watcher convention; handlers read it directly rather than
// receiving a *Settings through DI, because disk-analyzer is a single-shot CLI
// and the lifecycle is bounded by the cobra command, not a long-running runtime.
var GlobalSettings *Settings

// Default loads the gosdk config and unmarshals it into GlobalSettings.
// Must be called once from main() before any command runs.
func Default() error {
	config.Default(
		config.WithAppName("disk-analyzer"),
		config.WithDefaultValue(defaultSettingsJSON),
	)
	GlobalSettings = &Settings{}
	if err := viper.Unmarshal(GlobalSettings); err != nil {
		return fmt.Errorf("unmarshal settings: %w", err)
	}
	if err := GlobalSettings.validate(); err != nil {
		return fmt.Errorf("validate settings: %w", err)
	}
	// Set SQLITE_PATH so gosdk/db.InitSQLite() can pick it up on next call.
	viper.Set("SQLITE_PATH", GlobalSettings.SQLitePath)
	return nil
}

// validate enforces invariants and fills derived paths.
// Validation failures return errors; missing optional fields get sane defaults.
//
// Roots is intentionally NOT configurable via settings.json: disk-analyzer
// always scans "$HOME" by default. Use the --roots CLI flag to override
// for testing only.
func (s *Settings) validate() error {
	if len(s.Roots) == 0 {
		s.Roots = []string{"$HOME"}
	}
	for _, r := range s.Roots {
		if r == "" {
			return errors.New("empty path in roots")
		}
	}
	if s.Workers <= 0 {
		s.Workers = runtime.NumCPU() / 2
		if s.Workers < 1 {
			s.Workers = 1
		}
	}
	if s.BreakdownThreshold == "" {
		s.BreakdownThreshold = "200MB"
	}
	if s.ColdSizeThreshold == "" {
		s.ColdSizeThreshold = "100MB"
	}
	if s.ColdAgeDays <= 0 {
		s.ColdAgeDays = 180
	}
	if s.OverviewRootDepth <= 0 {
		s.OverviewRootDepth = 2
	}
	if s.OverviewHomeDepth <= 0 {
		s.OverviewHomeDepth = 1
	}
	if s.TopN <= 0 {
		s.TopN = 10
	}
	switch s.Interactive {
	case "", "auto":
		s.Interactive = "auto"
	case "true", "false":
		// explicit — keep as-is
	default:
		return fmt.Errorf("invalid interactive mode %q (want auto|true|false)", s.Interactive)
	}
	// Expand "~" in roots.
	s.ExpandRoots()
	// Derive paths from gosdk.
	dataDir := config.GetAppDataDir()
	if dataDir == "" {
		return errors.New("gosdk config: GetAppDataDir() returned empty — did Default() run with WithAppName?")
	}
	s.DataDir = dataDir
	s.CSVDir = dataDir
	s.LogDir = config.GetAppLogDir()
	s.SQLitePath = filepath.Join(dataDir, "history.db")
	if err := os.MkdirAll(s.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir %s: %w", s.DataDir, err)
	}
	if err := os.MkdirAll(s.LogDir, 0o755); err != nil {
		return fmt.Errorf("create log dir %s: %w", s.LogDir, err)
	}
	return nil
}

// ExpandRoots replaces "~" and "$HOME" with the user's home directory in each
// root path. Mutates in place.
func (s *Settings) ExpandRoots() {
	for i, r := range s.Roots {
		s.Roots[i] = expandHomeOrEnv(r)
	}
	for i, p := range s.PrivacyRedactPaths {
		s.PrivacyRedactPaths[i] = expandHomeOrEnv(p)
	}
	for i, p := range s.ExcludeList {
		s.ExcludeList[i] = expandHomeOrEnv(p)
	}
}

// expandHomeOrEnv handles both "~" / "~/..." and "$HOME" / "${HOME}" syntax.
// gosdk's ExpandHome only handles "~", so we layer os.ExpandEnv on top.
func expandHomeOrEnv(p string) string {
	expanded := os.ExpandEnv(p)
	if expanded == p {
		// No env var expansion happened; still try "~".
		expanded = config.ExpandHome(p)
	}
	return expanded
}

// BreakdownThresholdBytes parses BreakdownThreshold as a size string
// (e.g. "200MB", "1GB") and returns it in bytes.
func (s *Settings) BreakdownThresholdBytes() (int64, error) {
	return parseSize(s.BreakdownThreshold)
}

// ColdSizeThresholdBytes parses ColdSizeThreshold as a size string.
func (s *Settings) ColdSizeThresholdBytes() (int64, error) {
	return parseSize(s.ColdSizeThreshold)
}

// ParseSize accepts strings like "100B", "200KB", "100MB", "1GB" and returns
// the size in bytes. SI (1000) units — not binary (1024) — to match how the
// "human" sizes are usually displayed; for scan-thresholds this approximation
// is fine. Exposed so callers (handler/runner.go) can parse CLI overrides
// like --min-size without re-implementing the unit table.
func ParseSize(s string) (int64, error) {
	return parseSize(s)
}

// parseSize accepts strings like "100B", "200KB", "100MB", "1GB".
// SI (1000) units — not binary (1024) — to match how the
// "human" sizes are usually displayed; for scan-thresholds this
// approximation is fine.
func parseSize(s string) (int64, error) {
	if s == "" {
		return 0, errors.New("empty size string")
	}
	d, err := time.ParseDuration(s)
	if err == nil {
		return int64(d), nil
	}
	// Fallback: handle "MB" / "GB" by parsing last 2 chars as unit.
	var (
		num int64
		unit string
	)
	if _, err := fmt.Sscanf(s, "%d%s", &num, &unit); err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", s, err)
	}
	switch unit {
	case "B", "":
		return num, nil
	case "KB":
		return num * 1000, nil
	case "MB":
		return num * 1000 * 1000, nil
	case "GB":
		return num * 1000 * 1000 * 1000, nil
	case "TB":
		return num * 1000 * 1000 * 1000 * 1000, nil
	default:
		return 0, fmt.Errorf("unknown size unit %q", unit)
	}
}