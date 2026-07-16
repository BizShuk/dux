# Scan 多根目錄並行設計

日期：2026-07-16

## 目的

將 `dux scan` 的預設掃描範圍由單一 `$HOME` 擴充為：

- `$HOME`
- `/tmp`
- `/var`
- `/Applications`
- `/usr`

每個 root 使用一個 goroutine 執行掃描，使彼此獨立的檔案樹可並行處理。

## 範圍

- 僅調整 `scan` command 的預設掃描協調。
- 不改變通用 `du.Usage` API 或 `dux du` command 的既有語意。
- 所有成功 root 的 entries 合併至同一份每日 CSV。
- CSV schema、門檻、exclude 規則與輸出路徑維持不變。

## 設計

`scan` 建立固定 roots 清單，並為每個 root 啟動一個 goroutine。每個 goroutine 使用相同的 `du.Options`，但 `Paths` 僅包含自己的 root，然後把包含 `root`、`result`、`error` 的結果送回 channel。

主 goroutine 是唯一會修改合併 rows 的執行單元：

1. 接收每個 root 的結果。
2. 成功時將 `du.EntryInfo` 映射為 `svc.ReportEntry` 並合併。
3. 失敗時使用 `slog.Warn` 記錄 root 與 error，繼續接收其他結果。
4. 至少一個 root 成功時，依 size descending 排序並寫入同一份 CSV。
5. 所有 roots 都失敗時回傳 error，不建立空白 CSV。

此結構不共享可變 slice，避免 goroutine 間的 data race。共用 context 在收到 `SIGINT` 或 `SIGTERM` 時取消，所有正在掃描的 goroutine 都會透過 `du.Usage` 停止。

## 錯誤處理

- root 不存在、無法 stat 或其他 root-level error：記錄 warning，保留其他成功結果。
- root 內個別目錄無法讀取：沿用 `du` 現有行為，略過該目錄。
- 所有 roots 失敗：回傳包含失敗數量的 scan error。
- CSV 寫入失敗：維持現有 fail-fast 行為並回傳 wrapped error。

## 日誌

- 掃描開始時記錄 roots、root count 與 threshold。
- 單一 root 失敗時記錄 `root` 與 `error`。
- 完成時記錄成功 root 數、失敗 root 數、entry 數與 CSV 路徑。

## 測試

為避免測試直接掃描系統目錄，將並行協調抽成 package-private helper，注入可替代的單 root scan function。

測試涵蓋：

- 每個 root 恰好呼叫一次 scan function。
- 多個成功 root 的 entries 全部合併。
- 部分 root 失敗時仍回傳成功 entries。
- 所有 roots 失敗時回傳 error。
- 使用 race detector 驗證並行收集沒有 data race。

## 文件同步

更新 `README.md` 與 `CLAUDE.md`，將 `$HOME` 單一路徑敘述改為五個預設 roots，並說明每個 root 各用一個 goroutine 與 partial-success 行為。
