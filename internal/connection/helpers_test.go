package connection

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// waitForFile waits until path exists (a background script created it).
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s was never created", path)
}

// rewriteKnownHostsPort re-labels known_hosts entries for 127.0.0.1:from as
// 127.0.0.1:to, so a different server on port "to" meets a stale key.
func rewriteKnownHostsPort(t *testing.T, path string, from, to int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := strings.ReplaceAll(string(data), fmt.Sprintf("[127.0.0.1]:%d", from), fmt.Sprintf("[127.0.0.1]:%d", to))
	if out == string(data) {
		t.Fatalf("no entry for port %d in known_hosts:\n%s", from, data)
	}
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
}
