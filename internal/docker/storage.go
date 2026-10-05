package docker

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// StatusLinePrefix marks a streamed output line that the OutputScreen should
// render in place, overwriting the previous status line (e.g. a "received N MB"
// counter) instead of appending. It uses control bytes that never occur in real
// command output.
const StatusLinePrefix = "\x00status\x00"

// StorageSize returns the human-readable size of the storage directory inside the container.
func StorageSize(r Runner, containerID, rootPath string) (string, error) {
	dir := storageDir(rootPath)
	cmd := ExecShCmd(containerID, "du -sh "+shellQuote(dir)+" 2>/dev/null | cut -f1")
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(300 * time.Millisecond)
		}
		out, err := r.RunCommand(cmd)
		if err != nil {
			continue
		}
		out = stripNUL(out)
		size := strings.TrimSpace(out)
		if size != "" {
			return size, nil
		}
	}
	return "?", nil
}

// DownloadStorage archives the storage directory inside the container via
// tar | gzip | base64, transfers it line-by-line, decodes locally, and
// saves to ~/Downloads/<containerName>_storage_<timestamp>.tar.gz.
// Returns a channel emitting progress lines and a final "Saved to: <path>" line.
func DownloadStorage(r Runner, containerID, containerName, rootPath string) (<-chan string, error) {
	dir := storageDir(rootPath)
	// tar the directory relative to its parent so archive contains "storage/..."
	parent := filepath.Dir(strings.TrimRight(dir, "/"))
	base := filepath.Base(strings.TrimRight(dir, "/"))

	// -w 76 wraps base64 output at 76 chars per line so bufio.Scanner can read it.
	cmd := ExecShCmd(containerID,
		"tar -czf - -C "+shellQuote(parent)+" "+shellQuote(base)+" 2>/tmp/_ld_err | base64 -w 76; cat /tmp/_ld_err >&2")
	rawCh, _, err := r.StreamCommand(cmd)
	if err != nil {
		return nil, fmt.Errorf("storage archive: %w", err)
	}

	outCh := make(chan string, 16)
	go func() {
		defer close(outCh)
		outCh <- fmt.Sprintf("Archiving %s ...", dir)

		var b64 strings.Builder
		lineCount := 0
		for line := range rawCh {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			b64.WriteString(trimmed)
			lineCount++
			if lineCount%5000 == 0 {
				outCh <- StatusLinePrefix + fmt.Sprintf("  received %d MB", b64.Len()*3/4/1024/1024)
			}
		}

		if b64.Len() == 0 {
			outCh <- "ERROR: no data received — is the storage directory empty or missing?"
			return
		}

		outCh <- fmt.Sprintf("  received %d MB, decoding...", b64.Len()*3/4/1024/1024)

		decoded, err := base64.StdEncoding.DecodeString(b64.String())
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(b64.String())
			if err != nil {
				outCh <- fmt.Sprintf("ERROR decoding archive: %v", err)
				return
			}
		}

		home, err := os.UserHomeDir()
		if err != nil {
			outCh <- fmt.Sprintf("ERROR getting home dir: %v", err)
			return
		}
		downloadsDir := filepath.Join(home, "Downloads")
		_ = os.MkdirAll(downloadsDir, 0o755)

		ts := time.Now().Format("20060102_150405")
		safe := strings.NewReplacer("/", "_", " ", "_").Replace(containerName)
		outPath := filepath.Join(downloadsDir, fmt.Sprintf("%s_storage_%s.tar.gz", safe, ts))

		if err := os.WriteFile(outPath, decoded, 0o644); err != nil {
			outCh <- fmt.Sprintf("ERROR saving file: %v", err)
			return
		}

		sizeMB := float64(len(decoded)) / 1024 / 1024
		outCh <- fmt.Sprintf("Saved to: %s (%.2f MB)", outPath, sizeMB)
	}()

	return outCh, nil
}

// DirEntry is a single file or directory inside the storage browser.
type DirEntry struct {
	Name  string // base name
	Path  string // absolute path inside the container
	IsDir bool
	Size  int64 // bytes (du -sb for dirs, file size for files)
}

// ListDir lists the immediate children of path inside the container with their
// sizes. Directories are sized with du -sb (apparent recursive size), files with
// their byte size. Entries are returned dirs-first then files, each alphabetically.
func ListDir(r Runner, containerID, path string) ([]DirEntry, error) {
	path = strings.TrimRight(path, "/")
	if path == "" {
		path = "/"
	}
	// For each child: emit "<type>\t<size>\t<name>" where type is d or f.
	// du -sb gives bytes; on BusyBox du -sb may be unsupported, fall back to du -sk*1024.
	script := fmt.Sprintf(`cd %s 2>/dev/null || exit 0
for e in * .[!.]* ..?*; do
  [ -e "$e" ] || continue
  if [ -d "$e" ]; then
    sz=$(du -sb "$e" 2>/dev/null | cut -f1)
    [ -z "$sz" ] && sz=$(( $(du -sk "$e" 2>/dev/null | cut -f1) * 1024 ))
    printf 'd\t%%s\t%%s\n' "$sz" "$e"
  else
    sz=$(stat -c %%s "$e" 2>/dev/null || wc -c < "$e" 2>/dev/null)
    printf 'f\t%%s\t%%s\n' "$sz" "$e"
  fi
done
exit 0`, shellQuote(path))

	cmd := ExecShCmd(containerID, script)

	// du/stat on some entries (e.g. /proc) may exit non-zero, so we don't treat a
	// non-zero exit as fatal — the loop still prints valid lines for every entry.
	// Only surface an error when we parsed nothing at all.
	var out string
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(300 * time.Millisecond)
		}
		out, lastErr = r.RunCommand(cmd)
		if strings.TrimSpace(out) != "" {
			break
		}
	}
	out = stripNUL(out)

	var dirs, files []DirEntry
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		var size int64
		fmt.Sscanf(parts[1], "%d", &size)
		childPath := strings.TrimRight(path, "/") + "/" + parts[2]
		e := DirEntry{
			Name:  parts[2],
			Path:  childPath,
			IsDir: parts[0] == "d",
			Size:  size,
		}
		if e.IsDir {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	entries := append(dirs, files...)
	if len(entries) == 0 && lastErr != nil {
		return nil, fmt.Errorf("list dir: %w", lastErr)
	}
	return entries, nil
}

// DownloadPath archives an arbitrary file or directory inside the container via
// tar | gzip | base64, streams it, decodes locally, and saves to
// ~/Downloads/<containerName>_<basename>_<timestamp>.tar.gz. It mirrors
// DownloadStorage but works for any path. Nothing is written on the server, so
// there is no remote artifact to clean up afterwards.
func DownloadPath(r Runner, containerID, containerName, path string) (<-chan string, error) {
	path = strings.TrimRight(path, "/")
	parent := filepath.Dir(path)
	base := filepath.Base(path)

	cmd := ExecShCmd(containerID,
		"tar -czf - -C "+shellQuote(parent)+" "+shellQuote(base)+" 2>/tmp/_ld_err | base64 -w 76; cat /tmp/_ld_err >&2")
	rawCh, _, err := r.StreamCommand(cmd)
	if err != nil {
		return nil, fmt.Errorf("archive %s: %w", path, err)
	}

	outCh := make(chan string, 16)
	go func() {
		defer close(outCh)
		outCh <- fmt.Sprintf("Archiving %s ...", path)

		var b64 strings.Builder
		lineCount := 0
		for line := range rawCh {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			b64.WriteString(trimmed)
			lineCount++
			if lineCount%5000 == 0 {
				outCh <- StatusLinePrefix + fmt.Sprintf("  received %d MB", b64.Len()*3/4/1024/1024)
			}
		}

		if b64.Len() == 0 {
			outCh <- "ERROR: no data received — is the path empty or missing?"
			return
		}

		outCh <- fmt.Sprintf("  received %d MB, decoding...", b64.Len()*3/4/1024/1024)

		decoded, err := base64.StdEncoding.DecodeString(b64.String())
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(b64.String())
			if err != nil {
				outCh <- fmt.Sprintf("ERROR decoding archive: %v", err)
				return
			}
		}

		home, err := os.UserHomeDir()
		if err != nil {
			outCh <- fmt.Sprintf("ERROR getting home dir: %v", err)
			return
		}
		downloadsDir := filepath.Join(home, "Downloads")
		_ = os.MkdirAll(downloadsDir, 0o755)

		ts := time.Now().Format("20060102_150405")
		safe := strings.NewReplacer("/", "_", " ", "_").Replace(containerName)
		safeBase := strings.NewReplacer("/", "_", " ", "_").Replace(base)
		outPath := filepath.Join(downloadsDir, fmt.Sprintf("%s_%s_%s.tar.gz", safe, safeBase, ts))

		if err := os.WriteFile(outPath, decoded, 0o644); err != nil {
			outCh <- fmt.Sprintf("ERROR saving file: %v", err)
			return
		}

		sizeMB := float64(len(decoded)) / 1024 / 1024
		outCh <- fmt.Sprintf("Saved to: %s (%.2f MB)", outPath, sizeMB)
	}()

	return outCh, nil
}

func storageDir(rootPath string) string {
	return strings.TrimSuffix(appRoot(rootPath), "/") + "/storage"
}
