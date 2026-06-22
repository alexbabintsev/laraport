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

// DBListScreen shows discovered databases for the active database container.
type DBListScreen struct {
	databases  []string
	user       string
	password   string
	engine     docker.DBEngine
	historyKey string // prefix "serverName/containerName" — dbName appended on select
	selected   int
	scrollOff  int
	loading    bool
	errMsg     string
	sp         spinner.Model
	width      int
	height     int
}

func NewDBListScreen(historyKeyPrefix string, engine docker.DBEngine, width, height int) *DBListScreen {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styles.SpinnerStyle
	return &DBListScreen{
		historyKey: historyKeyPrefix,
		engine:     engine,
		loading:    true,
		sp:         sp,
		width:      width,
		height:     height,
	}
}

func (s *DBListScreen) Init() tea.Cmd {
	return s.sp.Tick
}

func (s *DBListScreen) visibleRows() int {
	v := s.height - 5
	if v < 3 {
		v = 3
	}
	return v
}

func (s *DBListScreen) clampScroll() {
	vis := s.visibleRows()
	if s.selected < s.scrollOff {
		s.scrollOff = s.selected
	}
	if s.selected >= s.scrollOff+vis {
		s.scrollOff = s.selected - vis + 1
	}
	if s.scrollOff < 0 {
		s.scrollOff = 0
	}
}

func (s *DBListScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.DBCredsLoadedMsg:
		if msg.Err != nil {
			s.loading = false
			s.errMsg = "credentials error: " + msg.Err.Error()
			return s, nil
		}
		s.user = msg.User
		s.password = msg.Password
		// creds loaded — caller (App) will now fire loadDBListCmd
		return s, nil

	case msgs.DBListLoadedMsg:
		s.loading = false
		if msg.Err != nil {
			s.errMsg = msg.Err.Error()
			return s, nil
		}
		s.databases = msg.Databases
		if len(s.databases) == 0 {
			if s.engine == docker.EngineSQLite {
				s.errMsg = "no .sqlite/.db files found under the app root"
			} else {
				s.errMsg = "no databases found"
			}
		}
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
			if s.selected < len(s.databases)-1 {
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
			if s.selected >= len(s.databases) {
				s.selected = len(s.databases) - 1
			}
			s.clampScroll()
		case "pgup":
			s.selected -= s.visibleRows()
			if s.selected < 0 {
				s.selected = 0
			}
			s.clampScroll()
		case "enter":
			if s.selected >= 0 && s.selected < len(s.databases) {
				db := s.databases[s.selected]
				user := s.user
				pass := s.password
				hk := strings.TrimRight(s.historyKey, "/") + "/" + db
				return s, func() tea.Msg {
					return msgs.PushDBActionsMsg{DBName: db, User: user, Password: pass, Engine: s.engine, HistoryKey: hk}
				}
			}
		}

	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
	}
	return s, nil
}

func (s *DBListScreen) View() string {
	title := styles.TitleBar.Render(s.engine.Label() + " Databases")

	loadingMsg := " detecting credentials and listing databases..."
	if s.engine == docker.EngineSQLite {
		loadingMsg = " searching for SQLite database files..."
	}

	var body string
	if s.loading {
		body = lipgloss.NewStyle().Padding(1, 2).Render(
			s.sp.View() + loadingMsg,
		)
	} else if s.errMsg != "" {
		body = lipgloss.NewStyle().Padding(1, 2).
			Foreground(styles.ColorDanger).Render("Error: " + s.errMsg)
	} else {
		nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#2ECC71"))
		selStyle := lipgloss.NewStyle().Background(lipgloss.Color("#1E3A5F")).Bold(true)

		vis := s.visibleRows()
		end := s.scrollOff + vis
		if end > len(s.databases) {
			end = len(s.databases)
		}

		var lines []string
		for i, db := range s.databases[s.scrollOff:end] {
			idx := s.scrollOff + i
			if idx == s.selected {
				lines = append(lines, selStyle.Width(s.width-4).Render("  "+db))
			} else {
				lines = append(lines, "  "+nameStyle.Render(db))
			}
		}
		if len(s.databases) > vis {
			lines = append(lines, lipgloss.NewStyle().
				Foreground(styles.ColorMuted).Padding(0, 2).
				Render(fmt.Sprintf("-- %d of %d --", s.selected+1, len(s.databases))))
		}
		body = strings.Join(lines, "\n")
	}

	var helpStr string
	if s.loading || s.errMsg != "" {
		helpStr = styles.StatusBar.Width(s.width).Render(
			styles.StatusBarKey.Render("esc") + " back",
		)
	} else {
		helpStr = styles.StatusBar.Width(s.width).Render(
			styles.StatusBarKey.Render("↑↓")+" navigate  "+
				styles.StatusBarKey.Render("enter")+" select  "+
				styles.StatusBarKey.Render("esc")+" back",
		)
	}

	content := lipgloss.JoinVertical(lipgloss.Left, title, "", body)
	return styles.PinToBottom(s.height, content, helpStr)
}
