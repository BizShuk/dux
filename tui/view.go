package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/bizshuk/dux/svc"

	"github.com/charmbracelet/lipgloss"
)

// View implements tea.Model.
func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	if m.showHelp {
		return m.renderHelp()
	}
	return m.renderList()
}

func (m *Model) renderList() string {
	if len(m.flat) == 0 {
		return m.styles.Warning.Render("No scan results to display.")
	}
	width := m.width
	if width <= 0 {
		width = 100
	}
	height := m.height
	if height <= 0 {
		height = 30
	}
	// Layout budget.
	headerLines := 4 // title, mini-help, blank, blank
	footerLines := 3 // blank, status bar
	visible := height - headerLines - footerLines
	if visible < 5 {
		visible = 5
	}

	var sb strings.Builder
	sb.WriteString(m.styles.Title.Render(" dux — TUI ") + "\n")
	sb.WriteString(m.styles.Section.Render(
		"  ↑/↓ navigate • →/← fold • e expand all • c collapse • enter trash (y confirm) • ?: help • q: quit") + "\n\n")

	// Compute visible window around cursor.
	start := m.cursor - visible/2
	if start < 0 {
		start = 0
	}
	end := start + visible
	if end > len(m.flat) {
		end = len(m.flat)
	}
	if start >= end {
		start = 0
		end = len(m.flat)
		if end > visible {
			end = visible
		}
	}

	for i := start; i < end; i++ {
		sb.WriteString(m.renderRow(i) + "\n")
	}

	// Scroll indicator.
	switch {
	case start > 0 && end < len(m.flat):
		sb.WriteString(m.styles.Section.Render(
			fmt.Sprintf("    … %d more rows above • %d below …\n", start, len(m.flat)-end)))
	case start > 0:
		sb.WriteString(m.styles.Section.Render(
			fmt.Sprintf("    … %d more rows above\n", start)))
	case end < len(m.flat):
		sb.WriteString(m.styles.Section.Render(
			fmt.Sprintf("    … %d more rows below\n", len(m.flat)-end)))
	}

	// Status bar.
	markedCount := len(m.marked)
	statusLine := fmt.Sprintf(
		" %d/%d rows • %d marked • cursor: %s ",
		len(m.flat), len(m.flat), markedCount, cursorSummary(m.cursorNode()),
	)
	if m.statusMessage != "" {
		statusLine = m.statusMessage + "  |  " + statusLine
	}
	sb.WriteString(m.styles.StatusBar.Render(statusLine))
	return sb.String()
}

// renderRow renders one treeItem. Layout:
//
//	[indent][fold][name...]   [size]                       (no prev data)
//	[indent][fold][name...]   [<prev> → <today>]           (with prev data)
//
// indent = 2 spaces * depth
// fold   = ▼ / ▶ / · / space
//
// When prevSizes is loaded the size column collapses into a single
// "<prev> → <today>" form so the user reads growth in one place instead
// of scanning two columns. New files (no yesterday entry) render with the
// left side fixed at "0" — i.e. "0 → <today>" — matching the user-facing
// convention. Paths that existed yesterday but not today are simply
// absent from rows, so they don't render at all.
func (m *Model) renderRow(idx int) string {
	item := m.flat[idx]
	n := item.node
	if n == nil {
		return ""
	}

	indent := strings.Repeat("  ", item.depth)
	var fold string
	switch {
	case n.isDir && item.expanded:
		fold = m.styles.Section.Render("▼")
	case n.isDir && item.hasChildren:
		fold = m.styles.Section.Render("▶")
	case n.isDir:
		// empty directory
		fold = "·"
	default:
		fold = " "
	}

	name := n.name
	if name == "" {
		name = n.path
	}
	if m.deletable[n.path] {
		name = m.styles.Deletable.Render(name + " [del]")
	}

	// Size column: either the bare today value or the unified
	// "<prev> → <today>" form when yesterday's snapshot was loaded.
	// The diff suppresses the column entirely when no prev map is set,
	// keeping the default view identical to the pre-feature layout.
	sizeCell := m.styles.Size.Render(svc.FormatBytes(n.size))
	if prev := m.prevCol(n); prev != "" {
		sizeCell = m.styles.Size.Render(prev)
	}
	row := fmt.Sprintf("%s%s %s    %s", indent, fold, name, sizeCell)

	if idx == m.cursor {
		return m.styles.Cursor.Render(row)
	}
	return row
}

// prevCol returns the size-column text when a yesterday snapshot is loaded,
// or "" to signal "fall back to bare today-size". For paths not present in
// yesterday's snapshot the left side of the arrow is fixed at "0", so
// new files render as "0 → <today>" and existing files render as
// "<yesterday> → <today>". Both ends use svc.FormatBytes (1024-base).
//
// Kept as a method (rather than inlined) so the rendering rule is testable
// without standing up the whole tea program.
func (m *Model) prevCol(n *treeNode) string {
	if n == nil || m.prevSizes == nil {
		return ""
	}
	today := svc.FormatBytes(n.size)
	prev, ok := m.prevSizes[n.path]
	if !ok {
		return "0 → " + today
	}
	return svc.FormatBytes(prev) + " → " + today
}

// cursorSummary is a short "what is the cursor on" label for the status bar
// (e.g. "dir:Documents" or "file:report.pdf").
func cursorSummary(n *treeNode) string {
	if n == nil {
		return "-"
	}
	if n.isDir {
		return "dir:" + n.name
	}
	return "file:" + n.name
}

func (m *Model) renderHelp() string {
	var sb strings.Builder
	sb.WriteString(m.styles.Title.Render(" Help ") + "\n\n")
	for _, group := range m.keymap.FullHelp() {
		for _, k := range group {
			sb.WriteString(fmt.Sprintf("  %-12s  %s\n",
				k.Key(), k.Desc()))
		}
		sb.WriteString("\n")
	}
	if !m.allowMark {
		sb.WriteString(m.styles.Warning.Render("  Note: mark/unmark disabled (--allow-mark not set)") + "\n\n")
	}
	sb.WriteString(m.styles.Help.Render("  Press ? again to return."))
	return sb.String()
}

// Cold-data threshold check kept so dependents (tests / future features)
// don't break. Not used in the default view; render flags cold files
// inside CSV exports where it matters more.
func isCold(t time.Time) bool {
	return !t.IsZero() && time.Since(t) > 180*24*time.Hour
}

var _ = lipgloss.NewStyle // keep import alive for future expansions
