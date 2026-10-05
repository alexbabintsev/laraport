package connection

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// interleaved writes N numbered lines to stdout and N to stderr, alternating,
// so stdout and stderr data arrive concurrently.
const interleaved = `i=0; while [ $i -lt 3000 ]; do echo "out-$i"; echo "err-$i" >&2; i=$((i+1)); done`

func countPrefixed(out, prefix string) int {
	n := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

// TestSSHRunCommandCompleteOutput is the regression test for the "incomplete
// data" bug: stdout and stderr are copied on separate goroutines, and writing
// both into one unsynchronised bytes.Buffer lost or zeroed parts of the output.
func TestSSHRunCommandCompleteOutput(t *testing.T) {
	s := newTestSSHServer(t)
	c := connectTest(t, s)

	for round := 0; round < 5; round++ {
		out, err := c.RunCommand(interleaved)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if strings.ContainsRune(out, 0) {
			t.Fatalf("round %d: output contains NUL bytes", round)
		}
		if got := countPrefixed(out, "out-"); got != 3000 {
			t.Fatalf("round %d: got %d stdout lines, want 3000", round, got)
		}
		if got := countPrefixed(out, "err-"); got != 3000 {
			t.Fatalf("round %d: got %d stderr lines, want 3000", round, got)
		}
	}
}

func TestSSHRunCommandExitStatus(t *testing.T) {
	s := newTestSSHServer(t)
	c := connectTest(t, s)

	out, err := c.RunCommand("echo hi; exit 3")
	if err == nil {
		t.Fatal("want error for exit 3")
	}
	if strings.TrimSpace(out) != "hi" {
		t.Fatalf("out = %q", out)
	}
}

func TestSSHRunCommandInput(t *testing.T) {
	s := newTestSSHServer(t)
	c := connectTest(t, s)

	out, err := c.RunCommandInput(`IFS= read -r p; printf '<%s>' "$p"`, "s3cr3t 'x' $y\n")
	if err != nil {
		t.Fatal(err)
	}
	if out != "<s3cr3t 'x' $y>" {
		t.Fatalf("out = %q", out)
	}
}

func TestSSHReconnectsAfterDrop(t *testing.T) {
	s := newTestSSHServer(t)
	c := connectTest(t, s)

	if out, err := c.RunCommand("echo one"); err != nil || strings.TrimSpace(out) != "one" {
		t.Fatalf("first: %q %v", out, err)
	}
	s.dropConnections()

	// The next command must transparently dial a new connection.
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, err := c.RunCommand("echo two")
		if err == nil && strings.TrimSpace(out) == "two" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("did not reconnect: %q %v", out, err)
		}
	}
	if n := s.connectionCount(); n != 1 {
		t.Fatalf("server sees %d connections, want 1", n)
	}
}

func TestSSHChannelRefusalKeepsConnection(t *testing.T) {
	s := newTestSSHServer(t)
	c := connectTest(t, s)

	s.mu.Lock()
	s.refuseSessions = true
	s.mu.Unlock()
	if _, err := c.RunCommand("true"); err == nil {
		t.Fatal("want error while sessions are refused")
	}
	s.mu.Lock()
	s.refuseSessions = false
	s.mu.Unlock()

	if out, err := c.RunCommand("echo ok"); err != nil || strings.TrimSpace(out) != "ok" {
		t.Fatalf("after refusal: %q %v", out, err)
	}
	if n := s.connectionCount(); n != 1 {
		t.Fatalf("connection was replaced: server sees %d connections", n)
	}
}

func TestSSHStreamCommandStopReleasesSlot(t *testing.T) {
	s := newTestSSHServer(t)
	c := connectTest(t, s)

	// Start more never-ending streams than there are session slots, reading
	// nothing, and stop each: every slot must come back.
	for i := 0; i < maxSessions*3; i++ {
		ch, stop, err := c.StreamCommand(`yes line`, "")
		if err != nil {
			t.Fatalf("stream %d: %v", i, err)
		}
		<-ch // at least one line arrived
		stop()
	}
	done := make(chan struct{})
	go func() {
		c.RunCommand("true") //nolint:errcheck
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("session slots leaked: RunCommand blocked")
	}
}

func TestSSHStreamCommandStdinEOFOnStop(t *testing.T) {
	s := newTestSSHServer(t)
	c := connectTest(t, s)

	marker := t.TempDir() + "/stopped"
	script := fmt.Sprintf(`IFS= read -r secret; echo "got $secret"; read _; echo done > %s`, marker)
	ch, stop, err := c.StreamCommand(script, "pw\n")
	if err != nil {
		t.Fatal(err)
	}
	if line := <-ch; line != "got pw" {
		t.Fatalf("line = %q", line)
	}
	stop()
	waitForFile(t, marker)
}

func TestSSHHostKeyTOFUAndMismatch(t *testing.T) {
	s := newTestSSHServer(t)
	kh := emptyKnownHosts(t)

	c, err := connectSSH("127.0.0.1", s.port(), "tester", s.clientKey, "", hostKeyPolicy{path: kh})
	if err != nil {
		t.Fatalf("first use should be accepted: %v", err)
	}
	c.Close()

	// Same server again: known key, accepted.
	c, err = connectSSH("127.0.0.1", s.port(), "tester", s.clientKey, "", hostKeyPolicy{path: kh})
	if err != nil {
		t.Fatalf("known host rejected: %v", err)
	}
	c.Close()

	// A different server (new host key) on the same address must be refused.
	port := s.port()
	s.close()
	s2 := newTestSSHServer(t)
	_ = port
	// Point the recorded entry at the new server's port by re-recording the
	// old key for it: rewrite known_hosts so the new port carries a stale key.
	rewriteKnownHostsPort(t, kh, port, s2.port())
	_, err = connectSSH("127.0.0.1", s2.port(), "tester", s2.clientKey, "", hostKeyPolicy{path: kh})
	if err == nil || !strings.Contains(err.Error(), "host key mismatch") {
		t.Fatalf("want host key mismatch, got %v", err)
	}
}

func TestSSHStrictHostKeys(t *testing.T) {
	s := newTestSSHServer(t)
	kh := emptyKnownHosts(t)
	_, err := connectSSH("127.0.0.1", s.port(), "tester", s.clientKey, "", hostKeyPolicy{path: kh, strict: true})
	if err == nil || !strings.Contains(err.Error(), "unknown host") || !strings.Contains(err.Error(), "SHA256:") ||
		!strings.Contains(err.Error(), "ssh-keyscan -p ") {
		t.Fatalf("strict unknown host: %v", err)
	}
	if data, _ := os.ReadFile(kh); len(data) != 0 {
		t.Fatal("strict mode recorded the key")
	}
	// Once known (recorded by an accept-new connection), strict mode connects.
	c, err := connectSSH("127.0.0.1", s.port(), "tester", s.clientKey, "", hostKeyPolicy{path: kh})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	c, err = connectSSH("127.0.0.1", s.port(), "tester", s.clientKey, "", hostKeyPolicy{path: kh, strict: true})
	if err != nil {
		t.Fatalf("strict known host: %v", err)
	}
	c.Close()
	if got := keyscanTarget("example.com:22"); got != "example.com" {
		t.Fatalf("keyscan target %q", got)
	}
}
