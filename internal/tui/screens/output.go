package screens

import (
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"strings"

	"github.com/alexbabintsev/laradok/internal/docker"
	"github.com/alexbabintsev/laradok/internal/msgs"
	"github.com/alexbabintsev/laradok/internal/tui/styles"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// OutputScreen streams command output into a scrollable viewport.
type OutputScreen struct {
	title        string
	vp           viewport.Model
	sp           spinner.Model
	lines        []string
	lastIsStatus bool // last appended line is an in-place status line
	running      bool
	wrap         bool
	width        int
	height       int
}

// wrapLines wraps each line to maxWidth terminal cells, breaking at spaces
// where possible. Widths are measured in display cells (not bytes), so
// multi-byte text such as Cyrillic is never cut mid-character and wide
// characters count double; ANSI colour codes are preserved.
func wrapLines(lines []string, maxWidth int) string {
	if maxWidth <= 0 {
		return strings.Join(lines, "\n")
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = ansi.Wrap(line, maxWidth, "")
	}
	return strings.Join(out, "\n")
}

// fitViewport keeps vp at least one line taller (and one column wider) than
// its style's frame: below that bubbles' viewport computes offsets past the
// end of the content.
func fitViewport(vp *viewport.Model) {
	vp.Height = max(vp.Height, vp.Style.GetVerticalFrameSize()+1)
	vp.Width = max(vp.Width, vp.Style.GetHorizontalFrameSize()+1)
}

// setViewportContent sets vp's content and pulls the scroll offset back into
// range. bubbles' viewport (v1.0.0) panics in ScrollUp when the offset is left
// past the end of shorter content — e.g. after re-wrapping on a resize.
func setViewportContent(vp *viewport.Model, content string) {
	fitViewport(vp)
	vp.SetContent(content)
	vp.SetYOffset(vp.YOffset)
}

func (s *OutputScreen) setContent() {
	if s.wrap {
		// vp.Width is the outer width; OutputStyle has border(2) + padding(2) = 4 chars overhead
		setViewportContent(&s.vp, wrapLines(s.lines, max(s.vp.Width-4, 1)))
	} else {
		setViewportContent(&s.vp, strings.Join(s.lines, "\n"))
	}
}

func NewOutputScreen(title string, width, height int) *OutputScreen {
	vp := viewport.New(max(width-4, 1), max(height-4, 1))
	vp.Style = styles.OutputStyle

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styles.SpinnerStyle

	return &OutputScreen{
		title:   title,
		vp:      vp,
		sp:      sp,
		running: true,
		width:   width,
		height:  height,
	}
}

func (s *OutputScreen) Init() tea.Cmd {
	return s.sp.Tick
}

// waitForLine returns a Cmd that reads one line from the channel.
func WaitForLine(ch <-chan string, sessionID uint64) tea.Cmd {
	return func() tea.Msg {
		line, ok := <-ch
		if !ok {
			return msgs.OutputDoneMsg{SessionID: sessionID}
		}
		return msgs.OutputLineMsg{Line: line, SessionID: sessionID}
	}
}

func (s *OutputScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.OutputLineMsg:
		if text, ok := strings.CutPrefix(msg.Line, docker.StatusLinePrefix); ok {
			// Status line: overwrite the previous status line in place.
			if s.lastIsStatus && len(s.lines) > 0 {
				s.lines[len(s.lines)-1] = text
			} else {
				s.lines = append(s.lines, text)
			}
			s.lastIsStatus = true
		} else {
			s.lines = append(s.lines, msg.Line)
			s.lastIsStatus = false
		}
		s.setContent()
		s.vp.GotoBottom()
		return s, nil // caller (App) will re-dispatch WaitForLine

	case msgs.OutputDoneMsg:
		s.running = false
		return s, nil

	case spinner.TickMsg:
		if s.running {
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
		case "f2":
			s.wrap = !s.wrap
			s.setContent()
			return s, nil
		}

	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
		s.vp.Width = max(msg.Width-4, 1)
		s.vp.Height = max(msg.Height-8, 1)
		s.setContent()
	}

	var cmd tea.Cmd
	s.vp, cmd = s.vp.Update(msg)
	return s, cmd
}

func (s *OutputScreen) View() string {
	headerContent := s.title
	if s.running {
		headerContent = s.sp.View() + " " + s.title
	} else {
		headerContent = styles.SuccessStyle.Render("✓") + " " + s.title
	}
	header := styles.TitleBar.Render(headerContent)

	lineCount := styles.DimStyle.Render(
		lipgloss.NewStyle().Render(" " + strings.Repeat("-", 20)),
	)
	_ = lineCount

	vpView := lipgloss.NewStyle().Padding(0, 2).Render(s.vp.View())

	wrapIndicator := "  " + styles.StatusBarKey.Render("f2") + " wrap"
	if s.wrap {
		wrapIndicator = "  " + styles.StatusBarKey.Render("f2") + " " +
			lipgloss.NewStyle().Foreground(styles.ColorSuccess).Render("wrap:on")
	}

	var status string
	if s.running {
		status = styles.StatusBar.Width(s.width).Render(
			styles.SpinnerStyle.Render("running...") + "  " +
				styles.StatusBarKey.Render("esc") + " back when done" + wrapIndicator,
		)
	} else {
		var scrollInfo string
		if s.vp.TotalLineCount() > 0 {
			scrollInfo = styles.DimStyle.Render(fmt.Sprintf("%.0f%%", s.vp.ScrollPercent()*100))
		}
		status = styles.StatusBar.Width(s.width).Render(
			styles.SuccessStyle.Render("done") + "  " +
				scrollInfo + "  " +
				styles.StatusBarKey.Render("↑↓") + " scroll  " +
				styles.StatusBarKey.Render("esc") + " back" + wrapIndicator,
		)
	}

	content := lipgloss.JoinVertical(lipgloss.Left, header, "", vpView)
	return styles.PinToBottom(s.height, content, status)
}

// SetWrap sets whether long lines are wrapped (the default comes from the
// settings; f2 toggles it).
func (s *OutputScreen) SetWrap(on bool) {
	s.wrap = on
	s.setContent()
}
