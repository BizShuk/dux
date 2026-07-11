# CLAUDE.md

技術脈絡 (technical context) for the disk-analyzer Go CLI。

## 模組與分層

```
disk-analyzer/
├── main.go                  # entrypoint (config.Default + cmd.Execute)
├── config/                  # gosdk config 整合 + Settings struct
├── cmd/                     # cobra sub-commands (scan / show / diff / tui / version)
├── du/                      # 並行 walk（核心 scan pipeline）
│   ├── du.go                # public API: Usage() + Options
│   ├── walk.go              # filepath.WalkDir 變體 + shouldEmit
│   ├── exclude.go           # glob exclude (bmatcuk/doublestar；支援 ** / * / basename)
│   └── stat_unix.go         # Lstat / capacity 計算
├── handler/                 # Runner DI container + show/diff/tui methods
├── svc/                     # domain 邏輯 — 寫 CSV / 報表 / render
│   ├── entry.go             # 資料型別 (EntryRow / ReportEntry)
│   ├── csv_writer.go        # BOM + UTF-8 sanitize
│   ├── report.go            # section builders + categorize
│   ├── render.go            # 終端表格
│   └── db.go                # SQLite（show/diff/tui 仍依賴；scan 不再寫入）
├── tui/                     # bubbletea 互動介面（待重接 CSV）
└── docs/                    # spec + daemon + privacy
```

> 2026-07-12 重構後：`svc/scanner.go` / `collector.go` / `hasher.go` / `metrics.go`
> 已刪除；scan pipeline 改走 `du.Usage()` → sort → CSV only。

## 設計原則

1. **單次觸發**：所有命令是 cobra single-shot；不像 file_watcher 有長期 daemon
2. **CSV 為主、DB 為輔**：scan 只產 CSV（`~/.config/<app>/data/YYYY-MM-DD.csv`）；
   `history.db` 殘留供舊版資料查詢，**新 scan 不再寫 DB**
3. **CSV schema 穩定**：`path, size, change_size, is_duplicate, last_accessed, category`
   + section comment `# <name>`，Excel 友善（BOM）
4. **隱私預設開啟**：`privacy_redact_paths` 黑名單；`tui` 顯示為 `<redacted>`
5. **不做 hard delete**：TUI 只能 mark
6. **Symbol MixedCaps**；const SCREAMING_SNAKE_CASE（gosdk 慣例）

## 關鍵決策

- **拒絕 `gosdk/statestore`**：改用 `gosdk/db` (SQLite via gorm)；目前 `entries` 表僅供向後相容
- **拒絕 `gosdk/parallel`**：手動 `errgroup` + `golang.org/x/sync/semaphore`（在 `du/walk.go`）
- **拒絕 `gosdk/cli`**：手動 cobra
- **拒絕 `gosdk/process` 守護**：排程由 pm2 處理
- **scan 不推 VictoriaMetrics**：v1 簡化版移除 `BufferedMetric`；觀測只剩 slog → stdout
- **CSV BOM**：加 `\xEF\xBB\xBF` 給 Excel；防禦性 UTF-8 sanitize（無效位元組換 `?`）
- **`du/` 唯一外部 dep**：`github.com/bmatcuk/doublestar/v4` 提供 `**` glob；其他仍是 stdlib + `golang.org/x/sys`

## gosdk 整合

- `config.Default(WithAppName("disk-analyzer"))` — 設定載入 + 自動建立 `~/.config/disk-analyzer/`
- `db.InitSQLite()` — singleton；`SQLITE_PATH` 在 `config.Default()` 之後用 `viper.Set` 設定
- `log` 是 `log/slog` 標準介面，env `LOG_LEVEL` / `LOG_FORMAT` 控制

## 排程

`ecosystem.config.js` 每日 06:00 觸發：

```js
cron_restart: '0 6 * * *'
autorestart: false,
max_retries: 0,
```

> **注意**：pm2 是 `~/.local/go/bin/pm2` 1.0.0（Go fork）。
> `apps[].args` 必須是 `[]string`（`['-lc', '...']`），不是單一字串；
> 字串會直接 `parse js exports: json: cannot unmarshal string ... into []string`。
> 註冊後必須 `pm2 save` 才會寫進 `~/.pm2/dump.json`，
> 否則 daemon 重啟時 cron 會消失。

鏡像 `stock/ecosystem.config.js` 慣例。

## 開發

```bash
go build -o bin/disk-analyzer .            # 編譯到 ./bin/
go test ./...                              # 跑單元測試
go test -race ./...                        # 含 race detector
go build -o ~/.local/bin/disk-analyzer .   # 安裝到 PATH（需 ~/.local/bin 在 $PATH）
./run.sh                                   # symlink ~/.config/disk-analyzer ./tmp/
```

> 專案**不**提供 Makefile；2026-07-12 由排程驗證時補上的決定。

## 規格與計畫

- 設計規格：`docs/spec/2026-07-11-disk-analyzer-design.md`（部分章節已被實際實作超越；見檔首「Superseded」標註）
- 部署範本：`docs/daemon.md`（pm2 + launchd + systemd）
- 隱私策略：`docs/privacy.md`

## 待辦

見 `README.todo`。
