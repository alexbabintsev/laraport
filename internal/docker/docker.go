package docker

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultRootPath = "/var/www/html"

func artisanPath(rootPath string) string {
	return strings.TrimSuffix(appRoot(rootPath), "/") + "/artisan"
}

func logsDir(rootPath string) string {
	return strings.TrimSuffix(appRoot(rootPath), "/") + "/storage/logs"
}

// Runner is an interface implemented by both SSHClient and LocalClient.
type Runner interface {
	RunCommand(cmd string) (string, error)
	StreamCommand(cmd string) (<-chan string, func(), error)
	InteractiveCommand(cmd string) (<-chan string, chan<- string, func(), error)
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

// retryDelay is the pause between attempts of commands that are retried to
// ride out transient docker/SSH hiccups.
const retryDelay = 300 * time.Millisecond

// runRetry runs cmd up to attempts times and returns the first non-empty
// output (NUL-stripped and trimmed). A run that succeeds with empty output is
// retried too. When every attempt fails it returns the last error; when the
// command keeps succeeding with no output it returns "" and nil.
func runRetry(r Runner, cmd string, attempts int) (string, error) {
	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			time.Sleep(retryDelay)
		}
		out, err := r.RunCommand(cmd)
		out = strings.TrimSpace(stripNUL(out))
		if err != nil {
			lastErr = commandError(err, out)
			continue
		}
		lastErr = nil
		if out != "" {
			return out, nil
		}
	}
	return "", lastErr
}

// commandError wraps a command failure with its (trimmed) output, if any.
func commandError(err error, out string) error {
	out = strings.TrimSpace(out)
	if out == "" {
		return err
	}
	return fmt.Errorf("%w\n%s", err, out)
}

// ListContainers returns all containers on the target host, including stopped
// ones (docker ps -a).
//
// Output right after an SSH connection opens can arrive truncated; a truncated
// line fails to parse as JSON, so the listing is retried (up to 5 times) until
// every line parses, keeping the most complete result seen.
func ListContainers(r Runner) ([]Container, error) {
	format := `{"id":{{json .ID}},"name":{{json .Names}},"image":{{json .Image}},"state":{{json .State}},"status":{{json .Status}},"ports":{{json .Ports}}}`
	cmd := "docker ps -a --format " + shellQuote(format)

	var best []Container
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			time.Sleep(retryDelay)
		}
		out, err := r.RunCommand(cmd)
		if err != nil {
			return nil, fmt.Errorf("docker ps: %w", commandError(err, out))
		}
		containers, complete := parseContainers(out)
		if len(containers) > len(best) {
			best = containers
		}
		if complete {
			return containers, nil
		}
	}
	return best, nil
}

// parseContainers parses `docker ps --format '{{json-ish}}'` output, one JSON
// object per line. complete is false when any non-empty line failed to parse
// (i.e. the output looks truncated).
func parseContainers(out string) (containers []Container, complete bool) {
	complete = true
	for _, line := range strings.Split(stripNUL(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var c Container
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			complete = false
			continue
		}
		c.Name = strings.TrimPrefix(c.Name, "/")
		containers = append(containers, c)
	}
	return containers, complete
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
	return r.StreamCommand(ExecShCmd(containerID, script))
}

// ArtisanCommand holds an artisan command name and its description.
type ArtisanCommand struct {
	Name string
	Desc string
}

// ListArtisanCommands returns all available artisan commands with descriptions.
func ListArtisanCommands(r Runner, containerID, rootPath string) ([]ArtisanCommand, error) {
	script := "php " + shellQuote(artisanPath(rootPath)) + " list --raw --no-ansi 2>/dev/null"
	out, _ := runRetry(r, ExecShCmd(containerID, script), 3)
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
	check, _ := r.RunCommand(ExecShCmd(containerID, "command -v composer 2>/dev/null"))
	if strings.TrimSpace(stripNUL(check)) != "" {
		return "composer", nil
	}

	pharPath := root + "/composer.phar"
	exist, _ := r.RunCommand(ExecShCmd(containerID, "test -f "+shellQuote(pharPath)+" && echo yes"))
	if strings.TrimSpace(stripNUL(exist)) != "yes" {
		dl := "cd " + shellQuote(root) + " && " + composerInstallScript
		if out, err := r.RunCommand(ExecShCmd(containerID, dl)); err != nil {
			return "", fmt.Errorf("downloading composer.phar: %w", commandError(err, out))
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
	script := "cd " + shellQuote(root) + " && " + composerBin + " list --no-ansi 2>/dev/null"
	out, _ := runRetry(r, ExecShCmd(containerID, script), 3)
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
	script := "cd " + shellQuote(appRoot(rootPath)) + " && npm run 2>/dev/null"
	out, _ := runRetry(r, ExecShCmd(containerID, script), 3)
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
	return r.StreamCommand(ExecShCmd(containerID, CustomCommandScript(workDir, command)))
}

// TailLaravelLog streams the Laravel log file from inside the container in real-time.
func TailLaravelLog(r Runner, containerID, rootPath string) (<-chan string, func(), int, int, error) {
	return TailLogFile(r, containerID, logsDir(rootPath)+"/laravel.log")
}

// LogFileInfo holds metadata about a log file inside a container.
type LogFileInfo struct {
	Path       string
	Size       int64
	Lines      int
	CreatedAt  time.Time
	ModifiedAt time.Time
}

// fileStatsScript prints one "size|mtime|btime|ctime|lines|path" line for
// every regular file matched by `find <findArgs>`, sorted by path. The path
// comes last so a '|' inside a file name cannot shift the other fields.
// findArgs must already be shell-quoted where needed.
func fileStatsScript(findArgs string) string {
	return `find ` + findArgs + ` 2>/dev/null | sort | while IFS= read -r f; do ` +
		`s=$(stat -c '%s|%Y|%W|%Z' "$f" 2>/dev/null) || s='0|0|0|0'; ` +
		`n=$(wc -l < "$f" 2>/dev/null) || n=0; ` +
		`printf '%s|%s|%s\n' "$s" "$(echo $n)" "$f"; done`
}

// parseFileStats parses fileStatsScript output.
func parseFileStats(out string) []LogFileInfo {
	var files []LogFileInfo
	for _, line := range strings.Split(stripNUL(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 6)
		if len(parts) != 6 || parts[5] == "" {
			continue
		}
		info := LogFileInfo{Path: parts[5]}
		info.Size, _ = strconv.ParseInt(parts[0], 10, 64)
		mtime, _ := strconv.ParseInt(parts[1], 10, 64)
		btime, _ := strconv.ParseInt(parts[2], 10, 64)
		ctime, _ := strconv.ParseInt(parts[3], 10, 64)
		info.Lines, _ = strconv.Atoi(strings.TrimSpace(parts[4]))
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
// with their size, timestamps and line counts, gathered in a single round-trip.
// Retries up to 3 times on empty result to handle transient docker/SSH issues.
func ListLogFiles(r Runner, containerID, rootPath string) ([]LogFileInfo, error) {
	script := fileStatsScript(shellQuote(logsDir(rootPath)) + " -type f")
	out, err := runRetry(r, ExecShCmd(containerID, script), 3)
	if err != nil {
		return nil, fmt.Errorf("list log files: %w", err)
	}
	return parseFileStats(out), nil
}

// LogChunkSize is the number of lines loaded per lazy log chunk.
const LogChunkSize = 1000

// tailFollowScript follows path from inside the container. The shell reads
// stdin so that closing our stdin pipe causes an EOF on read, which then
// kills tail.
func tailFollowScript(path string, initialLines int) string {
	return fmt.Sprintf(`tail -n %d -f %s & PID=$!; read X; kill $PID`, initialLines, shellQuote(path))
}

// TailLogFile loads the last LogChunkSize lines of a file as an initial batch,
// then follows new lines in real-time. Returns the output channel, a stop func,
// and the total/topLine for positioning.
func TailLogFile(r Runner, containerID, filePath string) (<-chan string, func(), int, int, error) {
	// 1. Count total lines
	total, err := CountFileLines(r, containerID, filePath)
	if err != nil || total == 0 {
		// Fallback: plain tail -f with the last chunk as initial output.
		ch, stop, err := r.StreamCommand(ExecShInteractiveCmd(containerID, tailFollowScript(filePath, LogChunkSize)))
		if err != nil {
			return nil, nil, 0, 0, err
		}
		return ch, stop, 0, 0, nil
	}

	// 2. Load the last chunk via the same mechanism as LoadLogChunk
	topLine := total - LogChunkSize + 1
	if topLine < 1 {
		topLine = 1
	}
	initialLines, _, err := LoadLogChunk(r, containerID, filePath, topLine)
	if err != nil {
		return nil, nil, 0, 0, err
	}

	// 3. Follow new lines only.
	tailCh, tailStop, err := r.StreamCommand(ExecShInteractiveCmd(containerID, tailFollowScript(filePath, 0)))
	if err != nil {
		return nil, nil, 0, 0, err
	}

	// 4. Merge: send initial lines first, then the follow stream. quit unblocks
	// the merger when the consumer stops reading before the stream ends.
	ch := make(chan string, 128)
	quit := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() { close(quit) })
		tailStop()
	}
	go func() {
		defer close(ch)
		for _, line := range initialLines {
			select {
			case ch <- line:
			case <-quit:
				return
			}
		}
		for line := range tailCh {
			select {
			case ch <- line:
			case <-quit:
				return
			}
		}
	}()

	return ch, stop, total, topLine, nil
}

// CountFileLines returns the total number of lines in a file inside the container.
// Retries up to 3 times to handle transient SSH issues.
func CountFileLines(r Runner, containerID, filePath string) (int, error) {
	out, err := runRetry(r, ExecShCmd(containerID, "wc -l < "+shellQuote(filePath)), 3)
	if err != nil {
		return 0, fmt.Errorf("count lines in %s: %w", filePath, err)
	}
	n, convErr := strconv.Atoi(out)
	if convErr != nil || n <= 0 {
		return 0, fmt.Errorf("failed to count lines in %s", filePath)
	}
	return n, nil
}

// LoadLogChunk reads lines [fromLine, fromLine+LogChunkSize) from a file inside
// the container (1-based line numbers). Returns the lines and whether the start
// of the file has been reached (fromLine <= 1).
// Retries up to 3 times, keeping the result with the most lines.
func LoadLogChunk(r Runner, containerID, filePath string, fromLine int) ([]string, bool, error) {
	if fromLine < 1 {
		fromLine = 1
	}
	script := fmt.Sprintf(`sed -n '%d,%dp' %s`, fromLine, fromLine+LogChunkSize-1, shellQuote(filePath))
	cmd := ExecShCmd(containerID, script)
	var best []string
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(retryDelay)
		}
		out, err := r.RunCommand(cmd)
		if err != nil {
			continue
		}
		out = strings.TrimRight(stripNUL(out), "\n")
		if out == "" {
			continue
		}
		lines := strings.Split(out, "\n")
		if len(lines) > len(best) {
			best = lines
		}
		// Stop retrying once we have a full chunk, or we're at the top of the
		// file and already got something (further retries won't add more lines).
		if len(best) >= LogChunkSize || (fromLine <= 1 && len(best) > 0) {
			break
		}
	}
	if len(best) == 0 {
		return nil, fromLine <= 1, fmt.Errorf("no lines loaded from %s", filePath)
	}
	return best, fromLine <= 1, nil
}

// TailDockerLogs streams the Docker container logs (stdout/stderr) in real-time.
func TailDockerLogs(r Runner, containerID string) (<-chan string, func(), error) {
	// The shell reads stdin so that closing the SSH stdin pipe causes an EOF
	// on read, which then kills docker logs.
	script := "docker logs -f --tail 100 " + shellQuote(containerID) + " & PID=$!; read X; kill $PID"
	return r.StreamCommand(HostShCmd(script))
}

// HostLog represents a discovered log file on the host server.
type HostLog struct {
	Service    string
	Path       string
	Size       int64
	Lines      int
	CreatedAt  time.Time
	ModifiedAt time.Time
}

// DiscoverContainerLogs finds log files inside the given container by scanning
// /var/log recursively for *.log files and common paths like syslog.
// Retries up to 3 times to handle transient SSH issues.
func DiscoverContainerLogs(r Runner, containerID string) ([]HostLog, error) {
	script := fileStatsScript(`/var/log -type f \( -name '*.log' -o -name 'syslog' \)`)
	out, _ := runRetry(r, ExecShCmd(containerID, script), 3)

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
