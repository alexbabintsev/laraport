package config

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type ServerType string

const (
	ServerTypeSSH   ServerType = "ssh"
	ServerTypeLocal ServerType = "local"
)

type Server struct {
	Name       string            `yaml:"name"`
	Host       string            `yaml:"host"`
	Port       int               `yaml:"port"`
	User       string            `yaml:"user"`
	Key        string            `yaml:"key"`
	Passphrase string            `yaml:"passphrase"`
	Type       ServerType        `yaml:"type"`
	Containers []ContainerConfig `yaml:"containers"`

	// implicit marks the Local server added when the config defines none; it
	// is not written back by Save unless it gained container settings.
	implicit bool
}

// FindContainerConfig returns the ContainerConfig matching a docker container name.
// The Name field supports glob patterns (e.g. "slu-app*").
// Exact matches take precedence over glob matches; earlier entries win.
func (s Server) FindContainerConfig(containerName string) (ContainerConfig, bool) {
	var globMatch *ContainerConfig
	for i, cc := range s.Containers {
		matched, err := path.Match(cc.Name, containerName)
		if err != nil {
			continue
		}
		if !matched {
			continue
		}
		if cc.Name == containerName {
			return cc, true
		}
		if globMatch == nil {
			globMatch = &s.Containers[i]
		}
	}
	if globMatch != nil {
		return *globMatch, true
	}
	return ContainerConfig{}, false
}

type Command struct {
	Label string `yaml:"label"`
	Cmd   string `yaml:"cmd"`
	Desc  string `yaml:"desc"`
}

type CommandGroup struct {
	Name     string    `yaml:"name"`
	Commands []Command `yaml:"commands"`
}

// ContainerConfig holds per-container overrides within a server.
type ContainerConfig struct {
	Name        string         `yaml:"name"`         // docker container name or ID prefix to match
	DisplayName string         `yaml:"display_name"` // custom display name
	Favorite    bool           `yaml:"favorite"`     // show with star, yellow, sorted to top
	Hidden      bool           `yaml:"hidden"`       // hide from container list
	RootPath    string         `yaml:"root_path"`    // custom path to app root (default: /var/www/html)
	CustomLogs  []string       `yaml:"custom_logs"`  // extra log file paths inside the container
	Commands    []CommandGroup `yaml:"commands"`     // configurable command groups for the Commands screen
}

type Config struct {
	Servers  []Server       `yaml:"servers"`
	Commands []CommandGroup `yaml:"commands"`
}

func Load(path string) (*Config, error) {
	cfg := &Config{}

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	if err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parsing config: %w", err)
		}
	}

	for i := range cfg.Servers {
		cfg.Servers[i].Key = expandHome(cfg.Servers[i].Key)
		if cfg.Servers[i].Port == 0 {
			cfg.Servers[i].Port = 22
		}
	}

	// If no servers defined, add a local server
	if len(cfg.Servers) == 0 {
		cfg.Servers = append(cfg.Servers, Server{
			Name:     "Local",
			Type:     ServerTypeLocal,
			implicit: true,
		})
	}

	return cfg, nil
}

// expandHome expands a leading "~" or "~/" to the home directory. "~user"
// forms are left untouched.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[1:])
}

func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(home, ".config", "laradok", "config.yaml")
}

// Save writes the config back to disk as YAML. It re-collapses absolute SSH key
// paths under the home directory back to "~/…" so saving doesn't bake in an
// absolute path that Load expanded. The whole file is rewritten, so any
// comments or custom formatting in the original are not preserved.
//
// The file may hold SSH key passphrases, so it is written with mode 0600, and
// atomically (temp file + rename) so a crash cannot leave it truncated. If
// path is a symlink (e.g. a dotfiles checkout), the link target is updated.
func (c *Config) Save(path string) error {
	// Work on a copy so the in-memory (expanded) paths keep working.
	out := *c
	out.Servers = nil
	home, _ := os.UserHomeDir()
	for _, s := range c.Servers {
		if s.implicit {
			continue
		}
		if home != "" && strings.HasPrefix(s.Key, home+string(filepath.Separator)) {
			s.Key = "~" + strings.TrimPrefix(s.Key, home)
		}
		out.Servers = append(out.Servers, s)
	}

	data, err := yaml.Marshal(&out)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	return writeFileAtomic(path, data)
}

// writeFileAtomic replaces path (or the file it links to) with data, mode 0600.
func writeFileAtomic(path string, data []byte) error {
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp") // mode 0600
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// UpsertContainerConfig stores per-container overrides for the named server,
// matching by exact container name. If an exact-name entry exists it is updated
// in place; otherwise a new entry is appended (so it takes precedence over any
// glob rule). Returns false if the server is not found.
func (c *Config) UpsertContainerConfig(serverName string, cc ContainerConfig) bool {
	for si := range c.Servers {
		if c.Servers[si].Name != serverName {
			continue
		}
		for ci := range c.Servers[si].Containers {
			if c.Servers[si].Containers[ci].Name == cc.Name {
				// Preserve existing command groups / custom logs not edited here.
				existing := c.Servers[si].Containers[ci]
				cc.Commands = existing.Commands
				cc.CustomLogs = existing.CustomLogs
				c.Servers[si].Containers[ci] = cc
				c.Servers[si].implicit = false
				return true
			}
		}
		c.Servers[si].Containers = append(c.Servers[si].Containers, cc)
		// A container override must survive Save, so the server is now part
		// of the written config.
		c.Servers[si].implicit = false
		return true
	}
	return false
}
