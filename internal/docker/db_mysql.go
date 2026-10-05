package docker

import "fmt"

// DetectMySQLCredentials reads MySQL credentials from container env.
// Prefers root with MYSQL_ROOT_PASSWORD (a non-root MYSQL_USER usually cannot
// see every database); falls back to MYSQL_USER/MYSQL_PASSWORD, then to root
// without a password. MARIADB_* variables are honoured too.
func DetectMySQLCredentials(r Runner, containerID string) (user, password string, err error) {
	env, err := containerEnv(r, containerID)
	if err != nil {
		return "", "", err
	}
	first := func(keys ...string) string {
		for _, k := range keys {
			if v := env[k]; v != "" {
				return v
			}
		}
		return ""
	}
	if rootPass := first("MYSQL_ROOT_PASSWORD", "MARIADB_ROOT_PASSWORD"); rootPass != "" {
		return "root", rootPass, nil
	}
	if u := first("MYSQL_USER", "MARIADB_USER"); u != "" {
		return u, first("MYSQL_PASSWORD", "MARIADB_PASSWORD"), nil
	}
	return "root", "", nil
}

// mysqlClient / mysqlDump pick the client binaries: MariaDB 11+ images only
// ship "mariadb" / "mariadb-dump".
const (
	mysqlClient = `"$(command -v mysql || command -v mariadb || echo mysql)"`
	mysqlDump   = `"$(command -v mysqldump || command -v mariadb-dump || echo mysqldump)"`
)

// mysqlSecrets passes the password via stdin → MYSQL_PWD.
func mysqlSecrets(password string) []Secret {
	return []Secret{{Name: "MYSQL_PWD", Value: password}}
}

// ListMySQLDatabases returns non-system databases from the MySQL container.
func ListMySQLDatabases(r Runner, containerID, user, password string) ([]string, error) {
	script := mysqlClient + " -u " + shellQuote(user) + " --batch --skip-column-names -e " + shellQuote("SHOW DATABASES")
	hc := ExecScript(containerID, script, mysqlSecrets(password)...)
	out, err := r.RunOutput(hc.Cmd, hc.Input)
	if err != nil {
		return nil, fmt.Errorf("listing databases: %w", err)
	}
	return parseDatabaseList(out, map[string]bool{
		"information_schema": true,
		"performance_schema": true,
		"mysql":              true,
		"sys":                true,
	}), nil
}

// MySQLExecCmd builds the command running one SQL statement against dbName,
// printed as a table.
func MySQLExecCmd(containerID, user, password, dbName, sql string) HostCommand {
	script := mysqlClient + " -u " + shellQuote(user) + " --table " + shellQuote(dbName) + " -e " + shellQuote(sql)
	return ExecStreamScript(containerID, script, mysqlSecrets(password)...)
}

// DumpMySQLDatabase streams mysqldump (gzipped in the container) to
// ~/Downloads/<db>_<timestamp>.sql.gz.
func DumpMySQLDatabase(r Runner, containerID, user, password, dbName string) (<-chan string, func(), error) {
	dump := mysqlDump + " --no-tablespaces --single-transaction -u " + shellQuote(user) + " " + shellQuote(dbName)
	hc := ExecStreamScript(containerID, gzipPipe(dump), mysqlSecrets(password)...)
	name := fmt.Sprintf("%s_%s.sql.gz", safeFileName(dbName), timestamp())
	return download(r, hc, "Starting mysqldump...", name, nil)
}
