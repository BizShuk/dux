package delete

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
	Use:   "tui [paths...]",
	Short: "Launch interactive TUI to select and delete directories",
	Long:  `Launch an interactive terminal UI allowing users to select target directories and batch delete them.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		targets := DefaultTargets
		if len(args) > 0 {
			targets = make([]TargetItem, len(args))
			for i, arg := range args {
				targets[i] = TargetItem{
					Path:        arg,
					Description: fmt.Sprintf("自訂目錄 (Custom path: %s)", arg),
				}
			}
		}

		p := tea.NewProgram(NewModel(targets))
		_, err := p.Run()
		return err
	},
}

type ItemState struct {
	Target   TargetItem
	Resolved string
	Cmd      string
	Size     uint64
	Exists   bool
	Selected bool
	Deleted  bool
	Err      error
}

type Model struct {
	Items      []ItemState
	Cursor     int
	Confirming bool
	StatusMsg  string
	Quitting   bool
}

func NewModel(targets []TargetItem) Model {
	items := make([]ItemState, len(targets))
	ctx := context.Background()
	for i, t := range targets {
		resolved, size, err := resolveTarget(ctx, t.Path)
		items[i] = ItemState{
			Target:   t,
			Resolved: resolved,
			Cmd:      t.FullCmd(resolved),
			Size:     size,
			Exists:   err == nil,
			Selected: false,
		}
	}
	return Model{
		Items:  items,
		Cursor: 0,
	}
}

type execDoneMsg struct {
	index int
	err   error
}

func (m Model) nextCmdExec() tea.Cmd {
	for i, it := range m.Items {
		if it.Selected && it.Exists && !it.Deleted && len(it.Target.Cmd) > 0 {
			idx := i
			c := exec.Command(it.Target.Cmd[0], it.Target.Cmd[1:]...)
			return tea.ExecProcess(c, func(err error) tea.Msg {
				return execDoneMsg{index: idx, err: err}
			})
		}
	}
	return nil
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case execDoneMsg:
		it := &m.Items[msg.index]
		if msg.err != nil {
			it.Err = msg.err
			it.Selected = false
			m.StatusMsg = fmt.Sprintf("執行失敗 (%s): %v", it.Cmd, msg.err)
		} else {
			it.Deleted = true
			it.Selected = false
			m.StatusMsg = fmt.Sprintf("已執行 %s，釋放 %s", it.Cmd, HumanSize(it.Size))
		}
		return m, m.nextCmdExec()
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.Quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.Cursor > 0 {
				m.Cursor--
			}
		case "down", "j":
			if m.Cursor < len(m.Items)-1 {
				m.Cursor++
			}
		case " ":
			if m.Cursor >= 0 && m.Cursor < len(m.Items) && m.Items[m.Cursor].Exists && !m.Items[m.Cursor].Deleted {
				m.Items[m.Cursor].Selected = !m.Items[m.Cursor].Selected
			}
		case "a":
			allSelected := true
			for _, it := range m.Items {
				if it.Exists && !it.Deleted && !it.Selected {
					allSelected = false
					break
				}
			}
			for i := range m.Items {
				if m.Items[i].Exists && !m.Items[i].Deleted {
					m.Items[i].Selected = !allSelected
				}
			}
		case "d", "enter":
			if m.Confirming {
				deletedCount := 0
				var freed uint64
				for i := range m.Items {
					if m.Items[i].Selected && m.Items[i].Exists && !m.Items[i].Deleted && len(m.Items[i].Target.Cmd) == 0 {
						err := os.RemoveAll(m.Items[i].Resolved)
						if err != nil {
							m.Items[i].Err = err
						} else {
							m.Items[i].Deleted = true
							m.Items[i].Selected = false
							freed += m.Items[i].Size
							deletedCount++
						}
					}
				}
				m.Confirming = false
				if cmd := m.nextCmdExec(); cmd != nil {
					if deletedCount > 0 {
						m.StatusMsg = fmt.Sprintf("已成功刪除 %d 個項目，共釋放 %s", deletedCount, HumanSize(freed))
					}
					return m, cmd
				}
				m.StatusMsg = fmt.Sprintf("已成功刪除 %d 個項目，共釋放 %s", deletedCount, HumanSize(freed))
			} else {
				hasSelected := false
				for _, it := range m.Items {
					if it.Selected && it.Exists && !it.Deleted {
						hasSelected = true
						break
					}
				}
				if hasSelected {
					m.Confirming = true
				} else {
					m.StatusMsg = "請先使用空白鍵 [Space] 勾選欲刪除的項目"
				}
			}
		case "n":
			if m.Confirming {
				m.Confirming = false
				m.StatusMsg = "已取消刪除"
			}
		}
	}
	return m, nil
}

func (m Model) View() string {
	if m.Quitting {
		return "已離開 dux delete TUI。\n"
	}

	var sb strings.Builder
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	cursorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)

	sb.WriteString(headerStyle.Render("=== dux delete TUI 選擇式清理工具 ==="))
	sb.WriteString("\n\n")

	for i, item := range m.Items {
		cursor := "  "
		if i == m.Cursor {
			cursor = cursorStyle.Render("> ")
		}

		checkbox := "[ ] "
		if item.Deleted {
			checkbox = dimStyle.Render("[已刪除] ")
		} else if item.Selected {
			checkbox = selectedStyle.Render("[x] ")
		} else if !item.Exists {
			checkbox = dimStyle.Render("[不存在] ")
		}

		line := fmt.Sprintf("%s%s%s (%s)", cursor, checkbox, item.Target.Description, item.Resolved)
		if item.Exists && !item.Deleted {
			line += fmt.Sprintf(" - %s", HumanSize(item.Size))
		}
		sb.WriteString(line)
		sb.WriteString("\n")
		sb.WriteString(fmt.Sprintf("     指令: %s\n", dimStyle.Render(item.Cmd)))
	}

	sb.WriteString("\n")
	sb.WriteString("--------------------------------------------------\n")
	sb.WriteString("按鍵指南: [↑/k/↓/j] 移動 | [Space] 勾選 | [a] 全選/全取消 | [d/Enter] 刪除 | [q/Esc] 離開\n")

	if m.Confirming {
		sb.WriteString("\n")
		sb.WriteString(alertStyle.Render("確定要刪除所有已勾選的項目？ (按下 'd' / 'Enter' 確認，或 'n' / 'Esc' 取消)"))
		sb.WriteString("\n")
	} else if m.StatusMsg != "" {
		sb.WriteString("\n")
		sb.WriteString(m.StatusMsg)
		sb.WriteString("\n")
	}

	return sb.String()
}
