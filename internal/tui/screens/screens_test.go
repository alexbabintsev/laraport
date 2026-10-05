package screens

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/alexbabintsev/laradok/internal/config"
	"github.com/alexbabintsev/laradok/internal/docker"
	"github.com/alexbabintsev/laradok/internal/msgs"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const w, h = 100, 30

// msgsOf runs cmd (expanding batches) and returns the messages produced
// within a short time; slow commands (spinner ticks, cursor blinks) are cut off.
func msgsOf(cmd tea.Cmd) []tea.Msg {
	out := make(chan tea.Msg, 64)
	var wg sync.WaitGroup
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := c()
			if b, ok := m.(tea.BatchMsg); ok {
				for _, sub := range b {
					run(sub)
				}
				return
			}
			out <- m
		}()
	}
	run(cmd)
	var res []tea.Msg
	timeout := time.After(150 * time.Millisecond)
	for {
		select {
		case m := <-out:
			res = append(res, m)
		case <-timeout:
			return res
		}
	}
}

// first returns the first message of type T in cmd's output.
func first[T any](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	for _, m := range msgsOf(cmd) {
		if v, ok := m.(T); ok {
			return v
		}
	}
	var zero T
	t.Fatalf("no %T produced", zero)
	return zero
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	case "f2":
		return tea.KeyMsg{Type: tea.KeyF2}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func typeText(m tea.Model, s string) tea.Model {
	for _, r := range s {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

// screenFactories builds every screen with representative data.
func screenFactories() map[string]func() tea.Model {
	srv := config.Server{Name: "srv", Containers: []config.ContainerConfig{{Name: "web", DisplayName: "Web"}}}
	groups := []config.CommandGroup{{Name: "Cache", Commands: []config.Command{{Label: "clear", Cmd: "php artisan cache:clear"}}}}
	return map[string]func() tea.Model{
		"confirm":       func() tea.Model { return NewConfirmScreen("sure?", "rm -rf", msgs.PushOutputMsg{}, w, h) },
		"commands":      func() tea.Model { return NewCommandsScreen(groups, w, h) },
		"containerList": func() tea.Model { return NewContainerListScreen(srv, nil, w, h) },
		"containerEdit": func() tea.Model { return NewContainerEditScreen("web", config.ContainerConfig{Name: "web"}, w, h) },
		"artisan":       func() tea.Model { return NewArtisanCmdScreen(w, h) },
		"composer":      func() tea.Model { return NewComposerCmdScreen(w, h) },
		"npm":           func() tea.Model { return NewNpmCmdScreen(w, h) },
		"dbActionsPG":   func() tea.Model { return NewDBActionsScreen("db", "u", "pw", "c1", docker.EnginePostgres, "k", w, h) },
		"dbActionsMy":   func() tea.Model { return NewDBActionsScreen("db", "u", "pw", "c1", docker.EngineMySQL, "k", w, h) },
		"dbActionsSQ":   func() tea.Model { return NewDBActionsScreen("/d.db", "", "", "c1", docker.EngineSQLite, "k", w, h) },
		"dbList":        func() tea.Model { return NewDBListScreen("srv/web", docker.EnginePostgres, w, h) },
		"dockerCmd":     func() tea.Model { return NewDockerCmdScreen("my app", w, h) },
		"globalCmd":     func() tea.Model { return NewGlobalCmdScreen("srv", w, h) },
		"fileBrowser":   func() tea.Model { return NewFileBrowserScreen("/", w, h) },
		"info":          func() tea.Model { return NewInfoScreen("web", w, h) },
		"logFilePicker": func() tea.Model { return NewLogFilePickerScreen(w, h) },
		"mainMenu": func() tea.Model {
			return NewMainMenuScreen(docker.Container{ID: "c1", Name: "web"}, config.ContainerConfig{}, w, h)
		},
		"logTail":  func() tea.Model { return NewLogTailScreen("log", w, h) },
		"rawCmd":   func() tea.Model { return NewRawCmdScreen(w, h) },
		"mongoCmd": func() tea.Model { return NewMongoCmdScreen("db", "c1", "mongosh", w, h) },
		"output":   func() tea.Model { return NewOutputScreen("out", w, h) },
		"redisCmd": func() tea.Model { return NewRedisCmdScreen("cache", w, h) },
		"sqlInput": func() tea.Model {
			return NewSQLInputScreen("db", "u", "pw", docker.EnginePostgres, "k", []string{"SELECT 1"}, w, h)
		},
		"serverList":    func() tea.Model { return NewServerListScreen([]config.Server{srv}, w, h) },
		"serverLogPick": func() tea.Model { return NewServerLogPickerScreen([]string{"/var/log/x.log"}, w, h) },
		"stats":         func() tea.Model { return NewStatsScreen("web", w, h) },
	}
}

// TestScreensSmoke drives every screen through resize, navigation and
// rendering at several sizes, checking nothing panics and views render.
func TestScreensSmoke(t *testing.T) {
	keys := []string{"down", "down", "up", "pgdown", "pgup", "tab", "f2", "home", "end", "x", "backspace"}
	for name, mk := range screenFactories() {
		t.Run(name, func(t *testing.T) {
			m := mk()
			m.Init()
			for _, size := range [][2]int{{100, 30}, {40, 10}, {200, 60}, {10, 3}} {
				m, _ = m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				for _, k := range keys {
					m, _ = m.Update(key(k))
				}
				if m.View() == "" {
					t.Errorf("empty view at %v", size)
				}
			}
		})
	}
}

// TestScreensEscPops checks that esc leaves every screen that has a parent.
func TestScreensEscPops(t *testing.T) {
	for name, mk := range screenFactories() {
		if name == "serverList" {
			continue // root screen
		}
		t.Run(name, func(t *testing.T) {
			m := mk()
			_, cmd := m.Update(key("esc"))
			found := false
			for _, msg := range msgsOf(cmd) {
				if _, ok := msg.(msgs.PopMsg); ok {
					found = true
				}
			}
			if !found {
				t.Fatal("esc did not pop")
			}
		})
	}
}

func TestDockerCmdQuotesContainerName(t *testing.T) {
	s := NewDockerCmdScreen("my app; rm -rf /", w, h)
	_, cmd := s.Update(key("enter"))
	run := first[msgs.PushOutputMsg](t, cmd)
	if !strings.Contains(run.Host.Cmd, "'my app; rm -rf /'") {
		t.Fatalf("cmd = %q", run.Host.Cmd)
	}
}

func TestRedisCmdUsesStdinForPassword(t *testing.T) {
	s := NewRedisCmdScreen("cache", w, h)
	if _, cmd := s.Update(key("enter")); cmd != nil {
		if len(msgsOf(cmd)) != 0 {
			t.Fatal("ran a command before the password was detected")
		}
	}
	m, _ := s.Update(msgs.RedisReadyMsg{ContainerID: "c1", Password: "s3cret"})
	_, cmd := m.Update(key("enter"))
	run := first[msgs.PushOutputMsg](t, cmd)
	if strings.Contains(run.Host.Cmd, "s3cret") || run.Host.Input != "s3cret\n" || !strings.Contains(run.Host.Cmd, "'c1'") {
		t.Fatalf("run = %+v", run)
	}
}

func TestMongoCmdUsesDetectedCredentials(t *testing.T) {
	s := NewMongoCmdScreen("db", "c1", "mongosh", w, h)
	m, _ := s.Update(msgs.MongoReadyMsg{User: "admin", Password: "pw"})
	_, cmd := m.Update(key("enter"))
	run := first[msgs.PushOutputMsg](t, cmd)
	if strings.Contains(run.Host.Cmd, "pw") || run.Host.Input != "admin\npw\n" {
		t.Fatalf("run = %+v", run)
	}
}

func TestDBActionsKeepPasswordOffCommandLine(t *testing.T) {
	for _, engine := range []docker.DBEngine{docker.EnginePostgres, docker.EngineMySQL} {
		s := NewDBActionsScreen("db", "u", "hunter2", "c1", engine, "k", w, h)
		var m tea.Model = s
		// Walk the list until an action produces a host command.
		var run msgs.PushOutputMsg
		for i := 0; i < 50 && run.Host.Cmd == ""; i++ {
			var cmd tea.Cmd
			m, cmd = m.Update(key("enter"))
			for _, msg := range msgsOf(cmd) {
				if r, ok := msg.(msgs.PushOutputMsg); ok {
					run = r
				}
			}
			m, _ = m.Update(key("down"))
		}
		if run.Host.Cmd == "" {
			t.Fatalf("%s: no SQL action found", engine)
		}
		if strings.Contains(run.Host.Cmd, "hunter2") || run.Host.Input != "hunter2\n" {
			t.Fatalf("%s: run = %+v", engine, run)
		}
	}
}

func TestGlobalCmdDestructiveNeedsConfirmation(t *testing.T) {
	var m tea.Model = NewGlobalCmdScreen("srv", w, h)
	sawConfirm, sawDirect := false, false
	for i := 0; i < 30; i++ {
		var cmd tea.Cmd
		m, cmd = m.Update(key("enter"))
		for _, msg := range msgsOf(cmd) {
			switch v := msg.(type) {
			case msgs.PushConfirmMsg:
				sawConfirm = true
				if !strings.Contains(v.Run.Host.Cmd, "prune") {
					t.Errorf("confirmation for a non-prune command: %q", v.Run.Host.Cmd)
				}
			case msgs.PushOutputMsg:
				sawDirect = true
				if strings.Contains(v.Host.Cmd, "prune") {
					t.Errorf("prune ran without confirmation: %q", v.Host.Cmd)
				}
			}
		}
		m, _ = m.Update(key("down"))
	}
	if !sawConfirm || !sawDirect {
		t.Fatalf("confirm=%v direct=%v", sawConfirm, sawDirect)
	}
}

func TestConfirmScreen(t *testing.T) {
	run := msgs.PushOutputMsg{Title: "x"}
	s := NewConfirmScreen("sure?", "detail", run, w, h)
	if got := first[msgs.ConfirmedMsg](t, func() tea.Cmd { _, c := s.Update(key("y")); return c }()); got.Run.Title != "x" {
		t.Fatalf("confirmed = %+v", got)
	}
	first[msgs.PopMsg](t, func() tea.Cmd { _, c := s.Update(key("n")); return c }())
	if !strings.Contains(s.View(), "detail") {
		t.Fatal("detail not shown")
	}
}

func TestOutputScreenStatusLines(t *testing.T) {
	s := NewOutputScreen("dl", w, h)
	feed := func(l string) { s.Update(msgs.OutputLineMsg{Line: l}) }
	feed("start")
	feed(docker.StatusLinePrefix + "received 1 MB")
	feed(docker.StatusLinePrefix + "received 2 MB")
	feed("Saved to: x")
	if got := strings.Join(s.lines, "|"); got != "start|received 2 MB|Saved to: x" {
		t.Fatalf("lines = %q", got)
	}
	s.Update(msgs.OutputDoneMsg{})
	if s.View() == "" {
		t.Fatal("empty view")
	}
}

func TestRawCmdScreenFlow(t *testing.T) {
	var m tea.Model = NewRawCmdScreen(w, h)
	m = typeText(m, "ls -la")
	_, cmd := m.Update(key("enter"))
	if got := first[msgs.PushRawCmdMsg](t, cmd); got.Cmd != "ls -la" {
		t.Fatalf("cmd = %q", got.Cmd)
	}
	in := make(chan string, 1)
	m, _ = m.Update(msgs.RawCmdStartMsg{InCh: in})
	m = typeText(m, "first")
	m, _ = m.Update(key("enter"))
	if got := <-in; got != "first" {
		t.Fatalf("stdin = %q", got)
	}
	// The buffer is full and nobody reads: sending must not block the UI.
	in <- "occupied"
	m = typeText(m, "second")
	done := make(chan struct{})
	go func() { m.Update(key("enter")); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Update blocked on a full stdin channel")
	}
	m, _ = m.Update(msgs.RawCmdStartMsg{Err: errors.New("nope")})
	if !strings.Contains(m.View(), "nope") {
		t.Fatal("start error not shown")
	}
}

func TestAutocompleteRunsSelectedCommand(t *testing.T) {
	var m tea.Model = NewArtisanCmdScreen(w, h)
	m, _ = m.Update(msgs.ArtisanCommandsLoadedMsg{Commands: []docker.ArtisanCommand{
		{Name: "about", Desc: "Info"}, {Name: "migrate", Desc: "Run migrations"}, {Name: "migrate:fresh"},
	}})
	m = typeText(m, "migrate:f")
	_, cmd := m.Update(key("enter"))
	run := first[msgs.PushOutputMsg](t, cmd)
	if run.RawCmd != "php artisan migrate:f" && run.RawCmd != "php artisan migrate:fresh" {
		t.Fatalf("raw = %q", run.RawCmd)
	}

	var c tea.Model = NewComposerCmdScreen(w, h)
	c, _ = c.Update(msgs.ComposerCommandsLoadedMsg{Commands: []docker.ArtisanCommand{{Name: "install"}}, ComposerBin: "php '/app/composer.phar'"})
	c = typeText(c, "install")
	_, cmd = c.Update(key("enter"))
	if run := first[msgs.PushOutputMsg](t, cmd); run.RawCmd != "php '/app/composer.phar' install" {
		t.Fatalf("composer raw = %q", run.RawCmd)
	}
}

func TestAutocompleteShowsListError(t *testing.T) {
	var m tea.Model = NewArtisanCmdScreen(w, h)
	m, _ = m.Update(msgs.ArtisanCommandsLoadedMsg{Err: errors.New("PHP Fatal error")})
	if !strings.Contains(m.View(), "PHP Fatal error") {
		t.Fatal("list error not shown")
	}
	// Manual input still runs.
	m = typeText(m, "about")
	_, cmd := m.Update(key("enter"))
	if run := first[msgs.PushOutputMsg](t, cmd); run.RawCmd != "php artisan about" {
		t.Fatalf("raw = %q", run.RawCmd)
	}
}

func TestContainerListOrderingAndHidden(t *testing.T) {
	srv := config.Server{Name: "srv", Containers: []config.ContainerConfig{
		{Name: "secret", Hidden: true},
		{Name: "fav", Favorite: true, DisplayName: "Favourite"},
	}}
	s := NewContainerListScreen(srv, nil, w, h)
	s.Update(msgs.ServerConnectedMsg{Server: srv})
	s.Update(msgs.ContainersLoadedMsg{Containers: []docker.Container{
		{ID: "1", Name: "zeta", State: "running"},
		{ID: "2", Name: "secret", State: "running"},
		{ID: "3", Name: "fav", State: "running"},
		{ID: "4", Name: "alpha", State: "exited"},
	}})
	var names []string
	for _, it := range s.list.Items() {
		names = append(names, it.(containerItem).displayName)
	}
	if got := strings.Join(names, ","); got != "Favourite,zeta,alpha" {
		t.Fatalf("order = %s", got)
	}
	_, cmd := s.Update(key("e"))
	if got := first[msgs.PushContainerEditMsg](t, cmd); got.ContainerName != "fav" {
		t.Fatalf("edit = %+v", got)
	}
	_, cmd = s.Update(key("enter"))
	if got := first[msgs.PushMainMenuMsg](t, cmd); got.Container.Name != "fav" {
		t.Fatalf("open = %+v", got)
	}
	s.Update(msgs.ContainersLoadedMsg{Err: errors.New("docker down")})
	if !strings.Contains(s.View(), "docker down") {
		t.Fatal("error not shown")
	}
}

func TestContainerEditSaves(t *testing.T) {
	var m tea.Model = NewContainerEditScreen("web", config.ContainerConfig{Name: "web"}, w, h)
	m = typeText(m, "Website")
	_, cmd := m.Update(key("ctrl+s"))
	got := first[msgs.SaveContainerConfigMsg](t, cmd)
	if got.Config.Name != "web" || got.Config.DisplayName != "Website" {
		t.Fatalf("saved = %+v", got.Config)
	}
	m, _ = m.Update(msgs.ContainerConfigSavedMsg{Err: errors.New("disk full")})
	if !strings.Contains(m.View(), "disk full") {
		t.Fatal("save error not shown")
	}
}

func TestMainMenuShowsProbeError(t *testing.T) {
	s := NewMainMenuScreen(docker.Container{ID: "c1", Name: "web"}, config.ContainerConfig{}, w, h)
	s.Update(msgs.ContainerCapsLoadedMsg{Err: errors.New("container is not running")})
	if !strings.Contains(s.View(), "container is not running") {
		t.Fatal("probe error not shown")
	}
}

func TestMainMenuItemsFollowCaps(t *testing.T) {
	// Tall enough for the whole menu on one page.
	s := NewMainMenuScreen(docker.Container{ID: "c1", Name: "web"}, config.ContainerConfig{}, w, 80)
	s.Update(msgs.ContainerCapsLoadedMsg{Caps: docker.ContainerCaps{HasLaravel: true, HasPostgres: true, HasRedis: true}})
	view := s.View()
	for _, want := range []string{"Artisan", "Database", "Redis"} {
		if !strings.Contains(view, want) {
			t.Errorf("menu lacks %s", want)
		}
	}
	plain := NewMainMenuScreen(docker.Container{ID: "c1", Name: "web"}, config.ContainerConfig{}, w, h)
	plain.Update(msgs.ContainerCapsLoadedMsg{})
	if strings.Contains(plain.View(), "Artisan") {
		t.Error("Artisan shown without Laravel")
	}
}

func TestSQLInputExecutesWithHistory(t *testing.T) {
	var m tea.Model = NewSQLInputScreen("db", "u", "pw", docker.EnginePostgres, "k", []string{"SELECT 1"}, w, h)
	m = typeText(m, "SELECT now()")
	_, cmd := m.Update(key("enter"))
	got := first[msgs.PushSQLExecMsg](t, cmd)
	if got.SQL != "SELECT now()" || got.History[len(got.History)-1] != "SELECT now()" || got.HistoryKey != "k" {
		t.Fatalf("exec = %+v", got)
	}
}

func TestFileBrowserNavigation(t *testing.T) {
	var m tea.Model = NewFileBrowserScreen("/", w, h)
	cmd := m.Init()
	if got := first[msgs.LoadDirMsg](t, cmd); got.Path != "/" {
		t.Fatalf("initial load = %+v", got)
	}
	m, _ = m.Update(msgs.DirLoadedMsg{Path: "/", Entries: []docker.DirEntry{
		{Name: "var", Path: "/var", IsDir: true},
		{Name: "f.log", Path: "/f.log", Size: 10},
	}})
	_, cmd = m.Update(key("d"))
	if got := first[msgs.PushPathDownloadMsg](t, cmd); got.Path != "/var" {
		t.Fatalf("download = %+v", got)
	}
	m, _ = m.Update(key("down"))
	_, cmd = m.Update(key("enter"))
	if got := first[msgs.PushLogTailMsg](t, cmd); got.FilePath != "/f.log" {
		t.Fatalf("open file = %+v", got)
	}
	m, _ = m.Update(key("up"))
	m, cmd = m.Update(key("enter"))
	if got := first[msgs.LoadDirMsg](t, cmd); got.Path != "/var" {
		t.Fatalf("enter dir = %+v", got)
	}
	// While /var loads, esc still works (goes back up instead of trapping the user).
	_, cmd = m.Update(key("esc"))
	if got := first[msgs.LoadDirMsg](t, cmd); got.Path != "/" {
		t.Fatalf("esc while loading = %+v", got)
	}
}

func TestLogFilePickerOpensFile(t *testing.T) {
	var m tea.Model = NewLogFilePickerScreen(w, h)
	m, _ = m.Update(msgs.LogFilesLoadedMsg{Files: []docker.LogFileInfo{{Path: "/app/storage/logs/laravel.log", Lines: 3}}})
	_, cmd := m.Update(key("enter"))
	if got := first[msgs.PushLogTailMsg](t, cmd); got.FilePath != "/app/storage/logs/laravel.log" {
		t.Fatalf("got %+v", got)
	}
	m, _ = m.Update(msgs.LogFilesLoadedMsg{Err: errors.New("permission denied")})
	if !strings.Contains(m.View(), "permission denied") {
		t.Fatal("error not shown")
	}
}

func TestDBListSelectsDatabase(t *testing.T) {
	var m tea.Model = NewDBListScreen("srv/web", docker.EnginePostgres, w, h)
	m, _ = m.Update(msgs.DBCredsLoadedMsg{User: "u", Password: "pw"})
	m, _ = m.Update(msgs.DBListLoadedMsg{Databases: []string{"main", "other"}})
	_, cmd := m.Update(key("enter"))
	got := first[msgs.PushDBActionsMsg](t, cmd)
	if got.DBName != "main" || got.Password != "pw" || got.HistoryKey != "srv/web/main" {
		t.Fatalf("got %+v", got)
	}
}

func TestLogTailTracksBytePositions(t *testing.T) {
	var m tea.Model = NewLogTailScreen("log", w, h)
	m, _ = m.Update(msgs.LogTailInitMsg{Pos: docker.LogPosition{Start: 1 << 30, End: 2 << 30, InitialLines: 2}})
	s := m.(*LogTailScreen)
	m.Update(msgs.OutputLineMsg{Line: "initial-1", SessionID: 1})
	m.Update(msgs.OutputLineMsg{Line: "initial-2", SessionID: 1})
	if s.fileSize != 2<<30 {
		t.Fatalf("initial lines changed the size: %d", s.fileSize)
	}
	m.Update(msgs.OutputLineMsg{Line: "new", SessionID: 1})
	if s.fileSize != 2<<30+4 {
		t.Fatalf("followed line not added: %d", s.fileSize)
	}
	if v := m.View(); !strings.Contains(v, "2.00 GB") || !strings.Contains(v, "%") {
		t.Fatalf("status lacks byte position:\n%s", v)
	}
	if s.atTop {
		t.Fatal("at top although the buffer starts at 1 GB")
	}

	// An earlier chunk is prepended and moves the buffer start back.
	m.Update(msgs.LogChunkLoadedMsg{Lines: []string{"older-1", "older-2"}, Start: 1<<30 - 100})
	if s.bufStart != 1<<30-100 || s.lines[0] != "older-1" || len(s.lines) != 5 {
		t.Fatalf("after chunk: start %d lines %q", s.bufStart, s.lines)
	}
	m.Update(msgs.LogChunkLoadedMsg{Lines: []string{"first"}, Start: 0, AtTop: true})
	if !s.atTop || s.lines[0] != "first" {
		t.Fatal("beginning of file not recorded")
	}

	// A failed load is shown and does not change the buffer.
	m.Update(msgs.LogChunkLoadedMsg{Err: errors.New("dd: permission denied")})
	if !strings.Contains(m.View(), "permission denied") || len(s.lines) != 6 {
		t.Fatal("load error not shown / buffer changed")
	}
}

func TestLogTailRequestsEarlierChunkWhenScrollingUp(t *testing.T) {
	var m tea.Model = NewLogTailScreen("log", w, h)
	m, _ = m.Update(msgs.LogTailInitMsg{Pos: docker.LogPosition{Start: 5000, End: 9000}})
	for i := 0; i < 200; i++ {
		m, _ = m.Update(msgs.OutputLineMsg{Line: fmt.Sprintf("line %d", i), SessionID: 3})
	}
	var got []msgs.LoadMoreLinesMsg
	for i := 0; i < 200 && len(got) == 0; i++ {
		var cmd tea.Cmd
		m, cmd = m.Update(key("up"))
		if cmd == nil {
			continue
		}
		for _, msg := range msgsOf(cmd) {
			if lm, ok := msg.(msgs.LoadMoreLinesMsg); ok {
				got = append(got, lm)
			}
		}
	}
	if len(got) != 1 || got[0].SessionID != 3 {
		t.Fatalf("load requests = %+v", got)
	}
	// While loading, no duplicate requests.
	_, cmd := m.Update(key("up"))
	for _, msg := range msgsOf(cmd) {
		if _, ok := msg.(msgs.LoadMoreLinesMsg); ok {
			t.Fatal("duplicate load request while loading")
		}
	}
}

func TestLogTailDockerLogsWithoutPositions(t *testing.T) {
	var m tea.Model = NewLogTailScreen("Docker Logs", w, h)
	for i := 0; i < 200; i++ {
		m, _ = m.Update(msgs.OutputLineMsg{Line: "x", SessionID: 1})
	}
	if !strings.Contains(m.View(), "200 lines") {
		t.Fatal("line-count status missing for docker logs")
	}
	// No file to page through: scrolling up never asks for more or spins.
	for i := 0; i < 50; i++ {
		var cmd tea.Cmd
		m, cmd = m.Update(key("up"))
		if cmd == nil {
			continue
		}
		for _, msg := range msgsOf(cmd) {
			if _, ok := msg.(msgs.LoadMoreLinesMsg); ok {
				t.Fatal("docker logs requested an earlier chunk")
			}
		}
	}
	if strings.Contains(m.View(), "loading") || strings.Contains(m.View(), "more") {
		t.Fatal("docker logs show a paging indicator")
	}
}

func TestLogPickersLineCounts(t *testing.T) {
	var lp tea.Model = NewLogFilePickerScreen(w, 60)
	lp, _ = lp.Update(msgs.LogFilesLoadedMsg{Files: []docker.LogFileInfo{
		{Path: "/l/a.log", Size: 3 << 30, Lines: -1},
		{Path: "/l/b.log", Size: 10, Lines: -1},
	}})
	if v := lp.View(); !strings.Contains(v, "counting lines…") || !strings.Contains(v, "3.00 GB") {
		t.Fatalf("pending state:\n%s", v)
	}
	lc := lp.(LineCounter)
	if strings.Join(lc.LogPaths(), ",") != "/l/a.log,/l/b.log" {
		t.Fatalf("paths %q", lc.LogPaths())
	}
	lc.SetLineCount("/l/a.log", 123456)
	lc.LineCountDone()
	v := lp.View()
	if !strings.Contains(v, "123456 lines") || !strings.Contains(v, "? lines") || strings.Contains(v, "counting") {
		t.Fatalf("after counting:\n%s", v)
	}

	var sp tea.Model = NewServerLogPickerScreen([]string{"/custom.log"}, w, 60)
	sp, _ = sp.Update(msgs.HostLogsDiscoveredMsg{Logs: []docker.HostLog{{Service: "nginx", Path: "/var/log/nginx/error.log", Lines: -1}}})
	slc := sp.(LineCounter)
	if len(slc.LogPaths()) != 2 {
		t.Fatalf("paths %q", slc.LogPaths())
	}
	slc.SetLineCount("/custom.log", 7)
	slc.LineCountDone()
	if v := sp.View(); !strings.Contains(v, "7 lines") || !strings.Contains(v, "? lines") {
		t.Fatalf("server picker:\n%s", v)
	}
}

func TestFormatBytes(t *testing.T) {
	cases := map[int64]string{0: "0 B", 1536: "1.5 KB", 5 << 20: "5.0 MB", 3 << 30: "3.00 GB"}
	for in, want := range cases {
		if got := formatBytes(in); got != want {
			t.Errorf("%d → %q, want %q", in, got, want)
		}
	}
}

func TestStatsScreenSamples(t *testing.T) {
	var m tea.Model = NewStatsScreen("web", w, h)
	for i := 0; i < 3; i++ {
		m, _ = m.Update(msgs.StatsSampleMsg{Sample: docker.LiveSample{CPUPercent: float64(10 * i), MemBytes: 1 << 20, MemLimit: 1 << 30}, Procs: []docker.ProcessInfo{{PID: "1", CPU: 5, Command: "php"}}})
	}
	m, _ = m.Update(msgs.StatsSampleMsg{Err: errors.New("container stopped")})
	if !strings.Contains(m.View(), "container stopped") {
		t.Fatal("error not shown")
	}
	_, cmd := m.Update(key("m"))
	_ = cmd
}

func TestInfoScreenRenders(t *testing.T) {
	var m tea.Model = NewInfoScreen("web", w, h)
	m, _ = m.Update(msgs.ContainerInfoLoadedMsg{Info: docker.ContainerInfo{Name: "web", Image: "php:8", Labels: []docker.KeyValue{{Key: "k", Value: "v"}}}})
	if !strings.Contains(m.View(), "php:8") {
		t.Fatal("info not rendered")
	}
}

func TestServerListOpensServer(t *testing.T) {
	srv := config.Server{Name: "prod", Type: config.ServerTypeSSH, Host: "h"}
	var m tea.Model = NewServerListScreen([]config.Server{srv}, w, h)
	_, cmd := m.Update(key("enter"))
	if got := first[msgs.PushContainerListMsg](t, cmd); !reflect.DeepEqual(got.Server, srv) {
		t.Fatalf("got %+v", got)
	}
}

func TestWrapLinesCountsCellsNotBytes(t *testing.T) {
	got := wrapLines([]string{"Привет мир это строка", "short", "abcdefghij"}, 10)
	for _, l := range strings.Split(got, "\n") {
		if !utf8.ValidString(l) {
			t.Fatalf("broken UTF-8 in %q", l)
		}
		if w := lipgloss.Width(l); w > 10 {
			t.Fatalf("line %q is %d cells wide", l, w)
		}
	}
	if !strings.Contains(got, "Привет мир") || !strings.Contains(got, "short") {
		t.Fatalf("got %q", got)
	}
	if wrapLines([]string{"a", "b"}, 0) != "a\nb" {
		t.Fatal("width 0 should not wrap")
	}
}

func TestMainMenuEveryItemNavigates(t *testing.T) {
	caps := docker.ContainerCaps{HasLaravel: true, LaravelRoot: "/app", HasComposer: true, HasNpm: true, HasPostgres: true, HasRedis: true, MongoBin: "mongosh", HasPHP: true}
	cc := config.ContainerConfig{Commands: []config.CommandGroup{{Name: "g"}}}
	var m tea.Model = NewMainMenuScreen(docker.Container{ID: "c1", Name: "web"}, cc, w, 80)
	m, _ = m.Update(msgs.ContainerCapsLoadedMsg{Caps: caps})
	seen := map[string]bool{}
	for i := 0; i < 25; i++ {
		_, cmd := m.Update(key("enter"))
		for _, msg := range msgsOf(cmd) {
			seen[reflect.TypeOf(msg).Name()] = true
		}
		m, _ = m.Update(key("down"))
	}
	for _, want := range []string{"PushInfoMsg", "PushStatsMsg", "OpenTerminalMsg", "PushCommandsMsg", "PushArtisanCmdMsg",
		"PushComposerCmdMsg", "PushNpmCmdMsg", "PushDockerCmdMsg", "PushCustomCmdMsg", "PushLogFilePickerMsg", "PushLogTailMsg",
		"PushServerLogPickerMsg", "PushDBScreenMsg", "PushRedisCmdMsg", "PushMongoCmdMsg", "PushStorageDownloadMsg", "PushFileBrowserMsg"} {
		if !seen[want] {
			t.Errorf("no menu item produced %s", want)
		}
	}
}

func TestAutocompleteNavigationAndGroups(t *testing.T) {
	var m tea.Model = NewArtisanCmdScreen(w, 12)
	var cmds []docker.ArtisanCommand
	for _, g := range []string{"cache", "db", "make", "queue"} {
		for i := 0; i < 6; i++ {
			cmds = append(cmds, docker.ArtisanCommand{Name: fmt.Sprintf("%s:cmd%d", g, i), Desc: "desc"})
		}
	}
	m, _ = m.Update(msgs.ArtisanCommandsLoadedMsg{Commands: cmds})
	if !strings.Contains(m.View(), "cache") {
		t.Fatal("group header missing")
	}
	for i := 0; i < 15; i++ {
		m, _ = m.Update(key("down"))
	}
	m, _ = m.Update(key("pgdown"))
	m, _ = m.Update(key("up"))
	m, _ = m.Update(key("pgup"))
	m, _ = m.Update(key("tab"))
	_, cmd := m.Update(key("enter"))
	if run := first[msgs.PushOutputMsg](t, cmd); !strings.HasPrefix(run.RawCmd, "php artisan ") {
		t.Fatalf("raw = %q", run.RawCmd)
	}
}

func TestStatsResortsProcesses(t *testing.T) {
	var m tea.Model = NewStatsScreen("web", w, h)
	procs := []docker.ProcessInfo{{PID: "1", CPU: 1, Mem: 9, Command: "a"}, {PID: "2", CPU: 9, Mem: 1, Command: "b"}}
	m, _ = m.Update(msgs.StatsSampleMsg{Procs: procs})
	m, _ = m.Update(key("m"))
	s := m.(*StatsScreen)
	if s.procs[0].PID != "1" {
		t.Fatalf("by mem: %+v", s.procs)
	}
	m, _ = m.Update(key("c"))
	if m.(*StatsScreen).procs[0].PID != "2" {
		t.Fatalf("by cpu: %+v", m.(*StatsScreen).procs)
	}
}

func TestServerLogPickerMergesCustomLogs(t *testing.T) {
	var m tea.Model = NewServerLogPickerScreen([]string{"/custom.log", "/var/log/nginx/error.log"}, w, h)
	m, _ = m.Update(msgs.HostLogsDiscoveredMsg{Logs: []docker.HostLog{{Service: "nginx", Path: "/var/log/nginx/error.log"}}})
	s := m.(*ServerLogPickerScreen)
	if n := len(s.list.Items()); n != 2 {
		t.Fatalf("items = %d", n)
	}
	_, cmd := m.Update(key("enter"))
	if got := first[msgs.PushLogTailMsg](t, cmd); got.FilePath == "" {
		t.Fatalf("got %+v", got)
	}
	var empty tea.Model = NewServerLogPickerScreen(nil, w, h)
	empty, _ = empty.Update(msgs.HostLogsDiscoveredMsg{})
	if !strings.Contains(empty.View(), "No service log files") {
		t.Fatal("empty state not shown")
	}
}

func TestRedisAndMongoViewsAfterReady(t *testing.T) {
	var r tea.Model = NewRedisCmdScreen("cache", w, h)
	r, _ = r.Update(msgs.RedisReadyMsg{ContainerID: "c1"})
	var mg tea.Model = NewMongoCmdScreen("db", "c1", "mongosh", w, h)
	mg, _ = mg.Update(msgs.MongoReadyMsg{})
	for _, m := range []tea.Model{r, mg} {
		for i := 0; i < 30; i++ {
			m, _ = m.Update(key("down"))
		}
		if m.View() == "" {
			t.Fatal("empty view")
		}
	}
}
