package du

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// dulevel mirrors coreutils du's struct dulevel — per-depth accumulator.
//
// For each directory depth level, we track:
//   - ent:    sum of sizes of all entries IN this directory (not recursive)
//   - subdir: sum of sizes of all entries in subdirectories below this level
type dulevel struct {
	ent    DirInfo
	subdir DirInfo
}

// walker holds the state for a depth-first disk usage traversal.
// It faithfully ports coreutils du's process_file state machine.
type walker struct {
	opts       *Options
	excludes   *excludeSet
	seenFiles  map[devIno]struct{} // hardlink dedup (di_files in du.c)
	hashAll    bool                // dedup all files, not just multi-link ones
	grandTotal DirInfo             // accumulated across all paths
	entries    []EntryInfo         // collected output (when OnEntry is nil)

	// Per-walk state, reset for each root path.
	levels    []dulevel // dulvl stack
	prevLevel int       // previous entry's depth
	rootDev   uint64    // device of the root path (for --one-file-system)
}

// walk performs a depth-first traversal of a single root path.
// This mirrors du.c's du_files function for a single argument.
func (w *walker) walk(ctx context.Context, root string) error {
	// Resolve the root path.
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}

	// If following command-line symlinks, resolve the root.
	if w.opts.Symlinks == SymlinkCommandLine || w.opts.Symlinks == SymlinkLogical {
		resolved, err := filepath.EvalSymlinks(absRoot)
		if err == nil {
			absRoot = resolved
		}
	}

	// Stat the root to get its device (for --one-file-system).
	rootInfo, err := os.Lstat(absRoot)
	if err != nil {
		return err
	}
	if dev, ok := getDev(rootInfo); ok {
		w.rootDev = dev
	}

	// Reset per-walk state.
	w.levels = make([]dulevel, 10)
	w.prevLevel = 0

	// Collect all entries depth-first using readdir, sorted for determinism.
	return w.walkDir(ctx, absRoot, 0)
}

// walkDir recursively walks a directory depth-first, processing entries
// in sorted order. This mimics fts's depth-first behavior.
func (w *walker) walkDir(ctx context.Context, dirPath string, depth int) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Stat the directory itself.
	dirInfo, err := w.statEntry(dirPath)
	if err != nil {
		return nil // skip inaccessible directories, like du does
	}

	// Check --one-file-system: skip if different device.
	if w.opts.OneFileSystem && depth > 0 {
		if dev, ok := getDev(dirInfo); ok && dev != w.rootDev {
			return nil
		}
	}

	// Check excludes.
	if !w.excludes.empty() && w.excludes.match(dirPath, filepath.Base(dirPath)) {
		return nil
	}

	// Read directory contents.
	dirEntries, err := os.ReadDir(dirPath)
	if err != nil {
		// Cannot read directory — still count its own size (like du does for FTS_DNR).
		w.processEntry(dirPath, dirInfo, depth, true)
		return nil
	}

	// Sort entries for deterministic output.
	sort.Slice(dirEntries, func(i, j int) bool {
		return dirEntries[i].Name() < dirEntries[j].Name()
	})

	// Process children: files first (emit immediately), then recurse into dirs.
	// We need to process in a way that mimics fts depth-first:
	// 1. On entering a directory (preorder): don't emit yet, just note we descended
	// 2. Process all children (files get emitted, subdirs recurse)
	// 3. On leaving a directory (postorder): emit the directory with accumulated sizes

	// Process files.
	for _, de := range dirEntries {
		childPath := filepath.Join(dirPath, de.Name())

		// Exclude check.
		if !w.excludes.empty() && w.excludes.match(childPath, de.Name()) {
			continue
		}

		if de.IsDir() {
			continue // handle dirs in next pass
		}

		childInfo, err := w.statChild(childPath, de)
		if err != nil {
			continue
		}

		// Symlink handling.
		if childInfo.Mode()&os.ModeSymlink != 0 && w.opts.Symlinks == SymlinkPhysical {
			// In physical mode, count the symlink itself (its size is tiny).
			w.processEntry(childPath, childInfo, depth+1, false)
			continue
		}

		// Hardlink dedup.
		if !w.opts.CountLinks {
			if nlink, ok := getNlink(childInfo); ok && nlink > 1 {
				di, diOK := getDevIno(childInfo)
				if diOK {
					if _, seen := w.seenFiles[di]; seen {
						continue // already counted this hardlink
					}
					w.seenFiles[di] = struct{}{}
				}
			} else if w.hashAll {
				// Hash all files when multiple paths or following symlinks.
				di, diOK := getDevIno(childInfo)
				if diOK {
					if _, seen := w.seenFiles[di]; seen {
						continue
					}
					w.seenFiles[di] = struct{}{}
				}
			}
		}

		w.processEntry(childPath, childInfo, depth+1, false)
	}

	// Recurse into subdirectories.
	for _, de := range dirEntries {
		if !de.IsDir() {
			continue
		}
		childPath := filepath.Join(dirPath, de.Name())

		// Exclude check.
		if !w.excludes.empty() && w.excludes.match(childPath, de.Name()) {
			continue
		}

		// Symlink check for directories.
		childInfo, err := w.statChild(childPath, de)
		if err != nil {
			continue
		}
		if childInfo.Mode()&os.ModeSymlink != 0 && w.opts.Symlinks == SymlinkPhysical {
			continue // don't follow directory symlinks in physical mode
		}

		if err := w.walkDir(ctx, childPath, depth+1); err != nil {
			return err
		}
	}

	// Postorder: process the directory itself (with accumulated children).
	w.processEntry(dirPath, dirInfo, depth, true)

	return nil
}

// processEntry is the core accumulation function, faithfully porting
// coreutils du's process_file logic.
//
// The dulevel stack tracks per-depth accumulators:
//   - levels[depth].ent    = sum of entries AT this depth
//   - levels[depth].subdir = sum of entries BELOW this depth
//
// When ascending (depth < prevLevel), we propagate child sums upward.
func (w *walker) processEntry(path string, info os.FileInfo, depth int, isDir bool) {
	var dui DirInfo
	dui.set(fileSize(info, w.opts.ApparentSize), info.ModTime())

	duiToPrint := dui

	// Ensure levels slice is large enough.
	for len(w.levels) <= depth {
		w.levels = append(w.levels, dulevel{})
	}

	if w.prevLevel == depth {
		// Same level — most common case. Nothing to do.
	} else if depth > w.prevLevel {
		// Descending: clear accumulators for new levels.
		for i := w.prevLevel + 1; i <= depth; i++ {
			for len(w.levels) <= i {
				w.levels = append(w.levels, dulevel{})
			}
			w.levels[i].ent.init()
			w.levels[i].subdir.init()
		}
	} else {
		// Ascending: propagate children's sums to parent.
		// In coreutils du, this asserts level == prevLevel - 1.
		// We handle arbitrary depth changes gracefully.
		for prev := w.prevLevel; prev > depth; prev-- {
			duiToPrint.add(&w.levels[prev].ent)
			if !w.opts.SeparateDirs {
				duiToPrint.add(&w.levels[prev].subdir)
			}
			if prev-1 >= 0 && prev-1 < len(w.levels) {
				w.levels[prev-1].subdir.add(&w.levels[prev].ent)
				w.levels[prev-1].subdir.add(&w.levels[prev].subdir)
			}
		}
	}

	w.prevLevel = depth

	// Accumulate into the current level (unless SeparateDirs + isDir).
	if !(w.opts.SeparateDirs && isDir) {
		w.levels[depth].ent.add(&dui)
	}

	// Always contribute to grand total.
	w.grandTotal.add(&dui)

	// Decide whether to emit this entry.
	if w.shouldEmit(depth, isDir, &duiToPrint) {
		entry := EntryInfo{
			Path:  path,
			Info:  duiToPrint,
			Depth: depth,
			IsDir: isDir,
		}
		if w.opts.OnEntry != nil {
			w.opts.OnEntry(entry)
		} else {
			w.entries = append(w.entries, entry)
		}
	}
}

// shouldEmit determines whether an entry should appear in the output.
// Mirrors coreutils du's output decision logic.
func (w *walker) shouldEmit(depth int, isDir bool, info *DirInfo) bool {
	// Root level (depth 0) is always emitted.
	if depth == 0 {
		return true
	}

	// Check max depth.
	if depth > w.opts.MaxDepth {
		return false
	}

	// Without --all, only directories are emitted.
	if !isDir && !w.opts.AllFiles {
		return false
	}

	// Check threshold.
	if w.opts.Threshold != 0 {
		size := info.Size
		if w.opts.Threshold > 0 {
			// Positive threshold: exclude entries smaller than threshold.
			if size < uint64(w.opts.Threshold) {
				return false
			}
		} else {
			// Negative threshold: exclude entries larger than |threshold|.
			if size > uint64(-w.opts.Threshold) {
				return false
			}
		}
	}

	return true
}

// statEntry stats a path, choosing Lstat or Stat based on symlink mode.
func (w *walker) statEntry(path string) (os.FileInfo, error) {
	if w.opts.Symlinks == SymlinkLogical {
		return os.Stat(path)
	}
	return os.Lstat(path)
}

// statChild stats a directory entry, handling symlink resolution.
func (w *walker) statChild(path string, de fs.DirEntry) (os.FileInfo, error) {
	if w.opts.Symlinks == SymlinkLogical {
		return os.Stat(path)
	}
	// Use Lstat for physical mode.
	return os.Lstat(path)
}
