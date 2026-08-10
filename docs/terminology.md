# dux — 術語表 (Terminology)

本檔是領域名詞、狀態值與縮寫的單一定義來源。CLI、CSV 欄位與文件使用同一組正名。

## 掃描 (Scanning)

| 術語 (Term) | 英文 (English) | 定義 (Definition) | 出處 (Source) |
| --- | --- | --- | --- |
| 掃描根 | Root | 一次掃描的起點目錄；預設五個為 `$HOME`、`/tmp`、`/var`、`/Applications`、`/usr` | `cmd/scan.go`、`scanAllRoots()` |
| 單次觸發 | Single-shot | 執行完即結束的執行模式；dux 沒有常駐 daemon，排程由 pm2 cron 負責 | `CLAUDE.md` 設計原則 |
| 用量計算 | Usage | 對單一 root 做深度優先走訪並回報大小的核心動作 | `du.Usage()` |
| 門檻 | Threshold | 條目要被輸出所需的最小大小，預設 200MB | `du.Options`、`shouldEmit` |
| 排除規則 | Exclude Glob | 以 glob 樣式略過的路徑；支援 `**`、`*` 與 basename 比對 | `du/exclude.go` |
| 深度 | Depth | 條目相對於其 root 的層數；`depth=0` 即 root 本身，會被過濾掉 | `du/walk.go`、`shouldEmit` |
| 部分失敗 | Partial Failure | 某個 root 掃描失敗時只記 warning、保留其他 root 成功結果的行為 | `scanAllRoots()` |

## 輸出 (Output)

| 術語 (Term) | 英文 (English) | 定義 (Definition) | 出處 (Source) |
| --- | --- | --- | --- |
| 報表 | Report | 一次掃描的完整輸出，寫成單一 CSV 檔 | `svc/report.go` |
| 條目列 | Entry Row | CSV 中的一列，代表一個檔案或資料夾的大小紀錄 | `svc.EntryRow` |
| 區段 | Section | 報表內的邏輯分組；v1 僅使用單一 `breakdown-home` 區段以相容既有 consumer | `svc/report.go` section builders |
| 掃描日 | Scan Date | 報表的日期鍵，同時是檔名 `YYYY-MM-DD.csv` | `~/.config/dux/data/YYYY-MM-DD.csv` |
| 分類 | Category | 依路徑特徵替條目標上的用途標籤 | `svc.categorize` |

## 歷史資料 (Historical Data)

| 術語 (Term) | 英文 (English) | 定義 (Definition) | 出處 (Source) |
| --- | --- | --- | --- |
| 歷史資料庫 | History DB | 舊版寫入的 SQLite 檔；`show` / `diff` / `tui` 仍讀它，但`新的 scan 不再寫入` | `svc/db.go`、`history.db` |
| 差異 | Diff | 兩個掃描日之間的大小變化比較 | `cmd/diff.go`、`handler` |
| 互動介面 | TUI | bubbletea 互動瀏覽介面；待重接 CSV 資料來源 | `tui/` |

> `已知落差`：v1 簡化後 scan 只產 CSV，而 `show` / `diff` / `tui` 仍依賴 SQLite
> `entries` 表，因此暫時讀不到新資料。追蹤項目見 `README.todo`。

## 與相鄰工具的區別 (Disambiguation)

| 術語 (Term) | 定義 (Definition) |
| --- | --- |
| `dux` | 單次觸發，回答「`現在`哪些東西可以清掉」，輸出 CSV 給人看 |
| `file_watcher` | 長期 daemon，回答「`過去 N 天`哪些路徑在長大」，輸出趨勢統計 |

兩者職責互補，不得互相取代；在文件中混用這兩個名字是錯誤。
