package du

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// excludeSet holds glob patterns used to skip files/directories during walk.
// Mirrors coreutils du's --exclude / --exclude-from / -X functionality.
type excludeSet struct {
	patterns []string
}

// newExcludeSet creates an excludeSet from the given glob patterns.
func newExcludeSet(patterns []string) *excludeSet {
	filtered := make([]string, 0, len(patterns))
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p != "" {
			filtered = append(filtered, p)
		}
	}
	return &excludeSet{patterns: filtered}
}

// merge adds all patterns from other into this set.
func (e *excludeSet) merge(other *excludeSet) {
	if other == nil {
		return
	}
	e.patterns = append(e.patterns, other.patterns...)
}

// match returns true if the given path or basename should be excluded.
// Patterns are matched against the basename using filepath.Match (glob).
// If a pattern contains a path separator, it is matched against the full path.
func (e *excludeSet) match(fullPath, basename string) bool {
	for _, pattern := range e.patterns {
		// Pattern with separator → match against full path.
		if strings.Contains(pattern, string(filepath.Separator)) || strings.Contains(pattern, "/") {
			if ok, _ := filepath.Match(pattern, fullPath); ok {
				return true
			}
			// Also try matching just the trailing components.
			if ok, _ := filepath.Match(pattern, filepath.Base(fullPath)); ok {
				return true
			}
			continue
		}
		// Basename glob match.
		if ok, _ := filepath.Match(pattern, basename); ok {
			return true
		}
	}
	return false
}

// empty reports whether the set has no patterns.
func (e *excludeSet) empty() bool {
	return len(e.patterns) == 0
}

// loadExcludeFile reads exclude patterns from a file, one per line.
// Empty lines and lines starting with # are ignored.
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
