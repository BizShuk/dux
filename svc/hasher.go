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
	"golang.org/x/sync/semaphore"
)

// HashOptions configures a DetectDuplicates invocation.
type HashOptions struct {
	Workers     int   // concurrent readers (default 4)
	HeaderBytes int   // bytes read for stage-2 header hash (default 64KB)
	BufferSize  int   // streaming buffer for stage-3 (default 4MB)
}

// Hasher holds config for a single DetectDuplicates call.
type Hasher struct {
	opts HashOptions
	sem  *semaphore.Weighted
}

// NewHasher returns a Hasher with sensible defaults applied.
func NewHasher(opts HashOptions) *Hasher {
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	if opts.HeaderBytes <= 0 {
		opts.HeaderBytes = 64 * 1024
	}
	if opts.BufferSize <= 0 {
		opts.BufferSize = 4 * 1024 * 1024
	}
	return &Hasher{
		opts: opts,
		sem:  semaphore.NewWeighted(int64(opts.Workers)),
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
func (h *Hasher) DetectDuplicates(ctx context.Context, candidates []Entry) ([]DuplicateGroup, error) {
	files := make([]Entry, 0, len(candidates))
	for _, e := range candidates {
		if !e.IsDir && e.Size > 0 {
			files = append(files, e)
		}
	}
	if len(files) == 0 {
		return nil, nil
	}

	// Stage 1 — size grouping.
	bySize := make(map[int64][]string, len(files))
	for _, e := range files {
		bySize[e.Size] = append(bySize[e.Size], e.Path)
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
	type headerResult struct {
		path  string
		size  int64
		hash  uint64
		err   error
	}
	resultsCh := make(chan headerResult, len(sizeCollisionPaths))
	g, gctx := errgroup.WithContext(ctx)
	for _, p := range sizeCollisionPaths {
		p := p
		size := sizeCollisionIndex[p]
		g.Go(func() error {
			if err := h.sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer h.sem.Release(1)
			hdr, err := readHeader(p, h.opts.HeaderBytes)
			if err != nil {
				resultsCh <- headerResult{path: p, size: size, err: err}
				return nil // record error but don't abort the group
			}
			resultsCh <- headerResult{
				path: p, size: size,
				hash: xxhash.Sum64(hdr),
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return nil, fmt.Errorf("hasher stage2: %w", err)
	}
	close(resultsCh)

	// Bucket by header hash.
	byHeader := make(map[uint64][]headerResult)
	for r := range resultsCh {
		if r.err != nil {
			continue
		}
		byHeader[r.hash] = append(byHeader[r.hash], r)
	}

	// Stage 3 — full hash for header-collision groups.
	var finalGroups []DuplicateGroup
	finalGroups = append(finalGroups, stage1Groups...) // size-only groups always reported

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
		fullHashes, err := h.fullHashGroup(gctx, members)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return finalGroups, err
			}
			return nil, err
		}
		byFull := make(map[uint64][]string)
		for fh, path := range fullHashes {
			byFull[fh] = append(byFull[fh], path)
		}
		for fh, paths := range byFull {
			if len(paths) < 2 {
				continue
			}
			sort.Strings(paths)
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
		if len(g.Paths) >= 2 {
			out = append(out, g)
		}
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
// Returns a map from hash → path. Files of differing sizes are filtered out
// upstream (size equality is required to reach stage 3).
func (h *Hasher) fullHashGroup(ctx context.Context, members []headerResult) (map[uint64]string, error) {
	out := make(map[uint64]string, len(members))
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	for _, m := range members {
		m := m
		g.Go(func() error {
			if err := h.sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer h.sem.Release(1)
			f, err := os.Open(m.path)
			if err != nil {
				return nil // skip unreadable file
			}
			defer f.Close()
			hasher := xxhash.New()
			buf := make([]byte, h.opts.BufferSize)
			if _, err := io.CopyBuffer(hasher, f, buf); err != nil {
				return nil // skip partial reads
			}
			fh := hasher.Sum64()
			mu.Lock()
			out[fh] = m.path
			mu.Unlock()
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