# laradok

A terminal UI for managing Laravel applications running inside Docker containers — locally or on remote servers over SSH.

![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat&logo=go)
![Platform](https://img.shields.io/badge/platform-macOS%20%2F%20Linux-lightgrey)

---

## Features

- **Multi-server support** — connect to any number of SSH servers or use your local Docker socket
- **Container browser** — lists all Docker containers (running and stopped) with favorites, custom display names, filtering, and per-container status, published ports, and live CPU/memory usage; press `e` to edit a container's config (display name, root path, favorite, hidden) and write it back to `config.yaml`
- **Artisan commands** — full autocomplete list of all `php artisan` commands with descriptions
- **Composer commands** — browse and run composer scripts; auto-downloads `composer.phar` if not installed
- **npm scripts** — browse and run scripts from `package.json`
- **Custom commands** — define reusable command groups per container or globally in config
- **Interactive shell** — run any command with live stdin/stdout streaming
- **Log viewer** — tail Laravel logs, Docker stdout/stderr, and host service logs (nginx, php-fpm, supervisor, etc.) with lazy chunk loading and line-wrap toggle; opens and scrolls multi-gigabyte logs instantly (see [Log viewer](#log-viewer))
- **Docker commands** — inspect, restart, stats, top, diff, network info, and more
- **Global Docker cleanup** — server-level disk usage and prune commands (images, volumes, networks, build cache, system) with a confirmation step for destructive actions
- **Database management** — connect to any PostgreSQL, MySQL, MariaDB, Percona, or SQLite container, browse databases, run SQL queries, explore schema, maintenance queries, and download compressed dumps
- **Redis inspection** — browse Redis `INFO`, key samples, config, and slowlog via curated `redis-cli` commands (auto-detects `REDIS_PASSWORD`)
- **MongoDB inspection** — browse server/DB stats, collections, and indexes via curated `mongosh`/`mongo` commands (auto-detects root credentials)
- **SQL query history** — per-database persistent history with `↑↓` navigation (stored in `~/.config/laradok/sql_history.json`)

---

## Installation

### Homebrew (macOS / Linux)

```bash
brew install alexbabintsev/tap/laradok
```

Upgrade later with `brew upgrade laradok`.

### From source

```bash
git clone https://github.com/alexbabintsev/laradok
cd laradok
go build -o laradok .
mv laradok /usr/local/bin/
```

### Requirements

- Docker installed and accessible on target hosts
- SSH key-based auth for remote servers (or `ssh-agent`)
- Go 1.26+ (only for building from source; the module pins the patched `go1.26.6` toolchain, which `go` downloads automatically)

---

## Configuration

Default config path: `~/.config/laradok/config.yaml`

Override with:
```bash
laradok /path/to/config.yaml
# or
LARADOK_CONFIG=/path/to/config.yaml laradok
```

### Minimal config (local Docker)

```yaml
# No config needed — laradok auto-adds a local server if none are defined.
```

### Full config example

```yaml
commands:
  - name: "Cache"
    commands:
      - label: "cache:clear"
        cmd: "php artisan cache:clear"
        desc: "Flush the application cache"

  - name: "Database"
    commands:
      - label: "migrate"
        cmd: "php artisan migrate --force"
        desc: "Run database migrations"
      - label: "migrate:rollback"
        cmd: "php artisan migrate:rollback"
        desc: "Rollback the last migration"

servers:
  - name: "Production"
    host: "your-server.com"
    port: 22
    user: "root"
    key: "~/.ssh/id_ed25519"
    type: ssh
    containers:
      - name: "myapp-*"               # glob pattern supported
        display_name: "My App"
        favorite: true
        root_path: "/var/www/html"    # default, can be omitted
        custom_logs:
          - /var/log/nginx/access.log
          - /var/log/nginx/error.log
        commands:
          - name: "Horizon"
            commands:
              - label: "horizon:status"
                cmd: "php artisan horizon:status"
                desc: "Get the current status of Horizon"

  - name: "Local Dev"
    type: local
```

### Container config options

`display_name`, `root_path`, `favorite` and `hidden` can also be edited from the UI — press `e` on a container in the list. Edits are written back here as an exact-name entry.

| Field | Type | Description |
|---|---|---|
| `name` | string | Docker container name or glob pattern (e.g. `app-*`) |
| `display_name` | string | Custom label shown in the container list |
| `favorite` | bool | Pin to top of list with a star indicator |
| `hidden` | bool | Hide from container list entirely |
| `root_path` | string | Path to Laravel root inside container (auto-detected from `/var/www/html` or `/app`; set to override) |
| `custom_logs` | []string | Extra log file paths shown in Server Logs |
| `commands` | []CommandGroup | Per-container command groups (appear before global commands) |

### SSH authentication

laradok tries auth methods in this order:

1. Explicit `key:` path from config (with optional `passphrase:`)
2. `ssh-agent` (via `SSH_AUTH_SOCK`)
3. Standard key files: `~/.ssh/id_ed25519`, `~/.ssh/id_rsa`, `~/.ssh/id_ecdsa`, etc.

### Host key verification

Server host keys are checked against `~/.ssh/known_hosts` (the same file OpenSSH uses), with `accept-new` semantics:

- a server you have never connected to is trusted on first use and its key is recorded;
- a server whose key **differs** from the recorded one is refused with a "host key mismatch" error — that is what a man-in-the-middle attack looks like. If the server was legitimately reinstalled, remove the old entry with `ssh-keygen -R <host>` (or `ssh-keygen -R '[host]:port'`).

### Connection drops

A dropped connection (network change, laptop sleep, server restart) is detected — via SSH keepalives every 30 s while idle, and a 10 s limit on opening a session — and the next action transparently reconnects. There is no need to go back to the server list.

---

## Usage

```bash
laradok                         # use default config
laradok ~/.config/laradok/config.yaml
```

### Navigation

| Key | Action |
|---|---|
| `↑` / `↓` / `j` / `k` | Navigate list |
| `Enter` | Select / run |
| `Esc` | Go back |
| `PgUp` / `PgDn` | Scroll by page |
| `F2` | Toggle line wrapping (output screens) |
| `Ctrl+C` | Quit |

---

## Main Menu

After selecting a container, the main menu offers:

| Option | Description | Shown when |
|---|---|---|
| **Info** | Container details: image, status, network/IP, mounts, and labels (from `docker inspect`) | always |
| **Stats** | Live CPU / memory / network / disk graphs plus a top-processes table (`c`/`m` to sort) | always |
| **Terminal** | Open an interactive shell (`bash`, falling back to `sh`) inside the container | always |
| **Commands** | Browse configured command groups | container has custom commands in config |
| **Artisan Commands** | Full `php artisan` list with autocomplete | `artisan` found (in root or `/app`) |
| **Composer Commands** | Browse and run composer scripts | `composer` or `php` found |
| **Npm Commands** | Browse and run npm scripts | `npm` found |
| **Docker Commands** | Container management (inspect, restart, stats…) | always |
| **Custom Command** | Interactive shell with live stdin | always |
| **Laravel Logs** | Browse and tail `storage/logs/*.log` files | `artisan` found (in root or `/app`) |
| **Docker Logs** | Stream container stdout/stderr | always |
| **Server Logs** | Tail nginx, php-fpm, supervisor logs | always |
| **Database** | PostgreSQL / MySQL / MariaDB / Percona / SQLite management (see below) | `psql`, `mysql`, or `sqlite3` found |
| **Redis** | Inspect Redis via curated `redis-cli` commands (see below) | `redis-cli` found |
| **MongoDB** | Inspect MongoDB via curated `mongosh`/`mongo` commands (see below) | `mongosh` or `mongo` found |
| **Download Storage** | Archive and download `storage/` to `~/Downloads/` | `artisan` found (in root or `/app`) |
| **File Browser** | Walk the container filesystem, view sizes, download any file or folder as `.tar.gz` | always |

Menu items are detected automatically with a single `docker exec` probe when the container is opened. A spinner is shown during detection.

**Terminal** suspends the TUI and attaches your real terminal to an interactive shell in the container, resuming laradok when you exit the shell (`exit` or `Ctrl+D`). For local servers it runs `docker exec -it`; for SSH servers it shells out to your system `ssh -t` using the server's host/port/key, so the same key/agent that works for `ssh` must be available.

---

## Log viewer

Log files are read by **byte offset**, never by line number, so the cost of opening or scrolling a log does not depend on its size:

- **Opening** reads the file size (`stat`), then the last 256 KB with a block-aligned `dd skip=… count=…` (a seek, not a scan), cut at line boundaries, and follows new lines with `tail -c +<offset> -f` from exactly where that chunk ended — a line still being written arrives whole, nothing is lost or repeated.
- **Scrolling up** loads the 256 KB before the earliest loaded byte the same way (growing the chunk for lines longer than that, up to 8 MB). Opening a 1 GB log and paging through it takes the same time as for a 1 MB one.
- The status bar shows the position as `bytes / size  percent` (it grows as new lines arrive). Docker stdout/stderr has no file, so it shows the buffered line count instead.

In the **Laravel Logs** and **Server Logs** pickers the list appears immediately with each file's size and dates (metadata only); line counts are computed in the background (`wc -l`, one file at a time) and filled in as they finish. Counting stops when you leave the picker.

---

## Global Docker Cleanup

From the container list, press **`g`** to open server-level Docker commands that are not tied to a single container. These run on the host (or remote server over SSH):

- **Disk usage** — `docker system df` and the verbose per-image/volume breakdown
- **Cleanup** — prune stopped containers, unused images (dangling or all), volumes, networks, build cache, or everything (`docker system prune`)

Every destructive command shows a **confirmation screen** with the exact command before running — press `y` to proceed or `n` to cancel. Cleanup commands are highlighted in red in the list.

---

## Database Management

Select a container running PostgreSQL, MySQL, MariaDB, Percona Server, or SQLite from the container list, then choose **Database** from the main menu. The engine is detected automatically:

- `psql` → **PostgreSQL** (wins if multiple clients are present)
- `mysql` (or `mariadb`, the only client in MariaDB 11+ images) → **MySQL**, or **MariaDB** / **Percona** if the client version string identifies that distribution
- `sqlite3` → **SQLite** (used only when no server client is present)

MariaDB and Percona reuse the MySQL client, `information_schema`, and `mysqldump` (`mariadb` / `mariadb-dump` where the `mysql*` names are missing), so they share the same actions as MySQL — only the engine label differs.

Passwords are never put on a command line (where any user on the host could read them with `ps`): laradok writes them to the command's stdin and the container-side shell exports them as `PGPASSWORD` / `MYSQL_PWD` / `REDISCLI_AUTH` just for that command.

For **PostgreSQL**, laradok auto-detects credentials from `POSTGRES_USER` / `POSTGRES_PASSWORD` (falling back to `postgres`). For the **MySQL family** (MySQL / MariaDB / Percona), it prefers `root` with `MYSQL_ROOT_PASSWORD` (or `MARIADB_ROOT_PASSWORD`), otherwise `MYSQL_USER` / `MYSQL_PASSWORD` (or the `MARIADB_*` equivalents). **SQLite** needs no credentials — laradok scans the app root (e.g. `database/`, `storage/`) for `*.sqlite`, `*.sqlite3`, and `*.db` files and lists each file as a database.

System databases are hidden from the list (PostgreSQL: `postgres`, `template0`, `template1`; MySQL family: `information_schema`, `performance_schema`, `mysql`, `sys`).

### PostgreSQL actions

#### Info & Stats
- **DB size** — total size on disk
- **DB version / uptime** — PostgreSQL version and start time
- **Table sizes (top 20)** — largest tables by total size including indexes
- **Table row counts** — estimated rows per table from `pg_stat_user_tables`
- **Cache hit ratio** — buffer cache hit percentage (healthy: >99%)
- **Active connections** — current sessions with duration and query preview
- **Long running queries** — queries active for more than 5 seconds

#### Schema
- **List tables** — all user tables with schema
- **List schemas** — non-system schemas with owner
- **List views** — all user-defined views
- **List sequences** — all sequences with last value
- **List functions** — user-defined functions with argument signatures

#### Indexes
- **Unused indexes** — indexes with zero scans (candidates for removal)
- **Duplicate indexes** — indexes covering identical column sets
- **Index usage** — scan counts and tuple stats per index

#### Maintenance
- **Table bloat** — dead tuple counts, live/dead ratio, last autovacuum time
- **Active locks** — current lock activity with query preview
- **Replication status** — streaming replication lag per replica
- **VACUUM ANALYZE** — run full vacuum analyze on all tables

#### Query
- **Run SQL query** — open SQL input with persistent per-database history

#### Backup
- **Download SQL dump** — `pg_dump --no-owner --no-acl | gzip`, saves to `~/Downloads/<db>_<timestamp>.sql.gz`
- **Download SQL dump (inserts)** — same but with `--inserts --column-inserts` (slower, more portable INSERT-based dump)
- **Download custom dump** — `pg_dump --no-owner --no-acl -Fc`, saves to `~/Downloads/<db>_<timestamp>.dump` (binary format, use with `pg_restore` for selective table restore)

### MySQL / MariaDB / Percona actions

The MySQL family exposes an equivalent set of actions built on `information_schema` / `performance_schema`:

- **Info & Stats** — DB size, version/uptime, table sizes (top 20), table row counts, active connections, long running queries
- **Schema** — list tables, views, columns, foreign keys, routines (procedures/functions)
- **Indexes** — list indexes per table, find tables without a primary key
- **Maintenance** — table status (engine, free space, auto_increment), InnoDB lock waits, generate `ANALYZE TABLE` statements
- **Query** — **Run SQL query** with persistent per-database history
- **Backup** — **Download SQL dump** via `mysqldump --single-transaction --no-tablespaces | gzip`, saves to `~/Downloads/<db>_<timestamp>.sql.gz`

### SQLite actions

SQLite is file-based, so its actions run against the selected `.sqlite` file via the `sqlite3` client (`sqlite_master` and `PRAGMA` instead of `information_schema`):

- **Info & Stats** — SQLite version, DB size (page count × page size), per-table row counts, encoding & journal/WAL mode
- **Schema** — list tables, views, triggers, and stored `CREATE TABLE` definitions
- **Indexes** — list indexes per table and their `CREATE INDEX` definitions
- **Maintenance** — `PRAGMA integrity_check`, `PRAGMA foreign_key_check`, freelist page count, and `VACUUM`
- **Query** — **Run SQL query** with persistent per-file history
- **Backup** — **Download SQL dump** via `sqlite3 <file> .dump | gzip`, saves to `~/Downloads/<file>_<timestamp>.sql.gz`

### SQL query history

- Stored per-database in `~/.config/laradok/sql_history.json`
- Key format: `serverName/containerName/dbName`
- Up to 200 queries per database
- `↑` / `↓` to navigate history in the SQL input screen
- Duplicate queries are deduplicated (most recent position kept)

---

## Redis

Select a container with the `redis-cli` client, then choose **Redis** from the main menu. laradok auto-detects the password from `REDIS_PASSWORD` (or the credentials in `REDIS_URL`) and runs every command as `redis-cli …` inside the container, passing the password through the `REDISCLI_AUTH` environment variable (not `-a`, which would expose it in process listings).

The screen offers curated, read-only commands grouped by purpose:

- **Info & Stats** — `INFO server` / `memory` / `stats` / `clients` / `keyspace` / `replication`, `DBSIZE`, `CLIENT LIST`
- **Keys** — sample the first 20 keys and count keys via non-blocking `SCAN`, key distribution, `SLOWLOG GET 10`
- **Configuration** — `CONFIG GET` for `maxmemory`, `maxmemory-policy`, `save`, `appendonly`
- **Maintenance** — `PING`, `LASTSAVE`, `LATENCY DOCTOR`
- **Backup** — **Download RDB snapshot** via `redis-cli --rdb`, saved to `~/Downloads/redis_<timestamp>.rdb`

Key listing uses `redis-cli --scan` (non-blocking) rather than `KEYS *`, so it is safe to run against production instances.

---

## MongoDB

Select a container with the `mongosh` (preferred) or legacy `mongo` shell, then choose **MongoDB** from the main menu. laradok auto-detects root credentials from `MONGO_INITDB_ROOT_USERNAME` / `MONGO_INITDB_ROOT_PASSWORD` (or the credentials in `MONGO_URL` / `MONGODB_URI`) and runs each command as `mongosh --quiet --eval '<js>'` inside the container. With `mongosh` the credentials are read from the environment by the script itself (`db.getSiblingDB('admin').auth(…)`), so they never appear on a command line; the legacy `mongo` shell cannot do that and falls back to `-u`/`-p`. `mongodump` gets its password from a private `--config` file that is removed afterwards.

Commands are curated JavaScript expressions grouped by purpose:

- **Info & Stats** — `db.version()`, server status, `listDatabases`, current DB `db.stats()`, in-progress operation count
- **Collections** — list collections, document counts, and data sizes per collection
- **Indexes** — list indexes for every collection
- **Maintenance** — `ping`, `replSetGetStatus`, profiling status
- **Backup** — **Download dump** via `mongodump --archive --gzip`, saved to `~/Downloads/mongo_<timestamp>.archive.gz` (restore with `mongorestore --archive=… --gzip`)

When no `MONGO_INITDB_ROOT_*` variables are present, commands run without authentication (suitable for local, unsecured instances).

---

## Architecture

```
laradok/
├── main.go
├── internal/
│   ├── config/          # YAML config loading, SSH key expansion, SQL history persistence
│   ├── connection/      # SSHClient and LocalClient implementing the Runner interface
│   ├── docker/          # Docker/psql command builders, log tailing, DB introspection
│   ├── msgs/            # Bubble Tea message types for screen navigation and streaming
│   └── tui/
│       ├── model.go     # Root App model — screen stack, async orchestration
│       ├── keys.go      # Key bindings
│       └── screens/     # Individual TUI screens
```

The `Runner` interface (`RunCommand`, `RunOutput`, `StreamCommand`, `InteractiveCommand`, `StartCommand`) is implemented by both `SSHClient` and `LocalClient` on top of one small transport primitive, making all features work identically on local and remote Docker hosts.

Every command that runs inside a container is built by `docker.ExecShCmd` / `ExecStreamScript`: the script is passed to `sh -c` as one single-quoted word, so the host shell never expands anything in it, and every embedded value (paths, DB names) is quoted individually. Streams that can be cancelled run as a background job inside the container that is terminated when laradok closes its stdin, so leaving a screen really stops `tail -f`, a long query or a dump on the server.

---

## Development

```bash
go build -o laradok .
go test -race ./...                                  # unit tests (hermetic)
LARADOK_INTEGRATION=1 go test -race ./internal/docker/  # + real Docker containers
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

- **Unit tests** need no Docker or network: the SSH layer is tested against an in-process SSH server (reconnects, dropped connections, host-key checks, complete stdout/stderr), and command builders run against a fake `docker` CLI that executes scripts with the real `sh`.
- **Integration tests** start throwaway containers (labelled `laradok-test`) for busybox, dash and bash shells, PostgreSQL, MariaDB, Redis and MongoDB, using hostile file names and passwords to verify quoting, cancellation and that secrets never appear in process listings.
- CI (`.github/workflows/test.yml`) runs gofmt, vet, the race-enabled unit tests on Linux and macOS, the integration tests and `govulncheck`; releases run the tests and `govulncheck` before publishing.

---

## Download Storage

Available from the main menu on any Laravel container. Shows the size of `storage/` before transferring, then:

1. Runs `tar -czf - storage/` inside the container (no temp files on server)
2. Streams the raw archive bytes straight to disk (constant memory, no base64 overhead)
3. Saves it as `~/Downloads/<container>_storage_<timestamp>.tar.gz` (mode `0600`) — only once the transfer has completed successfully; a failed or cancelled transfer (press `esc`) leaves nothing behind

Progress is shown as a single `received N MB` counter. Files that change while being archived (e.g. a live `laravel.log`) are archived anyway and reported as warnings.

---

## File Browser

Available from the main menu on **any** container, rooted at the filesystem root `/`.

- Lists directories first, then files, each with its size — directories sized recursively with `du -sb` (falls back to `du -sk` on BusyBox).
- `↑↓` to move, `enter`/`→` to open a directory **or view a file in the log viewer** (scroll, tail, lazy-load earlier lines), `←`/`backspace` to go up, `esc` to leave the browser at the root.
- Press `d` on any file or directory to archive and download it the same way as **Download Storage**: `tar -czf -` streamed to `~/Downloads/<container>_<name>_<timestamp>.tar.gz`. No temp files are created on the server, so nothing is left behind after the transfer.

---

## Data & Privacy

- No telemetry or network calls except to your configured servers
- SSH credentials stay local; only Docker and psql commands are executed on remote hosts
- SQL history is stored unencrypted (mode `0600`) at `~/.config/laradok/sql_history.json`
- Dump files are written to `~/Downloads/` with mode `0600` and never transmitted elsewhere
- `config.yaml` is rewritten with mode `0600` when edited from the UI, since it may contain key passphrases
- Database passwords are passed to the container over stdin, never as command-line arguments
