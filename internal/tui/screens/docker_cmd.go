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

type dockerCommand struct {
	label string
	cmd   string // may contain %s which is replaced with container name
	desc  string
}

type dockerGroup struct {
	name     string
	commands []dockerCommand
}

var dockerGroups = []dockerGroup{
	{
		name: "Container",
		commands: []dockerCommand{
			{"inspect", "docker inspect %s", "Show full container metadata"},
			{"stats", "docker stats %s --no-stream", "Display resource usage statistics"},
			{"top", "docker top %s", "Display running processes inside container"},
			{"restart", "docker restart %s", "Restart the container"},
			{"stop", "docker stop %s", "Stop the container"},
			{"start", "docker start %s", "Start the container"},
			{"pause", "docker pause %s", "Pause all processes in the container"},
			{"unpause", "docker unpause %s", "Unpause all processes in the container"},
		},
	},
	{
		name: "Filesystem",
		commands: []dockerCommand{
			{"diff", "docker diff %s", "Inspect changes to files or dirs on the filesystem"},
			{"cp from container", "docker cp %s:/var/www/html /tmp/container-backup", "Copy files from container to host"},
		},
	},
	{
		name: "Network",
		commands: []dockerCommand{
			{"network inspect", "docker inspect --format='{{json .NetworkSettings.Networks}}' %s", "Show container network configuration"},
			{"port", "docker port %s", "List port mappings for the container"},
		},
	},
	{
		name: "Image",
		commands: []dockerCommand{
			{"image inspect", "docker inspect --format='{{.Config.Image}}' %s", "Show image used by container"},
			{"history", "docker history $(docker inspect --format='{{.Config.Image}}' %s)", "Show image layer history"},
		},
	},
}

type dockerRow struct {
	isHeader bool
	label    string
	cmd      dockerCommand
	cmdIdx   int
}

// DockerCmdScreen shows a flat list of docker commands for the active container.
type DockerCmdScreen struct {
	containerName string
	rows          []dockerRow
	cmds          []dockerCommand // flat list
	selected      int
	scrollOff     int
	width         int
	height        int
}

func NewDockerCmdScreen(containerName string, width, height int) *DockerCmdScreen {
	s := &DockerCmdScreen{
		containerName: containerName,
		width:         width,
		height:        height,
	}
	s.buildRows()
	return s
}

func (s *DockerCmdScreen) buildRows() {
	s.rows = nil
	s.cmds = nil
	for _, g := range dockerGroups {
		s.rows = append(s.rows, dockerRow{isHeader: true, label: g.name, cmdIdx: -1})
		for _, c := range g.commands {
			s.rows = append(s.rows, dockerRow{cmd: c, cmdIdx: len(s.cmds)})
			s.cmds = append(s.cmds, c)
		}
	}
	s.selected = 0
}

func (s *DockerCmdScreen) Init() tea.Cmd { return nil }

func (s *DockerCmdScreen) visibleRows() int {
	v := s.height - 4
	if v < 3 {
		v = 3
	}
	return v
}

func (s *DockerCmdScreen) rowOfSelected() int {
	for i, r := range s.rows {
		if !r.isHeader && r.cmdIdx == s.selected {
			return i
		}
	}
	return 0
}

func (s *DockerCmdScreen) clampScroll() {
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

func (s *DockerCmdScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
				dc := s.cmds[s.selected]
				hostCmd := fmt.Sprintf(dc.cmd, docker.ShellQuote(s.containerName))
				title := dc.label
				return s, func() tea.Msg {
					return msgs.PushOutputMsg{
						Title: title,
						Host:  docker.HostCommand{Cmd: hostCmd},
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

func (s *DockerCmdScreen) View() string {
	title := styles.TitleBar.Render("Docker Commands — " + s.containerName)

	groupStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#F1C40F")).Bold(true)
	cmdStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#2ECC71"))
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

		if r.cmdIdx == s.selected {
			line := "  " + paddedLabel + " " + desc
			lines = append(lines, selStyle.Width(s.width-2).Render(line))
		} else {
			lines = append(lines, "  "+cmdStyle.Render(paddedLabel)+" "+descStyle.Render(desc))
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
