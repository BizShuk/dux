package svc

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// ErrNoScans is returned by LatestDate when csvDir has no scan CSV files.
// Callers translate this to the user-facing "no scans yet" message.
var ErrNoScans = errors.New("csv: no scans yet")

// CSV column indices, populated by detectSchema.
const (
	colPath = iota
	colIsDir
	colDepth
	colSize
	colChangeSize
	colIsDuplicate
	colLastAccessed
	colCategory
	numColsV2
)

// schemaV1 = pre-2026-07-12 layout, 6 columns, no is_dir/depth.
// schemaV2 = current layout, 8 columns.
type schema int

const (
	schemaUnknown schema = iota
	schemaV1
	schemaV2
)

// ReadCSV parses a single CSV produced by WriteCSV and returns the entries
// in the order they appear. Both v1 (6 columns) and v2 (8 columns) are
// supported; v1 rows are decoded with IsDir=false, Depth=0.
//
// The BOM at file start is consumed automatically.
func ReadCSV(path string) ([]ReportEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("csv: open %s: %w", path, err)
	}
	defer f.Close()

	br := bufio.NewReader(f)
	// Strip UTF-8 BOM if present.
	if first, err := br.Peek(3); err == nil && bytes.Equal(first, []byte{0xEF, 0xBB, 0xBF}) {
		_, _ = br.Discard(3)
	}

	r := csv.NewReader(br)
	r.FieldsPerRecord = -1 // allow variable until schema is known
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("csv: parse %s: %w", path, err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	header := rows[0]
	sch, colIdx, err := detectSchema(header)
	if err != nil {
		return nil, err
	}

	out := make([]ReportEntry, 0, len(rows)-1)
	for i, rec := range rows[1:] {
		e, err := parseRow(rec, sch, colIdx)
		if err != nil {
			slog.Warn("csv: skip bad row",
				"file", path, "line", i+2, "err", err)
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// LatestDate returns the most recent scan date (YYYY-MM-DD) found in csvDir,
// by inspecting the stem of every *.csv file. The max is computed by
// string-comparing ISO-8601 dates — equivalent to chronological comparison
// because the format is zero-padded.
//
// Returns ErrNoScans when the directory is empty or contains no valid CSVs.
func LatestDate(csvDir string) (string, error) {
	dates, err := AvailableDates(csvDir)
	if err != nil {
		return "", err
	}
	if len(dates) == 0 {
		return "", ErrNoScans
	}
	return dates[len(dates)-1], nil
}

// AvailableDates returns all valid scan dates (YYYY-MM-DD) in csvDir,
// sorted ascending. Files whose stem is not a parseable date are silently
// skipped (a warning is logged at debug level only).
func AvailableDates(csvDir string) ([]string, error) {
	entries, err := os.ReadDir(csvDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("csv: read dir %s: %w", csvDir, err)
	}
	dates := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) != ".csv" {
			continue
		}
		stem := name[:len(name)-len(".csv")]
		if _, perr := time.Parse("2006-01-02", stem); perr != nil {
			slog.Debug("csv: skipping non-date file", "name", name)
			continue
		}
		dates = append(dates, stem)
	}
	sort.Strings(dates)
	return dates, nil
}

// detectSchema inspects the header row and returns the layout plus the
// column-index map (only the columns we actually consume are mapped).
func detectSchema(header []string) (schema, map[string]int, error) {
	idx := make(map[string]int, len(header))
	for i, h := range header {
		idx[h] = i
	}
	// v2 requires is_dir + depth explicitly.
	if _, ok := idx["is_dir"]; ok {
		if _, ok2 := idx["depth"]; ok2 {
			return schemaV2, idx, nil
		}
	}
	// v1: path, size, change_size, is_duplicate, last_accessed, category.
	if _, ok := idx["path"]; ok {
		if _, ok2 := idx["size"]; ok2 {
			return schemaV1, idx, nil
		}
	}
	return schemaUnknown, nil, fmt.Errorf("csv: unrecognized header: %v", header)
}

// parseRow converts a CSV record into a ReportEntry. Missing optional
// columns (v1) yield zero-valued IsDir/Depth; missing required columns
// produce an error so the row is skipped.
func parseRow(rec []string, sch schema, idx map[string]int) (ReportEntry, error) {
	get := func(name string) (string, bool) {
		i, ok := idx[name]
		if !ok || i >= len(rec) {
			return "", false
		}
		return rec[i], true
	}
	pathStr, ok := get("path")
	if !ok || pathStr == "" {
		return ReportEntry{}, errors.New("missing path")
	}
	sizeStr, ok := get("size")
	if !ok {
		return ReportEntry{}, errors.New("missing size")
	}
	size, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		return ReportEntry{}, fmt.Errorf("size: %w", err)
	}

	e := ReportEntry{Path: pathStr, Size: size}

	if sch == schemaV2 {
		if s, ok := get("is_dir"); ok {
			e.IsDir, _ = strconv.ParseBool(s)
		}
		if s, ok := get("depth"); ok {
			d, derr := strconv.Atoi(s)
			if derr == nil {
				e.Depth = d
			}
		}
	}

	if s, ok := get("change_size"); ok {
		if cs, cerr := strconv.ParseInt(s, 10, 64); cerr == nil {
			e.ChangeSize = cs
		}
	}
	if s, ok := get("is_duplicate"); ok {
		e.IsDuplicate, _ = strconv.ParseBool(s)
	}
	if s, ok := get("last_accessed"); ok && s != "" {
		if t, terr := time.Parse(time.RFC3339, s); terr == nil {
			e.LastAccessed = t
		}
	}
	if s, ok := get("category"); ok {
		e.Category = s
	}
	return e, nil
}

// readCSVRows is a small helper for tests that bypass the BOM dance and
// read from an io.Reader. Not exported.
func readCSVRows(r io.Reader) ([][]string, error) {
	return csv.NewReader(r).ReadAll()
}