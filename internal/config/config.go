package config

import (
	"fmt"
	"os"
	"path"
	"path/filepath"

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
	Name           string         `yaml:"name"`            // docker container name or ID prefix to match
	DisplayName    string         `yaml:"display_name"`    // custom display name
	Favorite       bool           `yaml:"favorite"`        // show with star, yellow, sorted to top
	Hidden         bool           `yaml:"hidden"`          // hide from container list
	RootPath   string         `yaml:"root_path"`  // custom path to app root (default: /var/www/html)
	CustomLogs []string       `yaml:"custom_logs"` // extra log file paths inside the container
	Commands   []CommandGroup `yaml:"commands"`    // configurable command groups for the Commands screen
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

	// Expand ~ in key paths
	for i, s := range cfg.Servers {
		if len(s.Key) > 0 && s.Key[0] == '~' {
			home, err := os.UserHomeDir()
			if err == nil {
				cfg.Servers[i].Key = filepath.Join(home, s.Key[1:])
			}
		}
		if cfg.Servers[i].Port == 0 {
			cfg.Servers[i].Port = 22
		}
	}

	// If no servers defined, add a local server
	if len(cfg.Servers) == 0 {
		cfg.Servers = append(cfg.Servers, Server{
			Name: "Local",
			Type: ServerTypeLocal,
		})
	}

	return cfg, nil
}

func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(home, ".config", "laradok", "config.yaml")
}
