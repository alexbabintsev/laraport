package screens

import (
	"strconv"

	"github.com/alexbabintsev/laradok/internal/config"
	"github.com/alexbabintsev/laradok/internal/msgs"
	"github.com/alexbabintsev/laradok/internal/tui/styles"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// SettingsScreen edits application-wide settings.
type SettingsScreen struct {
	form   form
	status string
	isErr  bool
	width  int
	height int
}

// NewSettingsScreen opens the settings form with the current values.
func NewSettingsScreen(st config.Settings, width, height int) *SettingsScreen {
	s := &SettingsScreen{width: width, height: height}
	s.form.width = width
	hostKey := st.HostKeyCheck
	if hostKey == "" {
		hostKey = config.HostKeyAcceptNew
	}
	intText := func(v int) string {
		if v == 0 {
			return ""
		}
		return strconv.Itoa(v)
	}

	s.form.text("downloads_dir", "Downloads folder", st.DownloadsDir, "~/Downloads",
		"where database dumps and archives are saved")
	s.form.choose("host_key_check", "SSH host keys", []string{config.HostKeyAcceptNew, config.HostKeyStrict}, hostKey,
		"accept-new: trust a new server on first connect · strict: only servers already in ~/.ssh/known_hosts")
	s.form.toggle("wrap_logs", "Wrap long lines", st.WrapLogs, "start log and output screens with wrapping on (f2 toggles)")
	s.form.toggle("show_stopped", "Show stopped", !st.HideStopped, "list stopped containers too")
	s.form.text("stats_interval", "Stats refresh (s)", intText(st.StatsInterval), strconv.Itoa(config.DefaultStatsInterval),
		"seconds between Stats screen samples (1–60)")
	s.form.toggle("sql_history", "Save SQL history", !st.NoSQLHistory,
		"remember queries per database in ~/.config/laradok/sql_history.json")
	s.form.text("sql_history_size", "SQL history size", intText(st.SQLHistorySize), strconv.Itoa(config.DefaultSQLHistorySize),
		"queries kept per database").visible = func(f *form) bool { return f.isOn("sql_history") }
	s.form.button("clear_history", "Clear SQL history", "delete all saved queries now",
		func() tea.Msg { return msgs.ClearSQLHistoryMsg{} })
	s.form.start()
	return s
}

func (s *SettingsScreen) Init() tea.Cmd { return textinput.Blink }

// settings builds config.Settings from the form.
func (s *SettingsScreen) settings() (config.Settings, bool) {
	st := config.Settings{
		DownloadsDir: s.form.str("downloads_dir"),
		HostKeyCheck: s.form.selected("host_key_check"),
		WrapLogs:     s.form.isOn("wrap_logs"),
		HideStopped:  !s.form.isOn("show_stopped"),
		NoSQLHistory: !s.form.isOn("sql_history"),
	}
	if st.HostKeyCheck == config.HostKeyAcceptNew {
		st.HostKeyCheck = "" // the default stays implicit in the file
	}
	for _, f := range []struct {
		key string
		dst *int
		max int
	}{{"stats_interval", &st.StatsInterval, 60}, {"sql_history_size", &st.SQLHistorySize, 10000}} {
		v := s.form.str(f.key)
		if v == "" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > f.max {
			s.status, s.isErr = s.form.field(f.key).label+" must be a number between 1 and "+strconv.Itoa(f.max), true
			return st, false
		}
		*f.dst = n
	}
	if err := st.Validate(); err != nil {
		s.status, s.isErr = err.Error(), true
		return st, false
	}
	return st, true
}

func (s *SettingsScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.SettingsSavedMsg:
		if msg.Err != nil {
			s.status, s.isErr = msg.Err.Error(), true
		} else {
			s.status, s.isErr = "Saved.", false
		}
		return s, nil

	case msgs.SQLHistoryClearedMsg:
		if msg.Err != nil {
			s.status, s.isErr = "Could not clear SQL history: "+msg.Err.Error(), true
		} else {
			s.status, s.isErr = "SQL history cleared.", false
		}
		return s, nil

	case tea.WindowSizeMsg:
		s.width, s.height = msg.Width, msg.Height
		s.form.resize(msg.Width)
		return s, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return s, tea.Quit
		case "esc":
			return s, func() tea.Msg { return msgs.PopMsg{} }
		case "ctrl+s":
			return s, s.save()
		case "enter":
			if s.form.fields[s.form.focus].kind == formText {
				return s, s.save()
			}
		}
		s.status = ""
	}
	cmd, _ := s.form.update(msg)
	return s, cmd
}

func (s *SettingsScreen) save() tea.Cmd {
	st, ok := s.settings()
	if !ok {
		return nil
	}
	return func() tea.Msg { return msgs.SaveSettingsMsg{Settings: st} }
}

func (s *SettingsScreen) View() string {
	title := styles.TitleBar.Render("Settings")
	body := lipgloss.NewStyle().Padding(1, 2).Render(s.form.view())
	help := styles.StatusBar.Width(s.width).Render(
		styles.StatusBarKey.Render("↑↓/tab") + " move  " +
			styles.StatusBarKey.Render("space") + " toggle  " +
			styles.StatusBarKey.Render("ctrl+s") + " save  " +
			styles.StatusBarKey.Render("esc") + " back",
	)
	content := lipgloss.JoinVertical(lipgloss.Left, title, "", body,
		lipgloss.NewStyle().Width(max(s.width-4, 1)).Render(statusLine(s.status, s.isErr)))
	return styles.PinToBottom(s.height, content, help)
}
