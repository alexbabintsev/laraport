package screens

import (
	"fmt"

	"github.com/alexbabintsev/laradok/internal/config"
	"github.com/alexbabintsev/laradok/internal/msgs"
	"github.com/alexbabintsev/laradok/internal/tui/styles"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// serverItem wraps config.Server for the bubbles/list.
type serverItem struct {
	server config.Server
}

func (s serverItem) Title() string {
	badge := ""
	if s.server.Type == config.ServerTypeSSH {
		badge = styles.ServerBadgeSSH.Render("SSH")
	} else {
		badge = styles.ServerBadgeLocal.Render("LOCAL")
	}
	return fmt.Sprintf("%s  %s", badge, s.server.Name)
}

func (s serverItem) Description() string {
	if s.server.Type == config.ServerTypeSSH {
		return fmt.Sprintf("%s@%s:%d", s.server.User, s.server.Host, s.server.Port)
	}
	return "Docker socket (local)"
}

func (s serverItem) FilterValue() string { return s.server.Name }

// ServerListScreen shows the list of configured servers.
type ServerListScreen struct {
	list   list.Model
	width  int
	height int
}

func NewServerListScreen(servers []config.Server, width, height int) *ServerListScreen {
	items := make([]list.Item, len(servers))
	for i, s := range servers {
		items[i] = serverItem{server: s}
	}

	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(lipgloss.Color("#FFFFFF")).
		BorderLeftForeground(styles.ColorPrimary)
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(styles.ColorAccent).
		BorderLeftForeground(styles.ColorPrimary)

	l := list.New(items, delegate, width, height-6)
	l.Title = "Select Server"
	l.Styles.Title = styles.TitleBar
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.SetShowHelp(false)

	return &ServerListScreen{list: l, width: width, height: height}
}

func (s *ServerListScreen) Init() tea.Cmd { return nil }

func (s *ServerListScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case msg.String() == "q", msg.String() == "ctrl+c":
			return s, tea.Quit
		case msg.String() == "enter":
			if item, ok := s.list.SelectedItem().(serverItem); ok {
				return s, func() tea.Msg {
					return msgs.PushContainerListMsg{Server: item.server}
				}
			}
		}
	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
		s.list.SetSize(msg.Width, msg.Height-6)
	}
	var cmd tea.Cmd
	s.list, cmd = s.list.Update(msg)
	return s, cmd
}

func (s *ServerListScreen) View() string {
	help := styles.StatusBar.Width(s.width).Render(
		styles.StatusBarKey.Render("↑↓") + " navigate  " +
			styles.StatusBarKey.Render("enter") + " select  " +
			styles.StatusBarKey.Render("q") + " quit",
	)
	return styles.PinToBottom(s.height, s.list.View(), help)
}
