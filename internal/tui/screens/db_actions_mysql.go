package screens

import (
	"fmt"
	"strings"
)

// buildMySQLActionGroups returns the action groups for a MySQL database.
func buildMySQLActionGroups(dbName string) []dbActionGroup {
	db := strings.ReplaceAll(dbName, "'", "''") // SQL-escape for inline literals
	return []dbActionGroup{
		{
			name: "Info & Stats",
			actions: []dbAction{
				{
					"DB size", "Total size on disk (data + indexes)",
					fmt.Sprintf(`SELECT ROUND(SUM(data_length+index_length)/1024/1024,2) AS size_mb FROM information_schema.tables WHERE table_schema='%s'`, db),
					"",
				},
				{
					"DB version / uptime", "Server version and uptime in seconds",
					`SELECT VERSION() AS version, VARIABLE_VALUE AS uptime_seconds FROM performance_schema.global_status WHERE VARIABLE_NAME='Uptime'`,
					"",
				},
				{
					"Table sizes (top 20)", "Largest tables by total size",
					fmt.Sprintf(`SELECT table_name, ROUND((data_length+index_length)/1024/1024,2) AS total_mb, ROUND(data_length/1024/1024,2) AS data_mb, ROUND(index_length/1024/1024,2) AS index_mb FROM information_schema.tables WHERE table_schema='%s' ORDER BY data_length+index_length DESC LIMIT 20`, db),
					"",
				},
				{
					"Table row counts", "Estimated row count per table",
					fmt.Sprintf(`SELECT table_name, table_rows AS rows FROM information_schema.tables WHERE table_schema='%s' ORDER BY table_rows DESC`, db),
					"",
				},
				{
					"Active connections", "Current sessions on this database",
					fmt.Sprintf(`SELECT id, user, host, command, time, state, LEFT(info,80) AS query FROM information_schema.processlist WHERE db='%s' ORDER BY time DESC`, db),
					"",
				},
				{
					"Long running queries", "Queries running more than 5 seconds",
					`SELECT id, user, time, state, LEFT(info,100) AS query FROM information_schema.processlist WHERE command='Query' AND time>5 ORDER BY time DESC`,
					"",
				},
			},
		},
		{
			name: "Schema",
			actions: []dbAction{
				{
					"List tables", "All base tables",
					fmt.Sprintf(`SELECT table_name, engine, table_rows AS rows FROM information_schema.tables WHERE table_schema='%s' AND table_type='BASE TABLE' ORDER BY table_name`, db),
					"",
				},
				{
					"List views", "All views",
					fmt.Sprintf(`SELECT table_name AS view_name FROM information_schema.views WHERE table_schema='%s' ORDER BY table_name`, db),
					"",
				},
				{
					"List columns", "All columns with type per table",
					fmt.Sprintf(`SELECT table_name, column_name, column_type, is_nullable, column_key FROM information_schema.columns WHERE table_schema='%s' ORDER BY table_name, ordinal_position`, db),
					"",
				},
				{
					"List foreign keys", "Foreign key constraints",
					fmt.Sprintf(`SELECT table_name, column_name, constraint_name, referenced_table_name, referenced_column_name FROM information_schema.key_column_usage WHERE table_schema='%s' AND referenced_table_name IS NOT NULL ORDER BY table_name`, db),
					"",
				},
				{
					"List routines", "Stored procedures and functions",
					fmt.Sprintf(`SELECT routine_name, routine_type FROM information_schema.routines WHERE routine_schema='%s' ORDER BY routine_type, routine_name`, db),
					"",
				},
			},
		},
		{
			name: "Indexes",
			actions: []dbAction{
				{
					"List indexes", "All indexes per table",
					fmt.Sprintf(`SELECT table_name, index_name, GROUP_CONCAT(column_name ORDER BY seq_in_index) AS columns, NON_UNIQUE AS non_unique FROM information_schema.statistics WHERE table_schema='%s' GROUP BY table_name, index_name, NON_UNIQUE ORDER BY table_name, index_name`, db),
					"",
				},
				{
					"Tables without primary key", "Tables missing a PRIMARY KEY",
					fmt.Sprintf(`SELECT t.table_name FROM information_schema.tables t LEFT JOIN information_schema.statistics s ON s.table_schema=t.table_schema AND s.table_name=t.table_name AND s.index_name='PRIMARY' WHERE t.table_schema='%s' AND t.table_type='BASE TABLE' AND s.index_name IS NULL`, db),
					"",
				},
			},
		},
		{
			name: "Maintenance",
			actions: []dbAction{
				{
					"Table status", "Engine, rows, data/index size, auto_increment",
					fmt.Sprintf(`SELECT table_name, engine, table_rows, ROUND(data_free/1024/1024,2) AS free_mb, auto_increment FROM information_schema.tables WHERE table_schema='%s' ORDER BY data_free DESC`, db),
					"",
				},
				{
					"InnoDB lock waits", "Current InnoDB lock waits",
					`SELECT * FROM performance_schema.data_lock_waits LIMIT 50`,
					"",
				},
				{
					"ANALYZE all tables", "Update table statistics (run per table manually if needed)",
					fmt.Sprintf(`SELECT CONCAT('ANALYZE TABLE ', table_name, ';') AS stmt FROM information_schema.tables WHERE table_schema='%s' AND table_type='BASE TABLE'`, db),
					"",
				},
			},
		},
		{
			name: "Query",
			actions: []dbAction{
				{"Run SQL query", "Enter and run any SQL statement", "", "sql_input"},
			},
		},
		{
			name: "Backup",
			actions: []dbAction{
				{"Download SQL dump", "mysqldump --single-transaction → .sql.gz", "", "download"},
			},
		},
	}
}
