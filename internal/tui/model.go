package tui

import (
	"fmt"
	"sync"
	"time"

	"github.com/alexbabintsev/laraport/internal/config"
	"github.com/alexbabintsev/laraport/internal/connection"
	"github.com/alexbabintsev/laraport/internal/docker"
	"github.com/alexbabintsev/laraport/internal/msgs"
	"github.com/alexbabintsev/laraport/internal/tui/screens"
	tea "github.com/charmbracelet/bubbletea"
)

// App is the root Bubble Tea model. It manages a stack of screens.
type App struct {
	cfg            *config.Config
	cfgPath        string
	stack          []tea.Model
	width          int
	height         int
	runner         docker.Runner          // active runner for the selected server
	activeServer   config.Server          // currently selected server
	container      docker.Container       // active container
	containerCfg   config.ContainerConfig // config for the active container (may be zero)
	stopStream     func()                 // stops the active output/tail/download stream (nil = none)
	outputCh       <-chan string          // active streaming channel
	sessionID      uint64                 // incremented whenever a stream starts or its screen closes
	connectAttempt uint64                 // incremented on every server connect attempt
	countID        uint64                 // identifies the current background line count
	countCh        <-chan string          // background line count results
	stopCount      func()                 // stops the background line count (nil = none)
	logFilePath    string                 // file path for lazy log chunk loading (empty for docker logs)
	logOffset      int64                  // byte offset of the earliest loaded log line (-1 = unknown/docker)
	dbEngine       docker.DBEngine        // active database engine for the current DB session
}

// NewApp creates the root App model starting on the server list screen.
// cfgPath is where container-config edits are written back.
func NewApp(cfg *config.Config, cfgPath string) *App {
	app := &App{cfg: cfg, cfgPath: cfgPath}
	docker.SetDownloadsDir(cfg.Settings.Downloads())
	return app
}

func (a *App) Init() tea.Cmd {
	screen := screens.NewServerListScreen(a.cfg.Servers, a.width, a.height)
	a.stack = []tea.Model{screen}
	return screen.Init()
}

// push adds a new screen to the stack and returns its Init cmd.
func (a *App) push(m tea.Model) tea.Cmd {
	a.stack = append(a.stack, m)
	return m.Init()
}

// pop removes the top screen. Returns nil if already at root.
func (a *App) pop() {
	if len(a.stack) > 1 {
		a.stack = a.stack[:len(a.stack)-1]
	}
}

// top returns the current (top) screen.
func (a *App) top() tea.Model {
	return a.stack[len(a.stack)-1]
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		// Propagate to all screens
		var cmds []tea.Cmd
		for i, s := range a.stack {
			updated, cmd := s.Update(msg)
			a.stack[i] = updated
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return a, tea.Batch(cmds...)

	case msgs.PopMsg:
		// Leaving a log picker stops its background line count.
		if _, ok := a.top().(screens.LineCounter); ok {
			a.endLineCount()
		}
		// Leaving a screen ends any stream it owned; bumping the session also
		// discards a stream that is still starting.
		a.endStream()
		a.sessionID++
		a.pop()
		// Back on the server list: the connection is no longer needed.
		if _, ok := a.top().(*screens.ServerListScreen); ok {
			a.closeRunner()
			a.connectAttempt++ // ignore a connection still being established
		}
		return a, nil

	case msgs.PushContainerListMsg:
		a.closeRunner()
		a.activeServer = msg.Server
		a.connectAttempt++
		screen := screens.NewContainerListScreen(msg.Server, nil, a.width, a.height)
		screen.SetShowStopped(!a.cfg.Settings.HideStopped)
		return a, tea.Batch(a.push(screen), connectServerCmd(msg.Server, a.connectAttempt, a.cfg.Settings.StrictHostKeys()))

	case msgs.ServerConnectedMsg:
		if msg.Attempt != a.connectAttempt {
			// The user left before the connection finished.
			if msg.Err == nil {
				closeAsync(msg.Runner)
			}
			return a, nil
		}
		if msg.Err == nil {
			a.runner = msg.Runner
		}
		// Forward to the container list screen (currently on top) so it can
		// either start loading containers or display the error.
		updated, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = updated
		return a, cmd

	case msgs.PushMainMenuMsg:
		a.container = msg.Container
		a.containerCfg, _ = a.activeServer.FindContainerConfig(msg.Container.Name)
		if a.containerCfg.RootPath == "" {
			// The server's default root (if any) applies to containers
			// without their own.
			a.containerCfg.RootPath = a.activeServer.RootPath
		}
		screen := screens.NewMainMenuScreen(msg.Container, a.containerCfg, a.width, a.height)
		return a, tea.Batch(a.push(screen), a.loadCapsCmd())

	case msgs.PushContainerEditMsg:
		screen := screens.NewContainerEditScreen(msg.ContainerName, msg.Config, a.width, a.height)
		return a, a.push(screen)

	case msgs.SaveContainerConfigMsg:
		err := a.saveContainerConfig(msg.Config)
		// Forward the result to the edit screen for its status line.
		updated, cmd := a.top().Update(msgs.ContainerConfigSavedMsg{Err: err})
		a.stack[len(a.stack)-1] = updated
		cmds := []tea.Cmd{cmd}
		// Refresh the underlying container list so the change shows immediately.
		if err == nil {
			for i := range a.stack {
				if cl, ok := a.stack[i].(*screens.ContainerListScreen); ok {
					cmds = append(cmds, cl.UpdateServer(a.activeServer))
				}
			}
		}
		return a, tea.Batch(cmds...)

	case msgs.ContainerCapsLoadedMsg:
		// If the config didn't pin a root_path, adopt the root where artisan was
		// actually found (e.g. /app) so artisan/logs/storage target the right dir.
		if a.containerCfg.RootPath == "" && msg.Caps.LaravelRoot != "" {
			a.containerCfg.RootPath = msg.Caps.LaravelRoot
		}
		updated, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = updated
		return a, cmd

	case msgs.PushCommandsMsg:
		// Merge container-specific groups (first) with global config groups
		merged := make([]config.CommandGroup, 0, len(msg.ContainerGroups)+len(a.cfg.Commands))
		merged = append(merged, msg.ContainerGroups...)
		seen := make(map[string]bool)
		for _, g := range msg.ContainerGroups {
			seen[g.Name] = true
		}
		for _, g := range a.cfg.Commands {
			if !seen[g.Name] {
				merged = append(merged, g)
			}
		}
		screen := screens.NewCommandsScreen(merged, a.width, a.height)
		return a, a.push(screen)

	case msgs.PushOutputMsg:
		screen := a.newOutputScreen(msg.Title)
		return a, a.openStream(screen, a.commandStarter(msg))

	case msgs.PushInfoMsg:
		screen := screens.NewInfoScreen(a.container.Name, a.width, a.height)
		return a, tea.Batch(a.push(screen), a.loadInfoCmd())

	case msgs.PushStatsMsg:
		screen := screens.NewStatsScreen(a.container.Name, a.width, a.height)
		screen.SetInterval(a.cfg.Settings.StatsEvery())
		return a, tea.Batch(a.push(screen), a.sampleStatsCmd(docker.SortByCPU))

	case msgs.OpenTerminalMsg:
		target := docker.ShellTarget{ContainerID: a.container.ID, DockerCLI: a.activeServer.DockerCmd}
		if a.activeServer.Type == config.ServerTypeSSH {
			target.Host = a.activeServer.Host
			target.Port = a.activeServer.Port
			target.User = a.activeServer.User
			target.KeyPath = a.activeServer.Key // already ~-expanded at config load
			if j, key, _, ok, err := a.activeServer.ResolvedJump(); err == nil && ok {
				target.JumpUser, target.JumpHost, target.JumpPort, target.JumpKey = j.User, j.Host, j.Port, key
			}
		}
		cmd := docker.InteractiveShellCmd(target)
		// Suspend the TUI, attach the real terminal to the shell, resume on exit.
		return a, tea.ExecProcess(cmd, func(err error) tea.Msg {
			return msgs.TerminalFinishedMsg{Err: err}
		})

	case msgs.TerminalFinishedMsg:
		// The TUI has resumed; nothing to do beyond a redraw. Errors are
		// transient (e.g. shell missing) and the user already saw any output.
		return a, nil

	case msgs.StatsTickMsg:
		// Stop polling once the user has navigated away from the stats screen.
		if _, ok := a.top().(*screens.StatsScreen); !ok {
			return a, nil
		}
		return a, a.sampleStatsCmd(msg.SortProcs)

	case msgs.StatsSampleMsg:
		updated, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = updated
		return a, cmd

	case msgs.ContainerInfoLoadedMsg:
		updated, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = updated
		return a, cmd

	case msgs.PushDockerCmdMsg:
		screen := screens.NewDockerCmdScreen(a.container.Name, a.width, a.height)
		return a, a.push(screen)

	case msgs.PushGlobalCmdMsg:
		screen := screens.NewGlobalCmdScreen(a.activeServer.Name, a.width, a.height)
		return a, a.push(screen)

	case msgs.PushConfirmMsg:
		if msg.Then != nil {
			return a, a.push(screens.NewConfirmActionScreen(msg.Title, msg.Detail, msg.Then, a.width, a.height))
		}
		screen := screens.NewConfirmScreen(msg.Title, msg.Detail, msg.Run, a.width, a.height)
		return a, a.push(screen)

	case msgs.PushServerEditMsg:
		return a, a.push(screens.NewServerEditScreen(msg.Original, msg.Server, a.width, a.height))

	case msgs.SaveServerMsg:
		err := a.saveServer(msg.Original, msg.Server)
		updated, cmd := a.top().Update(msgs.ServerSavedMsg{Err: err})
		a.stack[len(a.stack)-1] = updated
		if err != nil {
			return a, cmd
		}
		status := "Added " + msg.Server.Name + "."
		if msg.Original != "" {
			status = "Saved " + msg.Server.Name + "."
		}
		return a, tea.Batch(cmd, a.refreshServerList(status, msg.Server.Name))

	case msgs.DeleteServerMsg:
		if err := a.deleteServer(msg.Name); err != nil {
			return a, a.serverListError(err)
		}
		return a, a.refreshServerList("Deleted "+msg.Name+".", "")

	case msgs.TestConnectionMsg:
		srv, strict := msg.Server, a.cfg.Settings.StrictHostKeys()
		return a, func() tea.Msg {
			result, err := testConnection(srv, strict)
			return msgs.ConnectionTestedMsg{Result: result, Err: err}
		}

	case msgs.ConnectionTestedMsg:
		updated, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = updated
		return a, cmd

	case msgs.PushSettingsMsg:
		return a, a.push(screens.NewSettingsScreen(a.cfg.Settings, a.width, a.height))

	case msgs.SaveSettingsMsg:
		err := a.saveSettings(msg.Settings)
		updated, cmd := a.top().Update(msgs.SettingsSavedMsg{Err: err})
		a.stack[len(a.stack)-1] = updated
		return a, cmd

	case msgs.ClearSQLHistoryMsg:
		return a, func() tea.Msg { return msgs.SQLHistoryClearedMsg{Err: config.ClearSQLHistory()} }

	case msgs.SQLHistoryClearedMsg:
		updated, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = updated
		return a, cmd

	case msgs.ConfirmedMsg:
		// Pop the confirmation screen, then run the confirmed command/action.
		a.pop()
		if msg.Then != nil {
			then := msg.Then
			return a, func() tea.Msg { return then }
		}
		run := msg.Run
		return a, func() tea.Msg { return run }

	case msgs.PushRedisCmdMsg:
		screen := screens.NewRedisCmdScreen(a.container.Name, a.width, a.height)
		return a, tea.Batch(a.push(screen), a.loadRedisPrefixCmd())

	case msgs.RedisReadyMsg:
		updated, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = updated
		return a, cmd

	case msgs.PushMongoCmdMsg:
		screen := screens.NewMongoCmdScreen(a.container.Name, a.container.ID, msg.MongoBin, a.width, a.height)
		return a, tea.Batch(a.push(screen), a.loadMongoCredsCmd())

	case msgs.MongoReadyMsg:
		updated, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = updated
		return a, cmd

	case msgs.PushArtisanCmdMsg:
		screen := screens.NewArtisanCmdScreen(a.width, a.height)
		return a, tea.Batch(a.push(screen), a.loadArtisanCommandsCmd())

	case msgs.PushComposerCmdMsg:
		screen := screens.NewComposerCmdScreen(a.width, a.height)
		return a, tea.Batch(a.push(screen), a.loadComposerCommandsCmd())

	case msgs.PushNpmCmdMsg:
		screen := screens.NewNpmCmdScreen(a.width, a.height)
		return a, tea.Batch(a.push(screen), a.loadNpmCommandsCmd())

	case msgs.PushCustomCmdMsg:
		screen := screens.NewRawCmdScreen(a.width, a.height)
		return a, a.push(screen)

	case msgs.PushRawCmdMsg:
		a.endStream()
		a.sessionID++
		sessionID := a.sessionID
		cmd := msg.Cmd
		runner := a.runner
		containerID := a.container.ID
		return a, func() tea.Msg {
			fullCmd := docker.ExecShInteractiveCmd(containerID, cmd)
			outCh, inCh, stop, err := runner.InteractiveCommand(fullCmd)
			return msgs.RawCmdStartMsg{OutCh: outCh, InCh: inCh, Stop: stop, SessionID: sessionID, Err: err}
		}

	case msgs.RawCmdStartMsg:
		if msg.SessionID != a.sessionID {
			if msg.Stop != nil {
				go msg.Stop()
			}
			return a, nil
		}
		if msg.Err == nil {
			a.outputCh = msg.OutCh
			a.stopStream = msg.Stop
		}
		updated, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = updated
		if msg.Err == nil {
			return a, tea.Batch(cmd, screens.WaitForLine(msg.OutCh, msg.SessionID))
		}
		return a, cmd

	case msgs.ArtisanCommandsLoadedMsg:
		updated, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = updated
		return a, cmd

	case msgs.ComposerCommandsLoadedMsg:
		updated, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = updated
		return a, cmd

	case msgs.NpmCommandsLoadedMsg:
		updated, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = updated
		return a, cmd

	case msgs.PushLogFilePickerMsg:
		screen := screens.NewLogFilePickerScreen(a.width, a.height)
		return a, tea.Batch(a.push(screen), a.loadLogFilesCmd())

	case msgs.LogFilesLoadedMsg:
		updated, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = updated
		return a, tea.Batch(cmd, a.startLineCount())

	case msgs.PushDBScreenMsg:
		prefix := a.activeServer.Name + "/" + a.container.Name
		a.dbEngine = msg.Engine
		screen := screens.NewDBListScreen(prefix, msg.Engine, a.width, a.height)
		// SQLite has no credentials — skip detection and list database files directly.
		if msg.Engine == docker.EngineSQLite {
			return a, tea.Batch(a.push(screen), a.loadDBListCmd("", ""))
		}
		return a, tea.Batch(a.push(screen), a.loadDBCredsCmd())

	case msgs.DBCredsLoadedMsg:
		// Forward to DBListScreen so it stores credentials, then load DB list.
		updated, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = updated
		if msg.Err == nil {
			return a, tea.Batch(cmd, a.loadDBListCmd(msg.User, msg.Password))
		}
		return a, cmd

	case msgs.DBListLoadedMsg:
		updated, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = updated
		return a, cmd

	case msgs.PushDBActionsMsg:
		screen := screens.NewDBActionsScreen(msg.DBName, msg.User, msg.Password, a.container.ID, msg.Engine, msg.HistoryKey, a.width, a.height)
		return a, a.push(screen)

	case msgs.PushSQLInputMsg:
		var history []string
		if a.cfg.Settings.SQLHistoryLimit() > 0 {
			history = config.LoadSQLHistory(msg.HistoryKey)
		}
		screen := screens.NewSQLInputScreen(msg.DBName, msg.User, msg.Password, msg.Engine, msg.HistoryKey, history, a.width, a.height)
		return a, a.push(screen)

	case msgs.PushSQLExecMsg:
		config.SaveSQLHistory(msg.HistoryKey, msg.History, a.cfg.Settings.SQLHistoryLimit()) //nolint:errcheck // history is best-effort
		hc := docker.DBExecHostCmd(msg.Engine, a.container.ID, msg.User, msg.Password, msg.DBName, msg.SQL)
		screen := a.newOutputScreen(msg.Title)
		return a, a.openStream(screen, a.commandStarter(msgs.PushOutputMsg{Title: msg.Title, Host: hc}))

	case msgs.PushDBDownloadMsg:
		screen := a.newOutputScreen("Download dump — " + msg.DBName)
		runner, containerID := a.runner, a.container.ID
		return a, a.openStream(screen, func() (<-chan string, func(), error) {
			switch {
			case msg.Engine == docker.EngineSQLite:
				return docker.DumpSQLiteDatabase(runner, containerID, msg.DBName)
			case msg.Engine.IsMySQLFamily():
				return docker.DumpMySQLDatabase(runner, containerID, msg.User, msg.Password, msg.DBName)
			case msg.CustomFormat:
				return docker.DumpPostgres(runner, containerID, msg.User, msg.Password, msg.DBName, docker.PGDumpCustom)
			case msg.Inserts:
				return docker.DumpPostgres(runner, containerID, msg.User, msg.Password, msg.DBName, docker.PGDumpInserts)
			default:
				return docker.DumpPostgres(runner, containerID, msg.User, msg.Password, msg.DBName, docker.PGDumpPlain)
			}
		})

	case msgs.PushRedisDumpMsg:
		screen := a.newOutputScreen("Download RDB — " + a.container.Name)
		runner, containerID := a.runner, a.container.ID
		return a, a.openStream(screen, func() (<-chan string, func(), error) {
			pass, err := docker.DetectRedisPassword(runner, containerID)
			if err != nil {
				return nil, nil, err
			}
			return docker.DumpRedis(runner, containerID, pass)
		})

	case msgs.PushMongoDumpMsg:
		screen := a.newOutputScreen("Download dump — " + a.container.Name)
		runner, containerID := a.runner, a.container.ID
		return a, a.openStream(screen, func() (<-chan string, func(), error) {
			return docker.DumpMongo(runner, containerID, msg.User, msg.Password)
		})

	case msgs.PushStorageDownloadMsg:
		screen := a.newOutputScreen("Download Storage — " + a.container.Name)
		return a, a.openStream(screen, a.storageDownloadStarter())

	case msgs.PushFileBrowserMsg:
		screen := screens.NewFileBrowserScreen("/", a.width, a.height)
		return a, a.push(screen)

	case msgs.LoadDirMsg:
		runner := a.runner
		containerID := a.container.ID
		path := msg.Path
		return a, func() tea.Msg {
			entries, err := docker.ListDir(runner, containerID, path)
			return msgs.DirLoadedMsg{Path: path, Entries: entries, Err: err}
		}

	case msgs.DirLoadedMsg:
		updated, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = updated
		return a, cmd

	case msgs.PushPathDownloadMsg:
		screen := a.newOutputScreen("Download — " + msg.Path)
		runner, containerID, containerName := a.runner, a.container.ID, a.container.Name
		return a, a.openStream(screen, func() (<-chan string, func(), error) {
			return docker.DownloadPath(runner, containerID, containerName, msg.Path)
		})

	case msgs.PushServerLogPickerMsg:
		screen := screens.NewServerLogPickerScreen(a.containerCfg.CustomLogs, a.width, a.height)
		return a, tea.Batch(a.push(screen), a.discoverContainerLogsCmd())

	case msgs.HostLogsDiscoveredMsg:
		updated, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = updated
		return a, tea.Batch(cmd, a.startLineCount())

	case msgs.LineCountStartedMsg:
		if msg.ID != a.countID {
			if msg.Stop != nil {
				go msg.Stop()
			}
			return a, nil
		}
		if msg.Err != nil {
			a.forLineCounter(func(lc screens.LineCounter) { lc.LineCountDone() })
			return a, nil
		}
		a.stopCount, a.countCh = msg.Stop, msg.Ch
		return a, waitLineCount(msg.Ch, msg.ID)

	case msgs.LineCountMsg:
		if msg.ID != a.countID {
			return a, nil
		}
		a.forLineCounter(func(lc screens.LineCounter) { lc.SetLineCount(msg.Path, msg.Lines) })
		return a, waitLineCount(a.countCh, msg.ID)

	case msgs.LineCountDoneMsg:
		if msg.ID != a.countID {
			return a, nil
		}
		a.forLineCounter(func(lc screens.LineCounter) { lc.LineCountDone() })
		a.endLineCount()
		return a, nil

	case msgs.PushLogTailMsg:
		title := msg.Title
		if title == "" {
			title = "Docker Logs"
		}
		screen := screens.NewLogTailScreen(title, a.width, a.height)
		screen.SetWrap(a.cfg.Settings.WrapLogs)
		// Track file path for lazy chunk loading; docker logs have no path.
		a.logFilePath, a.logOffset = "", -1
		if msg.LogType != "docker" && msg.FilePath != "" {
			a.logFilePath = msg.FilePath
		}
		a.endStream()
		a.sessionID++
		id := a.sessionID
		runner, containerID := a.runner, a.container.ID
		return a, tea.Batch(a.push(screen), func() tea.Msg {
			if msg.LogType == "docker" {
				ch, stop, err := docker.TailDockerLogs(runner, containerID)
				return msgs.StreamStartedMsg{SessionID: id, Ch: ch, Stop: stop, Err: err}
			}
			ch, stop, pos, err := docker.TailLogFile(runner, containerID, msg.FilePath)
			return msgs.StreamStartedMsg{SessionID: id, Ch: ch, Stop: stop, Err: err, LogPos: pos}
		})

	case msgs.StreamStartedMsg:
		if msg.SessionID != a.sessionID {
			// Its screen was closed while the stream was starting.
			if msg.Stop != nil {
				go msg.Stop()
			}
			return a, nil
		}
		ch := msg.Ch
		if msg.Err != nil {
			errCh := make(chan string, 1)
			errCh <- fmt.Sprintf("ERROR: %v", msg.Err)
			close(errCh)
			ch = errCh
		} else {
			a.stopStream = msg.Stop
		}
		a.outputCh = ch
		cmds := []tea.Cmd{screens.WaitForLine(ch, a.sessionID)}
		if msg.Err == nil && msg.LogPos != nil {
			a.logOffset = msg.LogPos.Start
			updated, cmd := a.top().Update(msgs.LogTailInitMsg{Pos: *msg.LogPos, SessionID: msg.SessionID})
			a.stack[len(a.stack)-1] = updated
			cmds = append(cmds, cmd)
		}
		return a, tea.Batch(cmds...)

	case msgs.LoadMoreLinesMsg:
		if msg.SessionID != a.sessionID || a.logFilePath == "" || a.logOffset <= 0 {
			return a, nil
		}
		runner, containerID, filePath, end, sessionID := a.runner, a.container.ID, a.logFilePath, a.logOffset, a.sessionID
		return a, func() tea.Msg {
			lines, start, err := docker.ReadLinesBefore(runner, containerID, filePath, end, docker.LogChunkBytes)
			return msgs.LogChunkLoadedMsg{Lines: lines, Start: start, AtTop: err == nil && start == 0, SessionID: sessionID, Err: err}
		}

	case msgs.LogChunkLoadedMsg:
		if msg.SessionID != a.sessionID {
			return a, nil
		}
		if msg.Err == nil {
			a.logOffset = msg.Start
		}
		updated, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = updated
		return a, cmd

	case msgs.OutputLineMsg:
		// Ignore lines from a stale (previous) session
		if msg.SessionID != a.sessionID {
			return a, nil
		}
		updated, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = updated
		var nextRead tea.Cmd
		if a.outputCh != nil {
			nextRead = screens.WaitForLine(a.outputCh, a.sessionID)
		}
		return a, tea.Batch(cmd, nextRead)

	case msgs.OutputDoneMsg:
		if msg.SessionID != a.sessionID {
			return a, nil
		}
		a.outputCh = nil
		// The stream ended on its own; stop() still releases its resources.
		a.endStream()
		updated, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = updated
		return a, cmd

	default:
		updated, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = updated
		return a, cmd
	}
}

func (a *App) View() string {
	if len(a.stack) == 0 {
		return ""
	}
	return a.top().View()
}

// connectServerCmd connects to a server asynchronously and returns a ServerConnectedMsg.
func connectServerCmd(s config.Server, attempt uint64, strictHostKeys bool) tea.Cmd {
	return func() tea.Msg {
		runner, err := connectServer(s, strictHostKeys)
		return msgs.ServerConnectedMsg{Server: s, Runner: runner, Err: err, Attempt: attempt}
	}
}

// connectServer creates a runner for the given server config, using the
// server's docker command.
func connectServer(s config.Server, strictHostKeys bool) (docker.Runner, error) {
	if s.Type == config.ServerTypeSSH {
		opts := connection.SSHOptions{
			Host: s.Host, Port: s.Port, User: s.User, KeyPath: s.Key, Passphrase: s.Passphrase,
			StrictHostKeys: strictHostKeys,
		}
		j, key, pass, hasJump, err := s.ResolvedJump()
		if err != nil {
			return nil, err
		}
		if hasJump {
			opts.Jump = &connection.JumpHost{Host: j.Host, Port: j.Port, User: j.User, KeyPath: key, Passphrase: pass}
		}
		c, err := connection.ConnectSSH(opts)
		if err != nil {
			return nil, err
		}
		return docker.WithDockerCLI(c, s.DockerCmd), nil
	}
	return docker.WithDockerCLI(connection.NewLocalClient(), s.DockerCmd), nil
}

// testConnection connects to s and asks Docker for its version.
func testConnection(s config.Server, strictHostKeys bool) (string, error) {
	r, err := connectServer(s, strictHostKeys)
	if err != nil {
		return "", err
	}
	defer r.Close()
	v, err := docker.ServerVersion(r)
	if err != nil {
		return "", err
	}
	return "Docker " + v, nil
}

// newOutputScreen creates an output screen with the configured wrapping.
func (a *App) newOutputScreen(title string) *screens.OutputScreen {
	s := screens.NewOutputScreen(title, a.width, a.height)
	s.SetWrap(a.cfg.Settings.WrapLogs)
	return s
}

// saveServer adds (original == "") or updates a server and writes the config;
// the in-memory config is restored if writing fails.
func (a *App) saveServer(original string, srv config.Server) error {
	before := a.cfg.Servers
	a.cfg.Servers = append([]config.Server(nil), before...)
	var err error
	if original == "" {
		err = a.cfg.AddServer(srv)
	} else {
		err = a.cfg.UpdateServer(original, srv)
	}
	if err == nil {
		err = a.cfg.Save(a.cfgPath)
	}
	if err != nil {
		a.cfg.Servers = before
	}
	return err
}

// deleteServer removes a server and writes the config (restored on failure).
func (a *App) deleteServer(name string) error {
	before := a.cfg.Servers
	a.cfg.Servers = append([]config.Server(nil), before...)
	err := a.cfg.DeleteServer(name)
	if err == nil {
		err = a.cfg.Save(a.cfgPath)
	}
	if err != nil {
		a.cfg.Servers = before
	}
	return err
}

// saveSettings validates, stores and applies new settings.
func (a *App) saveSettings(st config.Settings) error {
	if err := st.Validate(); err != nil {
		return err
	}
	before := a.cfg.Settings
	a.cfg.Settings = st
	if err := a.cfg.Save(a.cfgPath); err != nil {
		a.cfg.Settings = before
		return err
	}
	docker.SetDownloadsDir(st.Downloads())
	return nil
}

// refreshServerList pushes the current servers to the server list screen.
func (a *App) refreshServerList(status, selectName string) tea.Cmd {
	servers := append([]config.Server(nil), a.cfg.Servers...)
	for i, m := range a.stack {
		if sl, ok := m.(*screens.ServerListScreen); ok {
			updated, cmd := sl.Update(msgs.ServerListChangedMsg{Servers: servers, Status: status})
			a.stack[i] = updated
			if selectName != "" {
				return tea.Batch(cmd, sl.SetServers(servers, selectName))
			}
			return cmd
		}
	}
	return nil
}

// serverListError shows err on the server list screen.
func (a *App) serverListError(err error) tea.Cmd {
	for i, m := range a.stack {
		if sl, ok := m.(*screens.ServerListScreen); ok {
			updated, cmd := sl.Update(msgs.ServerListChangedMsg{Err: err})
			a.stack[i] = updated
			return cmd
		}
	}
	return nil
}

// startLineCount counts the lines of the logs listed by the top screen in the
// background; results arrive as LineCountMsg.
func (a *App) startLineCount() tea.Cmd {
	lc, ok := a.top().(screens.LineCounter)
	if !ok {
		return nil
	}
	paths := lc.LogPaths()
	a.endLineCount()
	if len(paths) == 0 {
		return nil
	}
	id := a.countID
	runner, containerID := a.runner, a.container.ID
	return func() tea.Msg {
		ch, stop, err := docker.CountLines(runner, containerID, paths)
		return msgs.LineCountStartedMsg{ID: id, Ch: ch, Stop: stop, Err: err}
	}
}

// waitLineCount reads the next parseable count from ch.
func waitLineCount(ch <-chan string, id uint64) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		for line := range ch {
			if path, n, ok := docker.ParseLineCount(line); ok {
				return msgs.LineCountMsg{ID: id, Path: path, Lines: n}
			}
		}
		return msgs.LineCountDoneMsg{ID: id}
	}
}

// endLineCount stops the background line count and invalidates its messages.
func (a *App) endLineCount() {
	a.countID++
	if a.stopCount != nil {
		go a.stopCount()
		a.stopCount = nil
	}
	a.countCh = nil
}

// forLineCounter applies f to every log picker in the stack (one may sit
// below a log viewer opened from it).
func (a *App) forLineCounter(f func(screens.LineCounter)) {
	for _, m := range a.stack {
		if lc, ok := m.(screens.LineCounter); ok {
			f(lc)
		}
	}
}

// shutdownTimeout bounds how long Shutdown waits for the active stream.
const shutdownTimeout = 3 * time.Second

// Shutdown stops the active stream (so nothing keeps running on the server or
// locally after exit) and closes the connection. Call it after the program
// has exited.
func (a *App) Shutdown() {
	if a.stopCount != nil {
		a.stopCount()
		a.stopCount = nil
	}
	if stop := a.stopStream; stop != nil {
		a.stopStream = nil
		done := make(chan struct{})
		go func() {
			stop()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(shutdownTimeout):
		}
	}
	if a.runner != nil {
		a.runner.Close() //nolint:errcheck
		a.runner = nil
	}
}

// closeRunner closes the active server connection, if any.
func (a *App) closeRunner() {
	if a.runner != nil {
		closeAsync(a.runner)
		a.runner = nil
	}
}

// closeAsync closes r without blocking the UI.
func closeAsync(r docker.Runner) {
	if r != nil {
		go r.Close() //nolint:errcheck
	}
}

// streamStarter starts an output stream. It runs off the UI goroutine.
type streamStarter func() (<-chan string, func(), error)

// openStream pushes screen and starts its stream asynchronously; the result
// arrives as a StreamStartedMsg for the new session.
func (a *App) openStream(screen tea.Model, start streamStarter) tea.Cmd {
	a.endStream()
	a.sessionID++
	id := a.sessionID
	return tea.Batch(a.push(screen), func() tea.Msg {
		ch, stop, err := start()
		return msgs.StreamStartedMsg{SessionID: id, Ch: ch, Stop: stop, Err: err}
	})
}

// endStream stops the active stream (without blocking the UI) and forgets it.
func (a *App) endStream() {
	if a.stopStream != nil {
		go a.stopStream()
		a.stopStream = nil
	}
	a.outputCh = nil
}

// commandStarter returns the starter for a PushOutputMsg command.
func (a *App) commandStarter(msg msgs.PushOutputMsg) streamStarter {
	runner, containerID, rootPath := a.runner, a.container.ID, a.containerCfg.RootPath
	return func() (<-chan string, func(), error) {
		switch {
		case msg.Host.Cmd != "":
			return docker.Stream(runner, msg.Host)
		case msg.RawCmd != "":
			return docker.ExecCustomCommand(runner, containerID, rootPath, msg.RawCmd)
		default:
			return docker.ExecArtisan(runner, containerID, rootPath, msg.ArtisanCmd)
		}
	}
}

// storageDownloadStarter reports the storage size, then streams the archive.
func (a *App) storageDownloadStarter() streamStarter {
	runner, containerID, containerName, rootPath := a.runner, a.container.ID, a.container.Name, a.containerCfg.RootPath
	return func() (<-chan string, func(), error) {
		size, _ := docker.StorageSize(runner, containerID, rootPath)
		ch, stop, err := docker.DownloadStorage(runner, containerID, containerName, rootPath)
		if err != nil {
			return nil, nil, err
		}
		return prependLines([]string{"Storage size: " + size}, ch, stop)
	}
}

// prependLines returns a stream that yields lines first and then everything
// from ch. Stopping it stops the underlying stream.
func prependLines(lines []string, ch <-chan string, stop func()) (<-chan string, func(), error) {
	out := make(chan string, 16)
	quit := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(out)
		send := func(l string) bool {
			select {
			case out <- l:
				return true
			case <-quit:
				return false
			}
		}
		for _, l := range lines {
			if !send(l) {
				return
			}
		}
		for l := range ch {
			if !send(l) {
				return
			}
		}
	}()
	var once sync.Once
	return out, func() {
		once.Do(func() { close(quit) })
		stop()
		<-done
	}, nil
}

// loadLogFilesCmd lists log files in the container asynchronously.
func (a *App) loadLogFilesCmd() tea.Cmd {
	runner, containerID, rootPath := a.runner, a.container.ID, a.containerCfg.RootPath
	return func() tea.Msg {
		files, err := docker.ListLogFiles(runner, containerID, rootPath)
		return msgs.LogFilesLoadedMsg{Files: files, Err: err}
	}
}

// discoverContainerLogsCmd probes the selected container for service log files asynchronously.
func (a *App) discoverContainerLogsCmd() tea.Cmd {
	runner, containerID := a.runner, a.container.ID
	return func() tea.Msg {
		logs, err := docker.DiscoverContainerLogs(runner, containerID)
		return msgs.HostLogsDiscoveredMsg{Logs: logs, Err: err}
	}
}

// loadArtisanCommandsCmd fetches the list of artisan commands from the container.
func (a *App) loadArtisanCommandsCmd() tea.Cmd {
	runner, containerID, rootPath := a.runner, a.container.ID, a.containerCfg.RootPath
	return func() tea.Msg {
		cmds, err := docker.ListArtisanCommands(runner, containerID, rootPath)
		return msgs.ArtisanCommandsLoadedMsg{Commands: cmds, Err: err}
	}
}

// loadComposerCommandsCmd fetches available composer commands from the container.
func (a *App) loadComposerCommandsCmd() tea.Cmd {
	runner, containerID, rootPath := a.runner, a.container.ID, a.containerCfg.RootPath
	return func() tea.Msg {
		cmds, bin, err := docker.ListComposerCommands(runner, containerID, rootPath)
		return msgs.ComposerCommandsLoadedMsg{Commands: cmds, ComposerBin: bin, Err: err}
	}
}

// loadNpmCommandsCmd fetches available npm scripts from the container.
func (a *App) loadNpmCommandsCmd() tea.Cmd {
	runner, containerID, rootPath := a.runner, a.container.ID, a.containerCfg.RootPath
	return func() tea.Msg {
		cmds, err := docker.ListNpmCommands(runner, containerID, rootPath)
		return msgs.NpmCommandsLoadedMsg{Commands: cmds, Err: err}
	}
}

// saveContainerConfig upserts per-container overrides into the in-memory config,
// writes the file, and refreshes the active-server snapshot so the change is
// reflected immediately in the list and menus.
func (a *App) saveContainerConfig(cc config.ContainerConfig) error {
	if !a.cfg.UpsertContainerConfig(a.activeServer.Name, cc) {
		return fmt.Errorf("server %q not found in config", a.activeServer.Name)
	}
	if err := a.cfg.Save(a.cfgPath); err != nil {
		return err
	}
	for _, s := range a.cfg.Servers {
		if s.Name == a.activeServer.Name {
			a.activeServer = s
			break
		}
	}
	return nil
}

// loadCapsCmd detects container capabilities (artisan, composer, npm, psql, php) asynchronously.
func (a *App) loadCapsCmd() tea.Cmd {
	runner, containerID, rootPath := a.runner, a.container.ID, a.containerCfg.RootPath
	return func() tea.Msg {
		caps, err := docker.DetectCapabilities(runner, containerID, rootPath)
		return msgs.ContainerCapsLoadedMsg{Caps: caps, Err: err}
	}
}

// loadRedisPrefixCmd detects the Redis password and builds the redis-cli prefix.
func (a *App) loadRedisPrefixCmd() tea.Cmd {
	runner, containerID := a.runner, a.container.ID
	return func() tea.Msg {
		pass, _ := docker.DetectRedisPassword(runner, containerID)
		return msgs.RedisReadyMsg{ContainerID: containerID, Password: pass}
	}
}

// sampleStatsCmd takes one docker stats reading plus the top process table for
// the live stats screen.
func (a *App) sampleStatsCmd(sortBy docker.ProcSortBy) tea.Cmd {
	runner := a.runner
	containerID := a.container.ID
	return func() tea.Msg {
		sample, err := docker.SampleContainerStats(runner, containerID)
		if err != nil {
			return msgs.StatsSampleMsg{Sample: sample, Err: err}
		}
		procs, _ := docker.TopProcesses(runner, containerID, sortBy, 20)
		return msgs.StatsSampleMsg{Sample: sample, Procs: procs}
	}
}

// loadInfoCmd runs docker inspect on the active container for the Info screen.
func (a *App) loadInfoCmd() tea.Cmd {
	runner := a.runner
	containerID := a.container.ID
	return func() tea.Msg {
		info, err := docker.InspectContainer(runner, containerID)
		return msgs.ContainerInfoLoadedMsg{Info: info, Err: err}
	}
}

// loadMongoCredsCmd detects MongoDB credentials from the active container env.
func (a *App) loadMongoCredsCmd() tea.Cmd {
	runner, containerID := a.runner, a.container.ID
	return func() tea.Msg {
		user, pass, _ := docker.DetectMongoCredentials(runner, containerID)
		return msgs.MongoReadyMsg{User: user, Password: pass}
	}
}

// loadDBCredsCmd detects database credentials from the active container env.
func (a *App) loadDBCredsCmd() tea.Cmd {
	engine, runner, containerID := a.dbEngine, a.runner, a.container.ID
	return func() tea.Msg {
		var user, pass string
		var err error
		if engine.IsMySQLFamily() {
			user, pass, err = docker.DetectMySQLCredentials(runner, containerID)
		} else {
			user, pass, err = docker.DetectPostgresCredentials(runner, containerID)
		}
		return msgs.DBCredsLoadedMsg{User: user, Password: pass, Err: err}
	}
}

// loadDBListCmd lists databases in the active database container.
func (a *App) loadDBListCmd(user, password string) tea.Cmd {
	engine, runner, containerID, rootPath := a.dbEngine, a.runner, a.container.ID, a.containerCfg.RootPath
	return func() tea.Msg {
		var dbs []string
		var err error
		switch {
		case engine == docker.EngineSQLite:
			dbs, err = docker.ListSQLiteDatabases(runner, containerID, rootPath)
		case engine.IsMySQLFamily():
			dbs, err = docker.ListMySQLDatabases(runner, containerID, user, password)
		default:
			dbs, err = docker.ListDatabases(runner, containerID, user, password)
		}
		return msgs.DBListLoadedMsg{Databases: dbs, Err: err}
	}
}
