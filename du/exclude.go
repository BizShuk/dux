package du

import (
	"bufio"
	"log/slog"
	"os"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// excludeSet holds glob patterns used to skip files/directories during walk.
//
// Patterns follow shell-glob syntax with ** support (any number of path
// segments including zero). Examples:
//
//	*.tmp                  — basename match
//	**/node_modules        — node_modules at any depth
//	foo/**/bar             — bar anywhere under foo
//	**/*.log               — all .log files anywhere
//
// Patterns are validated once at construction; invalid ones are dropped with
// a warning so one bad pattern doesn't disable the whole set. Match cost per
// entry is one doublestar.PathMatch per pattern.
type excludeSet struct {
	patterns []string // pre-validated; only valid patterns are stored
}

// newExcludeSet trims and validates patterns. Invalid patterns are skipped
// (logged at WARN) — better to silently under-exclude than to fail the whole
// scan because of one typo.
func newExcludeSet(patterns []string) *excludeSet {
	filtered := make([]string, 0, len(patterns))
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p != "" {
			filtered = append(filtered, p)
		}
	}
	valid := make([]string, 0, len(filtered))
	for _, p := range filtered {
		if !doublestar.ValidatePathPattern(p) {
			slog.Warn("exclude: invalid pattern, skipping",
				"pattern", p)
			continue
		}
		valid = append(valid, p)
	}
	return &excludeSet{patterns: valid}
}

// merge appends another set's patterns into this one.
func (e *excludeSet) merge(other *excludeSet) {
	if other == nil {
		return
	}
	e.patterns = append(e.patterns, other.patterns...)
}

// match reports whether fullPath matches any pattern. Patterns without a
// path separator are matched against basename (legacy du --exclude behaviour:
// "*.tmp" matches every file ending in .tmp regardless of depth). Patterns
// containing "/" are matched against the full path with doublestar ** semantics.
func (e *excludeSet) match(fullPath, basename string) bool {
	for _, p := range e.patterns {
		var name string
		if strings.ContainsRune(p, '/') {
			name = fullPath
		} else {
			name = basename
		}
		if matched, _ := doublestar.PathMatch(p, name); matched {
			return true
		}
	}
	return false
}

// empty reports whether the set has no usable patterns.
func (e *excludeSet) empty() bool {
	return len(e.patterns) == 0
}

// loadExcludeFile reads exclude patterns from a file, one per line.
// Empty lines and lines starting with # are ignored. Invalid patterns are
// dropped with a warning (same policy as newExcludeSet).
func loadExcludeFile(path string) (*excludeSet, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var patterns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return newExcludeSet(patterns), nil
}
