package screens

import (
	"fmt"
	"strings"

	"github.com/alexbabintsev/laradok/internal/config"
	"github.com/alexbabintsev/laradok/internal/msgs"
	"github.com/alexbabintsev/laradok/internal/tui/styles"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// cmdRow is a single renderable row: either a group header or a command entry.
type cmdRow struct {
	isHeader bool
	label    string         // group name (header rows only)
	cmd      config.Command // command (non-header rows only)
	cmdIdx   int            // index in flat commands slice; -1 for headers
}

// CommandsScreen shows a flat scrollable list of all commands grouped by section.
type CommandsScreen struct {
	groups    []config.CommandGroup
	rows      []cmdRow
	cmds      []config.Command // flat list of commands (parallel to cmdIdx)
	selected  int              // index in cmds
	scrollOff int              // first visible row index
	width     int
	height    int
}

func NewCommandsScreen(groups []config.CommandGroup, width, height int) *CommandsScreen {
	s := &CommandsScreen{
		groups: groups,
		width:  width,
		height: height,
	}
	s.buildRows()
	return s
}

func (s *CommandsScreen) buildRows() {
	s.rows = nil
	s.cmds = nil
	for _, g := range s.groups {
		if len(g.Commands) == 0 {
			continue
		}
		s.rows = append(s.rows, cmdRow{isHeader: true, label: g.Name, cmdIdx: -1})
		for _, c := range g.Commands {
			s.rows = append(s.rows, cmdRow{cmd: c, cmdIdx: len(s.cmds)})
			s.cmds = append(s.cmds, c)
		}
	}
	if len(s.cmds) > 0 {
		s.selected = 0
	} else {
		s.selected = -1
	}
}

func (s *CommandsScreen) Init() tea.Cmd { return nil }

func (s *CommandsScreen) visibleRows() int {
	v := s.height - 4 // title(1) + statusBar(1) + padding(2)
	if v < 3 {
		v = 3
	}
	return v
}

func (s *CommandsScreen) rowOfSelected() int {
	for i, r := range s.rows {
		if !r.isHeader && r.cmdIdx == s.selected {
			return i
		}
	}
	return 0
}

func (s *CommandsScreen) clampScroll() {
	if s.selected < 0 {
		return
	}
	vis := s.visibleRows()
	sel := s.rowOfSelected()
	if sel < s.scrollOff {
		s.scrollOff = sel
	}
	if sel >= s.scrollOff+vis {
		s.scrollOff = sel - vis + 1
	}
	if s.scrollOff < 0 {
		s.scrollOff = 0
	}
}

func (s *CommandsScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return s, tea.Quit
		case "esc":
			return s, func() tea.Msg { return msgs.PopMsg{} }
		case "down", "j":
			if s.selected < len(s.cmds)-1 {
				s.selected++
				s.clampScroll()
			}
		case "up", "k":
			if s.selected > 0 {
				s.selected--
				s.clampScroll()
			}
		case "pgdown":
			s.selected += s.visibleRows()
			if s.selected >= len(s.cmds) {
				s.selected = len(s.cmds) - 1
			}
			s.clampScroll()
		case "pgup":
			s.selected -= s.visibleRows()
			if s.selected < 0 {
				s.selected = 0
			}
			s.clampScroll()
		case "enter":
			if s.selected >= 0 && s.selected < len(s.cmds) {
				cmd := s.cmds[s.selected]
				return s, func() tea.Msg {
					return msgs.PushOutputMsg{
						Title:  cmd.Label,
						RawCmd: cmd.Cmd,
					}
				}
			}
		}
	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
	}
	return s, nil
}

func (s *CommandsScreen) View() string {
	title := styles.TitleBar.Render("Commands")

	groupStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#F1C40F")).Bold(true)
	cmdStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#2ECC71"))
	descStyle := lipgloss.NewStyle().Foreground(styles.ColorText)
	selStyle := lipgloss.NewStyle().Background(lipgloss.Color("#1E3A5F")).Bold(true)

	// Compute label column width for alignment
	labelWidth := 0
	for _, c := range s.cmds {
		if l := len(c.Label); l > labelWidth {
			labelWidth = l
		}
	}
	descOffset := labelWidth + 2

	vis := s.visibleRows()
	end := s.scrollOff + vis
	if end > len(s.rows) {
		end = len(s.rows)
	}

	var lines []string
	if len(s.cmds) == 0 {
		lines = append(lines, lipgloss.NewStyle().
			Foreground(styles.ColorTextDim).Padding(0, 2).Render("No commands configured"))
	} else {
		firstCmd, lastCmd := -1, -1
		for _, r := range s.rows[s.scrollOff:end] {
			if r.isHeader {
				lines = append(lines, groupStyle.Render(" "+r.label))
				continue
			}
			if firstCmd < 0 {
				firstCmd = r.cmdIdx
			}
			lastCmd = r.cmdIdx

			paddedLabel := fmt.Sprintf("%-*s", labelWidth, r.cmd.Label)
			desc := r.cmd.Desc
			if desc == "" {
				desc = r.cmd.Cmd
			}
			availDesc := s.width - descOffset - 4
			if availDesc > 0 && len(desc) > availDesc {
				desc = desc[:availDesc-1] + "…"
			} else if availDesc <= 0 {
				desc = ""
			}

			if r.cmdIdx == s.selected {
				line := "  " + paddedLabel + " " + desc
				lines = append(lines, selStyle.Width(s.width-2).Render(line))
			} else {
				line := "  " + cmdStyle.Render(paddedLabel) + " " + descStyle.Render(desc)
				lines = append(lines, line)
			}
		}
		// Scroll indicator
		if len(s.rows) > vis && lastCmd >= 0 {
			indicator := lipgloss.NewStyle().
				Foreground(styles.ColorMuted).Padding(0, 2).
				Render(fmt.Sprintf("-- %d–%d of %d --", firstCmd+1, lastCmd+1, len(s.cmds)))
			lines = append(lines, indicator)
		}
	}

	body := strings.Join(lines, "\n")

	help := styles.StatusBar.Width(s.width).Render(
		styles.StatusBarKey.Render("↑↓") + " navigate  " +
			styles.StatusBarKey.Render("enter") + " run  " +
			styles.StatusBarKey.Render("esc") + " back",
	)

	content := lipgloss.JoinVertical(lipgloss.Left, title, "", body)
	return styles.PinToBottom(s.height, content, help)
}
