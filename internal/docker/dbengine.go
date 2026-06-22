package docker

import "fmt"

// DBEngine identifies which database server runs in a container.
type DBEngine string

const (
	EnginePostgres DBEngine = "postgres"
	EngineMySQL    DBEngine = "mysql"
)

// Label returns a human-readable name for the engine.
func (e DBEngine) Label() string {
	switch e {
	case EngineMySQL:
		return "MySQL"
	default:
		return "PostgreSQL"
	}
}

// DBExecHostCmd builds the host shell command that runs a single SQL statement
// against the given database, formatted as a readable table, for the engine.
func DBExecHostCmd(engine DBEngine, containerID, user, password, dbName, sql string) string {
	if engine == EngineMySQL {
		return MySQLExecCmd(containerID, user, password, dbName, sql)
	}
	return fmt.Sprintf(
		`docker exec -e PGPASSWORD=%s -e PGUSER=%s %s psql -d %s -c %s`,
		ShellQuote(password), ShellQuote(user), containerID,
		ShellQuote(dbName), ShellQuote(sql),
	)
}
