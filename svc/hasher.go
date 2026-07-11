package svc

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/cespare/xxhash/v2"
	"golang.org/x/sync/errgroup"
)

// HashOptions configures a DetectDuplicates invocation.
type HashOptions struct {
	Workers     int   // concurrent readers (default 4)
	HeaderBytes int   // bytes read for stage-2 header hash (default 64KB)
	BufferSize  int   // streaming buffer for stage-3 (default 4MB)
}

// headerResult is an intermediate record passed from stage 2 to stage 3.
// Package-level so fullHashGroup (separate method) can accept it.
type headerResult struct {
	path string
	size int64
	hash uint64
	err  error
}

// Hasher holds config for a single DetectDuplicates call.
type Hasher struct {
	opts HashOptions
}

// NewHasher returns a Hasher with sensible defaults applied.
func NewHasher(opts HashOptions) *Hasher {
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	if opts.Workers > 5 {
		opts.Workers = 5
	}
	if opts.HeaderBytes <= 0 {
		opts.HeaderBytes = 64 * 1024
	}
	if opts.BufferSize <= 0 {
		opts.BufferSize = 4 * 1024 * 1024
	}
	return &Hasher{
		opts: opts,
	}
}

// DetectDuplicates runs the three-stage pipeline over candidates and returns
// the duplicate groups discovered. Inputs should be the file entries from a
// scan; directories are ignored.
//
// Pipeline:
//   - Stage 1: group by size (in-memory, zero IO)
//   - Stage 2: hash first HeaderBytes of each file in size-collisions
//   - Stage 3: hash full content of header-collisions (streaming)
//   - Returns only groups with len(paths) >= 2
func (h *Hasher) DetectDuplicates(ctx context.Context, stream func(cb func(EntryRow) error) error) ([]DuplicateGroup, error) {
	bySize := make(map[int64][]string)
	_ = stream(func(e EntryRow) error {
		if !e.IsDir && e.SizeBytes > 0 {
			bySize[e.SizeBytes] = append(bySize[e.SizeBytes], e.Path)
		}
		return nil
	})
	
	if len(bySize) == 0 {
		return nil, nil
	}

	// Emit stage-1 groups immediately for visibility (rare duplicates caught
	// by size alone — e.g. zero-padded images of identical size).
	var stage1Groups []DuplicateGroup
	var sizeCollisionPaths []string
	var sizeCollisionIndex = make(map[string]int64) // path → size
	for size, paths := range bySize {
		if len(paths) >= 2 {
			stage1Groups = append(stage1Groups, DuplicateGroup{
				Hash:  uint64(size), // size as a stable stand-in hash
				Size:  size,
				Paths: append([]string(nil), paths...),
				Stage: 1,
			})
			for _, p := range paths {
				sizeCollisionPaths = append(sizeCollisionPaths, p)
				sizeCollisionIndex[p] = size
			}
		}
	}

	if len(sizeCollisionPaths) == 0 {
		return stage1Groups, nil
	}

	// Stage 2 — header hash (first HeaderBytes bytes).
	pathsCh := make(chan string, 1000)
	resultsCh := make(chan headerResult, 1000)
	g, gctx := errgroup.WithContext(ctx)

	// Feed paths to workers.
	go func() {
		defer close(pathsCh)
		for _, p := range sizeCollisionPaths {
			select {
			case pathsCh <- p:
			case <-gctx.Done():
				return
			}
		}
	}()

	// Spawn workers.
	for i := 0; i < h.opts.Workers; i++ {
		g.Go(func() error {
			for p := range pathsCh {
				if gctx.Err() != nil {
					return gctx.Err()
				}
				size := sizeCollisionIndex[p]
				hdr, err := readHeader(p, h.opts.HeaderBytes)
				if err != nil {
					select {
					case resultsCh <- headerResult{path: p, size: size, err: err}:
					case <-gctx.Done():
						return gctx.Err()
					}
					continue
				}
				select {
				case resultsCh <- headerResult{
					path: p, size: size,
					hash: xxhash.Sum64(hdr),
				}:
				case <-gctx.Done():
					return gctx.Err()
				}
			}
			return nil
		})
	}

	// Drain resultsCh in a separate goroutine.
	byHeader := make(map[uint64][]headerResult)
	var collectDone = make(chan struct{})
	go func() {
		defer close(collectDone)
		for r := range resultsCh {
			if r.err != nil {
				continue
			}
			byHeader[r.hash] = append(byHeader[r.hash], r)
		}
	}()

	// Wait for workers to finish.
	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return nil, fmt.Errorf("hasher stage2: %w", err)
	}
	close(resultsCh)
	<-collectDone

	// Stage 3 — full hash for header-collision groups.
	var finalGroups []DuplicateGroup
	// Track which paths have been promoted to higher stages so we don't emit
	// them again in stage 1.
	promoted := make(map[string]bool)

	// First emit header-only groups that are NOT going to stage 3 (groups of size 2 with distinct headers).
	for hh, members := range byHeader {
		if len(members) == 1 {
			continue // not a duplicate, skip
		}
		// Decide if we need stage 3.
		// Heuristic: if all members are <= HeaderBytes, header hash is enough.
		allSmall := true
		for _, m := range members {
			if m.size > int64(h.opts.HeaderBytes) {
				allSmall = false
				break
			}
		}
		if allSmall {
			paths := make([]string, len(members))
			for i, m := range members {
				paths[i] = m.path
				promoted[m.path] = true
			}
			sort.Strings(paths)
			finalGroups = append(finalGroups, DuplicateGroup{
				Hash:  hh,
				Size:  members[0].size,
				Paths: paths,
				Stage: 2,
			})
			continue
		}

		// Stage 3 — full hash.
		pathHashes, err := h.fullHashGroup(ctx, members)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return finalGroups, err
			}
			return nil, err
		}
		byFull := make(map[uint64][]string)
		for path, fh := range pathHashes {
			byFull[fh] = append(byFull[fh], path)
		}
		for fh, paths := range byFull {
			if len(paths) < 2 {
				continue
			}
			sort.Strings(paths)
			for _, p := range paths {
				promoted[p] = true
			}
			finalGroups = append(finalGroups, DuplicateGroup{
				Hash:  fh,
				Size:  members[0].size,
				Paths: paths,
				Stage: 3,
			})
		}
	}

	// Filter out groups with < 2 paths (defensive — should not happen).
	out := finalGroups[:0]
	for _, g := range finalGroups {
		if len(g.Paths) < 2 {
			continue
		}
		// Suppress stage-1 groups whose paths have all been promoted.
		if g.Stage == 1 {
			allPromoted := true
			for _, p := range g.Paths {
				if !promoted[p] {
					allPromoted = false
					break
				}
			}
			if allPromoted {
				continue
			}
		}
		out = append(out, g)
	}
	return out, nil
}

// readHeader reads up to n bytes from path. Returns shorter slice if file is smaller.
// Does NOT follow symlinks; the caller is expected to filter symlinks upstream.
func readHeader(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	read, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	return buf[:read], nil
}

// fullHashGroup computes xxHash over the full content of each file in members.
// Returns a map from path → hash. Files of differing sizes are filtered out
// upstream (size equality is required to reach stage 3).
func (h *Hasher) fullHashGroup(ctx context.Context, members []headerResult) (map[string]uint64, error) {
	out := make(map[string]uint64, len(members))
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)

	membersCh := make(chan headerResult, 1000)
	// Feed members to workers.
	go func() {
		defer close(membersCh)
		for _, m := range members {
			select {
			case membersCh <- m:
			case <-gctx.Done():
				return
			}
		}
	}()

	// Spawn workers.
	for i := 0; i < h.opts.Workers; i++ {
		g.Go(func() error {
			for m := range membersCh {
				if gctx.Err() != nil {
					return gctx.Err()
				}
				f, err := os.Open(m.path)
				if err != nil {
					continue // skip unreadable file
				}
				hasher := xxhash.New()
				buf := make([]byte, h.opts.BufferSize)
				_, err = io.CopyBuffer(hasher, f, buf)
				f.Close()
				if err != nil {
					continue // skip partial reads
				}
				fh := hasher.Sum64()
				mu.Lock()
				out[m.path] = fh
				mu.Unlock()
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return nil, err
	}
	return out, nil
}

// HashByName returns a stable 64-bit hash for a string (used for size-group
// "hash" stand-ins). Currently uses FNV-1a for non-content groups; not
// relied upon for content equality.
func HashByName(name string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return h.Sum64()
}

// basenameOf returns filepath.Base for cleaner error messages.
func basenameOf(p string) string { return filepath.Base(p) }