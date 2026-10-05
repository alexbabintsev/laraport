package docker

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DetectMySQLCredentials reads MySQL credentials from container env.
// Prefers MYSQL_USER/MYSQL_PASSWORD; falls back to root with MYSQL_ROOT_PASSWORD.
func DetectMySQLCredentials(r Runner, containerID string) (user, password string, err error) {
	cmd := fmt.Sprintf(`docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' %s`, shellQuote(containerID))
	out, err := r.RunCommand(cmd)
	if err != nil {
		return "", "", fmt.Errorf("docker inspect: %w", err)
	}
	out = stripNUL(out)

	var mysqlUser, mysqlPass, rootPass string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if k, v, ok := strings.Cut(line, "="); ok {
			switch k {
			case "MYSQL_USER":
				mysqlUser = v
			case "MYSQL_PASSWORD":
				mysqlPass = v
			case "MYSQL_ROOT_PASSWORD":
				rootPass = v
			}
		}
	}

	// A non-root MySQL_USER usually cannot SHOW ALL databases, so prefer root
	// when a root password is available; otherwise use the configured user.
	if rootPass != "" {
		return "root", rootPass, nil
	}
	if mysqlUser != "" {
		return mysqlUser, mysqlPass, nil
	}
	return "root", "", nil
}

// mysqlExec builds a `docker exec` invocation running the mysql client with the
// given -e SQL statement. Password is passed via MYSQL_PWD env to avoid the
// "password on command line" warning.
func mysqlExec(containerID, user, password, dbName, sql string) string {
	db := ""
	if dbName != "" {
		db = " " + shellQuote(dbName)
	}
	return fmt.Sprintf(
		`docker exec -e MYSQL_PWD=%s %s mysql -u %s%s -e %s`,
		shellQuote(password), shellQuote(containerID), shellQuote(user), db, shellQuote(sql),
	)
}

// ListMySQLDatabases returns non-system databases from the MySQL container.
func ListMySQLDatabases(r Runner, containerID, user, password string) ([]string, error) {
	cmd := mysqlExec(containerID, user, password, "", "SHOW DATABASES") + " --batch --skip-column-names"
	var out string
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(300 * time.Millisecond)
		}
		raw, err := r.RunCommand(cmd)
		if err != nil {
			continue
		}
		raw = stripNUL(raw)
		out = strings.TrimSpace(raw)
		if out != "" {
			break
		}
	}
	skip := map[string]bool{
		"information_schema": true,
		"performance_schema": true,
		"mysql":              true,
		"sys":                true,
	}
	var dbs []string
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if name == "" || skip[name] || seen[name] {
			continue
		}
		seen[name] = true
		dbs = append(dbs, name)
	}
	return dbs, nil
}

// MySQLExecCmd builds the host command used to run an arbitrary SQL statement
// (with a tabular table-style output) against a MySQL database.
func MySQLExecCmd(containerID, user, password, dbName, sql string) string {
	return fmt.Sprintf(
		`docker exec -e MYSQL_PWD=%s %s mysql -u %s --table %s -e %s`,
		ShellQuote(password), shellQuote(containerID), ShellQuote(user),
		ShellQuote(dbName), ShellQuote(sql),
	)
}

// DumpMySQLDatabase runs mysqldump | gzip | base64 on the container,
// reassembles locally, and saves to ~/Downloads/<dbName>_<timestamp>.sql.gz.
func DumpMySQLDatabase(r Runner, containerID, user, password, dbName string) (<-chan string, error) {
	cmd := fmt.Sprintf(
		`docker exec -e MYSQL_PWD=%s %s sh -c %s`,
		shellQuote(password), shellQuote(containerID),
		shellQuote("mysqldump --no-tablespaces --single-transaction -u "+shellQuote(user)+" "+shellQuote(dbName)+" | gzip | base64 -w 76"),
	)
	rawCh, _, err := r.StreamCommand(cmd)
	if err != nil {
		return nil, fmt.Errorf("mysqldump stream: %w", err)
	}

	outCh := make(chan string, 16)
	go func() {
		defer close(outCh)
		outCh <- "Starting mysqldump..."

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

		ts := time.Now().Format("20060102_150405")
		outPath := filepath.Join(downloadsDir, fmt.Sprintf("%s_%s.sql.gz", dbName, ts))

		if err := os.WriteFile(outPath, decoded, 0o644); err != nil {
			outCh <- fmt.Sprintf("ERROR saving file: %v", err)
			return
		}

		sizeMB := float64(len(decoded)) / 1024 / 1024
		outCh <- fmt.Sprintf("Saved to: %s (%.2f MB)", outPath, sizeMB)
	}()

	return outCh, nil
}
