package svc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHasher_DetectsExactDuplicate(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.bin")
	b := filepath.Join(dir, "b.bin")
	content := []byte("hello, world! this is a duplicate test file.")
	must(t, os.WriteFile(a, content, 0o644))
	must(t, os.WriteFile(b, content, 0o644))

	h := NewHasher(HashOptions{Workers: 2})
	candidates := []Entry{
		{Path: a, Size: int64(len(content)), IsDir: false},
		{Path: b, Size: int64(len(content)), IsDir: false},
	}
	stream := func(cb func(EntryRow) error) error {
		for _, e := range candidates {
			if err := cb(RowFromEntry(e, "2020", "", e.Depth)); err != nil {
				return err
			}
		}
		return nil
	}
	groups, err := h.DetectDuplicates(context.Background(), stream)
	if err != nil {
		t.Fatalf("DetectDuplicates: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d: %+v", len(groups), groups)
	}
	if len(groups[0].Paths) != 2 {
		t.Errorf("expected 2 paths in group, got %d", len(groups[0].Paths))
	}
}

func TestHasher_NoDuplicates(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.bin")
	b := filepath.Join(dir, "b.bin")
	must(t, os.WriteFile(a, []byte("alpha"), 0o644))
	must(t, os.WriteFile(b, []byte("bravo charlie delta"), 0o644))

	h := NewHasher(HashOptions{Workers: 2})
	candidates := []Entry{
		{Path: a, Size: 5, IsDir: false},
		{Path: b, Size: 19, IsDir: false},
	}
	stream := func(cb func(EntryRow) error) error {
		for _, e := range candidates {
			if err := cb(RowFromEntry(e, "2020", "", e.Depth)); err != nil {
				return err
			}
		}
		return nil
	}
	groups, _ := h.DetectDuplicates(context.Background(), stream)
	if len(groups) != 0 {
		t.Errorf("expected no groups, got %d: %+v", len(groups), groups)
	}
}

func TestHasher_SkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	must(t, os.MkdirAll(sub, 0o755))

	h := NewHasher(HashOptions{Workers: 1})
	candidates := []Entry{
		{Path: dir, Size: 0, IsDir: true},
		{Path: sub, Size: 0, IsDir: true},
	}
	stream := func(cb func(EntryRow) error) error {
		for _, e := range candidates {
			if err := cb(RowFromEntry(e, "2020", "", e.Depth)); err != nil {
				return err
			}
		}
		return nil
	}
	groups, _ := h.DetectDuplicates(context.Background(), stream)
	if len(groups) != 0 {
		t.Errorf("directories should be skipped, got %d groups", len(groups))
	}
}

func TestHasher_LargeFileDuplicate(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "big1.bin")
	b := filepath.Join(dir, "big2.bin")
	// 200KB random-looking content
	content := make([]byte, 200*1024)
	for i := range content {
		content[i] = byte(i % 251)
	}
	must(t, os.WriteFile(a, content, 0o644))
	must(t, os.WriteFile(b, content, 0o644))

	h := NewHasher(HashOptions{Workers: 2, HeaderBytes: 64 * 1024})
	candidates := []Entry{
		{Path: a, Size: int64(len(content)), IsDir: false},
		{Path: b, Size: int64(len(content)), IsDir: false},
	}
	stream := func(cb func(EntryRow) error) error {
		for _, e := range candidates {
			if err := cb(RowFromEntry(e, "2020", "", e.Depth)); err != nil {
				return err
			}
		}
		return nil
	}
	groups, err := h.DetectDuplicates(context.Background(), stream)
	if err != nil {
		t.Fatalf("DetectDuplicates: %v", err)
	}
	// Should reach stage 3 since size > HeaderBytes.
	found := false
	for _, g := range groups {
		if g.Stage == 3 && len(g.Paths) == 2 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a stage-3 group with 2 paths, got: %+v", groups)
	}
}

func TestHasher_HeaderDistinguishes(t *testing.T) {
	// Two files with same size but different first bytes.
	dir := t.TempDir()
	a := filepath.Join(dir, "a.bin")
	b := filepath.Join(dir, "b.bin")
	// Make both files 128 bytes long (> default HeaderBytes? no, HeaderBytes=64KB, so use 70KB)
	const size = 70 * 1024
	acontent := make([]byte, size)
	bcontent := make([]byte, size)
	acontent[0] = 0xAA
	bcontent[0] = 0xBB
	must(t, os.WriteFile(a, acontent, 0o644))
	must(t, os.WriteFile(b, bcontent, 0o644))

	h := NewHasher(HashOptions{Workers: 2, HeaderBytes: 64 * 1024})
	candidates := []Entry{
		{Path: a, Size: size, IsDir: false},
		{Path: b, Size: size, IsDir: false},
	}
	stream := func(cb func(EntryRow) error) error {
		for _, e := range candidates {
			if err := cb(RowFromEntry(e, "2020", "", e.Depth)); err != nil {
				return err
			}
		}
		return nil
	}
	groups, _ := h.DetectDuplicates(context.Background(), stream)
	// Header differs → should not appear in any duplicate group.
	for _, g := range groups {
		for _, p := range g.Paths {
			if p == a || p == b {
				t.Errorf("expected distinct headers → no group, but %s appeared in %+v", p, g)
			}
		}
	}
}

func TestDuplicateGroup_WastedBytes(t *testing.T) {
	g := DuplicateGroup{Size: 100, Paths: []string{"a", "b", "c", "d"}}
	if got := g.WastedBytes(); got != 300 {
		t.Errorf("expected 300 wasted, got %d", got)
	}
	if g.WastedBytes() != 0 {
		// sanity: would panic if not initialized? no, len(0)=0 → 0
	}
}