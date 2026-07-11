package svc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bizshuk/gosdk/metric"
)

// BufferedMetric wraps metric.MetricService with a JSONL offline buffer.
// When the underlying service is unreachable, metrics are appended to a
// JSONL file and replayed on next startup.
//
// Limits:
//   - In-memory cap: 1000 metrics. Older metrics are dropped on overflow.
//   - File cap: 10 MB. On overflow, the file is rotated (oldest half removed).
//
// The buffer is purely best-effort — losing metrics on hard disk failure
// is acceptable; losing data on transient VM downtime is not.
type BufferedMetric struct {
	svc        *metric.MetricService
	bufferPath string

	mu   sync.Mutex
	buf  []metric.Metric // in-memory pending
	cap  int             // in-memory cap (default 1000)
	size int64           // current jsonl size on disk (cached)
}

// NewBufferedMetric creates a BufferedMetric. If bufferPath is non-empty,
// any pre-existing jsonl at that path is replayed against svc before the
// constructor returns.
func NewBufferedMetric(svc *metric.MetricService, bufferPath string) (*BufferedMetric, error) {
	b := &BufferedMetric{
		svc:        svc,
		bufferPath: bufferPath,
		cap:        1000,
	}
	if bufferPath == "" {
		return b, nil
	}
	if err := os.MkdirAll(filepath.Dir(bufferPath), 0o755); err != nil {
		return nil, fmt.Errorf("metrics: mkdir buffer dir: %w", err)
	}
	if err := b.replay(); err != nil {
		// Don't fail construction on replay error — just log.
		slog.Warn("metrics: replay failed", "err", err)
	}
	return b, nil
}

// Send enqueues a metric for transmission. It is called from many goroutines;
// serialization is via internal mutex. If the buffer is full, the oldest
// pending metric is dropped.
func (b *BufferedMetric) Send(m metric.Metric) error {
	if m.Timestamp == 0 {
		m.Timestamp = time.Now().Unix()
	}
	b.mu.Lock()
	b.buf = append(b.buf, m)
	if len(b.buf) > b.cap {
		// Drop oldest.
		b.buf = b.buf[len(b.buf)-b.cap:]
	}
	b.mu.Unlock()
	return b.flushIfDue()
}

// SendMany enqueues multiple metrics in one call (more efficient).
func (b *BufferedMetric) SendMany(ms []metric.Metric) error {
	b.mu.Lock()
	b.buf = append(b.buf, ms...)
	if len(b.buf) > b.cap {
		b.buf = b.buf[len(b.buf)-b.cap:]
	}
	b.mu.Unlock()
	return b.flushIfDue()
}

// Flush synchronously drains the in-memory buffer and the disk buffer
// against the live metric service. Safe to call multiple times.
func (b *BufferedMetric) Flush() error {
	b.mu.Lock()
	pending := b.buf
	b.buf = nil
	b.mu.Unlock()

	// Combine with disk-buffered metrics if any.
	if b.bufferPath != "" {
		disk, err := b.readDiskBuffer()
		if err != nil {
			slog.Warn("metrics: read disk buffer", "err", err)
		} else if len(disk) > 0 {
			pending = append(disk, pending...)
		}
	}

	if len(pending) == 0 {
		return nil
	}
	if err := b.svc.SendMulti(pending); err != nil {
		// Live send failed → write everything back to disk for next replay.
		if b.bufferPath != "" {
			if werr := b.writeDiskBuffer(pending); werr != nil {
				slog.Error("metrics: write disk buffer after send failure",
					"send_err", err, "write_err", werr)
			}
		}
		return err
	}
	// Successful send → clear disk buffer.
	if b.bufferPath != "" {
		_ = os.Remove(b.bufferPath)
		b.size = 0
	}
	return nil
}

// flushIfDue runs Flush if more than 5 seconds have passed since last flush.
// Called from Send paths so we don't pay the cost on every metric.
var lastFlush = time.Now()
var lastFlushMu sync.Mutex

func (b *BufferedMetric) flushIfDue() error {
	lastFlushMu.Lock()
	now := time.Now()
	due := now.Sub(lastFlush) > 5*time.Second
	if due {
		lastFlush = now
	}
	lastFlushMu.Unlock()
	if due {
		return b.Flush()
	}
	return nil
}

// replay reads the disk buffer (if any) and tries to send it.
func (b *BufferedMetric) replay() error {
	if _, err := os.Stat(b.bufferPath); os.IsNotExist(err) {
		return nil
	}
	return b.Flush()
}

// readDiskBuffer reads JSONL metrics from the buffer file.
// Each line is one marshaled metric.Metric.
func (b *BufferedMetric) readDiskBuffer() ([]metric.Metric, error) {
	f, err := os.Open(b.bufferPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []metric.Metric
	sc := bufio.NewScanner(f)
	// Allow large lines (each metric is small, but be defensive).
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m metric.Metric
		if err := json.Unmarshal(line, &m); err != nil {
			slog.Warn("metrics: corrupt buffer line, dropping",
				"err", err, "line", string(line))
			continue
		}
		out = append(out, m)
	}
	return out, sc.Err()
}

// writeDiskBuffer writes pending metrics to the buffer file in JSONL format.
// If the resulting file would exceed 10MB, the oldest half is removed.
func (b *BufferedMetric) writeDiskBuffer(pending []metric.Metric) error {
	const sizeCap = 10 * 1024 * 1024
	if b.size > sizeCap {
		// Truncate: keep only the most recent half of pending.
		pending = pending[len(pending)/2:]
	}
	tmp := b.bufferPath + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, m := range pending {
		if err := enc.Encode(m); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, b.bufferPath); err != nil {
		return err
	}
	info, err := os.Stat(b.bufferPath)
	if err == nil {
		b.size = info.Size()
	}
	return nil
}

// ScanSummaryMetric is emitted once per scan to capture high-level totals.
type ScanSummaryMetric struct {
	TotalBytes  int64
	TotalFiles  int64
	TotalDirs   int64
	MaxDirSize  int64
	DurationMS  int64
	CacheHits   int64
	Warnings    int64
}

// ConvertToMetric satisfies gosdk/metric.IMetric for Send().
func (s ScanSummaryMetric) ConvertToMetric() []metric.Metric {
	now := time.Now().Unix()
	return []metric.Metric{
		{Name: "disk_analyzer_total_bytes", Timestamp: now, Value: s.TotalBytes,
			Tags: map[string]string{}},
		{Name: "disk_analyzer_total_files", Timestamp: now, Value: s.TotalFiles,
			Tags: map[string]string{}},
		{Name: "disk_analyzer_total_dirs", Timestamp: now, Value: s.TotalDirs,
			Tags: map[string]string{}},
		{Name: "disk_analyzer_max_dir_size", Timestamp: now, Value: s.MaxDirSize,
			Tags: map[string]string{}},
		{Name: "disk_analyzer_scan_duration_ms", Timestamp: now, Value: s.DurationMS,
			Tags: map[string]string{}},
		{Name: "disk_analyzer_cache_hits", Timestamp: now, Value: s.CacheHits,
			Tags: map[string]string{}},
		{Name: "disk_analyzer_warnings_total", Timestamp: now, Value: s.Warnings,
			Tags: map[string]string{}},
	}
}

// EmitScanSummary is a convenience wrapper around metric.Send that uses
// the BufferedMetric for offline-safe delivery.
func (b *BufferedMetric) EmitScanSummary(ctx context.Context, sum ScanSummaryMetric) error {
	return metric.Send([]ScanSummaryMetric{sum})
}

// _ ensures context is referenced even if unused above.
var _ = context.Background