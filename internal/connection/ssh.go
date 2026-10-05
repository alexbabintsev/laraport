package connection

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// SSHClient wraps an SSH connection to a remote server.
type SSHClient struct {
	client *ssh.Client
	sem    chan struct{} // limits concurrent SSH sessions
}

// ConnectSSH establishes an SSH connection using key auth (with ssh-agent fallback).
func ConnectSSH(host string, port int, user, keyPath, passphrase string) (*SSHClient, error) {
	authMethods, err := buildAuthMethods(keyPath, passphrase)
	if err != nil {
		return nil, fmt.Errorf("building auth methods: %w", err)
	}

	knownHostsPath := os.ExpandEnv("$HOME/.ssh/known_hosts")
	hostKeyCallback := ssh.InsecureIgnoreHostKey() //nolint:gosec // intentional for first-use, user controls config
	if _, err := os.Stat(knownHostsPath); err == nil {
		cb, err := knownhosts.New(knownHostsPath)
		if err == nil {
			hostKeyCallback = cb
		}
	}

	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	sem := make(chan struct{}, 6)
	for i := range 6 {
		_ = i
		sem <- struct{}{}
	}
	return &SSHClient{client: client, sem: sem}, nil
}

// Close closes the underlying SSH connection.
func (c *SSHClient) Close() error {
	return c.client.Close()
}

// acquire blocks until a session slot is available.
func (c *SSHClient) acquire() { <-c.sem }

// release returns a session slot.
func (c *SSHClient) release() { c.sem <- struct{}{} }

// RunCommand runs a command on the remote host and returns combined stdout+stderr output.
func (c *SSHClient) RunCommand(cmd string) (string, error) {
	c.acquire()
	sess, err := c.client.NewSession()
	if err != nil {
		c.release()
		return "", fmt.Errorf("new ssh session: %w", err)
	}
	defer c.release()
	defer sess.Close()

	var buf bytes.Buffer
	sess.Stdout = &buf
	sess.Stderr = &buf

	runErr := sess.Run(cmd)
	return buf.String(), runErr
}

// StreamCommand runs a command and streams output line-by-line via the returned channel.
// The channel is closed when the command finishes or stop is called.
// stop blocks until the goroutine exits and the semaphore slot is released.
func (c *SSHClient) StreamCommand(cmd string) (<-chan string, func(), error) {
	c.acquire()
	sess, err := c.client.NewSession()
	if err != nil {
		c.release()
		return nil, nil, fmt.Errorf("new ssh session: %w", err)
	}

	pr, pw := io.Pipe()
	sess.Stdout = pw
	sess.Stderr = pw

	// Attach a stdin pipe so we can signal EOF to the remote process.
	// Commands that use "read; kill $PID" rely on this to terminate cleanly.
	stdinPipe, err := sess.StdinPipe()
	if err != nil {
		c.release()
		sess.Close()
		pw.Close()
		pr.Close()
		return nil, nil, fmt.Errorf("ssh stdin pipe: %w", err)
	}

	ch := make(chan string, 64)
	quit := make(chan struct{})
	done := make(chan struct{})

	var quitOnce sync.Once

	stop := func() {
		quitOnce.Do(func() { close(quit) })
		// Close stdin first — this sends EOF to the remote shell's "read",
		// which triggers "kill $PID" inside the container.
		stdinPipe.Close()
		sess.Close()
		pw.Close()
		<-done // wait for goroutine to finish and release semaphore
	}

	go func() {
		defer c.release()
		defer close(done)
		defer close(ch)

		if err := sess.Start(cmd); err != nil {
			pw.Close()
			select {
			case ch <- fmt.Sprintf("ERROR: %v", err):
			case <-quit:
			}
			return
		}

		// Close pw when the command finishes so the scanner gets EOF.
		go func() {
			sess.Wait() //nolint:errcheck
			pw.Close()
		}()

		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			select {
			case ch <- scanner.Text():
			case <-quit:
				return
			}
		}
	}()

	return ch, stop, nil
}

// InteractiveCommand runs a command over SSH, streaming output and accepting stdin input.
func (c *SSHClient) InteractiveCommand(cmd string) (<-chan string, chan<- string, func(), error) {
	c.acquire()
	sess, err := c.client.NewSession()
	if err != nil {
		c.release()
		return nil, nil, nil, fmt.Errorf("new ssh session: %w", err)
	}

	pr, pw := io.Pipe()
	sess.Stdout = pw
	sess.Stderr = pw

	stdinPipe, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		pw.Close()
		pr.Close()
		c.release()
		return nil, nil, nil, fmt.Errorf("stdin pipe: %w", err)
	}

	if err := sess.Start(cmd); err != nil {
		stdinPipe.Close()
		sess.Close()
		pw.Close()
		pr.Close()
		c.release()
		return nil, nil, nil, fmt.Errorf("start command: %w", err)
	}

	outCh := make(chan string, 64)
	inCh := make(chan string, 16)
	quit := make(chan struct{})
	done := make(chan struct{})

	var quitOnce sync.Once

	stop := func() {
		quitOnce.Do(func() { close(quit) })
		stdinPipe.Close()
		sess.Signal(ssh.SIGTERM) //nolint:errcheck
		sess.Close()
		pw.Close()
		<-done
	}

	go func() {
		defer stdinPipe.Close()
		for line := range inCh {
			fmt.Fprintln(stdinPipe, line) //nolint:errcheck
		}
	}()

	go func() {
		defer c.release()
		defer close(done)
		defer close(outCh)

		go func() {
			sess.Wait() //nolint:errcheck
			pw.Close()
		}()

		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			select {
			case outCh <- scanner.Text():
			case <-quit:
				return
			}
		}
	}()

	return outCh, inCh, stop, nil
}

func buildAuthMethods(keyPath, passphrase string) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	var keyErrors []string

	if keyPath != "" {
		// Explicit key specified — use ONLY that key, skip agent entirely.
		// This avoids MaxAuthTries failures when agent has many unrelated keys.
		signer, err := loadKey(keyPath, passphrase)
		if err != nil {
			return nil, fmt.Errorf("loading key %s: %w", keyPath, err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
		return methods, nil
	}

	// No key specified — try ssh-agent first, then auto-discover standard keys.
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			agentClient := agent.NewClient(conn)
			methods = append(methods, ssh.PublicKeysCallback(agentClient.Signers))
		}
	}

	home, _ := os.UserHomeDir()
	standardKeys := []string{
		filepath.Join(home, ".ssh", "id_ed25519"),
		filepath.Join(home, ".ssh", "id_rsa"),
		filepath.Join(home, ".ssh", "id_ecdsa"),
		filepath.Join(home, ".ssh", "id_ecdsa_sk"),
		filepath.Join(home, ".ssh", "id_ed25519_sk"),
	}
	for _, kp := range standardKeys {
		if _, err := os.Stat(kp); os.IsNotExist(err) {
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
			return nil, fmt.Errorf("no usable SSH key found. Errors:\n  %s\nAdd 'passphrase:' to config or use ssh-agent",
				strings.Join(keyErrors, "\n  "))
		}
		return nil, fmt.Errorf("no SSH keys found — specify 'key:' in config or add keys to ssh-agent")
	}
	return methods, nil
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
