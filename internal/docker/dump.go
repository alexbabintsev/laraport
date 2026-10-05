package docker

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// streamBase64Dump runs a host command that emits base64-wrapped binary data
// (e.g. `… | gzip | base64 -w 76`), reassembles it locally, and writes it to
// ~/Downloads/<baseName>_<timestamp>.<ext>. It returns a channel of progress
// lines ending with a "Saved to: <path>" line. startMsg is shown first.
func streamBase64Dump(r Runner, cmd, startMsg, baseName, ext string) (<-chan string, error) {
	rawCh, _, err := r.StreamCommand(cmd)
	if err != nil {
		return nil, fmt.Errorf("dump stream: %w", err)
	}

	outCh := make(chan string, 16)
	go func() {
		defer close(outCh)
		outCh <- startMsg

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
		outPath := filepath.Join(downloadsDir, fmt.Sprintf("%s_%s.%s", baseName, ts, ext))

		if err := os.WriteFile(outPath, decoded, 0o644); err != nil {
			outCh <- fmt.Sprintf("ERROR saving file: %v", err)
			return
		}

		sizeMB := float64(len(decoded)) / 1024 / 1024
		outCh <- fmt.Sprintf("Saved to: %s (%.2f MB)", outPath, sizeMB)
	}()

	return outCh, nil
}

// DumpRedis downloads an RDB snapshot of the Redis instance. `redis-cli --rdb`
// asks the server for a fresh dump and writes it to a file inside the
// container, which is then base64-streamed back and saved as redis_<ts>.rdb.
func DumpRedis(r Runner, containerID, password string) (<-chan string, error) {
	auth := ""
	if password != "" {
		auth = fmt.Sprintf(`--no-auth-warning -a %s `, shellQuote(password))
	}
	// --rdb writes to a path; we target a temp file then stream it out.
	inner := fmt.Sprintf(
		`redis-cli %s--rdb /tmp/laradok-dump.rdb >/dev/null 2>&1 && base64 -w 76 /tmp/laradok-dump.rdb; rm -f /tmp/laradok-dump.rdb`,
		auth,
	)
	cmd := ExecShCmd(containerID, inner)
	return streamBase64Dump(r, cmd, "Starting Redis RDB snapshot...", "redis", "rdb")
}

// DumpMongo downloads a gzipped archive of all MongoDB databases via
// `mongodump --archive --gzip`, saved as mongo_<ts>.archive.gz (restore with
// `mongorestore --archive=… --gzip`).
func DumpMongo(r Runner, containerID, user, password string) (<-chan string, error) {
	// mongodump is a separate binary from the mongo/mongosh shell.
	dumpBin := "mongodump"
	auth := ""
	if user != "" {
		auth = fmt.Sprintf(
			` -u %s -p %s --authenticationDatabase admin`,
			shellQuote(user), shellQuote(password),
		)
	}
	inner := fmt.Sprintf(`%s%s --archive --gzip 2>/dev/null | base64 -w 76`, dumpBin, auth)
	cmd := ExecShCmd(containerID, inner)
	return streamBase64Dump(r, cmd, "Starting mongodump...", "mongo", "archive.gz")
}
