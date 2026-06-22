package tui

import (
	"fmt"

	"github.com/alexbabintsev/laradok/internal/config"
	"github.com/alexbabintsev/laradok/internal/connection"
	"github.com/alexbabintsev/laradok/internal/docker"
	"github.com/alexbabintsev/laradok/internal/msgs"
	"github.com/alexbabintsev/laradok/internal/tui/screens"
	tea "github.com/charmbracelet/bubbletea"
)

// App is the root Bubble Tea model. It manages a stack of screens.
type App struct {
	cfg            *config.Config
	stack          []tea.Model
	width          int
	height         int
	runner         docker.Runner            // active runner for the selected server
	activeServer   config.Server            // currently selected server
	container      docker.Container         // active container
	containerCfg   config.ContainerConfig   // config for the active container (may be zero)
	stopTail       func()                   // stop function for active tail session
	outputCh       <-chan string             // active streaming channel
	sessionID      uint64                   // incremented on each new tail/output session
	logFilePath    string                   // file path for lazy log chunk loading (empty for docker logs)
	logTopLine     int                      // 1-based line number of earliest loaded line (0 = unknown/docker)
	dbEngine       docker.DBEngine          // active database engine for the current DB session
}

// NewApp creates the root App model starting on the server list screen.
func NewApp(cfg *config.Config) *App {
	app := &App{cfg: cfg}
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
		// Stop any active tail when leaving log/output screen
		if a.stopTail != nil {
			a.stopTail()
			a.stopTail = nil
		}
		a.outputCh = nil
		a.pop()
		return a, nil

	case msgs.PushContainerListMsg:
		a.activeServer = msg.Server
		screen := screens.NewContainerListScreen(msg.Server, nil, a.width, a.height)
		return a, tea.Batch(a.push(screen), a.connectServerCmd(msg.Server))

	case msgs.ServerConnectedMsg:
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
		screen := screens.NewMainMenuScreen(msg.Container, a.containerCfg, a.width, a.height)
		return a, tea.Batch(a.push(screen), a.loadCapsCmd())

	case msgs.ContainerCapsLoadedMsg:
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
		screen := screens.NewOutputScreen(msg.Title, a.width, a.height)
		ch, err := a.startCommand(msg)
		if err != nil {
			errCh := make(chan string, 1)
			errCh <- fmt.Sprintf("ERROR: %v", err)
			close(errCh)
			ch = errCh
		}
		a.sessionID++
		a.outputCh = ch
		return a, tea.Batch(a.push(screen), screens.WaitForLine(ch, a.sessionID))

	case msgs.PushInfoMsg:
		screen := screens.NewInfoScreen(a.container.Name, a.width, a.height)
		return a, tea.Batch(a.push(screen), a.loadInfoCmd())

	case msgs.PushStatsMsg:
		screen := screens.NewStatsScreen(a.container.Name, a.width, a.height)
		return a, tea.Batch(a.push(screen), a.sampleStatsCmd(docker.SortByCPU))

	case msgs.OpenTerminalMsg:
		target := docker.ShellTarget{ContainerID: a.container.ID}
		if a.activeServer.Type == config.ServerTypeSSH {
			target.Host = a.activeServer.Host
			target.Port = a.activeServer.Port
			target.User = a.activeServer.User
			target.KeyPath = a.activeServer.Key // already ~-expanded at config load
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
		screen := screens.NewConfirmScreen(msg.Title, msg.Detail, msg.Run, a.width, a.height)
		return a, a.push(screen)

	case msgs.ConfirmedMsg:
		// Pop the confirmation screen, then run the confirmed command.
		a.pop()
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
		sessionID := a.sessionID + 1
		a.sessionID = sessionID
		cmd := msg.Cmd
		runner := a.runner
		containerID := a.container.ID
		return a, func() tea.Msg {
			fullCmd := fmt.Sprintf(`docker exec -i %s sh -c %q`, containerID, cmd)
			outCh, inCh, stop, err := runner.InteractiveCommand(fullCmd)
			return msgs.RawCmdStartMsg{OutCh: outCh, InCh: inCh, Stop: stop, SessionID: sessionID, Err: err}
		}

	case msgs.RawCmdStartMsg:
		if msg.Err == nil {
			a.outputCh = msg.OutCh
			a.stopTail = msg.Stop
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
		return a, cmd

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
		history := config.LoadSQLHistory(msg.HistoryKey)
		screen := screens.NewSQLInputScreen(msg.DBName, msg.User, msg.Password, msg.Engine, msg.HistoryKey, history, a.width, a.height)
		return a, a.push(screen)

	case msgs.PushSQLExecMsg:
		config.SaveSQLHistory(msg.HistoryKey, msg.History) //nolint:errcheck
		hostCmd := docker.DBExecHostCmd(msg.Engine, a.container.ID, msg.User, msg.Password, msg.DBName, msg.SQL)
		screen := screens.NewOutputScreen(msg.Title, a.width, a.height)
		ch, err := a.startCommand(msgs.PushOutputMsg{Title: msg.Title, HostCmd: hostCmd})
		if err != nil {
			errCh := make(chan string, 1)
			errCh <- fmt.Sprintf("ERROR: %v", err)
			close(errCh)
			ch = errCh
		}
		a.sessionID++
		a.outputCh = ch
		return a, tea.Batch(a.push(screen), screens.WaitForLine(ch, a.sessionID))

	case msgs.PushDBDownloadMsg:
		title := "Download dump — " + msg.DBName
		screen := screens.NewOutputScreen(title, a.width, a.height)
		var ch <-chan string
		var err error
		if msg.Engine == docker.EngineSQLite {
			ch, err = docker.DumpSQLiteDatabase(a.runner, a.container.ID, msg.DBName)
		} else if msg.Engine.IsMySQLFamily() {
			ch, err = docker.DumpMySQLDatabase(a.runner, a.container.ID, msg.User, msg.Password, msg.DBName)
		} else if msg.CustomFormat {
			ch, err = docker.DumpDatabaseCustom(a.runner, a.container.ID, msg.User, msg.Password, msg.DBName)
		} else if msg.Inserts {
			ch, err = docker.DumpDatabaseInserts(a.runner, a.container.ID, msg.User, msg.Password, msg.DBName)
		} else {
			ch, err = docker.DumpDatabase(a.runner, a.container.ID, msg.User, msg.Password, msg.DBName)
		}
		if err != nil {
			errCh := make(chan string, 1)
			errCh <- fmt.Sprintf("ERROR: %v", err)
			close(errCh)
			ch = errCh
		}
		a.sessionID++
		a.outputCh = ch
		return a, tea.Batch(a.push(screen), screens.WaitForLine(ch, a.sessionID))

	case msgs.PushRedisDumpMsg:
		title := "Download RDB — " + a.container.Name
		screen := screens.NewOutputScreen(title, a.width, a.height)
		pass, _ := docker.DetectRedisPassword(a.runner, a.container.ID)
		ch, err := docker.DumpRedis(a.runner, a.container.ID, pass)
		if err != nil {
			errCh := make(chan string, 1)
			errCh <- fmt.Sprintf("ERROR: %v", err)
			close(errCh)
			ch = errCh
		}
		a.sessionID++
		a.outputCh = ch
		return a, tea.Batch(a.push(screen), screens.WaitForLine(ch, a.sessionID))

	case msgs.PushMongoDumpMsg:
		title := "Download dump — " + a.container.Name
		screen := screens.NewOutputScreen(title, a.width, a.height)
		ch, err := docker.DumpMongo(a.runner, a.container.ID, msg.User, msg.Password)
		if err != nil {
			errCh := make(chan string, 1)
			errCh <- fmt.Sprintf("ERROR: %v", err)
			close(errCh)
			ch = errCh
		}
		a.sessionID++
		a.outputCh = ch
		return a, tea.Batch(a.push(screen), screens.WaitForLine(ch, a.sessionID))

	case msgs.PushStorageDownloadMsg:
		title := "Download Storage — " + a.container.Name
		screen := screens.NewOutputScreen(title, a.width, a.height)
		ch, err := a.startStorageDownloadCmd()
		if err != nil {
			errCh := make(chan string, 1)
			errCh <- fmt.Sprintf("ERROR: %v", err)
			close(errCh)
			ch = errCh
		}
		a.sessionID++
		a.outputCh = ch
		return a, tea.Batch(a.push(screen), screens.WaitForLine(ch, a.sessionID))

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
		title := "Download — " + msg.Path
		screen := screens.NewOutputScreen(title, a.width, a.height)
		ch, err := docker.DownloadPath(a.runner, a.container.ID, a.container.Name, msg.Path)
		if err != nil {
			errCh := make(chan string, 1)
			errCh <- fmt.Sprintf("ERROR: %v", err)
			close(errCh)
			ch = errCh
		}
		a.sessionID++
		a.outputCh = ch
		return a, tea.Batch(a.push(screen), screens.WaitForLine(ch, a.sessionID))

	case msgs.PushServerLogPickerMsg:
		screen := screens.NewServerLogPickerScreen(a.containerCfg.CustomLogs, a.width, a.height)
		return a, tea.Batch(a.push(screen), a.discoverContainerLogsCmd())

	case msgs.HostLogsDiscoveredMsg:
		updated, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = updated
		return a, cmd

	case msgs.PushLogTailMsg:
		title := msg.Title
		if title == "" {
			title = "Docker Logs"
		}
		screen := screens.NewLogTailScreen(title, a.width, a.height)
		ch, stop, total, topLine, err := a.startLogTail(msg)
		if err != nil {
			errCh := make(chan string, 1)
			errCh <- fmt.Sprintf("ERROR: %v", err)
			close(errCh)
			ch = errCh
			stop = func() {}
		}
		a.sessionID++
		a.outputCh = ch
		a.stopTail = stop
		// Track file path for lazy chunk loading; docker logs have no path
		if msg.LogType != "docker" && msg.FilePath != "" {
			a.logFilePath = msg.FilePath
			a.logTopLine = topLine
		} else {
			a.logFilePath = ""
			a.logTopLine = 0
		}
		cmds := []tea.Cmd{a.push(screen), screens.WaitForLine(ch, a.sessionID)}
		// Send init msg synchronously if we already have file position
		if total > 0 && topLine > 0 {
			sessionID := a.sessionID
			cmds = append(cmds, func() tea.Msg {
				return msgs.LogTailInitMsg{TotalLines: total, TopLine: topLine, SessionID: sessionID}
			})
		}
		return a, tea.Batch(cmds...)

	case msgs.LogTailInitMsg:
		if msg.SessionID != a.sessionID {
			return a, nil
		}
		a.logTopLine = msg.TopLine
		updated, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = updated
		return a, cmd

	case msgs.LoadMoreLinesMsg:
		if msg.SessionID != a.sessionID || a.logFilePath == "" || a.logTopLine == 0 {
			return a, nil
		}
		filePath := a.logFilePath
		sessionID := a.sessionID
		runner := a.runner
		containerID := a.container.ID
		fromLine := a.logTopLine - docker.LogChunkSize
		if fromLine < 1 {
			fromLine = 1
		}
		a.logTopLine = fromLine
		return a, func() tea.Msg {
			total, _ := docker.CountFileLines(runner, containerID, filePath)
			lines, atTop, err := docker.LoadLogChunk(runner, containerID, filePath, fromLine)
			return msgs.LogChunkLoadedMsg{
				Lines: lines, AtTop: atTop,
				TotalLines: total, TopLine: fromLine,
				SessionID: sessionID, Err: err,
			}
		}

	case msgs.LogChunkLoadedMsg:
		if msg.SessionID != a.sessionID {
			return a, nil
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
func (a *App) connectServerCmd(s config.Server) tea.Cmd {
	return func() tea.Msg {
		runner, err := a.connectServer(s)
		return msgs.ServerConnectedMsg{Server: s, Runner: runner, Err: err}
	}
}

// connectServer creates a runner for the given server config.
func (a *App) connectServer(s config.Server) (docker.Runner, error) {
	switch s.Type {
	case config.ServerTypeLocal:
		return connection.NewLocalClient(), nil
	case config.ServerTypeSSH:
		return connection.ConnectSSH(s.Host, s.Port, s.User, s.Key, s.Passphrase)
	default:
		return connection.NewLocalClient(), nil
	}
}

// startCommand kicks off the appropriate docker command for PushOutputMsg.
func (a *App) startCommand(msg msgs.PushOutputMsg) (<-chan string, error) {
	if msg.HostCmd != "" {
		ch, _, err := a.runner.StreamCommand(msg.HostCmd)
		return ch, err
	}
	if msg.RawCmd != "" {
		return docker.ExecCustomCommand(a.runner, a.container.ID, msg.RawCmd)
	}
	return docker.ExecArtisan(a.runner, a.container.ID, a.containerCfg.RootPath, msg.ArtisanCmd)
}

// startStorageDownloadCmd checks storage size then streams the download.
// Emits progress lines including size info before starting the transfer.
func (a *App) startStorageDownloadCmd() (<-chan string, error) {
	runner := a.runner
	containerID := a.container.ID
	containerName := a.container.Name
	rootPath := a.containerCfg.RootPath

	ch := make(chan string, 16)
	go func() {
		defer close(ch)
		ch <- "Checking storage size..."
		size, _ := docker.StorageSize(runner, containerID, rootPath)
		ch <- fmt.Sprintf("Storage size: %s", size)
		ch <- "Starting archive (tar | gzip | base64)..."

		dlCh, err := docker.DownloadStorage(runner, containerID, containerName, rootPath)
		if err != nil {
			ch <- fmt.Sprintf("ERROR: %v", err)
			return
		}
		for line := range dlCh {
			ch <- line
		}
	}()
	return ch, nil
}

// loadLogFilesCmd lists log files in the container asynchronously.
func (a *App) loadLogFilesCmd() tea.Cmd {
	return func() tea.Msg {
		files, err := docker.ListLogFiles(a.runner, a.container.ID, a.containerCfg.RootPath)
		return msgs.LogFilesLoadedMsg{Files: files, Err: err}
	}
}

// discoverContainerLogsCmd probes the selected container for service log files asynchronously.
func (a *App) discoverContainerLogsCmd() tea.Cmd {
	return func() tea.Msg {
		logs, err := docker.DiscoverContainerLogs(a.runner, a.container.ID)
		return msgs.HostLogsDiscoveredMsg{Logs: logs, Err: err}
	}
}

// loadArtisanCommandsCmd fetches the list of artisan commands from the container.
func (a *App) loadArtisanCommandsCmd() tea.Cmd {
	return func() tea.Msg {
		cmds, err := docker.ListArtisanCommands(a.runner, a.container.ID, a.containerCfg.RootPath)
		return msgs.ArtisanCommandsLoadedMsg{Commands: cmds, Err: err}
	}
}

// loadComposerCommandsCmd fetches available composer commands from the container.
func (a *App) loadComposerCommandsCmd() tea.Cmd {
	return func() tea.Msg {
		cmds, bin, err := docker.ListComposerCommands(a.runner, a.container.ID, a.containerCfg.RootPath)
		return msgs.ComposerCommandsLoadedMsg{Commands: cmds, ComposerBin: bin, Err: err}
	}
}

// loadNpmCommandsCmd fetches available npm scripts from the container.
func (a *App) loadNpmCommandsCmd() tea.Cmd {
	return func() tea.Msg {
		cmds, err := docker.ListNpmCommands(a.runner, a.container.ID, a.containerCfg.RootPath)
		return msgs.NpmCommandsLoadedMsg{Commands: cmds, Err: err}
	}
}

// loadCapsCmd detects container capabilities (artisan, composer, npm, psql, php) asynchronously.
func (a *App) loadCapsCmd() tea.Cmd {
	return func() tea.Msg {
		caps, _ := docker.DetectCapabilities(a.runner, a.container.ID, a.containerCfg.RootPath)
		return msgs.ContainerCapsLoadedMsg{Caps: caps}
	}
}

// loadRedisPrefixCmd detects the Redis password and builds the redis-cli prefix.
func (a *App) loadRedisPrefixCmd() tea.Cmd {
	containerID := a.container.ID
	return func() tea.Msg {
		pass, _ := docker.DetectRedisPassword(a.runner, containerID)
		return msgs.RedisReadyMsg{CLIPrefix: docker.RedisCLIPrefix(containerID, pass)}
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
	containerID := a.container.ID
	return func() tea.Msg {
		user, pass, _ := docker.DetectMongoCredentials(a.runner, containerID)
		return msgs.MongoReadyMsg{User: user, Password: pass}
	}
}

// loadDBCredsCmd detects database credentials from the active container env.
func (a *App) loadDBCredsCmd() tea.Cmd {
	engine := a.dbEngine
	return func() tea.Msg {
		var user, pass string
		var err error
		if engine.IsMySQLFamily() {
			user, pass, err = docker.DetectMySQLCredentials(a.runner, a.container.ID)
		} else {
			user, pass, err = docker.DetectPostgresCredentials(a.runner, a.container.ID)
		}
		return msgs.DBCredsLoadedMsg{User: user, Password: pass, Err: err}
	}
}

// loadDBListCmd lists databases in the active database container.
func (a *App) loadDBListCmd(user, password string) tea.Cmd {
	engine := a.dbEngine
	rootPath := a.containerCfg.RootPath
	return func() tea.Msg {
		var dbs []string
		var err error
		switch {
		case engine == docker.EngineSQLite:
			dbs, err = docker.ListSQLiteDatabases(a.runner, a.container.ID, rootPath)
		case engine.IsMySQLFamily():
			dbs, err = docker.ListMySQLDatabases(a.runner, a.container.ID, user, password)
		default:
			dbs, err = docker.ListDatabases(a.runner, a.container.ID, user, password)
		}
		return msgs.DBListLoadedMsg{Databases: dbs, Err: err}
	}
}

// startLogTail starts tailing logs inside the container.
// For file logs, also returns total lines and top line number for positioning.
func (a *App) startLogTail(msg msgs.PushLogTailMsg) (<-chan string, func(), int, int, error) {
	switch msg.LogType {
	case "docker":
		ch, stop, err := docker.TailDockerLogs(a.runner, a.container.ID)
		return ch, stop, 0, 0, err
	default:
		return docker.TailLogFile(a.runner, a.container.ID, msg.FilePath)
	}
}
