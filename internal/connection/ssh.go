package connection

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

const (
	// maxSessions caps concurrent SSH sessions per connection (OpenSSH's
	// default MaxSessions is 10).
	maxSessions = 6
	// sessionSlotTimeout is how long a command waits for a free session slot.
	sessionSlotTimeout = 30 * time.Second
	// dialTimeout bounds TCP connect plus the SSH handshake.
	dialTimeout = 15 * time.Second
	// sessionOpenTimeout bounds opening a session; a connection that cannot
	// open one in time is considered dead and replaced.
	sessionOpenTimeout = 10 * time.Second
	// keepaliveInterval / keepaliveTimeout: how often the connection is probed
	// while idle, and how long a probe may take before the connection is
	// declared dead.
	keepaliveInterval = 30 * time.Second
	keepaliveTimeout  = 10 * time.Second
)

// ErrSessionsBusy is returned when no SSH session slot frees up in time.
var ErrSessionsBusy = errors.New("all SSH sessions are busy")

// SSHClient runs commands on a remote server over one SSH connection. The
// connection is (re)established lazily: if it drops — network change, laptop
// sleep, server restart — it is detected and the next command dials again.
type SSHClient struct {
	base

	addr   string
	config *ssh.ClientConfig
	agent  net.Conn // ssh-agent socket, nil when not used
	sem    chan struct{}

	mu     sync.Mutex
	client *ssh.Client
	closed bool
}

// ConnectSSH connects to host:port with key auth (or ssh-agent when no key is
// given) and verifies the host key against ~/.ssh/known_hosts (new hosts are
// recorded on first use; a changed key is rejected).
func ConnectSSH(host string, port int, user, keyPath, passphrase string) (*SSHClient, error) {
	khPath, err := defaultKnownHostsPath()
	if err != nil {
		return nil, err
	}
	return connectSSH(host, port, user, keyPath, passphrase, hostKeyPolicy{path: khPath})
}

func connectSSH(host string, port int, user, keyPath, passphrase string, policy hostKeyPolicy) (*SSHClient, error) {
	auth, agentConn, err := buildAuthMethods(keyPath, passphrase)
	if err != nil {
		return nil, fmt.Errorf("building auth methods: %w", err)
	}
	hostKeyCallback, err := policy.callback()
	if err != nil {
		if agentConn != nil {
			agentConn.Close()
		}
		return nil, err
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	c := &SSHClient{
		addr: addr,
		config: &ssh.ClientConfig{
			User:              user,
			Auth:              auth,
			HostKeyCallback:   hostKeyCallback,
			HostKeyAlgorithms: policy.algorithms(addr),
			Timeout:           dialTimeout,
		},
		agent: agentConn,
		sem:   make(chan struct{}, maxSessions),
	}
	c.base = base{sshTransport{c}}

	// Connect eagerly so configuration/auth errors surface immediately.
	if _, err := c.conn(); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// Close closes the connection. Further commands fail.
func (c *SSHClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	var err error
	if c.client != nil {
		err = c.client.Close()
		c.client = nil
	}
	if c.agent != nil {
		c.agent.Close()
		c.agent = nil
	}
	return err
}

// conn returns the live connection, dialing a new one if there is none.
func (c *SSHClient) conn() (*ssh.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("ssh client closed")
	}
	if c.client != nil {
		return c.client, nil
	}
	cl, err := dial(c.addr, c.config)
	if err != nil {
		return nil, err
	}
	c.client = cl
	go c.watch(cl)
	return cl, nil
}

// dial opens a TCP connection and performs the SSH handshake, with the whole
// exchange bounded by dialTimeout.
func dial(addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
	nc, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	if tc, ok := nc.(*net.TCPConn); ok {
		tc.SetKeepAlive(true)                    //nolint:errcheck
		tc.SetKeepAlivePeriod(keepaliveInterval) //nolint:errcheck
	}
	nc.SetDeadline(time.Now().Add(dialTimeout)) //nolint:errcheck
	sc, chans, reqs, err := ssh.NewClientConn(nc, addr, config)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("ssh handshake with %s: %w", addr, err)
	}
	nc.SetDeadline(time.Time{}) //nolint:errcheck
	return ssh.NewClient(sc, chans, reqs), nil
}

// watch forgets cl once its transport dies and probes it periodically so a
// silently dropped connection (NAT timeout, sleep) is noticed while idle.
func (c *SSHClient) watch(cl *ssh.Client) {
	dead := make(chan struct{})
	go func() {
		cl.Wait() //nolint:errcheck
		close(dead)
	}()
	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-dead:
			c.forget(cl)
			return
		case <-ticker.C:
			if !ping(cl, keepaliveTimeout) {
				cl.Close()
			}
		}
	}
}

// ping sends an OpenSSH keepalive request and reports whether the server
// answered within timeout.
func ping(cl *ssh.Client, timeout time.Duration) bool {
	res := make(chan error, 1)
	go func() {
		_, _, err := cl.SendRequest("keepalive@openssh.com", true, nil)
		res <- err
	}()
	select {
	case err := <-res:
		// Servers reply "failure" to unknown requests; any reply proves liveness.
		return err == nil
	case <-time.After(timeout):
		return false
	}
}

// forget drops cl if it is still the current connection.
func (c *SSHClient) forget(cl *ssh.Client) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client == cl {
		c.client = nil
	}
}

// newSession opens a session, replacing a dead connection once if needed.
func (c *SSHClient) newSession() (*ssh.Session, error) {
	cl, err := c.conn()
	if err != nil {
		return nil, err
	}
	sess, err := openSession(cl, sessionOpenTimeout)
	if err == nil {
		return sess, nil
	}
	var refused *ssh.OpenChannelError
	if errors.As(err, &refused) {
		// The server answered (e.g. MaxSessions reached): the connection is
		// alive, so don't tear it down under the other sessions.
		return nil, err
	}
	// The connection is unusable (closed, or hung after sleep): drop it and
	// try once more on a fresh one.
	cl.Close()
	c.forget(cl)
	cl, err = c.conn()
	if err != nil {
		return nil, fmt.Errorf("reconnecting: %w", err)
	}
	return openSession(cl, sessionOpenTimeout)
}

// openSession opens a session on cl, giving up after timeout.
func openSession(cl *ssh.Client, timeout time.Duration) (*ssh.Session, error) {
	type result struct {
		s   *ssh.Session
		err error
	}
	res := make(chan result, 1)
	go func() {
		s, err := cl.NewSession()
		res <- result{s, err}
	}()
	select {
	case r := <-res:
		return r.s, r.err
	case <-time.After(timeout):
		go func() {
			// Don't leak a session that opens after we gave up.
			if r := <-res; r.s != nil {
				r.s.Close()
			}
		}()
		return nil, fmt.Errorf("opening ssh session: timed out after %s", timeout)
	}
}

// acquire takes a session slot, waiting up to sessionSlotTimeout.
func (c *SSHClient) acquire() error {
	select {
	case c.sem <- struct{}{}:
		return nil
	case <-time.After(sessionSlotTimeout):
		return ErrSessionsBusy
	}
}

func (c *SSHClient) release() { <-c.sem }

type sshTransport struct{ c *SSHClient }

func (t sshTransport) start(cmd string, stdin io.Reader, stdout, stderr io.Writer) (*handle, error) {
	c := t.c
	if err := c.acquire(); err != nil {
		return nil, err
	}
	sess, err := c.newSession()
	if err != nil {
		c.release()
		return nil, fmt.Errorf("new ssh session: %w", err)
	}
	sess.Stdin = stdin
	sess.Stdout = stdout
	sess.Stderr = stderr
	if err := sess.Start(cmd); err != nil {
		sess.Close()
		c.release()
		return nil, fmt.Errorf("starting remote command: %w", err)
	}
	var releaseOnce sync.Once
	return &handle{
		wait: func() error {
			err := sess.Wait()
			sess.Close()
			releaseOnce.Do(c.release)
			return err
		},
		kill: func() {
			sess.Signal(ssh.SIGKILL) //nolint:errcheck // most servers ignore signals
			sess.Close()
		},
	}, nil
}

// buildAuthMethods returns the auth methods for the connection and, when
// ssh-agent is used, the agent socket (owned by the caller).
func buildAuthMethods(keyPath, passphrase string) ([]ssh.AuthMethod, net.Conn, error) {
	if keyPath != "" {
		// Explicit key specified — use ONLY that key, skip agent entirely.
		// This avoids MaxAuthTries failures when agent has many unrelated keys.
		signer, err := loadKey(keyPath, passphrase)
		if err != nil {
			return nil, nil, fmt.Errorf("loading key %s: %w", keyPath, err)
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil, nil
	}

	var methods []ssh.AuthMethod
	var keyErrors []string
	var agentConn net.Conn

	// No key specified — try ssh-agent first, then auto-discover standard keys.
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			agentConn = conn
			methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}

	home, _ := os.UserHomeDir()
	for _, name := range []string{"id_ed25519", "id_rsa", "id_ecdsa", "id_ecdsa_sk", "id_ed25519_sk"} {
		kp := filepath.Join(home, ".ssh", name)
		if _, err := os.Stat(kp); err != nil {
			continue
		}
		signer, err := loadKey(kp, passphrase)
		if err != nil {
			keyErrors = append(keyErrors, fmt.Sprintf("%s: %v", kp, err))
			continue
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}

	if len(methods) == 0 {
		if len(keyErrors) > 0 {
			return nil, nil, fmt.Errorf("no usable SSH key found. Errors:\n  %s\nAdd 'passphrase:' to config or use ssh-agent",
				strings.Join(keyErrors, "\n  "))
		}
		return nil, nil, fmt.Errorf("no SSH keys found — specify 'key:' in config or add keys to ssh-agent")
	}
	return methods, agentConn, nil
}

// loadKey parses a private key file, trying with passphrase if the raw parse fails.
func loadKey(path, passphrase string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	signer, err := ssh.ParsePrivateKey(data)
	if err == nil {
		return signer, nil
	}

	// Key is passphrase-protected
	var passphraseErr *ssh.PassphraseMissingError
	if errors.As(err, &passphraseErr) {
		if passphrase == "" {
			return nil, fmt.Errorf("key is passphrase-protected (add 'passphrase:' to config or use ssh-agent)")
		}
		signer, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(passphrase))
		if err != nil {
			return nil, fmt.Errorf("wrong passphrase: %w", err)
		}
		return signer, nil
	}

	return nil, err
}
