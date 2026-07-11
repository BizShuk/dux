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
type Settings struct {
	Roots              []string `json:"roots"`
	ExcludeList        []string `json:"exclude_list"`
	PrivacyRedactPaths []string `json:"privacy_redact_paths"`
	Workers            int      `json:"workers"`
	BreakdownThreshold string   `json:"breakdown_threshold"`
	ColdSizeThreshold  string   `json:"cold_size_threshold"`
	ColdAgeDays        int      `json:"cold_age_days"`
	OverviewRootDepth  int      `json:"overview_root_depth"`
	OverviewHomeDepth  int      `json:"overview_home_depth"`
	TopN               int      `json:"top_n"`
	PrivacyRedact      bool     `json:"privacy_redact"`
	FollowSymlinks     bool     `json:"follow_symlinks"`
	Interactive        string   `json:"interactive"` // "auto" / "true" / "false"

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
func (s *Settings) validate() error {
	if s.Roots == nil {
		return errors.New("missing roots")
	}
	if len(s.Roots) == 0 {
		return errors.New("empty roots")
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

// ExpandRoots replaces "~" with $HOME in each root path.
// Mutates in place; mirrors config/config.go ExpandPaths in file_watcher.
func (s *Settings) ExpandRoots() {
	for i, r := range s.Roots {
		s.Roots[i] = config.ExpandHome(r)
	}
	for i, p := range s.PrivacyRedactPaths {
		s.PrivacyRedactPaths[i] = config.ExpandHome(p)
	}
	for i, p := range s.ExcludeList {
		s.ExcludeList[i] = config.ExpandHome(p)
	}
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