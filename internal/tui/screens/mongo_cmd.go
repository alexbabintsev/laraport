package screens

import (
	"fmt"
	"strings"

	"github.com/alexbabintsev/laradok/internal/docker"
	"github.com/alexbabintsev/laradok/internal/msgs"
	"github.com/alexbabintsev/laradok/internal/tui/styles"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type mongoCommand struct {
	label string
	// js is the JavaScript expression passed to --eval. The sentinel value
	// mongoDownloadArchive instead triggers a mongodump archive download.
	js   string
	desc string
}

// mongoDownloadArchive is the js sentinel marking the "download dump" action.
const mongoDownloadArchive = "@download-archive"

type mongoGroup struct {
	name     string
	commands []mongoCommand
}

var mongoGroups = []mongoGroup{
	{
		name: "Info & Stats",
		commands: []mongoCommand{
			{"Server version", "db.version()", "MongoDB server version"},
			{"Server status", "JSON.stringify(db.serverStatus().host)", "Host, uptime and connection info"},
			{"List databases", "JSON.stringify(db.adminCommand({listDatabases:1}).databases)", "All databases with sizes"},
			{"Current DB stats", "JSON.stringify(db.stats())", "Collections, objects, data/storage size"},
			{"Current operations", "JSON.stringify(db.currentOp().inprog.length)", "Number of in-progress operations"},
		},
	},
	{
		name: "Collections",
		commands: []mongoCommand{
			{"List collections", "JSON.stringify(db.getCollectionNames())", "All collections in the current database"},
			{"Collection counts", "db.getCollectionNames().forEach(c=>print(c+': '+db[c].countDocuments({})))", "Document count per collection"},
			{"Collection sizes", "db.getCollectionNames().forEach(c=>print(c+': '+db[c].stats().size+' bytes'))", "Data size per collection"},
		},
	},
	{
		name: "Indexes",
		commands: []mongoCommand{
			{"List indexes", "db.getCollectionNames().forEach(c=>{print('== '+c);printjson(db[c].getIndexes())})", "Indexes for every collection"},
		},
	},
	{
		name: "Maintenance",
		commands: []mongoCommand{
			{"Ping", "JSON.stringify(db.adminCommand({ping:1}))", "Check the server responds"},
			{"Replica set status", "JSON.stringify(db.adminCommand({replSetGetStatus:1}))", "Replica set members and state"},
			{"Profiling level", "JSON.stringify(db.getProfilingStatus())", "Current profiler level and slow-op threshold"},
		},
	},
	{
		name: "Backup",
		commands: []mongoCommand{
			{"Download dump", mongoDownloadArchive, "mongodump --archive --gzip → ~/Downloads/mongo_<ts>.archive.gz"},
		},
	},
}

type mongoRow struct {
	isHeader bool
	label    string
	cmd      mongoCommand
	cmdIdx   int
}

// MongoCmdScreen shows curated mongo shell commands for the active container.
type MongoCmdScreen struct {
	containerName string
	containerID   string
	mongoBin      string
	user          string
	password      string
	ready         bool
	rows          []mongoRow
	cmds          []mongoCommand
	selected      int
	scrollOff     int
	loading       bool
	sp            spinner.Model
	width         int
	height        int
}

func NewMongoCmdScreen(containerName, containerID, mongoBin string, width, height int) *MongoCmdScreen {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styles.SpinnerStyle
	s := &MongoCmdScreen{
		containerName: containerName,
		containerID:   containerID,
		mongoBin:      mongoBin,
		loading:       true,
		sp:            sp,
		width:         width,
		height:        height,
	}
	s.buildRows()
	return s
}

func (s *MongoCmdScreen) buildRows() {
	s.rows = nil
	s.cmds = nil
	for _, g := range mongoGroups {
		s.rows = append(s.rows, mongoRow{isHeader: true, label: g.name, cmdIdx: -1})
		for _, c := range g.commands {
			s.rows = append(s.rows, mongoRow{cmd: c, cmdIdx: len(s.cmds)})
			s.cmds = append(s.cmds, c)
		}
	}
	s.selected = 0
}

func (s *MongoCmdScreen) Init() tea.Cmd { return s.sp.Tick }

func (s *MongoCmdScreen) visibleRows() int {
	v := s.height - 4
	if v < 3 {
		v = 3
	}
	return v
}

func (s *MongoCmdScreen) rowOfSelected() int {
	for i, r := range s.rows {
		if !r.isHeader && r.cmdIdx == s.selected {
			return i
		}
	}
	return 0
}

func (s *MongoCmdScreen) clampScroll() {
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

func (s *MongoCmdScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.MongoReadyMsg:
		s.loading = false
		s.ready = true
		s.user = msg.User
		s.password = msg.Password
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
			mc := s.cmds[s.selected]
			if mc.js == mongoDownloadArchive {
				user, pass := s.user, s.password
				return s, func() tea.Msg {
					return msgs.PushMongoDumpMsg{User: user, Password: pass}
				}
			}
			hostCmd := docker.MongoEvalCmd(s.containerID, s.mongoBin, s.user, s.password, "", mc.js)
			title := "mongo: " + mc.label
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

func (s *MongoCmdScreen) View() string {
	title := styles.TitleBar.Render("MongoDB — " + s.containerName)

	if s.loading {
		body := lipgloss.NewStyle().Padding(1, 2).Render(
			s.sp.View() + " detecting MongoDB credentials...",
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
