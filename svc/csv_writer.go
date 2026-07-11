package svc

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// CSVOptions configures a WriteCSV invocation.
type CSVOptions struct {
	Path     string          // output path; directory is created if missing
	Sections []ReportSection // sections, in display order
}

// SchemaVersion is the current CSV schema version. ReadCSV uses this to
// gracefully tolerate older 6-column CSVs that predate is_dir/depth.
//
//	v1 (deprecated): path,size,change_size,is_duplicate,last_accessed,category
//	v2 (current):    path,is_dir,depth,size,change_size,is_duplicate,last_accessed,category
const SchemaVersion = "v2"

// WriteCSV writes the given sections to path in CSV format with a UTF-8 BOM
// for Excel compatibility, using the v2 schema (8 columns).
//
// Defensive encoding:
//   - UTF-8 validation: invalid byte sequences are replaced with "?" per rune.
//   - newlines inside fields are kept (encoding/csv handles quoting).
//   - The BOM (\xEF\xBB\xBF) is the only non-comment magic written first.
//
// File format example (v2):
//
//	\xEF\xBB\xBF  <-- BOM
//	path,is_dir,depth,size,change_size,is_duplicate,last_accessed,category
//	/,true,0,123456,0,false,,directory
//	/Users,true,1,50000,0,false,2026-07-11T10:30:00Z,directory
//
// Readers (svc.ReadCSV) MUST tolerate the v1 6-column form by treating
// is_dir=false and depth=0 as defaults.
func WriteCSV(opts CSVOptions) error {
	if opts.Path == "" {
		return fmt.Errorf("csv: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(opts.Path), 0o755); err != nil {
		return fmt.Errorf("csv: mkdir %s: %w", filepath.Dir(opts.Path), err)
	}
	f, err := os.Create(opts.Path)
	if err != nil {
		return fmt.Errorf("csv: create %s: %w", opts.Path, err)
	}
	defer f.Close()

	// Write BOM.
	if _, err := f.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return fmt.Errorf("csv: write BOM: %w", err)
	}

	w := csv.NewWriter(f)
	if err := w.Write([]string{
		"path", "is_dir", "depth", "size", "change_size",
		"is_duplicate", "last_accessed", "category",
	}); err != nil {
		return fmt.Errorf("csv: write column header: %w", err)
	}

	for _, sec := range opts.Sections {
		for _, e := range sec.Entries {
			if err := w.Write([]string{
				sanitizeUTF8(e.Path),
				strconv.FormatBool(e.IsDir),
				strconv.Itoa(e.Depth),
				strconv.FormatInt(e.Size, 10),
				strconv.FormatInt(e.ChangeSize, 10),
				strconv.FormatBool(e.IsDuplicate),
				formatTime(e.LastAccessed),
				sanitizeUTF8(e.Category),
			}); err != nil {
				return fmt.Errorf("csv: write row %s: %w", e.Path, err)
			}
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return fmt.Errorf("csv: flush: %w", err)
	}
	return nil
}

// sanitizeUTF8 replaces invalid UTF-8 byte sequences with "?".
// Per the spec: "防禦性的編碼器以確保特殊字元路徑能正確寫入 CSV."
func sanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteByte('?')
			i++
			continue
		}
		b.WriteRune(r)
		i += size
	}
	return b.String()
}

// formatTime renders t as RFC3339 (or empty if zero).
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}