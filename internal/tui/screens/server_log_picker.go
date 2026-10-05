package screens

import (
	"fmt"

	"github.com/alexbabintsev/laradok/internal/docker"
	"github.com/alexbabintsev/laradok/internal/msgs"
	"github.com/alexbabintsev/laradok/internal/tui/styles"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type hostLogItem struct {
	log docker.HostLog
}

func (i hostLogItem) Title() string { return i.log.Service }
func (i hostLogItem) Description() string {
	size := formatBytes(i.log.Size)
	lines := fmt.Sprintf("%d lines", i.log.Lines)
	created := i.log.CreatedAt.Format("2006-01-02 15:04")
	modified := i.log.ModifiedAt.Format("2006-01-02 15:04")
	return fmt.Sprintf("%s  •  %s  •  %s  •  created %s  •  modified %s", i.log.Path, size, lines, created, modified)
}
func (i hostLogItem) FilterValue() string { return i.log.Service }

// ServerLogPickerScreen discovers host service logs and lets the user pick one.
type ServerLogPickerScreen struct {
	list       list.Model
	sp         spinner.Model
	loading    bool
	errMsg     string
	customLogs []string // extra log paths from container config
	width      int
	height     int
}

func NewServerLogPickerScreen(customLogs []string, width, height int) *ServerLogPickerScreen {
	sp := spinner.New()
	sp.Spinner = spinner.Line
	sp.Style = styles.SpinnerStyle

	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(lipgloss.Color("#FFFFFF")).
		BorderLeftForeground(styles.ColorPrimary)
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(styles.ColorAccent).
		BorderLeftForeground(styles.ColorPrimary)

	l := list.New([]list.Item{}, delegate, width, max(height-6, 1))
	l.Title = "Server Logs"
	l.Styles.Title = styles.TitleBar
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.SetShowHelp(false)

	return &ServerLogPickerScreen{
		list:       l,
		sp:         sp,
		loading:    true,
		customLogs: customLogs,
		width:      width,
		height:     height,
	}
}

func (s *ServerLogPickerScreen) Init() tea.Cmd {
	return s.sp.Tick
}

func (s *ServerLogPickerScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.HostLogsDiscoveredMsg:
		s.loading = false
		if msg.Err != nil {
			s.errMsg = msg.Err.Error()
			return s, nil
		}
		// Merge custom logs from config
		allLogs := msg.Logs
		seen := make(map[string]bool)
		for _, l := range allLogs {
			seen[l.Path] = true
		}
		for _, p := range s.customLogs {
			if !seen[p] {
				allLogs = append(allLogs, docker.HostLog{Service: "custom", Path: p})
			}
		}
		if len(allLogs) == 0 {
			s.errMsg = "No service log files found in this container"
			return s, nil
		}
		items := make([]list.Item, len(allLogs))
		for i, l := range allLogs {
			items[i] = hostLogItem{log: l}
		}
		cmd := s.list.SetItems(items)
		return s, cmd

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
			if !s.loading && s.errMsg == "" {
				if item, ok := s.list.SelectedItem().(hostLogItem); ok {
					l := item.log
					return s, func() tea.Msg {
						return msgs.PushLogTailMsg{
							FilePath: l.Path,
							Title:    fmt.Sprintf("%s log", l.Service),
						}
					}
				}
			}
		}

	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
		if !s.loading {
			s.list.SetWidth(msg.Width)
			s.list.SetHeight(msg.Height - 6)
		}
		return s, nil
	}

	if !s.loading && s.errMsg == "" {
		var cmd tea.Cmd
		s.list, cmd = s.list.Update(msg)
		return s, cmd
	}

	return s, nil
}

func (s *ServerLogPickerScreen) View() string {
	header := styles.TitleBar.Render("Server Logs")
	if s.loading {
		return header + "\n\n  " + s.sp.View() + " Detecting service logs..."
	}
	if s.errMsg != "" {
		return header + "\n\n  " + lipgloss.NewStyle().Foreground(styles.ColorDanger).Render(s.errMsg)
	}
	return s.list.View()
}
