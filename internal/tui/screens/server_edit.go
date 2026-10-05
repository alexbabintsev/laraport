package screens

import (
	"strconv"

	"github.com/alexbabintsev/laradok/internal/config"
	"github.com/alexbabintsev/laradok/internal/msgs"
	"github.com/alexbabintsev/laradok/internal/tui/styles"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ServerEditScreen adds or edits a server in the config.
type ServerEditScreen struct {
	original string // "" when adding
	form     form
	status   string
	isErr    bool
	testing  bool
	sp       spinner.Model
	width    int
	height   int
}

// NewServerEditScreen opens the form for srv; original is the server's
// current name, or "" for a new server.
func NewServerEditScreen(original string, srv config.Server, width, height int) *ServerEditScreen {
	s := &ServerEditScreen{original: original, width: width, height: height}
	s.form.width = width
	sshOnly := func(f *form) bool { return f.selected("type") == string(config.ServerTypeSSH) }

	typ := string(srv.Type)
	if typ == "" {
		typ = string(config.ServerTypeSSH)
		if original != "" {
			typ = string(config.ServerTypeLocal)
		}
	}
	port := ""
	if srv.Port != 0 {
		port = strconv.Itoa(srv.Port)
	}

	s.form.text("name", "Name", srv.Name, "e.g. Production", "shown in the server list")
	s.form.choose("type", "Type", []string{string(config.ServerTypeSSH), string(config.ServerTypeLocal)}, typ,
		"ssh: remote server · local: this machine's Docker")
	s.form.text("host", "Host", srv.Host, "example.com or 10.0.0.5", "").visible = sshOnly
	s.form.text("port", "Port", port, "22", "").visible = sshOnly
	s.form.text("user", "User", srv.User, "deploy", "").visible = sshOnly
	s.form.text("key", "Key", collapseHome(srv.Key), "~/.ssh/id_ed25519 (empty = ssh-agent / default keys)",
		"passphrase-protected keys: load them into ssh-agent").visible = sshOnly
	s.form.text("docker_cmd", "Docker command", srv.DockerCmd, "docker",
		`e.g. "sudo -n docker" if your user is not in the docker group, or "podman"`)
	s.form.text("root_path", "Default root path", srv.RootPath, "auto (/var/www/html or /app)",
		"app root for containers on this server without their own root_path")
	s.form.start()

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styles.SpinnerStyle
	s.sp = sp
	return s
}

func (s *ServerEditScreen) Init() tea.Cmd { return textinput.Blink }

// server builds a config.Server from the form; ok is false (with a status
// message set) when a field cannot be parsed.
func (s *ServerEditScreen) server() (config.Server, bool) {
	srv := config.Server{
		Name:      s.form.str("name"),
		Type:      config.ServerType(s.form.selected("type")),
		DockerCmd: s.form.str("docker_cmd"),
		RootPath:  s.form.str("root_path"),
	}
	if srv.Type == config.ServerTypeSSH {
		srv.Host = s.form.str("host")
		srv.User = s.form.str("user")
		srv.Key = s.form.str("key")
		if p := s.form.str("port"); p != "" {
			n, err := strconv.Atoi(p)
			if err != nil {
				s.status, s.isErr = "port must be a number", true
				return srv, false
			}
			srv.Port = n
		} else {
			srv.Port = 22
		}
	}
	if err := srv.Validate(); err != nil {
		s.status, s.isErr = err.Error(), true
		return srv, false
	}
	return srv, true
}

func (s *ServerEditScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.ServerSavedMsg:
		if msg.Err != nil {
			s.status, s.isErr = msg.Err.Error(), true
			return s, nil
		}
		return s, func() tea.Msg { return msgs.PopMsg{} }

	case msgs.ConnectionTestedMsg:
		s.testing = false
		if msg.Err != nil {
			s.status, s.isErr = "Connection failed: "+msg.Err.Error(), true
		} else {
			s.status, s.isErr = "Connection OK — "+msg.Result, false
		}
		return s, nil

	case spinner.TickMsg:
		if s.testing {
			var cmd tea.Cmd
			s.sp, cmd = s.sp.Update(msg)
			return s, cmd
		}
		return s, nil

	case tea.WindowSizeMsg:
		s.width, s.height = msg.Width, msg.Height
		s.form.resize(msg.Width)
		return s, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return s, tea.Quit
		case "esc":
			return s, func() tea.Msg { return msgs.PopMsg{} }
		case "ctrl+s":
			return s, s.save()
		case "ctrl+t":
			srv, ok := s.server()
			if !ok || s.testing {
				return s, nil
			}
			s.testing = true
			s.status, s.isErr = "Testing connection…", false
			return s, tea.Batch(s.sp.Tick, func() tea.Msg { return msgs.TestConnectionMsg{Server: srv} })
		case "enter":
			if s.form.fields[s.form.focus].kind == formText {
				return s, s.save()
			}
		}
		s.status = ""
	}
	cmd, _ := s.form.update(msg)
	return s, cmd
}

func (s *ServerEditScreen) save() tea.Cmd {
	srv, ok := s.server()
	if !ok {
		return nil
	}
	original := s.original
	return func() tea.Msg { return msgs.SaveServerMsg{Original: original, Server: srv} }
}

func (s *ServerEditScreen) View() string {
	heading := "Add server"
	if s.original != "" {
		heading = "Edit server — " + s.original
	}
	title := styles.TitleBar.Render(heading)
	body := lipgloss.NewStyle().Padding(1, 2).Render(s.form.view())
	status := statusLine(s.status, s.isErr)
	if s.testing {
		status = "  " + s.sp.View() + " " + s.status
	}
	help := styles.StatusBar.Width(s.width).Render(
		styles.StatusBarKey.Render("↑↓/tab") + " move  " +
			styles.StatusBarKey.Render("←→/space") + " choose  " +
			styles.StatusBarKey.Render("ctrl+t") + " test connection  " +
			styles.StatusBarKey.Render("ctrl+s") + " save  " +
			styles.StatusBarKey.Render("esc") + " cancel",
	)
	content := lipgloss.JoinVertical(lipgloss.Left, title, "", body, lipgloss.NewStyle().Width(max(s.width-4, 1)).Render(status))
	return styles.PinToBottom(s.height, content, help)
}

// collapseHome shows a path under the home directory as ~/….
func collapseHome(p string) string {
	if home := homeDir(); home != "" && len(p) > len(home) && p[:len(home)] == home && p[len(home)] == '/' {
		return "~" + p[len(home):]
	}
	return p
}
