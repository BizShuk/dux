// stat_unix.go provides platform-specific stat helpers for darwin and linux.
//
//go:build darwin || linux

package du

import (
	"os"
	"syscall"
)

// devIno is a device/inode pair used for hardlink deduplication.
// Mirrors coreutils du's di_set concept.
type devIno struct {
	Dev uint64
	Ino uint64
}

// getDevIno extracts the device and inode numbers from a FileInfo.
// Returns ok=false if the underlying Sys() is not a *syscall.Stat_t.
func getDevIno(info os.FileInfo) (di devIno, ok bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return devIno{}, false
	}
	return devIno{
		Dev: uint64(st.Dev),
		Ino: st.Ino,
	}, true
}

// diskBlocks returns the actual on-disk allocation in bytes.
// Uses st_blocks * 512 (POSIX: blocks are always 512-byte units).
// Falls back to info.Size() if Sys() is unavailable.
func diskBlocks(info os.FileInfo) uint64 {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return uint64(info.Size())
	}
	return uint64(st.Blocks) * 512
}

// fileSize returns the size to attribute to this file.
// If apparent is true, uses the logical size (st_size).
// Otherwise, uses actual disk allocation (st_blocks * 512).
func fileSize(info os.FileInfo, apparent bool) uint64 {
	if apparent {
		sz := info.Size()
		if sz < 0 {
			return 0
		}
		return uint64(sz)
	}
	return diskBlocks(info)
}

// getDev returns the device number for a FileInfo, used for --one-file-system.
func getDev(info os.FileInfo) (uint64, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return 0, false
	}
	return uint64(st.Dev), true
}

// getNlink returns the hard link count for a FileInfo.
func getNlink(info os.FileInfo) (uint64, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return 0, false
	}
	return uint64(st.Nlink), true
}
