package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Validate checks a server definition entered in the UI.
func (s Server) Validate() error {
	var errs []string
	if strings.TrimSpace(s.Name) == "" {
		errs = append(errs, "name is required")
	}
	switch s.Type {
	case ServerTypeLocal:
	case ServerTypeSSH:
		if strings.TrimSpace(s.Host) == "" {
			errs = append(errs, "host is required for SSH servers")
		} else if strings.ContainsAny(s.Host, " \t\n/@") || strings.HasPrefix(s.Host, "-") {
			errs = append(errs, "host must be a plain host name or IP address")
		}
		if s.Port < 1 || s.Port > 65535 {
			errs = append(errs, "port must be between 1 and 65535")
		}
		if strings.TrimSpace(s.User) == "" {
			errs = append(errs, "user is required for SSH servers")
		} else if strings.ContainsAny(s.User, " \t\n@") || strings.HasPrefix(s.User, "-") {
			errs = append(errs, "user must not contain spaces or '@'")
		}
		errs = append(errs, checkKeyFile("key", s.Key)...)
		if strings.TrimSpace(s.JumpHost) != "" {
			if _, err := ParseJumpHost(s.JumpHost); err != nil {
				errs = append(errs, err.Error())
			}
			errs = append(errs, checkKeyFile("jump key", s.JumpKey)...)
		} else if s.JumpKey != "" {
			errs = append(errs, "jump key is set but there is no jump host")
		}
	default:
		errs = append(errs, "type must be ssh or local")
	}
	if strings.ContainsAny(s.DockerCmd, "\n\r") {
		errs = append(errs, "docker command must be a single line")
	}
	if s.RootPath != "" && !strings.HasPrefix(s.RootPath, "/") {
		errs = append(errs, "root path must be absolute")
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// checkKeyFile validates an optional key path.
func checkKeyFile(what, path string) []string {
	if path == "" {
		return nil
	}
	st, err := os.Stat(expandHome(path))
	switch {
	case err != nil:
		return []string{fmt.Sprintf("%s file %s not found", what, path)}
	case st.IsDir():
		return []string{fmt.Sprintf("%s %s is a directory", what, path)}
	}
	return nil
}

// normalize trims fields and fills defaults before a server is stored.
func (s Server) normalize() Server {
	s.Name = strings.TrimSpace(s.Name)
	s.Host = strings.TrimSpace(s.Host)
	s.User = strings.TrimSpace(s.User)
	s.Key = expandHome(strings.TrimSpace(s.Key))
	s.JumpHost = strings.TrimSpace(s.JumpHost)
	s.JumpKey = expandHome(strings.TrimSpace(s.JumpKey))
	s.DockerCmd = strings.TrimSpace(s.DockerCmd)
	s.RootPath = strings.TrimSpace(s.RootPath)
	if s.Type == "" {
		s.Type = ServerTypeLocal
	}
	if s.Type == ServerTypeLocal {
		// Connection fields mean nothing for the local socket.
		s.Host, s.Port, s.User, s.Key, s.Passphrase = "", 0, "", "", ""
		s.JumpHost, s.JumpKey = "", ""
	}
	if s.Type == ServerTypeSSH && s.Port == 0 {
		s.Port = 22
	}
	return s
}

// IsImplicit reports whether s is the Local server added because the config
// defines no servers.
func (s Server) IsImplicit() bool { return s.implicit }

func (c *Config) indexOf(name string) int {
	for i, s := range c.Servers {
		if s.Name == name {
			return i
		}
	}
	return -1
}

// persistImplicit turns the implicit Local server into a real entry, so that
// adding or editing servers does not make it disappear on the next start.
func (c *Config) persistImplicit() {
	for i := range c.Servers {
		c.Servers[i].implicit = false
	}
}

// AddServer appends a new server after validating it.
func (c *Config) AddServer(s Server) error {
	s = s.normalize()
	if err := s.Validate(); err != nil {
		return err
	}
	if c.indexOf(s.Name) >= 0 {
		return fmt.Errorf("a server named %q already exists", s.Name)
	}
	c.persistImplicit()
	c.Servers = append(c.Servers, s)
	return nil
}

// UpdateServer replaces the server called oldName. Container overrides and a
// passphrase set in the file (which the UI does not edit) are kept.
func (c *Config) UpdateServer(oldName string, s Server) error {
	i := c.indexOf(oldName)
	if i < 0 {
		return fmt.Errorf("server %q not found", oldName)
	}
	s = s.normalize()
	if err := s.Validate(); err != nil {
		return err
	}
	if s.Name != oldName && c.indexOf(s.Name) >= 0 {
		return fmt.Errorf("a server named %q already exists", s.Name)
	}
	old := c.Servers[i]
	s.Containers = old.Containers
	if s.Type == ServerTypeSSH && old.Type == ServerTypeSSH && s.Key == old.Key {
		s.Passphrase = old.Passphrase
	}
	c.persistImplicit()
	c.Servers[i] = s
	return nil
}

// DeleteServer removes a server. When the last one goes, the implicit Local
// server comes back, exactly as with an empty config.
func (c *Config) DeleteServer(name string) error {
	i := c.indexOf(name)
	if i < 0 {
		return fmt.Errorf("server %q not found", name)
	}
	c.Servers = append(c.Servers[:i:i], c.Servers[i+1:]...)
	if len(c.Servers) == 0 {
		c.Servers = []Server{{Name: "Local", Type: ServerTypeLocal, implicit: true}}
	}
	return nil
}

// FindServer returns the server with the given name.
func (c *Config) FindServer(name string) (Server, bool) {
	if i := c.indexOf(name); i >= 0 {
		return c.Servers[i], true
	}
	return Server{}, false
}
