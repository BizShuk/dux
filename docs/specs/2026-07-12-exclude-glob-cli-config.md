# Plan: 自訂 exclude glob（CLI + config 雙介面）

## Context

`README.todo` 記錄了一個 1.x 待辦：「支援自訂 exclude glob（目前只支援精確與子字串比對）」。

實際檢視 `du/exclude.go` 後發現**描述已過時**：library 內部早就在用 `filepath.Match` 處理 glob（`*`、`?`、`[abc]`、`Library/Caches` 之類的路徑 glob 都行），也支援從檔案讀 patterns。真正缺的是**使用者介面層** — `cmd/scan.go` 沒暴露任何 flag，`config.Settings` 沒有 excludes 欄位，`default_settings.json` 也沒有。也就是說，使用者目前**完全沒有辦法**指定排除規則，所有 glob 邏輯雖然寫好了但形同孤兒。

目標：把這些既有但被埋沒的能力接到 CLI 與 config，使用者可以：

- 放 `~/.config/disk-analyzer/settings.json` 設常用的持久排除清單
- 用 `scan --exclude node_modules --exclude-from ~/excludes.txt` 臨時覆蓋或追加

## Scope 決策（已與使用者確認）

- **範圍**：CLI flag + config 都支援；兩者合併後傳給 `du.Options`
- **預設值**：`default_settings.json` 留空陣列，不主動猜測使用者想排除什麼
- **glob 引擎**：沿用既有的 `filepath.Match`（不引入 doublestar），範圍限縮在「讓現有機制真的能用」

## 設計

### 合併優先序（高 → 低）

```golang
final Excludes      = append(settings.Excludes, scanFlagExcludes...)  // 兩者相加
final ExcludeFrom   = scanFlagExcludeFrom                              // CLI 單值覆蓋
                     若空 → settings.ExcludeFrom
```

- `Excludes` 是 additive（兩個來源都貢獻 patterns），合併合理
- `ExcludeFrom` 是單一檔案路徑，無法合併；CLI 有值就用 CLI，沒設才退回 config

### 檔案異動清單

| 檔案                           | 異動                                                                                                                                                                                                       |
| ------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `config/config.go`             | `Settings` 加 `Excludes []string` 與 `ExcludeFrom string` 兩個欄位；`validate()` 內對 `ExcludeFrom` 做 `expandHomeOrEnv`（與 `PrivacyRedactPaths` 同手法）                                                 |
| `config/default_settings.json` | 加 `"excludes": []`、`"exclude_from": ""` 兩個 key（讓 schema 顯式存在）                                                                                                                                   |
| `config/config_test.go`        | 新增 `TestSettings_ExcludeFields` 驗證 unmarshal 正確、`ExcludeFrom` 有 `~` 展開                                                                                                                           |
| `cmd/scan.go`                  | 加 `scanExcludes []string` 與 `scanExcludeFrom string` 兩個 flag var；註冊 `--exclude`（`StringSliceVarP`，可重複）與 `--exclude-from` flag；組裝 `duOpts.Excludes` 與 `duOpts.ExcludeFrom` 時套用合併邏輯 |
| `du/du_test.go`                | 已有的 `TestUsage_Exclude` 涵蓋 glob 行為；新增 `TestUsage_ExcludeFrom` 驗證檔案載入、`TestExcludeSet_PathGlob` 驗證 `Library/Caches` 等含 slash 的 pattern 同時匹配完整路徑與子樹                         |
| `README.todo`                  | 把該項標為完成，附時間戳                                                                                                                                                                                   |

### 關鍵 code 片段

**`config/config.go` — Settings struct 擴充**

```go
type Settings struct {
    TopN               int      `json:"top_n" mapstructure:"top_n"`
    OverviewRootDepth  int      `json:"overview_root_depth" mapstructure:"overview_root_depth"`
    PrivacyRedactPaths []string `json:"privacy_redact_paths" mapstructure:"privacy_redact_paths"`
    Excludes           []string `json:"excludes" mapstructure:"excludes"`
    ExcludeFrom        string   `json:"exclude_from" mapstructure:"exclude_from"`
    // ... derived paths 維持不變
}
```

**`config/config.go` — validate() 加 ExcludeFrom 展開**

```go
// ExcludeFrom 是絕對檔案路徑，要展開 ~ / $HOME
if s.ExcludeFrom != "" {
    s.ExcludeFrom = expandHomeOrEnv(s.ExcludeFrom)
}
```

**`cmd/scan.go` — flag 註冊**

```go
var (
    scanExcludes     []string
    scanExcludeFrom  string
)

// in init():
ScanCmd.Flags().StringSliceVar(&scanExcludes, "exclude", nil,
    "glob pattern to exclude (repeatable); merged with config excludes")
ScanCmd.Flags().StringVar(&scanExcludeFrom, "exclude-from", "",
    "path to file of exclude patterns (one per line); overrides config exclude_from")
```

**`cmd/scan.go` — 合併後傳給 du.Options**

```go
// config first, CLI appended
merged := make([]string, 0, len(cfg.Excludes)+len(scanExcludes))
merged = append(merged, cfg.Excludes...)
merged = append(merged, scanExcludes...)

excludeFrom := scanExcludeFrom
if excludeFrom == "" {
    excludeFrom = cfg.ExcludeFrom
}

duOpts := du.Options{
    Paths:        []string{home},
    AllFiles:     true,
    Threshold:    threshold,
    MaxDepth:     math.MaxInt,
    Excludes:     merged,
    ExcludeFrom:  excludeFrom,
}
```

**`du/du_test.go` — 新增測試**

```go
func TestUsage_ExcludeFrom(t *testing.T) {
    root := makeTree(t)

    // Write an exclude file with *.txt pattern.
    exclFile := filepath.Join(t.TempDir(), "excludes.txt")
    require.NoError(t, os.WriteFile(exclFile, []byte("*.txt\n# comment\n"), 0o644))

    result, err := Usage(context.Background(), Options{
        Paths:        []string{root},
        ApparentSize: true,
        AllFiles:     true,
        MaxDepth:     -1,
        ExcludeFrom:  exclFile,
    })
    require.NoError(t, err)

    for _, e := range result.Entries {
        assert.NotContains(t, e.Path, ".txt",
            "exclude-from should drop .txt entries: %s", e.Path)
    }
}

func TestExcludeSet_PathGlob(t *testing.T) {
    // Patterns with "/" match against full path.
    excl := newExcludeSet([]string{"sub/deep"})
    assert.True(t, excl.match("/root/sub/deep/d.txt", "deep"),
        "path-pattern should match full path")
    assert.False(t, excl.match("/root/sub/c.txt", "c.txt"),
        "path-pattern should not match unrelated path")
}
```

## Verification

1. **單元測試**：`go test -race ./...` 應全綠
    - 新增 `TestSettings_ExcludeFields`、`TestUsage_ExcludeFrom`、`TestExcludeSet_PathGlob`
2. **編譯**：`go build -o bin/disk-analyzer .` 應乾淨
3. **手動煙霧測試**：
    - 在 `/tmp/scan-test/` 建一個 `node_modules/foo.txt` (10MB) 與 `keepme/bar.txt` (10MB)
    - `disk-analyzer scan --roots /tmp/scan-test --exclude 'node_modules'` → CSV 不應含 `node_modules/foo.txt`
    - `disk-analyzer scan --roots /tmp/scan-test --exclude-from /tmp/excl.txt`（檔案寫 `node_modules`）→ 同上
    - 在 `~/.config/disk-analyzer/settings.json` 加 `"excludes": ["node_modules"]`，scan 不帶 flag → 同上
4. **向後相容**：default_settings.json 改動只新增 key、不刪除既有 key；現有使用者的 settings.json 沒 excludes 欄位時 Go 會用 nil/空字串，行為與目前完全相同

## Out of scope

- **doublestar `**`支援**：本次不動`du/exclude.go` 的 glob 引擎；`filepath.Match` 已能覆蓋 80% 場景，`\*\*` 是獨立增強
- **硬編碼預設排除**（如 `Library/Caches`、`node_modules`）：使用者決定不要預設
- **`du/exclude.go` 的 basename fallback 邏輯**：觀察到 `match()` 在含 slash 的 pattern 上會額外試 `filepath.Base(fullPath)`，理論上可能誤判但實務難以觸發；本次不動，避免 scope 蔓延

## Critical files

- `du/exclude.go`（既有 glob 引擎，**不動**，僅驗證它能用）
- `du/du.go`（既有 `Options.Excludes` / `Options.ExcludeFrom`，**不動**）
- `du/walk.go`（既有呼叫端，**不動**）
- `config/config.go`（**改**：加兩個欄位 + `ExcludeFrom` 的 home 展開）
- `config/default_settings.json`（**改**：加兩個預設 key）
- `config/config_test.go`（**改**：加新欄位測試）
- `cmd/scan.go`（**改**：加 flag + 合併邏輯）
- `du/du_test.go`（**改**：加 exclude-from + path-glob 測試）
- `README.todo`（**改**：勾掉該項）
  xclude-from + path-glob 測試）
- `README.todo`（**改**：勾掉該項）
