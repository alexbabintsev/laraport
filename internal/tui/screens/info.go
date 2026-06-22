package screens

import (
	"fmt"
	"strings"

	"github.com/alexbabintsev/laradok/internal/docker"
	"github.com/alexbabintsev/laradok/internal/msgs"
	"github.com/alexbabintsev/laradok/internal/tui/styles"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// InfoScreen shows parsed `docker inspect` output for the active container.
type InfoScreen struct {
	containerName string
	info          docker.ContainerInfo
	lines         []string // pre-rendered display lines
	scrollOff     int
	loading       bool
	errMsg        string
	sp            spinner.Model
	width         int
	height        int
}

func NewInfoScreen(containerName string, width, height int) *InfoScreen {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styles.SpinnerStyle
	return &InfoScreen{
		containerName: containerName,
		loading:       true,
		sp:            sp,
		width:         width,
		height:        height,
	}
}

func (s *InfoScreen) Init() tea.Cmd { return s.sp.Tick }

func (s *InfoScreen) visibleRows() int {
	v := s.height - 4
	if v < 3 {
		v = 3
	}
	return v
}

func (s *InfoScreen) clampScroll() {
	vis := s.visibleRows()
	maxOff := len(s.lines) - vis
	if maxOff < 0 {
		maxOff = 0
	}
	if s.scrollOff > maxOff {
		s.scrollOff = maxOff
	}
	if s.scrollOff < 0 {
		s.scrollOff = 0
	}
}

func (s *InfoScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.ContainerInfoLoadedMsg:
		s.loading = false
		if msg.Err != nil {
			s.errMsg = msg.Err.Error()
			return s, nil
		}
		s.info = msg.Info
		s.buildLines()
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
		case "down", "j":
			s.scrollOff++
			s.clampScroll()
		case "up", "k":
			s.scrollOff--
			s.clampScroll()
		case "pgdown":
			s.scrollOff += s.visibleRows()
			s.clampScroll()
		case "pgup":
			s.scrollOff -= s.visibleRows()
			s.clampScroll()
		case "home", "g":
			s.scrollOff = 0
		case "end", "G":
			s.scrollOff = len(s.lines)
			s.clampScroll()
		}

	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
		if !s.loading && s.errMsg == "" {
			s.buildLines()
		}
	}
	return s, nil
}

func (s *InfoScreen) buildLines() {
	keyStyle := lipgloss.NewStyle().Foreground(styles.ColorMuted)
	valStyle := lipgloss.NewStyle().Foreground(styles.ColorText)
	sectionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#F1C40F")).Bold(true)
	linkStyle := lipgloss.NewStyle().Foreground(styles.ColorAccent)

	// row renders "  key   value" with an aligned key column.
	row := func(key, val string) string {
		return "  " + keyStyle.Render(fmt.Sprintf("%-14s", key)) + " " + valStyle.Render(val)
	}

	var out []string
	in := s.info

	out = append(out, sectionStyle.Render(" General"))
	out = append(out, row("Name", in.Name))
	out = append(out, row("ID", shortID(in.ID)))
	out = append(out, row("Image", in.Image))
	out = append(out, row("Status", in.Status))

	if len(in.Networks) > 0 {
		out = append(out, "", sectionStyle.Render(" Network"))
		for _, n := range in.Networks {
			ip := n.IPAddress
			if ip == "" {
				ip = "—"
			}
			out = append(out, "  "+keyStyle.Render(fmt.Sprintf("%-14s", n.Name))+" "+linkStyle.Render(ip))
		}
	}

	if len(in.Mounts) > 0 {
		out = append(out, "", sectionStyle.Render(" Mounts"))
		for _, m := range in.Mounts {
			mode := "ro"
			if m.RW {
				mode = "rw"
			}
			line := fmt.Sprintf("%s → %s (%s, %s)", m.Source, m.Destination, m.Type, mode)
			out = append(out, "  "+valStyle.Render(truncate(line, s.width-4)))
		}
	}

	if len(in.Labels) > 0 {
		out = append(out, "", sectionStyle.Render(" Labels"))
		// Align the value column to the longest key (capped).
		kw := 0
		for _, l := range in.Labels {
			if n := len(l.Key); n > kw && n <= 40 {
				kw = n
			}
		}
		for _, l := range in.Labels {
			val := l.Value
			avail := s.width - kw - 5
			if avail > 0 {
				val = truncate(val, avail)
			}
			out = append(out, "  "+keyStyle.Render(fmt.Sprintf("%-*s", kw, l.Key))+" "+valStyle.Render(val))
		}
	}

	s.lines = out
	s.clampScroll()
}

func (s *InfoScreen) View() string {
	title := styles.TitleBar.Render("Info — " + s.containerName)

	var body string
	switch {
	case s.loading:
		body = lipgloss.NewStyle().Padding(1, 2).Render(s.sp.View() + " inspecting container...")
	case s.errMsg != "":
		body = lipgloss.NewStyle().Padding(1, 2).
			Foreground(styles.ColorDanger).Render("Error: " + s.errMsg)
	default:
		vis := s.visibleRows()
		end := s.scrollOff + vis
		if end > len(s.lines) {
			end = len(s.lines)
		}
		visible := s.lines[s.scrollOff:end]
		body = strings.Join(visible, "\n")
		if len(s.lines) > vis {
			body += "\n" + lipgloss.NewStyle().Foreground(styles.ColorMuted).
				Padding(0, 2).Render(fmt.Sprintf("-- %d–%d of %d --", s.scrollOff+1, end, len(s.lines)))
		}
	}

	help := styles.StatusBar.Width(s.width).Render(
		styles.StatusBarKey.Render("↑↓") + " scroll  " +
			styles.StatusBarKey.Render("esc") + " back",
	)

	content := lipgloss.JoinVertical(lipgloss.Left, title, "", body)
	return styles.PinToBottom(s.height, content, help)
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func truncate(s string, max int) string {
	if max <= 1 || len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
