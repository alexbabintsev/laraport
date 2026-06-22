package docker

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// sqliteSearchDirs are the locations (relative to the app root, plus a couple
// of absolute fallbacks) scanned for SQLite database files.
func sqliteSearchDirs(rootPath string) []string {
	root := defaultRootPath
	if rootPath != "" {
		root = strings.TrimRight(rootPath, "/")
	}
	return []string{
		root + "/database",
		root + "/storage",
		root,
		"/data",
	}
}

// ListSQLiteDatabases finds SQLite database files inside the container.
// The returned values are absolute file paths, which double as the "database
// name" passed to the sqlite3 client by the rest of the pipeline.
func ListSQLiteDatabases(r Runner, containerID, rootPath string) ([]string, error) {
	dirs := sqliteSearchDirs(rootPath)
	// -maxdepth 3 keeps the scan cheap; matches the common Laravel layouts.
	quoted := make([]string, len(dirs))
	for i, d := range dirs {
		quoted[i] = shellQuote(d)
	}
	find := fmt.Sprintf(
		`find %s -maxdepth 3 -type f \( -name '*.sqlite' -o -name '*.sqlite3' -o -name '*.db' \) 2>/dev/null`,
		strings.Join(quoted, " "),
	)
	cmd := fmt.Sprintf(`docker exec %s sh -c %s`, containerID, shellQuote(find))

	out, err := r.RunCommand(cmd)
	if err != nil {
		return nil, fmt.Errorf("find sqlite files: %w", err)
	}
	out = strings.Map(func(r rune) rune {
		if r == 0 {
			return -1
		}
		return r
	}, out)

	var dbs []string
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		p := strings.TrimSpace(line)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		dbs = append(dbs, p)
	}
	return dbs, nil
}

// SQLiteExecCmd builds the host command that runs a single SQL statement
// against a SQLite database file, formatted as a readable table.
func SQLiteExecCmd(containerID, dbPath, sql string) string {
	// -header -column renders aligned columns with a header row.
	return fmt.Sprintf(
		`docker exec %s sqlite3 -header -column %s %s`,
		containerID, ShellQuote(dbPath), ShellQuote(sql),
	)
}

// DumpSQLiteDatabase runs `sqlite3 <file> .dump | gzip | base64`, reassembles
// locally, and saves to ~/Downloads/<file>_<timestamp>.sql.gz.
func DumpSQLiteDatabase(r Runner, containerID, dbPath string) (<-chan string, error) {
	cmd := fmt.Sprintf(
		`docker exec %s sh -c 'sqlite3 %s .dump | gzip | base64 -w 76'`,
		containerID, shellQuote(dbPath),
	)
	rawCh, _, err := r.StreamCommand(cmd)
	if err != nil {
		return nil, fmt.Errorf("sqlite3 .dump stream: %w", err)
	}

	outCh := make(chan string, 16)
	go func() {
		defer close(outCh)
		outCh <- "Starting sqlite3 .dump..."

		var b64 strings.Builder
		lineCount := 0
		for line := range rawCh {
			b64.WriteString(strings.TrimSpace(line))
			lineCount++
			if lineCount%5000 == 0 {
				outCh <- fmt.Sprintf("  receiving data... (%d KB)", b64.Len()*3/4/1024)
			}
		}

		if b64.Len() == 0 {
			outCh <- "ERROR: no dump data received"
			return
		}

		outCh <- fmt.Sprintf("  received %d KB, decoding...", b64.Len()*3/4/1024)

		decoded, err := base64.StdEncoding.DecodeString(b64.String())
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(b64.String())
			if err != nil {
				outCh <- fmt.Sprintf("ERROR decoding dump: %v", err)
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

		// Use the file's base name (without extension) for the dump file.
		base := strings.TrimSuffix(filepath.Base(dbPath), filepath.Ext(dbPath))
		if base == "" {
			base = "sqlite"
		}
		ts := time.Now().Format("20060102_150405")
		outPath := filepath.Join(downloadsDir, fmt.Sprintf("%s_%s.sql.gz", base, ts))

		if err := os.WriteFile(outPath, decoded, 0o644); err != nil {
			outCh <- fmt.Sprintf("ERROR saving file: %v", err)
			return
		}

		sizeMB := float64(len(decoded)) / 1024 / 1024
		outCh <- fmt.Sprintf("Saved to: %s (%.2f MB)", outPath, sizeMB)
	}()

	return outCh, nil
}
