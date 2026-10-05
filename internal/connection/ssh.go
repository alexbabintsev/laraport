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

// SSHClient runs commands on a remote server over one SSH connection,
// optionally tunnelled through a jump host (bastion). The connection is
// (re)established lazily: if it drops — network change, laptop sleep, server
// or bastion restart — it is detected and the next command dials again (the
// whole chain).
type SSHClient struct {
	base

	addr       string
	config     *ssh.ClientConfig
	jumpAddr   string            // "" = direct connection
	jumpConfig *ssh.ClientConfig // nil = direct connection
	agents     []net.Conn        // ssh-agent sockets, closed with the client
	sem        chan struct{}

	mu         sync.Mutex
	client     *ssh.Client
	jumpClient *ssh.Client // the bastion connection carrying client
	closed     bool
}

// SSHOptions describes how to reach a server.
type SSHOptions struct {
	Host       string
	Port       int
	User       string
	KeyPath    string // "" = ssh-agent, then the default key files
	Passphrase string
	// StrictHostKeys refuses hosts that are not in known_hosts yet (instead
	// of recording them on first use). Applies to the jump host too.
	StrictHostKeys bool
	// Jump, when set, is the bastion the connection is tunnelled through
	// (like OpenSSH's ProxyJump).
	Jump *JumpHost
}

// JumpHost is an SSH bastion.
type JumpHost struct {
	Host       string
	Port       int
	User       string
	KeyPath    string // "" = ssh-agent, then the default key files
	Passphrase string
}

// ConnectSSH connects to a server with key auth (or ssh-agent when no key is
// given), through opts.Jump when set, and verifies every host key against
// ~/.ssh/known_hosts: a changed key is always rejected; an unknown host is
// recorded on first use, or refused when opts.StrictHostKeys is set.
func ConnectSSH(opts SSHOptions) (*SSHClient, error) {
	khPath, err := defaultKnownHostsPath()
	if err != nil {
		return nil, err
	}
	return connectSSH(opts, hostKeyPolicy{path: khPath, strict: opts.StrictHostKeys})
}

func connectSSH(opts SSHOptions, policy hostKeyPolicy) (*SSHClient, error) {
	c := &SSHClient{
		addr: net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port)),
		sem:  make(chan struct{}, maxSessions),
	}
	c.base = base{sshTransport{c}}

	hostKeyCallback, err := policy.callback()
	if err != nil {
		return nil, err
	}
	clientConfig := func(addr, user, keyPath, passphrase string) (*ssh.ClientConfig, error) {
		auth, agentConn, err := buildAuthMethods(keyPath, passphrase)
		if err != nil {
			return nil, fmt.Errorf("building auth methods: %w", err)
		}
		if agentConn != nil {
			c.agents = append(c.agents, agentConn)
		}
		return &ssh.ClientConfig{
			User:              user,
			Auth:              auth,
			HostKeyCallback:   hostKeyCallback,
			HostKeyAlgorithms: policy.algorithms(addr),
			Timeout:           dialTimeout,
		}, nil
	}

	if c.config, err = clientConfig(c.addr, opts.User, opts.KeyPath, opts.Passphrase); err != nil {
		c.Close()
		return nil, err
	}
	if j := opts.Jump; j != nil {
		c.jumpAddr = net.JoinHostPort(j.Host, strconv.Itoa(j.Port))
		if c.jumpConfig, err = clientConfig(c.jumpAddr, j.User, j.KeyPath, j.Passphrase); err != nil {
			c.Close()
			return nil, fmt.Errorf("jump host %s: %w", c.jumpAddr, err)
		}
	}

	// Connect eagerly so configuration/auth errors surface immediately.
	if _, err := c.conn(); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// Close closes the connection (and the jump host connection). Further
// commands fail.
func (c *SSHClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	var err error
	if c.client != nil {
		err = c.client.Close()
		c.client = nil
	}
	if c.jumpClient != nil {
		c.jumpClient.Close()
		c.jumpClient = nil
	}
	for _, a := range c.agents {
		a.Close()
	}
	c.agents = nil
	return err
}

// conn returns the live connection, dialing a new one (through the jump host,
// if any) if there is none.
func (c *SSHClient) conn() (*ssh.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("ssh client closed")
	}
	if c.client != nil {
		return c.client, nil
	}
	if c.jumpConfig == nil {
		cl, err := dial(c.addr, c.config)
		if err != nil {
			return nil, err
		}
		c.client = cl
		go c.watch(cl)
		return cl, nil
	}

	jc, err := dial(c.jumpAddr, c.jumpConfig)
	if err != nil {
		return nil, fmt.Errorf("jump host: %w", err)
	}
	cl, err := dialVia(jc, c.addr, c.config, dialTimeout)
	if err != nil {
		jc.Close()
		return nil, err
	}
	c.client, c.jumpClient = cl, jc
	// A dead bastion takes the tunnelled connection down with it, so
	// watching the target (whose keepalives cross the bastion) covers both.
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

// dialVia opens a tunnel from the jump host jc to addr (a direct-tcpip
// channel, as `ssh -J` does) and performs the SSH handshake with the target
// over it. Tunnelled connections do not support deadlines, so the whole
// exchange is bounded with a timer instead.
func dialVia(jc *ssh.Client, addr string, config *ssh.ClientConfig, timeout time.Duration) (*ssh.Client, error) {
	type result struct {
		c   *ssh.Client
		err error
	}
	res := make(chan result, 1)
	go func() {
		nc, err := jc.Dial("tcp", addr)
		if err != nil {
			res <- result{nil, fmt.Errorf("jump host could not reach %s: %w", addr, err)}
			return
		}
		sc, chans, reqs, err := ssh.NewClientConn(nc, addr, config)
		if err != nil {
			nc.Close()
			res <- result{nil, fmt.Errorf("ssh handshake with %s (via jump host): %w", addr, err)}
			return
		}
		res <- result{ssh.NewClient(sc, chans, reqs), nil}
	}()
	select {
	case r := <-res:
		return r.c, r.err
	case <-time.After(timeout):
		go func() {
			// Don't leak a connection that completes after we gave up.
			if r := <-res; r.c != nil {
				r.c.Close()
			}
		}()
		return nil, fmt.Errorf("connecting to %s via the jump host: timed out after %s", addr, timeout)
	}
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

// forget drops cl (and the jump host connection carrying it) if it is still
// the current connection.
func (c *SSHClient) forget(cl *ssh.Client) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client == cl {
		c.client = nil
		if c.jumpClient != nil {
			c.jumpClient.Close()
			c.jumpClient = nil
		}
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
