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
	case m.keymap.Expand.Matches(key):
		m.toggleExpand(true)
		return m, nil
	case m.keymap.Collapse.Matches(key):
		m.toggleExpand(false)
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
	// Skip section header rows.
	for m.cursor < len(m.flat) && m.flat[m.cursor].depth < 0 {
		m.cursor++
	}
	if m.cursor >= len(m.flat) {
		m.cursor = len(m.flat) - 1
	}
}

func (m *Model) toggleExpand(expand bool) {
	if m.cursor >= len(m.flat) {
		return
	}
	m.expanded[m.cursor] = expand
}

func (m *Model) toggleMark() {
	if m.cursor >= len(m.flat) {
		return
	}
	item := m.flat[m.cursor]
	if item.depth < 0 {
		return
	}
	if m.marked[item.entry.Path] {
		delete(m.marked, item.entry.Path)
	} else {
		m.marked[item.entry.Path] = true
	}
}