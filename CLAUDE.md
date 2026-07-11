# CLAUDE.md

技術脈絡 (technical context) for the disk-analyzer Go CLI.

## 模組與分層

```
disk-analyzer/
├── main.go                  # entrypoint (config.Default + cmd.Execute)
├── config/                  # gosdk config 整合 + Settings struct
├── cmd/                     # cobra sub-commands (scan / show / diff / tui / version)
├── handler/                 # Runner DI container + 4 method (Scan/Show/Diff/TUI)
├── svc/                     # domain 邏輯 — 唯一商業邏輯層
│   ├── entry.go             # 資料型別
│   ├── scanner.go           # 並行 walk
│   ├── collector.go         # in-memory aggregate + cache lookup
│   ├── db.go                # SQLite via gosdk/db
│   ├── hasher.go            # 三階段 xxHash 去重
│   ├── report.go            # section builders + categorize
│   ├── csv_writer.go        # BOM + UTF-8 sanitize
│   ├── metrics.go           # BufferedMetric + JSONL replay
│   └── render.go            # 終端表格 + bar chart
└── tui/                     # bubbletea 互動介面
    ├── model.go
    ├── update.go
    ├── view.go
    ├── keymap.go
    └── styles.go
```

## 設計原則

1. **單次觸發**：所有命令是 cobra single-shot；不像 file_watcher 有長期 daemon
2. **DB 是單一 source of truth**：CSV 是 materialized view（給人看），DB 給程式查
3. **SQLite 統一**：cache 與 history 共用 `entries` 表（`path`, `scan_date` 為複合主鍵）
4. **隱私預設開啟**：`privacy_redact_paths` 黑名單 + `privacy_redact: true`
5. **不做 hard delete**：TUI 只能 mark（需 `--allow-mark` 顯式開啟）
6. **Symbol 採用 MixedCaps**：常數用 SCREAMING_SNAKE_CASE（gosdk 慣例）
7. **離線保護**：Metric 推失敗時 fallback 到 JSONL buffer（10MB cap）

## 關鍵決策

- **拒絕 `gosdk/statestore`**：因為 gosdk 沒有此套件；改用 `gosdk/db` (SQLite via gorm) 統一 cache + history
- **拒絕 `gosdk/parallel`**：手動 `errgroup` + `golang.org/x/sync/semaphore`
- **拒絕 `gosdk/cli`**：手動 cobra
- **拒絕 `gosdk/process` 守護**：排程由 pm2 處理
- **不寫 Loki 推送**：依賴 inf 的 promtail sidecar 收 stdout/stderr
- **CSV BOM**：加 `\xEF\xBB\xBF` 給 Excel；防禦性 UTF-8 sanitize（無效位元組換 `?`）

## gosdk 整合

- `config.Default(WithAppName("disk-analyzer"))` — 設定載入 + 自動建立 `~/.config/disk-analyzer/`
- `db.InitSQLite()` — singleton；`SQLITE_PATH` 必須在 `config.Default()` 之後用 `viper.Set` 設定
- `metric.NewMetricService("")` — 用預設 endpoint (`localhost:8428`)
- `metric.Send([]ScanSummaryMetric{...})` — 透過 `BufferedMetric` 推
- `log` 是 `log/slog` 標準介面，env `LOG_LEVEL` / `LOG_FORMAT` 控制

## 排程

`ecosystem.config.js` 週五 05:47 觸發：

```js
cron_restart: '47 5 * * 5'
autorestart: false,
max_retries: 0,
```

鏡像 `stock/ecosystem.config.js` 慣例。

## 開發

```bash
make build           # 編譯到 bin/disk-analyzer
make test            # go test ./...
make race            # go test -race ./...
make install         # 裝到 ~/.local/bin/disk-analyzer
./run.sh             # symlink ~/.config/disk-analyzer ./tmp/
```

## 規格與計畫

- 設計規格：`docs/spec/2026-07-11-disk-analyzer-design.md`
- 部署範本：`docs/daemon.md`（pm2 + launchd + systemd）
- 隱私策略：`docs/privacy.md`

## 待辦

見 `README.todo`。