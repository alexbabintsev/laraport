package docker

import (
	"fmt"
	"path"
	"strings"
)

// sqliteSearchDirs are the locations (relative to the app root, plus a couple
// of absolute fallbacks) scanned for SQLite database files.
func sqliteSearchDirs(rootPath string) []string {
	root := strings.TrimSuffix(appRoot(rootPath), "/")
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
	quoted := make([]string, len(dirs))
	for i, d := range dirs {
		quoted[i] = shellQuote(d)
	}
	// -maxdepth 3 keeps the scan cheap; matches the common Laravel layouts.
	// find exits non-zero for missing dirs, which is expected here.
	script := fmt.Sprintf(
		`find %s -maxdepth 3 -type f \( -name '*.sqlite' -o -name '*.sqlite3' -o -name '*.db' \) 2>/dev/null; true`,
		strings.Join(quoted, " "),
	)
	out, err := r.RunOutput(ExecShCmd(containerID, script), "")
	if err != nil {
		return nil, fmt.Errorf("find sqlite files: %w", err)
	}
	return parseDatabaseList(out, nil), nil
}

// SQLiteExecCmd builds the command running one SQL statement against a SQLite
// database file, formatted as a readable table.
func SQLiteExecCmd(containerID, dbPath, sql string) HostCommand {
	// -header -column renders aligned columns with a header row.
	return ExecStreamScript(containerID, "sqlite3 -header -column "+shellQuote(dbPath)+" "+shellQuote(sql))
}

// DumpSQLiteDatabase streams `sqlite3 <file> .dump` (gzipped in the
// container) to ~/Downloads/<file>_<timestamp>.sql.gz.
func DumpSQLiteDatabase(r Runner, containerID, dbPath string) (<-chan string, func(), error) {
	hc := ExecStreamScript(containerID, gzipPipe("sqlite3 "+shellQuote(dbPath)+" .dump"))
	base := strings.TrimSuffix(path.Base(dbPath), path.Ext(dbPath))
	name := fmt.Sprintf("%s_%s.sql.gz", safeFileName(base), timestamp())
	return download(r, hc, "Starting sqlite3 .dump...", name, nil)
}
