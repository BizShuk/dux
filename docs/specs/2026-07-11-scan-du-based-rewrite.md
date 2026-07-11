# Rewrite `scan` sub-command — du-based, $HOME only, CSV output

## Context

`scan` currently runs a complex pipeline: parallel walk → SQLite upsert → overview/breakdown/dedup/cold/metric emission → TUI/CSV. The user wants this **completely replaced** by a minimal tool:

- Walk `$HOME` using the existing `du` library (already in `du/du.go`, ports coreutils semantics)
- Filter entries ≥ 200MB (files individually; directories by aggregated subtree size)
- Write results to the existing CSV location (`<CSVDir>/<YYYY-MM-DD>.csv`)
- Reuse `svc.WriteCSV` so output format / column schema stays the same

Hardcoded `$HOME`, no excludes, no roots override. CLI surface reduced to `--date / --min-size / --top / --progress`.

Per user decision: **show/diff/tui are kept** but will be broken (scan no longer writes to DB) until a future PR rewires them. Today's change does **not** fix them.

## Outcome

After this change, `./du scan` produces a single CSV per run that any spreadsheet/pandas can ingest. The full DB-backed pipeline is gone. Build is faster; cognitive load drops massively.

## Implementation

### 1. Rewrite `cmd/scan.go`

Strip ~80% of the file. Keep only:

```go
var ScanCmd = &cobra.Command{
    Use:   "scan",
    Short: "Walk $HOME with du and write entries ≥ threshold to CSV",
    RunE: func(cmd *cobra.Command, args []string) error {
        ctx, cancel := context.WithCancel(context.Background())
        // ...SIGINT/SIGTERM → cancel (kept from current)
        cfg := config.GlobalSettings
        if cfg == nil { return errors.New("scan: config not initialized") }

        home, err := os.UserHomeDir()
        if err != nil { return fmt.Errorf("scan: home dir: %w", err) }

        date := scanDate
        if date == "" { date = time.Now().Format("2006-01-02") }

        threshold := int64(200 * 1024 * 1024)
        if scanMinSize != "" {
            v, err := config.ParseSize(scanMinSize)
            if err != nil { return fmt.Errorf("scan: --min-size %q: %w", scanMinSize, err) }
            threshold = v
        }

        duOpts := du.Options{
            Paths:     []string{home},
            AllFiles:  true,                       // emit both files & directories
            Threshold: threshold,                 // filter on size in bytes (positive = at-least)
            OnEntry:   nil,                       // collect into Result.Entries
        }

        result, err := du.Usage(ctx, duOpts)
        if err != nil { return fmt.Errorf("scan: du: %w", err) }

        rows := make([]svc.ReportEntry, 0, len(result.Entries))
        for _, e := range result.Entries {
            rows = append(rows, svc.ReportEntry{
                Path:         e.Path,
                Size:         int64(e.Info.Size),
                Category:     svc.CategorizeByExt(e.Path),
                LastAccessed: e.Info.MaxTime,       // du exposes mtime only; reuse for both
                Depth:        e.Depth,
            })
        }
        // Sort by size desc for human readability when opened in spreadsheet.
        sort.Slice(rows, func(i, j int) bool { return rows[i].Size > rows[j].Size })

        b := svc.NewReportBuilder()
        b.Set(svc.SectionBreakdownHome, rows)
        sections := b.Sections()

        csvPath := filepath.Join(cfg.CSVDir, date+".csv")
        if err := svc.WriteCSV(svc.CSVOptions{Path: csvPath, Sections: sections}); err != nil {
            return fmt.Errorf("scan: write csv: %w", err)
        }

        fmt.Fprintf(os.Stderr, "scan: %d entries ≥ %s → %s\n",
            len(rows), svc.FormatBytes(threshold), csvPath)
        slog.Info("scan complete",
            "entries", len(rows),
            "threshold_bytes", threshold,
            "csv", csvPath)
        return nil
    },
}
```

CLI flags retained:

| flag             | default     | notes                                                |
| ---------------- | ----------- | ---------------------------------------------------- |
| `--date`         | today       | date tag for CSV filename                            |
| `--min-size`     | `200MB`     | uses existing `config.ParseSize` (KB/MB/GB/TB SI)    |
| `--top`          | 10          | future-proof; not yet consumed (kept for parity)     |
| `--progress / -p` | false      | reserved — no-op in this PR; will be wired later     |

Flags **removed**: `--roots`, `--incremental`, `--workers`, `--exclude`, `--exclude-append`, `--exclude-reset`, `--cold-mode`, `--interactive`, `--allow-mark`, `--apparent-size`.

### 2. Slim `handler/runner.go`

- **Delete** `RunScan` entirely (lines 102-318).
- **Delete** `SetRoots` (lines 70-79) — no longer used.
- **Delete** `renderProgressToStderr` + `truncatePath` (lines 415-439) — no consumer yet.
- **Slim** `Wire()`: drop `BufferedMetric` init (lines 46-50), drop the metric field on Runner, drop `Flush` in `Close`. Runner keeps `db` only.
- **Keep** `RunShow`, `RunDiff`, `LatestDate`, `ReadDate`, `rowsToEntries`, `home()`, `splitCSV` (still used by show/diff/tui).
- `ScanOpts` struct: delete entirely.

### 3. Delete now-dead service files

These were exclusively consumed by `RunScan`:

| file                          | reason                                              |
| ----------------------------- | --------------------------------------------------- |
| `svc/scanner.go`              | replaced by `du.Usage()`                            |
| `svc/collector.go`            | bridged scanner → DB; both removed                  |
| `svc/hasher.go`               | 3-stage xxHash dedup; only `RunScan` used it        |
| `svc/metrics.go`              | `BufferedMetric`; only `RunScan` used it            |
| `svc/scanner_test.go`         | mirror of deleted code                              |
| `svc/collector_test.go`       | mirror                                              |
| `svc/hasher_test.go`          | mirror                                              |

`svc/entry.go`, `svc/db.go`, `svc/csv_writer.go`, `svc/report.go`, `svc/render.go` and their tests stay — show/diff still depend on them.

### 4. Trim `config/config.go`

With scan gone, several settings fields are orphans:

- **Delete** fields: `BreakdownThreshold`, `ColdSizeThreshold`, `ColdAgeDays`, `OverviewRootDepth`, `OverviewHomeDepth`, `PrivacyRedact`, `PrivacyRedactPaths`, `ExcludeList`, `FollowSymlinks`, `Interactive`, `Roots`.
- **Delete** `ExpandRoots`, `expandHomeOrEnv`, `BreakdownThresholdBytes`, `ColdSizeThresholdBytes`.
- **Keep**: `Workers` (still used by scan? — no, drop), `TopN` (used by show/diff).
- `parseSize` + `ParseSize` stay — `cmd/scan.go` uses them for `--min-size`.
- Default settings JSON: drop the now-meaningless keys.
- `validate()` shrinks accordingly. SQLITE_PATH is still set (show/diff/tui need DB).

### 5. Wire-up

- `cmd/scan.go` imports `github.com/bizshuk/disk-analyzer/du` (the package lives at repo root, currently no other `cmd/*` imports it — verify module path matches `go.mod`).
- `svc.FormatBytes` exists per `render.go` — used in progress stderr log.

## Critical files modified

- `cmd/scan.go` — full rewrite
- `handler/runner.go` — delete `RunScan` body + `Wire` metric init
- `config/config.go` — slim Settings struct + `validate`
- `config/default_settings.json` — drop orphan keys
- `README.todo` — mark `Rewrite scan to du-based` done

## Critical files deleted

- `svc/scanner.go`, `svc/collector.go`, `svc/hasher.go`, `svc/metrics.go`
- `svc/scanner_test.go`, `svc/collector_test.go`, `svc/hasher_test.go`

## Verification

1. **Build**: `make build` — must compile without referencing deleted symbols.
2. **Smoke**: `./run.sh` then `./bin/du scan --min-size 200MB`. Expect:
   - stderr line `scan: N entries ≥ 200.0 MB → <CSVDir>/<date>.csv`
   - `<CSVDir>/<date>.csv` exists, has BOM + `# breakdown-home` section + rows.
3. **Schema check**: `head -5 ~/.config/disk-analyzer/data/<date>.csv` — columns are `path,size,change_size,is_duplicate,last_accessed,category`. Files & directories both present, sizes ≥ 200,000,000.
4. **Sub-size negative check**: `./bin/du scan --min-size 50GB` — output count < 200MB scan, no errors.
5. **Cancel**: `Ctrl-C` mid-scan — process exits cleanly, partial CSV is not written (single-shot at end).
6. **Negative**: show/diff/tui will print "no scans in DB yet" since no new entries are written. Document this in `README.todo` as a known follow-up.

## Out of scope (future PRs)

- Rewiring `show` / `diff` / `tui` to read from CSV instead of SQLite.
- Wiring `--progress` live counter (snapshot helper from old RunScan can be lifted if needed).
- Privacy redaction (paths like `~/.ssh`, `~/.aws` were redacted before; revisit when show/tui is fixed).
