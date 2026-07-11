package svc

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
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
}

// ScanResult is the aggregated output of a Scan invocation.
type ScanResult struct {
	Entries      []Entry
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
	sem          *semaphore.Weighted
	excludeLower []string // pre-lowercased exclude patterns
	privacyLower []string // pre-lowercased privacy patterns
	startedAt    time.Time
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
	s := &Scanner{
		opts:         opts,
		excludeLower: toLowerAll(opts.ExcludeList),
		privacyLower: toLowerAll(opts.PrivacyRedactPaths),
		sem:          semaphore.NewWeighted(int64(opts.Workers)),
		startedAt:    time.Now(),
	}
	return s, nil
}

// Scan walks every configured root concurrently and returns the aggregated
// result. Cancellation is honored via ctx; in-flight workers will wrap up
// at their next walkFn call.
func (s *Scanner) Scan(ctx context.Context) (*ScanResult, error) {
	g, gctx := errgroup.WithContext(ctx)
	for _, root := range s.opts.Roots {
		root := root
		g.Go(func() error {
			return s.walk(gctx, root, 0)
		})
	}
	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return nil, err
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

// walk is the recursive walker for a single root. Uses filepath.WalkDir
// which calls Lstat (so symlinks are NOT followed by default).
func (s *Scanner) walk(ctx context.Context, root string, depth int) error {
	// Acquire a semaphore slot for parallelism; release after walk completes.
	if err := s.sem.Acquire(ctx, 1); err != nil {
		return err
	}
	defer s.sem.Release(1)

	// Verify root exists and is accessible.
	rootInfo, err := os.Lstat(root)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			s.addWarning(fmt.Sprintf("permission denied: %s", root))
			return nil // skip silently, do not abort
		}
		if errors.Is(err, fs.ErrNotExist) {
			s.addWarning(fmt.Sprintf("not found: %s", root))
			return nil
		}
		return err
	}

	// Emit the root itself as an entry.
	s.emit(ctx, root, rootInfo, depth)

	// If root is not a directory, we are done.
	if !rootInfo.IsDir() {
		return nil
	}

	// Skip the rest if max depth reached.
	if s.opts.MaxDepth > 0 && depth >= s.opts.MaxDepth {
		return nil
	}

	// WalkDir uses Lstat semantics — symlinks are reported but not recursed.
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		// Skip the root itself (already emitted above).
		if p == root {
			return nil
		}
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				s.addWarning(fmt.Sprintf("permission denied: %s", p))
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if errors.Is(err, fs.ErrNotExist) {
				return nil // file disappeared mid-walk; not fatal
			}
			return err
		}
		// Exclude check (by name + substring).
		if s.shouldExclude(d.Name()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		// Honor context cancellation cooperatively.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		info, err := d.Info()
		if err != nil {
			return nil // skip unreadable entry
		}
		s.emit(ctx, p, info, depth+1)
		return nil
	})
}

// emit records a single entry's facts into the scanner's shared counters.
// Does NOT push to entries slice — that is the collector's job. This keeps
// the scanner stateless about output and lets the caller decide what to persist.
func (s *Scanner) emit(ctx context.Context, path string, info os.FileInfo, depth int) {
	if info.IsDir() {
		s.dirCount.Add(1)
	} else {
		s.fileCount.Add(1)
		s.bytes.Add(info.Size())
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
	slog.DebugContext(ctx, "scan emit",
		"path", finalPath,
		"size", info.Size(),
		"is_dir", info.IsDir(),
		"depth", depth,
	)
}

// shouldExclude returns true if name matches any pattern in the exclude list.
// Patterns are matched as:
//   - exact basename match (e.g. "node_modules" matches a directory literally named so)
//   - substring match (e.g. ".cache" matches "Library/Caches/..cache-version")
func (s *Scanner) shouldExclude(name string) bool {
	if name == "" {
		return false
	}
	lower := strings.ToLower(name)
	for _, pattern := range s.excludeLower {
		if pattern == "" {
			continue
		}
		// Exact match or substring match.
		if lower == pattern || strings.Contains(lower, pattern) {
			return true
		}
		// Also try filepath.Match for glob patterns.
		if ok, _ := filepath.Match(pattern, name); ok {
			return true
		}
	}
	return false
}

// ShouldExclude is exposed so the collector (which decides whether to walk
// a directory by checking cache) can reuse the same exclusion logic.
func (s *Scanner) ShouldExclude(name string) bool {
	return s.shouldExclude(name)
}

func toLowerAll(in []string) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = strings.ToLower(v)
	}
	return out
}