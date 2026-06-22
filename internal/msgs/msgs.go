package msgs

import (
	"github.com/alexbabintsev/laradok/internal/config"
	"github.com/alexbabintsev/laradok/internal/docker"
)

// --- Navigation messages ---

// PopMsg tells the root model to pop the current screen.
type PopMsg struct{}

// PushContainerListMsg navigates to the container list for a server.
type PushContainerListMsg struct {
	Server config.Server
}

// PushMainMenuMsg navigates to the main menu for the selected container.
type PushMainMenuMsg struct {
	Container docker.Container
}

// PushCommandsMsg navigates to the flat commands list screen.
// ContainerGroups are merged before global groups so container-specific ones appear first.
type PushCommandsMsg struct {
	ContainerGroups []config.CommandGroup
}

// PushOutputMsg navigates to the output screen and starts running a command.
type PushOutputMsg struct {
	Title      string
	ArtisanCmd string
	RawCmd     string  // executed inside the container via docker exec
	HostCmd    string  // executed directly on the host (for docker inspect, restart, etc.)
}

// PushDockerCmdMsg navigates to the docker commands screen.
type PushDockerCmdMsg struct{}

// PushInfoMsg navigates to the container info (docker inspect) screen.
type PushInfoMsg struct{}

// ContainerInfoLoadedMsg carries parsed `docker inspect` data for the info screen.
type ContainerInfoLoadedMsg struct {
	Info docker.ContainerInfo
	Err  error
}

// PushGlobalCmdMsg navigates to the global (server-level) docker commands screen.
type PushGlobalCmdMsg struct{}

// PushConfirmMsg navigates to a yes/no confirmation screen. On confirmation the
// embedded Run message is dispatched; on cancel the screen is popped.
type PushConfirmMsg struct {
	Title  string
	Detail string        // the exact command or consequence shown to the user
	Run    PushOutputMsg // dispatched when the user confirms
}

// ConfirmedMsg is emitted by the confirmation screen when the user accepts.
// The App pops the confirm screen and dispatches Run.
type ConfirmedMsg struct {
	Run PushOutputMsg
}

// PushRedisCmdMsg navigates to the Redis commands screen.
type PushRedisCmdMsg struct{}

// RedisReadyMsg carries the resolved redis-cli prefix (with auth, if any).
type RedisReadyMsg struct {
	CLIPrefix string
}

// PushMongoCmdMsg navigates to the MongoDB commands screen.
type PushMongoCmdMsg struct {
	MongoBin string // "mongosh" or "mongo"
}

// MongoReadyMsg carries detected MongoDB credentials (empty = unauthenticated).
type MongoReadyMsg struct {
	User     string
	Password string
}

// PushArtisanCmdMsg navigates to the artisan command input screen (autocomplete).
type PushArtisanCmdMsg struct{}

// PushComposerCmdMsg navigates to the composer command autocomplete screen.
type PushComposerCmdMsg struct{}

// PushNpmCmdMsg navigates to the npm command autocomplete screen.
type PushNpmCmdMsg struct{}

// PushCustomCmdMsg navigates to the raw custom command input screen.
type PushCustomCmdMsg struct{}

// PushRawCmdMsg is sent by RawCmdScreen to run a shell command interactively.
type PushRawCmdMsg struct {
	Cmd string
}

// PushLogFilePickerMsg navigates to the log file picker screen.
type PushLogFilePickerMsg struct{}

// PushServerLogPickerMsg navigates to the server (host) log picker screen.
type PushServerLogPickerMsg struct{}

// PushLogTailMsg navigates to the log tail screen.
type PushLogTailMsg struct {
	LogType    string // "docker" = container stdout; "host" = host file; empty = file inside container
	FilePath   string // path to tail (inside container or on host)
	Title      string // display title
}

// LogFilesLoadedMsg carries the result of listing log files in the container.
type LogFilesLoadedMsg struct {
	Files []docker.LogFileInfo
	Err   error
}

// HostLogsDiscoveredMsg carries the result of discovering host service log files.
type HostLogsDiscoveredMsg struct {
	Logs []docker.HostLog
	Err  error
}

// ArtisanCommandsLoadedMsg carries the list of artisan commands from the container.
type ArtisanCommandsLoadedMsg struct {
	Commands []docker.ArtisanCommand
	Err      error
}

// ComposerCommandsLoadedMsg carries the list of composer commands from the container.
type ComposerCommandsLoadedMsg struct {
	Commands    []docker.ArtisanCommand
	ComposerBin string // resolved invocation, e.g. "composer" or "php /var/www/html/composer.phar"
	Err         error
}

// NpmCommandsLoadedMsg carries the list of npm scripts from the container.
type NpmCommandsLoadedMsg struct {
	Commands []docker.ArtisanCommand
	Err      error
}

// LoadMoreLinesMsg is sent by LogTailScreen when the user scrolls to the top.
type LoadMoreLinesMsg struct {
	SessionID uint64
}

// LogTailInitMsg carries the total line count of the file after the tail starts.
type LogTailInitMsg struct {
	TotalLines int
	TopLine    int // 1-based line number of the first line currently shown
	SessionID  uint64
}

// LogChunkLoadedMsg carries a batch of earlier lines loaded on demand.
type LogChunkLoadedMsg struct {
	Lines      []string
	AtTop      bool // true when we've reached the beginning of the file
	TotalLines int  // total lines in the file at time of load
	TopLine    int  // 1-based line number of the first line in Lines
	SessionID  uint64
	Err        error
}

// --- Streaming messages ---

// OutputLineMsg carries a single output line for the output/log screens.
type OutputLineMsg struct {
	Line      string
	SessionID uint64
}

// OutputDoneMsg signals that a command has finished.
type OutputDoneMsg struct {
	SessionID uint64
}

// RawCmdStartMsg signals that an interactive command has started.
type RawCmdStartMsg struct {
	OutCh     <-chan string
	InCh      chan<- string
	Stop      func()
	SessionID uint64
	Err       error
}

// ContainerCapsLoadedMsg carries the detected capabilities of the active container.
type ContainerCapsLoadedMsg struct {
	Caps docker.ContainerCaps
}

// PushDBScreenMsg navigates to the database list screen for the active container.
type PushDBScreenMsg struct {
	Engine docker.DBEngine
}

// DBCredsLoadedMsg carries detected database credentials.
type DBCredsLoadedMsg struct {
	User     string
	Password string
	Err      error
}

// DBListLoadedMsg carries the list of databases in the PostgreSQL container.
type DBListLoadedMsg struct {
	Databases []string
	Err       error
}

// PushDBActionsMsg navigates to the per-database actions screen.
type PushDBActionsMsg struct {
	DBName     string
	User       string
	Password   string
	Engine     docker.DBEngine
	HistoryKey string // "serverName/containerName/dbName"
}

// PushSQLInputMsg navigates to the SQL input screen.
type PushSQLInputMsg struct {
	DBName     string
	User       string
	Password   string
	Engine     docker.DBEngine
	HistoryKey string   // "serverName/containerName/dbName"
	History    []string // previously executed queries, most recent last
}

// PushSQLExecMsg runs a SQL query and navigates to the output screen.
type PushSQLExecMsg struct {
	Title      string
	DBName     string
	User       string
	Password   string
	Engine     docker.DBEngine
	SQL        string
	HistoryKey string   // passed through so App can persist on execution
	History    []string // updated history to pass back on return
}

// PushDBDownloadMsg triggers a database dump download for the given database.
type PushDBDownloadMsg struct {
	DBName       string
	User         string
	Password     string
	Engine       docker.DBEngine
	CustomFormat bool // true = pg_dump -Fc (.dump), false = plain SQL gzipped (.sql.gz)
	Inserts      bool // true = add --inserts --column-inserts (slower, more portable)
}

// PushStorageDownloadMsg triggers download of the container's storage directory.
type PushStorageDownloadMsg struct{}

// PushFileBrowserMsg opens the file browser rooted at the container filesystem root.
type PushFileBrowserMsg struct{}

// LoadDirMsg requests listing the contents of a directory inside the container.
type LoadDirMsg struct {
	Path string
}

// DirLoadedMsg carries the listing of a directory inside the container.
type DirLoadedMsg struct {
	Path    string
	Entries []docker.DirEntry
	Err     error
}

// PushPathDownloadMsg triggers an archive download of an arbitrary file or
// directory inside the container.
type PushPathDownloadMsg struct {
	Path string
}

// StorageSizeLoadedMsg carries the result of checking storage directory size.
type StorageSizeLoadedMsg struct {
	Size string
	Err  error
}

// ContainersLoadedMsg carries the loaded container list.
type ContainersLoadedMsg struct {
	Containers []docker.Container
	Err        error
}

// ContainerStatsLoadedMsg carries live resource stats for all containers,
// loaded lazily after the container list is shown. Keyed by name and short ID.
type ContainerStatsLoadedMsg struct {
	Stats map[string]docker.ContainerStat
	Err   error
}

// ServerConnectedMsg is returned after an async SSH (or local) connection attempt.
type ServerConnectedMsg struct {
	Server config.Server
	Runner docker.Runner
	Err    error
}
