# Deployment — disk-analyzer

disk-analyzer 是單次觸發的 CLI 工具，主要由 pm2 排程執行；亦可由 launchd / systemd / cron 觸發。

## pm2（推薦）

`ecosystem.config.js` 在 repo 根目錄，鏡像 `stock/ecosystem.config.js` 的單次任務慣例：

```js
cron_restart: '47 5 * * 5'  // 每週五 05:47
autorestart: false          // 單次觸發，失敗不重啟
max_retries: 0
```

啟動：

```bash
cd ~/projects/disk-analyzer
pm2 start ecosystem.config.js
pm2 list | grep disk-analyzer
```

手動觸發測試：

```bash
pm2 trigger disk-analyzer-weekly
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
        <string>/Users/shuk/projects/disk-analyzer/bin/disk-analyzer</string>
        <string>scan</string>
        <string>--interactive=false</string>
    </array>

    <key>StartCalendarInterval</key>
    <dict>
        <key>Hour</key>
        <integer>5</integer>
        <key>Minute</key>
        <integer>47</integer>
        <key>Weekday</key>
        <integer>5</integer>
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

## Linux systemd（替代方案）

`~/.config/systemd/user/disk-analyzer.service`：

```ini
[Unit]
Description=Disk Analyzer weekly scan
After=network.target

[Service]
Type=oneshot
ExecStart=/home/yourusername/projects/disk-analyzer/bin/disk-analyzer scan --interactive=false
WorkingDirectory=/home/yourusername/projects/disk-analyzer

[Install]
WantedBy=default.target
```

`~/.config/systemd/user/disk-analyzer.timer`：

```ini
[Unit]
Description=Run disk-analyzer weekly

[Timer]
OnCalendar=Fri *-*-* 05:47:00
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

檢查上次掃描是否成功：

```bash
sqlite3 ~/.config/disk-analyzer/data/history.db \
    "SELECT scan_date, COUNT(*) FROM entries GROUP BY scan_date ORDER BY scan_date DESC LIMIT 5;"
```

檢查 CSV 是否更新：

```bash
ls -lat ~/.config/disk-analyzer/data/*.csv | head -3
```

檢查 metric 是否推達 VictoriaMetrics：

```bash
curl -sG http://localhost:8428/api/v1/query \
    --data-urlencode 'query=disk_analyzer_total_bytes' | jq
```