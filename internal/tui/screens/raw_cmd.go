package screens

import (
	"strings"

	"github.com/alexbabintsev/laradok/internal/msgs"
	"github.com/alexbabintsev/laradok/internal/tui/styles"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// RawCmdScreen lets the user type any shell command, run it, and interact with its stdin.
type RawCmdScreen struct {
	cmdInput  textinput.Model // top: command to run
	stdinInput textinput.Model // bottom: interactive stdin when running
	sp        spinner.Model
	vp        viewport.Model
	lines     []string
	running   bool
	inCh      chan<- string // nil when not running
	width     int
	height    int
}

func NewRawCmdScreen(width, height int) *RawCmdScreen {
	cmd := textinput.New()
	cmd.Placeholder = "e.g.  docker exec app bash"
	cmd.Focus()
	cmd.CharLimit = 512
	cmd.Width = width - 6
	cmd.PromptStyle = lipgloss.NewStyle().Foreground(styles.ColorPrimary)
	cmd.TextStyle = lipgloss.NewStyle().Foreground(styles.ColorText)

	stdin := textinput.New()
	stdin.Placeholder = "type input and press enter…"
	stdin.CharLimit = 512
	stdin.Width = width - 6
	stdin.PromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#F1C40F"))
	stdin.TextStyle = lipgloss.NewStyle().Foreground(styles.ColorText)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styles.SpinnerStyle

	vp := viewport.New(width-4, height-12)
	vp.Style = styles.OutputStyle

	return &RawCmdScreen{
		cmdInput:   cmd,
		stdinInput: stdin,
		sp:         sp,
		vp:         vp,
		width:      width,
		height:     height,
	}
}

func (s *RawCmdScreen) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, s.sp.Tick)
}

func (s *RawCmdScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.RawCmdStartMsg:
		if msg.Err != nil {
			s.lines = append(s.lines, "ERROR: "+msg.Err.Error())
			s.vp.SetContent(strings.Join(s.lines, "\n"))
			s.running = false
			return s, nil
		}
		s.running = true
		s.inCh = msg.InCh
		s.stdinInput.Focus()
		s.cmdInput.Blur()
		return s, WaitForLine(msg.OutCh, msg.SessionID)

	case msgs.OutputLineMsg:
		s.lines = append(s.lines, msg.Line)
		s.vp.SetContent(strings.Join(s.lines, "\n"))
		s.vp.GotoBottom()
		return s, nil // App will re-dispatch WaitForLine

	case msgs.OutputDoneMsg:
		s.running = false
		s.inCh = nil
		s.stdinInput.Blur()
		s.cmdInput.Focus()
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
		case "enter":
			if s.running {
				// Send line to stdin of running command
				line := s.stdinInput.Value()
				s.stdinInput.SetValue("")
				if s.inCh != nil {
					ch := s.inCh
					go func() { ch <- line }()
				}
				return s, nil
			}
			// Start the command
			val := strings.TrimSpace(s.cmdInput.Value())
			if val == "" {
				return s, nil
			}
			s.lines = nil
			s.vp.SetContent("")
			cmd := val
			return s, func() tea.Msg {
				return msgs.PushRawCmdMsg{Cmd: cmd}
			}
		}

	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
		s.cmdInput.Width = msg.Width - 6
		s.stdinInput.Width = msg.Width - 6
		s.vp.Width = msg.Width - 4
		s.vp.Height = msg.Height - 12
	}

	// Route keyboard input to the active input
	var cmd tea.Cmd
	if s.running {
		s.stdinInput, cmd = s.stdinInput.Update(msg)
	} else {
		s.cmdInput, cmd = s.cmdInput.Update(msg)
	}
	return s, cmd
}

func (s *RawCmdScreen) View() string {
	title := styles.TitleBar.Render("Custom Command")

	promptLabel := lipgloss.NewStyle().Foreground(styles.ColorTextDim).Bold(true).Render("$")
	cmdArea := lipgloss.NewStyle().Padding(1, 2).Render(
		promptLabel + "\n" + s.cmdInput.View(),
	)

	vpView := lipgloss.NewStyle().Padding(0, 2).Render(s.vp.View())

	var inputArea string
	if s.running {
		stdinLabel := lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render("stdin:")
		inputArea = lipgloss.NewStyle().Padding(0, 2).Render(
			stdinLabel + " " + s.stdinInput.View(),
		)
	}

	var status string
	if s.running {
		status = styles.StatusBar.Width(s.width).Render(
			s.sp.View()+" running...  "+
				styles.StatusBarKey.Render("enter")+" send input  "+
				styles.StatusBarKey.Render("esc")+" back",
		)
	} else {
		status = styles.StatusBar.Width(s.width).Render(
			styles.StatusBarKey.Render("enter")+" run  "+
				styles.StatusBarKey.Render("esc")+" back",
		)
	}

	parts := []string{title, cmdArea}
	if len(s.lines) > 0 || s.running {
		parts = append(parts, vpView)
	}
	if s.running {
		parts = append(parts, inputArea)
	}

	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
	return styles.PinToBottom(s.height, content, status)
}
