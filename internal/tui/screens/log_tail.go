package screens

import (
	"fmt"
	"strings"

	"github.com/alexbabintsev/laradok/internal/msgs"
	"github.com/alexbabintsev/laradok/internal/tui/styles"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// LogTailScreen streams output (file log or docker logs) in real-time.
type LogTailScreen struct {
	title          string
	vp             viewport.Model
	sp             spinner.Model
	lines          []string
	active         bool
	follow         bool // auto-scroll to bottom; paused when user scrolls up
	wrap           bool
	atTop          bool // true when we've loaded all the way to the beginning of the file
	loading        bool // true while a chunk is being loaded
	totalFileLines int  // total lines in the file (0 = unknown / docker logs)
	bufferTopLine  int  // 1-based file line number of lines[0] (0 = unknown)
	initLineCount  int  // number of initial lines expected (don't increment total for these)
	sessionID      uint64
	width          int
	height         int
}

func NewLogTailScreen(title string, width, height int) *LogTailScreen {
	vp := viewport.New(max(width-4, 1), max(height-4, 1))
	vp.Style = styles.OutputStyle

	sp := spinner.New()
	sp.Spinner = spinner.Line
	sp.Style = styles.SpinnerStyle

	return &LogTailScreen{
		title:  title,
		vp:     vp,
		sp:     sp,
		active: true,
		follow: true,
		width:  width,
		height: height,
	}
}

func (s *LogTailScreen) setContent() {
	if s.wrap {
		// vp.Width is the outer width; OutputStyle has border(2) + padding(2) = 4 chars overhead
		setViewportContent(&s.vp, wrapLines(s.lines, max(s.vp.Width-4, 1)))
	} else {
		setViewportContent(&s.vp, strings.Join(s.lines, "\n"))
	}
}

func (s *LogTailScreen) Init() tea.Cmd {
	return s.sp.Tick
}

func (s *LogTailScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.LogTailInitMsg:
		s.totalFileLines = msg.TotalLines
		s.bufferTopLine = msg.TopLine
		// The initial batch has (total - topLine + 1) lines already counted in totalFileLines
		s.initLineCount = msg.TotalLines - msg.TopLine + 1
		// If we loaded from line 1, we're already at the top — no further chunks to load.
		if msg.TopLine <= 1 {
			s.atTop = true
		}
		return s, nil

	case msgs.OutputLineMsg:
		s.sessionID = msg.SessionID
		s.lines = append(s.lines, msg.Line)
		if s.initLineCount > 0 {
			s.initLineCount-- // initial batch line, already counted
		} else if s.totalFileLines > 0 {
			s.totalFileLines++ // truly new line, file grew
		}
		s.setContent()
		if s.follow {
			s.vp.GotoBottom()
		}
		return s, nil

	case msgs.OutputDoneMsg:
		s.active = false
		return s, nil

	case msgs.LogChunkLoadedMsg:
		s.loading = false
		if msg.Err == nil && len(msg.Lines) > 0 {
			s.atTop = msg.AtTop
			s.bufferTopLine = msg.TopLine
			if msg.TotalLines > 0 {
				s.totalFileLines = msg.TotalLines
			}

			// Save the current YOffset before changing content
			prevOffset := s.vp.YOffset

			// Prepend lines
			s.lines = append(msg.Lines, s.lines...)
			s.setContent()

			// Restore scroll position: the content grew by len(msg.Lines) at the top,
			// so shift the offset by the same amount to keep the same content visible.
			s.vp.SetYOffset(prevOffset + len(msg.Lines))
		} else if msg.AtTop {
			s.atTop = true
		}
		return s, nil

	case spinner.TickMsg:
		if s.active || s.loading {
			var cmd tea.Cmd
			s.sp, cmd = s.sp.Update(msg)
			return s, cmd
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return s, tea.Quit
		case "esc":
			s.active = false
			return s, func() tea.Msg { return msgs.PopMsg{} }
		case "f", "end":
			s.follow = true
			s.vp.GotoBottom()
			return s, nil
		case "up", "k", "pgup", "u":
			s.follow = false
		case "f2":
			s.wrap = !s.wrap
			s.setContent()
			return s, nil
		}

	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
		s.vp.Width = max(msg.Width-4, 1)
		s.vp.Height = max(msg.Height-4, 1)
		s.setContent()
	}

	prevOffset := s.vp.YOffset
	var cmd tea.Cmd
	s.vp, cmd = s.vp.Update(msg)

	// Trigger preload when user scrolls up and approaches the top of the buffer.
	// Only trigger when the viewport actually moved upward (not on every tick).
	const preloadThreshold = 100
	if !s.atTop && !s.loading && s.sessionID > 0 &&
		s.vp.YOffset < preloadThreshold && s.vp.YOffset < prevOffset {
		s.loading = true
		sessionID := s.sessionID
		return s, tea.Batch(cmd, func() tea.Msg {
			return msgs.LoadMoreLinesMsg{SessionID: sessionID}
		})
	}

	return s, cmd
}

// visibleContentHeight returns the number of content lines the viewport displays
// (viewport height minus the frame/border overhead from the style).
func (s *LogTailScreen) visibleContentHeight() int {
	h := s.vp.Height - s.vp.Style.GetVerticalFrameSize()
	if h < 1 {
		h = 1
	}
	return h
}

func (s *LogTailScreen) View() string {
	var statusIcon string
	if s.active {
		statusIcon = s.sp.View() + " "
	} else {
		statusIcon = styles.DimStyle.Render("● ")
	}

	pauseTag := ""
	if s.active && !s.follow {
		pauseTag = "  " + lipgloss.NewStyle().
			Background(styles.ColorWarning).
			Foreground(lipgloss.Color("#000000")).
			Padding(0, 1).
			Bold(true).
			Render("PAUSED")
	}

	header := styles.TitleBar.Render(statusIcon+s.title) + pauseTag

	lineInfo := ""
	if s.vp.TotalLineCount() > 0 {
		prefix := ""
		if s.loading {
			prefix = s.sp.View() + " loading…  "
		} else if !s.atTop {
			prefix = styles.DimStyle.Render("↑ more") + "  "
		}

		var stats string
		if s.bufferTopLine > 0 {
			// We know file positions — show current line at bottom of viewport
			visH := s.visibleContentHeight()
			bottomLine := s.bufferTopLine + s.vp.YOffset + visH - 1
			total := s.totalFileLines
			if total == 0 {
				// Estimate: buffer covers from bufferTopLine to bufferTopLine+len(lines)-1
				total = s.bufferTopLine + len(s.lines) - 1
			}
			if bottomLine > total {
				bottomLine = total
			}
			if bottomLine < 1 {
				bottomLine = 1
			}
			pct := bottomLine * 100 / total
			stats = fmt.Sprintf("line %d/%d  %d%%", bottomLine, total, pct)
			lineInfo = prefix + styles.DimStyle.Render(stats)
		} else {
			// File position not yet known (counting or docker logs)
			stats = fmt.Sprintf("%d lines  %.0f%%", len(s.lines), s.vp.ScrollPercent()*100)
			lineInfo = prefix + styles.DimStyle.Render(stats)
		}
	}

	vpView := lipgloss.NewStyle().Padding(0, 2).Render(s.vp.View())

	wrapIndicator := "  " + styles.StatusBarKey.Render("f2") + " wrap"
	if s.wrap {
		wrapIndicator = "  " + styles.StatusBarKey.Render("f2") + " " +
			lipgloss.NewStyle().Foreground(styles.ColorSuccess).Render("wrap:on")
	}

	var helpStr string
	if s.follow {
		helpStr = styles.StatusBarKey.Render("↑/PgUp") + " scroll & pause  " +
			styles.StatusBarKey.Render("esc") + " stop & back"
	} else {
		helpStr = styles.StatusBarKey.Render("↑↓/PgUp/PgDn") + " scroll  " +
			styles.StatusBarKey.Render("f/End") + " follow  " +
			styles.StatusBarKey.Render("esc") + " back"
	}

	help := styles.StatusBar.Width(s.width).Render(lineInfo + "  " + helpStr + wrapIndicator)

	content := lipgloss.JoinVertical(lipgloss.Left, header, "", vpView)
	return styles.PinToBottom(s.height, content, help)
}
