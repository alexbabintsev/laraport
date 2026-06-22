package docker

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const defaultRootPath = "/var/www/html"

func artisanPath(rootPath string) string {
	if rootPath == "" {
		return defaultRootPath + "/artisan"
	}
	return strings.TrimRight(rootPath, "/") + "/artisan"
}

func logsDir(rootPath string) string {
	if rootPath == "" {
		return defaultRootPath + "/storage/logs"
	}
	return strings.TrimRight(rootPath, "/") + "/storage/logs"
}

// Runner is an interface implemented by both SSHClient and LocalClient.
type Runner interface {
	RunCommand(cmd string) (string, error)
	StreamCommand(cmd string) (<-chan string, func(), error)
	InteractiveCommand(cmd string) (<-chan string, chan<- string, func(), error)
	TailFile(path string) (<-chan string, func(), error)
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

// ListContainers returns all running containers on the target host.
// Retries up to 5 times, continuing as long as the result keeps growing — this handles
// truncated SSH output right after connection open where docker ps output arrives partially.
func ListContainers(r Runner) ([]Container, error) {
	format := `{"id":"{{.ID}}","name":"{{.Names}}","image":"{{.Image}}","state":"{{.State}}","status":"{{.Status}}","ports":"{{.Ports}}"}`
	cmd := fmt.Sprintf(`docker ps --format '%s'`, format)

	var best []Container
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			time.Sleep(300 * time.Millisecond)
		}
		out, err := r.RunCommand(cmd)
		if err != nil {
			return nil, fmt.Errorf("docker ps: %w\n%s", err, out)
		}
		// Strip null bytes that can appear in SSH output right after connection open
		out = strings.Map(func(r rune) rune {
			if r == 0 {
				return -1
			}
			return r
		}, out)

		var containers []Container
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var c Container
			if err := json.Unmarshal([]byte(line), &c); err != nil {
				continue
			}
			c.Name = strings.TrimPrefix(c.Name, "/")
			containers = append(containers, c)
		}

		if len(containers) > len(best) {
			best = containers
		}

		// Stop early only if we got a stable non-empty result on two consecutive equal reads.
		// A simple heuristic: if result didn't grow from previous best and we have something, done.
		if len(containers) > 0 && len(containers) == len(best) && attempt > 0 {
			break
		}
	}
	return best, nil
}

// ExecArtisan runs an artisan command inside a container and streams the output.
func ExecArtisan(r Runner, containerID, rootPath, artisanCmd string) (<-chan string, error) {
	cmd := fmt.Sprintf(`docker exec %s php %s %s`, containerID, artisanPath(rootPath), artisanCmd)
	ch, _, err := r.StreamCommand(cmd)
	return ch, err
}

// ArtisanCommand holds an artisan command name and its description.
type ArtisanCommand struct {
	Name string
	Desc string
}

// ListArtisanCommands returns all available artisan commands with descriptions.
func ListArtisanCommands(r Runner, containerID, rootPath string) ([]ArtisanCommand, error) {
	cmd := fmt.Sprintf(`docker exec %s php %s list --raw --no-ansi 2>/dev/null`, containerID, artisanPath(rootPath))
	var lastOut string
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(300 * time.Millisecond)
		}
		raw, err := r.RunCommand(cmd)
		if err != nil {
			continue
		}
		raw = strings.Map(func(r rune) rune {
			if r == 0 {
				return -1
			}
			return r
		}, raw)
		lastOut = strings.TrimSpace(raw)
		if lastOut != "" {
			break
		}
	}
	var cmds []ArtisanCommand
	for _, line := range strings.Split(lastOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: "command:name   Description text here"
		// Split on 2+ spaces to separate name from description
		idx := strings.Index(line, "  ")
		if idx < 0 {
			cmds = append(cmds, ArtisanCommand{Name: line})
			continue
		}
		name := strings.TrimSpace(line[:idx])
		desc := strings.TrimSpace(line[idx:])
		if name != "" {
			cmds = append(cmds, ArtisanCommand{Name: name, Desc: desc})
		}
	}
	return cmds, nil
}

// resolveComposer returns the shell invocation for composer inside the container.
// It prefers the system `composer` binary. If not found, it ensures composer.phar
// exists in root (downloading it if needed) and returns "php <root>/composer.phar".
func resolveComposer(r Runner, containerID, root string) (string, error) {
	// Check for system composer
	check, _ := r.RunCommand(fmt.Sprintf(`docker exec %s sh -c "command -v composer 2>/dev/null"`, containerID))
	if strings.TrimSpace(check) != "" {
		return "composer", nil
	}

	pharPath := root + "/composer.phar"

	// Check if composer.phar already exists
	exist, _ := r.RunCommand(fmt.Sprintf(`docker exec %s sh -c "test -f %s && echo yes"`, containerID, pharPath))
	if strings.TrimSpace(exist) != "yes" {
		// Download composer.phar
		dl := fmt.Sprintf(
			`docker exec %s sh -c "cd %s && php -r \"copy('https://getcomposer.org/installer', 'composer-setup.php');\" && php composer-setup.php --quiet && rm composer-setup.php"`,
			containerID, root,
		)
		if _, err := r.RunCommand(dl); err != nil {
			return "", fmt.Errorf("downloading composer.phar: %w", err)
		}
	}

	return "php " + pharPath, nil
}

// ListComposerCommands returns available composer commands with descriptions.
// Also returns the resolved composer binary string (e.g. "composer" or "php /var/www/html/composer.phar").
func ListComposerCommands(r Runner, containerID, rootPath string) ([]ArtisanCommand, string, error) {
	root := defaultRootPath
	if rootPath != "" {
		root = strings.TrimRight(rootPath, "/")
	}

	composerBin, err := resolveComposer(r, containerID, root)
	if err != nil {
		return nil, "", err
	}

	cmd := fmt.Sprintf(`docker exec %s sh -c "cd %s && %s list --no-ansi 2>/dev/null"`, containerID, root, composerBin)
	var lastOut string
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(300 * time.Millisecond)
		}
		raw, err := r.RunCommand(cmd)
		if err != nil {
			continue
		}
		raw = strings.Map(func(r rune) rune {
			if r == 0 {
				return -1
			}
			return r
		}, raw)
		lastOut = strings.TrimSpace(raw)
		if lastOut != "" {
			break
		}
	}
	return parseComposerCommands(lastOut), composerBin, nil
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
		// Format: "  name    Description"  (leading spaces, then 2+ spaces between name and desc)
		idx := strings.Index(line, "  ")
		if idx < 0 {
			cmds = append(cmds, ArtisanCommand{Name: line})
			continue
		}
		name := strings.TrimSpace(line[:idx])
		desc := strings.TrimSpace(line[idx:])
		if name != "" {
			cmds = append(cmds, ArtisanCommand{Name: name, Desc: desc})
		}
	}
	return cmds
}

// ListNpmCommands returns available npm scripts from package.json with descriptions.
func ListNpmCommands(r Runner, containerID, rootPath string) ([]ArtisanCommand, error) {
	root := defaultRootPath
	if rootPath != "" {
		root = strings.TrimRight(rootPath, "/")
	}
	// Use npm run (lists scripts) and also check npm help for built-in commands
	cmd := fmt.Sprintf(`docker exec %s sh -c "cd %s && npm run 2>/dev/null"`, containerID, root)
	var lastOut string
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(300 * time.Millisecond)
		}
		raw, err := r.RunCommand(cmd)
		if err != nil {
			continue
		}
		raw = strings.Map(func(r rune) rune {
			if r == 0 {
				return -1
			}
			return r
		}, raw)
		lastOut = strings.TrimSpace(raw)
		if lastOut != "" {
			break
		}
	}
	return parseNpmCommands(lastOut), nil
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
		// Script name lines have no leading spaces in trimmed form and don't start with special chars
		if !strings.HasPrefix(lines[i], "  ") {
			continue
		}
		// Two-space indent = script name; four-space indent = script body
		if strings.HasPrefix(lines[i], "    ") {
			continue
		}
		name := line
		desc := ""
		// Next line (if 4-space indent) is the script body — use as description
		if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "    ") {
			desc = strings.TrimSpace(lines[i+1])
			i++ // consume the body line
		}
		if name != "" {
			cmds = append(cmds, ArtisanCommand{Name: name, Desc: desc})
		}
	}
	return cmds
}

// ExecCustomCommand runs an arbitrary shell command inside a container and streams the output.
func ExecCustomCommand(r Runner, containerID, command string) (<-chan string, error) {
	cmd := fmt.Sprintf(`docker exec %s sh -c %q`, containerID, command)
	ch, _, err := r.StreamCommand(cmd)
	return ch, err
}

// TailLaravelLog streams the Laravel log file from inside the container in real-time.
func TailLaravelLog(r Runner, containerID, rootPath string) (<-chan string, func(), int, int, error) {
	return TailLogFile(r, containerID, logsDir(rootPath)+"/laravel.log")
}

// LogFileInfo holds metadata about a log file inside a container.
type LogFileInfo struct {
	Path      string
	Size      int64
	Lines     int
	CreatedAt time.Time
	ModifiedAt time.Time
}

// ListLogFiles returns all files in the container's storage/logs directory.
// Retries up to 3 times on empty result to handle transient docker/SSH issues.
// sort is done in Go to avoid shell-pipe silently masking docker exec failures.
func ListLogFiles(r Runner, containerID, rootPath string) ([]LogFileInfo, error) {
	// Use stat to get size, modification time, and birth time (if available).
	// Format: path|size|mtime_epoch|ctime_epoch
	// We use find + stat; birth time (%W) may be 0 on Linux — fall back to ctime (%Z).
	cmd := fmt.Sprintf(
		`docker exec %s sh -c "find %s -type f | sort | xargs -I{} stat -c '{}|%%s|%%Y|%%W|%%Z' {} 2>/dev/null"`,
		containerID, logsDir(rootPath),
	)

	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(300 * time.Millisecond)
		}
		out, err := r.RunCommand(cmd)
		if err != nil {
			return nil, fmt.Errorf("list log files: %w\n%s", err, out)
		}
		out = strings.Map(func(r rune) rune {
			if r == 0 {
				return -1
			}
			return r
		}, out)
		var files []LogFileInfo
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.Split(line, "|")
			if len(parts) != 5 {
				// Fallback: just the path
				files = append(files, LogFileInfo{Path: line})
				continue
			}
			info := LogFileInfo{Path: parts[0]}
			fmt.Sscanf(parts[1], "%d", &info.Size)
			var mtime, btime, ctime int64
			fmt.Sscanf(parts[2], "%d", &mtime)
			fmt.Sscanf(parts[3], "%d", &btime)
			fmt.Sscanf(parts[4], "%d", &ctime)
			info.ModifiedAt = time.Unix(mtime, 0)
			if btime > 0 {
				info.CreatedAt = time.Unix(btime, 0)
			} else {
				info.CreatedAt = time.Unix(ctime, 0)
			}
			files = append(files, info)
		}
		if len(files) == 0 {
			continue
		}
		// Fetch line counts for all files in one pass
		paths := make([]string, len(files))
		for i, f := range files {
			paths[i] = f.Path
		}
		wcCmd := fmt.Sprintf(
			`docker exec %s sh -c "wc -l %s 2>/dev/null"`,
			containerID, strings.Join(paths, " "),
		)
		wcOut, _ := r.RunCommand(wcCmd)
		wcOut = strings.Map(func(r rune) rune {
			if r == 0 {
				return -1
			}
			return r
		}, wcOut)
		lineCounts := make(map[string]int)
		for _, wl := range strings.Split(strings.TrimSpace(wcOut), "\n") {
			wl = strings.TrimSpace(wl)
			if wl == "" || strings.HasPrefix(wl, "total") {
				continue
			}
			var n int
			var p string
			if _, err := fmt.Sscanf(wl, "%d %s", &n, &p); err == nil && p != "" {
				lineCounts[p] = n
			}
		}
		for i := range files {
			files[i].Lines = lineCounts[files[i].Path]
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		return files, nil
	}
	return nil, nil
}

const LogChunkSize = 1000

// TailLogFile loads the last LogChunkSize lines of a file as an initial batch,
// then follows new lines in real-time. Returns the output channel, a stop func,
// and the total/topLine for positioning.
func TailLogFile(r Runner, containerID, filePath string) (<-chan string, func(), int, int, error) {
	// 1. Count total lines
	total, err := CountFileLines(r, containerID, filePath)
	if err != nil || total == 0 {
		// Fallback: stream with tail -f. The shell reads stdin so that closing
		// the SSH stdin pipe causes an EOF on read, which then kills tail.
		cmd := fmt.Sprintf(
			`docker exec -i %s sh -c 'tail -n %d -f %s & PID=$!; read X; kill $PID'`,
			containerID, LogChunkSize, filePath,
		)
		ch, stop, err := r.StreamCommand(cmd)
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

	// 3. Start tail -f -n 0 (follow only, no initial output) for new lines.
	// The shell reads stdin so that closing the SSH stdin pipe causes an EOF
	// on read, which then kills tail inside the container.
	cmd := fmt.Sprintf(
		`docker exec -i %s sh -c 'tail -n 0 -f %s & PID=$!; read X; kill $PID'`,
		containerID, filePath,
	)
	tailCh, tailStop, err := r.StreamCommand(cmd)
	if err != nil {
		return nil, nil, 0, 0, err
	}

	// 4. Merge: send initial lines first, then follow stream
	ch := make(chan string, 128)
	go func() {
		defer close(ch)
		for _, line := range initialLines {
			ch <- line
		}
		for line := range tailCh {
			ch <- line
		}
	}()

	return ch, tailStop, total, topLine, nil
}

// CountFileLines returns the total number of lines in a file inside the container.
// Retries up to 3 times to handle transient SSH issues.
func CountFileLines(r Runner, containerID, filePath string) (int, error) {
	cmd := fmt.Sprintf(`docker exec %s sh -c "wc -l < %s"`, containerID, filePath)
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(300 * time.Millisecond)
		}
		out, err := r.RunCommand(cmd)
		if err != nil {
			continue
		}
		out = strings.Map(func(r rune) rune {
			if r == 0 {
				return -1
			}
			return r
		}, out)
		out = strings.TrimSpace(out)
		var n int
		if _, err := fmt.Sscanf(out, "%d", &n); err == nil && n > 0 {
			return n, nil
		}
	}
	return 0, fmt.Errorf("failed to count lines in %s", filePath)
}

// LoadLogChunk reads lines [fromLine, fromLine+logChunkSize) from a file inside
// the container (1-based line numbers). Returns the lines and whether the start
// of the file has been reached (fromLine <= 1).
// Retries up to 3 times, keeping the result with the most lines.
func LoadLogChunk(r Runner, containerID, filePath string, fromLine int) ([]string, bool, error) {
	if fromLine < 1 {
		fromLine = 1
	}
	cmd := fmt.Sprintf(
		`docker exec %s sh -c "sed -n '%d,%dp' %s"`,
		containerID, fromLine, fromLine+LogChunkSize-1, filePath,
	)
	var best []string
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(300 * time.Millisecond)
		}
		out, err := r.RunCommand(cmd)
		if err != nil {
			continue
		}
		out = strings.Map(func(r rune) rune {
			if r == 0 {
				return -1
			}
			return r
		}, out)
		out = strings.TrimRight(out, "\n")
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
	cmd := fmt.Sprintf(
		`sh -c 'docker logs -f --tail 100 %s & PID=$!; read X; kill $PID'`,
		containerID,
	)
	ch, stop, err := r.StreamCommand(cmd)
	if err != nil {
		return nil, nil, err
	}
	return ch, stop, nil
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
	cmd := fmt.Sprintf(
		`docker exec %s sh -c "find /var/log -type f \( -name '*.log' -o -name 'syslog' \) 2>/dev/null | sort | xargs -I{} stat -c '{}|%%s|%%Y|%%W|%%Z' {} 2>/dev/null"`,
		containerID,
	)

	var lastOut string
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(300 * time.Millisecond)
		}
		raw, _ := r.RunCommand(cmd)
		raw = strings.Map(func(r rune) rune {
			if r == 0 {
				return -1
			}
			return r
		}, raw)
		lastOut = strings.TrimSpace(raw)
		if lastOut != "" {
			break
		}
	}

	var logs []HostLog
	seen := make(map[string]bool)
	for _, line := range strings.Split(lastOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		var log HostLog
		if len(parts) == 5 {
			log.Path = parts[0]
			fmt.Sscanf(parts[1], "%d", &log.Size)
			var mtime, btime, ctime int64
			fmt.Sscanf(parts[2], "%d", &mtime)
			fmt.Sscanf(parts[3], "%d", &btime)
			fmt.Sscanf(parts[4], "%d", &ctime)
			log.ModifiedAt = time.Unix(mtime, 0)
			if btime > 0 {
				log.CreatedAt = time.Unix(btime, 0)
			} else {
				log.CreatedAt = time.Unix(ctime, 0)
			}
		} else {
			log.Path = line
		}
		if log.Path == "" || seen[log.Path] {
			continue
		}
		seen[log.Path] = true
		log.Service = containerLogService(log.Path)
		logs = append(logs, log)
	}

	if len(logs) == 0 {
		return logs, nil
	}

	// Fetch line counts for all files in one pass
	paths := make([]string, len(logs))
	for i, l := range logs {
		paths[i] = l.Path
	}
	wcCmd := fmt.Sprintf(
		`docker exec %s sh -c "wc -l %s 2>/dev/null"`,
		containerID, strings.Join(paths, " "),
	)
	wcOut, _ := r.RunCommand(wcCmd)
	wcOut = strings.Map(func(r rune) rune {
		if r == 0 {
			return -1
		}
		return r
	}, wcOut)
	lineCounts := make(map[string]int)
	for _, wl := range strings.Split(strings.TrimSpace(wcOut), "\n") {
		wl = strings.TrimSpace(wl)
		if wl == "" || strings.HasPrefix(wl, "total") {
			continue
		}
		var n int
		var p string
		if _, err := fmt.Sscanf(wl, "%d %s", &n, &p); err == nil && p != "" {
			lineCounts[p] = n
		}
	}
	for i := range logs {
		logs[i].Lines = lineCounts[logs[i].Path]
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
