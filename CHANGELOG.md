# Changelog

All notable changes to laradok are documented here.

---

## [Unreleased]

### Added
- **Edit container config from the UI** — press `e` on a container in the list to open a form for its display name, root path, favorite and hidden flags
  - Saves back to `config.yaml` (`Config.Save`), creating an exact-name entry that takes precedence over any glob rule
  - The list refreshes immediately; `~` in SSH key paths is preserved on write
- **Redis & MongoDB backups** — the Redis and MongoDB inspection screens gained a **Backup** action
  - Redis: **Download RDB snapshot** via `redis-cli --rdb` → `~/Downloads/redis_<timestamp>.rdb`
  - MongoDB: **Download dump** via `mongodump --archive --gzip` → `~/Downloads/mongo_<timestamp>.archive.gz`
  - (SQL engines already supported dumps; this brings the NoSQL engines to parity)
- **Terminal** — new container-menu entry that opens an interactive shell inside the container
  - Suspends the TUI and attaches the real terminal (full PTY) via `tea.ExecProcess`, resuming on shell exit
  - Local servers run `docker exec -it`; SSH servers shell out to `ssh -t` with the server's host/port/key
  - Clears the screen on entry so no TUI remnants are left behind
  - Prefers `bash`, falling back to `sh`
- **Live Stats screen** — new **Stats** entry in the container menu with real-time charts that refresh every 2s
  - Sparkline graphs for CPU %, memory, network rate, and disk I/O rate (network/disk derived as per-second deltas from cumulative `docker stats` totals)
  - Top-processes table underneath (from `ps` inside the container) with `c` / `m` to sort by CPU or memory
  - Polling stops automatically when you leave the screen
- **Container Info screen** — new **Info** entry in the container menu showing parsed `docker inspect` data: name, ID, image, status, networks/IP, mounts (source → destination), and labels — in a scrollable, sectioned view
- **Global Docker cleanup** — press `g` in the container list to open server-level Docker commands not tied to a container
  - Disk usage (`docker system df`, verbose) and prune commands for stopped containers, images, volumes, networks, build cache, and full `system prune`
  - Destructive commands require a `y`/`n` confirmation screen showing the exact command; cleanup entries are highlighted in red
- **Stopped containers in the list** — the container browser now uses `docker ps -a`, so stopped/exited containers are shown too
  - Non-running containers are dimmed and tagged with their state (e.g. `myapp (exited)`)
  - Sorting puts running containers first, then favorites, then alphabetical — stopped containers group at the bottom
- **Container list stats** — each container now shows its status (uptime/health), published host ports, and live CPU / memory usage
  - Status and ports come free from the existing `docker ps` call
  - CPU/memory are fetched lazily with a single background `docker stats --no-stream` after the list is shown, so the list never blocks on the slower stats call
  - Refreshing the list (`r`) re-fetches both
- **MySQL support** — the **Database** menu now works with MySQL containers in addition to PostgreSQL
  - Engine auto-detected via the container probe (`mysql` client → MySQL; `psql` still wins if both are present)
  - Credentials read from `MYSQL_ROOT_PASSWORD` (preferred, as `root`) or `MYSQL_USER` / `MYSQL_PASSWORD`
  - Lists non-system databases (excludes `information_schema`, `performance_schema`, `mysql`, `sys`)
  - Per-database action menu mirroring the PostgreSQL one: Info & Stats, Schema, Indexes, Maintenance, Run SQL query, and SQL dump download
  - **Download SQL dump** via `mysqldump --single-transaction --no-tablespaces | gzip` → `~/Downloads/<db>_<timestamp>.sql.gz`
- **MariaDB & Percona support** — both are detected from the `mysql --version` string and reuse the full MySQL pipeline (client, `information_schema`, `mysqldump`); only the engine label differs
- **SQLite support** — file-based databases via the `sqlite3` client (used when no server DB client is present)
  - Scans the app root (`database/`, `storage/`, root, `/data`) for `*.sqlite` / `*.sqlite3` / `*.db` files and lists each as a database
  - No credentials needed; actions use `sqlite_master` and `PRAGMA` (version, size, row counts, schema, indexes, integrity/foreign-key checks, `VACUUM`)
  - **Download SQL dump** via `sqlite3 <file> .dump | gzip` → `~/Downloads/<file>_<timestamp>.sql.gz`
- **Redis inspection** — new **Redis** menu item (shown when `redis-cli` is present)
  - Auto-detects the password from `REDIS_PASSWORD` / `REDIS_URL` and runs commands as `docker exec … redis-cli [-a …]`
  - Curated read-only commands: `INFO` sections, `DBSIZE`, `CLIENT LIST`, key sampling/counting via non-blocking `SCAN`, `SLOWLOG`, `CONFIG GET`, `PING`, `LASTSAVE`, `LATENCY DOCTOR`
- **MongoDB inspection** — new **MongoDB** menu item (shown when `mongosh` or legacy `mongo` is present)
  - Auto-detects root credentials from `MONGO_INITDB_ROOT_USERNAME` / `MONGO_INITDB_ROOT_PASSWORD` / `MONGO_URL` / `MONGODB_URI` and runs `docker exec … <shell> --eval '<js>'`
  - Curated commands: server/DB stats, `listDatabases`, collection names/counts/sizes, indexes, `ping`, `replSetGetStatus`, profiling status
- **File Browser** — walk the container filesystem from the main menu (any container, rooted at `/`)
  - Lists directories first then files, each with its size (`du -sb` for dirs, byte size for files)
  - Navigate with `↑↓`, `enter`/`→` to open a directory or view a file in the log viewer, `←`/`backspace` to go up, `esc` to leave
  - Press `d` to archive and download any file or directory to `~/Downloads/<container>_<name>_<timestamp>.tar.gz` via streamed `tar | base64` (no temp files left on the server)

### Changed
- Saving the config no longer writes the implicit "Local" server (added when the config defines none) unless it gained container settings; a symlinked `config.yaml` (dotfiles) keeps its link
- `~user/…` key paths are no longer mangled (only `~` / `~/…` are expanded)
- Removed the unused `internal/dbg` package
- **Commands run in the app root** — custom commands (config `commands:`) and the Artisan/Composer/Npm pickers now `cd` into the container's app root (configured or detected) before running, falling back to the image `WORKDIR` when it does not exist
- **Log file list in one round-trip** — size, timestamps and line counts are gathered by a single in-container script instead of two
- **Faster container list** — `docker ps` is no longer always run twice; it is retried only when the output looks truncated. Fields are emitted with `{{json …}}`, so names/statuses containing quotes no longer drop the container
- **Laravel detection** — the capability probe now also looks for `artisan` (and `composer.phar`) in `/app`, not just the configured root / `/var/www/html`. When `artisan` is found at `/app` and no `root_path` is set in config, laradok adopts that root so artisan, logs and storage commands target the right directory.
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
- **Incomplete / corrupted output from remote servers** (container list missing entries, partial database lists, stray NUL bytes) — the SSH runner wrote stdout and stderr into one unsynchronised `bytes.Buffer` from two goroutines; whenever the remote side printed anything on stderr (shell rc files, CLI warnings) the buffer was corrupted. Output is now collected safely, and parsed commands read stdout only, so stderr noise cannot leak into lists. The retry-on-empty / NUL-stripping workarounds are gone
- **Dropped SSH connections are re-established** — after a network change, sleep or server restart the next action reconnects transparently instead of failing until the app is restarted. Dead connections are detected via keepalives (every 30 s) and a 10 s limit on opening a session; connecting and the SSH handshake time out after 15 s
- **UI freeze after leaving running commands** — output streams were started synchronously inside the UI loop and their stop function was discarded, so leaving an artisan/SQL/dump screen early leaked an SSH session slot; after six such exits the whole UI hung. Every stream now starts in the background and is stopped when its screen closes, and waiting for a session slot times out after 30 s
- **Leaving a screen now stops the command on the server** — cancelled streams (tail, `docker logs`, artisan, SQL, dumps) run as a background job inside the container that is killed when laradok closes stdin; previously closing a remote `docker exec` left e.g. `queue:work` or `tail -f` running in the container
- **Log tail no longer drops lines** appended between counting the file and starting to follow it (`tail -n +<N+1> -f`)
- **Storage download failed on live Laravel apps** — tar's "file changed as we read it" went into the data stream and broke decoding; it is now reported as a warning and the archive is kept
- **Large downloads no longer load into memory** — dumps and archives were collected as base64 text, then decoded (≈4× the file size in RAM). They now stream raw bytes straight to a private temp file that is renamed into place only on success; failed or cancelled downloads leave nothing behind, and existing files are never overwritten
- **Dump failures are reported** — `pg_dump | gzip` reported gzip's (successful) exit status, so a failed dump could be saved as a valid-looking empty file; the producer's status is now propagated
- **MariaDB 11+ containers** (which ship only `mariadb` / `mariadb-dump`) are detected and fully supported; `MARIADB_*` credential variables are honoured
- PostgreSQL database list is read from `pg_database` (one name per line) instead of parsing `psql -l`, so names containing `|` or spaces are listed correctly
- Credentials embedded in `REDIS_URL` / `MONGO_URL` / `MONGODB_URI` are percent-decoded; multi-host Mongo URIs are supported
- The capability probe reports errors (e.g. stopped container) in the menu instead of silently showing an empty menu
- Background loaders no longer read the active container from a goroutine, so switching containers quickly can't send a command to the wrong one
- Switching servers closes the previous SSH connection (it previously leaked); a connection that finishes after you left the screen is closed
- Local commands run in their own process group, so stopping a stream kills the whole pipeline
- Shell quoting bug in all psql/pg_dump commands: replaced `sh -c "PGPASSWORD='...' psql ..."` (broken double-quote nesting) with `docker exec -e PGPASSWORD=...` to inject credentials as environment variables — no shell interpolation of user-controlled values
- `ListDatabases` parsing: `psql -lqt` ACL entries (e.g. `postgres=CTc/postgres`) were incorrectly parsed as database names; now requires `len(parts) >= 2` and rejects names containing `=` or `/`
- `base64 -w 0` replaced with `base64 -w 76` in storage and pg_dump pipelines — `bufio.Scanner` has a 64 KB token limit and silently dropped single-line base64 output from large archives, causing "no data received" errors

### Security
- **SSH host keys are verified** against `~/.ssh/known_hosts` with `accept-new` semantics — unknown hosts are recorded on first use, changed keys are rejected. Previously a missing `known_hosts` file silently disabled verification (`InsecureIgnoreHostKey`)
- **Passwords no longer appear in process listings** — PostgreSQL, MySQL and Redis passwords were passed as `docker exec -e PGPASSWORD=…` / `-a …` arguments, readable by any user on the host via `ps`. They are now written to the command's stdin and exported inside the container only; `mongosh` authenticates from the environment and `mongodump` uses a private `--config` file
- Downloaded dumps/archives are created with mode `0600`; `config.yaml` (which may hold key passphrases) and the SQL history are written atomically with mode `0600`
- Redis RDB snapshots use a private `mktemp` file instead of a fixed `/tmp` path
- **Host command injection via container-controlled names** — log file paths, files picked in the file browser, SQLite paths, DB/user names and `root_path` were interpolated into `docker exec … sh -c "…"` without quoting, so a file named e.g. `x$(cmd).log` inside a container ran `cmd` on the host (or the SSH server) when the log list was opened. Every in-container script is now built by `docker.ExecShCmd`, which single-quotes both the container ID and the script, and every embedded value is quoted individually
- **Raw / custom / config commands ran `$…` on the host** — they were wrapped with Go's `%q`, which does not escape `$` or backticks, so `echo $HOME` or `$(…)` expanded on the host. They now run verbatim in the container's shell
- Artisan arguments are passed to the container's shell instead of the host shell (pipes and `;` now apply inside the container)
- `composer.phar` auto-install verifies the installer's SHA-384 signature before running it
- Terminal over SSH passes `--` before the destination so a host value cannot be read as an `ssh` option
- Removed the unused `TailFile` runner method (it interpolated an unquoted path)


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
