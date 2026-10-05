package connection

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// testSSHServer is a minimal in-process SSH server that runs "exec" requests
// with the local `sh -c`, wiring stdin/stdout/stderr to the channel the way
// OpenSSH does (stderr as extended data) and reporting the exit status.
type testSSHServer struct {
	t         *testing.T
	ln        net.Listener
	config    *ssh.ServerConfig
	hostKey   ssh.Signer
	clientKey string // path of the authorised client private key (OpenSSH PEM)

	mu    sync.Mutex
	conns []net.Conn
	// refuseSessions makes the server reject new session channels.
	refuseSessions bool
	// noForwarding makes the server reject direct-tcpip (jump host) channels.
	noForwarding bool
	// forwards counts tunnels opened through this server (as a jump host).
	forwards int
}

func newTestSSHServer(t *testing.T) *testSSHServer {
	t.Helper()
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}
	return newTestSSHServerWithHostKey(t, hostSigner)
}

func newTestSSHServerWithHostKey(t *testing.T, hostSigner ssh.Signer) *testSSHServer {
	t.Helper()
	clientPub, clientPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	authorized, err := ssh.NewPublicKey(clientPub)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(clientPriv, "")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(key.Marshal()) == string(authorized.Marshal()) {
				return nil, nil
			}
			return nil, io.EOF
		},
	}
	cfg.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &testSSHServer{t: t, ln: ln, config: cfg, hostKey: hostSigner, clientKey: keyPath}
	go s.serve()
	t.Cleanup(s.close)
	return s
}

func (s *testSSHServer) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *testSSHServer) close() {
	s.ln.Close()
	s.dropConnections()
}

// dropConnections abruptly closes every client connection, simulating a
// network drop or server restart.
func (s *testSSHServer) dropConnections() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		c.Close()
	}
	s.conns = nil
}

func (s *testSSHServer) connectionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

func (s *testSSHServer) serve() {
	for {
		nc, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns = append(s.conns, nc)
		s.mu.Unlock()
		go s.handle(nc)
	}
}

func (s *testSSHServer) handle(nc net.Conn) {
	_, chans, reqs, err := ssh.NewServerConn(nc, s.config)
	if err != nil {
		nc.Close()
		return
	}
	go func() {
		for req := range reqs {
			if req.WantReply {
				req.Reply(false, nil) //nolint:errcheck // keepalives get "failure", like OpenSSH
			}
		}
	}()
	for nch := range chans {
		s.mu.Lock()
		refuse, noFwd := s.refuseSessions, s.noForwarding
		s.mu.Unlock()
		if nch.ChannelType() == "direct-tcpip" {
			if noFwd {
				nch.Reject(ssh.Prohibited, "port forwarding is disabled") //nolint:errcheck
				continue
			}
			go s.forward(nch)
			continue
		}
		if nch.ChannelType() != "session" || refuse {
			nch.Reject(ssh.Prohibited, "no sessions") //nolint:errcheck
			continue
		}
		ch, chReqs, err := nch.Accept()
		if err != nil {
			continue
		}
		go s.session(ch, chReqs)
	}
}

func (s *testSSHServer) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	for req := range reqs {
		if req.Type != "exec" {
			if req.WantReply {
				req.Reply(false, nil) //nolint:errcheck
			}
			continue
		}
		var payload struct{ Command string }
		if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
			req.Reply(false, nil) //nolint:errcheck
			return
		}
		req.Reply(true, nil) //nolint:errcheck

		cmd := exec.Command("sh", "-c", payload.Command)
		cmd.Stdout = ch
		cmd.Stderr = ch.Stderr()
		stdin, _ := cmd.StdinPipe()
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			sendExit(ch, 127)
			return
		}
		go func() {
			io.Copy(stdin, ch) //nolint:errcheck
			stdin.Close()
		}()
		// Like sshd: when the channel goes away the command's stdin is closed
		// (EOF) and its output pipes break shortly after.
		exited := make(chan struct{})
		go func() {
			for range reqs {
			}
			stdin.Close()
			select {
			case <-exited:
			case <-time.After(time.Second):
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) //nolint:errcheck
			}
		}()
		err := cmd.Wait()
		close(exited)
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		sendExit(ch, code)
		return
	}
}

// forward serves a direct-tcpip channel like sshd does for `ssh -J`.
func (s *testSSHServer) forward(nch ssh.NewChannel) {
	var req struct {
		Host     string
		Port     uint32
		OrigHost string
		OrigPort uint32
	}
	if err := ssh.Unmarshal(nch.ExtraData(), &req); err != nil {
		nch.Reject(ssh.ConnectionFailed, "bad request") //nolint:errcheck
		return
	}
	target, err := net.Dial("tcp", net.JoinHostPort(req.Host, strconv.Itoa(int(req.Port))))
	if err != nil {
		nch.Reject(ssh.ConnectionFailed, err.Error()) //nolint:errcheck
		return
	}
	ch, reqs, err := nch.Accept()
	if err != nil {
		target.Close()
		return
	}
	s.mu.Lock()
	s.forwards++
	s.conns = append(s.conns, target) // dropped with the server's connections
	s.mu.Unlock()
	go ssh.DiscardRequests(reqs)
	go func() {
		io.Copy(target, ch) //nolint:errcheck
		target.Close()
	}()
	io.Copy(ch, target) //nolint:errcheck
	ch.Close()
}

func (s *testSSHServer) forwardCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.forwards
}

func sendExit(ch ssh.Channel, code int) {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(code))
	ch.SendRequest("exit-status", false, b) //nolint:errcheck
}

// knownHostsWith returns a known_hosts path (in a temp dir) that is empty.
func emptyKnownHosts(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "ssh", "known_hosts")
}

// connectTest connects an SSHClient to s with a fresh known_hosts file.
func connectTest(t *testing.T, s *testSSHServer) *SSHClient {
	t.Helper()
	c, err := connectSSH(SSHOptions{Host: "127.0.0.1", Port: s.port(), User: "tester", KeyPath: s.clientKey, Passphrase: ""}, hostKeyPolicy{path: emptyKnownHosts(t)})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
