package screens

import (
	"os"
	"strings"

	"github.com/alexbabintsev/laraport/internal/tui/styles"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// formKind is the kind of a form field.
type formKind int

const (
	formText   formKind = iota // free text (textinput)
	formToggle                 // on/off
	formChoice                 // one of choices, cycled with ←/→ or space
	formAction                 // a button: enter emits the field's action
)

// formField is one row of a form.
type formField struct {
	key     string
	label   string
	kind    formKind
	input   textinput.Model
	on      bool
	choices []string
	choice  int
	hint    string
	action  func() tea.Msg   // formAction
	visible func(*form) bool // nil = always visible
}

// form is a vertical list of fields with keyboard navigation, shared by the
// server editor and the settings screen.
type form struct {
	fields []*formField
	focus  int
	width  int
}

func newTextInput(value, placeholder string, width int) textinput.Model {
	ti := textinput.New()
	ti.SetValue(value)
	ti.Placeholder = placeholder
	ti.CharLimit = 512
	ti.Width = max(width-24, 1)
	ti.PromptStyle = lipgloss.NewStyle().Foreground(styles.ColorPrimary)
	ti.TextStyle = lipgloss.NewStyle().Foreground(styles.ColorText)
	return ti
}

func (f *form) add(field *formField) *formField {
	f.fields = append(f.fields, field)
	return field
}

func (f *form) text(key, label, value, placeholder, hint string) *formField {
	return f.add(&formField{key: key, label: label, kind: formText, input: newTextInput(value, placeholder, f.width), hint: hint})
}

func (f *form) toggle(key, label string, on bool, hint string) *formField {
	return f.add(&formField{key: key, label: label, kind: formToggle, on: on, hint: hint})
}

func (f *form) choose(key, label string, choices []string, value, hint string) *formField {
	idx := 0
	for i, c := range choices {
		if c == value {
			idx = i
		}
	}
	return f.add(&formField{key: key, label: label, kind: formChoice, choices: choices, choice: idx, hint: hint})
}

func (f *form) button(key, label, hint string, action func() tea.Msg) *formField {
	return f.add(&formField{key: key, label: label, kind: formAction, hint: hint, action: action})
}

func (f *form) field(key string) *formField {
	for _, fl := range f.fields {
		if fl.key == key {
			return fl
		}
	}
	panic("form: no field " + key)
}

// Value accessors.
func (f *form) str(key string) string      { return strings.TrimSpace(f.field(key).input.Value()) }
func (f *form) isOn(key string) bool       { return f.field(key).on }
func (f *form) selected(key string) string { fl := f.field(key); return fl.choices[fl.choice] }

func (f *form) isVisible(i int) bool {
	fl := f.fields[i]
	return fl.visible == nil || fl.visible(f)
}

// start focuses the first visible field.
func (f *form) start() {
	for i := range f.fields {
		if f.isVisible(i) {
			f.setFocus(i)
			return
		}
	}
}

func (f *form) setFocus(i int) {
	for j, fl := range f.fields {
		if fl.kind == formText {
			if j == i {
				fl.input.Focus()
			} else {
				fl.input.Blur()
			}
		}
	}
	f.focus = i
}

func (f *form) move(delta int) {
	for i := f.focus + delta; i >= 0 && i < len(f.fields); i += delta {
		if f.isVisible(i) {
			f.setFocus(i)
			return
		}
	}
}

func (f *form) resize(width int) {
	f.width = width
	for _, fl := range f.fields {
		if fl.kind == formText {
			fl.input.Width = max(width-24, 1)
		}
	}
}

// update handles navigation and editing keys. handled reports whether the key
// was consumed; cmd may carry a button's action.
func (f *form) update(msg tea.Msg) (cmd tea.Cmd, handled bool) {
	cur := f.fields[f.focus]
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "up", "shift+tab":
			f.move(-1)
			return nil, true
		case "down", "tab":
			f.move(1)
			return nil, true
		case " ", "enter":
			switch cur.kind {
			case formToggle:
				cur.on = !cur.on
				return nil, true
			case formChoice:
				cur.choice = (cur.choice + 1) % len(cur.choices)
				f.revalidateFocus()
				return nil, true
			case formAction:
				if km.String() == "enter" && cur.action != nil {
					return cur.action, true
				}
				return nil, true
			}
		case "left", "right":
			if cur.kind == formChoice {
				d := 1
				if km.String() == "left" {
					d = len(cur.choices) - 1
				}
				cur.choice = (cur.choice + d) % len(cur.choices)
				f.revalidateFocus()
				return nil, true
			}
		}
	}
	if cur.kind == formText {
		var c tea.Cmd
		cur.input, c = cur.input.Update(msg)
		_, isKey := msg.(tea.KeyMsg)
		return c, isKey
	}
	return nil, false
}

// revalidateFocus keeps focus on a visible field after visibility changed.
func (f *form) revalidateFocus() {
	if !f.isVisible(f.focus) {
		f.start()
	}
}

// view renders the visible fields.
func (f *form) view() string {
	labelStyle := lipgloss.NewStyle().Foreground(styles.ColorText).Bold(true)
	hintStyle := lipgloss.NewStyle().Foreground(styles.ColorMuted)
	selStyle := lipgloss.NewStyle().Foreground(styles.ColorPrimary).Bold(true)

	var rows []string
	for i, fl := range f.fields {
		if !f.isVisible(i) {
			continue
		}
		marker := "  "
		ls := labelStyle
		if i == f.focus {
			marker = lipgloss.NewStyle().Foreground(styles.ColorPrimary).Render("▸ ")
			ls = selStyle
		}
		var control string
		switch fl.kind {
		case formText:
			control = fl.input.View()
		case formToggle:
			if fl.on {
				control = lipgloss.NewStyle().Foreground(styles.ColorSuccess).Render("[✓] on")
			} else {
				control = hintStyle.Render("[ ] off")
			}
		case formChoice:
			var parts []string
			for j, c := range fl.choices {
				if j == fl.choice {
					parts = append(parts, lipgloss.NewStyle().Foreground(styles.ColorSuccess).Bold(true).Render("● "+c))
				} else {
					parts = append(parts, hintStyle.Render("○ "+c))
				}
			}
			control = strings.Join(parts, "  ")
		case formAction:
			control = lipgloss.NewStyle().Foreground(styles.ColorAccent).Render("[ enter ]")
		}
		rows = append(rows, marker+ls.Render(padRight(fl.label, 20))+"  "+control)
		if fl.hint != "" {
			rows = append(rows, "    "+hintStyle.Render(fl.hint))
		}
		rows = append(rows, "")
	}
	return strings.Join(rows, "\n")
}

// statusLine renders a save/test status message (red for errors).
func statusLine(status string, isErr bool) string {
	if status == "" {
		return ""
	}
	st := lipgloss.NewStyle().Foreground(styles.ColorSuccess)
	if isErr {
		st = lipgloss.NewStyle().Foreground(styles.ColorDanger)
	}
	return "  " + st.Render(status)
}

// homeDir returns the user's home directory ("" if unknown).
func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}
