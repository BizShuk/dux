// ecosystem.config.js — pm2 configuration for disk-analyzer.
//
// Mirror stock/ecosystem.config.js conventions:
//   - cron_restart + autorestart:false + max_retries:0 = one-shot scheduled task
//   - independent out_file / error_file under ~/.config/<app>/logs/
//   - merge_logs: true so stderr wraps stdout
//
// Schedule: every Friday 05:47 AM (cron expression minute is offset to avoid
// the :00/:30 thundering herd — see CLAUDE.md global convention).

module.exports = {
  apps: [
    {
      name: 'disk-analyzer-weekly',
      namespace: 'Local',
      script: 'bash',
      args: '-lc "cd /Users/shuk/projects/disk-analyzer && ./bin/disk-analyzer scan --interactive=false --workers=8 --top=10"',
      cron_restart: '47 5 * * 5',
      autorestart: false,
      max_retries: 0,
      out_file: '/Users/shuk/.config/disk-analyzer/logs/disk-analyzer.out.log',
      error_file: '/Users/shuk/.config/disk-analyzer/logs/disk-analyzer.err.log',
      merge_logs: true,
      time: true,
    },
  ],
};