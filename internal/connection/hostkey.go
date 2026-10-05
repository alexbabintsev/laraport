package connection

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// knownHostsMu serialises appends to known_hosts from concurrent connections.
var knownHostsMu sync.Mutex

// defaultKnownHostsPath returns ~/.ssh/known_hosts.
func defaultKnownHostsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory for known_hosts: %w", err)
	}
	return filepath.Join(home, ".ssh", "known_hosts"), nil
}

// hostKeyPolicy verifies server host keys against an OpenSSH known_hosts file
// with "accept-new" semantics (like StrictHostKeyChecking=accept-new):
//
//   - a host that is not in the file yet is trusted on first use and its key
//     is appended to the file;
//   - a host whose key differs from the recorded one is rejected — that is
//     what a man-in-the-middle looks like.
//
// With strict set, unknown hosts are refused as well (like
// StrictHostKeyChecking=yes): the key must be added to known_hosts by hand.
type hostKeyPolicy struct {
	path   string
	strict bool
}

// callback returns the ssh.HostKeyCallback implementing the policy. The file
// (and its directory) is created if missing.
func (p hostKeyPolicy) callback() (ssh.HostKeyCallback, error) {
	if err := ensureFile(p.path); err != nil {
		return nil, err
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		// Re-read on every check so keys appended by an earlier connection
		// (or by the user) are seen.
		check, err := knownhosts.New(p.path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", p.path, err)
		}
		err = check(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) && len(keyErr.Want) == 0 {
			if p.strict {
				return fmt.Errorf("unknown host %s (key %s) and host_key_check is strict. "+
					"Verify the fingerprint with the server's administrator, then add it with: "+
					"ssh-keyscan %s >> %s",
					hostname, ssh.FingerprintSHA256(key), keyscanTarget(hostname), p.path)
			}
			// Unknown host: trust on first use.
			return p.add(hostname, key)
		}
		if errors.As(err, &keyErr) {
			return fmt.Errorf("host key mismatch for %s — the server's key differs from the one in %s (line %d). "+
				"This can mean a man-in-the-middle attack, or that the server was reinstalled. "+
				"If the change is expected, remove the old entry with: ssh-keygen -R %s",
				hostname, keyErr.Want[0].Filename, keyErr.Want[0].Line, knownhosts.Normalize(hostname))
		}
		return err
	}, nil
}

// algorithms returns the host key algorithms already recorded for addr, so
// the server is asked for a key type we can actually verify. Without this a
// server offering e.g. ed25519 first would look like a key mismatch when only
// its ecdsa key is on file. Returns nil for an unknown host (any type is fine).
func (p hostKeyPolicy) algorithms(addr string) []string {
	check, err := knownhosts.New(p.path)
	if err != nil {
		return nil
	}
	// Probe with a throwaway key: the KeyError lists the recorded keys.
	probe := probeKey()
	if probe == nil {
		return nil
	}
	err = check(addr, &net.TCPAddr{IP: net.IPv4zero}, probe)
	var keyErr *knownhosts.KeyError
	if !errors.As(err, &keyErr) || len(keyErr.Want) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var algos []string
	for _, k := range keyErr.Want {
		for _, a := range algorithmsForKeyType(k.Key.Type()) {
			if !seen[a] {
				seen[a] = true
				algos = append(algos, a)
			}
		}
	}
	return algos
}

// algorithmsForKeyType maps a key type to the host key signature algorithms
// that can be verified with it (RSA keys sign with SHA-2 variants too).
func algorithmsForKeyType(keyType string) []string {
	if keyType == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	}
	return []string{keyType}
}

// keyscanTarget renders host:port as ssh-keyscan arguments.
func keyscanTarget(hostport string) string {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil || port == "22" {
		if err != nil {
			return hostport
		}
		return host
	}
	return "-p " + port + " " + host
}

// add appends a known_hosts line for the host.
func (p hostKeyPolicy) add(hostname string, key ssh.PublicKey) error {
	knownHostsMu.Lock()
	defer knownHostsMu.Unlock()

	f, err := os.OpenFile(p.path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("recording host key: %w", err)
	}
	defer f.Close()

	if _, err := fmt.Fprintln(f, knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)); err != nil {
		return fmt.Errorf("recording host key: %w", err)
	}
	return nil
}

var (
	probeOnce sync.Once
	probePub  ssh.PublicKey
)

// probeKey returns a random public key used only to query known_hosts.
func probeKey() ssh.PublicKey {
	probeOnce.Do(func() {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return
		}
		probePub, _ = ssh.NewPublicKey(pub)
	})
	return probePub
}

// ensureFile creates path (mode 0600, parent 0700) if it does not exist.
func ensureFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	return f.Close()
}
