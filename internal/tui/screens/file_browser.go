package screens

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alexbabintsev/laraport/internal/docker"
	"github.com/alexbabintsev/laraport/internal/msgs"
	"github.com/alexbabintsev/laraport/internal/tui/styles"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// FileBrowserScreen is a file browser over the container filesystem. The user
// can walk into subdirectories (each shown with its size, never above the root)
// and download any file or directory as a .tar.gz archive.
type FileBrowserScreen struct {
	rootPath  string // browsing never goes above this (e.g. "/")
	path      string // current directory
	entries   []docker.DirEntry
	selected  int
	scrollOff int
	loading   bool
	errMsg    string
	sp        spinner.Model
	width     int
	height    int
}

// normDir normalizes a directory path: strips trailing slashes but keeps "/".
func normDir(p string) string {
	p = strings.TrimRight(p, "/")
	if p == "" {
		return "/"
	}
	return p
}

func NewFileBrowserScreen(rootPath string, width, height int) *FileBrowserScreen {
	root := normDir(rootPath)
	sp := spinner.New()
	sp.Spinner = spinner.Line
	sp.Style = styles.SpinnerStyle
	return &FileBrowserScreen{
		rootPath: root,
		path:     root,
		loading:  true,
		sp:       sp,
		width:    width,
		height:   height,
	}
}

func (s *FileBrowserScreen) Init() tea.Cmd {
	return tea.Batch(s.sp.Tick, func() tea.Msg {
		return msgs.LoadDirMsg{Path: s.path}
	})
}

func (s *FileBrowserScreen) visibleRows() int {
	v := s.height - 6
	if v < 3 {
		v = 3
	}
	return v
}

func (s *FileBrowserScreen) clampScroll() {
	vis := s.visibleRows()
	if s.selected < s.scrollOff {
		s.scrollOff = s.selected
	}
	if s.selected >= s.scrollOff+vis {
		s.scrollOff = s.selected - vis + 1
	}
	if s.scrollOff < 0 {
		s.scrollOff = 0
	}
}

// atRoot reports whether the current path is the browse root.
func (s *FileBrowserScreen) atRoot() bool {
	return s.path == s.rootPath
}

func (s *FileBrowserScreen) enter(path string) tea.Cmd {
	s.path = path
	s.loading = true
	s.errMsg = ""
	s.selected = 0
	s.scrollOff = 0
	return tea.Batch(s.sp.Tick, func() tea.Msg {
		return msgs.LoadDirMsg{Path: path}
	})
}

func (s *FileBrowserScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.DirLoadedMsg:
		if msg.Path != s.path {
			return s, nil // stale load
		}
		s.loading = false
		if msg.Err != nil {
			s.errMsg = msg.Err.Error()
			return s, nil
		}
		s.entries = msg.Entries
		s.errMsg = ""
		return s, nil

	case spinner.TickMsg:
		if s.loading {
			var cmd tea.Cmd
			s.sp, cmd = s.sp.Update(msg)
			return s, cmd
		}

	case tea.KeyMsg:
		// While a listing loads (du over a large tree can be slow) only
		// navigation away is allowed, so a slow directory never traps the user.
		if s.loading && msg.String() != "ctrl+c" && msg.String() != "esc" {
			return s, nil
		}
		switch msg.String() {
		case "ctrl+c":
			return s, tea.Quit
		case "esc":
			if s.atRoot() {
				return s, func() tea.Msg { return msgs.PopMsg{} }
			}
			return s, s.enter(normDir(filepath.Dir(s.path)))
		case "backspace", "left", "h":
			if !s.atRoot() {
				return s, s.enter(normDir(filepath.Dir(s.path)))
			}
		case "down", "j":
			if s.selected < len(s.entries)-1 {
				s.selected++
				s.clampScroll()
			}
		case "up", "k":
			if s.selected > 0 {
				s.selected--
				s.clampScroll()
			}
		case "pgdown":
			s.selected += s.visibleRows()
			if s.selected >= len(s.entries) {
				s.selected = len(s.entries) - 1
			}
			s.clampScroll()
		case "pgup":
			s.selected -= s.visibleRows()
			if s.selected < 0 {
				s.selected = 0
			}
			s.clampScroll()
		case "enter", "right", "l":
			if e, ok := s.current(); ok {
				if e.IsDir {
					return s, s.enter(e.Path)
				}
				// Open a file in the log viewer.
				path := e.Path
				name := e.Name
				return s, func() tea.Msg {
					return msgs.PushLogTailMsg{FilePath: path, Title: name}
				}
			}
		case "d":
			if e, ok := s.current(); ok {
				path := e.Path
				return s, func() tea.Msg { return msgs.PushPathDownloadMsg{Path: path} }
			}
		}

	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
	}
	return s, nil
}

func (s *FileBrowserScreen) current() (docker.DirEntry, bool) {
	if s.selected >= 0 && s.selected < len(s.entries) {
		return s.entries[s.selected], true
	}
	return docker.DirEntry{}, false
}

func (s *FileBrowserScreen) View() string {
	title := styles.TitleBar.Render("File Browser")

	pathLine := lipgloss.NewStyle().Foreground(styles.ColorMuted).Padding(0, 0, 0, 2).
		Render(s.path)

	var body string
	switch {
	case s.loading:
		body = lipgloss.NewStyle().Padding(1, 2).Render(
			s.sp.View() + " loading " + s.path + " ...",
		)
	case s.errMsg != "":
		body = lipgloss.NewStyle().Padding(1, 2).
			Foreground(styles.ColorDanger).Render("Error: " + s.errMsg)
	case len(s.entries) == 0:
		body = lipgloss.NewStyle().Padding(1, 2).
			Foreground(styles.ColorMuted).Render("(empty directory)")
	default:
		dirStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#4A9EFF")).Bold(true)
		fileStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#2ECC71"))
		sizeStyle := lipgloss.NewStyle().Foreground(styles.ColorMuted)
		selStyle := lipgloss.NewStyle().Background(lipgloss.Color("#1E3A5F")).Bold(true)

		vis := s.visibleRows()
		end := s.scrollOff + vis
		if end > len(s.entries) {
			end = len(s.entries)
		}

		var lines []string
		for i, e := range s.entries[s.scrollOff:end] {
			idx := s.scrollOff + i
			icon := "📄"
			name := e.Name
			if e.IsDir {
				icon = "📁"
				name = e.Name + "/"
			}
			size := formatBytes(e.Size)
			if idx == s.selected {
				row := fmt.Sprintf("  %s %-40s %10s", icon, name, size)
				lines = append(lines, selStyle.Width(s.width-2).Render(row))
			} else {
				nameStyle := fileStyle
				if e.IsDir {
					nameStyle = dirStyle
				}
				row := fmt.Sprintf("  %s %s  %s", icon,
					nameStyle.Render(fmt.Sprintf("%-40s", name)),
					sizeStyle.Render(fmt.Sprintf("%10s", size)))
				lines = append(lines, row)
			}
		}
		if len(s.entries) > vis {
			lines = append(lines, lipgloss.NewStyle().
				Foreground(styles.ColorMuted).Padding(0, 2).
				Render(fmt.Sprintf("-- %d of %d --", s.selected+1, len(s.entries))))
		}
		body = strings.Join(lines, "\n")
	}

	var help string
	if s.loading || s.errMsg != "" {
		help = styles.StatusBar.Width(s.width).Render(
			styles.StatusBarKey.Render("esc") + " back",
		)
	} else {
		help = styles.StatusBar.Width(s.width).Render(
			styles.StatusBarKey.Render("↑↓") + " navigate  " +
				styles.StatusBarKey.Render("enter") + " open/view  " +
				styles.StatusBarKey.Render("d") + " download  " +
				styles.StatusBarKey.Render("←/bksp") + " up  " +
				styles.StatusBarKey.Render("esc") + " back",
		)
	}

	content := lipgloss.JoinVertical(lipgloss.Left, title, pathLine, "", body)
	return styles.PinToBottom(s.height, content, help)
}
