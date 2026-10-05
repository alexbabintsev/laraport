package screens

import (
	"fmt"
	"strings"

	"github.com/alexbabintsev/laradok/internal/docker"
	"github.com/alexbabintsev/laradok/internal/msgs"
	"github.com/alexbabintsev/laradok/internal/tui/styles"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const maxSQLHistory = 100

// SQLInputScreen lets the user type a SQL query with ↑↓ history navigation.
type SQLInputScreen struct {
	input      textinput.Model
	dbName     string
	user       string
	password   string
	engine     docker.DBEngine
	historyKey string
	history    []string // all queries, index 0 = oldest
	historyPos int      // -1 = new input; 0..len-1 = navigating history (0=oldest)
	draft      string   // saved draft when user navigates away from new input
	width      int
	height     int
}

func NewSQLInputScreen(dbName, user, password string, engine docker.DBEngine, historyKey string, history []string, width, height int) *SQLInputScreen {
	ti := textinput.New()
	ti.Placeholder = "SELECT * FROM users LIMIT 10;"
	ti.Focus()
	ti.CharLimit = 2048
	ti.Width = max(width-6, 1)
	ti.PromptStyle = lipgloss.NewStyle().Foreground(styles.ColorPrimary)
	ti.TextStyle = lipgloss.NewStyle().Foreground(styles.ColorText)

	h := make([]string, len(history))
	copy(h, history)

	return &SQLInputScreen{
		input:      ti,
		dbName:     dbName,
		user:       user,
		password:   password,
		engine:     engine,
		historyKey: historyKey,
		history:    h,
		historyPos: -1,
		width:      width,
		height:     height,
	}
}

func (s *SQLInputScreen) Init() tea.Cmd {
	return textinput.Blink
}

func (s *SQLInputScreen) historyUp() {
	if len(s.history) == 0 {
		return
	}
	if s.historyPos == -1 {
		// Save current draft before leaving new-input position
		s.draft = s.input.Value()
		s.historyPos = len(s.history) - 1
	} else if s.historyPos > 0 {
		s.historyPos--
	}
	s.input.SetValue(s.history[s.historyPos])
	s.input.CursorEnd()
}

func (s *SQLInputScreen) historyDown() {
	if s.historyPos == -1 {
		return
	}
	if s.historyPos < len(s.history)-1 {
		s.historyPos++
		s.input.SetValue(s.history[s.historyPos])
	} else {
		// Back to new input
		s.historyPos = -1
		s.input.SetValue(s.draft)
	}
	s.input.CursorEnd()
}

func (s *SQLInputScreen) addToHistory(sql string) {
	// Deduplicate: remove existing entry if same query
	for i, q := range s.history {
		if q == sql {
			s.history = append(s.history[:i], s.history[i+1:]...)
			break
		}
	}
	s.history = append(s.history, sql)
	if len(s.history) > maxSQLHistory {
		s.history = s.history[len(s.history)-maxSQLHistory:]
	}
}

func (s *SQLInputScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return s, tea.Quit
		case "esc":
			return s, func() tea.Msg { return msgs.PopMsg{} }
		case "up":
			s.historyUp()
			return s, nil
		case "down":
			s.historyDown()
			return s, nil
		case "enter":
			sql := strings.TrimSpace(s.input.Value())
			if sql == "" {
				return s, nil
			}
			s.addToHistory(sql)
			history := make([]string, len(s.history))
			copy(history, s.history)
			dbName := s.dbName
			user := s.user
			pass := s.password
			title := "SQL — " + dbName
			// Reset to new input after execution
			s.historyPos = -1
			s.draft = ""
			s.input.SetValue("")
			historyKey := s.historyKey
			return s, func() tea.Msg {
				return msgs.PushSQLExecMsg{
					Title:      title,
					DBName:     dbName,
					User:       user,
					Password:   pass,
					Engine:     s.engine,
					SQL:        sql,
					HistoryKey: historyKey,
					History:    history,
				}
			}
		}
	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
		s.input.Width = max(msg.Width-6, 1)
	}

	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	return s, cmd
}

func (s *SQLInputScreen) View() string {
	title := styles.TitleBar.Render("SQL Query — " + s.dbName)

	label := lipgloss.NewStyle().Foreground(styles.ColorTextDim).Bold(true).Render("SQL>")
	inputArea := lipgloss.NewStyle().Padding(1, 2).Render(label + "\n" + s.input.View())

	// History indicator
	var histLine string
	if len(s.history) > 0 {
		if s.historyPos >= 0 {
			histLine = lipgloss.NewStyle().Padding(0, 2).
				Foreground(styles.ColorAccent).
				Render(fmt.Sprintf("history %d/%d", s.historyPos+1, len(s.history)))
		} else {
			histLine = lipgloss.NewStyle().Padding(0, 2).
				Foreground(styles.ColorMuted).
				Render(fmt.Sprintf("%d queries in history  ↑ to browse", len(s.history)))
		}
	}

	helpParts := styles.StatusBarKey.Render("enter") + " run  " +
		styles.StatusBarKey.Render("↑↓") + " history  " +
		styles.StatusBarKey.Render("esc") + " back"
	help := styles.StatusBar.Width(s.width).Render(helpParts)

	parts := []string{title, inputArea}
	if histLine != "" {
		parts = append(parts, histLine)
	}
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
	return styles.PinToBottom(s.height, content, help)
}
