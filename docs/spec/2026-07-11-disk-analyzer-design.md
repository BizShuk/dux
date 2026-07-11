# disk-analyzer — 設計規格

日期：2026-07-11
狀態：**部分章節已被實際實作超越**（見下方「實作後變更」）

> ⚠️ Superseded（部分）
>
> 2026-07-11 寫定的「v1 全功能」設計在 2026-07-12 由一次重構大幅簡化
> （scan 改 du-based + CSV only；移除去重 / 冷資料 / metric buffer）。
> 本檔保留作為**設計意圖與決策脈絡**，但 § 2 / § 3.4 / § 3.5 / § 3.6 /
> § 3.7 / § 3.8 已不反映當前實作；請對照 `CLAUDE.md` 的「設計原則」與
> 「關鍵決策」讀取實際狀態。

## 1. 動機

磁碟分析是週尺度事件（CI artifact 下載、cache 累積、VM image 膨脹）。現有工具 `du`、`ncdu` 缺少：

- 並行掃描（10TB 在 HDD 上要 30 分鐘）
- 增量快取（重複掃描浪費 IO）— *（已移除）*
- 重複偵測（手動 `fdupes` 對 10TB 太慢）— *（已移除）*
- 冷資料識別（半年沒用的大檔）— *（已移除）*
- TUI 互動瀏覽 — *（待重接 CSV）*
- 觀測整合（VM + Loki）— *（VM metric 已移除；僅 slog）*
- 排程友善（pm2 / launchd / systemd）

## 2. 架構

> ⚠️ 本節已被超越。

```
cmd/*.go  ── cobra CLI entry
    │
    ▼
du.Usage() ── 並行 walk $HOME (errgroup + semaphore)
    │
    ▼
sort + filter (depth=0 skip, threshold ≥ 200MB)
    │
    ▼
svc.WriteCSV  ── BOM + UTF-8 sanitize + section comment
    │
    ▼
~/.config/disk-analyzer/data/YYYY-MM-DD.csv
```

歷史版本（2026-07-12 重構前）的 `svc.RunScan() ── scan → collect → hash → report → csv → metric` pipeline 已不存在。

## 3. 決策紀錄

### 3.1 gosdk 套件採用

> ⚠️ `db` 採用狀態更新：scan 已不寫 DB；`gosdk/db` 殘留供 `show` / `diff` / `tui` 相容舊資料。

| 套件 | 採用 | 理由 |
|---|---|---|
| `config` | ✅ | 設定載入 + GetAppDataDir() / GetAppLogDir() |
| `log` | ✅ | slog init via `import _` |
| `metric` | ⚠️ 殘留 | scan 不再推 VM；保留供未來重接 |
| `db` | ⚠️ 殘留 | SQLite singleton；scan 不寫，僅供 show/diff/tui 舊資料相容 |
| `notify` | ❌ | CLI 工具不需要 |
| `scheduler` | ❌ | 單次觸發，pm2 處理週期 |
| `router`, `mw`, `server` | ❌ | HTTP 工具用不到 |
| `statestore` | ❌ | **不存在**；原本打算改用 `db` 統一 cache + history |

### 3.2 「statestore」替代方案

> ⚠️ 本節的雙軌方案已不再適用。當前只有 CSV（scan 唯一寫入源），
> SQLite `entries` 表為向後相容殘留。

| 方案 | 採用 | 理由 |
|---|---|---|
| SQLite 單一 `entries` 表（cache + history 共表） | ⚠️ 殘留 | 簡單；單一 source of truth — *但 scan 已不寫* |
| CSV only（du.Usage → WriteCSV） | ✅ 當前主路徑 | 新 scan 只寫 CSV；其他指令待重接 |
| JSON + SQLite 雙軌 | ❌ | 一致性風險 |
| 純 JSON | ❌ | 大目錄（> 10K entries）慢 |

### 3.3 並行模型

`errgroup` + `golang.org/x/sync/semaphore` 限制 workers 數。

`filepath.WalkDir` 預設用 `Lstat` 語意，symlink 不遞迴（安全）。

> 細節見 `du/walk.go`；使用者設定的 `roots` 與 `workers` 等欄位
> 已在 2026-07-12 config 精簡時移除，改為硬編碼 `$HOME` + 預設 5 workers。

### 3.4 重複偵測 pipeline

> ⚠️ **已移除**（`svc/hasher.go` 連同測試一起刪除）。

| Stage | 動作 | IO 量 |
|---|---|---|
| 1 | group by size | 0 |
| 2 | hash 64KB header (xxHash) | N files × 64KB |
| 3 | hash full file (xxHash, 4MB buffer) | N files × avg size |

### 3.5 CSV 編碼

> ✅ 本節仍有效。

- BOM (`\xEF\xBB\xBF`) 給 Excel 開啟
- `utf8.ValidString()` 驗證 path；無效位元組換 `?`
- 每個 section 開頭 `# <name>` 註解行
- 欄位：`path, size, change_size, is_duplicate, last_accessed, category`

### 3.6 Metric 離線 buffer

> ⚠️ **已移除**（`svc/metrics.go` + `BufferedMetric` 整套刪除）。

`metric.Send()` 無 retry / buffer；網路斷就掉資料。原本 disk-analyzer 加 `BufferedMetric`：

- in-memory cap 1000 metrics
- 5 秒 flush 一次
- 失敗 → 寫入 `data/metric-buffer.jsonl`
- jsonl 超 10MB → 砍最舊一半
- 啟動時 replay

### 3.7 觀測鏈

> ⚠️ VM push 已移除。

```
disk-analyzer
    ├─► VictoriaMetrics :8428 (push, via gosdk/metric)  ← 已移除
    └─► stdout/stderr (slog) ──► promtail sidecar ──► Loki :3100
```

Loki 不由 disk-analyzer 主動 push（避免引入 retry / batching 邏輯）。

### 3.8 pm2 排程

> ✅ 排程已更新為每日 06:00。

```js
cron_restart: '0 6 * * *'  // 每日 06:00（先前為週五 05:47）
autorestart: false,
max_retries: 0,
```

註冊後務必 `pm2 save` 讓 cron 寫進 `~/.pm2/dump.json`。

## 4. TUI 設計

> ⚠️ TUI 程式碼仍在 `tui/`，但當前 `scan` 不再寫 SQLite，所以 TUI 找不到新資料；
> 重接 CSV 前先不要把 TUI 推上 pm2。

- bubbletea + lipgloss
- tree 攤平為 flat list（v1 簡化版）
- 鍵位：`j/k` 移動、`l/h` 展開收合、`m` mark（需 `--allow-mark`）、`q` 離開
- 永不實作 hard delete
- `--allow-mark=false` 時 mark 鍵禁用

## 5. 隱私

- 預設排除常見 cache 目錄（在 `du/exclude.go`）
- 預設 redact `.ssh`, `.aws`, `.kube`, `.docker`, `Library/Keychains`,
  `Library/Application Support/Keybase`, `.config/gh/hosts.yml` 等敏感路徑
- TUI 不提供 delete 動作
- 詳見 `docs/privacy.md`

## 6. 風險與緩解

| 風險 | 緩解 |
|---|---|
| 10TB 掃描 OOM | `Entry` 只保留必要欄位；新版直接 stream `du.Usage()` → sort → write，不再 in-memory 累積整個 tree |
| Permission denied 累積 | `du.Usage` 內部吞 warning + stderr |
| Symlink cycle | `filepath.WalkDir` Lstat 安全 |
| VM image 假副檔名 | `CategorizeByContent` 對 qcow2/vmdk 做 magic bytes 識別 |
| 隱私資料外洩 | redact 黑名單 + TUI 無 delete |

## 7. 未來工作

見 `README.todo`。
