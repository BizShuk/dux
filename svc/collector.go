package svc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Collector aggregates scanner output into an in-memory map keyed by path,
// persists rows to the DB, and provides the incremental-cache lookup that
// short-circuits walking directories whose (mtime, size) hasn't changed.
//
// Cache hit semantics: a hit means "the DB has a row for this path whose
// MTimeUnix matches the directory's current mtime AND SizeBytes matches its
// current size." This is intentionally conservative — if either value differs
// we re-walk to be safe.
type Collector struct {
	db *DB

	mu         sync.RWMutex
	entries    map[string]*Entry     // path → entry (mutable aggregation)
	dirSize    map[string]int64      // path → accumulated size of children
	date       string                // scan_date (YYYY-MM-DD) for this run
	added      atomic.Int64
	updated    atomic.Int64
	cacheHits  atomic.Int64
	cacheMiss  atomic.Int64
	startTime  time.Time
}

// NewCollector creates a Collector bound to db and the scan_date for this run.
// scan_date is the YYYY-MM-DD value that will be stamped on every persisted row.
func NewCollector(d *DB, scanDate string) (*Collector, error) {
	if d == nil {
		return nil, errors.New("collector: nil DB")
	}
	if scanDate == "" {
		return nil, errors.New("collector: scan_date required")
	}
	return &Collector{
		db:        d,
		entries:   make(map[string]*Entry),
		dirSize:   make(map[string]int64),
		date:      scanDate,
		startTime: time.Now(),
	}, nil
}

// Date returns the scan_date this collector is accumulating for.
func (c *Collector) Date() string {
	return c.date
}

// Stats returns a snapshot of counters for the metric emitter.
type CollectorStats struct {
	Added     int64
	Updated   int64
	CacheHits int64
	CacheMiss int64
	Duration  time.Duration
}

// Stats returns the current counters.
func (c *Collector) Stats() CollectorStats {
	return CollectorStats{
		Added:     c.added.Load(),
		Updated:   c.updated.Load(),
		CacheHits: c.cacheHits.Load(),
		CacheMiss: c.cacheMiss.Load(),
		Duration:  time.Since(c.startTime),
	}
}

// CachedSize returns (size, true) if the DB has a row for path whose
// (mtime, size) matches info. Returns (0, false) on miss or any error.
//
// On a hit, the caller SHOULD skip walking into `path` and trust the
// returned size — this is the entire point of the incremental cache.
func (c *Collector) CachedSize(path string, info os.FileInfo) (int64, bool) {
	if c.db == nil || !info.IsDir() {
		return 0, false
	}
	row, err := c.db.LastEntry(path)
	if err != nil {
		slog.Debug("collector.CachedSize: DB lookup failed", "path", path, "err", err)
		c.cacheMiss.Add(1)
		return 0, false
	}
	if row == nil {
		c.cacheMiss.Add(1)
		return 0, false
	}
	if row.IsDir && row.MTimeUnix == info.ModTime().Unix() && row.SizeBytes == info.Size() {
		c.cacheHits.Add(1)
		return row.SizeBytes, true
	}
	c.cacheMiss.Add(1)
	return 0, false
}

// AddDir records a directory entry and its (possibly cache-derived) size.
// depth is the directory depth from the scan root (0 for roots themselves).
func (c *Collector) AddDir(path string, size int64, mtime, atime time.Time, depth int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[path] = &Entry{
		Path:         path,
		Size:         size,
		LastModified: mtime,
		LastAccessed: atime,
		IsDir:        true,
		Depth:        depth,
	}
	c.dirSize[path] = size
	c.added.Add(1)
}

// AddFile records a single file entry.
func (c *Collector) AddFile(path string, size int64, mtime, atime time.Time, depth int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[path] = &Entry{
		Path:         path,
		Size:         size,
		LastModified: mtime,
		LastAccessed: atime,
		IsDir:        false,
		Depth:        depth,
	}
	c.added.Add(1)
}

// Update sets an existing entry's size (used by the scanner after walking
// a directory's contents to compute its total).
func (c *Collector) Update(path string, size int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[path]; ok {
		e.Size = size
		c.dirSize[path] = size
		c.updated.Add(1)
	}
}

// Entries returns a sorted snapshot of all collected entries.
// Sorting is by path ascending — matches the requirement that every
// section in the report is sorted by path.
func (c *Collector) Entries() []Entry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Entry, 0, len(c.entries))
	for _, e := range c.entries {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Flush persists all currently-collected entries to the DB in a single batch.
// Returns the number of rows written. Safe to call multiple times — successive
// calls write the same data (idempotent on (path, scan_date) primary key).
func (c *Collector) Flush(ctx context.Context) (int, error) {
	entries := c.Entries()
	if len(entries) == 0 {
		return 0, nil
	}
	rows := make([]EntryRow, 0, len(entries))
	for _, e := range entries {
		cat := CategorizeByExt(filepath.Ext(e.Path))
		rows = append(rows, RowFromEntry(e, c.date, cat, e.Depth))
	}
	return c.db.UpsertEntries(rows)
}

// FilterByMinSize returns entries whose size is at least threshold bytes,
// sorted by size descending (caller can re-sort if needed).
func (c *Collector) FilterByMinSize(threshold int64) []Entry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Entry, 0, len(c.entries))
	for _, e := range c.entries {
		if e.Size >= threshold {
			out = append(out, *e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Size > out[j].Size })
	return out
}

// SizeOf returns the accumulated size for a path (0 if absent).
func (c *Collector) SizeOf(path string) int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.dirSize[path]
}

// ErrNoSuchEntry is returned by SizeOf when the path has not been recorded.
var ErrNoSuchEntry = errors.New("collector: no such entry")

// String returns a human-readable summary for logs.
func (c *Collector) String() string {
	st := c.Stats()
	return fmt.Sprintf("collector[date=%s entries=%d added=%d updated=%d cache_hits=%d cache_miss=%d duration=%s]",
		c.date, len(c.Entries()), st.Added, st.Updated, st.CacheHits, st.CacheMiss, st.Duration)
}