# Changelog

All notable changes to laradok are documented here.

---

## [Unreleased]

### Added
- **File Browser** — walk the container filesystem from the main menu (any container, rooted at `/`)
  - Lists directories first then files, each with its size (`du -sb` for dirs, byte size for files)
  - Navigate with `↑↓`, `enter`/`→` to open a directory or view a file in the log viewer, `←`/`backspace` to go up, `esc` to leave
  - Press `d` to archive and download any file or directory to `~/Downloads/<container>_<name>_<timestamp>.tar.gz` via streamed `tar | base64` (no temp files left on the server)

### Changed
- **Download progress** — archive downloads (File Browser + Download Storage) now show a single `received N MB` counter that updates in place instead of flooding the output with one line per chunk
- **PostgreSQL management** — full database browser accessible from the main menu
  - Auto-detects `POSTGRES_USER` / `POSTGRES_PASSWORD` from container environment
  - Lists non-system databases (excludes `postgres`, `template0`, `template1`)
  - Per-database actions menu with 20+ built-in queries across 4 categories
- **Info & Stats queries** — DB size, version/uptime, table sizes (top 20), row counts, cache hit ratio, active connections, long-running queries (>5 s)
- **Schema queries** — list tables, schemas, views, sequences, and user-defined functions
- **Index queries** — unused indexes, duplicate indexes, index usage statistics
- **Maintenance queries** — table bloat / dead tuples, active locks, replication status, VACUUM ANALYZE
- **SQL query input screen** — freeform SQL with `↑↓` history navigation
- **Persistent SQL history** — stored per-database in `~/.config/laradok/sql_history.json`, key `serverName/containerName/dbName`, up to 200 entries, deduplicated
- **Database dump download** — two formats available:
  - **SQL dump** — `pg_dump --no-owner --no-acl | gzip` → `~/Downloads/<db>_<timestamp>.sql.gz`; `\restrict` and `\unrestrict` lines stripped after decompression
  - **SQL dump (inserts)** — same but with `--inserts --column-inserts` (slower, more portable INSERT-based format)
  - **Custom dump** — `pg_dump --no-owner --no-acl -Fc` → `~/Downloads/<db>_<timestamp>.dump` (binary format for `pg_restore` with selective restore)
- `docker.ShellQuote` exported for safe shell argument construction
- `config.LoadSQLHistory` / `config.SaveSQLHistory` for persistent per-key history
- **Automatic menu filtering** — main menu probes the container on open (one `docker exec` round-trip) and shows only relevant items: Artisan/Composer/Npm/Laravel Logs/Download Storage hidden for non-Laravel containers; Database hidden when `psql` is absent; spinner shown during detection; menu items not shown at all until detection completes (no flash of irrelevant items)
- **Download Storage** — new main menu item archives `storage/` inside the container via `tar | gzip | base64 -w 76`, shows directory size before transfer, saves to `~/Downloads/<container>_storage_<timestamp>.tar.gz`; no temporary files created on the server

### Fixed
- Shell quoting bug in all psql/pg_dump commands: replaced `sh -c "PGPASSWORD='...' psql ..."` (broken double-quote nesting) with `docker exec -e PGPASSWORD=...` to inject credentials as environment variables — no shell interpolation of user-controlled values
- `ListDatabases` parsing: `psql -lqt` ACL entries (e.g. `postgres=CTc/postgres`) were incorrectly parsed as database names; now requires `len(parts) >= 2` and rejects names containing `=` or `/`
- `base64 -w 0` replaced with `base64 -w 76` in storage and pg_dump pipelines — `bufio.Scanner` has a 64 KB token limit and silently dropped single-line base64 output from large archives, causing "no data received" errors

---

## [0.5.0] — 2026-04-29

### Added
- Project renamed from **doklar** to **laradok** across all configuration, logging, and UI components

---

## [0.4.0] — 2026-04-28

### Added
- **Lazy log loading** — log files load in chunks (`LogChunkSize = 1000` lines); scrolling to the top triggers async loading of earlier chunks
- `LogTailInitMsg` / `LogChunkLoadedMsg` message types for chunk-based log state management
- `CountFileLines` and `LoadLogChunk` functions in the docker package

### Changed
- `LogTailScreen` state management refactored for chunk-based position tracking (`topLine`, `totalLines`)
- Chunk loading stops retrying when a full chunk is obtained or the top of the file is reached

---

## [0.3.0] — 2026-04-27

### Added
- **Line wrapping toggle** (`F2`) in `LogTailScreen` and `OutputScreen`
- `wrapLines` helper for soft-wrapping long output lines at word boundaries

### Changed
- `StreamCommand` now returns a `stop` function for graceful termination in both `LocalClient` and `SSHClient`
- SSH stop closes stdin pipe first, triggering `read; kill $PID` inside the container before closing the session

---

## [0.2.0] — 2026-04-26

### Added
- **Docker Commands screen** — hardcoded groups (Container, Filesystem, Network, Image) accessible from the main menu
- `PushDockerCmdMsg` navigation message
- `HostCmd` field on `PushOutputMsg` for commands that run directly on the host (not inside the container via `docker exec`)

### Changed
- `startCommand` in `model.go` now routes `HostCmd` directly to `runner.StreamCommand`, bypassing `docker exec`
- Screen rendering uses `PinToBottom` layout helper for consistent status bar placement

---

## [0.1.0] — 2026-04-25

### Added
- Initial release
- Multi-server config with SSH and local Docker support
- Container list with favorites, custom display names, glob-pattern matching, hidden flag
- **Artisan Commands** screen — loads full `php artisan list` with descriptions, fuzzy-navigable
- **Composer Commands** screen — resolves `composer` binary or downloads `composer.phar` automatically
- **npm Commands** screen — parses `npm run` output for script names and bodies
- **Custom Command** screen — interactive shell with live stdin/stdout via `InteractiveCommand`
- **Laravel Logs** browser — lists `storage/logs/*.log` with size, line count, and timestamps
- **Docker Logs** — streams `docker logs -f --tail 100` for any container
- **Server Logs** — discovers `/var/log/**/*.log` files inside the container with service grouping
- `RawCmdScreen` for per-container `custom_logs` and freeform shell commands
- Config-driven global and per-container command groups
- SSH key auth with ssh-agent fallback and standard key auto-discovery
- Session ID mechanism to discard output from stale (navigated-away) sessions
- `OutputScreen` with scrollable viewport, spinner during execution, checkmark on completion
