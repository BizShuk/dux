// Package du provides a Go implementation of coreutils du (disk usage)
// core semantics as a reusable library.
//
// The package faithfully ports the depth-first accumulation logic from
// GNU coreutils du.c — including the dulevel stack, hardlink deduplication
// via dev/ino pairs, apparent-size vs. disk-blocks sizing, and all major
// filtering options (max-depth, threshold, exclude, separate-dirs,
// one-file-system).
//
// Zero external dependencies beyond stdlib + golang.org/x/sys, plus
// github.com/bmatcuk/doublestar/v4 for ** glob support in exclude patterns.
package du

import (
	"context"
	"math"
	"time"
)

// SymlinkMode controls how symbolic links are handled during traversal.
type SymlinkMode int

const (
	// SymlinkPhysical does not follow symlinks (default, like du -P).
	SymlinkPhysical SymlinkMode = iota
	// SymlinkLogical follows all symlinks (like du -L).
	SymlinkLogical
	// SymlinkCommandLine follows only symlinks given as arguments (like du -D/-H).
	SymlinkCommandLine
)

// Options configures a Usage invocation. Mirrors the flags of coreutils du.
type Options struct {
	// Paths to scan. Defaults to ["."] if empty.
	Paths []string

	// ApparentSize uses st_size instead of disk blocks (du -A / --apparent-size).
	ApparentSize bool

	// AllFiles reports all files, not just directories (du -a / --all).
	AllFiles bool

	// MaxDepth limits output to entries at most N levels deep.
	// -1 means unlimited (default). 0 is equivalent to --summarize.
	MaxDepth int

	// Threshold filters entries by size.
	// Positive: exclude entries smaller than Threshold.
	// Negative: exclude entries larger than |Threshold|.
	// Zero: no filtering.
	Threshold int64

	// SeparateDirs does not include subdirectory sizes in parent totals (du -S).
	SeparateDirs bool

	// OneFileSystem skips directories on different filesystems (du -x).
	OneFileSystem bool

	// Symlinks controls symlink dereferencing behavior.
	Symlinks SymlinkMode

	// CountLinks counts each hard link separately (du -l / --count-links).
	// When false (default), files with nlink > 1 are counted only once.
	CountLinks bool

	// Excludes is a list of glob patterns to exclude.
	Excludes []string

	// ExcludeFrom is a path to a file containing exclude patterns (one per line).
	ExcludeFrom string

	// OnEntry is an optional streaming callback invoked for each entry that
	// passes the output filters (max-depth, threshold). If nil, entries are
	// collected into Result.Entries instead.
	OnEntry func(EntryInfo)
}

// DirInfo accumulates size, inode count, and the latest timestamp for a
// directory subtree. Mirrors coreutils du's struct duinfo.
type DirInfo struct {
	// Size in bytes (disk blocks or apparent, depending on Options).
	Size uint64

	// Inodes is the count of filesystem objects.
	Inodes uint64

	// MaxTime is the latest mtime found in the subtree.
	MaxTime time.Time
}

// init resets a DirInfo to its zero state.
func (d *DirInfo) init() {
	d.Size = 0
	d.Inodes = 0
	d.MaxTime = time.Time{}
}

// set populates a DirInfo from a single file's stats.
func (d *DirInfo) set(size uint64, mtime time.Time) {
	d.Size = size
	d.Inodes = 1
	d.MaxTime = mtime
}

// add accumulates another DirInfo into this one. Size saturates at MaxUint64.
func (d *DirInfo) add(other *DirInfo) {
	sum := d.Size + other.Size
	if sum < d.Size { // overflow
		sum = math.MaxUint64
	}
	d.Size = sum
	d.Inodes += other.Inodes
	if other.MaxTime.After(d.MaxTime) {
		d.MaxTime = other.MaxTime
	}
}

// EntryInfo represents a single file or directory in the scan output.
type EntryInfo struct {
	// Path is the filesystem path (as traversed).
	Path string

	// Info contains the accumulated size/inodes/time for this entry.
	Info DirInfo

	// Depth is the distance from the scan root (root = 0).
	Depth int

	// IsDir indicates whether this entry is a directory.
	IsDir bool
}

// Result is the aggregated output of a Usage call.
type Result struct {
	// Entries contains all items that passed the output filters.
	// Empty if Options.OnEntry was set (entries are streamed instead).
	Entries []EntryInfo

	// GrandTotal is the accumulated total across all scanned paths.
	GrandTotal DirInfo
}

// Usage scans the given paths and returns disk usage results.
// It faithfully implements the depth-first accumulation semantics of
// coreutils du.
func Usage(ctx context.Context, opts Options) (*Result, error) {
	if len(opts.Paths) == 0 {
		opts.Paths = []string{"."}
	}
	if opts.MaxDepth == 0 {
		// MaxDepth 0 means summarize only (root level).
		// We keep it as 0 — the walker interprets this correctly.
	}
	if opts.MaxDepth < 0 {
		opts.MaxDepth = math.MaxInt
	}

	// Build exclude set.
	excl := newExcludeSet(opts.Excludes)
	if opts.ExcludeFrom != "" {
		fromFile, err := loadExcludeFile(opts.ExcludeFrom)
		if err != nil {
			return nil, err
		}
		excl.merge(fromFile)
	}

	w := &walker{
		opts:      &opts,
		excludes:  excl,
		seenFiles: make(map[devIno]struct{}),
		// hashAll: dedup aggressively when multiple paths or following symlinks.
		hashAll: len(opts.Paths) > 1 || opts.Symlinks == SymlinkLogical,
	}

	for _, p := range opts.Paths {
		if err := w.walk(ctx, p); err != nil {
			return nil, err
		}
	}

	return &Result{
		Entries:    w.entries,
		GrandTotal: w.grandTotal,
	}, nil
}
