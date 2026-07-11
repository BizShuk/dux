# disk-analyzer — 設計規格

日期：2026-07-11
狀態：v1 設計定稿

## 1. 動機

磁碟分析是週尺度事件（CI artifact 下載、cache 累積、VM image 膨脹）。現有工具 `du`、`ncdu` 缺少：

- 並行掃描（10TB 在 HDD 上要 30 分鐘）
- 增量快取（重複掃描浪費 IO）
- 重複偵測（手動 `fdupes` 對 10TB 太慢）
- 冷資料識別（半年沒用的大檔）
- TUI 互動瀏覽
- 觀測整合（VM + Loki）
- 排程友善（pm2 / launchd / systemd）

## 2. 架構

```
cmd/*.go  ── cobra CLI entry
    │
    ▼
handler.Wire() ── DI: cfg + db + buffered_metric
    │
    ▼
svc.RunScan() ── scan → collect → hash → report → csv → metric
    │
    ▼
~/.config/disk-analyzer/{data, logs, settings.json}
```

## 3. 決策紀錄

### 3.1 gosdk 套件採用

| 套件 | 採用 | 理由 |
|---|---|---|
| `config` | ✅ | 設定載入 + GetAppDataDir() |
| `log` | ✅ | slog init via `import _` |
| `metric` | ✅ | VictoriaMetrics push |
| `db` | ✅ | SQLite singleton |
| `notify` | ❌ | CLI 工具不需要 |
| `scheduler` | ❌ | 單次觸發，pm2 處理週期 |
| `router`, `mw`, `server` | ❌ | HTTP 工具用不到 |
| `statestore` | ❌ | **不存在**；改用 `db` |

### 3.2 「statestore」替代方案

| 方案 | 採用 | 理由 |
|---|---|---|
| SQLite 單一 `entries` 表（cache + history 共表） | ✅ | 簡單；單一 source of truth |
| JSON + SQLite 雙軌 | ❌ | 一致性風險 |
| 純 JSON | ❌ | 大目錄（> 10K entries）慢 |

### 3.3 並行模型

`errgroup` + `golang.org/x/sync/semaphore` 限制 workers 數。

`filepath.WalkDir` 預設用 `Lstat` 語意，symlink 不遞迴（安全）。

### 3.4 重複偵測 pipeline

| Stage | 動作 | IO 量 |
|---|---|---|
| 1 | group by size | 0 |
| 2 | hash 64KB header (xxHash) | N files × 64KB |
| 3 | hash full file (xxHash, 4MB buffer) | N files × avg size |

stage 1 立即輸出 group（包含所有 size-collision）。
stage 2 header 不撞 → 視為 distinct。
stage 3 全檔 hash 不撞 → 視為 distinct。

### 3.5 CSV 編碼

- BOM (`\xEF\xBB\xBF`) 給 Excel 開啟
- `utf8.ValidString()` 驗證 path；無效位元組換 `?`
- 每個 section 開頭 `# <name>` 註解行
- 欄位：`path, size, change_size, is_duplicate, last_accessed, category`

### 3.6 Metric 離線 buffer

`metric.Send()` 無 retry / buffer；網路斷就掉資料。disk-analyzer 加 `BufferedMetric`：

- in-memory cap 1000 metrics
- 5 秒 flush 一次
- 失敗 → 寫入 `data/metric-buffer.jsonl`
- jsonl 超 10MB → 砍最舊一半
- 啟動時 replay

### 3.7 觀測鏈

```
disk-analyzer
    ├─► VictoriaMetrics :8428 (push, via gosdk/metric)
    └─► stdout/stderr (slog) ──► promtail sidecar ──► Loki :3100
```

Loki 不由 disk-analyzer 主動 push（避免引入 retry / batching 邏輯）。

### 3.8 pm2 排程

每週五 05:47（offset 47 分鐘避開 :00 thundering herd）。

## 4. TUI 設計

- bubbletea + lipgloss
- tree 攤平為 flat list（v1 簡化版）
- 鍵位：`j/k` 移動、`l/h` 展開收合、`m` mark（需 `--allow-mark`）、`q` 離開
- 永不實作 hard delete
- `--allow-mark=false` 時 mark 鍵禁用

## 5. 隱私

- 預設排除常見 cache 目錄
- 預設 redact `.ssh`, `.aws`, `.kube`, `.docker` 等敏感路徑
- TUI 不提供 delete 動作
- 詳見 `docs/privacy.md`

## 6. 風險與緩解

| 風險 | 緩解 |
|---|---|
| 10TB 掃描 OOM | `Entry` 只保留必要欄位；DB 寫入用 chunked transaction |
| Permission denied 累積 | `addWarning` slice + stderr + metric |
| Symlink cycle | `filepath.WalkDir` Lstat 安全 |
| VM image 假副檔名 | `CategorizeByContent` 對 qcow2/vmdk 做 magic bytes 識別 |
| 隱私資料外洩 | redact 黑名單 + TUI 無 delete |

## 7. 未來工作

見 `README.todo`。