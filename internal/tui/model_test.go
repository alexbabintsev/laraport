package tui

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexbabintsev/laradok/internal/config"
	"github.com/alexbabintsev/laradok/internal/connection"
	"github.com/alexbabintsev/laradok/internal/docker"
	"github.com/alexbabintsev/laradok/internal/msgs"
	"github.com/alexbabintsev/laradok/internal/tui/screens"
	tea "github.com/charmbracelet/bubbletea"
)

// fakeRunner records what the App asks of it.
type fakeRunner struct {
	mu       sync.Mutex
	streams  []string // commands passed to StreamCommand
	inputs   []string
	outputs  []string // commands passed to RunOutput
	stops    int
	closed   int
	lastFeed chan string                             // channel of the most recent stream
	outputFn func(cmd, input string) (string, error) // RunOutput behaviour (nil = "", nil)
}

func (f *fakeRunner) setOutput(fn func(cmd, input string) (string, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outputFn = fn
}

func newFakeRunner() *fakeRunner { return &fakeRunner{} }

func (f *fakeRunner) RunCommand(cmd string) (string, error) { return "", nil }

func (f *fakeRunner) RunOutput(cmd, input string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outputs = append(f.outputs, cmd)
	if f.outputFn != nil {
		return f.outputFn(cmd, input)
	}
	return "", nil
}

func (f *fakeRunner) StreamCommand(cmd, input string) (<-chan string, func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.streams = append(f.streams, cmd)
	f.inputs = append(f.inputs, input)
	// Lines the test sends on lastFeed come out of the stream; stop ends it.
	feed := make(chan string, 16)
	f.lastFeed = feed
	ch := make(chan string)
	quit := make(chan struct{})
	go func() {
		defer close(ch)
		for {
			select {
			case l, ok := <-feed:
				if !ok {
					return
				}
				select {
				case ch <- l:
				case <-quit:
					return
				}
			case <-quit:
				return
			}
		}
	}()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			f.mu.Lock()
			f.stops++
			f.mu.Unlock()
			close(quit)
		})
	}, nil
}

func (f *fakeRunner) InteractiveCommand(cmd string) (<-chan string, chan<- string, func(), error) {
	return nil, nil, nil, errors.New("not supported")
}

func (f *fakeRunner) StartCommand(cmd, input string) (*connection.Process, error) {
	return nil, errors.New("not supported")
}

func (f *fakeRunner) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	return nil
}

func (f *fakeRunner) counts() (stops, closed, streams int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stops, f.closed, len(f.streams)
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// collect runs cmd (expanding batches) and returns the messages of type T
// produced within a short time. Slow commands (spinner ticks, cursor blinks)
// are ignored.
func collect[T any](cmd tea.Cmd) []T {
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
			msg := c()
			if b, ok := msg.(tea.BatchMsg); ok {
				for _, sub := range b {
					run(sub)
				}
				return
			}
			out <- msg
		}()
	}
	run(cmd)
	var res []T
	timeout := time.After(300 * time.Millisecond)
	for {
		select {
		case m := <-out:
			if v, ok := m.(T); ok {
				res = append(res, v)
			}
		case <-timeout:
			return res
		}
	}
}

// newTestApp returns an App connected to a fake runner with a container open.
func newTestApp(t *testing.T) (*App, *fakeRunner) {
	t.Helper()
	cfg := &config.Config{Servers: []config.Server{{Name: "srv", Type: config.ServerTypeLocal}}}
	app := NewApp(cfg, "")
	app.Init()
	r := newFakeRunner()
	app.Update(msgs.PushContainerListMsg{Server: cfg.Servers[0]})
	app.Update(msgs.ServerConnectedMsg{Server: cfg.Servers[0], Runner: r, Attempt: app.connectAttempt})
	app.Update(msgs.PushMainMenuMsg{Container: docker.Container{ID: "c1", Name: "web"}})
	return app, r
}

func TestOutputStreamStartsAsyncAndStopsOnPop(t *testing.T) {
	app, r := newTestApp(t)
	_, cmd := app.Update(msgs.PushOutputMsg{Title: "t", Host: docker.HostCommand{Cmd: "echo hi", Input: "secret\n"}})
	if _, _, n := r.counts(); n != 0 {
		t.Fatal("stream started inside Update (must be async)")
	}
	started := collect[msgs.StreamStartedMsg](cmd)
	if len(started) != 1 || started[0].Err != nil {
		t.Fatalf("started = %+v", started)
	}
	if r.inputs[0] != "secret\n" {
		t.Fatalf("stdin input not passed: %q", r.inputs)
	}
	app.Update(started[0])
	if app.stopStream == nil {
		t.Fatal("stop function not kept")
	}
	app.Update(msgs.PopMsg{})
	eventually(t, "stream stop on pop", func() bool { s, _, _ := r.counts(); return s == 1 })
	if app.stopStream != nil || app.outputCh != nil {
		t.Fatal("stream state not cleared")
	}
}

func TestStreamStartedAfterPopIsStopped(t *testing.T) {
	app, r := newTestApp(t)
	_, cmd := app.Update(msgs.PushOutputMsg{Title: "t", Host: docker.HostCommand{Cmd: "slow"}})
	app.Update(msgs.PopMsg{}) // user leaves before the stream has started
	started := collect[msgs.StreamStartedMsg](cmd)
	if len(started) != 1 {
		t.Fatalf("started = %+v", started)
	}
	app.Update(started[0])
	eventually(t, "stale stream to be stopped", func() bool { s, _, _ := r.counts(); return s == 1 })
	if app.stopStream != nil {
		t.Fatal("stale stream adopted")
	}
}

func TestNewStreamStopsPrevious(t *testing.T) {
	app, r := newTestApp(t)
	_, cmd := app.Update(msgs.PushOutputMsg{Title: "a", Host: docker.HostCommand{Cmd: "a"}})
	app.Update(collect[msgs.StreamStartedMsg](cmd)[0])
	_, cmd = app.Update(msgs.PushOutputMsg{Title: "b", Host: docker.HostCommand{Cmd: "b"}})
	eventually(t, "previous stream stop", func() bool { s, _, _ := r.counts(); return s == 1 })
	app.Update(collect[msgs.StreamStartedMsg](cmd)[0])
	if app.stopStream == nil {
		t.Fatal("new stream not tracked")
	}
}

func TestStreamStartErrorShowsError(t *testing.T) {
	app, _ := newTestApp(t)
	app.Update(msgs.PushOutputMsg{Title: "t", Host: docker.HostCommand{Cmd: "x"}})
	_, cmd := app.Update(msgs.StreamStartedMsg{SessionID: app.sessionID, Err: errors.New("boom")})
	lines := collect[msgs.OutputLineMsg](cmd)
	if len(lines) != 1 || lines[0].Line != "ERROR: boom" {
		t.Fatalf("lines = %+v", lines)
	}
}

func TestOutputDoneReleasesStream(t *testing.T) {
	app, r := newTestApp(t)
	_, cmd := app.Update(msgs.PushOutputMsg{Title: "t", Host: docker.HostCommand{Cmd: "x"}})
	app.Update(collect[msgs.StreamStartedMsg](cmd)[0])
	app.Update(msgs.OutputDoneMsg{SessionID: app.sessionID})
	eventually(t, "stop after done", func() bool { s, _, _ := r.counts(); return s == 1 })
}

func TestStaleConnectionIsClosed(t *testing.T) {
	cfg := &config.Config{Servers: []config.Server{{Name: "srv"}}}
	app := NewApp(cfg, "")
	app.Init()
	app.Update(msgs.PushContainerListMsg{Server: cfg.Servers[0]})
	attempt := app.connectAttempt
	app.Update(msgs.PopMsg{}) // back to the server list before connecting finished
	late := newFakeRunner()
	app.Update(msgs.ServerConnectedMsg{Server: cfg.Servers[0], Runner: late, Attempt: attempt})
	eventually(t, "late runner to be closed", func() bool { _, c, _ := late.counts(); return c == 1 })
	if app.runner != nil {
		t.Fatal("stale runner adopted")
	}
}

func TestBackToServerListClosesRunner(t *testing.T) {
	app, r := newTestApp(t)
	app.Update(msgs.PopMsg{}) // main menu → container list
	if _, c, _ := r.counts(); c != 0 {
		t.Fatal("runner closed while still on the container list")
	}
	app.Update(msgs.PopMsg{}) // container list → server list
	eventually(t, "runner close", func() bool { _, c, _ := r.counts(); return c == 1 })
	if app.runner != nil {
		t.Fatal("runner still set")
	}
}

func TestSwitchingServerClosesPrevious(t *testing.T) {
	app, r := newTestApp(t)
	app.Update(msgs.PushContainerListMsg{Server: config.Server{Name: "other"}})
	eventually(t, "previous runner close", func() bool { _, c, _ := r.counts(); return c == 1 })
}

func TestShutdown(t *testing.T) {
	app, r := newTestApp(t)
	_, cmd := app.Update(msgs.PushOutputMsg{Title: "t", Host: docker.HostCommand{Cmd: "x"}})
	app.Update(collect[msgs.StreamStartedMsg](cmd)[0])
	app.Shutdown()
	if s, c, _ := r.counts(); s != 1 || c != 1 {
		t.Fatalf("stops=%d closed=%d", s, c)
	}
	app.Shutdown() // idempotent
}

func TestLoadersCaptureContainerAtCallTime(t *testing.T) {
	app, r := newTestApp(t)
	cmd := app.loadCapsCmd()
	// The user switches containers before the command runs.
	app.container = docker.Container{ID: "c2", Name: "other"}
	cmd()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.outputs) != 1 || !strings.Contains(r.outputs[0], "'c1'") {
		t.Fatalf("probe ran against %q", r.outputs)
	}
}

func TestCustomCommandRunsInAppRoot(t *testing.T) {
	app, r := newTestApp(t)
	app.Update(msgs.ContainerCapsLoadedMsg{Caps: docker.ContainerCaps{HasLaravel: true, LaravelRoot: "/app"}})
	_, cmd := app.Update(msgs.PushOutputMsg{Title: "t", RawCmd: "php artisan about"})
	collect[msgs.StreamStartedMsg](cmd)
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.streams) != 1 || !strings.Contains(r.streams[0], "cd '\\''/app'\\''") {
		t.Fatalf("stream = %q", r.streams)
	}
}

func TestPopKeepsRootScreen(t *testing.T) {
	cfg := &config.Config{Servers: []config.Server{{Name: "srv"}}}
	app := NewApp(cfg, "")
	app.Init()
	app.Update(msgs.PopMsg{})
	if len(app.stack) != 1 {
		t.Fatalf("stack = %d", len(app.stack))
	}
	if _, ok := app.top().(*screens.ServerListScreen); !ok {
		t.Fatal("root screen lost")
	}
}
