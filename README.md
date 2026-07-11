# disk-analyzer

高效能磁碟空間分析器 (single-shot disk usage analyzer) — 基於 `bizshuk/gosdk` 框架。

## 業務定義 (Business Definition)

`disk-analyzer` 是給 macOS/Linux 桌面使用者用的單次深度磁碟分析工具，每週由 pm2 排程觸發。產出：

- **Overview**：根目錄 `/` 2 層、家目錄 `~/` 1 層概覽
- **Breakdown**：> 200MB 的檔案與資料夾；> 200MB 的資料夾會遞迴拆解
- **Duplicate**：相同大小的檔案以 xxHash 偵測內容重複
- **Cold data**：> 180 天未存取且 > 100MB 的檔案
- **CSV 報表**：每個 section 寫入 `~/.config/disk-analyzer/data/YYYY-MM-DD.csv` 供離線檢視
- **TUI**：互動式樹狀瀏覽器，支援標記待處理（**不做 hard delete**）

## 與 file_watcher 的差異

| 工具 | 模式 | 用途 |
|---|---|---|
| `file_watcher` | 長期 daemon（排程監控 + 定期 flush stats） | 追蹤 watch_list 下的檔案數量/大小**增長趨勢** |
| `disk-analyzer` | 單次觸發（每週 pm2 cron） | 給出**當前**磁碟使用全貌 + 重複/冷資料分析 |

兩個工具職責互補：file_watcher 回答「過去 N 天哪些路徑在長大」，disk-analyzer 回答「現在哪些東西可以清掉」。

## Domain Flow

```
pm2 cron (週五 05:47)
        │
        ▼
   ./disk-analyzer scan
        │
        ├─► scanner: 並行 walk /, $HOME  ── (errgroup + semaphore + exclude + privacy)
        │
        ├─► collector: 寫入 SQLite entries 表（cache + history 共表）
        │
        ├─► hasher: size → 64KB header xxHash → full xxHash 三階段去重
        │
        ├─► report: 組裝 Overview / Breakdown / Cold / Duplicate sections
        │
        ├─► csv_writer: 寫 ~/.config/disk-analyzer/data/YYYY-MM-DD.csv
        │
        ├─► render: 印到 stdout（path 升冪排序）
        │
        └─► metric: 推 VictoriaMetrics (push to :8428)
              log:   寫 stdout/stderr → promtail → Loki
```

## 快速開始

```bash
# 1. 編譯
make build

# 2. 第一次掃描
./bin/disk-analyzer scan --interactive=false

# 3. 查看當次結果
./bin/disk-analyzer show

# 4. 比較前後兩次
./bin/disk-analyzer diff --from 2026-07-04 --to 2026-07-11

# 5. 互動瀏覽（TTY 自動偵測）
./bin/disk-analyzer scan              # TTY 環境啟 TUI
./bin/disk-analyzer scan --interactive=false  # CI / pm2 用

# 6. 註冊 pm2
pm2 start ecosystem.config.js
```

## 設定

設定檔：`~/.config/disk-analyzer/settings.json`（首次執行自動從 embedded default 產生）

```json
{
    "roots": ["/", "$HOME"],
    "exclude_list": ["node_modules", ".git", "..."],
    "privacy_redact_paths": [".ssh", ".aws", "..."],
    "workers": 4,
    "breakdown_threshold": "200MB",
    "cold_size_threshold": "100MB",
    "cold_age_days": 180,
    "overview_root_depth": 2,
    "overview_home_depth": 1,
    "top_n": 10,
    "privacy_redact": true,
    "follow_symlinks": false,
    "interactive": "auto"
}
```

完整設定範例見 `config/default_settings.json`。

## 輸出位置

| 路徑 | 用途 |
|---|---|
| `~/.config/disk-analyzer/settings.json` | 使用者設定（首跑自動建立） |
| `~/.config/disk-analyzer/data/history.db` | SQLite — entries 表（cache + history） |
| `~/.config/disk-analyzer/data/2026-07-11.csv` | 當日 CSV 報表 |
| `~/.config/disk-analyzer/data/metric-buffer.jsonl` | Metric 離線 buffer（VM 離線時 replay） |
| `~/.config/disk-analyzer/log/` | log 目錄（pm2 / launchd 接管） |

## 觀測整合

- **Metrics**：掃描結束推 `disk_analyzer_total_bytes` / `disk_analyzer_total_files` / `disk_analyzer_max_dir_size` / `disk_analyzer_scan_duration_ms` / `disk_analyzer_cache_hits` / `disk_analyzer_warnings_total` 至 VictoriaMetrics `:8428`
- **Logs**：所有 slog 寫 stdout/stderr；inf 的 promtail sidecar 收 → Loki `:3100`
- **離線保護**：Metric 推失敗時暫存到 `data/metric-buffer.jsonl`（10MB cap），下次啟動 replay

## 安全 / 隱私

- 預設排除 `node_modules`, `.git`, `.venv`, `Library/Caches` 等常見大但無價值的目錄
- 預設 redact `.ssh`, `.aws`, `.kube`, `.docker` 等敏感路徑（CSV 與 log 中顯示 `<redacted:contains-private>`）
- TUI **永遠不做 hard delete**，僅提供 `m` 標記（需 `--allow-mark` 開啟）

詳見 [docs/privacy.md](docs/privacy.md)。

## 架構與慣例

- 模組路徑：`github.com/bizshuk/disk-analyzer`
- Go 1.26
- 設定、目錄、log、metric、db 一律走 `bizshuk/gosdk`
- CLI 走 cobra，DI 走 `handler.Wire()`，domain 邏輯在 `svc/`，互動在 `tui/`
- 寫 `make` 編譯、`make test` 跑單元測試、`make install` 裝到 `~/.local/bin`

詳見 [CLAUDE.md](CLAUDE.md) 技術脈絡。