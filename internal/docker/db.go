package docker

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DetectPostgresCredentials reads POSTGRES_USER and POSTGRES_PASSWORD from container env.
func DetectPostgresCredentials(r Runner, containerID string) (user, password string, err error) {
	cmd := fmt.Sprintf(`docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' %s`, shellQuote(containerID))
	out, err := r.RunCommand(cmd)
	if err != nil {
		return "", "", fmt.Errorf("docker inspect: %w", err)
	}
	out = stripNUL(out)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if k, v, ok := strings.Cut(line, "="); ok {
			switch k {
			case "POSTGRES_USER":
				user = v
			case "POSTGRES_PASSWORD":
				password = v
			}
		}
	}
	if user == "" {
		user = "postgres"
	}
	return user, password, nil
}

// ListDatabases returns non-system databases from the PostgreSQL container.
func ListDatabases(r Runner, containerID, user, password string) ([]string, error) {
	cmd := fmt.Sprintf(
		`docker exec -e PGPASSWORD=%s -e PGUSER=%s %s psql -lqt --no-align --field-separator '|'`,
		shellQuote(password), shellQuote(user), shellQuote(containerID),
	)
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
		"template0": true,
		"template1": true,
		"postgres":  true,
	}
	var dbs []string
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, "|")
		// Real DB lines have at least 2 fields (name|owner|...).
		// ACL continuation lines have no | separator and contain '=' or '/'.
		if len(parts) < 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		if name == "" || skip[name] || seen[name] {
			continue
		}
		// DB names never contain '=' or '/' — skip ACL fragments that leaked through.
		if strings.ContainsAny(name, "=/") {
			continue
		}
		seen[name] = true
		dbs = append(dbs, name)
	}
	return dbs, nil
}

// ExecSQL runs a SQL query inside the container and streams output.
func ExecSQL(r Runner, containerID, user, password, dbName, sql string) (<-chan string, error) {
	cmd := fmt.Sprintf(
		`docker exec -e PGPASSWORD=%s %s psql -U %s -d %s -c %s`,
		shellQuote(password), shellQuote(containerID),
		shellQuote(user), shellQuote(dbName), shellQuote(sql),
	)
	ch, _, err := r.StreamCommand(cmd)
	return ch, err
}

// ExecPsqlMeta runs a psql meta-command (e.g. \dt) inside the container and streams output.
func ExecPsqlMeta(r Runner, containerID, user, password, dbName, metacmd string) (<-chan string, error) {
	cmd := fmt.Sprintf(
		`docker exec -e PGPASSWORD=%s %s psql -U %s -d %s -c %s`,
		shellQuote(password), shellQuote(containerID),
		shellQuote(user), shellQuote(dbName),
		shellQuote(metacmd),
	)
	ch, _, err := r.StreamCommand(cmd)
	return ch, err
}

// DumpDatabase runs pg_dump (plain SQL) | gzip | base64 on the container,
// reassembles locally, saves to ~/Downloads/<dbName>_<timestamp>.sql.gz.
// Returns a channel that emits progress lines and a final "Saved to: <path>" line.
func DumpDatabase(r Runner, containerID, user, password, dbName string) (<-chan string, error) {
	return dumpDatabase(r, containerID, user, password, dbName, false, false)
}

// DumpDatabaseInserts is like DumpDatabase but adds --inserts --column-inserts
// for a more portable but slower dump.
func DumpDatabaseInserts(r Runner, containerID, user, password, dbName string) (<-chan string, error) {
	return dumpDatabase(r, containerID, user, password, dbName, false, true)
}

// DumpDatabaseCustom runs pg_dump --format=custom | base64, saves as .dump
// (suitable for pg_restore with selective restore).
func DumpDatabaseCustom(r Runner, containerID, user, password, dbName string) (<-chan string, error) {
	return dumpDatabase(r, containerID, user, password, dbName, true, false)
}

func dumpDatabase(r Runner, containerID, user, password, dbName string, customFormat bool, inserts bool) (<-chan string, error) {
	pipeline := "pg_dump --no-owner --no-acl | gzip | base64 -w 76"
	if customFormat {
		// -Fc produces a compressed binary archive — no need for gzip.
		pipeline = "pg_dump --no-owner --no-acl -Fc | base64 -w 76"
	} else if inserts {
		// --inserts --column-inserts: slower, more portable (INSERT statements instead of COPY).
		pipeline = "pg_dump --no-owner --no-acl --inserts --column-inserts | gzip | base64 -w 76"
	}
	cmd := fmt.Sprintf(
		`docker exec -e PGPASSWORD=%s -e PGUSER=%s -e PGDATABASE=%s %s sh -c %s`,
		shellQuote(password), shellQuote(user), shellQuote(dbName), shellQuote(containerID), shellQuote(pipeline),
	)
	rawCh, _, err := r.StreamCommand(cmd)
	if err != nil {
		return nil, fmt.Errorf("pg_dump stream: %w", err)
	}

	outCh := make(chan string, 16)
	go func() {
		defer close(outCh)
		outCh <- "Starting pg_dump..."

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
			// Try raw (no padding)
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
		ext := "sql.gz"
		if customFormat {
			ext = "dump"
		}

		// Strip license/restriction lines from plain SQL dumps before re-compressing.
		if !customFormat {
			decoded = filterDumpLines(decoded)
		}

		outPath := filepath.Join(downloadsDir, fmt.Sprintf("%s_%s.%s", dbName, ts, ext))

		if err := os.WriteFile(outPath, decoded, 0o644); err != nil {
			outCh <- fmt.Sprintf("ERROR saving file: %v", err)
			return
		}

		sizeMB := float64(len(decoded)) / 1024 / 1024
		outCh <- fmt.Sprintf("Saved to: %s (%.2f MB)", outPath, sizeMB)
	}()

	return outCh, nil
}

// filterDumpLines decompresses a gzipped SQL dump, removes known restriction
// lines, and re-compresses. Returns original bytes on any error.
func filterDumpLines(data []byte) []byte {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return data
	}
	defer gr.Close()

	var filtered bytes.Buffer
	gw := gzip.NewWriter(&filtered)

	scanner := bufio.NewScanner(gr)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, `\restrict `) || strings.HasPrefix(line, `\unrestrict `) {
			continue
		}
		gw.Write([]byte(line + "\n")) //nolint:errcheck
	}
	if err := scanner.Err(); err != nil {
		return data
	}
	if err := gw.Close(); err != nil {
		return data
	}
	return filtered.Bytes()
}
