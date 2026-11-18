// ecosystem.config.js — pm2 configuration for dux.
//
// Schedule: daily 06:00 (cron minute=0 because the user pinned 6am exactly —
// do not offset to avoid the :00/:30 herd without an explicit ask).

module.exports = {
    apps: [
        // Daily 06:00 disk usage scan.
        {
            name: "Disk Analysis - Daily",
            namespace: "Local",
            script: "dux",
            args: ["scan"],
            cron: "0 6 * * *"
        }
    ]
};
