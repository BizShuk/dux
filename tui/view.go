package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/bizshuk/disk-analyzer/svc"

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
	pathW := width - 30
	if pathW < 30 {
		pathW = 30
	}
	var sb strings.Builder
	// Title.
	sb.WriteString(m.styles.Title.Render(" disk-analyzer — TUI ") + "\n")
	sb.WriteString(m.styles.Section.Render("  j/k or ↑/↓: navigate • ?: help • q: quit") + "\n\n")

	// Compute visible window around cursor.
	visible := height - 6 // account for title/help/status
	if visible < 5 {
		visible = 5
	}
	start := m.cursor - visible/2
	if start < 0 {
		start = 0
	}
	end := start + visible
	if end > len(m.flat) {
		end = len(m.flat)
	}

	for i := start; i < end; i++ {
		item := m.flat[i]
		if item.depth < 0 {
			// Section header.
			sb.WriteString(m.styles.Section.Render(item.entry.Path) + "\n")
			continue
		}
		sb.WriteString(m.renderRow(i, item, pathW) + "\n")
	}

	// Status bar.
	sb.WriteString("\n")
	markedCount := len(m.marked)
	sb.WriteString(m.styles.StatusBar.Render(fmt.Sprintf(
		" %d items • %d marked • cursor at %d ",
		len(m.flat), markedCount, m.cursor,
	)))
	return sb.String()
}

func (m *Model) renderRow(idx int, item treeItem, pathW int) string {
	e := item.entry
	// Truncate path.
	path := e.Path
	if len(path) > pathW {
		path = "..." + path[len(path)-(pathW-3):]
	}

	// Indicators.
	var inds []string
	if m.marked[e.Path] {
		inds = append(inds, m.styles.Marked.Render("★"))
	}
	if e.IsDuplicate {
		inds = append(inds, m.styles.Duplicate.Render("◆"))
	}
	if !e.LastAccessed.IsZero() && isCold(e.LastAccessed) {
		inds = append(inds, m.styles.Cold.Render("❄"))
	}
	ind := strings.Join(inds, "")
	if ind == "" {
		ind = "  "
	}

	size := m.styles.Size.Render(svc.FormatBytes(e.Size))
	row := fmt.Sprintf("  %-*s  %12s  %s", pathW, path, size, ind)

	if idx == m.cursor {
		return m.styles.Cursor.Render(row)
	}
	return row
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

func isCold(t time.Time) bool {
	return !t.IsZero() && time.Since(t) > 180*24*time.Hour
}

var _ = lipgloss.NewStyle // keep import alive in case of future expansions