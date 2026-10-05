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
	desc := "Docker socket (local)"
	if s.server.Type == config.ServerTypeSSH {
		desc = fmt.Sprintf("%s@%s:%d", s.server.User, s.server.Host, s.server.Port)
	}
	if s.server.DockerCmd != "" {
		desc += "  ·  " + s.server.DockerCmd
	}
	return desc
}

func (s serverItem) FilterValue() string { return s.server.Name }

// ServerListScreen shows the configured servers and manages them: add,
// edit, delete, plus the settings screen.
type ServerListScreen struct {
	list   list.Model
	status string
	isErr  bool
	width  int
	height int
}

func serverItems(servers []config.Server) []list.Item {
	items := make([]list.Item, len(servers))
	for i, s := range servers {
		items[i] = serverItem{server: s}
	}
	return items
}

func NewServerListScreen(servers []config.Server, width, height int) *ServerListScreen {
	items := serverItems(servers)

	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(lipgloss.Color("#FFFFFF")).
		BorderLeftForeground(styles.ColorPrimary)
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(styles.ColorAccent).
		BorderLeftForeground(styles.ColorPrimary)

	l := list.New(items, delegate, width, max(height-6, 1))
	l.Title = "Select Server"
	l.Styles.Title = styles.TitleBar
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.SetShowHelp(false)

	return &ServerListScreen{list: l, width: width, height: height}
}

func (s *ServerListScreen) Init() tea.Cmd { return nil }

// SetServers replaces the listed servers, keeping the selection by name.
func (s *ServerListScreen) SetServers(servers []config.Server, selectName string) tea.Cmd {
	cmd := s.list.SetItems(serverItems(servers))
	for i, srv := range servers {
		if srv.Name == selectName {
			s.list.Select(i)
		}
	}
	return cmd
}

func (s *ServerListScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.ServerListChangedMsg:
		if msg.Err != nil {
			s.status, s.isErr = msg.Err.Error(), true
			return s, nil
		}
		s.status, s.isErr = msg.Status, false
		sel := ""
		if item, ok := s.list.SelectedItem().(serverItem); ok {
			sel = item.server.Name
		}
		return s, s.SetServers(msg.Servers, sel)

	case tea.KeyMsg:
		s.status = ""
		item, hasItem := s.list.SelectedItem().(serverItem)
		switch msg.String() {
		case "q", "ctrl+c":
			return s, tea.Quit
		case "enter":
			if hasItem {
				return s, func() tea.Msg { return msgs.PushContainerListMsg{Server: item.server} }
			}
		case "a":
			return s, func() tea.Msg { return msgs.PushServerEditMsg{} }
		case "e":
			if hasItem {
				return s, func() tea.Msg {
					return msgs.PushServerEditMsg{Original: item.server.Name, Server: item.server}
				}
			}
		case "d", "delete":
			if hasItem && !item.server.IsImplicit() {
				detail := "Remove server " + item.server.Name + " from the config."
				if n := len(item.server.Containers); n > 0 {
					detail += fmt.Sprintf(" Its %d container setting(s) (names, favorites, commands) are removed too.", n)
				}
				name := item.server.Name
				return s, func() tea.Msg {
					return msgs.PushConfirmMsg{Title: "Delete server " + name + "?", Detail: detail, Then: msgs.DeleteServerMsg{Name: name}}
				}
			}
		case "s":
			return s, func() tea.Msg { return msgs.PushSettingsMsg{} }
		}
	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
		s.list.SetSize(msg.Width, max(msg.Height-6, 1))
	}
	var cmd tea.Cmd
	s.list, cmd = s.list.Update(msg)
	return s, cmd
}

func (s *ServerListScreen) View() string {
	help := styles.StatusBar.Width(s.width).Render(
		styles.StatusBarKey.Render("↑↓") + " navigate  " +
			styles.StatusBarKey.Render("enter") + " select  " +
			styles.StatusBarKey.Render("a") + " add  " +
			styles.StatusBarKey.Render("e") + " edit  " +
			styles.StatusBarKey.Render("d") + " delete  " +
			styles.StatusBarKey.Render("s") + " settings  " +
			styles.StatusBarKey.Render("q") + " quit",
	)
	content := s.list.View()
	if len(s.list.Items()) == 1 {
		if item, ok := s.list.Items()[0].(serverItem); ok && item.server.IsImplicit() {
			content = lipgloss.JoinVertical(lipgloss.Left, content,
				lipgloss.NewStyle().Foreground(styles.ColorMuted).Padding(0, 2).
					Render("No servers configured yet — press a to add a remote server."))
		}
	}
	if s.status != "" {
		content = lipgloss.JoinVertical(lipgloss.Left, content, statusLine(s.status, s.isErr))
	}
	return styles.PinToBottom(s.height, content, help)
}
