# Deployment — disk-analyzer

disk-analyzer 是單次觸發的 CLI 工具，主要由 pm2 排程執行；亦可由 launchd / systemd / cron 觸發。

## pm2（推薦）

`ecosystem.config.js` 在 repo 根目錄，鏡像 `stock/ecosystem.config.js` 的單次任務慣例：

```js
cron_restart: '0 6 * * *'  // 每日 06:00
autorestart: false         // 單次觸發，失敗不重啟
max_retries: 0
```

> **注意**：本機的 `pm2` 是 1.0.0（Go fork）；`apps[].args` 必須是
> `args: ['-lc', '<cmd>']` 的 list 形式，**不是** upstream Node pm2 那種
> `args: '-lc "..."'` 單一字串。

啟動：

```bash
cd ~/projects/dux
pm2 start ecosystem.config.js
pm2 save                  # 重要：persist cron 到 ~/.pm2/dump.json
pm2 list | grep disk-analyzer
```

手動觸發跑一次（在排程到點前測試用）：

```bash
pm2 restart disk-analyzer-daily     # 馬上跑一次，cron 不變
pm2 trigger disk-analyzer-daily     # pm2 1.0.0 不支援，請用 restart 代替
```

查看日誌：

```bash
tail -f ~/.config/disk-analyzer/logs/disk-analyzer.out.log
tail -f ~/.config/disk-analyzer/logs/disk-analyzer.err.log
```

## macOS launchd（替代方案）

如不使用 pm2，可改用 launchd。建立 `~/Library/LaunchAgents/com.user.disk-analyzer.plist`：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.user.disk-analyzer</string>

    <key>ProgramArguments</key>
    <array>
        <string>/Users/shuk/projects/dux/bin/disk-analyzer</string>
        <string>scan</string>
    </array>

    <key>StartCalendarInterval</key>
    <dict>
        <key>Hour</key>
        <integer>6</integer>
        <key>Minute</key>
        <integer>0</integer>
    </dict>

    <key>StandardOutPath</key>
    <string>/Users/shuk/.config/disk-analyzer/logs/launchd.out.log</string>
    <key>StandardErrorPath</key>
    <string>/Users/shuk/.config/disk-analyzer/logs/launchd.err.log</string>
</dict>
</plist>
```

載入：

```bash
launchctl load ~/Library/LaunchAgents/com.user.disk-analyzer.plist
launchctl start com.user.disk-analyzer
```

解除綁定：

```bash
launchctl unload ~/Library/LaunchAgents/com.user.disk-analyzer.plist
```

## Linux systemd（替代方案）

`~/.config/systemd/user/disk-analyzer.service`：

```ini
[Unit]
Description=Disk Analyzer daily CSV scan
After=network.target

[Service]
Type=oneshot
ExecStart=/home/yourusername/projects/dux/bin/disk-analyzer scan
WorkingDirectory=/home/yourusername/projects/dux

[Install]
WantedBy=default.target
```

`~/.config/systemd/user/disk-analyzer.timer`：

```ini
[Unit]
Description=Run disk-analyzer daily at 06:00

[Timer]
OnCalendar=*-*-* 06:00:00
Persistent=true

[Install]
WantedBy=timers.target
```

啟用：

```bash
systemctl --user daemon-reload
systemctl --user enable --now disk-analyzer.timer
```

## 健康檢查

當日 CSV 是否更新：

```bash
ls -lat ~/.config/disk-analyzer/data/*.csv | head -3
head -5 ~/.config/disk-analyzer/data/$(date +%Y-%m-%d).csv
```

最近一次掃描的 stderr 摘要（成功會列 entries 數）：

```bash
tail -20 ~/.config/disk-analyzer/logs/disk-analyzer.err.log
```

> v1 簡化版後 `history.db` 不再被 scan 寫入，所以 sqlite 查詢只反映歷史資料。
> 後續若要把 `show` / `diff` / `tui` 改吃 CSV，這段需要再更新。
