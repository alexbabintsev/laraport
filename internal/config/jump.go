package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Jump is a parsed jump_host.
type Jump struct {
	User string
	Host string
	Port int
}

// ParseJumpHost parses "[user@]host[:port]" (IPv6 as "[::1]:port"), like
// the value of OpenSSH's ProxyJump. Only one hop is supported.
func ParseJumpHost(spec string) (Jump, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return Jump{}, errors.New("jump host is empty")
	}
	if strings.Contains(spec, ",") {
		return Jump{}, errors.New("only one jump host is supported")
	}
	if strings.HasPrefix(spec, "ssh://") {
		spec = strings.TrimPrefix(spec, "ssh://")
	}
	var j Jump
	if at := strings.LastIndex(spec, "@"); at >= 0 {
		j.User, spec = spec[:at], spec[at+1:]
		if j.User == "" || strings.ContainsAny(j.User, " \t@") || strings.HasPrefix(j.User, "-") {
			return Jump{}, fmt.Errorf("invalid jump host user %q", j.User)
		}
	}
	host, port := spec, ""
	switch {
	case strings.HasPrefix(spec, "["):
		end := strings.Index(spec, "]")
		if end < 0 {
			return Jump{}, fmt.Errorf("invalid jump host %q", spec)
		}
		host = spec[1:end]
		if rest := spec[end+1:]; rest != "" {
			if !strings.HasPrefix(rest, ":") {
				return Jump{}, fmt.Errorf("invalid jump host %q", spec)
			}
			port = rest[1:]
		}
	case strings.Count(spec, ":") == 1:
		host, port, _ = strings.Cut(spec, ":")
	}
	if host == "" || strings.ContainsAny(host, " \t/@[]") || strings.HasPrefix(host, "-") {
		return Jump{}, fmt.Errorf("invalid jump host %q", host)
	}
	j.Host, j.Port = host, 22
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return Jump{}, fmt.Errorf("invalid jump host port %q", port)
		}
		j.Port = n
	}
	return j, nil
}

// String renders the jump host as "user@host:port" (port omitted when 22),
// suitable for `ssh -J`.
func (j Jump) String() string {
	host := j.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	s := host
	if j.User != "" {
		s = j.User + "@" + s
	}
	if j.Port != 0 && j.Port != 22 {
		s += ":" + strconv.Itoa(j.Port)
	}
	return s
}

// ResolvedJump returns the server's jump host with defaults applied: the
// server's user when the jump host names none, and the key to use for it
// (jump_key, else the server's key; "" = ssh-agent / default keys) plus its
// passphrase. ok is false when the server has no jump host.
func (s Server) ResolvedJump() (j Jump, key, passphrase string, ok bool, err error) {
	if strings.TrimSpace(s.JumpHost) == "" {
		return Jump{}, "", "", false, nil
	}
	j, err = ParseJumpHost(s.JumpHost)
	if err != nil {
		return Jump{}, "", "", false, err
	}
	if j.User == "" {
		j.User = s.User
	}
	key = s.JumpKey
	if key == "" {
		key, passphrase = s.Key, s.Passphrase
	}
	return j, key, passphrase, true, nil
}
