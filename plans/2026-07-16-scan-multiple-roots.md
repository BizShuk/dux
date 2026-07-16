# Scan 多根目錄並行 Implementation Plan

> `For agentic workers:` REQUIRED SUB-SKILL: Use `executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

`Goal:` 讓 `dux scan` 以五個 goroutine 並行掃描 `$HOME`、`/tmp`、`/var`、`/Applications`、`/usr`，並在部分 root 失敗時輸出其餘成功資料。

`Architecture:` 在 `cmd` package 新增 package-private 並行協調 helper。每個 goroutine 只呼叫一次單 root `du.Usage`，透過 buffered channel 回傳 immutable result；主 goroutine 負責 warning、合併與錯誤判定，既有 CSV mapping 與排序流程維持單執行緒。

`Tech Stack:` Go 1.26、Cobra、`du.Usage`、standard library goroutine/channel、Testify。

## Global Constraints

- 預設 roots 固定為 `$HOME`、`/tmp`、`/var`、`/Applications`、`/usr`。
- 每個 root 恰好使用一個 goroutine。
- 單一 root 失敗只記 warning；至少一個 root 成功時仍輸出 CSV。
- 所有 roots 失敗時回傳 error 且不寫空白 CSV。
- 不改變 `du.Usage` API、`dux du` command、CSV schema 或既有 excludes。
- Symbol 使用 MixedCaps；const 使用 SCREAMING_SNAKE_CASE。

## File Structure

- Create: `cmd/scan_test.go` — 驗證預設 roots、並行啟動、partial success 與 total failure。
- Modify: `cmd/scan.go` — 提供預設 roots 與並行協調 helper，將 `RunE` 接到合併結果。
- Modify: `README.md` — 更新業務定義、domain flow 與快速開始。
- Modify: `CLAUDE.md` — 記錄 scan roots、goroutine 與 partial-success 技術決策。

---

### Task 1: 並行 root 掃描協調

`Files:`

- Create: `cmd/scan_test.go`
- Modify: `cmd/scan.go`

`Interfaces:`

- Consumes: `du.Usage(ctx context.Context, opts du.Options) (*du.Result, error)`
- Produces: `defaultScanRoots(home string) []string`
- Produces: `scanAllRoots(ctx context.Context, roots []string, opts du.Options, usage scanUsageFunc) ([]du.EntryInfo, int, error)`

- [ ] `Step 1: Write the failing tests`

```go
package cmd

import (
    "context"
    "errors"
    "testing"
    "time"

    "github.com/bizshuk/dux/du"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestDefaultScanRoots(t *testing.T) {
    assert.Equal(t, []string{"/home/test", "/tmp", "/var", "/Applications", "/usr"},
        defaultScanRoots("/home/test"))
}

func TestScanAllRootsRunsConcurrentlyAndMergesEntries(t *testing.T) {
    roots := []string{"/one", "/two", "/three"}
    started := make(chan string, len(roots))
    release := make(chan struct{})
    t.Cleanup(func() {
        select {
        case <-release:
        default:
            close(release)
        }
    })

    usage := func(_ context.Context, opts du.Options) (*du.Result, error) {
        root := opts.Paths[0]
        started <- root
        <-release
        return &du.Result{Entries: []du.EntryInfo{{Path: root}}}, nil
    }

    type outcome struct {
        entries  []du.EntryInfo
        failures int
        err      error
    }
    done := make(chan outcome, 1)
    go func() {
        entries, failures, err := scanAllRoots(context.Background(), roots, du.Options{}, usage)
        done <- outcome{entries: entries, failures: failures, err: err}
    }()

    seen := make([]string, 0, len(roots))
    for range roots {
        select {
        case root := <-started:
            seen = append(seen, root)
        case <-time.After(time.Second):
            t.Fatal("roots did not start concurrently")
        }
    }
    close(release)

    result := <-done
    require.NoError(t, result.err)
    assert.Zero(t, result.failures)
    assert.ElementsMatch(t, roots, seen)
    assert.ElementsMatch(t, roots, []string{
        result.entries[0].Path,
        result.entries[1].Path,
        result.entries[2].Path,
    })
}

func TestScanAllRootsKeepsSuccessfulEntries(t *testing.T) {
    usage := func(_ context.Context, opts du.Options) (*du.Result, error) {
        if opts.Paths[0] == "/bad" {
            return nil, errors.New("unavailable")
        }
        return &du.Result{Entries: []du.EntryInfo{{Path: opts.Paths[0]}}}, nil
    }

    entries, failures, err := scanAllRoots(
        context.Background(), []string{"/good", "/bad"}, du.Options{}, usage,
    )

    require.NoError(t, err)
    assert.Equal(t, 1, failures)
    require.Len(t, entries, 1)
    assert.Equal(t, "/good", entries[0].Path)
}

func TestScanAllRootsErrorsWhenEveryRootFails(t *testing.T) {
    usage := func(context.Context, du.Options) (*du.Result, error) {
        return nil, errors.New("unavailable")
    }

    entries, failures, err := scanAllRoots(
        context.Background(), []string{"/one", "/two"}, du.Options{}, usage,
    )

    assert.Nil(t, entries)
    assert.Equal(t, 2, failures)
    assert.EqualError(t, err, "scan: all 2 roots failed")
}
```

- [ ] `Step 2: Run tests and verify RED`

Run: `go test ./cmd -run 'Test(DefaultScanRoots|ScanAllRoots)' -count=1`

Expected: build FAIL because `defaultScanRoots`, `scanUsageFunc`, and `scanAllRoots` are undefined.

- [ ] `Step 3: Implement the minimal coordinator`

Add to `cmd/scan.go`:

```go
type scanUsageFunc func(context.Context, du.Options) (*du.Result, error)

type rootScanResult struct {
    root   string
    result *du.Result
    err    error
}

func defaultScanRoots(home string) []string {
    return []string{home, "/tmp", "/var", "/Applications", "/usr"}
}

func scanAllRoots(
    ctx context.Context,
    roots []string,
    opts du.Options,
    usage scanUsageFunc,
) ([]du.EntryInfo, int, error) {
    results := make(chan rootScanResult, len(roots))
    for _, root := range roots {
        go func() {
            rootOpts := opts
            rootOpts.Paths = []string{root}
            result, err := usage(ctx, rootOpts)
            results <- rootScanResult{root: root, result: result, err: err}
        }()
    }

    var entries []du.EntryInfo
    failures := 0
    for range roots {
        scanned := <-results
        if scanned.err != nil {
            failures++
            slog.Warn("scan: root failed", "root", scanned.root, "error", scanned.err)
            continue
        }
        entries = append(entries, scanned.result.Entries...)
    }
    if failures == len(roots) {
        return nil, failures, fmt.Errorf("scan: all %d roots failed", failures)
    }
    return entries, failures, nil
}
```

In `RunE`, replace the single `Paths: []string{home}` call with:

```go
roots := defaultScanRoots(home)
duOpts := du.Options{
    AllFiles:    true,
    Threshold:   threshold,
    MaxDepth:    math.MaxInt,
    Excludes:    mergedExcludes,
    ExcludeFrom: excludeFrom,
}

slog.Info("scan: starting du walks",
    "roots", roots,
    "root_count", len(roots),
    "threshold_bytes", threshold,
    "threshold_human", svc.FormatBytes(threshold),
)

entries, failedRoots, err := scanAllRoots(ctx, roots, duOpts, du.Usage)
if err != nil {
    return err
}
```

Map `entries` instead of `result.Entries`, and add completion fields:

```go
"successful_roots", len(roots)-failedRoots,
"failed_roots", failedRoots,
```

- [ ] `Step 4: Format and verify GREEN`

Run: `gofmt -w cmd/scan.go cmd/scan_test.go`

Run: `go test ./cmd -run 'Test(DefaultScanRoots|ScanAllRoots)' -count=1`

Expected: PASS with package output `ok github.com/bizshuk/dux/cmd`.

- [ ] `Step 5: Run the focused race test`

Run: `go test -race ./cmd -run 'Test(DefaultScanRoots|ScanAllRoots)' -count=1`

Expected: PASS without `DATA RACE`.

- [ ] `Step 6: Commit the coordinator`

```bash
git add cmd/scan.go cmd/scan_test.go
git commit -m "feat: scan default roots concurrently"
```

### Task 2: 同步專案文件

`Files:`

- Modify: `README.md`
- Modify: `CLAUDE.md`

`Interfaces:`

- Consumes: Task 1 的固定 root 集合與 partial-success 語意。
- Produces: 與程式一致的業務概覽、domain flow 與技術脈絡。

- [ ] `Step 1: Update README.md`

將「列出 `$HOME` 下」改成「列出 `$HOME`、`/tmp`、`/var`、`/Applications`、`/usr` 下」，並將 domain flow 的 scan 節點更新為：

```text
├─► 5 goroutines × du.Usage
│      ├─ $HOME
│      ├─ /tmp
│      ├─ /var
│      ├─ /Applications
│      └─ /usr
│         └─ root 失敗只記 warning；保留其他成功結果
```

在快速開始說明無參數 `scan` 會並行掃描五個預設 roots。

- [ ] `Step 2: Update CLAUDE.md`

在 `設計原則` 新增：

```markdown
7. `多 root 並行`：`scan` 固定掃描 `$HOME`、`/tmp`、`/var`、`/Applications`、`/usr`；每個 root 一個 goroutine，單一 root 失敗不阻斷其他結果
```

將模組說明中的 scan pipeline 更新為 `scanAllRoots()` → `du.Usage()` → merge → sort → CSV。

- [ ] `Step 3: Check documentation consistency`

Run: `rg -n '\$HOME|breakdown-home|du\.Usage|scan pipeline' README.md CLAUDE.md`

Expected: 不再把 `scan` 描述為只掃描 `$HOME`；保留 `breakdown-home` 僅作穩定 CSV section 名稱時須明確註明相容性。

- [ ] `Step 4: Commit documentation`

```bash
git add README.md CLAUDE.md
git commit -m "docs: describe concurrent default scan roots"
```

### Task 3: 完整驗證

`Files:`

- Verify only: all Go packages and current git diff.

`Interfaces:`

- Consumes: Task 1 implementation and Task 2 documentation.
- Produces: build、unit tests、race tests 與 formatting evidence。

- [ ] `Step 1: Run full tests`

Run: `go test ./... -count=1`

Expected: all packages PASS.

- [ ] `Step 2: Run full race tests`

Run: `go test -race ./... -count=1`

Expected: all packages PASS without `DATA RACE`.

- [ ] `Step 3: Build the CLI`

Run: `go build -o bin/dux .`

Expected: exit code 0 and `bin/dux` exists.

- [ ] `Step 4: Check formatting and diff hygiene`

Run: `test -z "$(gofmt -l cmd/scan.go cmd/scan_test.go)" && git diff --check && git status --short`

Expected: no formatting or whitespace errors; status only contains intentional plan or implementation changes.
