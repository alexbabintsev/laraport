package screens

import (
	"fmt"
	"sort"

	"github.com/alexbabintsev/laradok/internal/config"
	"github.com/alexbabintsev/laradok/internal/docker"
	"github.com/alexbabintsev/laradok/internal/msgs"
	"github.com/alexbabintsev/laradok/internal/tui/styles"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type containerItem struct {
	container   docker.Container
	displayName string
	favorite    bool
}

func (c containerItem) Title() string {
	if c.favorite {
		return lipgloss.NewStyle().Foreground(styles.ColorWarning).Render("* " + c.displayName)
	}
	return c.displayName
}
func (c containerItem) Description() string {
	return fmt.Sprintf("%s", c.container.Image)
	// return fmt.Sprintf("%s • %s", c.container.Image, c.container.ID[:12])
}
func (c containerItem) FilterValue() string { return c.displayName }

// ContainerListScreen loads and shows running containers for a server.
type ContainerListScreen struct {
	server           config.Server
	runner           docker.Runner
	containerConfigs []config.ContainerConfig
	list             list.Model
	spinner          spinner.Model
	loading          bool
	connecting       bool // true while SSH handshake is in progress
	err              error
	width            int
	height           int
}

func NewContainerListScreen(server config.Server, runner docker.Runner, width, height int) *ContainerListScreen {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styles.SpinnerStyle

	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(lipgloss.Color("#FFFFFF")).
		BorderLeftForeground(styles.ColorPrimary)
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(styles.ColorAccent).
		BorderLeftForeground(styles.ColorPrimary)

	l := list.New([]list.Item{}, delegate, width, height-6)
	l.Title = fmt.Sprintf("Containers - %s", server.Name)
	l.Styles.Title = styles.TitleBar
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.SetShowHelp(false)

	connecting := runner == nil
	return &ContainerListScreen{
		server:           server,
		runner:           runner,
		containerConfigs: server.Containers,
		list:             l,
		spinner:          sp,
		loading:          !connecting,
		connecting:       connecting,
		width:            width,
		height:           height,
	}
}

func (s *ContainerListScreen) Init() tea.Cmd {
	if s.connecting {
		// runner not yet available — just spin, wait for ServerConnectedMsg
		return s.spinner.Tick
	}
	return tea.Batch(s.spinner.Tick, s.loadContainers())
}

func (s *ContainerListScreen) loadContainers() tea.Cmd {
	return func() tea.Msg {
		containers, err := docker.ListContainers(s.runner)
		return msgs.ContainersLoadedMsg{Containers: containers, Err: err}
	}
}

func (s *ContainerListScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.ServerConnectedMsg:
		if msg.Err != nil {
			s.connecting = false
			s.loading = false
			s.err = msg.Err
			return s, nil
		}
		s.connecting = false
		s.loading = true
		s.runner = msg.Runner
		return s, tea.Batch(s.spinner.Tick, s.loadContainers())

	case msgs.ContainersLoadedMsg:
		s.loading = false
		if msg.Err != nil {
			s.err = msg.Err
			return s, nil
		}
		// Build items, applying hidden/favorite/displayName via glob-aware lookup
		var items []containerItem
		for _, c := range msg.Containers {
			cc, found := s.server.FindContainerConfig(c.Name)
			if found && cc.Hidden {
				continue
			}
			displayName := c.Name
			if found && cc.DisplayName != "" {
				displayName = cc.DisplayName
			}
			items = append(items, containerItem{
				container:   c,
				displayName: displayName,
				favorite:    found && cc.Favorite,
			})
		}
		// Sort: favorites first, then alphabetical
		sort.Slice(items, func(i, j int) bool {
			if items[i].favorite != items[j].favorite {
				return items[i].favorite
			}
			return items[i].displayName < items[j].displayName
		})
		listItems := make([]list.Item, len(items))
		for i, it := range items {
			listItems[i] = it
		}
		cmd := s.list.SetItems(listItems)
		return s, cmd

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return s, tea.Quit
		case "esc":
			return s, func() tea.Msg { return msgs.PopMsg{} }
		case "enter":
			if !s.loading && s.err == nil {
				if item, ok := s.list.SelectedItem().(containerItem); ok {
					return s, func() tea.Msg {
						return msgs.PushMainMenuMsg{Container: item.container}
					}
				}
			}
		case "r":
			if s.runner == nil {
				return s, nil // still connecting, ignore refresh
			}
			s.loading = true
			s.err = nil
			return s, tea.Batch(s.spinner.Tick, s.loadContainers())
		}

	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
		s.list.SetSize(msg.Width, msg.Height-6)

	case spinner.TickMsg:
		if s.loading || s.connecting {
			var cmd tea.Cmd
			s.spinner, cmd = s.spinner.Update(msg)
			return s, cmd
		}
	}

	if !s.loading && !s.connecting {
		var cmd tea.Cmd
		s.list, cmd = s.list.Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *ContainerListScreen) View() string {
	if s.connecting || s.loading {
		label := "Loading containers..."
		if s.connecting {
			label = fmt.Sprintf("Connecting to %s...", s.server.Host)
		}
		body := lipgloss.Place(s.width, s.height-4,
			lipgloss.Center, lipgloss.Center,
			styles.SpinnerStyle.Render(s.spinner.View()+" "+label),
		)
		content := lipgloss.JoinVertical(lipgloss.Left,
			styles.TitleBar.Render(fmt.Sprintf("Containers - %s", s.server.Name)),
			body,
		)
		return styles.PinToBottom(s.height, content, s.helpBar())
	}
	if s.err != nil {
		body := lipgloss.Place(s.width, s.height-4,
			lipgloss.Center, lipgloss.Center,
			styles.ErrorStyle.Render("Error: "+s.err.Error()),
		)
		content := lipgloss.JoinVertical(lipgloss.Left,
			styles.TitleBar.Render(fmt.Sprintf("Containers - %s", s.server.Name)),
			body,
		)
		return styles.PinToBottom(s.height, content, s.helpBar())
	}
	return styles.PinToBottom(s.height, s.list.View(), s.helpBar())
}

func (s *ContainerListScreen) helpBar() string {
	return styles.StatusBar.Width(s.width).Render(
		styles.StatusBarKey.Render("↑↓") + " navigate  " +
			styles.StatusBarKey.Render("enter") + " select  " +
			styles.StatusBarKey.Render("r") + " refresh  " +
			styles.StatusBarKey.Render("esc") + " back",
	)
}
