# dux

高效能磁碟空間分析器 (single-shot disk usage analyzer) — 基於 `bizshuk/gosdk` 框架。

## 業務定義 (Business Definition)

`dux` 是給 macOS/Linux 桌面使用者用的單次深度磁碟分析工具，每日 06:00 由 pm2 排程觸發。產出：

- **CSV 報表**：每個掃描日寫入 `~/.config/dux/data/YYYY-MM-DD.csv`，
  列出 `$HOME` 下 ≥ 200MB 的檔案與資料夾（依 size desc 排序）
- 當前 v1 範圍：單一 `breakdown-home` section；其他 section / 去重 / 冷資料 / TUI 互動瀏覽已不在主路徑

> v1 簡化版後 `show` / `diff` / `tui` 暫時無新資料可讀（仍依賴 SQLite `entries` 表，
> 而 scan 已不再寫入）；見 `README.todo` 的追蹤項目。

## 與 file_watcher 的差異

| 工具           | 模式                                       | 用途                                            |
| -------------- | ------------------------------------------ | ----------------------------------------------- |
| `file_watcher` | 長期 daemon（排程監控 + 定期 flush stats） | 追蹤 watch_list 下的檔案數量/大小**增長趨勢**   |
| `dux`          | 單次觸發（每日 06:00 pm2 cron）            | 給出**當前** `$HOME` 磁碟使用全貌（CSV 給人看） |

兩個工具職責互補：file_watcher 回答「過去 N 天哪些路徑在長大」，
dux 回答「現在哪些東西可以清掉」。

## Domain Flow

```
pm2 cron (每日 06:00)
        │
        ▼
   dux scan
        │
        ├─► du.Usage: 並行 walk $HOME  ── (errgroup + semaphore in du/walk.go)
        │      │
        │      ├─ shouldEmit 過濾 depth=0 + threshold (default 200MB)
        │      └─ exclude.go 子字串比對 (node_modules / .git / .venv ...)
        │
        ├─► svc: map du.EntryInfo → ReportEntry
        │      └─ sort.Slice by Size desc
        │
        ├─► svc.WriteCSV  ─ BOM + UTF-8 sanitize + section comment
        │      └─ ~/.config/dux/data/YYYY-MM-DD.csv
        │
        └─► slog → stdout/stderr → promtail (inf) → Loki :3100
```

## 快速開始

```bash
# 1. 編譯
go build -o bin/dux .

# 2. 跑一次掃描（會覆寫當日 CSV）
./bin/dux scan                       # 預設 ≥ 200MB
./bin/dux scan --min-size 500MB      # 自訂門檻
./bin/dux scan --date 2026-07-11     # 指定日期檔名

# 3. 註冊 pm2（每日 06:00 觸發）
pm2 start ecosystem.config.js
pm2 save                  # 重要：persist cron 到 ~/.pm2/dump.json

# 4. 確認排程已 persist
pm2 list | grep dux
```

CLI flag 列表：`scan --help` 即可（`--date` / `--min-size` / `--top` 保留 / `--progress` 預留）。
詳細部署見 [docs/daemon.md](docs/daemon.md)（pm2 / launchd / systemd 三方案）。

## 設定

設定檔：`~/.config/dux/settings.json`（首次執行自動從 embedded default 產生）。
當前欄位（trim 後只剩三個）：

```json
{
    "privacy_redact_paths": [
        ".ssh",
        ".gnupg",
        ".aws",
        ".kube",
        ".docker",
        "Library/Keychains",
        "Library/Application Support/Keybase",
        ".config/gh/hosts.yml"
    ],
    "overview_root_depth": 2,
    "top_n": 10
}
```

完整預設值見 `config/default_settings.json`。
`DataDir` / `LogDir` / `SQLitePath` / `CSVDir` 由 `gosdk` 推導，不在 JSON 內。

## 輸出位置

| 路徑                                | 用途                                                          |
| ----------------------------------- | ------------------------------------------------------------- |
| `~/.config/dux/settings.json`       | 使用者設定（首跑自動建立）                                    |
| `~/.config/dux/data/YYYY-MM-DD.csv` | 當日 CSV 報表（含 BOM + section 註解）                        |
| `~/.config/dux/data/history.db`     | SQLite — 殘留供 `show` / `diff` / `tui` 讀，**scan 不再寫入** |
| `~/.config/dux/logs/`               | pm2 stdout/stderr（`dux.{out,err}.log`）                      |

## 觀測整合

- **Logs**：slog 寫 stdout/stderr；inf 的 promtail sidecar 收 → Loki `:3100`
- **Metrics**：v1 簡化版**不再推** VictoriaMetrics（`BufferedMetric` 已刪除）；
  純 slog 觀察即可，無需查詢 VM

## 安全 / 隱私

- 預設排除 `node_modules`, `.git`, `.venv`, `Library/Caches` 等常見大但無價值的目錄
- 預設 redact `.ssh`, `.aws`, `.kube`, `.docker`, `Library/Keychains`,
  `.config/gh/hosts.yml` 等敏感路徑（TUI 顯示為 `<redacted>`）
- TUI **永遠不做 hard delete**

詳見 [docs/privacy.md](docs/privacy.md)。

## 架構與慣例

- 模組路徑：`github.com/bizshuk/dux`
- Go 1.26
- 設定、目錄、log 走 `bizshuk/gosdk`
- CLI 走 cobra，domain 邏輯在 `svc/`，並行 walk 在 `du/`
- `go build -o bin/dux .` 編譯；`go test ./...` 跑單元測試
- **不**提供 Makefile；以 direct `go` 命令為主

詳見 [CLAUDE.md](CLAUDE.md) 技術脈絡。
