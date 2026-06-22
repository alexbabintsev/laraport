package screens

import (
	"fmt"
	"strings"

	"github.com/alexbabintsev/laradok/internal/msgs"
	"github.com/alexbabintsev/laradok/internal/tui/styles"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type redisCommand struct {
	label string
	// args is appended to the resolved redis-cli prefix. A %s placeholder, if
	// present, is left as-is for the user to see (these commands take no input).
	// The sentinel value redisDownloadRDB triggers an RDB snapshot download
	// instead of running a redis-cli command into the output screen.
	args string
	desc string
}

// redisDownloadRDB is the args sentinel marking the "download RDB" action.
const redisDownloadRDB = "@download-rdb"

type redisGroup struct {
	name     string
	commands []redisCommand
}

var redisGroups = []redisGroup{
	{
		name: "Info & Stats",
		commands: []redisCommand{
			{"INFO server", "INFO server", "Version, uptime, process and config paths"},
			{"INFO memory", "INFO memory", "Memory usage, peak, fragmentation ratio"},
			{"INFO stats", "INFO stats", "Total connections, commands, hits/misses"},
			{"INFO clients", "INFO clients", "Connected clients and blocked clients"},
			{"INFO keyspace", "INFO keyspace", "Per-database key counts and expirations"},
			{"INFO replication", "INFO replication", "Master/replica role and replication offset"},
			{"DBSIZE", "DBSIZE", "Number of keys in the current database"},
			{"CLIENT LIST", "CLIENT LIST", "All connected client connections"},
		},
	},
	{
		name: "Keys",
		commands: []redisCommand{
			{"Sample 20 keys", "--scan | head -20", "First 20 keys via non-blocking SCAN"},
			{"Count keys (SCAN)", "--scan | wc -l", "Total key count via non-blocking SCAN"},
			{"Memory usage (top)", "INFO keyspace", "Key distribution across databases"},
			{"Slowlog (last 10)", "SLOWLOG GET 10", "Ten most recent slow commands"},
		},
	},
	{
		name: "Configuration",
		commands: []redisCommand{
			{"maxmemory", "CONFIG GET maxmemory", "Configured memory limit"},
			{"maxmemory-policy", "CONFIG GET maxmemory-policy", "Eviction policy when memory is full"},
			{"save", "CONFIG GET save", "RDB snapshot schedule"},
			{"appendonly", "CONFIG GET appendonly", "Whether AOF persistence is enabled"},
		},
	},
	{
		name: "Maintenance",
		commands: []redisCommand{
			{"PING", "PING", "Check the server responds"},
			{"LASTSAVE", "LASTSAVE", "Unix time of the last successful RDB save"},
			{"LATENCY DOCTOR", "LATENCY DOCTOR", "Human-readable latency diagnosis"},
		},
	},
	{
		name: "Backup",
		commands: []redisCommand{
			{"Download RDB snapshot", redisDownloadRDB, "redis-cli --rdb → ~/Downloads/redis_<ts>.rdb"},
		},
	},
}

type redisRow struct {
	isHeader bool
	label    string
	cmd      redisCommand
	cmdIdx   int
}

// RedisCmdScreen shows curated redis-cli commands for the active container.
type RedisCmdScreen struct {
	containerName string
	cliPrefix     string // resolved `docker exec ... redis-cli [-a pass]`
	rows          []redisRow
	cmds          []redisCommand
	selected      int
	scrollOff     int
	loading       bool
	sp            spinner.Model
	width         int
	height        int
}

func NewRedisCmdScreen(containerName string, width, height int) *RedisCmdScreen {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styles.SpinnerStyle
	s := &RedisCmdScreen{
		containerName: containerName,
		loading:       true,
		sp:            sp,
		width:         width,
		height:        height,
	}
	s.buildRows()
	return s
}

func (s *RedisCmdScreen) buildRows() {
	s.rows = nil
	s.cmds = nil
	for _, g := range redisGroups {
		s.rows = append(s.rows, redisRow{isHeader: true, label: g.name, cmdIdx: -1})
		for _, c := range g.commands {
			s.rows = append(s.rows, redisRow{cmd: c, cmdIdx: len(s.cmds)})
			s.cmds = append(s.cmds, c)
		}
	}
	s.selected = 0
}

func (s *RedisCmdScreen) Init() tea.Cmd { return s.sp.Tick }

func (s *RedisCmdScreen) visibleRows() int {
	v := s.height - 4
	if v < 3 {
		v = 3
	}
	return v
}

func (s *RedisCmdScreen) rowOfSelected() int {
	for i, r := range s.rows {
		if !r.isHeader && r.cmdIdx == s.selected {
			return i
		}
	}
	return 0
}

func (s *RedisCmdScreen) clampScroll() {
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

func (s *RedisCmdScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.RedisReadyMsg:
		s.loading = false
		s.cliPrefix = msg.CLIPrefix
		return s, nil

	case spinner.TickMsg:
		if s.loading {
			var cmd tea.Cmd
			s.sp, cmd = s.sp.Update(msg)
			return s, cmd
		}

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
			if s.loading || s.selected < 0 || s.selected >= len(s.cmds) {
				return s, nil
			}
			rc := s.cmds[s.selected]
			if rc.args == redisDownloadRDB {
				return s, func() tea.Msg { return msgs.PushRedisDumpMsg{} }
			}
			// Pipe-based commands (--scan | head) need a shell; others run direct.
			hostCmd := s.cliPrefix + " " + rc.args
			title := "redis: " + rc.label
			return s, func() tea.Msg {
				return msgs.PushOutputMsg{Title: title, HostCmd: hostCmd}
			}
		}
	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
	}
	return s, nil
}

func (s *RedisCmdScreen) View() string {
	title := styles.TitleBar.Render("Redis — " + s.containerName)

	if s.loading {
		body := lipgloss.NewStyle().Padding(1, 2).Render(
			s.sp.View() + " detecting Redis credentials...",
		)
		help := styles.StatusBar.Width(s.width).Render(styles.StatusBarKey.Render("esc") + " back")
		content := lipgloss.JoinVertical(lipgloss.Left, title, "", body)
		return styles.PinToBottom(s.height, content, help)
	}

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
		styles.StatusBarKey.Render("↑↓")+" navigate  "+
			styles.StatusBarKey.Render("enter")+" run  "+
			styles.StatusBarKey.Render("esc")+" back",
	)

	content := lipgloss.JoinVertical(lipgloss.Left, title, "", body)
	return styles.PinToBottom(s.height, content, help)
}
