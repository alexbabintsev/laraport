package screens

import (
	"fmt"
	"path/filepath"

	"github.com/alexbabintsev/laradok/internal/docker"
	"github.com/alexbabintsev/laradok/internal/msgs"
	"github.com/alexbabintsev/laradok/internal/tui/styles"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type logFileItem struct {
	info docker.LogFileInfo
}

func (i logFileItem) Title() string { return filepath.Base(i.info.Path) }
func (i logFileItem) Description() string {
	size := formatBytes(i.info.Size)
	lines := fmt.Sprintf("%d lines", i.info.Lines)
	created := i.info.CreatedAt.Format("2006-01-02 15:04")
	modified := i.info.ModifiedAt.Format("2006-01-02 15:04")
	return fmt.Sprintf("%s  •  %s  •  %s  •  created %s  •  modified %s", i.info.Path, size, lines, created, modified)
}
func (i logFileItem) FilterValue() string { return i.info.Path }

func formatBytes(b int64) string {
	switch {
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// LogFilePickerScreen lists log files in the container and lets the user pick one.
type LogFilePickerScreen struct {
	list    list.Model
	sp      spinner.Model
	loading bool
	errMsg  string
	width   int
	height  int
}

func NewLogFilePickerScreen(width, height int) *LogFilePickerScreen {
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

	l := list.New([]list.Item{}, delegate, width, height-6)
	l.Title = "Select Log File"
	l.Styles.Title = styles.TitleBar
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.SetShowHelp(false)

	return &LogFilePickerScreen{
		list:    l,
		sp:      sp,
		loading: true,
		width:   width,
		height:  height,
	}
}

func (s *LogFilePickerScreen) Init() tea.Cmd {
	return s.sp.Tick
}

func (s *LogFilePickerScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.LogFilesLoadedMsg:
		s.loading = false
		if msg.Err != nil {
			s.errMsg = msg.Err.Error()
			return s, nil
		}
		items := make([]list.Item, len(msg.Files))
		for i, f := range msg.Files {
			items[i] = logFileItem{info: f}
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
				if item, ok := s.list.SelectedItem().(logFileItem); ok {
					path := item.info.Path
					title := filepath.Base(path)
					return s, func() tea.Msg {
						return msgs.PushLogTailMsg{FilePath: path, Title: title}
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

func (s *LogFilePickerScreen) View() string {
	header := styles.TitleBar.Render("Log Files")
	if s.loading {
		return header + "\n\n  " + s.sp.View() + " Loading log files..."
	}
	if s.errMsg != "" {
		return header + "\n\n  " + lipgloss.NewStyle().Foreground(styles.ColorDanger).Render("Error: "+s.errMsg)
	}
	return s.list.View()
}
