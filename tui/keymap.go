package tui

import "strings"

// KeyMap defines all key bindings for the disk-analyzer TUI.
//
// We deliberately do NOT import github.com/charmbracelet/bubbletea/key because
// in recent bubbletea versions the `key` subpackage is its own module and adds
// an extra dependency. Manual key matching keeps the dependency surface small.
type KeyMap struct {
	Up       Binding
	Down     Binding
	Expand   Binding
	Collapse Binding
	Mark     Binding
	Tab      Binding
	Help     Binding
	Quit     Binding
}

// Binding is a single key with optional alternative matches + help text.
// Mirrors the subset of bubbletea/key.Binding we actually use.
type Binding struct {
	keys []string
	help string
}

// Matches reports whether the given KeyMsg string is one of this binding's keys.
func (b Binding) Matches(msg string) bool {
	for _, k := range b.keys {
		if k == msg {
			return true
		}
	}
	return false
}

// Help returns a copy with key + description for the help bar.
func (b Binding) Help() Binding { return b }

// Key returns the primary key string (first in the list).
func (b Binding) Key() string {
	if len(b.keys) == 0 {
		return ""
	}
	return b.keys[0]
}

// Desc returns the help description.
func (b Binding) Desc() string {
	if b.help == "" {
		return b.Key()
	}
	return b.help
}

// DefaultKeyMap returns the standard key bindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up:       Binding{keys: []string{"up", "k"}, help: "up"},
		Down:     Binding{keys: []string{"down", "j"}, help: "down"},
		Expand:   Binding{keys: []string{"right", "l", "enter"}, help: "expand"},
		Collapse: Binding{keys: []string{"left", "h"}, help: "collapse"},
		Mark:     Binding{keys: []string{"m", " "}, help: "mark/unmark"},
		Tab:      Binding{keys: []string{"tab"}, help: "next marked"},
		Help:     Binding{keys: []string{"?"}, help: "help"},
		Quit:     Binding{keys: []string{"q", "ctrl+c", "esc"}, help: "quit"},
	}
}

// ShortHelp returns bindings for the mini help line.
func (k KeyMap) ShortHelp() []Binding {
	return []Binding{k.Up, k.Down, k.Expand, k.Collapse, k.Mark, k.Quit}
}

// FullHelp returns all bindings grouped for the help overlay.
func (k KeyMap) FullHelp() [][]Binding {
	return [][]Binding{
		{k.Up, k.Down},
		{k.Expand, k.Collapse},
		{k.Mark, k.Tab},
		{k.Help, k.Quit},
	}
}

// String returns the primary key string for a KeyMsg.
// Handles special keys like "ctrl+c" → "\x03" not needed because bubbletea's
// KeyMsg.String() returns the canonical form ("ctrl+c", "up", "down", ...).
func normalizeKeyMsg(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	// Strip common representations back to canonical.
	switch s {
	case " ":
		return "space"
	}
	return s
}