// Package config loads disk-analyzer settings from gosdk + default_settings.json
// and exposes the few fields the remaining sub-commands actually need.
//
// After the scan rewrite, the surviving sub-commands (`show`, `diff`, `tui`)
// only require:
//
//   - TopN — for `show` / `diff` top-N truncation
//   - OverviewRootDepth — for `show` root overview depth
//   - PrivacyRedactPaths — for `tui` path redaction patterns
//   - SQLitePath / DataDir / CSVDir / LogDir — derived from gosdk config paths
//
// All other historical fields (breakdown threshold, cold data, excludes,
// roots, workers, follow-symlinks, interactive) were exclusively consumed by
// the dropped `scan` pipeline.
package config

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	TopN              int      `json:"top_n" mapstructure:"top_n"`
	OverviewRootDepth int      `json:"overview_root_depth" mapstructure:"overview_root_depth"`
	PrivacyRedactPaths []string `json:"privacy_redact_paths" mapstructure:"privacy_redact_paths"`

	// Derived paths — populated by validate() from gosdk config paths.
	SQLitePath string `json:"-"`
	DataDir    string `json:"-"`
	CSVDir     string `json:"-"`
	LogDir     string `json:"-"`
}

// GlobalSettings is the package-level singleton holding the active Settings.
// Single-shot CLI lifecycle; handlers read it directly rather than receiving
// a *Settings through DI.
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
	if s.TopN <= 0 {
		s.TopN = 10
	}
	if s.OverviewRootDepth <= 0 {
		s.OverviewRootDepth = 2
	}
	// PrivacyRedactPaths is consumed by tui.NewModel directly; expand
	// "~" / "$HOME" here so callers receive absolute patterns.
	for i, p := range s.PrivacyRedactPaths {
		s.PrivacyRedactPaths[i] = expandHomeOrEnv(p)
	}
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

// ParseSize accepts strings like "100B", "200KB", "100MB", "1GB" and returns
// the size in bytes. SI (1000) units — not binary (1024) — to match how the
// "human" sizes are usually displayed; for scan-thresholds this approximation
// is fine. Exposed so callers (cmd/scan.go) can parse CLI overrides like
// --min-size without re-implementing the unit table.
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
		num  int64
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