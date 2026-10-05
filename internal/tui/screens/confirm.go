package screens

import (
	"github.com/alexbabintsev/laraport/internal/msgs"
	"github.com/alexbabintsev/laraport/internal/tui/styles"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ConfirmScreen asks the user to confirm a destructive action before it runs.
type ConfirmScreen struct {
	title  string
	detail string
	run    msgs.PushOutputMsg
	then   tea.Msg // dispatched instead of run when set
	width  int
	height int
}

func NewConfirmScreen(title, detail string, run msgs.PushOutputMsg, width, height int) *ConfirmScreen {
	return &ConfirmScreen{
		title:  title,
		detail: detail,
		run:    run,
		width:  width,
		height: height,
	}
}

// NewConfirmActionScreen asks to confirm an action other than running a
// command; then is dispatched on "y".
func NewConfirmActionScreen(title, detail string, then tea.Msg, width, height int) *ConfirmScreen {
	return &ConfirmScreen{title: title, detail: detail, then: then, width: width, height: height}
}

func (s *ConfirmScreen) Init() tea.Cmd { return nil }

func (s *ConfirmScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return s, tea.Quit
		case "y", "Y", "enter":
			run, then := s.run, s.then
			return s, func() tea.Msg { return msgs.ConfirmedMsg{Run: run, Then: then} }
		case "n", "N", "esc":
			return s, func() tea.Msg { return msgs.PopMsg{} }
		}
	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
	}
	return s, nil
}

func (s *ConfirmScreen) View() string {
	title := styles.TitleBar.Render("Confirm")

	warn := lipgloss.NewStyle().Foreground(styles.ColorDanger).Bold(true).
		Render("⚠ This action cannot be undone.")
	prompt := lipgloss.NewStyle().Foreground(styles.ColorText).
		Render(s.title)
	detail := s.detail
	if s.then == nil {
		detail = "$ " + detail // a command
	}
	cmdBox := lipgloss.NewStyle().
		Foreground(styles.ColorAccent).
		Padding(0, 1).
		Render(detail)

	body := lipgloss.NewStyle().Padding(1, 2).Render(
		lipgloss.JoinVertical(lipgloss.Left,
			prompt,
			"",
			cmdBox,
			"",
			warn,
		),
	)

	help := styles.StatusBar.Width(s.width).Render(
		styles.StatusBarKey.Render("y") + " confirm  " +
			styles.StatusBarKey.Render("n") + " cancel",
	)

	content := lipgloss.JoinVertical(lipgloss.Left, title, "", body)
	return styles.PinToBottom(s.height, content, help)
}
