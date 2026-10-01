package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/bizshuk/dux/svc"

	tea "github.com/charmbracelet/bubbletea"
)

// treeNode is one node in the directory tree built from the scan rows.
// The DB stores every directory that the scanner encountered (with
// aggregated sizes from bubbleUpSize) plus all files, subject to the
// configured entry-size floor (default: breakdown_threshold = 200MB).
// Sub-threshold rows are dropped at scan time so the DB stays compact
// and the tree only carries items the user cares about.
// Building the tree is purely a path-prefix exercise — no filesystem I/O.
type treeNode struct {
	path     string // absolute path; the unique node key
	name     string // filepath.Base(path) for display
	isDir    bool
	size     int64
	depth    int // path-component depth from a virtual root (0 for the top-level roots)
	parent   *treeNode
	children []*treeNode // sorted by size desc, tie-break path asc
}

// treeItem is one visible row produced by walking the tree with the
// current expansion state. flat []treeItem is what View() iterates.
type treeItem struct {
	node        *treeNode
	depth       int // visual indentation depth
	hasChildren bool
	expanded    bool // only meaningful for directories; mirrors m.expanded[node.path]
}

// Model is the bubbletea model for the dux TUI.
//
// State machine:
//   - rows:      the immutable raw data from the DB
//   - roots:     the top-level treeNode pointers built from rows
//   - expanded:  path -> bool; defaults to "all roots expanded, everything else collapsed"
//   - flat:      derived from roots+expanded by rebuildFlat()
//   - pendingDelete: non-nil while awaiting a `y` confirmation for trash
//   - prevSizes: optional path -> yesterday's byte size; when non-nil, the
//     size column collapses into "<prev> → <today>" so growth is read in
//     one place. Paths missing from yesterday render as "0 → <today>"
//     (new files). Files that existed yesterday but not today are
//     simply not displayed — they were never in rows to begin with.
//
// Cursor is an index into flat. Marked-set is path -> bool.
type Model struct {
	rows             []svc.EntryRow
	roots            []*treeNode
	flat             []treeItem
	cursor           int
	expanded         map[string]bool // path -> expanded; survives rebuildFlat
	marked           map[string]bool
	keymap           KeyMap
	styles           Styles
	width            int
	height           int
	showHelp         bool
	quitting         bool
	allowMark        bool
	privacyRedactSet map[string]bool // lowercase substrings from cfg.privacy_redact_paths; delete refuses any match
	prevSizes        map[string]int64
	deletable        map[string]bool // analyze delete-candidate paths; rendered highlighted
	statusMessage    string          // transient message shown in the status bar after a destructive op
	pendingDelete    *treeNode       // non-nil when waiting for `y` to confirm trash
}

// NewModel constructs a Model from a full set of DB rows for one scan date.
// allowMark must be true for mark/unmark keys to take effect.
//
// Defaults: every top-level root is expanded so the user sees content
// immediately; everything else starts collapsed so the visible list stays
// manageable. The user folds/unfolds with h/l (←/→) and Enter.
//
// privacyRedactPaths is the cfg.privacy_redact_paths list — substrings that
// delete refuses to move to Trash (safety net for .ssh / .aws / .gnupg / etc).
//
// prevSizes is yesterday's path → byte-size snapshot. Pass nil to disable
// the diff column; when non-nil the view appends "<prev> →" to each row so
// growth is visible at a glance. Paths missing from the snapshot render as
// "0 →" (i.e. they were not present yesterday).
//
// deletable is the analyze delete-candidate path set; matching rows render
// in the Deletable style. Pass nil to disable highlighting.
func NewModel(rows []svc.EntryRow, prevSizes map[string]int64, deletable map[string]bool, allowMark bool, privacyRedactPaths []string) *Model {
	roots := buildTree(rows)
	expanded := make(map[string]bool, len(roots))
	for _, r := range roots {
		if r != nil {
			expanded[r.path] = true
		}
	}
	redactSet := make(map[string]bool, len(privacyRedactPaths))
	for _, p := range privacyRedactPaths {
		if p == "" {
			continue
		}
		redactSet[strings.ToLower(p)] = true
	}
	m := &Model{
		rows:             rows,
		roots:            roots,
		expanded:         expanded,
		marked:           make(map[string]bool),
		keymap:           DefaultKeyMap(),
		styles:           DefaultStyles(),
		allowMark:        allowMark,
		privacyRedactSet: redactSet,
		prevSizes:        prevSizes,
		deletable:        deletable,
	}
	m.rebuildFlat()
	return m
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	return nil
}

// Marked returns the set of paths currently marked by the user.
// Empty if --allow-mark was not passed.
func (m *Model) Marked() []string {
	out := make([]string, 0, len(m.marked))
	for p := range m.marked {
		out = append(out, p)
	}
	return out
}

// Cursor returns the entry currently under the cursor, or nil.
func (m *Model) Cursor() *svc.ReportEntry {
	if m.cursor < 0 || m.cursor >= len(m.flat) {
		return nil
	}
	n := m.flat[m.cursor].node
	if n == nil {
		return nil
	}
	return &svc.ReportEntry{Path: n.path, Size: n.size, IsDuplicate: false, Depth: n.depth}
}

// cursorNode returns the node under the cursor, or nil.
func (m *Model) cursorNode() *treeNode {
	if m.cursor < 0 || m.cursor >= len(m.flat) {
		return nil
	}
	return m.flat[m.cursor].node
}

// rebuildFlat walks the tree in pre-order and produces the flat list of
// currently visible rows. Called whenever expansion state changes.
// O(visible) — does not touch collapsed subtrees.
func (m *Model) rebuildFlat() {
	m.flat = m.flat[:0]
	var walk func(n *treeNode, depth int)
	walk = func(n *treeNode, depth int) {
		if n == nil {
			return
		}
		exp := n.isDir && m.expanded[n.path]
		m.flat = append(m.flat, treeItem{
			node:        n,
			depth:       depth,
			hasChildren: len(n.children) > 0,
			expanded:    exp,
		})
		if exp {
			for _, c := range n.children {
				walk(c, depth+1)
			}
		}
	}
	for _, r := range m.roots {
		walk(r, 0)
	}
	// Keep cursor within bounds; if it slid off the end (e.g. a parent was
	// collapsed away), clamp.
	if m.cursor >= len(m.flat) {
		m.cursor = len(m.flat) - 1
	}
	if m.cursor < 0 && len(m.flat) > 0 {
		m.cursor = 0
	}
}

// findNextMarked moves the cursor to the next visible item whose path is in
// m.marked. Returns the new cursor index, or -1 if no more marked items.
func (m *Model) findNextMarked(from int) int {
	for i := from + 1; i < len(m.flat); i++ {
		if m.flat[i].node == nil {
			continue
		}
		if m.marked[m.flat[i].node.path] {
			return i
		}
	}
	return -1
}

// buildTree constructs the forest of treeNodes from the flat DB rows.
//
// Algorithm:
//  1. Create a node per row.
//  2. For each node, find its parent: the longest existing row whose
//     path is a directory-prefix of this node's path
//     (i.e. node.path == parent.path + "/" + rest).
//  3. Nodes with no parent become top-level roots.
//  4. Children are sorted by size desc, then path asc.
func buildTree(rows []svc.EntryRow) []*treeNode {
	byPath := make(map[string]*treeNode, len(rows))
	for _, r := range rows {
		byPath[r.Path] = &treeNode{
			path:  r.Path,
			name:  filepath.Base(r.Path),
			isDir: r.IsDir,
			size:  r.SizeBytes,
			depth: r.Depth,
		}
	}
	var roots []*treeNode
	for _, n := range byPath {
		parent := findParent(n.path, byPath)
		if parent == nil {
			roots = append(roots, n)
		} else {
			n.parent = parent
			parent.children = append(parent.children, n)
		}
	}
	// Sort roots and children by size desc, path asc.
	sortNodes := func(ns []*treeNode) {
		sort.SliceStable(ns, func(i, j int) bool {
			if ns[i].size != ns[j].size {
				return ns[i].size > ns[j].size
			}
			return ns[i].path < ns[j].path
		})
	}
	sortNodes(roots)
	for _, n := range byPath {
		if len(n.children) > 0 {
			sortNodes(n.children)
		}
	}
	return roots
}

// findParent walks up the path hierarchy of p looking for the longest
// existing directory row whose path is a prefix of p. Returns nil if no
// parent is in the map — the caller treats such nodes as roots.
//
// Walks filepath.Dir() up the path so we don't depend on the scanner
// having emitted every intermediate directory (it does, but defensively).
func findParent(p string, byPath map[string]*treeNode) *treeNode {
	dir := filepath.Dir(p)
	for {
		if dir == "" || dir == "." || dir == p {
			return nil
		}
		if candidate, ok := byPath[dir]; ok && candidate.isDir {
			return candidate
		}
		if dir == "/" {
			return nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

// trashDir returns the OS-appropriate trash directory for the current user.
//
//   - macOS: $HOME/.Trash (Finder / Files.app both surface this)
//   - Linux: $XDG_DATA_HOME/Trash/files or $HOME/.local/share/Trash/files
//
// We don't try to invoke `gio trash` / `trash-cli` — those aren't always
// installed — so the move itself is just a directory rename below.
func trashDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, ".Trash")
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "Trash", "files")
	}
	return filepath.Join(home, ".local", "share", "Trash", "files")
}

// uniqueTrashPath returns a destination inside trashDir that doesn't collide
// with anything already there. Uses a unix-nanos suffix; collisions are
// extraordinarily unlikely on a single-user trash.
func uniqueTrashPath(trash, srcPath string) string {
	base := filepath.Base(srcPath)
	dest := filepath.Join(trash, base)
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		return dest
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	return fmt.Sprintf("%s-%d%s", filepath.Join(trash, stem), time.Now().UnixNano(), ext)
}

// isPrivacyPath reports whether path contains any substring from the
// configured privacy_redact_paths list (case-insensitive). Used by
// deleteAtCursor to refuse moving sensitive paths to Trash.
func (m *Model) isPrivacyPath(path string) bool {
	lower := strings.ToLower(path)
	for substr := range m.privacyRedactSet {
		if substr == "" {
			continue
		}
		if strings.Contains(lower, substr) {
			return true
		}
	}
	return false
}

// requestDeleteAtCursor is the first half of the trash flow. It runs the
// same pre-flight checks deleteAtCursor used to (privacy-redact refusal,
// redacted placeholder refusal, trash-dir resolvability, mkdir) and on
// success sets m.pendingDelete so the user must press `y` to actually
// move the file. While pending, the status bar shows the confirmation
// prompt.
//
// Refusals (privacy / redacted / trash-dir) write a statusMessage and
// return WITHOUT setting pendingDelete — there's nothing to confirm.
func (m *Model) requestDeleteAtCursor() {
	n := m.cursorNode()
	if n == nil {
		m.statusMessage = "nothing to delete"
		return
	}
	if strings.HasPrefix(n.path, "<") {
		m.statusMessage = "refused: redacted placeholder"
		return
	}
	if m.isPrivacyPath(n.path) {
		m.statusMessage = "refused: matches privacy_redact_paths → " + n.path
		return
	}
	trash := trashDir()
	if trash == "" {
		m.statusMessage = "refused: cannot determine trash dir"
		return
	}
	// Mkdir up front so the user doesn't get a confirm prompt only to
	// fail at rename time.
	if err := os.MkdirAll(trash, 0o755); err != nil {
		m.statusMessage = "trash mkdir failed: " + err.Error()
		return
	}
	m.pendingDelete = n
	m.statusMessage = fmt.Sprintf("delete '%s'? press y to confirm, any other key to cancel", n.name)
}

// executeDelete performs the actual rename into the system trash and
// detaches the node from the tree. Called by handleKey only after the
// user presses `y` to confirm a pendingDelete. Safe to call with a nil
// argument — it's a no-op then.
func (m *Model) executeDelete(n *treeNode) {
	if n == nil {
		return
	}
	trash := trashDir()
	dest := uniqueTrashPath(trash, n.path)
	if err := os.Rename(n.path, dest); err != nil {
		m.statusMessage = "mv to trash failed: " + err.Error()
		return
	}
	// Detach from tree so the user sees the row disappear immediately.
	m.detachNode(n)
	m.rebuildFlat()
	m.statusMessage = fmt.Sprintf("moved to trash: %s → %s", n.path, dest)
}

// detachNode removes n from its parent's children slice (or from the roots
// slice if n is a top-level root). Children of n are dropped from view
// entirely — they're not re-attached to n's parent, because they were
// measured through n and now n is gone. If the user wants to see them,
// they can re-scan.
func (m *Model) detachNode(n *treeNode) {
	if n == nil {
		return
	}
	if n.parent != nil {
		for i, c := range n.parent.children {
			if c == n {
				n.parent.children = append(n.parent.children[:i], n.parent.children[i+1:]...)
				break
			}
		}
		n.parent = nil
		return
	}
	// Top-level root.
	for i, r := range m.roots {
		if r == n {
			m.roots = append(m.roots[:i], m.roots[i+1:]...)
			return
		}
	}
}
