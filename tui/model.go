package tui

import (
	"github.com/bizshuk/disk-analyzer/svc"

	tea "github.com/charmbracelet/bubbletea"
)

// treeItem is one row in the flattened list shown by the TUI.
// It is produced by walking svc.ReportSection entries and inserting
// child entries when their parent is expanded.
type treeItem struct {
	entry      svc.ReportEntry
	section    string // which section this came from
	depth      int    // visual indentation depth
	hasChildren bool
}

// Model is the bubbletea model for the disk-analyzer TUI.
type Model struct {
	sections []svc.ReportSection
	flat     []treeItem
	cursor   int
	expanded map[int]bool // expanded flags per flat index (parent item indices)
	marked   map[string]bool
	keymap   KeyMap
	styles   Styles
	width    int
	height   int
	showHelp bool
	quitting bool
	allowMark bool
}

// NewModel constructs a Model from the given sections.
// allowMark must be true for mark/unmark keys to take effect.
func NewModel(sections []svc.ReportSection, allowMark bool) *Model {
	m := &Model{
		sections:  sections,
		expanded:  make(map[int]bool),
		marked:    make(map[string]bool),
		keymap:    DefaultKeyMap(),
		styles:    DefaultStyles(),
		allowMark: allowMark,
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
	entry := m.flat[m.cursor].entry
	return &entry
}

// rebuildFlat walks sections and produces a flat list of items, expanding
// children of expanded parents. Sections are kept in canonical order.
func (m *Model) rebuildFlat() {
	m.flat = m.flat[:0]
	for _, sec := range m.sections {
		// Section header as a non-selectable line.
		m.flat = append(m.flat, treeItem{
			entry:   svc.ReportEntry{Path: "── " + sec.Title + " ──", Category: "section"},
			section: sec.Name,
			depth:   -1, // negative = not navigable
		})
		for i, e := range sec.Entries {
			m.flat = append(m.flat, treeItem{
				entry:       e,
				section:     sec.Name,
				depth:       0,
				hasChildren: len(sec.Entries) > 1 && i == 0, // placeholder; real tree would have parent/child edges
			})
		}
	}
}

// findNextMarked moves the cursor to the next item whose path is in m.marked.
// Returns the new cursor index, or -1 if no more marked items.
func (m *Model) findNextMarked(from int) int {
	for i := from + 1; i < len(m.flat); i++ {
		if m.flat[i].depth < 0 {
			continue
		}
		if m.marked[m.flat[i].entry.Path] {
			return i
		}
	}
	return -1
}