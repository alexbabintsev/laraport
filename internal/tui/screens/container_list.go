package screens

import (
	"fmt"
	"sort"
	"strings"

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
	stat        docker.ContainerStat
	hasStat     bool
}

func (c containerItem) Title() string {
	name := c.displayName
	if c.favorite {
		name = "* " + name
	}
	// Dim and tag containers that are not running so they stand out as inactive.
	if c.container.State != "running" && c.container.State != "" {
		return lipgloss.NewStyle().Foreground(styles.ColorMuted).
			Render(name + " (" + c.container.State + ")")
	}
	if c.favorite {
		return lipgloss.NewStyle().Foreground(styles.ColorWarning).Render(name)
	}
	return name
}

func (c containerItem) Description() string {
	parts := []string{}
	if s := strings.TrimSpace(c.container.Status); s != "" {
		parts = append(parts, s)
	}
	if c.hasStat {
		if cpu := strings.TrimSpace(c.stat.CPUPerc); cpu != "" {
			parts = append(parts, "CPU "+cpu)
		}
		if mem := strings.TrimSpace(c.stat.MemUsage); mem != "" {
			parts = append(parts, "Mem "+mem)
		}
	}
	if p := shortPorts(c.container.Ports); p != "" {
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return c.container.Image
	}
	return strings.Join(parts, " · ")
}

func (c containerItem) FilterValue() string { return c.displayName }

// shortPorts condenses a docker ports string to just published host ports,
// e.g. "0.0.0.0:8080->80/tcp, :::8080->80/tcp" → ":8080->80".
func shortPorts(ports string) string {
	ports = strings.TrimSpace(ports)
	if ports == "" {
		return ""
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range strings.Split(ports, ",") {
		p = strings.TrimSpace(p)
		arrow := strings.Index(p, "->")
		if arrow < 0 {
			continue // unpublished port, skip
		}
		hostPart := p[:arrow]
		// keep only the :port suffix of the host side
		if colon := strings.LastIndex(hostPart, ":"); colon >= 0 {
			hostPart = hostPart[colon:]
		}
		target := strings.TrimSuffix(p[arrow+2:], "/tcp")
		mapping := hostPart + "->" + target
		if !seen[mapping] {
			seen[mapping] = true
			out = append(out, mapping)
		}
	}
	return strings.Join(out, " ")
}

// ContainerListScreen loads and shows running containers for a server.
type ContainerListScreen struct {
	server           config.Server
	runner           docker.Runner
	containerConfigs []config.ContainerConfig
	containers       []docker.Container
	stats            map[string]docker.ContainerStat
	list             list.Model
	spinner          spinner.Model
	loading          bool
	connecting       bool // true while SSH handshake is in progress
	showStopped      bool // include stopped containers (docker ps -a)
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

	l := list.New([]list.Item{}, delegate, width, max(height-6, 1))
	l.Title = fmt.Sprintf("Containers - %s", server.Name)
	l.Styles.Title = styles.TitleBar
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.SetShowHelp(false)

	connecting := runner == nil
	return &ContainerListScreen{
		showStopped:      true,
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

// UpdateServer replaces the cached server config (after an edit) and rebuilds
// the list so new display names / favorites take effect immediately.
func (s *ContainerListScreen) UpdateServer(server config.Server) tea.Cmd {
	s.server = server
	s.containerConfigs = server.Containers
	return s.rebuildItems()
}

func (s *ContainerListScreen) Init() tea.Cmd {
	if s.connecting {
		// runner not yet available — just spin, wait for ServerConnectedMsg
		return s.spinner.Tick
	}
	return tea.Batch(s.spinner.Tick, s.loadContainers())
}

// SetShowStopped selects whether stopped containers are listed.
func (s *ContainerListScreen) SetShowStopped(show bool) { s.showStopped = show }

func (s *ContainerListScreen) loadContainers() tea.Cmd {
	runner, all := s.runner, s.showStopped
	return func() tea.Msg {
		containers, err := docker.ListContainers(runner, all)
		return msgs.ContainersLoadedMsg{Containers: containers, Err: err}
	}
}

// loadStats fetches a one-shot resource snapshot for all containers. It runs
// after the list is already shown because docker stats is comparatively slow.
func (s *ContainerListScreen) loadStats() tea.Cmd {
	runner := s.runner
	return func() tea.Msg {
		stats, err := docker.ListContainerStats(runner)
		return msgs.ContainerStatsLoadedMsg{Stats: stats, Err: err}
	}
}

// rebuildItems constructs the list items from the stored containers, applying
// config (hidden/favorite/displayName) and any loaded stats.
func (s *ContainerListScreen) rebuildItems() tea.Cmd {
	var items []containerItem
	for _, c := range s.containers {
		cc, found := s.server.FindContainerConfig(c.Name)
		if found && cc.Hidden {
			continue
		}
		displayName := c.Name
		if found && cc.DisplayName != "" {
			displayName = cc.DisplayName
		}
		st, hasStat := docker.LookupStat(s.stats, c.Name, c.ID)
		items = append(items, containerItem{
			container:   c,
			displayName: displayName,
			favorite:    found && cc.Favorite,
			stat:        st,
			hasStat:     hasStat,
		})
	}
	// Sort: running first, then favorites, then alphabetical. This keeps
	// stopped containers grouped at the bottom of the list.
	sort.Slice(items, func(i, j int) bool {
		ri := items[i].container.State == "running"
		rj := items[j].container.State == "running"
		if ri != rj {
			return ri
		}
		if items[i].favorite != items[j].favorite {
			return items[i].favorite
		}
		return items[i].displayName < items[j].displayName
	})
	listItems := make([]list.Item, len(items))
	for i, it := range items {
		listItems[i] = it
	}
	return s.list.SetItems(listItems)
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
		s.containers = msg.Containers
		cmd := s.rebuildItems()
		// Lazily fetch live CPU/Mem stats now that the list is visible.
		return s, tea.Batch(cmd, s.loadStats())

	case msgs.ContainerStatsLoadedMsg:
		if msg.Err == nil {
			s.stats = msg.Stats
			return s, s.rebuildItems()
		}
		return s, nil

	case tea.KeyMsg:
		// While the filter input is active, let the list consume keystrokes so
		// letters like "r"/"g" type into the filter instead of triggering shortcuts.
		if s.list.FilterState() == list.Filtering {
			break
		}
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
		case "g":
			if s.runner == nil || s.loading {
				return s, nil // not connected yet
			}
			return s, func() tea.Msg { return msgs.PushGlobalCmdMsg{} }
		case "e":
			if s.loading || s.err != nil {
				return s, nil
			}
			if item, ok := s.list.SelectedItem().(containerItem); ok {
				name := item.container.Name
				cc, _ := s.server.FindContainerConfig(name)
				cc.Name = name // edits target an exact-name entry
				return s, func() tea.Msg {
					return msgs.PushContainerEditMsg{ContainerName: name, Config: cc}
				}
			}
		}

	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
		s.list.SetSize(msg.Width, max(msg.Height-6, 1))

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
			styles.StatusBarKey.Render("e") + " configure  " +
			styles.StatusBarKey.Render("g") + " cleanup  " +
			styles.StatusBarKey.Render("r") + " refresh  " +
			styles.StatusBarKey.Render("esc") + " back",
	)
}
