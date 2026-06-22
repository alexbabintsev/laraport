package screens

import (
	"fmt"

	"github.com/alexbabintsev/laradok/internal/config"
	"github.com/alexbabintsev/laradok/internal/docker"
	"github.com/alexbabintsev/laradok/internal/msgs"
	"github.com/alexbabintsev/laradok/internal/tui/styles"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type menuItem struct {
	title string
	desc  string
}

func (m menuItem) Title() string       { return m.title }
func (m menuItem) Description() string { return m.desc }
func (m menuItem) FilterValue() string { return m.title }

// MainMenuScreen shows main options for the selected container.
type MainMenuScreen struct {
	container       docker.Container
	containerCfg    config.ContainerConfig
	containerGroups []config.CommandGroup
	list            list.Model
	sp              spinner.Model
	loading         bool
	dbEngine        docker.DBEngine
	mongoBin        string
	width           int
	height          int
}

func NewMainMenuScreen(container docker.Container, ccfg config.ContainerConfig, width, height int) *MainMenuScreen {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styles.SpinnerStyle

	return &MainMenuScreen{
		container:       container,
		containerCfg:    ccfg,
		containerGroups: ccfg.Commands,
		sp:              sp,
		loading:         true,
		width:           width,
		height:          height,
	}
}

func buildMenuList(container docker.Container, ccfg config.ContainerConfig, caps docker.ContainerCaps, width, height int) list.Model {
	var items []list.Item

	// Always shown
	items = append(items, menuItem{"Info", "Image, status, network, mounts, and labels"})
	items = append(items, menuItem{"Stats", "Live CPU, memory, network, disk graphs and top processes"})
	if len(ccfg.Commands) > 0 {
		items = append(items, menuItem{"Commands", "Browse command groups from config"})
	}

	// Laravel-specific
	if caps.HasLaravel {
		items = append(items, menuItem{"Artisan Commands", "Run any artisan command with autocomplete"})
	}
	if caps.HasComposer || caps.HasPHP {
		items = append(items, menuItem{"Composer Commands", "Browse and run composer commands"})
	}
	if caps.HasNpm {
		items = append(items, menuItem{"Npm Commands", "Browse and run npm scripts"})
	}

	// Always shown
	items = append(items,
		menuItem{"Docker Commands", "Inspect, restart, stats and other docker commands"},
		menuItem{"Custom Command", "Run any shell command with interactive input"},
	)

	// Laravel-specific logs
	if caps.HasLaravel {
		items = append(items, menuItem{"Laravel Logs", "Browse and tail log files in storage/logs"})
	}

	// Always shown
	items = append(items,
		menuItem{"Docker Logs", "Stream Docker container stdout/stderr"},
		menuItem{"Server Logs", "Tail nginx, php, supervisor and other service logs"},
	)

	// Database (PostgreSQL, MySQL family, or SQLite)
	if caps.HasDatabase() {
		items = append(items, menuItem{"Database", "Browse databases, run SQL queries, download dumps"})
	}

	// Redis
	if caps.HasRedis {
		items = append(items, menuItem{"Redis", "Inspect Redis: INFO, keys, config, slowlog"})
	}

	// MongoDB
	if caps.HasMongo() {
		items = append(items, menuItem{"MongoDB", "Inspect MongoDB: stats, collections, indexes"})
	}

	// Laravel storage download
	if caps.HasLaravel {
		items = append(items, menuItem{"Download Storage", "Archive and download storage/ to ~/Downloads/"})
	}

	// File browser — available on any container
	items = append(items, menuItem{"File Browser", "Browse the container filesystem, view sizes, download files or folders"})

	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(lipgloss.Color("#FFFFFF")).
		BorderLeftForeground(styles.ColorPrimary)
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(styles.ColorAccent).
		BorderLeftForeground(styles.ColorPrimary)

	l := list.New(items, delegate, width, height-6)
	title := container.Name
	if ccfg.DisplayName != "" {
		title = ccfg.DisplayName
	}
	l.Title = title
	l.Styles.Title = styles.TitleBar
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.SetShowHelp(false)
	return l
}

func (s *MainMenuScreen) Init() tea.Cmd {
	return s.sp.Tick
}

func (s *MainMenuScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.ContainerCapsLoadedMsg:
		s.loading = false
		s.dbEngine = msg.Caps.DBEngine()
		s.mongoBin = msg.Caps.MongoBin
		s.list = buildMenuList(s.container, s.containerCfg, msg.Caps, s.width, s.height)
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
		case "enter":
			if s.loading {
				return s, nil
			}
			if item, ok := s.list.SelectedItem().(menuItem); ok {
				switch item.title {
				case "Info":
					return s, func() tea.Msg { return msgs.PushInfoMsg{} }
				case "Stats":
					return s, func() tea.Msg { return msgs.PushStatsMsg{} }
				case "Commands":
					groups := s.containerGroups
					return s, func() tea.Msg { return msgs.PushCommandsMsg{ContainerGroups: groups} }
				case "Artisan Commands":
					return s, func() tea.Msg { return msgs.PushArtisanCmdMsg{} }
				case "Composer Commands":
					return s, func() tea.Msg { return msgs.PushComposerCmdMsg{} }
				case "Npm Commands":
					return s, func() tea.Msg { return msgs.PushNpmCmdMsg{} }
				case "Docker Commands":
					return s, func() tea.Msg { return msgs.PushDockerCmdMsg{} }
				case "Custom Command":
					return s, func() tea.Msg { return msgs.PushCustomCmdMsg{} }
				case "Laravel Logs":
					return s, func() tea.Msg { return msgs.PushLogFilePickerMsg{} }
				case "Docker Logs":
					return s, func() tea.Msg { return msgs.PushLogTailMsg{LogType: "docker", Title: "Docker Logs"} }
				case "Server Logs":
					return s, func() tea.Msg { return msgs.PushServerLogPickerMsg{} }
				case "Database":
					engine := s.dbEngine
					return s, func() tea.Msg { return msgs.PushDBScreenMsg{Engine: engine} }
				case "Redis":
					return s, func() tea.Msg { return msgs.PushRedisCmdMsg{} }
				case "MongoDB":
					mongoBin := s.mongoBin
					return s, func() tea.Msg { return msgs.PushMongoCmdMsg{MongoBin: mongoBin} }
				case "Download Storage":
					return s, func() tea.Msg { return msgs.PushStorageDownloadMsg{} }
				case "File Browser":
					return s, func() tea.Msg { return msgs.PushFileBrowserMsg{} }
				}
			}
		}
	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
		if !s.loading {
			s.list.SetSize(msg.Width, msg.Height-6)
		}
	}

	if s.loading {
		return s, nil
	}
	var cmd tea.Cmd
	s.list, cmd = s.list.Update(msg)
	return s, cmd
}

func (s *MainMenuScreen) View() string {
	title := styles.TitleBar.Render(func() string {
		if s.containerCfg.DisplayName != "" {
			return s.containerCfg.DisplayName
		}
		return s.container.Name
	}())

	help := styles.StatusBar.Width(s.width).Render(
		fmt.Sprintf("%s %s  ",
			styles.DimStyle.Render("container:"),
			styles.BreadcrumbActive.Render(s.container.Name),
		)+
			styles.StatusBarKey.Render("↑↓")+" navigate  "+
			styles.StatusBarKey.Render("enter")+" select  "+
			styles.StatusBarKey.Render("esc")+" back",
	)

	if s.loading {
		body := lipgloss.NewStyle().Padding(1, 2).
			Foreground(styles.ColorMuted).
			Render(s.sp.View() + " detecting container capabilities...")
		content := lipgloss.JoinVertical(lipgloss.Left, title, body)
		return styles.PinToBottom(s.height, content, help)
	}

	return styles.PinToBottom(s.height, s.list.View(), help)
}
