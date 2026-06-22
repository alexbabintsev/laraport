package screens

// buildSQLiteActionGroups returns the action groups for a SQLite database file.
// Unlike the server engines, there is no per-database isolation — the queries
// run against the single attached file.
func buildSQLiteActionGroups() []dbActionGroup {
	return []dbActionGroup{
		{
			name: "Info & Stats",
			actions: []dbAction{
				{
					"SQLite version", "Library version of the sqlite3 client",
					`SELECT sqlite_version() AS version`,
					"",
				},
				{
					"DB size", "Page count × page size, in bytes",
					`SELECT (SELECT page_count FROM pragma_page_count()) * (SELECT page_size FROM pragma_page_size()) AS size_bytes`,
					"",
				},
				{
					"Table row counts", "Row count per table",
					`SELECT name AS table_name, (SELECT COUNT(*) FROM "main".name) AS rows FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`,
					"",
				},
				{
					"Encoding & journal mode", "Text encoding and journal/WAL mode",
					`SELECT (SELECT encoding FROM pragma_encoding()) AS encoding, (SELECT journal_mode FROM pragma_journal_mode()) AS journal_mode`,
					"",
				},
			},
		},
		{
			name: "Schema",
			actions: []dbAction{
				{
					"List tables", "All user tables",
					`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`,
					"",
				},
				{
					"List views", "All views",
					`SELECT name FROM sqlite_master WHERE type='view' ORDER BY name`,
					"",
				},
				{
					"List triggers", "All triggers",
					`SELECT name, tbl_name AS on_table FROM sqlite_master WHERE type='trigger' ORDER BY name`,
					"",
				},
				{
					"Table definitions", "Stored CREATE statements for every table",
					`SELECT name, sql FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`,
					"",
				},
			},
		},
		{
			name: "Indexes",
			actions: []dbAction{
				{
					"List indexes", "All indexes with their table",
					`SELECT name, tbl_name AS on_table FROM sqlite_master WHERE type='index' AND name NOT LIKE 'sqlite_%' ORDER BY tbl_name, name`,
					"",
				},
				{
					"Index definitions", "Stored CREATE INDEX statements",
					`SELECT name, sql FROM sqlite_master WHERE type='index' AND sql IS NOT NULL ORDER BY tbl_name, name`,
					"",
				},
			},
		},
		{
			name: "Maintenance",
			actions: []dbAction{
				{
					"Integrity check", "Run PRAGMA integrity_check",
					`PRAGMA integrity_check`,
					"",
				},
				{
					"Foreign key check", "Report foreign key violations",
					`PRAGMA foreign_key_check`,
					"",
				},
				{
					"Freelist (vacuum candidate)", "Unused page count — high values suggest VACUUM",
					`SELECT (SELECT freelist_count FROM pragma_freelist_count()) AS free_pages`,
					"",
				},
				{
					"VACUUM", "Rebuild the database file to reclaim space",
					`VACUUM`,
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
				{"Download SQL dump", "sqlite3 .dump | gzip → .sql.gz", "", "download"},
			},
		},
	}
}
