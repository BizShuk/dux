package svc

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
)

// rollup.go answers "what can I delete?" over a scan snapshot.
//
// The whole point is that every byte is counted once. A directory whose entire
// subtree is regenerable is emitted as a single candidate and its descendants are
// suppressed; a directory that is not wholly deletable is never a candidate, so the
// rollup drills into it looking for deletable children instead.

//go:embed rules.json
var defaultRulesJSON []byte

// Verdict is the deletability tier assigned to a path.
type Verdict string

const (
	VerdictKeep   Verdict = "keep"   // never touch, and prune the whole subtree
	VerdictWhole  Verdict = "whole"  // the entire directory is regenerable
	VerdictReview Verdict = "review" // large but needs a human decision
)

// Rule maps a path glob to a verdict. Globs are doublestar patterns matched
// against the absolute path.
type Rule struct {
	Glob string `json:"glob"`
	Why  string `json:"why"`
	// Collapse folds sibling matches of this same rule into one row anchored at
	// their common ancestor. Use it for patterns that repeat dozens of times in
	// one tree (per-version __pycache__, per-simulator caches) and would
	// otherwise crowd out everything else in the top-N.
	Collapse bool    `json:"collapse"`
	Verdict  Verdict `json:"-"`
}

// RuleSet is an ordered rule table. Precedence follows tier order — keep, then
// whole, then review — and the first match inside a tier wins.
type RuleSet struct {
	rules []Rule
}

type rulesFile struct {
	Keep   []Rule `json:"keep"`
	Whole  []Rule `json:"whole"`
	Review []Rule `json:"review"`
}

// DefaultRules returns the rule table embedded in the binary.
func DefaultRules() (*RuleSet, error) { return parseRules(defaultRulesJSON) }

// LoadRules reads a rule table from disk, replacing the embedded default.
func LoadRules(path string) (*RuleSet, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read rules %q: %w", path, err)
	}
	return parseRules(raw)
}

func parseRules(raw []byte) (*RuleSet, error) {
	var f rulesFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse rules: %w", err)
	}
	rs := &RuleSet{}
	for _, tier := range []struct {
		verdict Verdict
		rules   []Rule
	}{{VerdictKeep, f.Keep}, {VerdictWhole, f.Whole}, {VerdictReview, f.Review}} {
		for _, r := range tier.rules {
			if !doublestar.ValidatePattern(r.Glob) {
				return nil, fmt.Errorf("parse rules: invalid glob %q", r.Glob)
			}
			r.Verdict = tier.verdict
			rs.rules = append(rs.rules, r)
		}
	}
	if len(rs.rules) == 0 {
		return nil, fmt.Errorf("parse rules: empty rule table")
	}
	return rs, nil
}

// Rules exposes the table in precedence order for `analyze --show-rules`.
func (rs *RuleSet) Rules() []Rule { return rs.rules }

// Classify returns the verdict for a path. Paths matching no rule default to
// VerdictReview, so anything unrecognized is shown to a human rather than
// proposed for deletion.
func (rs *RuleSet) Classify(path string) (Verdict, Rule) {
	for _, r := range rs.rules {
		if ok, err := doublestar.Match(r.Glob, path); err == nil && ok {
			return r.Verdict, r
		}
	}
	return VerdictReview, Rule{Verdict: VerdictReview}
}

// Candidate is one row of the analysis: a path that can be reclaimed as a unit,
// or — when Count > 1 — a collapsed group of sibling paths anchored at Path.
type Candidate struct {
	Path         string
	Size         int64
	Why          string
	LastAccessed time.Time
	Glob         string
	Count        int      // 1 for a plain row; >1 for a collapsed group
	Members      []string // the grouped paths, set only when Count > 1
}

// Collapsed reports whether the row stands for several paths under Path rather
// than for Path itself. A collapsed row must never be deleted by removing Path.
func (c Candidate) Collapsed() bool { return c.Count > 1 }

// RollupResult separates what can be deleted from what a human must judge.
type RollupResult struct {
	Candidates []Candidate // safe to delete, non-overlapping
	Review     []Candidate // large, not automatically safe, hides nothing reclaimable
	Total      int64       // sum of Candidates; no double counting
	Scanned    int         // entries considered after the size filter
}

// RollupOptions tunes the analysis.
type RollupOptions struct {
	Top        int   // candidates to keep (<=0 for all)
	ReviewTop  int   // manual-review rows to keep (0 hides the section, <0 for all)
	MinSize    int64 // ignore entries smaller than this
	NoCollapse bool  // list every sibling match instead of grouping them
}

// minCollapseSegments keeps a collapsed group anchored somewhere meaningful.
// Grouping matches whose only shared ancestor is "/" or "$HOME" would produce a
// row nobody can act on.
const minCollapseSegments = 3

// Rollup turns scan entries into a non-overlapping deletion plan.
func Rollup(entries []ReportEntry, rs *RuleSet, opts RollupOptions) RollupResult {
	rows := make([]ReportEntry, 0, len(entries))
	for _, e := range entries {
		if e.Path == "" || e.Size < opts.MinSize {
			continue
		}
		rows = append(rows, e)
	}
	// Largest first; shallower path wins a size tie so a parent is considered
	// before the single child that fills it.
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Size != rows[j].Size {
			return rows[i].Size > rows[j].Size
		}
		return strings.Count(rows[i].Path, "/") < strings.Count(rows[j].Path, "/")
	})

	var kept []string
	classified := make([]struct {
		entry   ReportEntry
		verdict Verdict
		rule    Rule
	}, 0, len(rows))
	for _, e := range rows {
		v, r := rs.Classify(e.Path)
		if v == VerdictKeep {
			kept = append(kept, e.Path)
		}
		classified = append(classified, struct {
			entry   ReportEntry
			verdict Verdict
			rule    Rule
		}{e, v, r})
	}

	live := classified[:0:0]
	for _, c := range classified {
		if pathUnderAny(c.entry.Path, kept) {
			continue // inside a keep subtree
		}
		live = append(live, c)
	}

	var candidates []Candidate
	for _, c := range live {
		if c.verdict != VerdictWhole {
			continue
		}
		if isDescendantOfCandidate(c.entry.Path, candidates) {
			continue // an ancestor is already being deleted whole
		}
		candidates = append(candidates, Candidate{
			Path:         c.entry.Path,
			Size:         c.entry.Size,
			Why:          c.rule.Why,
			LastAccessed: c.entry.LastAccessed,
			Glob:         c.rule.Glob,
			Count:        1,
		})
	}

	if !opts.NoCollapse {
		candidates = collapseGroups(candidates, rs)
	}
	sortCandidates(candidates)

	result := RollupResult{Scanned: len(rows)}
	result.Candidates = candidates
	if opts.Top > 0 && len(result.Candidates) > opts.Top {
		result.Candidates = result.Candidates[:opts.Top]
	}
	for _, c := range result.Candidates {
		result.Total += c.Size
	}
	result.Review = selectReview(live, result.Candidates, opts.ReviewTop)
	return result
}

// selectReview picks the biggest entries a human still has to judge. An entry is
// skipped when it sits inside a candidate, when drilling into it already produced
// a candidate, or when a larger review row already covers it.
func selectReview(live []struct {
	entry   ReportEntry
	verdict Verdict
	rule    Rule
}, candidates []Candidate, top int) []Candidate {
	if top == 0 {
		return nil
	}
	var reviews []Candidate
	for _, c := range live {
		if c.verdict != VerdictReview {
			continue
		}
		covered := false
		for _, cand := range candidates {
			if c.entry.Path == cand.Path || pathUnder(c.entry.Path, cand.Path) || pathUnder(cand.Path, c.entry.Path) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		if isDescendantOfCandidate(c.entry.Path, reviews) {
			continue
		}
		why := c.rule.Why
		if why == "" {
			why = "未分類, 自行判斷"
		}
		reviews = append(reviews, Candidate{
			Path:         c.entry.Path,
			Size:         c.entry.Size,
			Why:          why,
			LastAccessed: c.entry.LastAccessed,
			Count:        1,
		})
		if top > 0 && len(reviews) == top {
			break
		}
	}
	return reviews
}

// collapseGroups folds repeated matches of a collapse-flagged rule into one row
// anchored at their common ancestor, so a tree full of __pycache__ costs one line
// instead of twenty.
func collapseGroups(candidates []Candidate, rs *RuleSet) []Candidate {
	collapsible := map[string]bool{}
	for _, r := range rs.Rules() {
		if r.Collapse {
			collapsible[r.Glob] = true
		}
	}
	if len(collapsible) == 0 {
		return candidates
	}

	groups := map[string][]Candidate{}
	order := []string{}
	out := candidates[:0:0]
	for _, c := range candidates {
		if !collapsible[c.Glob] {
			out = append(out, c)
			continue
		}
		if _, seen := groups[c.Glob]; !seen {
			order = append(order, c.Glob)
		}
		groups[c.Glob] = append(groups[c.Glob], c)
	}

	for _, glob := range order {
		members := groups[glob]
		anchor := commonAncestor(members)
		if len(members) < 2 || segmentCount(anchor) < minCollapseSegments {
			out = append(out, members...)
			continue
		}
		merged := Candidate{
			Path:  anchor,
			Why:   members[0].Why,
			Glob:  glob,
			Count: len(members),
		}
		for _, m := range members {
			merged.Size += m.Size
			merged.Members = append(merged.Members, m.Path)
			if m.LastAccessed.After(merged.LastAccessed) {
				merged.LastAccessed = m.LastAccessed
			}
		}
		out = append(out, merged)
	}
	return out
}

func sortCandidates(c []Candidate) {
	sort.SliceStable(c, func(i, j int) bool { return c[i].Size > c[j].Size })
}

func commonAncestor(items []Candidate) string {
	if len(items) == 0 {
		return ""
	}
	prefix := strings.Split(strings.TrimSuffix(items[0].Path, "/"), "/")
	for _, it := range items[1:] {
		parts := strings.Split(strings.TrimSuffix(it.Path, "/"), "/")
		n := 0
		for n < len(prefix) && n < len(parts) && prefix[n] == parts[n] {
			n++
		}
		prefix = prefix[:n]
	}
	// Drop the matched leaf itself so the anchor is a directory that contains
	// the group rather than one of its members.
	if len(prefix) > 0 && prefix[len(prefix)-1] == pathBase(items[0].Path) {
		prefix = prefix[:len(prefix)-1]
	}
	joined := strings.Join(prefix, "/")
	if joined == "" {
		return "/"
	}
	return joined
}

func segmentCount(p string) int {
	n := 0
	for _, seg := range strings.Split(p, "/") {
		if seg != "" {
			n++
		}
	}
	return n
}

func pathUnder(child, parent string) bool {
	return strings.HasPrefix(child, strings.TrimSuffix(parent, "/")+"/")
}

func pathUnderAny(child string, parents []string) bool {
	for _, p := range parents {
		if child == p || pathUnder(child, p) {
			return true
		}
	}
	return false
}

func isDescendantOfCandidate(path string, candidates []Candidate) bool {
	for _, c := range candidates {
		if pathUnder(path, c.Path) {
			return true
		}
	}
	return false
}
