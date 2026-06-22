package docker

import "fmt"

// DBEngine identifies which database server runs in a container.
type DBEngine string

const (
	EnginePostgres DBEngine = "postgres"
	EngineMySQL    DBEngine = "mysql"
	EngineMariaDB  DBEngine = "mariadb"
	EnginePercona  DBEngine = "percona"
)

// IsMySQLFamily reports whether the engine speaks the MySQL protocol/CLI
// (the `mysql` client, `information_schema`, `mysqldump`).
func (e DBEngine) IsMySQLFamily() bool {
	return e == EngineMySQL || e == EngineMariaDB || e == EnginePercona
}

// Label returns a human-readable name for the engine.
func (e DBEngine) Label() string {
	switch e {
	case EngineMySQL:
		return "MySQL"
	case EngineMariaDB:
		return "MariaDB"
	case EnginePercona:
		return "Percona"
	default:
		return "PostgreSQL"
	}
}

// DBExecHostCmd builds the host shell command that runs a single SQL statement
// against the given database, formatted as a readable table, for the engine.
func DBExecHostCmd(engine DBEngine, containerID, user, password, dbName, sql string) string {
	if engine.IsMySQLFamily() {
		return MySQLExecCmd(containerID, user, password, dbName, sql)
	}
	return fmt.Sprintf(
		`docker exec -e PGPASSWORD=%s -e PGUSER=%s %s psql -d %s -c %s`,
		ShellQuote(password), ShellQuote(user), containerID,
		ShellQuote(dbName), ShellQuote(sql),
	)
}
