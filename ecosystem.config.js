// ecosystem.config.js — pm2 configuration for dux.
//
// Mirror stock/ecosystem.config.js conventions:
//   - cron_restart + autorestart:false + max_retries:0 = one-shot scheduled task
//   - independent out_file / error_file under ~/.config/<app>/logs/
//   - merge_logs: true so stderr wraps stdout
//
// Schedule: daily 06:00 (cron minute=0 because the user pinned 6am exactly —
// do not offset to avoid the :00/:30 herd without an explicit ask).

module.exports = {
    apps: [
        {
            name: "dux-daily",
            namespace: "Local",
            script: "dux",
            args: ["scan"],
            cron: "0 6 * * *"
        }
    ]
};
