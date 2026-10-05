package docker

import (
	"encoding/json"
	"fmt"
	"github.com/alexbabintsev/laradok/internal/connection"
	"strconv"
	"strings"
	"time"
)

const defaultRootPath = "/var/www/html"

func artisanPath(rootPath string) string {
	return strings.TrimSuffix(appRoot(rootPath), "/") + "/artisan"
}

func logsDir(rootPath string) string {
	return strings.TrimSuffix(appRoot(rootPath), "/") + "/storage/logs"
}

// Runner runs host commands. It is implemented by connection.SSHClient and
// connection.LocalClient.
type Runner interface {
	// RunCommand returns combined stdout+stderr.
	RunCommand(cmd string) (string, error)
	// RunOutput returns stdout only (stdin gets input); the error carries stderr.
	RunOutput(cmd, input string) (string, error)
	// StreamCommand streams combined output lines; stdin gets input and then
	// stays open until stop.
	StreamCommand(cmd, input string) (<-chan string, func(), error)
	InteractiveCommand(cmd string) (<-chan string, chan<- string, func(), error)
	// StartCommand exposes raw stdout for binary transfers.
	StartCommand(cmd, input string) (*connection.Process, error)
	Close() error
}

// Stream starts hc as a line stream.
func Stream(r Runner, hc HostCommand) (<-chan string, func(), error) {
	return r.StreamCommand(hc.Cmd, hc.Input)
}

// Container represents a running Docker container.
type Container struct {
	ID     string
	Name   string
	Image  string
	State  string
	Status string // human-readable, e.g. "Up 3 hours (healthy)"
	Ports  string // published ports, e.g. "0.0.0.0:8080->80/tcp"
}

// runOutput runs cmd and returns its trimmed stdout.
func runOutput(r Runner, cmd string) (string, error) {
	out, err := r.RunOutput(cmd, "")
	return strings.TrimSpace(out), err
}

// ListContainers returns all containers on the target host, including stopped
// ones (docker ps -a).
func ListContainers(r Runner) ([]Container, error) {
	format := `{"id":{{json .ID}},"name":{{json .Names}},"image":{{json .Image}},"state":{{json .State}},"status":{{json .Status}},"ports":{{json .Ports}}}`
	out, err := r.RunOutput("docker ps -a --format "+shellQuote(format), "")
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	return parseContainers(out), nil
}

// parseContainers parses `docker ps --format` output, one JSON object per
// line. Lines that are not container JSON (e.g. noise printed by the remote
// shell's rc files) are ignored.
func parseContainers(out string) []Container {
	var containers []Container
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var c Container
		if err := json.Unmarshal([]byte(line), &c); err != nil || c.ID == "" {
			continue
		}
		c.Name = strings.TrimPrefix(c.Name, "/")
		containers = append(containers, c)
	}
	return containers
}

// appRoot returns the app root inside the container with no trailing slash.
func appRoot(rootPath string) string {
	if rootPath == "" {
		return defaultRootPath
	}
	if r := strings.TrimRight(rootPath, "/"); r != "" {
		return r
	}
	return "/"
}

// ExecArtisan runs an artisan command inside a container and streams the
// output. artisanCmd is the argument string as typed by the user; it is
// interpreted by the shell inside the container (so quoting and pipes work
// there), never by the host shell.
func ExecArtisan(r Runner, containerID, rootPath, artisanCmd string) (<-chan string, func(), error) {
	script := "php " + shellQuote(artisanPath(rootPath)) + " " + artisanCmd
	return Stream(r, ExecStreamScript(containerID, script))
}

// ArtisanCommand holds an artisan command name and its description.
type ArtisanCommand struct {
	Name string
	Desc string
}

// ListArtisanCommands returns all available artisan commands with descriptions.
func ListArtisanCommands(r Runner, containerID, rootPath string) ([]ArtisanCommand, error) {
	script := "php " + shellQuote(artisanPath(rootPath)) + " list --raw --no-ansi"
	out, err := runOutput(r, ExecShCmd(containerID, script))
	if err != nil {
		return nil, fmt.Errorf("artisan list: %w", err)
	}
	return parseArtisanCommands(out), nil
}

// parseArtisanCommands parses `artisan list --raw` output, where each line is
// "command:name   Description text".
func parseArtisanCommands(out string) []ArtisanCommand {
	var cmds []ArtisanCommand
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if c, ok := splitNameDesc(line); ok {
			cmds = append(cmds, c)
		}
	}
	return cmds
}

// splitNameDesc splits "name   description" on the first run of 2+ spaces.
func splitNameDesc(line string) (ArtisanCommand, bool) {
	idx := strings.Index(line, "  ")
	if idx < 0 {
		return ArtisanCommand{Name: line}, line != ""
	}
	name := strings.TrimSpace(line[:idx])
	return ArtisanCommand{Name: name, Desc: strings.TrimSpace(line[idx:])}, name != ""
}

// composerInstallScript downloads composer.phar into the current directory,
// verifying the installer against its published SHA-384 signature (the
// procedure recommended on getcomposer.org) before running it.
const composerInstallScript = `php -r "copy('https://getcomposer.org/installer', 'composer-setup.php');" && ` +
	`php -r "copy('https://composer.github.io/installer.sig', 'composer-setup.sig');" && ` +
	`php -r "if (hash_file('sha384', 'composer-setup.php') !== trim(file_get_contents('composer-setup.sig'))) { fwrite(STDERR, 'composer installer signature mismatch' . PHP_EOL); exit(1); }" && ` +
	`php composer-setup.php --quiet; rc=$?; rm -f composer-setup.php composer-setup.sig; exit $rc`

// resolveComposer returns the shell invocation for composer inside the container.
// It prefers the system `composer` binary. If not found, it ensures composer.phar
// exists in root (downloading it if needed) and returns "php '<root>/composer.phar'".
// The returned invocation is already shell-quoted for use inside the container.
func resolveComposer(r Runner, containerID, root string) (string, error) {
	check, _ := runOutput(r, ExecShCmd(containerID, "command -v composer 2>/dev/null"))
	if check != "" {
		return "composer", nil
	}

	pharPath := root + "/composer.phar"
	exist, _ := runOutput(r, ExecShCmd(containerID, "test -f "+shellQuote(pharPath)+" && echo yes"))
	if exist != "yes" {
		dl := "cd " + shellQuote(root) + " && " + composerInstallScript
		if _, err := r.RunOutput(ExecShCmd(containerID, dl), ""); err != nil {
			return "", fmt.Errorf("downloading composer.phar: %w", err)
		}
	}

	return "php " + shellQuote(pharPath), nil
}

// ListComposerCommands returns available composer commands with descriptions.
// Also returns the resolved composer invocation (e.g. "composer" or
// "php '/var/www/html/composer.phar'").
func ListComposerCommands(r Runner, containerID, rootPath string) ([]ArtisanCommand, string, error) {
	root := appRoot(rootPath)
	composerBin, err := resolveComposer(r, containerID, root)
	if err != nil {
		return nil, "", err
	}
	script := "cd " + shellQuote(root) + " && " + composerBin + " list --no-ansi"
	out, err := runOutput(r, ExecShCmd(containerID, script))
	if err != nil {
		return nil, composerBin, fmt.Errorf("composer list: %w", err)
	}
	return parseComposerCommands(out), composerBin, nil
}

// parseComposerCommands parses `composer list` output into ArtisanCommand entries.
// The output has sections separated by blank lines; commands appear as:
//
//	command:name   Description text
func parseComposerCommands(out string) []ArtisanCommand {
	var cmds []ArtisanCommand
	inCommands := false
	for _, line := range strings.Split(out, "\n") {
		// Section headers are lines like "Available commands:" or "composer"
		if strings.HasSuffix(strings.TrimSpace(line), ":") {
			inCommands = strings.Contains(line, "command")
			continue
		}
		if !inCommands {
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if c, ok := splitNameDesc(line); ok {
			cmds = append(cmds, c)
		}
	}
	return cmds
}

// ListNpmCommands returns available npm scripts from package.json with descriptions.
func ListNpmCommands(r Runner, containerID, rootPath string) ([]ArtisanCommand, error) {
	script := "cd " + shellQuote(appRoot(rootPath)) + " && npm run"
	out, err := runOutput(r, ExecShCmd(containerID, script))
	if err != nil {
		return nil, fmt.Errorf("npm run: %w", err)
	}
	return parseNpmCommands(out), nil
}

// parseNpmCommands parses `npm run` output into ArtisanCommand entries.
// Output format:
//
//	Scripts available in <package> via `npm run-script`:
//	  script-name
//	    <command>
func parseNpmCommands(out string) []ArtisanCommand {
	var cmds []ArtisanCommand
	lines := strings.Split(out, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "Scripts available") || strings.HasPrefix(line, "Lifecycle scripts") {
			continue
		}
		// Two-space indent = script name; four-space indent = script body.
		if !strings.HasPrefix(lines[i], "  ") || strings.HasPrefix(lines[i], "    ") {
			continue
		}
		name := line
		desc := ""
		// Next line (if 4-space indent) is the script body — use as description
		if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "    ") {
			desc = strings.TrimSpace(lines[i+1])
			i++ // consume the body line
		}
		cmds = append(cmds, ArtisanCommand{Name: name, Desc: desc})
	}
	return cmds
}

// CustomCommandScript builds the in-container script for a user/config
// command: it changes into workDir first (when given, ignoring a missing
// directory so the container's WORKDIR is used instead) and then runs command
// with the container's shell.
func CustomCommandScript(workDir, command string) string {
	if workDir == "" {
		return command
	}
	return "cd " + shellQuote(workDir) + " 2>/dev/null; " + command
}

// ExecCustomCommand runs an arbitrary shell command inside a container and
// streams the output. The command is interpreted by the container's shell, so
// variables and $(…) expand inside the container, not on the host.
func ExecCustomCommand(r Runner, containerID, workDir, command string) (<-chan string, func(), error) {
	return Stream(r, ExecStreamScript(containerID, CustomCommandScript(workDir, command)))
}

// LogFileInfo holds metadata about a log file inside a container.
type LogFileInfo struct {
	Path       string
	Size       int64
	Lines      int // -1 until counted
	CreatedAt  time.Time
	ModifiedAt time.Time
}

// fileStatsScript prints one "size|mtime|btime|ctime|path" line for every
// regular file matched by `find <findArgs>`, sorted by path. Only metadata is
// read (stat), never file contents, so it is instant even for multi-GB logs;
// line counts are gathered separately in the background (CountLines). The
// path comes last so a '|' inside a file name cannot shift the other fields.
// findArgs must already be shell-quoted where needed.
func fileStatsScript(findArgs string) string {
	return `find ` + findArgs + ` 2>/dev/null | sort | while IFS= read -r f; do ` +
		`s=$(stat -c '%s|%Y|%W|%Z' "$f" 2>/dev/null) || s="$(wc -c < "$f" 2>/dev/null | tr -d ' ')|0|0|0"; ` +
		`printf '%s|%s\n' "$s" "$f"; done`
}

// parseFileStats parses fileStatsScript output. Lines is set to -1
// (not counted yet).
func parseFileStats(out string) []LogFileInfo {
	var files []LogFileInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 5)
		if len(parts) != 5 || parts[4] == "" {
			continue
		}
		info := LogFileInfo{Path: parts[4], Lines: -1}
		info.Size, _ = strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
		mtime, _ := strconv.ParseInt(parts[1], 10, 64)
		btime, _ := strconv.ParseInt(parts[2], 10, 64)
		ctime, _ := strconv.ParseInt(parts[3], 10, 64)
		info.ModifiedAt = time.Unix(mtime, 0)
		// Birth time (%W) is 0 or unsupported on many filesystems — fall back to ctime.
		if btime > 0 {
			info.CreatedAt = time.Unix(btime, 0)
		} else {
			info.CreatedAt = time.Unix(ctime, 0)
		}
		files = append(files, info)
	}
	return files
}

// ListLogFiles returns all files in the container's storage/logs directory
// with their size and timestamps (Lines = -1; see CountLines).
func ListLogFiles(r Runner, containerID, rootPath string) ([]LogFileInfo, error) {
	script := fileStatsScript(shellQuote(logsDir(rootPath)) + " -type f")
	out, err := r.RunOutput(ExecShCmd(containerID, script), "")
	if err != nil {
		return nil, fmt.Errorf("list log files: %w", err)
	}
	return parseFileStats(out), nil
}

// TailDockerLogs streams the Docker container logs (stdout/stderr) in real-time.
func TailDockerLogs(r Runner, containerID string) (<-chan string, func(), error) {
	return Stream(r, HostStreamScript("docker logs -f --tail 100 "+shellQuote(containerID)))
}

// HostLog represents a discovered log file on the host server.
type HostLog struct {
	Service    string
	Path       string
	Size       int64
	Lines      int // -1 until counted
	CreatedAt  time.Time
	ModifiedAt time.Time
}

// DiscoverContainerLogs finds log files inside the given container by scanning
// /var/log recursively for *.log files and common paths like syslog.
func DiscoverContainerLogs(r Runner, containerID string) ([]HostLog, error) {
	script := fileStatsScript(`/var/log -type f \( -name '*.log' -o -name 'syslog' \)`)
	out, err := r.RunOutput(ExecShCmd(containerID, script), "")
	if err != nil {
		return nil, fmt.Errorf("discover logs: %w", err)
	}

	var logs []HostLog
	seen := make(map[string]bool)
	for _, f := range parseFileStats(out) {
		if seen[f.Path] {
			continue
		}
		seen[f.Path] = true
		logs = append(logs, HostLog{
			Service:    containerLogService(f.Path),
			Path:       f.Path,
			Size:       f.Size,
			Lines:      f.Lines,
			CreatedAt:  f.CreatedAt,
			ModifiedAt: f.ModifiedAt,
		})
	}
	return logs, nil
}

// containerLogService derives a short service name from a log file path inside a container.
// e.g. /var/log/nginx/error.log -> "nginx", /var/log/php8.2-fpm.log -> "php8.2-fpm"
func containerLogService(path string) string {
	parts := strings.Split(path, "/")
	// /var/log/<service>/file.log  → parts[3]
	if len(parts) >= 5 {
		return parts[3]
	}
	// /var/log/filename.log
	base := parts[len(parts)-1]
	return strings.TrimSuffix(base, ".log")
}
