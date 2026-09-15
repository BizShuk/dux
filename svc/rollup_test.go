package svc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testRules(t *testing.T) *RuleSet {
	t.Helper()
	rs, err := DefaultRules()
	require.NoError(t, err)
	return rs
}

func entry(path string, size int64) ReportEntry {
	return ReportEntry{Path: path, Size: size, IsDir: true, LastAccessed: time.Unix(0, 0)}
}

func paths(cands []Candidate) []string {
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.Path)
	}
	return out
}

func TestClassifyPrecedence(t *testing.T) {
	rs := testRules(t)

	// keep beats whole: an app bundle's bundled dependencies are not deletable.
	v, _ := rs.Classify("/Applications/Editor.app/Contents/Resources/app/node_modules")
	assert.Equal(t, VerdictKeep, v)

	v, _ = rs.Classify("/Users/x/projects/site/node_modules")
	assert.Equal(t, VerdictWhole, v)

	// Anything unrecognized falls back to human judgement, never to deletion.
	v, _ = rs.Classify("/Users/x/projects/site/assets")
	assert.Equal(t, VerdictReview, v)
}

func TestRollupSuppressesDescendantsOfWholeCandidates(t *testing.T) {
	rows := []ReportEntry{
		entry("/Users/x", 100<<30),
		entry("/Users/x/Library", 50<<30),
		entry("/Users/x/Library/Caches", 40<<30),
		entry("/Users/x/Library/Caches/big-app", 30<<30),
		entry("/Users/x/Library/Caches/big-app/blobs", 25<<30),
	}
	res := Rollup(rows, testRules(t), RollupOptions{Top: 10, ReviewTop: -1})

	assert.Equal(t, []string{"/Users/x/Library/Caches"}, paths(res.Candidates))
	assert.Equal(t, int64(40<<30), res.Total, "children of a deleted folder must not be counted again")
}

func TestRollupDrillsIntoNonDeletableParents(t *testing.T) {
	rows := []ReportEntry{
		entry("/Users/x/Documents", 80<<30),
		entry("/Users/x/Documents/thesis", 60<<30),
		entry("/Users/x/Documents/thesis/node_modules", 20<<30),
	}
	res := Rollup(rows, testRules(t), RollupOptions{Top: 10, ReviewTop: -1})

	assert.Equal(t, []string{"/Users/x/Documents/thesis/node_modules"}, paths(res.Candidates))
	assert.Empty(t, paths(res.Review), "a parent that hides a candidate is not reported again")
}

func TestRollupPrunesKeepSubtrees(t *testing.T) {
	rows := []ReportEntry{
		entry("/Users/x/Library/Mobile Documents", 90<<30),
		entry("/Users/x/Library/Mobile Documents/Cache", 80<<30),
	}
	res := Rollup(rows, testRules(t), RollupOptions{Top: 10, ReviewTop: -1})

	assert.Empty(t, res.Candidates)
	assert.Empty(t, res.Review)
}

func TestRollupCollapsesRepeatedMatches(t *testing.T) {
	rows := []ReportEntry{
		entry("/Users/x/.local/lib/python3.14/a/__pycache__", 3<<30),
		entry("/Users/x/.local/lib/python3.14/b/__pycache__", 2<<30),
		entry("/Users/x/.local/lib/python3.14/c/__pycache__", 1<<30),
	}
	res := Rollup(rows, testRules(t), RollupOptions{Top: 10, ReviewTop: -1})

	require.Len(t, res.Candidates, 1)
	c := res.Candidates[0]
	assert.True(t, c.Collapsed())
	assert.Equal(t, 3, c.Count)
	assert.Equal(t, "/Users/x/.local/lib/python3.14", c.Path)
	assert.Equal(t, int64(6<<30), c.Size)
	assert.Equal(t, int64(6<<30), res.Total)

	expanded := Rollup(rows, testRules(t), RollupOptions{Top: 10, ReviewTop: -1, NoCollapse: true})
	assert.Len(t, expanded.Candidates, 3)
	assert.Equal(t, res.Total, expanded.Total, "collapsing must not change the reclaimable total")
}

func TestRollupReviewTopZeroHidesSection(t *testing.T) {
	rows := []ReportEntry{entry("/Users/x/Documents", 5<<30)}

	assert.Empty(t, Rollup(rows, testRules(t), RollupOptions{ReviewTop: 0}).Review)
	assert.Len(t, Rollup(rows, testRules(t), RollupOptions{ReviewTop: -1}).Review, 1)
}

func TestRollupMinSizeFiltersEntries(t *testing.T) {
	rows := []ReportEntry{
		entry("/Users/x/a/node_modules", 2<<30),
		entry("/Users/x/b/node_modules", 1<<20),
	}
	res := Rollup(rows, testRules(t), RollupOptions{Top: 10, MinSize: 1 << 30})

	assert.Equal(t, []string{"/Users/x/a/node_modules"}, paths(res.Candidates))
	assert.Equal(t, 1, res.Scanned)
}
