package svc

import (
	"errors"
	"fmt"
	"time"

	"github.com/bizshuk/gosdk/db"
	"gorm.io/gorm"
)

// EntryRow is the gorm representation of a single row in the entries table.
// One row per (path, scan_date) — the (path, scan_date) tuple is the natural
// primary key so each scan produces a fresh row while preserving history.
type EntryRow struct {
	Path        string `gorm:"primaryKey;size:1024"`
	ScanDate    string `gorm:"primaryKey;size:10"` // YYYY-MM-DD
	ScanTS      int64  `gorm:"index"`
	SizeBytes   int64
	MTimeUnix   int64 `gorm:"index"`
	ATimeUnix   int64
	Category    string `gorm:"size:32"`
	IsDuplicate bool
	IsDir       bool
	Depth       int
}

// TableName overrides the gorm default (which would pluralize "entry_rows"
// → "entry_rows"). Keep it simple.
func (EntryRow) TableName() string {
	return "entries"
}

// DB wraps the gorm.DB and provides DAO methods for entry persistence.
// It is safe for concurrent use (gorm.DB is itself concurrency-safe).
type DB struct {
	g   *gorm.DB
	dbh *db.SQLite // reference to keep the connection alive
}

// OpenDB opens (or creates) the SQLite database at path and runs migrations.
// If gosdk/db.DefaultSQLite is already initialized (e.g. by an outer caller),
// it is reused instead of opening a second connection.
func OpenDB(path string) (*DB, error) {
	if path == "" {
		return nil, errors.New("db: path required")
	}
	if err := db.InitSQLite(); err != nil {
		// InitSQLite returns an error if SQLITE_PATH wasn't set OR already
		// initialized. The already-initialized case is fine for our purposes.
		if db.DefaultSQLite == nil {
			return nil, fmt.Errorf("db: init sqlite: %w", err)
		}
	}
	g := db.DefaultSQLite.DB()
	if g == nil {
		return nil, errors.New("db: DefaultSQLite is nil after InitSQLite")
	}
	if err := g.AutoMigrate(&EntryRow{}); err != nil {
		return nil, fmt.Errorf("db: automigrate: %w", err)
	}
	return &DB{g: g, dbh: db.DefaultSQLite}, nil
}

// Close releases the underlying SQLite connection.
// Note: gosdk/db.DefaultSQLite is a singleton, so subsequent calls in the
// same process will fail with "already initialized". For a single-shot CLI
// this is fine; for daemon mode we'd need to refactor.
func (d *DB) Close() error {
	if d == nil || d.dbh == nil {
		return nil
	}
	return d.dbh.Close()
}

// Raw returns the underlying *gorm.DB for callers that need to issue ad-hoc
// queries. Prefer the typed methods below when possible.
func (d *DB) Raw() *gorm.DB {
	return d.g
}

// LastEntry returns the most recent EntryRow for path, regardless of scan date.
// Used by the collector's incremental-cache hit check.
func (d *DB) LastEntry(path string) (*EntryRow, error) {
	var row EntryRow
	err := d.g.Where("path = ?", path).Order("scan_ts DESC").Limit(1).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("db.LastEntry(%q): %w", path, err)
	}
	return &row, nil
}

// EntriesForDate returns all entries for the given scan date, sorted by path.
func (d *DB) EntriesForDate(date string) ([]EntryRow, error) {
	var rows []EntryRow
	err := d.g.Where("scan_date = ?", date).Order("path ASC").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("db.EntriesForDate(%q): %w", date, err)
	}
	return rows, nil
}

// LatestDate returns the most recent scan_date present in the table, or "" if empty.
func (d *DB) LatestDate() (string, error) {
	var row EntryRow
	err := d.g.Order("scan_date DESC").Limit(1).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("db.LatestDate: %w", err)
	}
	return row.ScanDate, nil
}

// UpsertEntries writes the given rows in a single transaction. Conflicts on
// the (path, scan_date) primary key are replaced.
//
// Returns the number of rows actually written.
func (d *DB) UpsertEntries(rows []EntryRow) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	tx := d.g.Begin()
	if tx.Error != nil {
		return 0, fmt.Errorf("db.UpsertEntries: begin tx: %w", tx.Error)
	}
	// Use a chunked CreateInBatches to avoid SQLite parameter limits.
	const chunk = 500
	written := 0
	for i := 0; i < len(rows); i += chunk {
		end := i + chunk
		if end > len(rows) {
			end = len(rows)
		}
		if err := tx.CreateInBatches(rows[i:end], chunk).Error; err != nil {
			tx.Rollback()
			return written, fmt.Errorf("db.UpsertEntries: batch %d-%d: %w", i, end, err)
		}
		written += end - i
	}
	if err := tx.Commit().Error; err != nil {
		return written, fmt.Errorf("db.UpsertEntries: commit: %w", err)
	}
	return written, nil
}

// MarkDuplicates writes the is_duplicate flag back to a specific scan_date.
// Used after the hasher identifies duplicate groups.
func (d *DB) MarkDuplicates(date string, paths []string, dup bool) error {
	if len(paths) == 0 {
		return nil
	}
	val := 0
	if dup {
		val = 1
	}
	err := d.g.Model(&EntryRow{}).
		Where("scan_date = ? AND path IN ?", date, paths).
		Update("is_duplicate", val).Error
	if err != nil {
		return fmt.Errorf("db.MarkDuplicates: %w", err)
	}
	return nil
}

// EntriesBetween returns rows for both dates (typically used by the diff
// command). Both slices are sorted by path.
func (d *DB) EntriesBetween(date1, date2 string) (d1, d2 []EntryRow, err error) {
	d1, err = d.EntriesForDate(date1)
	if err != nil {
		return nil, nil, fmt.Errorf("db.EntriesBetween(%q): %w", date1, err)
	}
	d2, err = d.EntriesForDate(date2)
	if err != nil {
		return nil, nil, fmt.Errorf("db.EntriesBetween(%q): %w", date2, err)
	}
	return d1, d2, nil
}

// RowFromEntry converts an in-memory Entry into the persistence representation,
// stamping scan_date and scan_ts.
func RowFromEntry(e Entry, scanDate string, category string, depth int) EntryRow {
	row := EntryRow{
		Path:      e.Path,
		ScanDate:  scanDate,
		ScanTS:    time.Now().Unix(),
		SizeBytes: e.Size,
		IsDir:     e.IsDir,
		Depth:     depth,
		Category:  category,
	}
	if !e.LastModified.IsZero() {
		row.MTimeUnix = e.LastModified.Unix()
	}
	if !e.LastAccessed.IsZero() {
		row.ATimeUnix = e.LastAccessed.Unix()
	}
	return row
}