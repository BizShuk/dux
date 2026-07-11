package svc

import (
	"sort"
	"strings"
	"time"
)

// Category constants — stable strings used in CSV columns and report rows.
const (
	CatMedia   = "media"
	CatArchive = "archive"
	CatCode    = "code"
	CatVM      = "vm"
	CatDoc     = "doc"
	CatOther   = "other"
)

// extToCategory is the lookup table for the fast-path (no magic-byte check).
// Order matters only for readability — all matches are checked.
var extToCategory = map[string]string{
	// media
	".mp4": CatMedia, ".mkv": CatMedia, ".avi": CatMedia, ".mov": CatMedia,
	".webm": CatMedia, ".flv": CatMedia, ".wmv": CatMedia, ".m4v": CatMedia,
	".mp3": CatMedia, ".flac": CatMedia, ".wav": CatMedia, ".aac": CatMedia, ".ogg": CatMedia, ".m4a": CatMedia,
	".jpg": CatMedia, ".jpeg": CatMedia, ".png": CatMedia, ".gif": CatMedia,
	".webp": CatMedia, ".bmp": CatMedia, ".tiff": CatMedia, ".heic": CatMedia,
	// archive
	".zip": CatArchive, ".tar": CatArchive, ".gz": CatArchive, ".tgz": CatArchive,
	".bz2": CatArchive, ".xz": CatArchive, ".7z": CatArchive, ".rar": CatArchive,
	".iso": CatArchive, ".dmg": CatArchive, ".pkg": CatArchive,
	// code
	".go": CatCode, ".py": CatCode, ".js": CatCode, ".jsx": CatCode,
	".ts": CatCode, ".tsx": CatCode, ".rs": CatCode, ".java": CatCode,
	".swift": CatCode, ".kt": CatCode, ".c": CatCode, ".cpp": CatCode,
	".h": CatCode, ".hpp": CatCode, ".rb": CatCode, ".php": CatCode,
	".scala": CatCode, ".cs": CatCode,
	// vm
	".vmdk": CatVM, ".vdi": CatVM, ".vhd": CatVM, ".vhdx": CatVM,
	".qcow2": CatVM, ".img": CatVM, ".ovf": CatVM, ".ova": CatVM,
	// docs
	".pdf": CatDoc, ".doc": CatDoc, ".docx": CatDoc, ".epub": CatDoc,
	".mobi": CatDoc,
}

// CategorizeByExt returns the category for a path based on its extension.
// Returns CatOther if no match. Fast-path only — magic-byte detection is
// performed separately (see CategorizeByContent).
func CategorizeByExt(path string) string {
	ext := strings.ToLower(extDot(path))
	if cat, ok := extToCategory[ext]; ok {
		return cat
	}
	base := strings.ToLower(pathBase(path))
	for _, compound := range []string{".tar.gz", ".tar.bz2", ".tar.xz"} {
		if strings.HasSuffix(base, compound) {
			return CatArchive
		}
	}
	return CatOther
}

// CategorizeByContent returns the category based on magic-byte sniffing.
// For v1, only VM image formats get magic-byte recognition; everything else
// falls back to extension-based categorization.
func CategorizeByContent(path string, head []byte) string {
	if len(head) < 4 {
		return CatOther
	}
	if len(head) >= 4 && string(head[:4]) == "QFI\xfb" {
		return CatVM
	}
	if len(head) >= 4 && string(head[:4]) == "KDMV" {
		return CatVM
	}
	return CategorizeByExt(path)
}

// extDot / pathBase avoid pulling in path/filepath for these hot lookups.
func extDot(p string) string {
	for i := len(p) - 1; i >= 0 && p[i] != '/'; i-- {
		if p[i] == '.' {
			return p[i:]
		}
	}
	return ""
}

func pathBase(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}

// ReportBuilder accumulates ReportSections produced for a single scan.
type ReportBuilder struct {
	sections []ReportSection
}

// NewReportBuilder creates an empty ReportBuilder.
func NewReportBuilder() *ReportBuilder {
	return &ReportBuilder{}
}

// Add appends an entry to a named section. Creates the section if needed.
func (b *ReportBuilder) Add(name string, entry ReportEntry) {
	for i := range b.sections {
		if b.sections[i].Name == name {
			b.sections[i].Entries = append(b.sections[i].Entries, entry)
			return
		}
	}
	b.sections = append(b.sections, ReportSection{
		Name:    name,
		Title:   SectionTitles[name],
		Entries: []ReportEntry{entry},
	})
}

// Set replaces all entries of a section.
func (b *ReportBuilder) Set(name string, entries []ReportEntry) {
	for i := range b.sections {
		if b.sections[i].Name == name {
			b.sections[i].Entries = entries
			return
		}
	}
	b.sections = append(b.sections, ReportSection{
		Name:    name,
		Title:   SectionTitles[name],
		Entries: entries,
	})
}

// Sections returns sections in canonical order.
func (b *ReportBuilder) Sections() []ReportSection {
	order := []string{
		SectionOverviewRoot, SectionOverviewHome,
		SectionBreakdownRoot, SectionBreakdownHome,
		SectionDuplicate, SectionCold, SectionPermissionWarn,
	}
	indexOf := make(map[string]int, len(b.sections))
	for i, s := range b.sections {
		indexOf[s.Name] = i
	}
	out := make([]ReportSection, 0, len(order))
	for _, name := range order {
		if i, ok := indexOf[name]; ok {
			out = append(out, b.sections[i])
		}
	}
	return out
}

// SortEntriesByPath sorts every section's entries by Path ascending.
func (b *ReportBuilder) SortEntriesByPath() {
	for i := range b.sections {
		entries := b.sections[i].Entries
		sort.SliceStable(entries, func(a, b int) bool {
			return entries[a].Path < entries[b].Path
		})
		b.sections[i].Entries = entries
	}
}

// BuildOverview groups entries by depth-prefix under root up to maxDepth and
// returns the top-N directories by size. The "overview" is intentionally
// shallow: maxDepth=2 means we look at "/" → {Users, Library, var, ...} →
// {shuk, public, root, ...}. Per spec: root gets 2 levels.
//
// Only directory entries are considered; files at the boundary are dropped.
//
// Input must already be sorted (or order doesn't matter — we sort by size
// desc at the end anyway). After the CSV-backed refactor, the streamer
// closure is gone — callers load CSV rows directly via svc.ReadCSV.
func BuildOverview(entries []ReportEntry, root string, maxDepth int, topN int) []ReportEntry {
	if maxDepth <= 0 {
		maxDepth = 1
	}
	type pair struct {
		size int64
		path string
	}
	seen := make(map[string]*pair, len(entries))
	for i := range entries {
		e := entries[i]
		if !e.IsDir {
			continue
		}
		if !strings.HasPrefix(e.Path, root) {
			continue
		}
		rel := strings.TrimPrefix(e.Path, root)
		rel = strings.TrimPrefix(rel, "/")
		depth := 0
		if rel != "" {
			depth = len(strings.Split(rel, "/"))
		}
		if depth > maxDepth {
			continue
		}
		if existing, ok := seen[e.Path]; ok {
			if e.Size > existing.size {
				existing.size = e.Size
			}
			continue
		}
		seen[e.Path] = &pair{size: e.Size, path: e.Path}
	}
	out := make([]ReportEntry, 0, len(seen))
	for _, p := range seen {
		out = append(out, ReportEntry{
			Path: p.path, Size: p.size, IsDir: true, Category: "directory", Depth: 0,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Size > out[j].Size })
	if topN > 0 && len(out) > topN {
		out = out[:topN]
	}
	return out
}

// DiffEntries computes the change_size for each path using prev as the
// baseline. prev may be nil (no baseline) → all change_size = current.size.
//
// Returns ReportEntry rows (not Entry[]) because downstream rendering
// (svc.TopGrowth, svc/render.PrintTable) consumes ReportEntry shape.
func DiffEntries(current []Entry, prev []Entry) []ReportEntry {
	if prev == nil {
		out := make([]ReportEntry, 0, len(current))
		for _, e := range current {
			out = append(out, ReportEntry{
				Path: e.Path, IsDir: e.IsDir, Size: e.Size, ChangeSize: e.Size,
				LastAccessed: e.LastAccessed,
				Category:     CategorizeByExt(e.Path),
				Depth:        e.Depth,
			})
		}
		return out
	}
	prevSize := make(map[string]int64, len(prev))
	for _, e := range prev {
		prevSize[e.Path] = e.Size
	}
	out := make([]ReportEntry, 0, len(current))
	for _, e := range current {
		cs := e.Size - prevSize[e.Path]
		out = append(out, ReportEntry{
			Path: e.Path, IsDir: e.IsDir, Size: e.Size, ChangeSize: cs,
			LastAccessed: e.LastAccessed,
			Category:     CategorizeByExt(e.Path),
			Depth:        e.Depth,
		})
	}
	return out
}

// TopGrowth returns the top-N entries sorted by ChangeSize descending,
// for use in the "top-10 growth" historical comparison view.
func TopGrowth(entries []ReportEntry, n int) []ReportEntry {
	if n <= 0 {
		n = 10
	}
	out := append([]ReportEntry(nil), entries...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ChangeSize != out[j].ChangeSize {
			return out[i].ChangeSize > out[j].ChangeSize
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// Compile-time assertion: time import retained for future LastAccessed work.
var _ = time.Time{}