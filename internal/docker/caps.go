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
	IsMariaDB   bool // mysql client reports a MariaDB distribution
	IsPercona   bool // mysql client reports a Percona distribution
	HasSQLite   bool // sqlite3 binary present
	HasPHP      bool // php binary present
}

// HasDatabase reports whether any supported database engine is present.
func (c ContainerCaps) HasDatabase() bool {
	return c.HasPostgres || c.HasMySQL || c.HasSQLite
}

// DBEngine returns the database engine to use for this container.
// PostgreSQL takes precedence when multiple clients happen to be installed.
func (c ContainerCaps) DBEngine() DBEngine {
	switch {
	case c.HasPostgres:
		return EnginePostgres
	case c.IsMariaDB:
		return EngineMariaDB
	case c.IsPercona:
		return EnginePercona
	case c.HasMySQL:
		return EngineMySQL
	default:
		// Only reached when HasDatabase() is true but no server client exists.
		return EngineSQLite
	}
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
			`command -v mysql >/dev/null 2>&1 && { v=$(mysql --version 2>/dev/null); echo "$v" | grep -qi mariadb && echo IS_MARIADB; echo "$v" | grep -qi percona && echo IS_PERCONA; }; `+
			`command -v sqlite3 >/dev/null 2>&1 && echo HAS_SQLITE; `+
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
		case "IS_MARIADB":
			caps.IsMariaDB = true
		case "IS_PERCONA":
			caps.IsPercona = true
		case "HAS_SQLITE":
			caps.HasSQLite = true
		case "HAS_PHP":
			caps.HasPHP = true
		}
	}
	return caps, nil
}
