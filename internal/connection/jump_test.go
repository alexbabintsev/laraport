package connection

import (
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// jumpOpts builds options reaching target through bastion with the keys the
// test servers authorise.
func jumpOpts(target, bastion *testSSHServer) SSHOptions {
	return SSHOptions{
		Host: "127.0.0.1", Port: target.port(), User: "tester", KeyPath: target.clientKey,
		Jump: &JumpHost{Host: "127.0.0.1", Port: bastion.port(), User: "jumper", KeyPath: bastion.clientKey},
	}
}

func TestJumpHostConnects(t *testing.T) {
	target, bastion := newTestSSHServer(t), newTestSSHServer(t)
	kh := emptyKnownHosts(t)
	c, err := connectSSH(jumpOpts(target, bastion), hostKeyPolicy{path: kh})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	out, err := c.RunCommand(interleaved)
	if err != nil || countPrefixed(out, "out-") != 3000 || countPrefixed(out, "err-") != 3000 {
		t.Fatalf("command via jump host: %v", err)
	}
	if bastion.forwardCount() != 1 {
		t.Fatalf("bastion forwards = %d", bastion.forwardCount())
	}
	// Both host keys were recorded, each under its own name.
	data, _ := os.ReadFile(kh)
	for _, p := range []int{target.port(), bastion.port()} {
		if !strings.Contains(string(data), "[127.0.0.1]:"+itoaPort(p)) {
			t.Fatalf("known_hosts lacks port %d:\n%s", p, data)
		}
	}
}

func itoaPort(p int) string { return strconv.Itoa(p) }

func TestJumpHostReconnectsAfterBastionDrop(t *testing.T) {
	target, bastion := newTestSSHServer(t), newTestSSHServer(t)
	c, err := connectSSH(jumpOpts(target, bastion), hostKeyPolicy{path: emptyKnownHosts(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.RunCommand("true"); err != nil {
		t.Fatal(err)
	}
	bastion.dropConnections() // the bastion goes away; the tunnel dies with it

	deadline := time.Now().Add(10 * time.Second)
	for {
		out, err := c.RunCommand("echo back")
		if err == nil && strings.TrimSpace(out) == "back" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("did not reconnect via the bastion: %q %v", out, err)
		}
	}
	if bastion.forwardCount() != 2 {
		t.Fatalf("expected a second tunnel, got %d", bastion.forwardCount())
	}
}

func TestJumpHostCloseClosesBoth(t *testing.T) {
	target, bastion := newTestSSHServer(t), newTestSSHServer(t)
	c, err := connectSSH(jumpOpts(target, bastion), hostKeyPolicy{path: emptyKnownHosts(t)})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	waitUntil(t, func() bool {
		// The server side notices the closed connections when reading fails.
		return connClosed(bastion) && connClosed(target)
	})
}

// connClosed reports whether every connection the server accepted is closed.
func connClosed(s *testSSHServer) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		c.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
		buf := make([]byte, 1)
		if _, err := c.Read(buf); err == nil || isTimeout(err) {
			return false
		}
	}
	return true
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}

func TestJumpHostErrors(t *testing.T) {
	target, bastion := newTestSSHServer(t), newTestSSHServer(t)

	// Wrong key for the bastion: the error names the jump host.
	opts := jumpOpts(target, bastion)
	opts.Jump.KeyPath = target.clientKey
	if _, err := connectSSH(opts, hostKeyPolicy{path: emptyKnownHosts(t)}); err == nil || !strings.Contains(err.Error(), "jump host") {
		t.Fatalf("bad bastion key: %v", err)
	}

	// Bastion is fine, target unreachable from it.
	opts = jumpOpts(target, bastion)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	opts.Port = ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if _, err := connectSSH(opts, hostKeyPolicy{path: emptyKnownHosts(t)}); err == nil || !strings.Contains(err.Error(), "jump host could not reach") {
		t.Fatalf("unreachable target: %v", err)
	}

	// Bastion refuses forwarding.
	bastion.mu.Lock()
	bastion.noForwarding = true
	bastion.mu.Unlock()
	if _, err := connectSSH(jumpOpts(target, bastion), hostKeyPolicy{path: emptyKnownHosts(t)}); err == nil ||
		!strings.Contains(err.Error(), "forwarding is disabled") {
		t.Fatalf("forwarding refused: %v", err)
	}

	// Wrong key for the target (bastion OK): handshake error via jump host.
	bastion.mu.Lock()
	bastion.noForwarding = false
	bastion.mu.Unlock()
	opts = jumpOpts(target, bastion)
	opts.KeyPath = bastion.clientKey
	if _, err := connectSSH(opts, hostKeyPolicy{path: emptyKnownHosts(t)}); err == nil || !strings.Contains(err.Error(), "via jump host") {
		t.Fatalf("bad target key: %v", err)
	}
}

func TestJumpHostStrictAppliesToBastion(t *testing.T) {
	target, bastion := newTestSSHServer(t), newTestSSHServer(t)
	kh := emptyKnownHosts(t)
	_, err := connectSSH(jumpOpts(target, bastion), hostKeyPolicy{path: kh, strict: true})
	if err == nil || !strings.Contains(err.Error(), "jump host") || !strings.Contains(err.Error(), "unknown host") {
		t.Fatalf("strict unknown bastion: %v", err)
	}
	// Record both (accept-new), then strict works.
	c, err := connectSSH(jumpOpts(target, bastion), hostKeyPolicy{path: kh})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	c, err = connectSSH(jumpOpts(target, bastion), hostKeyPolicy{path: kh, strict: true})
	if err != nil {
		t.Fatalf("strict known hosts: %v", err)
	}
	c.Close()
}

func TestDialViaTimeout(t *testing.T) {
	// A "target" that accepts TCP but never speaks SSH: the handshake through
	// the tunnel must time out instead of hanging.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	bastion := newTestSSHServer(t)
	c := connectTest(t, bastion)
	jc, err := c.conn()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = dialVia(jc, ln.Addr().String(), c.config, 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") || time.Since(start) > 3*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
}
