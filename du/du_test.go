package du

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeTree creates a temporary directory tree for testing.
// Returns the root path.
//
//	root/
//	  a.txt      (1024 bytes)
//	  b.txt      (2048 bytes)
//	  sub/
//	    c.txt    (4096 bytes)
//	    deep/
//	      d.txt  (8192 bytes)
func makeTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Create files with known sizes.
	writeFile(t, filepath.Join(root, "a.txt"), 1024)
	writeFile(t, filepath.Join(root, "b.txt"), 2048)

	sub := filepath.Join(root, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	writeFile(t, filepath.Join(sub, "c.txt"), 4096)

	deep := filepath.Join(sub, "deep")
	require.NoError(t, os.MkdirAll(deep, 0o755))
	writeFile(t, filepath.Join(deep, "d.txt"), 8192)

	return root
}

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	data := make([]byte, size)
	require.NoError(t, os.WriteFile(path, data, 0o644))
}

func TestUsage_BasicDir(t *testing.T) {
	root := makeTree(t)

	result, err := Usage(context.Background(), Options{
		Paths:        []string{root},
		ApparentSize: true,
		AllFiles:     true,
		MaxDepth:     -1,
	})
	require.NoError(t, err)

	// Grand total should be the sum of all file sizes.
	// 1024 + 2048 + 4096 + 8192 = 15360
	// Plus directory entries themselves (size 0 or platform-dependent).
	// With apparent size, directories may have a small size.
	// We check that file sizes are correctly accumulated.
	assert.GreaterOrEqual(t, result.GrandTotal.Size, uint64(15360),
		"grand total should include all file sizes")

	// Should have entries for root, sub, deep, and all 4 files = 7 entries.
	assert.GreaterOrEqual(t, len(result.Entries), 7,
		"should have entries for all files and directories")

	// Verify root entry exists with accumulated size.
	var rootEntry *EntryInfo
	for i := range result.Entries {
		if result.Entries[i].Path == root && result.Entries[i].IsDir {
			rootEntry = &result.Entries[i]
			break
		}
	}
	require.NotNil(t, rootEntry, "root entry should exist")
	assert.GreaterOrEqual(t, rootEntry.Info.Size, uint64(15360),
		"root entry should accumulate all children sizes")
}

func TestUsage_MaxDepth(t *testing.T) {
	root := makeTree(t)

	// MaxDepth=0 should only show the root.
	result, err := Usage(context.Background(), Options{
		Paths:        []string{root},
		ApparentSize: true,
		MaxDepth:     0,
	})
	require.NoError(t, err)

	assert.Len(t, result.Entries, 1, "MaxDepth=0 should only show root")
	assert.Equal(t, root, result.Entries[0].Path)

	// MaxDepth=1 should show root + sub (not deep).
	result, err = Usage(context.Background(), Options{
		Paths:        []string{root},
		ApparentSize: true,
		MaxDepth:     1,
	})
	require.NoError(t, err)

	for _, e := range result.Entries {
		assert.LessOrEqual(t, e.Depth, 1, "no entry should exceed MaxDepth=1")
	}
}

func TestUsage_Threshold(t *testing.T) {
	root := makeTree(t)

	// Positive threshold: only entries >= 4096 bytes.
	result, err := Usage(context.Background(), Options{
		Paths:        []string{root},
		ApparentSize: true,
		AllFiles:     true,
		MaxDepth:     -1,
		Threshold:    4096,
	})
	require.NoError(t, err)

	for _, e := range result.Entries {
		if e.Depth > 0 { // root is always emitted
			assert.GreaterOrEqual(t, e.Info.Size, uint64(4096),
				"entries should meet positive threshold: %s has size %d", e.Path, e.Info.Size)
		}
	}

	// Negative threshold: only entries <= 2048 bytes.
	result, err = Usage(context.Background(), Options{
		Paths:        []string{root},
		ApparentSize: true,
		AllFiles:     true,
		MaxDepth:     -1,
		Threshold:    -2048,
	})
	require.NoError(t, err)

	for _, e := range result.Entries {
		if e.Depth > 0 {
			assert.LessOrEqual(t, e.Info.Size, uint64(2048),
				"entries should meet negative threshold: %s has size %d", e.Path, e.Info.Size)
		}
	}
}

func TestUsage_SeparateDirs(t *testing.T) {
	root := makeTree(t)

	// With SeparateDirs, subdirectory sizes should NOT be included in parent.
	result, err := Usage(context.Background(), Options{
		Paths:        []string{root},
		ApparentSize: true,
		SeparateDirs: true,
		MaxDepth:     -1,
	})
	require.NoError(t, err)

	// Without SeparateDirs.
	resultNormal, err := Usage(context.Background(), Options{
		Paths:        []string{root},
		ApparentSize: true,
		MaxDepth:     -1,
	})
	require.NoError(t, err)

	// Find root entry in both results.
	var sepRoot, normalRoot *EntryInfo
	for i := range result.Entries {
		if result.Entries[i].Path == root {
			sepRoot = &result.Entries[i]
			break
		}
	}
	for i := range resultNormal.Entries {
		if resultNormal.Entries[i].Path == root {
			normalRoot = &resultNormal.Entries[i]
			break
		}
	}
	require.NotNil(t, sepRoot)
	require.NotNil(t, normalRoot)

	// SeparateDirs root should be smaller or equal (only direct children).
	assert.LessOrEqual(t, sepRoot.Info.Size, normalRoot.Info.Size,
		"SeparateDirs root should not include subdirectory sizes")
}

func TestUsage_Exclude(t *testing.T) {
	root := makeTree(t)

	// Exclude *.txt files.
	result, err := Usage(context.Background(), Options{
		Paths:        []string{root},
		ApparentSize: true,
		AllFiles:     true,
		MaxDepth:     -1,
		Excludes:     []string{"*.txt"},
	})
	require.NoError(t, err)

	for _, e := range result.Entries {
		assert.NotContains(t, e.Path, ".txt",
			"excluded .txt files should not appear: %s", e.Path)
	}
}

func TestUsage_HardlinkDedup(t *testing.T) {
	root := t.TempDir()

	// Create a file and a hard link to it.
	origPath := filepath.Join(root, "original.dat")
	linkPath := filepath.Join(root, "hardlink.dat")
	writeFile(t, origPath, 4096)
	err := os.Link(origPath, linkPath)
	if err != nil {
		t.Skip("hard links not supported on this filesystem")
	}

	// Without CountLinks: hardlink should be counted only once.
	result, err := Usage(context.Background(), Options{
		Paths:        []string{root},
		ApparentSize: true,
		AllFiles:     true,
		MaxDepth:     -1,
		CountLinks:   false,
	})
	require.NoError(t, err)

	// With CountLinks: both links should be counted.
	resultCount, err := Usage(context.Background(), Options{
		Paths:        []string{root},
		ApparentSize: true,
		AllFiles:     true,
		MaxDepth:     -1,
		CountLinks:   true,
	})
	require.NoError(t, err)

	// CountLinks total should be larger (4096 more for the duplicate).
	assert.Greater(t, resultCount.GrandTotal.Size, result.GrandTotal.Size,
		"CountLinks should count the hardlink twice")
}

func TestUsage_ApparentVsBlocks(t *testing.T) {
	root := t.TempDir()

	// Create a small file.
	writeFile(t, filepath.Join(root, "small.dat"), 100)

	// Apparent size should be exactly 100.
	resultApparent, err := Usage(context.Background(), Options{
		Paths:        []string{root},
		ApparentSize: true,
		AllFiles:     true,
		MaxDepth:     -1,
	})
	require.NoError(t, err)

	// Disk blocks will typically be at least one filesystem block (4096).
	resultBlocks, err := Usage(context.Background(), Options{
		Paths:        []string{root},
		ApparentSize: false,
		AllFiles:     true,
		MaxDepth:     -1,
	})
	require.NoError(t, err)

	// Find the small.dat entry.
	var apparentSize, blockSize uint64
	for _, e := range resultApparent.Entries {
		if filepath.Base(e.Path) == "small.dat" {
			apparentSize = e.Info.Size
		}
	}
	for _, e := range resultBlocks.Entries {
		if filepath.Base(e.Path) == "small.dat" {
			blockSize = e.Info.Size
		}
	}

	assert.Equal(t, uint64(100), apparentSize, "apparent size should be exact file size")
	// Block size is typically >= 4096 (one FS block) for a 100-byte file.
	// On some filesystems, small files may be inlined, so we just check >= 100.
	assert.GreaterOrEqual(t, blockSize, uint64(100),
		"block size should be at least the file size")
}
