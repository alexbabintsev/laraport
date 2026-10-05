package tui

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alexbabintsev/laraport/internal/config"
	"github.com/alexbabintsev/laraport/internal/docker"
	"github.com/alexbabintsev/laraport/internal/msgs"
	"github.com/alexbabintsev/laraport/internal/tui/screens"
	tea "github.com/charmbracelet/bubbletea"
)

// TestPushRoutes checks that every navigation message opens the expected
// screen and kicks off the expected background work.
func TestPushRoutes(t *testing.T) {
	cases := []struct {
		msg      tea.Msg
		screen   any     // pointer to the expected top screen type
		produces tea.Msg // a message type the returned command must produce (nil = none checked)
	}{
		{msgs.PushInfoMsg{}, (*screens.InfoScreen)(nil), msgs.ContainerInfoLoadedMsg{}},
		{msgs.PushStatsMsg{}, (*screens.StatsScreen)(nil), msgs.StatsSampleMsg{}},
		{msgs.PushDockerCmdMsg{}, (*screens.DockerCmdScreen)(nil), nil},
		{msgs.PushGlobalCmdMsg{}, (*screens.GlobalCmdScreen)(nil), nil},
		{msgs.PushConfirmMsg{Title: "sure?"}, (*screens.ConfirmScreen)(nil), nil},
		{msgs.PushRedisCmdMsg{}, (*screens.RedisCmdScreen)(nil), msgs.RedisReadyMsg{}},
		{msgs.PushMongoCmdMsg{MongoBin: "mongosh"}, (*screens.MongoCmdScreen)(nil), msgs.MongoReadyMsg{}},
		{msgs.PushArtisanCmdMsg{}, (*screens.AutocompleteScreen)(nil), msgs.ArtisanCommandsLoadedMsg{}},
		{msgs.PushComposerCmdMsg{}, (*screens.AutocompleteScreen)(nil), msgs.ComposerCommandsLoadedMsg{}},
		{msgs.PushNpmCmdMsg{}, (*screens.AutocompleteScreen)(nil), msgs.NpmCommandsLoadedMsg{}},
		{msgs.PushCustomCmdMsg{}, (*screens.RawCmdScreen)(nil), nil},
		{msgs.PushLogFilePickerMsg{}, (*screens.LogFilePickerScreen)(nil), msgs.LogFilesLoadedMsg{}},
		{msgs.PushServerLogPickerMsg{}, (*screens.ServerLogPickerScreen)(nil), msgs.HostLogsDiscoveredMsg{}},
		{msgs.PushDBScreenMsg{Engine: docker.EnginePostgres}, (*screens.DBListScreen)(nil), msgs.DBCredsLoadedMsg{}},
		{msgs.PushDBScreenMsg{Engine: docker.EngineMySQL}, (*screens.DBListScreen)(nil), msgs.DBCredsLoadedMsg{}},
		{msgs.PushDBScreenMsg{Engine: docker.EngineSQLite}, (*screens.DBListScreen)(nil), msgs.DBListLoadedMsg{}},
		{msgs.PushDBActionsMsg{DBName: "db", Engine: docker.EnginePostgres}, (*screens.DBActionsScreen)(nil), nil},
		{msgs.PushFileBrowserMsg{}, (*screens.FileBrowserScreen)(nil), nil},
		{msgs.PushCommandsMsg{}, (*screens.CommandsScreen)(nil), nil},
		{msgs.PushContainerEditMsg{ContainerName: "web"}, (*screens.ContainerEditScreen)(nil), nil},
		{msgs.PushDBDownloadMsg{DBName: "db", Engine: docker.EnginePostgres}, (*screens.OutputScreen)(nil), msgs.StreamStartedMsg{}},
		{msgs.PushDBDownloadMsg{DBName: "db", Engine: docker.EngineMariaDB}, (*screens.OutputScreen)(nil), msgs.StreamStartedMsg{}},
		{msgs.PushDBDownloadMsg{DBName: "/d.db", Engine: docker.EngineSQLite}, (*screens.OutputScreen)(nil), msgs.StreamStartedMsg{}},
		{msgs.PushDBDownloadMsg{DBName: "db", CustomFormat: true}, (*screens.OutputScreen)(nil), msgs.StreamStartedMsg{}},
		{msgs.PushDBDownloadMsg{DBName: "db", Inserts: true}, (*screens.OutputScreen)(nil), msgs.StreamStartedMsg{}},
		{msgs.PushRedisDumpMsg{}, (*screens.OutputScreen)(nil), msgs.StreamStartedMsg{}},
		{msgs.PushMongoDumpMsg{User: "u"}, (*screens.OutputScreen)(nil), msgs.StreamStartedMsg{}},
		{msgs.PushStorageDownloadMsg{}, (*screens.OutputScreen)(nil), msgs.StreamStartedMsg{}},
		{msgs.PushPathDownloadMsg{Path: "/x"}, (*screens.OutputScreen)(nil), msgs.StreamStartedMsg{}},
		{msgs.PushLogTailMsg{LogType: "docker"}, (*screens.LogTailScreen)(nil), msgs.StreamStartedMsg{}},
		{msgs.PushLogTailMsg{FilePath: "/var/log/x.log"}, (*screens.LogTailScreen)(nil), msgs.StreamStartedMsg{}},
		{msgs.PushSQLExecMsg{Title: "q", DBName: "db", Engine: docker.EnginePostgres, SQL: "SELECT 1", HistoryKey: "k"}, (*screens.OutputScreen)(nil), msgs.StreamStartedMsg{}},
	}
	// SQL exec persists history; keep it out of the real home.
	t.Setenv("HOME", t.TempDir())

	for _, c := range cases {
		name := reflect.TypeOf(c.msg).Name()
		t.Run(name, func(t *testing.T) {
			app, _ := newTestApp(t)
			_, cmd := app.Update(c.msg)
			if got, want := reflect.TypeOf(app.top()), reflect.TypeOf(c.screen); got != want {
				t.Fatalf("top screen = %v, want %v", got, want)
			}
			if app.View() == "" {
				t.Error("empty view")
			}
			if c.produces == nil {
				return
			}
			found := false
			for _, m := range collect[tea.Msg](cmd) {
				if reflect.TypeOf(m) == reflect.TypeOf(c.produces) {
					found = true
					// Feed it back: the screen must handle it without panicking.
					app.Update(m)
				}
			}
			if !found {
				t.Fatalf("command did not produce %T", c.produces)
			}
		})
	}
}

func TestPushSQLInputLoadsHistory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app, _ := newTestApp(t)
	app.Update(msgs.PushSQLInputMsg{DBName: "db", HistoryKey: "k"})
	if _, ok := app.top().(*screens.SQLInputScreen); !ok {
		t.Fatalf("top = %T", app.top())
	}
}

func TestConfirmedRunsCommand(t *testing.T) {
	app, _ := newTestApp(t)
	run := msgs.PushOutputMsg{Title: "prune", Host: docker.HostCommand{Cmd: "docker system prune -f"}}
	app.Update(msgs.PushConfirmMsg{Title: "sure?", Run: run})
	depth := len(app.stack)
	_, cmd := app.Update(msgs.ConfirmedMsg{Run: run})
	if len(app.stack) != depth-1 {
		t.Fatal("confirm screen not popped")
	}
	if got := collect[msgs.PushOutputMsg](cmd); len(got) != 1 || got[0].Host.Cmd != run.Host.Cmd {
		t.Fatalf("got %+v", got)
	}
}

func TestStatsTickOnlyWhileOnStatsScreen(t *testing.T) {
	app, _ := newTestApp(t)
	if _, cmd := app.Update(msgs.StatsTickMsg{}); cmd != nil {
		t.Fatal("polling continued off the stats screen")
	}
	app.Update(msgs.PushStatsMsg{})
	if _, cmd := app.Update(msgs.StatsTickMsg{}); cmd == nil {
		t.Fatal("no poll on the stats screen")
	}
}

func TestCapsAdoptDetectedRoot(t *testing.T) {
	app, _ := newTestApp(t)
	app.Update(msgs.ContainerCapsLoadedMsg{Caps: docker.ContainerCaps{HasLaravel: true, LaravelRoot: "/app"}})
	if app.containerCfg.RootPath != "/app" {
		t.Fatalf("root = %q", app.containerCfg.RootPath)
	}
	app.containerCfg.RootPath = "/configured"
	app.Update(msgs.ContainerCapsLoadedMsg{Caps: docker.ContainerCaps{HasLaravel: true, LaravelRoot: "/app"}})
	if app.containerCfg.RootPath != "/configured" {
		t.Fatal("configured root overridden")
	}
}

func TestLoadDirUsesCapturedContainer(t *testing.T) {
	app, r := newTestApp(t)
	_, cmd := app.Update(msgs.LoadDirMsg{Path: "/var"})
	app.container.ID = "changed"
	got := collect[msgs.DirLoadedMsg](cmd)
	if len(got) != 1 || got[0].Path != "/var" {
		t.Fatalf("got %+v", got)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !strings.Contains(r.outputs[len(r.outputs)-1], "'c1'") {
		t.Fatalf("ListDir ran against %q", r.outputs[len(r.outputs)-1])
	}
}

func TestLoadMoreLinesByOffset(t *testing.T) {
	app, r := newTestApp(t)
	app.Update(msgs.PushLogTailMsg{FilePath: "/l.log"})
	id := app.sessionID
	app.Update(msgs.StreamStartedMsg{SessionID: id, Ch: make(chan string), Stop: func() {}, LogPos: &docker.LogPosition{Start: 1 << 20, End: 2 << 20}})
	if app.logOffset != 1<<20 {
		t.Fatalf("offset = %d", app.logOffset)
	}
	// The fake container returns a chunk "a\nb\nc\n" for any read.
	r.setOutput(func(cmd, input string) (string, error) { return "partial\nb\nc\n", nil })
	_, cmd := app.Update(msgs.LoadMoreLinesMsg{SessionID: id})
	got := collect[msgs.LogChunkLoadedMsg](cmd)
	if len(got) != 1 || got[0].SessionID != id || got[0].Start >= 1<<20 || strings.Join(got[0].Lines, ",") != "b,c" {
		t.Fatalf("got %+v", got)
	}
	r.mu.Lock()
	last := r.outputs[len(r.outputs)-1]
	r.mu.Unlock()
	if !strings.Contains(last, "dd if=") || !strings.Contains(last, "/l.log") {
		t.Fatalf("read command = %q", last)
	}
	app.Update(got[0])
	if app.logOffset != got[0].Start {
		t.Fatalf("offset not advanced: %d", app.logOffset)
	}
	// A failed load keeps the offset.
	before := app.logOffset
	app.Update(msgs.LogChunkLoadedMsg{SessionID: id, Err: errors.New("x")})
	if app.logOffset != before {
		t.Fatal("offset moved on error")
	}
	// Stale session and docker logs: ignored.
	if _, cmd := app.Update(msgs.LoadMoreLinesMsg{SessionID: id + 7}); cmd != nil {
		t.Fatal("stale LoadMoreLines handled")
	}
	app.Update(msgs.PushLogTailMsg{LogType: "docker"})
	if _, cmd := app.Update(msgs.LoadMoreLinesMsg{SessionID: app.sessionID}); cmd != nil {
		t.Fatal("docker logs have no file to page through")
	}
}

func TestBackgroundLineCount(t *testing.T) {
	app, r := newTestApp(t)
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 60})
	app.Update(msgs.PushLogFilePickerMsg{})
	_, cmd := app.Update(msgs.LogFilesLoadedMsg{Files: []docker.LogFileInfo{{Path: "/l/a.log", Lines: -1}, {Path: "/l/b.log", Lines: -1}}})
	started := collect[msgs.LineCountStartedMsg](cmd)
	if len(started) != 1 || started[0].Err != nil {
		t.Fatalf("started = %+v", started)
	}
	r.mu.Lock()
	countCmd := r.streams[len(r.streams)-1]
	feed := r.lastFeed
	r.mu.Unlock()
	if !strings.Contains(countCmd, "wc -l") || !strings.Contains(countCmd, "/l/a.log") {
		t.Fatalf("count command = %q", countCmd)
	}
	_, cmd = app.Update(started[0])

	// Results arrive one by one and update the picker in place.
	feed <- "noise"
	feed <- "42|/l/a.log"
	msg := collect[msgs.LineCountMsg](cmd)
	if len(msg) != 1 || msg[0].Path != "/l/a.log" || msg[0].Lines != 42 {
		t.Fatalf("count msg = %+v", msg)
	}
	_, cmd = app.Update(msg[0])
	if !strings.Contains(app.View(), "42 lines") {
		t.Fatal("picker not updated")
	}

	// Opening a log from the picker keeps counting (the picker stays below).
	app.Update(msgs.PushLogTailMsg{FilePath: "/l/a.log"})
	if s, _, _ := r.counts(); s != 0 {
		t.Fatal("count stopped when opening a log")
	}
	app.Update(msgs.PopMsg{}) // back to the picker
	close(feed)
	done := collect[msgs.LineCountDoneMsg](cmd)
	if len(done) != 1 {
		t.Fatalf("done = %+v", done)
	}
	app.Update(done[0])
	if !strings.Contains(app.View(), "? lines") {
		t.Fatal("uncounted file not marked unknown")
	}
}

func TestLeavingPickerStopsLineCount(t *testing.T) {
	app, r := newTestApp(t)
	app.Update(msgs.PushServerLogPickerMsg{})
	_, cmd := app.Update(msgs.HostLogsDiscoveredMsg{Logs: []docker.HostLog{{Service: "x", Path: "/var/log/x.log", Lines: -1}}})
	started := collect[msgs.LineCountStartedMsg](cmd)
	app.Update(started[0])
	app.Update(msgs.PopMsg{})
	eventually(t, "count stop", func() bool { s, _, _ := r.counts(); return s == 1 })

	// A count that finishes starting after the picker closed is stopped too.
	stopped := make(chan struct{})
	app.Update(msgs.LineCountStartedMsg{ID: app.countID - 1, Stop: func() { close(stopped) }})
	<-stopped
	// Late results for the old count are ignored.
	if _, cmd := app.Update(msgs.LineCountMsg{ID: app.countID - 1, Path: "/x", Lines: 1}); cmd != nil {
		t.Fatal("stale count handled")
	}
}

func TestSaveContainerConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := &config.Config{Servers: []config.Server{{Name: "srv", Type: config.ServerTypeLocal}}}
	app := NewApp(cfg, path)
	app.Init()
	app.Update(msgs.PushContainerListMsg{Server: cfg.Servers[0]})
	app.Update(msgs.ServerConnectedMsg{Server: cfg.Servers[0], Runner: newFakeRunner(), Attempt: app.connectAttempt})
	app.Update(msgs.PushContainerEditMsg{ContainerName: "web"})
	app.Update(msgs.SaveContainerConfigMsg{Config: config.ContainerConfig{Name: "web", DisplayName: "Website"}})

	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "display_name: Website") {
		t.Fatalf("saved config:\n%s (%v)", data, err)
	}
	if cc, ok := app.activeServer.FindContainerConfig("web"); !ok || cc.DisplayName != "Website" {
		t.Fatal("active server snapshot not refreshed")
	}

	app.activeServer.Name = "gone"
	if err := app.saveContainerConfig(config.ContainerConfig{Name: "x"}); err == nil {
		t.Fatal("saving into a missing server succeeded")
	}
}

func TestPrependLines(t *testing.T) {
	src := make(chan string, 2)
	src <- "a"
	src <- "b"
	close(src)
	stopped := false
	ch, stop, err := prependLines([]string{"first"}, src, func() { stopped = true })
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for l := range ch {
		got = append(got, l)
	}
	stop()
	if strings.Join(got, ",") != "first,a,b" || !stopped {
		t.Fatalf("got %q stopped %v", got, stopped)
	}

	// Stopping without reading must not block.
	blocked := make(chan string)
	ch, stop, _ = prependLines([]string{"x", "y"}, blocked, func() { close(blocked) })
	_ = ch
	stop()
}

func TestConnectServerTypes(t *testing.T) {
	r, err := connectServer(config.Server{Type: config.ServerTypeLocal}, false)
	if err != nil || r == nil {
		t.Fatalf("local: %v", err)
	}
	r.Close()
	if _, err := connectServer(config.Server{Type: config.ServerTypeSSH, Host: "127.0.0.1", Port: 1, Key: "/nonexistent"}, false); err == nil {
		t.Fatal("ssh with a missing key connected")
	}
}

func TestWindowSizePropagates(t *testing.T) {
	app, _ := newTestApp(t)
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if app.width != 120 || app.height != 40 {
		t.Fatal("size not stored")
	}
}

func TestOpenTerminalBuildsCommand(t *testing.T) {
	app, _ := newTestApp(t)
	if _, cmd := app.Update(msgs.OpenTerminalMsg{}); cmd == nil {
		t.Fatal("no exec command")
	}
	if _, cmd := app.Update(msgs.TerminalFinishedMsg{}); cmd != nil {
		t.Fatal("unexpected command after terminal")
	}
}

func TestRawCmdLifecycle(t *testing.T) {
	app, _ := newTestApp(t)
	app.Update(msgs.PushCustomCmdMsg{})
	_, cmd := app.Update(msgs.PushRawCmdMsg{Cmd: "ls"})
	started := collect[msgs.RawCmdStartMsg](cmd)
	if len(started) != 1 || started[0].Err == nil {
		t.Fatalf("fake runner should refuse interactive: %+v", started)
	}
	app.Update(started[0])

	// A stale start (session moved on) is stopped, not adopted.
	stopped := make(chan struct{})
	app.Update(msgs.RawCmdStartMsg{SessionID: app.sessionID + 1, Stop: func() { close(stopped) }})
	<-stopped
}
