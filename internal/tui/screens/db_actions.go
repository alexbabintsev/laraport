package screens

import (
	"fmt"
	"strings"

	"github.com/alexbabintsev/laradok/internal/docker"
	"github.com/alexbabintsev/laradok/internal/msgs"
	"github.com/alexbabintsev/laradok/internal/tui/styles"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type dbAction struct {
	label string
	desc  string
	// sqlQuery is non-empty for direct SQL execution via HostCmd.
	// If empty, the action is handled specially (download or SQL input).
	sqlQuery string
	// special action key
	special string
}

type dbActionGroup struct {
	name    string
	actions []dbAction
}

// buildDBActionGroups dispatches to the engine-specific action groups.
func buildDBActionGroups(engine docker.DBEngine, dbName string) []dbActionGroup {
	if engine == docker.EngineMySQL {
		return buildMySQLActionGroups(dbName)
	}
	return buildPostgresActionGroups(dbName)
}

func buildPostgresActionGroups(dbName string) []dbActionGroup {
	db := strings.ReplaceAll(dbName, "'", "''") // SQL-escape for inline literals
	return []dbActionGroup{
		{
			name: "Info & Stats",
			actions: []dbAction{
				{
					"DB size", "Total size on disk",
					fmt.Sprintf(`SELECT pg_size_pretty(pg_database_size('%s')) AS db_size`, db),
					"",
				},
				{
					"DB version / uptime", "PostgreSQL version and server start time",
					`SELECT version(), pg_postmaster_start_time() AS started_at`,
					"",
				},
				{
					"Table sizes (top 20)", "Largest tables by total size",
					`SELECT schemaname, tablename, pg_size_pretty(pg_total_relation_size(schemaname||'.'||tablename)) AS total, pg_size_pretty(pg_relation_size(schemaname||'.'||tablename)) AS data FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema') ORDER BY pg_total_relation_size(schemaname||'.'||tablename) DESC LIMIT 20`,
					"",
				},
				{
					"Table row counts", "Estimated row count per table",
					`SELECT schemaname, relname AS tablename, n_live_tup AS rows FROM pg_stat_user_tables ORDER BY n_live_tup DESC`,
					"",
				},
				{
					"Cache hit ratio", "Buffer cache hit rate (should be >99%)",
					`SELECT sum(heap_blks_hit)*100.0/nullif(sum(heap_blks_hit)+sum(heap_blks_read),0) AS cache_hit_pct FROM pg_statio_user_tables`,
					"",
				},
				{
					"Active connections", "Current sessions",
					fmt.Sprintf(`SELECT pid, usename, application_name, state, now()-query_start AS duration, left(query,80) AS query FROM pg_stat_activity WHERE datname='%s' AND state IS NOT NULL ORDER BY duration DESC NULLS LAST`, db),
					"",
				},
				{
					"Long running queries", "Queries running more than 5 seconds",
					`SELECT pid, usename, now()-query_start AS duration, state, left(query,100) AS query FROM pg_stat_activity WHERE state='active' AND query_start < now()-interval '5 seconds' ORDER BY duration DESC`,
					"",
				},
			},
		},
		{
			name: "Schema",
			actions: []dbAction{
				{
					"List tables", "All user tables with schema",
					`SELECT schemaname, tablename FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema') ORDER BY schemaname, tablename`,
					"",
				},
				{
					"List schemas", "All non-system schemas",
					`SELECT schema_name, schema_owner FROM information_schema.schemata WHERE schema_name NOT IN ('pg_catalog','information_schema','pg_toast') ORDER BY schema_name`,
					"",
				},
				{
					"List views", "All user-defined views",
					`SELECT schemaname, viewname, viewowner FROM pg_views WHERE schemaname NOT IN ('pg_catalog','information_schema') ORDER BY schemaname, viewname`,
					"",
				},
				{
					"List sequences", "All sequences",
					`SELECT schemaname, sequencename, last_value FROM pg_sequences WHERE schemaname NOT IN ('pg_catalog','information_schema') ORDER BY schemaname, sequencename`,
					"",
				},
				{
					"List functions", "User-defined functions",
					`SELECT n.nspname AS schema, p.proname AS function, pg_get_function_arguments(p.oid) AS args FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname NOT IN ('pg_catalog','information_schema') ORDER BY schema, function`,
					"",
				},
			},
		},
		{
			name: "Indexes",
			actions: []dbAction{
				{
					"Unused indexes", "Indexes never scanned (candidates for removal)",
					`SELECT schemaname, tablename, indexname, idx_scan AS scans FROM pg_stat_user_indexes WHERE idx_scan=0 ORDER BY schemaname, tablename`,
					"",
				},
				{
					"Duplicate indexes", "Indexes with identical columns",
					`SELECT indrelid::regclass AS table, array_agg(indexrelid::regclass) AS indexes FROM pg_index GROUP BY indrelid, indkey HAVING count(*)>1`,
					"",
				},
				{
					"Index usage", "Scan counts per index",
					`SELECT schemaname, tablename, indexname, idx_scan, idx_tup_read, idx_tup_fetch FROM pg_stat_user_indexes ORDER BY idx_scan DESC`,
					"",
				},
			},
		},
		{
			name: "Maintenance",
			actions: []dbAction{
				{
					"Table bloat", "Dead tuples per table (candidates for VACUUM)",
					`SELECT schemaname, relname AS table, n_dead_tup AS dead_rows, n_live_tup AS live_rows, round(n_dead_tup*100.0/nullif(n_live_tup+n_dead_tup,0),1) AS dead_pct, last_autovacuum FROM pg_stat_user_tables ORDER BY n_dead_tup DESC`,
					"",
				},
				{
					"Active locks", "Current lock activity",
					`SELECT pid, relation::regclass AS table, mode, granted, left(query,80) AS query FROM pg_locks l JOIN pg_stat_activity a USING(pid) WHERE relation IS NOT NULL ORDER BY granted, table`,
					"",
				},
				{
					"Replication status", "Streaming replication lag",
					`SELECT client_addr, state, sent_lsn, write_lsn, flush_lsn, replay_lsn, write_lag, flush_lag, replay_lag FROM pg_stat_replication`,
					"",
				},
				{
					"VACUUM ANALYZE", "Run VACUUM ANALYZE on all tables",
					`VACUUM ANALYZE`,
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
				{"Download SQL dump", "pg_dump --no-owner --no-acl → .sql.gz", "", "download"},
				{"Download SQL dump (inserts)", "Same but with --inserts --column-inserts (slower, more portable)", "", "download_inserts"},
				{"Download custom dump", "pg_dump -Fc --no-owner --no-acl → .dump (for pg_restore)", "", "download_custom"},
			},
		},
	}
}

type dbActionRow struct {
	isHeader bool
	label    string
	action   dbAction
	idx      int
}

// DBActionsScreen shows actions available for a selected database.
type DBActionsScreen struct {
	dbName      string
	containerID string
	user        string
	password    string
	engine      docker.DBEngine
	historyKey  string
	rows        []dbActionRow
	actions     []dbAction
	selected    int
	scrollOff   int
	width       int
	height      int
}

func NewDBActionsScreen(dbName, user, password, containerID string, engine docker.DBEngine, historyKey string, width, height int) *DBActionsScreen {
	s := &DBActionsScreen{
		dbName:      dbName,
		containerID: containerID,
		user:        user,
		password:    password,
		engine:      engine,
		historyKey:  historyKey,
		width:       width,
		height:      height,
	}
	s.buildRows(dbName)
	return s
}

func (s *DBActionsScreen) buildRows(dbName string) {
	s.rows = nil
	s.actions = nil
	for _, g := range buildDBActionGroups(s.engine, dbName) {
		s.rows = append(s.rows, dbActionRow{isHeader: true, label: g.name, idx: -1})
		for _, a := range g.actions {
			s.rows = append(s.rows, dbActionRow{action: a, idx: len(s.actions)})
			s.actions = append(s.actions, a)
		}
	}
	s.selected = 0
}

func (s *DBActionsScreen) Init() tea.Cmd { return nil }

func (s *DBActionsScreen) visibleRows() int {
	v := s.height - 4
	if v < 3 {
		v = 3
	}
	return v
}

func (s *DBActionsScreen) rowOfSelected() int {
	for i, r := range s.rows {
		if !r.isHeader && r.idx == s.selected {
			return i
		}
	}
	return 0
}

func (s *DBActionsScreen) clampScroll() {
	vis := s.visibleRows()
	sel := s.rowOfSelected()
	if sel < s.scrollOff {
		s.scrollOff = sel
	}
	if sel >= s.scrollOff+vis {
		s.scrollOff = sel - vis + 1
	}
	if s.scrollOff < 0 {
		s.scrollOff = 0
	}
}

func (s *DBActionsScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return s, tea.Quit
		case "esc":
			return s, func() tea.Msg { return msgs.PopMsg{} }
		case "down", "j":
			if s.selected < len(s.actions)-1 {
				s.selected++
				s.clampScroll()
			}
		case "up", "k":
			if s.selected > 0 {
				s.selected--
				s.clampScroll()
			}
		case "pgdown":
			s.selected += s.visibleRows()
			if s.selected >= len(s.actions) {
				s.selected = len(s.actions) - 1
			}
			s.clampScroll()
		case "pgup":
			s.selected -= s.visibleRows()
			if s.selected < 0 {
				s.selected = 0
			}
			s.clampScroll()
		case "enter":
			if s.selected < 0 || s.selected >= len(s.actions) {
				return s, nil
			}
			a := s.actions[s.selected]
			dbName := s.dbName
			user := s.user
			pass := s.password

			engine := s.engine
			switch a.special {
			case "sql_input":
				hk := s.historyKey
				return s, func() tea.Msg {
					return msgs.PushSQLInputMsg{DBName: dbName, User: user, Password: pass, Engine: engine, HistoryKey: hk}
				}
			case "download":
				return s, func() tea.Msg {
					return msgs.PushDBDownloadMsg{DBName: dbName, User: user, Password: pass, Engine: engine, CustomFormat: false, Inserts: false}
				}
			case "download_inserts":
				return s, func() tea.Msg {
					return msgs.PushDBDownloadMsg{DBName: dbName, User: user, Password: pass, Engine: engine, CustomFormat: false, Inserts: true}
				}
			case "download_custom":
				return s, func() tea.Msg {
					return msgs.PushDBDownloadMsg{DBName: dbName, User: user, Password: pass, Engine: engine, CustomFormat: true, Inserts: false}
				}
			default:
				// Direct SQL via HostCmd — use docker exec -e to avoid shell quoting issues.
				hostCmd := docker.DBExecHostCmd(engine, s.containerID, user, pass, dbName, a.sqlQuery)
				title := a.label + " — " + dbName
				return s, func() tea.Msg {
					return msgs.PushOutputMsg{Title: title, HostCmd: hostCmd}
				}
			}
		}
	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
	}
	return s, nil
}

func (s *DBActionsScreen) View() string {
	title := styles.TitleBar.Render("Database: " + s.dbName)

	groupStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#F1C40F")).Bold(true)
	cmdStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#2ECC71"))
	descStyle := lipgloss.NewStyle().Foreground(styles.ColorText)
	selStyle := lipgloss.NewStyle().Background(lipgloss.Color("#1E3A5F")).Bold(true)

	labelWidth := 0
	for _, a := range s.actions {
		if l := len(a.label); l > labelWidth {
			labelWidth = l
		}
	}

	vis := s.visibleRows()
	end := s.scrollOff + vis
	if end > len(s.rows) {
		end = len(s.rows)
	}

	var lines []string
	firstCmd, lastCmd := -1, -1
	for _, r := range s.rows[s.scrollOff:end] {
		if r.isHeader {
			lines = append(lines, groupStyle.Render(" "+r.label))
			continue
		}
		if firstCmd < 0 {
			firstCmd = r.idx
		}
		lastCmd = r.idx

		paddedLabel := fmt.Sprintf("%-*s", labelWidth, r.action.label)
		desc := r.action.desc
		avail := s.width - labelWidth - 6
		if avail > 0 && len(desc) > avail {
			desc = desc[:avail-1] + "…"
		} else if avail <= 0 {
			desc = ""
		}

		if r.idx == s.selected {
			line := "  " + paddedLabel + " " + desc
			lines = append(lines, selStyle.Width(s.width-2).Render(line))
		} else {
			lines = append(lines, "  "+cmdStyle.Render(paddedLabel)+" "+descStyle.Render(desc))
		}
	}
	if len(s.rows) > vis && lastCmd >= 0 {
		lines = append(lines, lipgloss.NewStyle().
			Foreground(styles.ColorMuted).Padding(0, 2).
			Render(fmt.Sprintf("-- %d–%d of %d --", firstCmd+1, lastCmd+1, len(s.actions))))
	}

	body := strings.Join(lines, "\n")
	help := styles.StatusBar.Width(s.width).Render(
		styles.StatusBarKey.Render("↑↓")+" navigate  "+
			styles.StatusBarKey.Render("enter")+" run  "+
			styles.StatusBarKey.Render("esc")+" back",
	)

	content := lipgloss.JoinVertical(lipgloss.Left, title, "", body)
	return styles.PinToBottom(s.height, content, help)
}
