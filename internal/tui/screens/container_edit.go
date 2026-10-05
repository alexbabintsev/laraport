package screens

import (
	"strings"

	"github.com/alexbabintsev/laradok/internal/config"
	"github.com/alexbabintsev/laradok/internal/msgs"
	"github.com/alexbabintsev/laradok/internal/tui/styles"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// fieldKind distinguishes editable text fields from boolean toggles.
type fieldKind int

const (
	fieldText fieldKind = iota
	fieldToggle
)

type editField struct {
	label string
	kind  fieldKind
	input textinput.Model // used when kind == fieldText
	value bool            // used when kind == fieldToggle
	hint  string
}

// ContainerEditScreen edits per-container config overrides (display name, root
// path, favorite, hidden) and writes them back to the config file.
type ContainerEditScreen struct {
	containerName string
	fields        []editField
	focus         int
	status        string // transient message after save
	width         int
	height        int
}

const (
	fDisplayName = 0
	fRootPath    = 1
	fFavorite    = 2
	fHidden      = 3
)

func NewContainerEditScreen(containerName string, cc config.ContainerConfig, width, height int) *ContainerEditScreen {
	mk := func(val, placeholder string) textinput.Model {
		ti := textinput.New()
		ti.SetValue(val)
		ti.Placeholder = placeholder
		ti.CharLimit = 256
		ti.Width = max(width-20, 1)
		ti.PromptStyle = lipgloss.NewStyle().Foreground(styles.ColorPrimary)
		ti.TextStyle = lipgloss.NewStyle().Foreground(styles.ColorText)
		return ti
	}

	fields := []editField{
		{label: "Display name", kind: fieldText, input: mk(cc.DisplayName, containerName), hint: "shown in the list instead of the container name"},
		{label: "Root path", kind: fieldText, input: mk(cc.RootPath, "auto (/var/www/html or /app)"), hint: "app root for artisan, logs, storage"},
		{label: "Favorite", kind: fieldToggle, value: cc.Favorite, hint: "pin to the top with a star"},
		{label: "Hidden", kind: fieldToggle, value: cc.Hidden, hint: "hide from the container list"},
	}
	fields[fDisplayName].input.Focus()

	return &ContainerEditScreen{
		containerName: containerName,
		fields:        fields,
		width:         width,
		height:        height,
	}
}

func (s *ContainerEditScreen) Init() tea.Cmd { return textinput.Blink }

func (s *ContainerEditScreen) focusField(i int) {
	for j := range s.fields {
		if s.fields[j].kind == fieldText {
			if j == i {
				s.fields[j].input.Focus()
			} else {
				s.fields[j].input.Blur()
			}
		}
	}
	s.focus = i
}

func (s *ContainerEditScreen) save() tea.Cmd {
	cc := config.ContainerConfig{
		Name:        s.containerName,
		DisplayName: strings.TrimSpace(s.fields[fDisplayName].input.Value()),
		RootPath:    strings.TrimSpace(s.fields[fRootPath].input.Value()),
		Favorite:    s.fields[fFavorite].value,
		Hidden:      s.fields[fHidden].value,
	}
	return func() tea.Msg { return msgs.SaveContainerConfigMsg{Config: cc} }
}

func (s *ContainerEditScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.ContainerConfigSavedMsg:
		if msg.Err != nil {
			s.status = "Error: " + msg.Err.Error()
		} else {
			s.status = "Saved."
		}
		return s, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return s, tea.Quit
		case "esc":
			return s, func() tea.Msg { return msgs.PopMsg{} }
		case "ctrl+s":
			return s, s.save()
		case "up", "shift+tab":
			if s.focus > 0 {
				s.focusField(s.focus - 1)
			}
			return s, nil
		case "down", "tab":
			if s.focus < len(s.fields)-1 {
				s.focusField(s.focus + 1)
			}
			return s, nil
		case " ":
			if s.fields[s.focus].kind == fieldToggle {
				s.fields[s.focus].value = !s.fields[s.focus].value
				s.status = ""
				return s, nil
			}
		case "enter":
			// Toggle flips the value; on a text field, Enter saves.
			if s.fields[s.focus].kind == fieldToggle {
				s.fields[s.focus].value = !s.fields[s.focus].value
				s.status = ""
				return s, nil
			}
			return s, s.save()
		}

	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
		for i := range s.fields {
			if s.fields[i].kind == fieldText {
				s.fields[i].input.Width = max(msg.Width-20, 1)
			}
		}
	}

	// Route key events to the focused text input.
	if s.fields[s.focus].kind == fieldText {
		var cmd tea.Cmd
		s.fields[s.focus].input, cmd = s.fields[s.focus].input.Update(msg)
		s.status = ""
		return s, cmd
	}
	return s, nil
}

func (s *ContainerEditScreen) View() string {
	title := styles.TitleBar.Render("Configure — " + s.containerName)

	labelStyle := lipgloss.NewStyle().Foreground(styles.ColorText).Bold(true)
	hintStyle := lipgloss.NewStyle().Foreground(styles.ColorMuted)
	selStyle := lipgloss.NewStyle().Foreground(styles.ColorPrimary).Bold(true)

	var rows []string
	for i, f := range s.fields {
		marker := "  "
		ls := labelStyle
		if i == s.focus {
			marker = lipgloss.NewStyle().Foreground(styles.ColorPrimary).Render("▸ ")
			ls = selStyle
		}

		var control string
		switch f.kind {
		case fieldText:
			control = f.input.View()
		case fieldToggle:
			if f.value {
				control = lipgloss.NewStyle().Foreground(styles.ColorSuccess).Render("[✓] on")
			} else {
				control = hintStyle.Render("[ ] off")
			}
		}

		row := marker + ls.Render(padRight(f.label, 14)) + "  " + control
		rows = append(rows, row, "    "+hintStyle.Render(f.hint), "")
	}

	body := lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(rows, "\n"))

	if s.status != "" {
		st := lipgloss.NewStyle().Foreground(styles.ColorSuccess)
		if strings.HasPrefix(s.status, "Error") {
			st = lipgloss.NewStyle().Foreground(styles.ColorDanger)
		}
		body = lipgloss.JoinVertical(lipgloss.Left, body, "  "+st.Render(s.status))
	}

	help := styles.StatusBar.Width(s.width).Render(
		styles.StatusBarKey.Render("↑↓/tab") + " move  " +
			styles.StatusBarKey.Render("space") + " toggle  " +
			styles.StatusBarKey.Render("ctrl+s") + " save  " +
			styles.StatusBarKey.Render("esc") + " back",
	)

	content := lipgloss.JoinVertical(lipgloss.Left, title, "", body)
	return styles.PinToBottom(s.height, content, help)
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
