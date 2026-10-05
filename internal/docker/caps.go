package docker

import (
	"fmt"
	"strings"
)

// ContainerCaps describes which features are available in a container.
type ContainerCaps struct {
	HasLaravel  bool   // artisan file exists
	HasComposer bool   // composer binary or composer.phar present
	HasNpm      bool   // npm binary present
	HasPostgres bool   // psql binary present
	HasMySQL    bool   // mysql binary present
	IsMariaDB   bool   // mysql client reports a MariaDB distribution
	IsPercona   bool   // mysql client reports a Percona distribution
	HasSQLite   bool   // sqlite3 binary present
	HasRedis    bool   // redis-cli binary present
	MongoBin    string // "mongosh", "mongo", or "" — mongo shell binary present
	HasPHP      bool   // php binary present

	// LaravelRoot is the directory where artisan was actually found (the
	// configured root or /app). Empty when HasLaravel is false. Callers use it
	// to run artisan/logs/storage against the right path.
	LaravelRoot string
}

// HasMongo reports whether a mongo shell client is available.
func (c ContainerCaps) HasMongo() bool { return c.MongoBin != "" }

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
	root := appRoot(rootPath)

	// Each check emits a tag if the binary/file exists. The Laravel/Composer
	// file checks try the configured root and /app, a common alternate root
	// (FrankenPHP/Octane images, some Sail setups).
	script := fmt.Sprintf(
		`if [ -f %s ]; then echo LARAVEL_ROOT=%s; elif [ -f /app/artisan ]; then echo LARAVEL_ROOT=/app; fi; `+
			`(command -v composer >/dev/null 2>&1 || [ -f %s ] || [ -f /app/composer.phar ]) && echo HAS_COMPOSER; `+
			`command -v npm >/dev/null 2>&1 && echo HAS_NPM; `+
			`command -v psql >/dev/null 2>&1 && echo HAS_POSTGRES; `+
			// MariaDB 11+ images ship only the "mariadb" client (no mysql symlink).
			`m=$(command -v mysql || command -v mariadb) && { echo HAS_MYSQL; v=$("$m" --version 2>/dev/null); echo "$v" | grep -qi mariadb && echo IS_MARIADB; echo "$v" | grep -qi percona && echo IS_PERCONA; }; `+
			`command -v sqlite3 >/dev/null 2>&1 && echo HAS_SQLITE; `+
			`command -v redis-cli >/dev/null 2>&1 && echo HAS_REDIS; `+
			`if command -v mongosh >/dev/null 2>&1; then echo HAS_MONGOSH; elif command -v mongo >/dev/null 2>&1; then echo HAS_MONGO; fi; `+
			`command -v php >/dev/null 2>&1 && echo HAS_PHP; `+
			`true`,
		shellQuote(root+"/artisan"), shellQuote(root), shellQuote(root+"/composer.phar"),
	)
	cmd := ExecShCmd(containerID, script)

	out, err := r.RunOutput(cmd, "")
	if err != nil {
		return ContainerCaps{}, fmt.Errorf("probing container: %w", err)
	}

	caps := ContainerCaps{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if r, ok := strings.CutPrefix(line, "LARAVEL_ROOT="); ok {
			caps.HasLaravel = true
			caps.LaravelRoot = r
			continue
		}
		switch line {
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
		case "HAS_REDIS":
			caps.HasRedis = true
		case "HAS_MONGOSH":
			caps.MongoBin = "mongosh"
		case "HAS_MONGO":
			caps.MongoBin = "mongo"
		case "HAS_PHP":
			caps.HasPHP = true
		}
	}
	return caps, nil
}
