package screens

import (
	"fmt"
	"strings"

	"github.com/alexbabintsev/laradok/internal/docker"
	"github.com/alexbabintsev/laradok/internal/msgs"
	"github.com/alexbabintsev/laradok/internal/tui/styles"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const maxSuggestions = 20

// AutocompleteScreen provides a text input with live-filtering autocomplete for any
// set of commands (artisan, composer, npm…).
// title      — TitleBar text
// headerLabel — prompt label shown above the input (e.g. "php artisan")
// cmdPrefix  — prepended to the command name when executing (e.g. "php artisan", "composer", "npm run")
type AutocompleteScreen struct {
	title       string
	headerLabel string
	cmdPrefix   string
	input       textinput.Model
	sp          spinner.Model
	loading     bool
	allCmds     []docker.ArtisanCommand
	suggestions []docker.ArtisanCommand
	selected    int  // index in suggestions; -1 = none
	scrollOff   int  // first visible row index
	navigating  bool // true when input change came from navigation, not user typing
	width       int
	height      int
}

func newAutocompleteScreen(title, headerLabel, cmdPrefix string, width, height int) *AutocompleteScreen {
	ti := textinput.New()
	ti.Placeholder = "type to filter…"
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = width - 20
	ti.PromptStyle = lipgloss.NewStyle().Foreground(styles.ColorPrimary)
	ti.TextStyle = lipgloss.NewStyle().Foreground(styles.ColorText)

	sp := spinner.New()
	sp.Spinner = spinner.Line
	sp.Style = styles.SpinnerStyle

	return &AutocompleteScreen{
		title:       title,
		headerLabel: headerLabel,
		cmdPrefix:   cmdPrefix,
		input:       ti,
		sp:          sp,
		loading:     true,
		selected:    -1,
		width:       width,
		height:      height,
	}
}

// Constructors for each tool type.

func NewArtisanCmdScreen(width, height int) *AutocompleteScreen {
	s := newAutocompleteScreen("Artisan Commands", "php artisan", "php artisan", width, height)
	s.input.Placeholder = "e.g.  queue:work --queue=default"
	return s
}

func NewComposerCmdScreen(width, height int) *AutocompleteScreen {
	s := newAutocompleteScreen("Composer Commands", "composer", "composer", width, height)
	s.input.Placeholder = "e.g.  require vendor/package"
	return s
}

func NewNpmCmdScreen(width, height int) *AutocompleteScreen {
	s := newAutocompleteScreen("Npm Commands", "npm run", "npm run", width, height)
	s.input.Placeholder = "e.g.  build"
	return s
}

func (s *AutocompleteScreen) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, s.sp.Tick)
}

// groupOf returns the namespace of a command (part before the first ':').
func groupOf(name string) string {
	if idx := strings.Index(name, ":"); idx >= 0 {
		return name[:idx]
	}
	return ""
}

// displayRow is a renderable line: either a group header or a command entry.
type displayRow struct {
	isHeader bool
	label    string
	cmd      docker.ArtisanCommand
	cmdIdx   int // -1 for headers
}

func (s *AutocompleteScreen) buildRows() []displayRow {
	var rows []displayRow
	const sentinel = "\x00"
	currentGroup := sentinel
	for i, c := range s.suggestions {
		g := groupOf(c.Name)
		if g != currentGroup {
			label := g
			if label == "" {
				label = "general"
			}
			rows = append(rows, displayRow{isHeader: true, label: label, cmdIdx: -1})
			currentGroup = g
		}
		rows = append(rows, displayRow{cmd: c, cmdIdx: i})
	}
	return rows
}

func (s *AutocompleteScreen) updateSuggestions() {
	query := strings.TrimSpace(s.input.Value())
	qLow := strings.ToLower(query)
	s.suggestions = nil
	for _, c := range s.allCmds {
		if query == "" ||
			strings.Contains(strings.ToLower(c.Name), qLow) ||
			strings.Contains(strings.ToLower(c.Desc), qLow) {
			s.suggestions = append(s.suggestions, c)
			if query != "" && len(s.suggestions) == maxSuggestions {
				break
			}
		}
	}
	if s.selected >= len(s.suggestions) {
		s.selected = len(s.suggestions) - 1
	}
	s.scrollOff = 0
	s.clampScroll()
}

func (s *AutocompleteScreen) visibleRows() int {
	v := s.height - 7 // title(1) + inputArea(5) + statusBar(1)
	if v < 3 {
		v = 3
	}
	return v
}

func (s *AutocompleteScreen) rowOfSelected(rows []displayRow) int {
	for i, r := range rows {
		if !r.isHeader && r.cmdIdx == s.selected {
			return i
		}
	}
	return 0
}

func (s *AutocompleteScreen) clampScroll() {
	vis := s.visibleRows()
	if s.selected >= 0 {
		rows := s.buildRows()
		sel := s.rowOfSelected(rows)
		if sel < s.scrollOff {
			s.scrollOff = sel
		}
		if sel >= s.scrollOff+vis {
			s.scrollOff = sel - vis + 1
		}
	}
	if s.scrollOff < 0 {
		s.scrollOff = 0
	}
}

func (s *AutocompleteScreen) syncInput() {
	if s.selected >= 0 && s.selected < len(s.suggestions) {
		s.navigating = true
		s.input.SetValue(s.suggestions[s.selected].Name)
		s.input.CursorEnd()
	}
}

func (s *AutocompleteScreen) runSelected(name string) tea.Cmd {
	prefix := s.cmdPrefix
	full := prefix + " " + name
	return func() tea.Msg {
		return msgs.PushOutputMsg{
			Title:  s.title + ": " + name,
			RawCmd: full,
		}
	}
}

func (s *AutocompleteScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.ArtisanCommandsLoadedMsg:
		s.loading = false
		if msg.Err == nil {
			s.allCmds = msg.Commands
		}
		s.updateSuggestions()
		return s, nil

	case msgs.ComposerCommandsLoadedMsg:
		s.loading = false
		if msg.Err == nil {
			s.allCmds = msg.Commands
			s.cmdPrefix = msg.ComposerBin
			s.headerLabel = msg.ComposerBin
		}
		s.updateSuggestions()
		return s, nil

	case msgs.NpmCommandsLoadedMsg:
		s.loading = false
		if msg.Err == nil {
			s.allCmds = msg.Commands
		}
		s.updateSuggestions()
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
			if s.selected >= 0 {
				s.selected = -1
				s.input.SetValue("")
				s.updateSuggestions()
				return s, nil
			}
			return s, func() tea.Msg { return msgs.PopMsg{} }
		case "tab", "down":
			if len(s.suggestions) > 0 {
				s.selected = (s.selected + 1) % len(s.suggestions)
				s.clampScroll()
				s.syncInput()
			}
			return s, nil
		case "up":
			if len(s.suggestions) > 0 {
				if s.selected <= 0 {
					s.selected = len(s.suggestions) - 1
				} else {
					s.selected--
				}
				s.clampScroll()
				s.syncInput()
			}
			return s, nil
		case "pgdown":
			if len(s.suggestions) > 0 {
				s.selected += s.visibleRows()
				if s.selected >= len(s.suggestions) {
					s.selected = len(s.suggestions) - 1
				}
				s.clampScroll()
				s.syncInput()
			}
			return s, nil
		case "pgup":
			if len(s.suggestions) > 0 {
				s.selected -= s.visibleRows()
				if s.selected < 0 {
					s.selected = 0
				}
				s.clampScroll()
				s.syncInput()
			}
			return s, nil
		case "enter":
			val := strings.TrimSpace(s.input.Value())
			if s.selected >= 0 {
				val = s.suggestions[s.selected].Name
			}
			if val == "" {
				return s, nil
			}
			return s, s.runSelected(val)
		}

	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
		s.input.Width = msg.Width - 20
	}

	prev := s.input.Value()
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	if s.input.Value() != prev {
		if s.navigating {
			s.navigating = false
		} else {
			s.selected = -1
			s.updateSuggestions()
		}
	}
	return s, cmd
}

func (s *AutocompleteScreen) View() string {
	title := styles.TitleBar.Render(s.title)

	headerLabel := lipgloss.NewStyle().Foreground(styles.ColorTextDim).Bold(true).Render(s.headerLabel)
	inputArea := lipgloss.NewStyle().Padding(1, 2).Render(
		headerLabel + "\n" + s.input.View(),
	)

	groupStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#F1C40F")).Bold(true)
	cmdStyle   := lipgloss.NewStyle().Foreground(lipgloss.Color("#2ECC71"))
	descStyle  := lipgloss.NewStyle().Foreground(styles.ColorText)
	selStyle   := lipgloss.NewStyle().Background(lipgloss.Color("#1E3A5F")).Bold(true)

	nameWidth := 0
	for _, c := range s.suggestions {
		if l := len(c.Name); l > nameWidth {
			nameWidth = l
		}
	}
	descOffset := nameWidth + 2

	var listLines []string
	if s.loading {
		listLines = append(listLines, "  "+s.sp.View()+" Loading commands...")
	} else if len(s.suggestions) == 0 {
		listLines = append(listLines, lipgloss.NewStyle().
			Foreground(styles.ColorTextDim).Padding(0, 2).Render("No matches"))
	} else {
		rows := s.buildRows()
		vis := s.visibleRows()
		end := s.scrollOff + vis
		if end > len(rows) {
			end = len(rows)
		}
		firstCmd, lastCmd := -1, -1
		for _, r := range rows[s.scrollOff:end] {
			if r.isHeader {
				listLines = append(listLines, groupStyle.Render(r.label))
				continue
			}
			if firstCmd < 0 {
				firstCmd = r.cmdIdx
			}
			lastCmd = r.cmdIdx

			paddedName := fmt.Sprintf("%-*s", nameWidth, r.cmd.Name)
			desc := r.cmd.Desc
			availDesc := s.width - descOffset - 2
			if availDesc > 0 && len(desc) > availDesc {
				desc = desc[:availDesc-1] + "…"
			} else if availDesc <= 0 {
				desc = ""
			}

			if r.cmdIdx == s.selected {
				line := " " + paddedName + " " + desc
				listLines = append(listLines, selStyle.Width(s.width-2).Render(line))
			} else {
				line := " " + cmdStyle.Render(paddedName) + " " + descStyle.Render(desc)
				listLines = append(listLines, line)
			}
		}
		if len(rows) > vis && lastCmd >= 0 {
			indicator := lipgloss.NewStyle().
				Foreground(styles.ColorMuted).Padding(0, 2).
				Render(fmt.Sprintf("-- %d–%d of %d --", firstCmd+1, lastCmd+1, len(s.suggestions)))
			listLines = append(listLines, indicator)
		}
	}
	listView := strings.Join(listLines, "\n")

	help := styles.StatusBar.Width(s.width).Render(
		styles.StatusBarKey.Render("enter")+" run  "+
			styles.StatusBarKey.Render("↓/tab")+" navigate  "+
			styles.StatusBarKey.Render("↑")+" up  "+
			styles.StatusBarKey.Render("esc")+" back",
	)

	content := lipgloss.JoinVertical(lipgloss.Left, title, inputArea, listView)
	return styles.PinToBottom(s.height, content, help)
}
