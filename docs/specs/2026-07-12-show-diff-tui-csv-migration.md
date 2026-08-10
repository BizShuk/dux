# Plan: show / diff / tui 改讀 CSV，移除 SQLite 層

## Context

- 2026-07-11 commit `061ccef` 把 `scan` 改寫為 `du.Usage()` + 直接寫 CSV；不再寫 SQLite。
- `show` / `diff` / `tui` 仍走 `handler.Runner` → `svc.OpenDB()`，呼叫 `svc.DB.{LatestDate, StreamEntries, EntriesBetween, EntriesForDate}`。
- 結果：DB 永遠空著，三個指令都提早退出（`runner.go:71` "no scans in DB yet" / `tui.go:39` 同訊息）。
- `README.todo` 第 4 行已記這條；現在一次收掉。

目標：show / diff / tui 全部讀 `~/.config/disk-analyzer/data/<date>.csv`；移除整個 `svc.DB` 與測試；handler.Runner 變薄；一次性 commit。

## 為什麼要順手擴充 CSV schema

- `svc.BuildOverview`（給 `show`）與 `tui.buildTree`（給 `tui`）都需要 `IsDir` 與 `Depth` 兩個欄位；目前 CSV 沒寫這兩欄，reader 無從恢復。
- 同時 `cmd/scan.go:114` 把 `Depth == 0` 列為過濾條件，連帶 root 子節點 (`$HOME` 本身) 不進 CSV → TUI 無 root 可掛。
- 解法：CSV schema 順手加 `is_dir`、`depth` 兩欄，scan 不再丟 depth-0；舊 CSV 在 reader 端 graceful（缺欄位 → `IsDir=false`、`Depth=0`）。

## 改動清單

### 1. CSV schema 擴充

**`svc/csv_writer.go`**
- header 改為 `path,is_dir,depth,size,change_size,is_duplicate,last_accessed,category`（8 欄）。
- row 寫入順序比照新增欄位。
- file docstring 的範例註解同步更新（補上 `# <name>` 實際未輸出的現實）。

**`cmd/scan.go`**
- `EntryInfo → ReportEntry` 映射（line 112-124）填入 `IsDir: e.Info.IsDir`、`Depth: e.Info.Depth`。
- 取消 `e.Depth == 0` 過濾（line 114-116 整段刪），讓 root 與其所有子目錄全進 CSV。
- 既有 sort by size desc 維持。

**`svc/csv_writer_test.go`**
- `TestWriteCSV_BOMAndHeader` header 斷言更新為新欄位字串。
- 新增 roundtrip 小測資（若有需要）。

### 2. 新檔 `svc/csv_reader.go`

公開 API：
- `ReadCSV(path string) ([]ReportEntry, error)` — 解析單一 CSV；BOM 自動跳過；欄位不足時 `IsDir=false`、`Depth=0` 並 log warn。
- `LatestDate(csvDir string) (string, error)` — 列 `*.csv`，stem parse `2006-01-02`，回最大；無檔回 `("", ErrNoScans)`。
- `AvailableDates(csvDir string) ([]string, error)` — 列 `*.csv`，日期升冪，方便 `diff` 提示可用日期。

私有：`parseCSVRow`（欄位數判定 + 容忍缺欄）、`sanitizeTime`（RFC3339 容錯）。

**新增測試** `svc/csv_reader_test.go`：
- roundtrip：WriteCSV → ReadCSV 等於輸入（含 is_dir/depth）。
- 7-欄舊 CSV 讀回：IsDir=false、Depth=0、無 error。
- `LatestDate` 對空目錄 / 單檔 / 多檔三種情境。
- malformed date stem：略過不 panic。

### 3. `handler/runner.go` 縮減

- 移除 `db *svc.DB` 欄位。
- `Wire()` 不再開 DB，回傳 `*Runner` 只需 `cfg`。
- `(*Runner).Close()` 整段刪。
- `RunShow(ctx, date, prefix)`：
  1. `date == ""` → `csv.LatestDate(cfg.CSVDir)`，空目錄回 `ErrNoScans`。
  2. `rows := csv.ReadCSV(filepath.Join(cfg.CSVDir, date+".csv"))`。
  3. `overview := svc.BuildOverview(rows, "/", r.cfg.OverviewRootDepth, r.cfg.TopN)`（簽名改；見 §4）。
  4. 沿用既有 prefix 過濾 + `svc.PrintSections`。
- `RunDiff(ctx, from, to, prefix)`：
  1. `d1, d2 := csv.ReadCSV(...)` 兩份 CSV。
  2. `entriesFromCSV(d) []svc.Entry`（取代 DB 版 `rowsToEntries`，從 ReportEntry 欄位複製：Path/Size/LastAccessed/Depth，並設定 `IsDir=rows[i].IsDir`）。
  3. `diff := svc.DiffEntries(d2, d1)` → `svc.TopGrowth(...)` → 既有輸出格式。
- `LatestDate()` → 直接呼叫 `csv.LatestDate(cfg.CSVDir)`。
- `ReadDate(date)` 改回傳 `[]svc.EntryRow`（給 `tui.NewModel`），內部用 `entriesToRows` 把 ReportEntry 轉成 EntryRow（Path/IsDir/SizeBytes/Depth）。
- 刪掉 `rowsToEntries`（DB→Entry bridge）與 dead `retainDateShape`（line 163-170 從未被呼叫）。

### 4. `svc/report.go` signature 簡化

- `BuildOverview(stream func(cb func(EntryRow) error) error, root string, maxDepth int, topN int) []ReportEntry`
  → `BuildOverview(entries []ReportEntry, root string, maxDepth int, topN int) []ReportEntry`
- 內部用 `du.EntryInfo`-風格的逐筆處理改為對 `ReportEntry` 直接操作（Path/Size/IsDir/Depth 已存在；`LastAccessed`/`ChangeSize` 直接轉欄位命名）。
- 順手刪 `BuildBreakdown` / `BuildCold` / `AttachDuplicates` / `DuplicateSectionEntries` — agent 探勘已確認「無 production caller」。`DiffEntries` / `TopGrowth` 保留。

### 5. 移除 SQLite 層

- 刪 `svc/db.go`（269 行；gorm/sqlite driver 依賴一併退役）。
- 刪 `svc/db_test.go`（137 行）。
- 從 `config.Settings` 移除 `SQLitePath` 欄位；`config.validate()` 同步拿掉 `SQLitePath` 驗證邏輯。
- `go.mod`：`gorm.io/driver/sqlite`、`gorm.io/gorm` 若無其他用途一併 `go mod tidy`。

### 6. `cmd/{show,diff,tui}.go` 配套

- 三檔 `init()` 與 cobra 設定不動；只驗證 import 還能編（不再引用 `svc.EntryRow` 以外的 DB 形態）。
- `cmd/tui.go:41` 從 `r.ReadDate(date)` 取資料；現在回 `[]EntryRow`（Row 來自 CSV→ReportEntry→EntryRow adapter），TUI 契約不破。
- `cmd/show.go` Long 描述 `ShowCmd`：把 "latest in DB" 文案改成 "latest scan CSV"。

### 7. 文檔 / 規格

- `svc/csv_writer.go` docstring：修掉 outdated `# <name>` 範例；列出實際 8 欄 schema；註明 reader 對 6-欄舊檔的容錯行為。
- `docs/specs/2026-07-11-disk-analyzer-design.md` 補一段 "Schema v2 (post DB-removal)"。
- `README.todo` 第 4 行改為 `[x]`，加上 `@Completed` 時間戳。

## 驗證

```bash
go build -o bin/disk-analyzer .       # 全編譯乾淨
go vet ./...                          # 抓型別錯誤
go test ./...                         # roundtrip + reader + report
go test -race ./...                   # race detector

# 端到端煙霧
./bin/disk-analyzer scan --date 2026-07-12          # 寫新 schema CSV
./bin/disk-analyzer show --date 2026-07-12          # overview 渲染
./bin/disk-analyzer diff --from 2026-07-11 --to 2026-07-12   # 雙日 diff
./bin/disk-analyzer tui --date 2026-07-12           # tree 結構

# 向後相容（手動）
# 從 snapshot 拿一份 6-欄舊 CSV 放進 data/；
# ./bin/disk-analyzer show --date <old>  → 不掛、列條目（無 IsDir/Depth）
# ./bin/disk-analyzer tui --date <old>   → flat list、無 crash
```

## 關鍵決策一覽

| 議題 | 選擇 |
| --- | --- |
| CSV 是否加 is_dir / depth | **是**（show 與 tui 需要） |
| scan 是否保留 depth-0 drop | **否**（TUI 需要 root） |
| DB 層處置 | **整個刪除**（db.go + db_test.go） |
| tui.NewModel 簽名 | **保留** `[]svc.EntryRow`（runner 內做 CSV→Row 轉接） |
| BuildOverview 簽名 | 改為 `[]ReportEntry`（擺脫 streamer 耦合） |
| Delete dead report helpers | 刪 `BuildBreakdown` / `BuildCold` / `AttachDuplicates` / `DuplicateSectionEntries` |
| 提交粒度 | 1 個 commit（user 指定） |
