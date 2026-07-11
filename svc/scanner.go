package svc

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
)

// ScanOptions configures a single Scan invocation.
type ScanOptions struct {
	Roots          []string // absolute paths to start from
	ExcludeList    []string // names or substring patterns to skip
	Workers        int      // max concurrent workers (default = NumCPU/2)
	MaxDepth       int      // 0 = unlimited
	FollowSymlinks bool     // false = use Lstat semantics (safe)

	// PrivacyRedactPaths: any path containing one of these substrings is
	// recorded as "<redacted:contains-private>" instead of its real value.
	// If PrivacyRedact is true, paths are redacted even when roots enter a
	// sensitive directory.
	PrivacyRedact        bool
	PrivacyRedactPaths   []string
	PrivacyRedactAllPaths bool // if true, paths are not recorded at all (size only)

	// UseApparentSize: if true, report each file's logical size (info.Size())
	// instead of its on-disk allocation. Default (false) matches `du` behavior
	// and prevents sparse files like VM disk images (OrbStack .img.raw,
	// Docker raw, qcow2, vdi, sparsebundle) from inflating parent directory
	// totals by 50x or more.
	UseApparentSize bool

	// Progress, if non-nil, is invoked periodically during the scan with
	// snapshot counters. Caller is responsible for thread-safety of any state
	// inside Progress; it runs in the scanner's internal goroutine.
	//
	// The callback should be cheap (no I/O, no locking of large structures)
	// because it runs every ~250ms. For UI rendering, prefer writing to a
	// channel that a dedicated goroutine drains.
	Progress func(snapshot ProgressSnapshot)

	// OnEntry is called for every file and directory discovered.
	// If it returns an error, the scan is aborted.
	OnEntry func(Entry) error
}

// ProgressSnapshot is the read-only view passed to ScanOptions.Progress.
type ProgressSnapshot struct {
	Files     int64
	Dirs      int64
	Bytes     int64
	Warnings  int64
	Goroutines int    // runtime.NumGoroutine() at the time of snapshot
	PID       int    // os.Getpid() — process identifier
	Elapsed   time.Duration
	// CurrentPath is the directory most recently picked up by a worker.
	// With multiple workers running in parallel, this is a best-effort
	// "where are we now" hint rather than a strict single-thread trace.
	// Empty before the first processDir call.
	CurrentPath string
}

// ScanResult is the aggregated output of a Scan invocation.
type ScanResult struct {
	Warnings     []string
	FileCount    int64
	DirCount     int64
	BytesScanned int64
	Duration     time.Duration
	StartedAt    time.Time
	CacheHits    int64 // directories whose size came from cache without walking
}

// Scanner carries shared state and configuration for a Scan invocation.
// It is not safe for concurrent Scan() calls on the same Scanner; create
// a new Scanner per scan.
type Scanner struct {
	opts         ScanOptions
	warnMu       sync.Mutex
	warnings     []string
	fileCount    atomic.Int64
	dirCount     atomic.Int64
	bytes        atomic.Int64
	cacheHits    atomic.Int64
	excludeLower []string // pre-lowercased exclude patterns
	privacyLower []string // pre-lowercased privacy patterns
	startedAt    time.Time
	dirMap       sync.Map // path -> *Entry
	// currentPath is the directory most recently picked up by a worker.
	// Workers Store() their path before processing; readers (progress
	// ticker) Load() it. atomic.Pointer gives race-free last-writer-wins.
	currentPath atomic.Pointer[string]
}

// bubbleUpSize adds size to all parent directories currently in dirMap.
func (s *Scanner) bubbleUpSize(path string, size int64) {
	if size == 0 {
		return
	}
	dir := filepath.Dir(path)
	for {
		if v, ok := s.dirMap.Load(dir); ok {
			atomic.AddInt64(&v.(*Entry).Size, size)
		}
		if dir == "/" || dir == "." || dir == "" {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
}

// NewScanner constructs a Scanner from opts. It does not start the scan;
// call Scan() to execute.
func NewScanner(opts ScanOptions) (*Scanner, error) {
	if len(opts.Roots) == 0 {
		return nil, errors.New("scanner: no roots configured")
	}
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	if opts.Workers > 5 {
		opts.Workers = 5
	}
	s := &Scanner{
		opts:         opts,
		excludeLower: toLowerAll(opts.ExcludeList),
		privacyLower: toLowerAll(opts.PrivacyRedactPaths),
		startedAt:    time.Now(),
	}
	return s, nil
}

// Scan walks every configured root concurrently using a parallel BFS queue and returns the aggregated
// result. Cancellation is honored via ctx.
func (s *Scanner) Scan(ctx context.Context) (*ScanResult, error) {
	q := newBFSQueue()

	// Feed initial roots.
	for _, root := range s.opts.Roots {
		q.Push(queueItem{
			path:     root,
			depth:    0,
			origRoot: root,
		})
	}

	// Watch for context cancellation to close the queue.
	go func() {
		<-ctx.Done()
		q.Close()
	}()

	// Optional progress ticker — fires every ~250ms while the scan is in
	// progress. We run it OUTSIDE the errgroup and stop it via a local
	// done channel that we close right before returning. This avoids the
	// infinite-loop-keeps-gctx-alive deadlock that occurs if the ticker
	// is inside errgroup (gctx is only canceled when Wait returns).
	progressDone := make(chan struct{})
	if s.opts.Progress != nil {
		go func() {
			t := time.NewTicker(250 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-progressDone:
					return
				case <-t.C:
					s.warnMu.Lock()
					wc := int64(len(s.warnings))
					s.warnMu.Unlock()
					var cp string
					if p := s.currentPath.Load(); p != nil {
						cp = *p
					}
					s.opts.Progress(ProgressSnapshot{
						Files:       s.fileCount.Load(),
						Dirs:        s.dirCount.Load(),
						Bytes:       s.bytes.Load(),
						Warnings:    wc,
						Goroutines:  runtime.NumGoroutine(),
						PID:         os.Getpid(),
						Elapsed:     time.Since(s.startedAt),
						CurrentPath: cp,
					})
				}
			}
		}()
	}

	// Spawn worker goroutines.
	g, gctx := errgroup.WithContext(ctx)
	for i := 0; i < s.opts.Workers; i++ {
		g.Go(func() error {
			for {
				if gctx.Err() != nil {
					return gctx.Err()
				}
				item, ok := q.Pop()
				if !ok {
					return nil
				}
				err := s.processDir(gctx, q, item)
				q.Done()
				if err != nil {
					return err
				}
			}
		})
	}

	scanErr := g.Wait()

	var emitErr error
	if scanErr == nil && s.opts.OnEntry != nil {
		s.dirMap.Range(func(key, value any) bool {
			e := value.(*Entry)
			if err := s.opts.OnEntry(*e); err != nil {
				emitErr = err
				return false
			}
			return true
		})
	}

	// Signal the progress goroutine to exit. Safe even if it was never started.
	close(progressDone)
	if scanErr != nil && !errors.Is(scanErr, context.Canceled) {
		return nil, scanErr
	}
	if emitErr != nil {
		return nil, emitErr
	}
	return s.result(), nil
}

// result snapshots atomic counters into a non-concurrent struct.
func (s *Scanner) result() *ScanResult {
	s.warnMu.Lock()
	warnings := append([]string(nil), s.warnings...)
	s.warnMu.Unlock()
	return &ScanResult{
		Warnings:     warnings,
		FileCount:    s.fileCount.Load(),
		DirCount:     s.dirCount.Load(),
		BytesScanned: s.bytes.Load(),
		Duration:     time.Since(s.startedAt),
		StartedAt:    s.startedAt,
		CacheHits:    s.cacheHits.Load(),
	}
}

func (s *Scanner) addWarning(msg string) {
	s.warnMu.Lock()
	s.warnings = append(s.warnings, msg)
	s.warnMu.Unlock()
}

// processDir reads a directory and emits its entries, pushing subdirectories into the BFS queue.
func (s *Scanner) processDir(ctx context.Context, q *bfsQueue, item queueItem) error {
	// Publish the path being processed so the progress ticker can show
	// "where we are now". Last-writer-wins across workers is fine for a
	// best-effort indicator; the value is just a UI hint.
	if p := item.path; p != "" {
		s.currentPath.Store(&p)
	}

	// Verify directory exists and is accessible.
	info, err := os.Lstat(item.path)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			if slog.Default().Enabled(ctx, slog.LevelDebug) {
				s.addWarning(fmt.Sprintf("permission denied: %s", item.path))
			}
			return nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			if slog.Default().Enabled(ctx, slog.LevelDebug) {
				s.addWarning(fmt.Sprintf("not found: %s", item.path))
			}
			return nil
		}
		return err
	}

	// Emit the directory itself.
	if err := s.emit(ctx, item.path, info, item.depth); err != nil {
		return err
	}

	// If MaxDepth reached, we do not traverse inside.
	if s.opts.MaxDepth > 0 && item.depth >= s.opts.MaxDepth {
		return nil
	}

	// Read directory entries.
	entries, err := os.ReadDir(item.path)
	if err != nil {
		if slog.Default().Enabled(ctx, slog.LevelDebug) {
			s.addWarning(fmt.Sprintf("walk error at %s: %v", item.path, err))
		}
		return nil
	}

	for _, d := range entries {
		p := filepath.Join(item.path, d.Name())

		// Exclude check (by name + full-path).
		if s.shouldExclude(d.Name(), p) {
			continue
		}

		// Competing-root dedup: if this is one of the other roots, skip it.
		if d.IsDir() && s.isOtherRoot(p, item.origRoot) {
			s.addWarning(fmt.Sprintf("skipping competing root: %s (scanned separately)", p))
			continue
		}

		// Read info for the entry.
		dinfo, err := d.Info()
		if err != nil {
			continue // skip unreadable entry
		}

		if d.IsDir() {
			// Push subdirectory to the queue.
			q.Push(queueItem{
				path:     p,
				depth:    item.depth + 1,
				origRoot: item.origRoot,
			})
		} else {
			// Emit file entry immediately.
			if err := s.emit(ctx, p, dinfo, item.depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// emit records a single entry's facts into the scanner's shared counters and
// streams an Entry via OnEntry. Privacy redaction mutates the path
// stored in the Entry.
//
// File size is reported as actual disk allocation (matching `du`) unless
// ScanOptions.UseApparentSize is set. This avoids sparse files (OrbStack VM
// images, swap files, sparsebundles) inflating directory totals by orders
// of magnitude via bubbleUpSize.
func (s *Scanner) emit(ctx context.Context, path string, info os.FileInfo, depth int) error {
	size := reportSize(info, s.opts.UseApparentSize)
	if info.IsDir() {
		s.dirCount.Add(1)
	} else {
		s.fileCount.Add(1)
		s.bytes.Add(size)
	}
	// Privacy: redact path if it matches a sensitive substring.
	finalPath := path
	if s.opts.PrivacyRedact {
		lower := strings.ToLower(path)
		for _, p := range s.privacyLower {
			if strings.Contains(lower, p) {
				finalPath = "<redacted:contains-private>"
				break
			}
		}
	}
	if s.opts.PrivacyRedactAllPaths {
		finalPath = "<redacted>"
	}
	e := &Entry{
		Path:         finalPath,
		Size:         size,
		LastModified: info.ModTime(),
		LastAccessed: atimeOf(info),
		IsDir:        info.IsDir(),
		IsSymlink:    info.Mode()&os.ModeSymlink != 0,
		Depth:        depth,
	}
	if info.IsDir() {
		s.dirMap.Store(path, e)
	}
	s.bubbleUpSize(path, size)

	if !info.IsDir() && s.opts.OnEntry != nil {
		if err := s.opts.OnEntry(*e); err != nil {
			return err
		}
	}
	slog.DebugContext(ctx, "scan emit",
		"path", finalPath,
		"size", size,
		"is_dir", info.IsDir(),
		"depth", depth,
	)
	return nil
}

// reportSize returns the byte count the scanner should attribute to info.
//
// Default: actual disk allocation (st_blocks × 512), matching `du`.
// This prevents sparse files like VM disk images from inflating parent
// directory totals — a 228 GB apparent OrbStack .img.raw may only occupy
// 4 GB on disk.
//
// If useApparent is true, returns info.Size() (logical length) instead —
// useful when the user wants to size a VM's maximum capacity rather than
// current footprint.
func reportSize(info os.FileInfo, useApparent bool) int64 {
	if useApparent {
		return info.Size()
	}
	return onDiskBytes(info)
}

// onDiskBytes returns the actual disk space consumed by info, in bytes,
// matching `du`. Falls back to info.Size() when the syscall doesn't expose
// block counts (e.g., some Windows paths or unusual filesystems).
func onDiskBytes(info os.FileInfo) int64 {
	if info == nil {
		return 0
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Blocks > 0 {
		// syscall.Stat_t.Blocks is in 512-byte sectors regardless of fs block size.
		return st.Blocks * 512
	}
	return info.Size()
}

// shouldExclude returns true if the entry should be skipped during walk.
//
// Patterns are interpreted as either:
//   - Path-prefix match (if the pattern starts with "/", e.g. "/dev" matches
//     "/dev/fd", "/dev/null"): skip if fullPath equals or starts with pattern + "/"
//   - Basename substring match (otherwise, e.g. "node_modules" matches any
//     directory literally named so or any file with "node_modules" in name)
//
// This dual-mode lets users exclude both absolute system paths (with leading "/")
// and common directory basenames (without "/").
func (s *Scanner) shouldExclude(name, fullPath string) bool {
	if name == "" {
		return false
	}
	lowerName := strings.ToLower(name)
	lowerPath := strings.ToLower(fullPath)
	for _, pattern := range s.excludeLower {
		if pattern == "" {
			continue
		}
		// Path-prefix patterns (start with "/").
		if strings.HasPrefix(pattern, "/") {
			if lowerPath == pattern || strings.HasPrefix(lowerPath, pattern+"/") {
				return true
			}
			// Also catch the trailing-slash variant.
			if lowerPath == strings.TrimSuffix(pattern, "/") {
				return true
			}
			continue
		}
		// Relative path patterns (contain "/").
		if strings.Contains(pattern, "/") {
			if lowerPath == pattern || strings.HasPrefix(lowerPath, pattern+"/") ||
				strings.HasSuffix(lowerPath, "/"+pattern) || strings.Contains(lowerPath, "/"+pattern+"/") {
				return true
			}
			continue
		}
		// Basename patterns: exact or substring.
		if lowerName == pattern || strings.Contains(lowerName, pattern) {
			return true
		}
		// Glob fallback.
		if ok, _ := filepath.Match(pattern, name); ok {
			return true
		}
	}
	return false
}

// ShouldExclude is exposed so the collector (which decides whether to walk
// a directory by checking cache) can reuse the same exclusion logic.
func (s *Scanner) ShouldExclude(name string) bool {
	return s.shouldExclude(name, "")
}

// isOtherRoot reports whether path `p` is one of the OTHER scan roots
// (not the one we started walking from). Used to prevent double-counting.
func (s *Scanner) isOtherRoot(p, origRoot string) bool {
	cleanP := filepath.Clean(p)
	for _, r := range s.opts.Roots {
		if filepath.Clean(r) == filepath.Clean(origRoot) {
			continue
		}
		if filepath.Clean(r) == cleanP {
			return true
		}
	}
	return false
}

type queueItem struct {
	path     string
	depth    int
	origRoot string
}

type bfsQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []queueItem
	active int
	closed bool
}

func newBFSQueue() *bfsQueue {
	q := &bfsQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *bfsQueue) Push(item queueItem) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.items = append(q.items, item)
	q.cond.Signal()
}

func (q *bfsQueue) Pop() (queueItem, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		if q.active == 0 {
			q.closed = true
			q.cond.Broadcast()
			return queueItem{}, false
		}
		q.cond.Wait()
	}
	if q.closed {
		return queueItem{}, false
	}
	item := q.items[0]
	q.items = q.items[1:]
	q.active++
	return item, true
}

func (q *bfsQueue) Done() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.active--
	q.cond.Broadcast()
}

func (q *bfsQueue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.cond.Broadcast()
}

func toLowerAll(in []string) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = strings.ToLower(v)
	}
	return out
}

// atimeOf best-effort extracts atime from os.FileInfo. On Linux with noatime
// filesystems, atime is unreliable; we still capture whatever the FS returns.
func atimeOf(info os.FileInfo) time.Time {
	type interfaceWithAtime interface {
		ModTime() time.Time
	}
	// os.FileInfo only exposes ModTime(). For atime we rely on the caller
	// (handler) to enrich with platform-specific stat if needed.
	// Returning ModTime() is incorrect for cold-data detection — but the
	// BuildCold filter treats IsZero as cold, which is the safer default on
	// platforms that don't expose atime.
	_ = interfaceWithAtime(nil)
	return time.Time{}
}