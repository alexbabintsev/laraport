package docker

import (
	"fmt"
	"strings"
)

// ContainerCaps describes which features are available in a container.
type ContainerCaps struct {
	HasLaravel  bool // artisan file exists
	HasComposer bool // composer binary or composer.phar present
	HasNpm      bool // npm binary present
	HasPostgres bool // psql binary present
	HasMySQL    bool // mysql binary present
	HasPHP      bool // php binary present
}

// HasDatabase reports whether any supported database engine is present.
func (c ContainerCaps) HasDatabase() bool {
	return c.HasPostgres || c.HasMySQL
}

// DBEngine returns the database engine to use for this container.
// PostgreSQL takes precedence when both clients happen to be installed.
func (c ContainerCaps) DBEngine() DBEngine {
	if c.HasPostgres {
		return EnginePostgres
	}
	return EngineMySQL
}

// DetectCapabilities probes the container with a single sh -c command to check
// for artisan, composer, npm, psql, and php in one round-trip.
func DetectCapabilities(r Runner, containerID, rootPath string) (ContainerCaps, error) {
	root := defaultRootPath
	if rootPath != "" {
		root = strings.TrimRight(rootPath, "/")
	}

	// Each check emits a tag if the binary/file exists.
	script := fmt.Sprintf(
		`[ -f %s/artisan ] && echo HAS_LARAVEL; `+
			`(command -v composer >/dev/null 2>&1 || [ -f %s/composer.phar ]) && echo HAS_COMPOSER; `+
			`command -v npm >/dev/null 2>&1 && echo HAS_NPM; `+
			`command -v psql >/dev/null 2>&1 && echo HAS_POSTGRES; `+
			`command -v mysql >/dev/null 2>&1 && echo HAS_MYSQL; `+
			`command -v php >/dev/null 2>&1 && echo HAS_PHP; `+
			`true`,
		root, root,
	)
	cmd := fmt.Sprintf(`docker exec %s sh -c %s`, containerID, shellQuote(script))

	out, _ := r.RunCommand(cmd)
	out = strings.Map(func(r rune) rune {
		if r == 0 {
			return -1
		}
		return r
	}, out)

	caps := ContainerCaps{}
	for _, line := range strings.Split(out, "\n") {
		switch strings.TrimSpace(line) {
		case "HAS_LARAVEL":
			caps.HasLaravel = true
		case "HAS_COMPOSER":
			caps.HasComposer = true
		case "HAS_NPM":
			caps.HasNpm = true
		case "HAS_POSTGRES":
			caps.HasPostgres = true
		case "HAS_MYSQL":
			caps.HasMySQL = true
		case "HAS_PHP":
			caps.HasPHP = true
		}
	}
	return caps, nil
}
