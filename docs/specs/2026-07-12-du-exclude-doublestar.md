# Plan: `du/exclude.go` 加 `**` 支援

## Context

延續上一個 todo「支援自訂 exclude glob」：介面（CLI + config）已接通，但底層 `du/exclude.go` 的 glob 引擎用 stdlib `filepath.Match`，**`%60*%60` 不跨 separator** — 寫 `Library/Caches` 不會匹配 `/Users/me/Library/Caches/foo.txt`，寫 `**/node_modules` 完全沒作用（`**` 被當成兩個連續的 `*`，按字面比對失敗）。

實際使用情境：
- `**/node_modules` — 任一深度的 `node_modules`
- `**/.git` — 任一深度的 `.git`
- `foo/**/bar` — `foo` 之下任一層的 `bar`
- `**/*.log` — 任一深度的 `.log` 檔案

這些是日常排除規則的主流語法，stdlib 給不出來。採用外部函式庫補齊。

## Scope 決策（已與使用者確認）

- **Matcher**：引入 `github.com/baldinof/glob`（CLI 乾淨的 `Compile(pattern, separator) → Match(path)` 介面，支援 `**` `*` `?` `[...]` `{a,b}`）
- **整合方式**：全面換成新 matcher — 不再走「path-glob → 試 basename fallback」的雙軌邏輯；單一路徑，語意乾淨
- **CLAUDE.md 影響**：`du/du.go` 開頭寫「Zero external dependencies beyond stdlib + golang.org/x/sys」需要更新，把 `baldinof/glob` 列為明確例外

## 設計

### 新 matcher 介面

```go
import globpkg "github.com/baldinof/glob"

type excludeSet struct {
    patterns []string
    matchers []globpkg.Pattern  // precompiled; nil if compile failed
}

func newExcludeSet(patterns []string) *excludeSet {
    // trim/filter → 對每個 pattern 嘗試 Compile
    // 編譯失敗的 pattern 跳過並 log warning（避免一個錯 pattern 把整個 exclude 弄壞）
}
```

### `match()` 簡化為單一路徑

```go
func (e *excludeSet) match(fullPath, basename string) bool {
    for i, m := range e.matchers {
        if m == nil {
            continue
        }
        if m.Match(fullPath) {
            return true
        }
    }
    return false
}
```

注意：basename 參數保留（不改 `walk.go` 呼叫端簽名），但新 matcher 只用 fullPath 比對 — 對真實路徑 glob 來說 basename 比對本來就是 edge case，`baldinof/glob` 對 `*`/`**` 在 segment 模式下都會給出正確答案。

### 範例行為對照

| Pattern | Path | 舊 (filepath.Match) | 新 (baldinof) |
|---|---|---|---|
| `*.tmp` | `/foo/bar.tmp` | ✓ basename match | ✓ |
| `*.tmp` | `/foo/sub/bar.tmp` | ✓ basename match | ✓ |
| `**/node_modules` | `/foo/node_modules/x` | ✗ 字面失敗 | ✓ |
| `**/node_modules` | `/foo/bar/node_modules/x` | ✗ 字面失敗 | ✓ |
| `foo/**/bar` | `/foo/x/y/bar` | ✗ | ✓ |
| `**/*.log` | `/foo/bar.log` | ✗ | ✓ |
| `**` | 任意 | ✗ | ✓ |

### 檔案異動清單

| 檔案 | 異動 |
|---|---|
| `go.mod` / `go.sum` | `require github.com/baldinof/glob vX.Y.Z`（最新穩定，撰寫時 `v1.1.0`） |
| `du/exclude.go` | 重寫為使用 `baldinof/glob`：`newExcludeSet` 內 precompile；`match()` 簡化為 `m.Match(fullPath)`；`loadExcludeFile()` 介面不變（內部仍走 `newExcludeSet`） |
| `du/du.go` | 開頭註解更新：「Zero external dependencies beyond stdlib + golang.org/x/sys」→ 加上「plus github.com/baldinof/glob」 |
| `du/du_test.go` | 新增 `TestExcludeSet_Doublestar`、`TestUsage_ExcludeDoublestar`；既有的 `TestUsage_Exclude` / `TestUsage_ExcludeSubdir` / `TestUsage_ExcludeFromFile` 應繼續綠（他們用的 `*.txt` / `sub` pattern 在新 matcher 仍 work） |
| `CLAUDE.md` | 「模組與分層」段落 `du/du.go` 描述可順帶更新；若想保持簡潔就只動 `du/du.go` 內檔頭 |

### 關鍵 code 片段

**`du/exclude.go` — 重寫核心**

```go
package du

import (
    "bufio"
    "log/slog"
    "os"
    "strings"

    globpkg "github.com/baldinof/glob"
)

// excludeSet holds precompiled glob patterns used to skip files/directories.
// Patterns follow shell-glob syntax with ** support (any number of segments
// including zero). Mirrors coreutils du's --exclude / --exclude-from semantics.
type excludeSet struct {
    patterns []string         // raw pattern strings (for diagnostics)
    matchers []globpkg.Pattern // precompiled matchers; nil entry = compile failure
}

// newExcludeSet compiles each pattern. Compile failures are logged and the
// pattern is skipped — one bad pattern should not disable the entire exclude
// set.
func newExcludeSet(patterns []string) *excludeSet {
    filtered := make([]string, 0, len(patterns))
    for _, p := range patterns {
        p = strings.TrimSpace(p)
        if p != "" {
            filtered = append(filtered, p)
        }
    }
    e := &excludeSet{
        patterns: make([]string, len(filtered)),
        matchers: make([]globpkg.Pattern, len(filtered)),
    }
    for i, p := range filtered {
        m, err := globpkg.Compile(p, '/')
        if err != nil {
            slog.Warn("exclude: pattern failed to compile, skipping",
                "pattern", p, "err", err)
            continue
        }
        e.patterns[i] = p
        e.matchers[i] = m
    }
    return e
}

func (e *excludeSet) merge(other *excludeSet) {
    if other == nil { return }
    e.patterns = append(e.patterns, other.patterns...)
    e.matchers = append(e.matchers, other.matchers...)
}

// match reports whether fullPath matches any pattern.
func (e *excludeSet) match(fullPath, _ string) bool {
    for _, m := range e.matchers {
        if m != nil && m.Match(fullPath) {
            return true
        }
    }
    return false
}

func (e *excludeSet) empty() bool {
    for _, m := range e.matchers {
        if m != nil { return false }
    }
    return true
}

func loadExcludeFile(path string) (*excludeSet, error) {
    f, err := os.Open(path)
    if err != nil { return nil, err }
    defer f.Close()

    var patterns []string
    scanner := bufio.NewScanner(f)
    for scanner.Scan() {
        line := strings.TrimSpace(scanner.Text())
        if line == "" || strings.HasPrefix(line, "#") { continue }
        patterns = append(patterns, line)
    }
    if err := scanner.Err(); err != nil { return nil, err }
    return newExcludeSet(patterns), nil
}
```

**`du/du_test.go` — 新增測試**

```go
func TestExcludeSet_Doublestar(t *testing.T) {
    excl := newExcludeSet([]string{"**/node_modules", "foo/**/bar", "**/*.log"})
    require.NotNil(t, excl)

    cases := []struct {
        path string
        want bool
    }{
        // **/node_modules
        {"/u/node_modules", true},
        {"/u/p/node_modules", true},
        {"/u/p/q/node_modules", true},
        {"/u/other", false},
        // foo/**/bar
        {"/x/foo/bar", true},
        {"/x/foo/v/bar", true},
        {"/x/foo/v/w/bar", true},
        {"/x/foo/baz", false},
        {"/x/other/bar", false},
        // **/*.log
        {"/u/app.log", true},
        {"/u/p/app.log", true},
        {"/u/p/q/app.log", true},
        {"/u/app.txt", false},
    }
    for _, c := range cases {
        assert.Equal(t, c.want, excl.match(c.path, filepath.Base(c.path)),
            "match(%q)", c.path)
    }
}

// TestUsage_ExcludeDoublestar is end-to-end via Usage() — proves the wiring
// (du.Options.Excludes → excludeSet → walker.skip) all carry doublestars.
func TestUsage_ExcludeDoublestar(t *testing.T) {
    root := t.TempDir()
    require.NoError(t, os.MkdirAll(filepath.Join(root, "a", "node_modules", "x"), 0o755))
    require.NoError(t, os.MkdirAll(filepath.Join(root, "a", "b", "node_modules", "y"), 0o755))
    require.NoError(t, os.MkdirAll(filepath.Join(root, "keep"), 0o755))
    writeFile(t, filepath.Join(root, "a", "node_modules", "x", "f"), 4096)
    writeFile(t, filepath.Join(root, "a", "b", "node_modules", "y", "f"), 4096)
    writeFile(t, filepath.Join(root, "keep", "f"), 4096)

    result, err := Usage(context.Background(), Options{
        Paths:        []string{root},
        ApparentSize: true,
        AllFiles:     true,
        MaxDepth:     -1,
        Excludes:     []string{"**/node_modules"},
    })
    require.NoError(t, err)

    for _, e := range result.Entries {
        assert.NotContains(t, e.Path, "node_modules",
            "**/node_modules should drop all node_modules entries: %s", e.Path)
    }
}
```

## Verification

1. **依賴**：`go mod tidy` 拉 `baldinof/glob`，確認 `go.sum` 含該 module
2. **既有測試不退步**：`go test -race ./...` 全綠
   - `TestUsage_Exclude`（`*.txt`）— 仍綠
   - `TestUsage_ExcludeSubdir`（`sub`）— 仍綠
   - `TestUsage_ExcludeFromFile`（`*.txt` + `deep`）— 仍綠
3. **新測試**：`TestExcludeSet_Doublestar` + `TestUsage_ExcludeDoublestar` 綠
4. **編譯**：`go build ./...` 乾淨
5. **煙霧測試**：在 /tmp 建一棵 `a/node_modules/f` + `a/b/node_modules/g` + `keep/f`，跑 `da scan --exclude '**/node_modules'` 確認 CSV 不含任何 `node_modules` 路徑
6. **pattern 編譯失敗**：在 config 故意寫錯（例：`[unclosed`），scan 應 log warning 但不中斷；其他有效 pattern 仍生效

## Out of scope

- 替換既有 exclude 測試 — 全部保留，只新增
- 重寫 TUI / show / diff 任何東西
- `loadExcludeFile` 的檔案格式（已支援 `#` 註解與空行）保持不變
- 自寫 matcher — 已確認交給函式庫

## Critical files

- `go.mod` / `go.sum`（**改**：加 dep）
- `du/exclude.go`（**改**：重寫為 baldinof/glob）
- `du/du.go`（**改**：檔頭註解更新）
- `du/du_test.go`（**改**：加 2 個測試）
- `CLAUDE.md`（**選改**：du 套件描述；非必要）

## Notes for CLAUDE.md

若要更新 `~/projects/dux/CLAUDE.md` 的「模組與分層」段落，把：

```
│   ├── du.go                # public API: Usage() + Options
```

加註一句「(exclude 用 baldinof/glob 提供 ** 支援)」即可。其餘段落不必動。