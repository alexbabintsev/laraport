package docker

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// StorageSize returns the human-readable size of the storage directory inside the container.
func StorageSize(r Runner, containerID, rootPath string) (string, error) {
	out, err := r.RunOutput(ExecShCmd(containerID, "du -sh "+shellQuote(storageDir(rootPath))+" 2>/dev/null | cut -f1"), "")
	if size := strings.TrimSpace(out); size != "" {
		return size, nil
	}
	if err != nil {
		return "?", err
	}
	return "?", nil
}

// archiveScript tars p (relative to its parent so the archive holds
// "<base>/…") to stdout. GNU tar exits 1 for "file changed as we read it",
// which is routine for live log files, so that status is treated as success;
// real failures (status 2+) still fail the download.
func archiveScript(p string) string {
	p = strings.TrimRight(p, "/")
	if p == "" {
		p = "/"
	}
	parent, base := path.Dir(p), path.Base(p)
	return "tar -czf - -C " + shellQuote(parent) + " " + shellQuote(base) + "; s=$?; [ $s -eq 1 ] && s=0; exit $s"
}

// DownloadStorage archives the app's storage directory and streams it to
// ~/Downloads/<container>_storage_<timestamp>.tar.gz.
func DownloadStorage(r Runner, containerID, containerName, rootPath string) (<-chan string, func(), error) {
	dir := storageDir(rootPath)
	hc := ExecStreamScript(containerID, archiveScript(dir))
	name := fmt.Sprintf("%s_storage_%s.tar.gz", safeFileName(containerName), timestamp())
	return download(r, hc, fmt.Sprintf("Archiving %s ...", dir), name, nil)
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

	// The script always exits 0 (du/stat failures on entries such as /proc are
	// tolerated), so an error means docker exec itself failed — but surface it
	// only when nothing could be parsed.
	out, lastErr := r.RunOutput(cmd, "")

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

// DownloadPath archives an arbitrary file or directory inside the container
// and streams it to ~/Downloads/<container>_<basename>_<timestamp>.tar.gz.
// Nothing is written on the server.
func DownloadPath(r Runner, containerID, containerName, p string) (<-chan string, func(), error) {
	hc := ExecStreamScript(containerID, archiveScript(p))
	name := fmt.Sprintf("%s_%s_%s.tar.gz", safeFileName(containerName), safeFileName(path.Base(strings.TrimRight(p, "/"))), timestamp())
	return download(r, hc, fmt.Sprintf("Archiving %s ...", p), name, nil)
}

func storageDir(rootPath string) string {
	return strings.TrimSuffix(appRoot(rootPath), "/") + "/storage"
}
