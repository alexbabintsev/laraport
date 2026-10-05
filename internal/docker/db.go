package docker

import (
	"fmt"
	"strings"
)

// DetectPostgresCredentials reads POSTGRES_USER and POSTGRES_PASSWORD from container env.
func DetectPostgresCredentials(r Runner, containerID string) (user, password string, err error) {
	env, err := containerEnv(r, containerID)
	if err != nil {
		return "", "", err
	}
	user = env["POSTGRES_USER"]
	if user == "" {
		user = "postgres"
	}
	return user, env["POSTGRES_PASSWORD"], nil
}

// pgSecrets passes the password to psql/pg_dump via stdin → PGPASSWORD.
func pgSecrets(password string) []Secret {
	return []Secret{{Name: "PGPASSWORD", Value: password}}
}

// ListDatabases returns non-system databases from the PostgreSQL container.
func ListDatabases(r Runner, containerID, user, password string) ([]string, error) {
	// One name per line, straight from the catalog: robust against names with
	// '|' or spaces and against psql's multi-line ACL column.
	script := "psql -U " + shellQuote(user) + " -d postgres -X -A -t -c " +
		shellQuote("SELECT datname FROM pg_database WHERE NOT datistemplate AND datallowconn ORDER BY datname")
	hc := ExecScript(containerID, script, pgSecrets(password)...)
	out, err := r.RunOutput(hc.Cmd, hc.Input)
	if err != nil {
		return nil, fmt.Errorf("listing databases: %w", err)
	}
	return parseDatabaseList(out, map[string]bool{"postgres": true}), nil
}

// parseDatabaseList turns one-name-per-line output into a deduplicated list,
// leaving out the names in skip.
func parseDatabaseList(out string, skip map[string]bool) []string {
	var dbs []string
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimRight(line, "\r")
		if strings.TrimSpace(name) == "" || skip[name] || seen[name] {
			continue
		}
		seen[name] = true
		dbs = append(dbs, name)
	}
	return dbs
}

// PostgresExecCmd builds the command running one SQL statement (or psql
// meta-command) against dbName, printed as a psql table.
func PostgresExecCmd(containerID, user, password, dbName, sql string) HostCommand {
	script := "psql -U " + shellQuote(user) + " -d " + shellQuote(dbName) + " -X -c " + shellQuote(sql)
	return ExecStreamScript(containerID, script, pgSecrets(password)...)
}

// PGDumpFormat selects the pg_dump output flavour.
type PGDumpFormat int

const (
	PGDumpPlain   PGDumpFormat = iota // plain SQL with COPY, gzipped
	PGDumpInserts                     // plain SQL with INSERT statements, gzipped
	PGDumpCustom                      // pg_dump -Fc archive for pg_restore
)

// DumpPostgres streams pg_dump to ~/Downloads/<db>_<timestamp>.sql.gz (or
// .dump for the custom format). See download for the channel/stop contract.
func DumpPostgres(r Runner, containerID, user, password, dbName string, format PGDumpFormat) (<-chan string, func(), error) {
	base := "pg_dump --no-owner --no-acl -U " + shellQuote(user) + " -d " + shellQuote(dbName)
	var script, ext string
	var filter filterFunc
	switch format {
	case PGDumpCustom:
		// -Fc output is already compressed.
		script, ext = base+" -Fc", "dump"
	case PGDumpInserts:
		script, ext, filter = gzipPipe(base+" --inserts --column-inserts"), "sql.gz", stripRestrictLines
	default:
		script, ext, filter = gzipPipe(base), "sql.gz", stripRestrictLines
	}
	hc := ExecStreamScript(containerID, script, pgSecrets(password)...)
	name := fmt.Sprintf("%s_%s.%s", safeFileName(dbName), timestamp(), ext)
	return download(r, hc, "Starting pg_dump...", name, filter)
}
