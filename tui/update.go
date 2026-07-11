package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		key := msg.String()
		return m.handleKey(key)
	}
	return m, nil
}

func (m *Model) handleKey(key string) (tea.Model, tea.Cmd) {
	// Pending-delete confirmation: `y` executes the pending trash move,
	// anything else cancels and the key continues to its normal handler
	// so e.g. arrow keys still navigate after a cancel.
	if m.pendingDelete != nil {
		if key == "y" {
			m.executeDelete(m.pendingDelete) // sets statusMessage to "moved to trash: …" on success
			m.pendingDelete = nil
			return m, nil
		}
		m.pendingDelete = nil
		m.statusMessage = "delete cancelled"
		// fall through to the regular handler
	}
	switch {
	case m.keymap.Quit.Matches(key):
		m.quitting = true
		return m, tea.Quit
	case m.keymap.Help.Matches(key):
		m.showHelp = !m.showHelp
		return m, nil
	case m.keymap.Up.Matches(key):
		m.moveCursor(-1)
		return m, nil
	case m.keymap.Down.Matches(key):
		m.moveCursor(+1)
		return m, nil
	case m.keymap.PageUp.Matches(key):
		m.moveCursor(-10)
		return m, nil
	case m.keymap.PageDown.Matches(key):
		m.moveCursor(+10)
		return m, nil
	case m.keymap.Expand.Matches(key):
		m.handleExpand() // right/l/Enter
		return m, nil
	case m.keymap.Collapse.Matches(key):
		m.handleCollapse() // left/h
		return m, nil
	case m.keymap.ExpandAll.Matches(key):
		m.expandAll()
		return m, nil
	case m.keymap.CollapseAll.Matches(key):
		m.collapseAll()
		return m, nil
	case m.keymap.Delete.Matches(key):
		m.requestDeleteAtCursor() // sets m.pendingDelete; confirm via `y`
		return m, nil
	case m.keymap.Mark.Matches(key):
		if m.allowMark {
			m.toggleMark()
		}
		return m, nil
	case m.keymap.Tab.Matches(key):
		next := m.findNextMarked(m.cursor)
		if next >= 0 {
			m.cursor = next
		}
		return m, nil
	}
	return m, nil
}

// moveCursor moves the cursor by delta (clamped). Skips past any rows
// whose node is nil (shouldn't happen, but defensive).
func (m *Model) moveCursor(delta int) {
	if len(m.flat) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.flat) {
		m.cursor = len(m.flat) - 1
	}
}

// handleExpand — right/l/Enter semantics:
//   - on a collapsed directory with children: expand it
//   - on an already-expanded directory: move cursor to its first child
//   - on a file (leaf): no-op
func (m *Model) handleExpand() {
	item := m.cursorItem()
	if item == nil {
		return
	}
	if !item.hasChildren {
		return
	}
	if !item.expanded {
		m.expanded[item.node.path] = true
		m.rebuildFlat()
		return
	}
	// Already expanded: move cursor down to the first child.
	if m.cursor+1 < len(m.flat) {
		m.cursor++
	}
}

// handleCollapse — left/h semantics:
//   - on an expanded directory: collapse it
//   - on a collapsed directory / file: move cursor up to its parent
func (m *Model) handleCollapse() {
	item := m.cursorItem()
	if item == nil {
		return
	}
	if item.hasChildren && item.expanded {
		delete(m.expanded, item.node.path)
		m.rebuildFlat()
		return
	}
	// Move to parent. Parent path is in m.flat[i-1] with depth-1.
	for i := m.cursor - 1; i >= 0; i-- {
		if m.flat[i].depth == item.depth-1 {
			m.cursor = i
			return
		}
	}
}

// cursorItem returns the treeItem under the cursor, or nil.
func (m *Model) cursorItem() *treeItem {
	if m.cursor < 0 || m.cursor >= len(m.flat) {
		return nil
	}
	return &m.flat[m.cursor]
}

// expandAll expands every directory in the tree. Big trees become huge —
// caller can collapse-all to recover.
func (m *Model) expandAll() {
	var walk func(n *treeNode)
	walk = func(n *treeNode) {
		if n == nil {
			return
		}
		if n.isDir && len(n.children) > 0 {
			m.expanded[n.path] = true
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	for _, r := range m.roots {
		walk(r)
	}
	m.rebuildFlat()
}

// collapseAll collapses everything except the top-level roots, restoring
// the default view.
func (m *Model) collapseAll() {
	// Collect keys first; mutating the map during range is unsafe.
	keys := make([]string, 0, len(m.expanded))
	for k := range m.expanded {
		keys = append(keys, k)
	}
	for _, k := range keys {
		m.expanded[k] = false
	}
	for _, r := range m.roots {
		if r != nil {
			m.expanded[r.path] = true
		}
	}
	m.rebuildFlat()
}

func (m *Model) toggleMark() {
	n := m.cursorNode()
	if n == nil {
		return
	}
	if m.marked[n.path] {
		delete(m.marked, n.path)
	} else {
		m.marked[n.path] = true
	}
}