package screens

import (
	"fmt"
	"strings"

	"github.com/alexbabintsev/laraport/internal/docker"
	"github.com/alexbabintsev/laraport/internal/msgs"
	"github.com/alexbabintsev/laraport/internal/tui/styles"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type globalCommand struct {
	label   string
	cmd     string
	desc    string
	confirm bool // destructive — require a yes/no confirmation first
}

type globalGroup struct {
	name     string
	commands []globalCommand
}

var globalGroups = []globalGroup{
	{
		name: "Disk usage",
		commands: []globalCommand{
			{"Docker disk usage", "docker system df", "Space used by images, containers, volumes, build cache", false},
			{"Disk usage (verbose)", "docker system df -v", "Per-image / per-volume breakdown", false},
		},
	},
	{
		name: "Cleanup",
		commands: []globalCommand{
			{"Clean stopped containers", "docker container prune -f", "Remove all stopped containers", true},
			{"Clean unused images", "docker image prune -f", "Remove dangling (untagged) images", true},
			{"Clean unused images (all)", "docker image prune -a -f", "Remove all images not used by a container", true},
			{"Clean unused volumes", "docker volume prune -f", "Remove volumes not used by any container", true},
			{"Clean unused networks", "docker network prune -f", "Remove networks not used by any container", true},
			{"Clean build cache", "docker builder prune -f", "Remove the Docker build cache", true},
			{"Clean all (system prune)", "docker system prune -f", "Remove stopped containers, unused networks, dangling images and build cache", true},
		},
	},
}

type globalRow struct {
	isHeader bool
	label    string
	cmd      globalCommand
	cmdIdx   int
}

// GlobalCmdScreen shows server-level docker commands not tied to a container.
type GlobalCmdScreen struct {
	serverName string
	rows       []globalRow
	cmds       []globalCommand
	selected   int
	scrollOff  int
	width      int
	height     int
}

func NewGlobalCmdScreen(serverName string, width, height int) *GlobalCmdScreen {
	s := &GlobalCmdScreen{
		serverName: serverName,
		width:      width,
		height:     height,
	}
	s.buildRows()
	return s
}

func (s *GlobalCmdScreen) buildRows() {
	s.rows = nil
	s.cmds = nil
	for _, g := range globalGroups {
		s.rows = append(s.rows, globalRow{isHeader: true, label: g.name, cmdIdx: -1})
		for _, c := range g.commands {
			s.rows = append(s.rows, globalRow{cmd: c, cmdIdx: len(s.cmds)})
			s.cmds = append(s.cmds, c)
		}
	}
	s.selected = 0
}

func (s *GlobalCmdScreen) Init() tea.Cmd { return nil }

func (s *GlobalCmdScreen) visibleRows() int {
	v := s.height - 4
	if v < 3 {
		v = 3
	}
	return v
}

func (s *GlobalCmdScreen) rowOfSelected() int {
	for i, r := range s.rows {
		if !r.isHeader && r.cmdIdx == s.selected {
			return i
		}
	}
	return 0
}

func (s *GlobalCmdScreen) clampScroll() {
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

func (s *GlobalCmdScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			if s.selected < 0 || s.selected >= len(s.cmds) {
				return s, nil
			}
			gc := s.cmds[s.selected]
			run := msgs.PushOutputMsg{Title: gc.label, Host: docker.HostCommand{Cmd: gc.cmd}}
			if gc.confirm {
				return s, func() tea.Msg {
					return msgs.PushConfirmMsg{
						Title:  gc.label,
						Detail: gc.cmd,
						Run:    run,
					}
				}
			}
			return s, func() tea.Msg { return run }
		}
	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
	}
	return s, nil
}

func (s *GlobalCmdScreen) View() string {
	title := styles.TitleBar.Render("Docker Cleanup — " + s.serverName)

	groupStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#F1C40F")).Bold(true)
	cmdStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#2ECC71"))
	dangerStyle := lipgloss.NewStyle().Foreground(styles.ColorDanger)
	descStyle := lipgloss.NewStyle().Foreground(styles.ColorText)
	selStyle := lipgloss.NewStyle().Background(lipgloss.Color("#1E3A5F")).Bold(true)

	labelWidth := 0
	for _, c := range s.cmds {
		if l := len(c.label); l > labelWidth {
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

		paddedLabel := fmt.Sprintf("%-*s", labelWidth, r.cmd.label)
		desc := r.cmd.desc
		avail := s.width - descOffset - 4
		if avail > 0 && len(desc) > avail {
			desc = desc[:avail-1] + "…"
		} else if avail <= 0 {
			desc = ""
		}

		ls := cmdStyle
		if r.cmd.confirm {
			ls = dangerStyle
		}
		if r.cmdIdx == s.selected {
			line := "  " + paddedLabel + " " + desc
			lines = append(lines, selStyle.Width(s.width-2).Render(line))
		} else {
			lines = append(lines, "  "+ls.Render(paddedLabel)+" "+descStyle.Render(desc))
		}
	}
	if len(s.rows) > vis && lastCmd >= 0 {
		lines = append(lines, lipgloss.NewStyle().
			Foreground(styles.ColorMuted).Padding(0, 2).
			Render(fmt.Sprintf("-- %d–%d of %d --", firstCmd+1, lastCmd+1, len(s.cmds))))
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
