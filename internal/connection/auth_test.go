package connection

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func writeKey(t *testing.T, dir, name, passphrase string) (string, ed25519.PrivateKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return p, priv
}

func TestLoadKey(t *testing.T) {
	dir := t.TempDir()
	plain, _ := writeKey(t, dir, "plain", "")
	locked, _ := writeKey(t, dir, "locked", "pw")
	os.WriteFile(filepath.Join(dir, "junk"), []byte("not a key"), 0o600)

	if _, err := loadKey(plain, ""); err != nil {
		t.Fatalf("plain: %v", err)
	}
	if _, err := loadKey(locked, "pw"); err != nil {
		t.Fatalf("locked with passphrase: %v", err)
	}
	if _, err := loadKey(locked, ""); err == nil || !strings.Contains(err.Error(), "passphrase-protected") {
		t.Fatalf("missing passphrase: %v", err)
	}
	if _, err := loadKey(locked, "wrong"); err == nil || !strings.Contains(err.Error(), "wrong passphrase") {
		t.Fatalf("wrong passphrase: %v", err)
	}
	if _, err := loadKey(filepath.Join(dir, "junk"), ""); err == nil {
		t.Fatal("junk accepted")
	}
	if _, err := loadKey(filepath.Join(dir, "missing"), ""); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestBuildAuthMethodsExplicitKey(t *testing.T) {
	k, _ := writeKey(t, t.TempDir(), "k", "")
	methods, agentConn, err := buildAuthMethods(k, "")
	if err != nil || len(methods) != 1 || agentConn != nil {
		t.Fatalf("methods %d agent %v err %v", len(methods), agentConn, err)
	}
	if _, _, err := buildAuthMethods(k+".missing", ""); err == nil {
		t.Fatal("missing explicit key accepted")
	}
}

func TestBuildAuthMethodsDiscovery(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "")
	sshDir := filepath.Join(home, ".ssh")
	os.MkdirAll(sshDir, 0o700)

	if _, _, err := buildAuthMethods("", ""); err == nil || !strings.Contains(err.Error(), "no SSH keys found") {
		t.Fatalf("no keys: %v", err)
	}
	writeKey(t, sshDir, "id_rsa", "locked")
	if _, _, err := buildAuthMethods("", ""); err == nil || !strings.Contains(err.Error(), "no usable SSH key") {
		t.Fatalf("only locked key: %v", err)
	}
	writeKey(t, sshDir, "id_ed25519", "")
	methods, _, err := buildAuthMethods("", "")
	if err != nil || len(methods) != 1 {
		t.Fatalf("methods %d err %v", len(methods), err)
	}
}

func TestBuildAuthMethodsAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sock := filepath.Join(t.TempDir(), "agent.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("unix sockets unavailable: %v", err)
	}
	defer ln.Close()
	keyring := agent.NewKeyring()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go agent.ServeAgent(keyring, c) //nolint:errcheck
		}
	}()
	t.Setenv("SSH_AUTH_SOCK", sock)
	methods, agentConn, err := buildAuthMethods("", "")
	if err != nil || len(methods) != 1 || agentConn == nil {
		t.Fatalf("methods %d agent %v err %v", len(methods), agentConn, err)
	}
	agentConn.Close()
}

func TestConnectSSHViaAgentAndDefaultKnownHosts(t *testing.T) {
	s := newTestSSHServer(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Serve the server's authorised key through an agent.
	data, _ := os.ReadFile(s.clientKey)
	raw, err := ssh.ParseRawPrivateKey(data)
	if err != nil {
		t.Fatal(err)
	}
	keyring := agent.NewKeyring()
	keyring.Add(agent.AddedKey{PrivateKey: raw}) //nolint:errcheck
	sock := filepath.Join(t.TempDir(), "a.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("unix sockets unavailable: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go agent.ServeAgent(keyring, c) //nolint:errcheck
		}
	}()
	t.Setenv("SSH_AUTH_SOCK", sock)

	c, err := ConnectSSH(SSHOptions{Host: "127.0.0.1", Port: s.port(), User: "tester", KeyPath: "", Passphrase: "", StrictHostKeys: false})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if out, err := c.RunCommand("echo ok"); err != nil || strings.TrimSpace(out) != "ok" {
		t.Fatalf("%q %v", out, err)
	}
	kh, err := os.ReadFile(filepath.Join(home, ".ssh", "known_hosts"))
	if err != nil || !strings.Contains(string(kh), "[127.0.0.1]:") {
		t.Fatalf("known_hosts not recorded: %q %v", kh, err)
	}
	st, _ := os.Stat(filepath.Join(home, ".ssh"))
	if st.Mode().Perm() != 0o700 {
		t.Errorf(".ssh mode %v", st.Mode().Perm())
	}
}

func TestConnectSSHAuthFailure(t *testing.T) {
	s := newTestSSHServer(t)
	other, _ := writeKey(t, t.TempDir(), "other", "")
	_, err := connectSSH(SSHOptions{Host: "127.0.0.1", Port: s.port(), User: "tester", KeyPath: other, Passphrase: ""}, hostKeyPolicy{path: emptyKnownHosts(t)})
	if err == nil || !strings.Contains(err.Error(), "handshake") {
		t.Fatalf("err = %v", err)
	}
}

func TestConnectSSHDialFailure(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // nothing listens there now
	k, _ := writeKey(t, t.TempDir(), "k", "")
	_, err := connectSSH(SSHOptions{Host: "127.0.0.1", Port: port, User: "u", KeyPath: k, Passphrase: ""}, hostKeyPolicy{path: emptyKnownHosts(t)})
	if err == nil || !strings.Contains(err.Error(), "ssh dial") {
		t.Fatalf("err = %v", err)
	}
}

func TestPing(t *testing.T) {
	s := newTestSSHServer(t)
	c := connectTest(t, s)
	cl, err := c.conn()
	if err != nil {
		t.Fatal(err)
	}
	if !ping(cl, time.Second) {
		t.Fatal("live connection failed ping")
	}
	cl.Close()
	if ping(cl, time.Second) {
		t.Fatal("closed connection passed ping")
	}
}

func TestClosedClientRefusesCommands(t *testing.T) {
	s := newTestSSHServer(t)
	c := connectTest(t, s)
	c.Close()
	if _, err := c.RunCommand("true"); err == nil {
		t.Fatal("closed client ran a command")
	}
	c.Close() // idempotent
}

func TestHostKeyAlgorithmsFollowKnownHosts(t *testing.T) {
	s := newTestSSHServer(t)
	kh := emptyKnownHosts(t)
	p := hostKeyPolicy{path: kh}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(s.port()))
	if algos := p.algorithms(addr); algos != nil {
		t.Fatalf("unknown host algos = %v", algos)
	}
	c, err := connectSSH(SSHOptions{Host: "127.0.0.1", Port: s.port(), User: "tester", KeyPath: s.clientKey, Passphrase: ""}, p)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if algos := p.algorithms(addr); len(algos) != 1 || algos[0] != ssh.KeyAlgoED25519 {
		t.Fatalf("algos = %v", algos)
	}
	if got := algorithmsForKeyType(ssh.KeyAlgoRSA); len(got) != 3 {
		t.Fatalf("rsa algos = %v", got)
	}
}
