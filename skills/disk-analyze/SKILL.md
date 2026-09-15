---
name: disk-analyze
description: Use when disk space is running low or someone asks what is taking up space, what can be deleted, which folders are largest, or wants to reclaim space on a Mac or Linux box. Triggers on "磁碟快滿了", "空間不夠", "什麼可以刪", "清理磁碟", "disk full", "no space left on device", "largest folders", "top 20 biggest", "what is eating my disk".
---

# disk-analyze

## Overview

把整台機器的磁碟用量變成一張`可以直接照做的刪除清單`. 兩個指令: `dux scan` 產出 CSV snapshot,
`dux analyze` 把 snapshot 排成 top-N 候選. 兩者都在 `dux` binary 裡, `沒有外部 script 依賴`.

核心是 `rollup 語意`: 一個資料夾若`整個`都是可重建的產物 (cache, build artifact, 依賴),
就`只列這個資料夾一行`, 它底下的子檔案與子資料夾`完全不再出現`. 反過來, 不能整個刪的資料夾
`永遠不會被列為候選`, 分析器改成`往下鑽`, 去找它裡面真正能刪的部分. 因此清單上每一個 byte
`只被算一次`, 總計數字可以直接相信.

## When to Use

- 磁碟剩餘空間告急, 需要知道`刪什麼最划算`
- 想看`最大的 20 個`檔案或資料夾, 而且不要被同一棵樹的父子項目洗版
- 準備清理前想先確認`哪些是安全的, 哪些要人工判斷`

`不適用`: 找重複檔案, 追蹤某路徑的成長趨勢 (那是 `file_watcher` 的職責),
或直接執行刪除 (本技能`只產出清單, 不刪檔`).

## Workflow

### 1. 掃描

```bash
dux scan --min-size 50MB
```

預設掃 `$HOME`, `/tmp`, `/var`, `/usr`, 寫入 `~/.config/dux/data/YYYY-MM-DD.csv`.

`/Applications 預設不掃`: 它幾乎全是不可刪的內容, app 要用解除安裝而非 `rm -rf`,
掃它只是讓時間加倍. 真的要看就明確指定 `dux scan --roots /Applications`.

`門檻決定候選數量`. `--min-size` 預設 `200MB`, 一般機器上只會留下約 400 列, top 20 可能`湊不滿`;
要湊滿就降到 `50MB` 或更低. 已經有當天的 CSV 就`跳過這步`, 直接分析既有檔案.

### 2. 分析

```bash
dux analyze --top 20
```

預設讀`最新`的 CSV, 也可以 `--date 2026-09-13` 指定歷史 snapshot 做比對.

輸出兩段:

- `候選清單` — 依大小排序, 每行含理由與 mtime, 結尾給出`不重複計算`的總可回收量
- `manual review` — 很大但`不能自動判定安全`的項目 (個人文件, VM image, `/Applications`),
  只在`它底下找不到任何可刪候選`時才出現, 避免蓋掉已經鑽進去的結果

### 3. 交給使用者決定

把候選清單原樣呈現, `不要代為執行 rm`. 使用者確認後才逐項刪除;
Xcode 相關目錄可改用 `dux delete` 的互動流程.

## Quick Reference

| 需求 | 指令 |
| --- | --- |
| 完整重掃 + 分析 | `dux scan --min-size 50MB && dux analyze` |
| 只看前 10 名 | `dux analyze --top 10` |
| 不看人工判斷段 | `dux analyze --review 0` |
| 分析舊 snapshot | `dux analyze --date 2026-09-13` |
| 只看 1GB 以上 | `dux analyze --min-size 1GB` |
| 看判定規則 | `dux analyze --show-rules` |
| 展開被合併的群組 | `dux analyze --members` |
| 連 /Applications 一起掃 | `dux scan --roots $HOME,/Applications` |

## Deletability Rules

判定表`內嵌在 binary`, 用 `dux analyze --show-rules` 查看, 原始檔在 `svc/rules.json`.
三個等級, `優先序 keep > whole > review`, 同級內`先命中者勝`:

| 等級 | 意義 | 對 rollup 的效果 |
| --- | --- | --- |
| `keep` | 絕不可刪 (系統分割區, iCloud, 照片圖庫, keychain, app bundle 內部) | `整棵子樹剪掉`, 不列候選也不列 review |
| `whole` | 整個資料夾都是可重建產物 | `列為候選`, 並壓掉所有子項目 |
| `review` | 大但需要人工判斷 | `永不列為候選`, 往下鑽找可刪子項目 |

未命中任何規則`預設為 review`, 也就是`偏保守`: 沒被明確認證安全的東西不會進候選清單.

`collapse`: 標了這個旗標的規則, 重複命中時會`合併成一行`, 錨定在共同上層目錄,
顯示成 `<共同目錄>  [N × <名稱>]`. 用在會在同一棵樹裡出現幾十次的 pattern
(每個 Python 版本一份 `__pycache__`, 每個 simulator 一份 cache), 否則它們會洗掉整個 top-20.
`合併列不能直接刪錨點目錄`, 要 `--members` 展開看實際路徑.

新增規則就在 `svc/rules.json` 對應陣列加一筆 `{ "glob", "why" }`, 需要時加 `"collapse": true`.
glob 是 doublestar pattern: `**` 跨目錄, `*` 只在單層內, `{a,b}` 是二選一.
`why` 會直接印在清單上, 寫成使用者看得懂的理由. 改完要`重新編譯`才生效
(規則以 `go:embed` 內嵌), 臨時試驗可用 `dux analyze --rules <path>`.

## Common Mistakes

| 錯誤 | 後果 | 正解 |
| --- | --- | --- |
| 直接把 CSV 依 size 排序取前 20 | 前幾名全是 `$HOME` → `Library` → `Application Support` 這種父子鏈, 同一份資料被列四次 | 用 `dux analyze`, 只列可整個刪的節點 |
| 把大資料夾當成可刪 | 誤刪個人文件 | 只有 `whole` 等級進候選, 其餘一律人工判斷 |
| 對合併列的錨點目錄下 `rm -rf` | 刪掉整個 Python 或 Containers 目錄 | 合併列先 `--members` 展開, 逐個刪 |
| 把 review 段的數字加進可回收總量 | 高估數倍 | 總計`只加候選清單`, review 段不計入 |
| 用舊的 CSV 下結論 | 已經刪過的東西又出現一次 | 先確認輸出第一行的 CSV 日期, 必要時重掃 |
| 掃完發現不到 20 個候選 | 以為分析器壞了 | 是 `--min-size` 太高, 降門檻重掃 |

## Permissions

macOS 上 `~/Library` 部分子目錄需要 `Full Disk Access` 才讀得到; 缺權限的 root 只會記 warning,
其餘 root 照常產出. 若結果明顯偏小, 先確認執行的 terminal 是否已被授權.
