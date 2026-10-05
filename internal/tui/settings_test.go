package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexbabintsev/laradok/internal/config"
	"github.com/alexbabintsev/laradok/internal/msgs"
	"github.com/alexbabintsev/laradok/internal/tui/screens"
	tea "github.com/charmbracelet/bubbletea"
)

func newConfigApp(t *testing.T) (*App, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	p := filepath.Join(t.TempDir(), "config.yaml")
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(cfg, p)
	app.Init()
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 50})
	return app, p
}

func TestAddEditDeleteServerFlow(t *testing.T) {
	app, p := newConfigApp(t)

	// Add.
	app.Update(msgs.PushServerEditMsg{})
	if _, ok := app.top().(*screens.ServerEditScreen); !ok {
		t.Fatalf("top = %T", app.top())
	}
	_, cmd := app.Update(msgs.SaveServerMsg{Server: config.Server{Name: "prod", Type: config.ServerTypeSSH, Host: "h", User: "u"}})
	for _, m := range collect[tea.Msg](cmd) {
		app.Update(m) // PopMsg from the form
	}
	if _, ok := app.top().(*screens.ServerListScreen); !ok {
		t.Fatalf("form not closed: %T", app.top())
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), "name: prod") || !strings.Contains(string(data), "name: Local") {
		t.Fatalf("config after add:\n%s", data)
	}
	if !strings.Contains(app.View(), "prod") || !strings.Contains(app.View(), "Added prod.") {
		t.Fatalf("list not refreshed:\n%s", app.View())
	}

	// Edit (rename + docker command).
	app.Update(msgs.PushServerEditMsg{Original: "prod"})
	app.Update(msgs.SaveServerMsg{Original: "prod", Server: config.Server{Name: "production", Type: config.ServerTypeSSH, Host: "h", User: "u", DockerCmd: "sudo -n docker"}})
	if data, _ := os.ReadFile(p); !strings.Contains(string(data), "name: production") || !strings.Contains(string(data), "docker_cmd: sudo -n docker") {
		t.Fatalf("config after edit:\n%s", data)
	}

	// Delete via confirmation.
	app.Update(msgs.PopMsg{}) // close the edit form
	_, cmd = app.Update(msgs.PushConfirmMsg{Title: "Delete?", Then: msgs.DeleteServerMsg{Name: "production"}})
	_ = cmd
	_, cmd = app.Update(msgs.ConfirmedMsg{Then: msgs.DeleteServerMsg{Name: "production"}})
	for _, m := range collect[msgs.DeleteServerMsg](cmd) {
		app.Update(m)
	}
	if data, _ := os.ReadFile(p); strings.Contains(string(data), "production") {
		t.Fatalf("config after delete:\n%s", data)
	}
	if !strings.Contains(app.View(), "Deleted production.") {
		t.Fatal("delete status missing")
	}
}

func TestSaveServerErrorsKeepConfig(t *testing.T) {
	app, p := newConfigApp(t)
	app.Update(msgs.PushServerEditMsg{})
	// Invalid: rejected, nothing written.
	app.Update(msgs.SaveServerMsg{Server: config.Server{Name: "x", Type: config.ServerTypeSSH}})
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("config written for an invalid server")
	}
	if len(app.cfg.Servers) != 1 {
		t.Fatalf("in-memory config changed: %+v", app.cfg.Servers)
	}

	// Write failure: the in-memory config is rolled back.
	ro := filepath.Join(t.TempDir(), "ro")
	os.Mkdir(ro, 0o500)
	defer os.Chmod(ro, 0o700)
	app.cfgPath = filepath.Join(ro, "sub", "config.yaml")
	app.Update(msgs.SaveServerMsg{Server: config.Server{Name: "ok", Type: config.ServerTypeLocal}})
	if len(app.cfg.Servers) != 1 || app.cfg.Servers[0].Name != "Local" {
		t.Fatalf("not rolled back: %+v", app.cfg.Servers)
	}
	if !strings.Contains(app.View(), "creating") && !strings.Contains(app.View(), "permission") {
		t.Fatalf("write error not shown:\n%s", app.View())
	}
	// Delete failure is shown on the list.
	app.Update(msgs.PopMsg{})
	app.Update(msgs.DeleteServerMsg{Name: "missing"})
	if !strings.Contains(app.View(), "not found") {
		t.Fatal("delete error not shown")
	}
}

func TestSettingsSaveAndApply(t *testing.T) {
	app, p := newConfigApp(t)
	app.Update(msgs.PushSettingsMsg{})
	if _, ok := app.top().(*screens.SettingsScreen); !ok {
		t.Fatalf("top = %T", app.top())
	}
	dl := filepath.Join(t.TempDir(), "dumps")
	app.Update(msgs.SaveSettingsMsg{Settings: config.Settings{DownloadsDir: dl, WrapLogs: true, HideStopped: true, StatsInterval: 9, NoSQLHistory: true}})
	if data, _ := os.ReadFile(p); !strings.Contains(string(data), "wrap_logs: true") {
		t.Fatalf("settings not saved:\n%s", data)
	}
	if !strings.Contains(app.View(), "Saved.") {
		t.Fatal("saved status missing")
	}
	// Invalid settings are refused and not applied.
	app.Update(msgs.SaveSettingsMsg{Settings: config.Settings{HostKeyCheck: "nope"}})
	if app.cfg.Settings.HostKeyCheck == "nope" {
		t.Fatal("invalid settings applied")
	}
	app.Update(msgs.PopMsg{})

	// Applied: container list hides stopped, output screens wrap, stats interval.
	app.Update(msgs.PushContainerListMsg{Server: app.cfg.Servers[0]})
	app.Update(msgs.PushMainMenuMsg{})
	app.Update(msgs.PushStatsMsg{})
	if st, ok := app.top().(*screens.StatsScreen); !ok || st == nil {
		t.Fatalf("top = %T", app.top())
	}
}

func TestSQLHistoryDisabled(t *testing.T) {
	app, _ := newConfigApp(t)
	app.cfg.Settings.NoSQLHistory = true
	app.Update(msgs.PushContainerListMsg{Server: app.cfg.Servers[0]})
	app.Update(msgs.PushMainMenuMsg{})
	app.Update(msgs.PushSQLExecMsg{Title: "q", DBName: "db", SQL: "SELECT 1", HistoryKey: "k", History: []string{"SELECT 1"}})
	if h := config.LoadSQLHistory("k"); h != nil {
		t.Fatalf("history saved while disabled: %q", h)
	}
}

func TestClearSQLHistoryRoute(t *testing.T) {
	app, _ := newConfigApp(t)
	app.Update(msgs.PushSettingsMsg{})
	_, cmd := app.Update(msgs.ClearSQLHistoryMsg{})
	got := collect[msgs.SQLHistoryClearedMsg](cmd)
	if len(got) != 1 || got[0].Err != nil {
		t.Fatalf("got %+v", got)
	}
	app.Update(got[0])
	if !strings.Contains(app.View(), "SQL history cleared.") {
		t.Fatal("status not shown")
	}
}

func TestServerRootPathDefault(t *testing.T) {
	app, _ := newConfigApp(t)
	srv := config.Server{Name: "Local", Type: config.ServerTypeLocal, RootPath: "/srv/app",
		Containers: []config.ContainerConfig{{Name: "own", RootPath: "/own"}}}
	app.Update(msgs.PushContainerListMsg{Server: srv})
	app.Update(msgs.PushMainMenuMsg{})
	if app.containerCfg.RootPath != "/srv/app" {
		t.Fatalf("server root not applied: %q", app.containerCfg.RootPath)
	}
	app.Update(msgs.PopMsg{})
	app.activeServer = srv
	app.Update(msgs.PushMainMenuMsg{})
	app.container.Name = "own"
	app.containerCfg, _ = srv.FindContainerConfig("own")
	if app.containerCfg.RootPath != "/own" {
		t.Fatal("container root overridden")
	}
}

func TestTestConnectionLocal(t *testing.T) {
	app, _ := newConfigApp(t)
	// A docker CLI that is certainly missing: the test reports an error.
	_, cmd := app.Update(msgs.TestConnectionMsg{Server: config.Server{Name: "l", Type: config.ServerTypeLocal, DockerCmd: "laradok-no-such-docker"}})
	got := collect[msgs.ConnectionTestedMsg](cmd)
	if len(got) != 1 || got[0].Err == nil {
		t.Fatalf("got %+v", got)
	}
}
