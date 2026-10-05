package connection

// Integration test against real OpenSSH servers in Docker: a bastion with a
// published port, and a target reachable only from the bastion's network.
//
// Run with: LARAPORT_INTEGRATION=1 go test ./internal/connection/

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func requireDocker(t *testing.T) {
	t.Helper()
	if os.Getenv("LARAPORT_INTEGRATION") != "1" {
		t.Skip("set LARAPORT_INTEGRATION=1 to run Docker integration tests")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker not available: %v", err)
	}
}

func docker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// sshdScript installs and runs OpenSSH in an alpine container, authorising
// $PUBKEY for user "tester".
const sshdScript = `set -e
apk add --no-cache openssh >/dev/null
ssh-keygen -A >/dev/null
adduser -D -s /bin/sh tester
echo 'tester:*' | chpasswd -e >/dev/null 2>&1 || sed -i 's/^tester:!/tester:*/' /etc/shadow
mkdir -p /home/tester/.ssh
echo "$PUBKEY" > /home/tester/.ssh/authorized_keys
chown -R tester /home/tester/.ssh && chmod 700 /home/tester/.ssh && chmod 600 /home/tester/.ssh/authorized_keys
exec /usr/sbin/sshd -D -e -o AllowTcpForwarding=yes -o PasswordAuthentication=no`

func TestIntegrationRealOpenSSHJumpHost(t *testing.T) {
	requireDocker(t)

	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	pub, _ := ssh.NewPublicKey(priv.Public())
	block, _ := ssh.MarshalPrivateKey(priv, "")
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600)
	pubLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))

	network := fmt.Sprintf("laraport-test-%d", time.Now().UnixNano())
	docker(t, "network", "create", "--label", "laraport-test=1", network)
	t.Cleanup(func() { exec.Command("docker", "network", "rm", network).Run() }) //nolint:errcheck

	run := func(name string, publish bool) string {
		args := []string{"run", "-d", "--label", "laraport-test=1", "--network", network, "--network-alias", name, "-e", "PUBKEY=" + pubLine}
		if publish {
			args = append(args, "-p", "127.0.0.1::22")
		}
		args = append(args, "alpine:3.20", "sh", "-c", sshdScript)
		id := docker(t, args...)
		t.Cleanup(func() { exec.Command("docker", "rm", "-f", id).Run() }) //nolint:errcheck
		return id
	}
	target := run("target", false)
	bastion := run("bastion", true)
	_ = target

	portOut := docker(t, "port", bastion, "22/tcp")
	_, portStr, _ := net.SplitHostPort(strings.Split(portOut, "\n")[0])
	bastionPort, _ := strconv.Atoi(portStr)

	opts := SSHOptions{
		Host: "target", Port: 22, User: "tester", KeyPath: keyPath,
		Jump: &JumpHost{Host: "127.0.0.1", Port: bastionPort, User: "tester", KeyPath: keyPath},
	}
	kh := emptyKnownHosts(t)

	// sshd needs a few seconds to install and start.
	var c *SSHClient
	deadline := time.Now().Add(90 * time.Second)
	for {
		var err error
		c, err = connectSSH(opts, hostKeyPolicy{path: kh})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("connect via real bastion: %v", err)
		}
		time.Sleep(time.Second)
	}
	defer c.Close()

	out, err := c.RunCommand("hostname; whoami")
	if err != nil || !strings.Contains(out, "tester") {
		t.Fatalf("command on target: %q %v", out, err)
	}
	if !strings.Contains(out, target[:12]) {
		t.Fatalf("ran on %q, not on the target container %s", out, target[:12])
	}

	// The target cannot be reached directly (only via the bastion).
	if _, err := net.DialTimeout("tcp", "target:22", time.Second); err == nil {
		t.Fatal("target unexpectedly reachable directly")
	}

	// Both host keys are recorded, and a strict reconnect now succeeds.
	data, _ := os.ReadFile(kh)
	if !strings.Contains(string(data), "target") || !strings.Contains(string(data), "[127.0.0.1]:"+portStr) {
		t.Fatalf("known_hosts:\n%s", data)
	}
	strict, err := connectSSH(opts, hostKeyPolicy{path: kh, strict: true})
	if err != nil {
		t.Fatalf("strict reconnect: %v", err)
	}
	strict.Close()

	// Restarting the bastion's sshd kills the tunnel; the client reconnects.
	docker(t, "exec", bastion, "sh", "-c", "pkill -f '^sshd: tester' || true")
	deadline = time.Now().Add(30 * time.Second)
	for {
		out, err := c.RunCommand("echo again")
		if err == nil && strings.TrimSpace(out) == "again" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no reconnect through the bastion: %q %v", out, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
