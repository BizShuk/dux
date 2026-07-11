# Privacy Policy — disk-analyzer

disk-analyzer 會掃描整個磁碟，包含使用者家目錄。本文件說明如何降低隱私風險。

## 預設行為

1. **敏感路徑 redact**：`privacy_redact_paths` 黑名單中的路徑會被取代為 `<redacted:contains-private>`
2. **預設排除清單**：跳過 `Library/Caches`, `Library/Developer`, `.git` 等
3. **symlink 安全**：預設 `follow_symlinks: false`，避免誤追蹤至系統目錄
4. **TUI 不刪除**：mark-only 設計避免誤刪

## 預設敏感路徑黑名單

```json
[".ssh", ".gnupg", ".aws", ".kube", ".docker",
 "Library/Keychains", "Library/Application Support/Keybase",
 ".config/gh/hosts.yml"]
```

## 客製化

可在 `~/.config/disk-analyzer/settings.json` 修改：

```json
{
    "privacy_redact_paths": [
        ".ssh",
        ".aws",
        "MyCompany/secrets",
        "Work/Credentials"
    ],
    "privacy_redact": true
}
```

`--privacy-redact=false` 可關閉 redact（**不建議**，會把路徑寫進 log/CSV）。

## Log 過濾建議

即使路徑被 redact，仍可能在 log 出現其他 metadata。建議在 inf 的 promtail 設定中對 disk-analyzer 的 log 加 retention 過濾：

```yaml
# inf/loki/promtail-config.yaml snippet
- job_name: disk-analyzer
  static_configs:
    - targets: [localhost]
      labels:
        job: disk-analyzer
        __path__: /Users/shuk/.config/disk-analyzer/logs/*.out.log
  pipeline_stages:
    - match:
        selector: '{job="disk-analyzer"}'
        stages:
          - regex:
              expression: '.*path=(?P<path>[^ ]+).*'
          - labeldrop:
              - path
```

## CSV 處理建議

CSV 包含 path 與 size，但不包含檔案內容。可考慮：

- 把 `~/.config/disk-analyzer/data/*.csv` 加入 gitignore / 加密備份
- 不要把 CSV 上傳到公開位置
- 若需要外部分享，先用 `awk` 過濾敏感 path：

```bash
awk -F, 'NR==1 || !/(\.ssh|\.aws|\.kube)/' report.csv > redacted.csv
```

## 已知限制

- macOS Spotlight metadata（`/.Spotlight-V100`）預設已排除
- Windows：不支援（未來工作）
- Linux noatime 檔案系統：`atime` 為零，會被標記為 cold（可能是誤判）