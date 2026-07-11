// stat_other.go provides fallback stat helpers for non-unix platforms.
//
//go:build !darwin && !linux

package du

import "os"

// devIno is a device/inode pair used for hardlink deduplication.
type devIno struct {
	Dev uint64
	Ino uint64
}

// getDevIno always returns ok=false on non-unix platforms.
func getDevIno(info os.FileInfo) (di devIno, ok bool) {
	return devIno{}, false
}

// diskBlocks falls back to info.Size() on non-unix platforms.
func diskBlocks(info os.FileInfo) uint64 {
	sz := info.Size()
	if sz < 0 {
		return 0
	}
	return uint64(sz)
}

// fileSize returns the logical file size on non-unix platforms.
// The apparent flag has no effect since block counts are unavailable.
func fileSize(info os.FileInfo, apparent bool) uint64 {
	sz := info.Size()
	if sz < 0 {
		return 0
	}
	return uint64(sz)
}

// getDev always returns ok=false on non-unix platforms.
func getDev(info os.FileInfo) (uint64, bool) {
	return 0, false
}

// getNlink always returns ok=false on non-unix platforms.
func getNlink(info os.FileInfo) (uint64, bool) {
	return 0, false
}
