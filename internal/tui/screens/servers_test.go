package screens

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexbabintsev/laraport/internal/config"
	"github.com/alexbabintsev/laraport/internal/msgs"
	tea "github.com/charmbracelet/bubbletea"
)

// focusField moves the form focus to the (visible) field with key, using the
// up/down keys like a user would.
func focusField(t *testing.T, m tea.Model, f *form, key string) tea.Model {
	t.Helper()
	target := -1
	for i, fl := range f.fields {
		if fl.key == key {
			target = i
		}
	}
	if target < 0 || !f.isVisible(target) {
		t.Fatalf("field %s not reachable", key)
	}
	for i := 0; i < 2*len(f.fields) && f.focus != target; i++ {
		dir := "down"
		if f.focus > target {
			dir = "up"
		}
		m, _ = m.Update(key2(dir))
	}
	if f.focus != target {
		t.Fatalf("field %s not reachable", key)
	}
	return m
}

func key2(s string) tea.KeyMsg {
	if s == "ctrl+t" {
		return tea.KeyMsg{Type: tea.KeyCtrlT}
	}
	return key(s)
}

func TestServerEditAddSSH(t *testing.T) {
	s := NewServerEditScreen("", config.Server{}, w, 60)
	var m tea.Model = s
	m = typeText(m, "Production")
	m = focusField(t, m, &s.form, "host")
	m = typeText(m, "prod.example.com")
	m = focusField(t, m, &s.form, "user")
	m = typeText(m, "deploy")
	m = focusField(t, m, &s.form, "docker_cmd")
	m = typeText(m, "sudo -n docker")
	_, cmd := m.Update(key("ctrl+s"))
	got := first[msgs.SaveServerMsg](t, cmd)
	srv := got.Server
	if got.Original != "" || srv.Name != "Production" || srv.Type != config.ServerTypeSSH || srv.Host != "prod.example.com" ||
		srv.Port != 22 || srv.User != "deploy" || srv.DockerCmd != "sudo -n docker" {
		t.Fatalf("saved %+v", got)
	}
	// Saved: the form closes.
	_, cmd = m.Update(msgs.ServerSavedMsg{})
	first[msgs.PopMsg](t, cmd)
}

func TestServerEditValidationAndLocal(t *testing.T) {
	s := NewServerEditScreen("", config.Server{}, w, 60)
	var m tea.Model = s
	m = typeText(m, "x")
	_, cmd := m.Update(key("ctrl+s"))
	if cmd != nil && len(msgsOf(cmd)) != 0 {
		t.Fatal("invalid server submitted")
	}
	if !strings.Contains(m.View(), "host is required") {
		t.Fatalf("validation message missing:\n%s", m.View())
	}
	// Port must be numeric.
	m = focusField(t, m, &s.form, "port")
	m = typeText(m, "abc")
	m.Update(key("ctrl+s"))
	if !strings.Contains(m.View(), "port must be a number") {
		t.Fatal("port error missing")
	}
	// Switching the type to local hides the SSH fields and saves without them.
	m = focusField(t, m, &s.form, "type")
	m, _ = m.Update(key("right"))
	if strings.Contains(m.View(), "Host") {
		t.Fatal("SSH fields shown for a local server")
	}
	_, cmd = m.Update(key("ctrl+s"))
	if got := first[msgs.SaveServerMsg](t, cmd); got.Server.Type != config.ServerTypeLocal || got.Server.Host != "" {
		t.Fatalf("saved %+v", got.Server)
	}
	// A save error from the App is shown.
	m, _ = m.Update(msgs.ServerSavedMsg{Err: errors.New("disk full")})
	if !strings.Contains(m.View(), "disk full") {
		t.Fatal("save error not shown")
	}
}

func TestServerEditExistingAndTest(t *testing.T) {
	home := homeDir()
	keyPath := filepath.Join(home, ".ssh", "laraport-test-key")
	srv := config.Server{Name: "prod", Type: config.ServerTypeSSH, Host: "h", Port: 2222, User: "u", Key: keyPath, RootPath: "/app"}
	s := NewServerEditScreen("prod", srv, w, 60)
	if v := s.View(); !strings.Contains(v, "Edit server — prod") || !strings.Contains(v, "2222") || !strings.Contains(v, "~/.ssh/laraport-test-key") {
		t.Fatalf("existing values not shown:\n%s", v)
	}
	// Test connection needs a valid server: the key file must exist.
	os.MkdirAll(filepath.Dir(keyPath), 0o700)
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		os.WriteFile(keyPath, nil, 0o600)
		defer os.Remove(keyPath)
	}
	var m tea.Model = s
	_, cmd := m.Update(key2("ctrl+t"))
	got := first[msgs.TestConnectionMsg](t, cmd)
	if got.Server.Port != 2222 || got.Server.RootPath != "/app" {
		t.Fatalf("test %+v", got)
	}
	m, _ = m.Update(msgs.ConnectionTestedMsg{Result: "Docker 27.1"})
	if !strings.Contains(m.View(), "Connection OK — Docker 27.1") {
		t.Fatal("test result not shown")
	}
	m, _ = m.Update(msgs.ConnectionTestedMsg{Err: errors.New("permission denied")})
	if !strings.Contains(m.View(), "Connection failed: permission denied") {
		t.Fatal("test failure not shown")
	}
	_, cmd = m.Update(key("ctrl+s"))
	if got := first[msgs.SaveServerMsg](t, cmd); got.Original != "prod" {
		t.Fatalf("original = %q", got.Original)
	}
}

func TestServerListManagementKeys(t *testing.T) {
	servers := []config.Server{
		{Name: "prod", Type: config.ServerTypeSSH, Host: "h", Port: 22, User: "u", DockerCmd: "sudo docker",
			Containers: []config.ContainerConfig{{Name: "web"}, {Name: "db"}}},
	}
	var m tea.Model = NewServerListScreen(servers, w, 40)
	if !strings.Contains(m.View(), "sudo docker") {
		t.Fatal("docker command not shown")
	}
	_, cmd := m.Update(key("a"))
	if got := first[msgs.PushServerEditMsg](t, cmd); got.Original != "" {
		t.Fatalf("add = %+v", got)
	}
	_, cmd = m.Update(key("e"))
	if got := first[msgs.PushServerEditMsg](t, cmd); got.Original != "prod" || got.Server.Host != "h" {
		t.Fatalf("edit = %+v", got)
	}
	_, cmd = m.Update(key("d"))
	conf := first[msgs.PushConfirmMsg](t, cmd)
	if del, ok := conf.Then.(msgs.DeleteServerMsg); !ok || del.Name != "prod" || !strings.Contains(conf.Detail, "2 container setting") {
		t.Fatalf("confirm = %+v", conf)
	}
	_, cmd = m.Update(key("s"))
	first[msgs.PushSettingsMsg](t, cmd)

	m, _ = m.Update(msgs.ServerListChangedMsg{Servers: append(servers, config.Server{Name: "new", Type: config.ServerTypeLocal}), Status: "Added new."})
	if v := m.View(); !strings.Contains(v, "new") || !strings.Contains(v, "Added new.") {
		t.Fatalf("refresh:\n%s", v)
	}
	m, _ = m.Update(msgs.ServerListChangedMsg{Err: errors.New("cannot write")})
	if !strings.Contains(m.View(), "cannot write") {
		t.Fatal("error not shown")
	}
}

func TestServerListImplicitLocal(t *testing.T) {
	cfg, _ := config.Load(filepath.Join(t.TempDir(), "none.yaml"))
	var m tea.Model = NewServerListScreen(cfg.Servers, w, 40)
	if !strings.Contains(m.View(), "press a to add") {
		t.Fatal("first-run hint missing")
	}
	// The implicit Local server cannot be deleted.
	if _, cmd := m.Update(key("d")); cmd != nil && len(msgsOf(cmd)) != 0 {
		t.Fatal("implicit Local offered for deletion")
	}
}

func TestConfirmActionScreen(t *testing.T) {
	s := NewConfirmActionScreen("Delete?", "Remove it.", msgs.DeleteServerMsg{Name: "x"}, w, h)
	if strings.Contains(s.View(), "$ Remove") {
		t.Fatal("action shown as a command")
	}
	_, cmd := s.Update(key("y"))
	got := first[msgs.ConfirmedMsg](t, cmd)
	if d, ok := got.Then.(msgs.DeleteServerMsg); !ok || d.Name != "x" {
		t.Fatalf("confirmed = %+v", got)
	}
}

func TestSettingsScreen(t *testing.T) {
	st := config.Settings{HostKeyCheck: config.HostKeyStrict, StatsInterval: 5, HideStopped: true}
	s := NewSettingsScreen(st, w, 80)
	var m tea.Model = s
	if v := m.View(); !strings.Contains(v, "● strict") || !strings.Contains(v, "SQL history size") {
		t.Fatalf("initial view:\n%s", v)
	}
	// Turn wrapping on, turn SQL history off (hides its size field).
	m = focusField(t, m, &s.form, "wrap_logs")
	m, _ = m.Update(key(" "))
	m = focusField(t, m, &s.form, "sql_history")
	m, _ = m.Update(key(" "))
	if strings.Contains(m.View(), "SQL history size") {
		t.Fatal("size shown while history is off")
	}
	_, cmd := m.Update(key("ctrl+s"))
	got := first[msgs.SaveSettingsMsg](t, cmd).Settings
	want := config.Settings{HostKeyCheck: config.HostKeyStrict, StatsInterval: 5, HideStopped: true, WrapLogs: true, NoSQLHistory: true}
	if got != want {
		t.Fatalf("saved %+v, want %+v", got, want)
	}
	m, _ = m.Update(msgs.SettingsSavedMsg{})
	if !strings.Contains(m.View(), "Saved.") {
		t.Fatal("saved status missing")
	}

	// Invalid numbers are rejected in the form.
	m = focusField(t, m, &s.form, "stats_interval")
	s.form.field("stats_interval").input.SetValue("0")
	if _, cmd := m.Update(key("ctrl+s")); cmd != nil && len(msgsOf(cmd)) != 0 {
		t.Fatal("invalid interval submitted")
	}
	if !strings.Contains(m.View(), "between 1 and 60") {
		t.Fatal("interval error missing")
	}

	// Clear history button.
	m = focusField(t, m, &s.form, "clear_history")
	_, cmd = m.Update(key("enter"))
	first[msgs.ClearSQLHistoryMsg](t, cmd)
	m, _ = m.Update(msgs.SQLHistoryClearedMsg{})
	if !strings.Contains(m.View(), "SQL history cleared.") {
		t.Fatal("cleared status missing")
	}
}

func TestSettingsAcceptNewStaysImplicit(t *testing.T) {
	s := NewSettingsScreen(config.Settings{}, w, 80)
	_, cmd := s.Update(key("ctrl+s"))
	if got := first[msgs.SaveSettingsMsg](t, cmd).Settings; got != (config.Settings{}) {
		t.Fatalf("defaults saved as %+v", got)
	}
}

func TestFormChoiceKeys(t *testing.T) {
	var f form
	f.width = 80
	f.choose("c", "C", []string{"a", "b", "c"}, "b", "")
	f.start()
	f.update(key("right"))
	if f.selected("c") != "c" {
		t.Fatal("right")
	}
	f.update(key("right"))
	if f.selected("c") != "a" {
		t.Fatal("wrap around")
	}
	f.update(key("left"))
	if f.selected("c") != "c" {
		t.Fatal("left")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("unknown field did not panic")
		}
	}()
	f.field("nope")
}

func TestStatsIntervalAndWrapSetters(t *testing.T) {
	st := NewStatsScreen("web", w, h)
	st.SetInterval(0) // ignored
	if st.interval != defaultStatsInterval {
		t.Fatal("zero interval accepted")
	}
	st.SetInterval(7e9)
	if st.interval.Seconds() != 7 {
		t.Fatal("interval not set")
	}
	o := NewOutputScreen("o", w, h)
	o.SetWrap(true)
	if !o.wrap {
		t.Fatal("wrap not set")
	}
	l := NewLogTailScreen("l", w, h)
	l.SetWrap(true)
	if !l.wrap {
		t.Fatal("log wrap not set")
	}
	cl := NewContainerListScreen(config.Server{}, nil, w, h)
	if !cl.showStopped {
		t.Fatal("stopped containers hidden by default")
	}
	cl.SetShowStopped(false)
	if cl.showStopped {
		t.Fatal("SetShowStopped")
	}
}

func TestServerEditJumpFields(t *testing.T) {
	s := NewServerEditScreen("", config.Server{}, w, 80)
	var m tea.Model = s
	if strings.Contains(m.View(), "Jump key") {
		t.Fatal("jump key shown without a jump host")
	}
	m = typeText(m, "prod")
	m = focusField(t, m, &s.form, "host")
	m = typeText(m, "10.0.0.5")
	m = focusField(t, m, &s.form, "user")
	m = typeText(m, "deploy")
	m = focusField(t, m, &s.form, "jump_host")
	m = typeText(m, "ops@bastion:2222")
	if !strings.Contains(m.View(), "Jump key") {
		t.Fatal("jump key hidden although a jump host is set")
	}
	_, cmd := m.Update(key("ctrl+s"))
	got := first[msgs.SaveServerMsg](t, cmd).Server
	if got.JumpHost != "ops@bastion:2222" || got.JumpKey != "" {
		t.Fatalf("saved %+v", got)
	}
	// Invalid jump host is reported by the form.
	s.form.field("jump_host").input.SetValue("a,b")
	m.Update(key("ctrl+s"))
	if !strings.Contains(m.View(), "only one jump host") {
		t.Fatal("jump host error not shown")
	}
}

func TestServerListShowsJumpHost(t *testing.T) {
	m := NewServerListScreen([]config.Server{{Name: "p", Type: config.ServerTypeSSH, Host: "h", Port: 22, User: "u", JumpHost: "ops@bastion"}}, w, 40)
	if !strings.Contains(m.View(), "via ops@bastion") {
		t.Fatal("jump host not shown")
	}
}
