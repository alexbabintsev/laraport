# Changelog

All notable changes to laraport are documented here.

---

## [Unreleased]

---

## [0.3.3] - 2026-11-09

### Added
- Artisan, Composer and npm remember the commands you run, per container: they are listed under *recent* above the commands (newest first, filtered as you type) and run again with ↓ and enter, or can be edited first. `ctrl+d` forgets one. They are saved with the SQL and Tinker history and follow its settings.

### Changed
- The history file is now `~/.config/laraport/history.json` (it keeps SQL, Tinker and commands); an existing `sql_history.json` is moved there on first use. The settings say Save history, History size and Clear history.

### Fixed
- Artisan, Composer and npm: the first character typed after picking a suggestion with ↓ did not count - with a single added character, enter still ran the picked command without it.

---

## [0.3.2] - 2026-11-08

### Added
- Artisan, Composer and npm commands: `ctrl+t` (or `alt+enter`) runs the command in the terminal, so commands that ask questions work (`make:filament-user` failed with "Required."). The output stays until enter, and the journal keeps the exit status.

---

## [0.3.1] - 2026-11-05

### Added
- The home screen says FREE or LICENSED next to the version, and l opens the License screen.

### Changed
- The recent containers on the home screen are a list below the servers: move down to one and press enter to open it.

### Fixed
- The License screen no longer shows the key field once a license is active; r opens it to replace the key.
- Releases get their notes from CHANGELOG.md: 0.3.0 was published without them.

---

## [0.3.0] - 2026-11-05

### Added
- License keys. Without one laraport works with local Docker and one SSH server, the first in the config; a key adds all the others. Other servers stay in the config, are marked LICENSE on the home screen and open the License screen. Keys are checked offline, so nothing is sent anywhere.
- `laraport license [key]`, and License in the settings and the palette.
- Issue templates for bugs and feature requests, and GitHub Discussions for questions and ideas.

### Changed
- laraport is commercial software from this version on, and its source is private: local Docker and one SSH server stay free.
- Releases are built from the private repository and published to the public one, with notes from this file.
- README: how to install on Linux without Homebrew.
- The hero clip shows the latest version; the site's screens and clips take it from one constant.
- README: Windows has no native build; laraport should run in WSL2, not tested there yet.

---

## [0.2.1] - 2026-11-05

### Added
- **Screens for laraport.app** - `TestSiteShots` renders the screens the site shows (containers with the server panel, the Laravel tab, logs, errors, Tinker, Horizon, a table, routes, the command palette, a confirmation, files and server resources) as true-colour ANSI from made-up data: `LARAPORT_SITE_SHOTS=dir go test -run TestSiteShots ./internal/tui/`; `TestSiteShots80` adds the screens of the site's feature sections at 80x24. The site turns them into HTML instead of recorded clips.
- **New clips in the README**, four of them for what had none: grouped errors, Redis keys, a deploy and routes. `TestSiteClips` plays them with real key presses on made-up data, and `npm run clips` in the site's repository turns the frames into `docs/media/*.gif`: sharper at 1072x632 and half the size.
- **`SECURITY.md`**: how to report a vulnerability privately, and what counts as one.

### Changed
- **New logo**: a red anchor inside a container, in the README and on laraport.app.
- README: the Stand With Ukraine banner and live badges for CI, the release, Go and the license.
- CI: actions are pinned by commit, and Dependabot keeps them and the Go modules up to date.
- Dependencies: `x/ansi` 0.11.9, `go-colorful` 1.4.1, `x/crypto` 0.58.0; GitHub Actions on their latest major versions.

### Fixed
- The *Migrations* label on the container's card no longer breaks over two lines.
- A hyphen instead of the em dash in every text of the interface.
- A long hint in a form (*Jump host* when adding a server) wraps under the field, not back at the window's left edge.
- The log's error counter says *1 error*, not *1 errors*.

---

## [0.2.0] - 2026-11-03

### Added
- **New version notice.** Once a day laraport asks GitHub for its latest release; when it is newer, the home screen says so with how to upgrade (`brew upgrade laraport` for a Homebrew install, the releases page otherwise).
  - Nothing about you is sent: one GET with `laraport/<version>` as the user agent, the answer kept for a day in `~/.config/laraport/update.json`. Development builds never ask, prereleases are not offered, and a failure stays silent.
  - *Check for new versions* in Settings (`no_update_check: true`) or `LARAPORT_NO_UPDATE_CHECK=1` turns it off.
- **Jump to a container from anywhere** (`ctrl+p`): the containers opened lately (newest first, with the server and when), the connected server's other containers and every server, filtered as you type.
  - On the same server the screens above its container list close and the container opens; on another server laraport goes home, connects and opens it once the list arrives. A Swarm task or Compose container is found by its service, so a redeploy's new name does not lose it; one that is gone says so.
  - The home screen shows the last four under the servers. Kept in `~/.config/laraport/recent.json` (20 at most, mode 0600); a server deleted or renamed takes its entries with it.
- **Compose actions** in the Docker tab of a container started by Docker Compose: Pull image (`docker compose pull <service>`; nothing restarts), Recreate (`up -d --no-deps <service>`: applies a new image or config, other services untouched) and Update project (`pull`, then `up -d`).
  - They run where the project was started from, read off the container's labels: its working directory, compose files and env files. When a file is not on the server (the project was started elsewhere, or moved) the command stops and says which. `docker compose` v2 is used, or `docker-compose` where only v1 is installed; a custom docker command (`sudo -n docker`) applies.
  - Each asks first with the compose command, the directory and what happens; the journal records the compose command.
- **Run a scheduled task now** (`t` in Laravel › Schedule): `php artisan schedule:test --name=…` for the task under the cursor, after a confirmation with the exact command. It runs once, the way the scheduler runs it; its schedule does not change. Commands, `exec` tasks, jobs and closures with a description are found by name; two tasks with the same name are refused, since `schedule:test` cannot tell them apart. Before Laravel 9 artisan refuses `--name`, so nothing runs by mistake.
  - The Schedule keeps the cursor on its row when read again.
- **Routes** (Laravel tab): the app's routes from `php artisan route:list --json` as a table: method (`GET` without the `HEAD` that comes with it), URI, name, controller without the `App\Http\Controllers\` prefix, middleware, and the domain when any route has one.
  - `/` filters as you type, every word anywhere in the route (`POST orders`, `auth:sanctum`, `OrderController`); `esc` clears it. `v` reads them again without the routes packages register (`--except-vendor`), `enter` shows one route in full.
  - Warnings printed before the JSON are skipped; an app that cannot boot shows its error.
- **Action journal** (`h` on the home screen for every server; Journal in a server's palette for that one, `a` for all). Every change laraport makes is recorded when it starts: the server, the container, the action and its command, SQL or path, and then how it ended: finished, failed with the error, stopped before the end, `1 row(s) changed`, saved with the backup's name.
  - Recorded: commands other than known reads, container actions, SQL other than a single SELECT, table edits, file saves and uploads, restores, Tinker, shells and tunnels. Reads (logs, tables, inspect, downloads) and actions a read-only server refused are not.
  - Kept on this computer only, in `~/.config/laraport/journal.jsonl` (mode 0600, one JSON line per action, cut back to its newer half past 4 MB). An action is written before it runs, so a crash midway still leaves it on record. Passwords never reach it: they travel over stdin, not in commands.
- **Read-only servers.** A server marked *Read-only* (in its settings, or `read_only: true`) refuses everything that would change it, in one place before a screen opens or a command runs, with a window saying what was refused and how to turn it off. The home screen and the status bar mark the server `READ-ONLY`.
  - Refused: commands other than known reads (artisan `about`, `route:list`, `migrate:status`, `queue:failed`…, `composer show` / `outdated` / `audit`, `npm ls` / `outdated` / `audit`; anything with shell syntax is not a known read), container and Swarm actions, deploy commands, prune, editing and uploading files, editing table data, SQL other than a single SELECT, Tinker, shells, the raw command prompt, deleting Redis keys, restoring dumps and tunnels.
  - Allowed: logs and the errors summary, tables and SELECT queries, files and `.env` (viewing), inspect, stats, events, Redis keys and the Redis / MongoDB info commands, every download.
- **Restore a dump into a database** (Backup in the PostgreSQL and MySQL/MariaDB actions). A local `.sql`, `.sql.gz` or (PostgreSQL) `pg_dump -Fc` `.dump` is streamed to the client in the container over stdin, with progress; nothing is copied to the server's disk and the password goes over stdin as before.
  - Off until *Allow restoring dumps* is switched on in Settings (`allow_restore`). Then the window shows the exact command, says what happens on an error and marks a production app, and runs only after the database's name is typed.
  - PostgreSQL restores in one transaction: laraport sends `BEGIN` before the dump and `COMMIT` after it (`psql -v ON_ERROR_STOP=1`; `pg_restore --single-transaction --exit-on-error --no-owner --no-acl` for `.dump`), so an error, a file cut short or a dropped connection leaves the database as it was. A `.sql.gz` is checked to be whole before anything is sent. MySQL runs statement by statement and stops at the first error. Either way the restore counts as done only when the client printed laraport's end marker, so a stream that stopped early is never reported as restored.
- **Container events** (`E` in the container list for the whole server; Docker tab › Events for one container, or every task of its Swarm service): died with the exit code explained (137: out of memory or SIGKILL), out of memory, killed with the signal, restarted, started, stopped, unhealthy, newest first, from `docker events`.
  - The title counts the problems; problems are marked `×`, `b` keeps only them, `t` switches between the last 24 hours, 1 hour and 7 days. When Docker's in-memory buffer (its last 256 events) is full, the screen says older ones are gone.
- **Redis key browser** (Data tab › Redis keys, for the container's Redis and the app's). A pattern (`/` edits it: `laravel_cache:*`, `*session*`), the matching keys with type, expiry and size, `enter` for a value laid out by type (a string, a hash's fields, a list's items, a sorted set's scores, a stream's entries; the first 200 items or 64 KB), `D` deletes a key with `UNLINK` after a confirmation, and the keys are scanned again.
  - One Lua script per read, so keys with spaces or quotes are safe, and the scan is bounded (100 SCAN steps, 500 keys) so it never blocks a busy server; the screen says when to narrow the pattern. The password goes over stdin as before.
- **Tunnel to the app's database or Redis** (Data tab) for desktop clients: TablePlus, DBeaver, RedisInsight. It listens on `127.0.0.1` only, on the server's port + 10000 (15432, 13306, 16379) or a free one, and shows where to connect with the database, user and a `postgresql://` / `mysql://` / `redis://` URL (`u` copies it with the password, `p` the password alone; the password is never on screen).
  - Each connection runs `docker exec -i` in the app's container with a small PHP relay: the container resolves the server's name and is on its network, so servers on a Swarm overlay (Dokploy) are reached too, and nothing is installed or listens on the server.
  - The tunnel stays open while its screen does; leaving it closes the tunnel and every connection. A connection the relay cannot make is reported on the screen.
- **Import servers from `~/.ssh/config`** (`i` on the home screen). Every concrete `Host` with its `HostName`, `User`, `Port`, the first `IdentityFile` that exists and `ProxyJump` (a jump through another Host becomes its address), resolved like ssh does: first value wins, `Host *` and patterns with `!` apply, `Include` files are read, `Match` blocks are skipped.
  - Hosts already configured (same name, or same user, host and port), aliases of the same machine and git hosts (github.com, gitlab.com…) are left out; the rest are all chosen, `space` unchooses one, `a` all or none. A host that does not validate is reported and skipped; the config is written once.
- **Every server at a glance.** `c` on the home screen (or the *Check servers on start* setting) checks all servers in parallel, each on a connection of its own that is closed after, and shows a line under each one: `✓ 21/26 running`, or what is wrong: `2 down: shop-worker, mailpit`, `1 unhealthy`, the disk and load, the server panel's warnings, or why it could not be reached.
  - Down counts containers that should run: stopped containers hidden by config, old Swarm tasks a newer one replaced and one-offs that exited with 0 are left out; a Swarm service without a running task counts once.
- **Swarm service actions** in the Docker tab of a Swarm task (Dokploy apps are Swarm services).
  - Service tasks: every task with its state, node, image and, in full, why it failed (`docker service ps --no-trunc`): what you look for when a deploy does not come up. `enter` shows one task.
  - Redeploy service (`docker service update --force`), Roll back service (`docker service rollback`) and Scale service (a replica count from 0 to 50; global services are refused), from the tab or with `u` / `b` / `S` on the tasks. Each asks first with the exact command, waits for the service to converge, and reloads the tasks after.
  - Restarting a Swarm task from the container list now points to Redeploy service for restarting the whole service.
- **After a deploy.** The container card of a Laravel app shows its migrations: `✓ all 42 ran`, or `⚠ 2 pending` with the first one's name (read with `migrate:status`, which changes nothing).
  - The Laravel tab has the commands a deploy needs: Run migrations (the menu says what is pending), Optimize, Clear caches, Restart queue workers (`queue:restart`, plus `horizon:terminate` where Horizon is installed) and Maintenance mode on or off, whichever applies.
  - Each asks first and shows the exact command; Run migrations lists the pending ones and says when the app is production. Back on the container, the card is read again: caches, maintenance and migrations show the result.
- **Errors summary of a Laravel log** (Logs tab → Errors for the newest log, or `e` on a file in Laravel logs). Every kind of error and warning once: count, level, exception, message, where it was thrown, first and last time; `enter` shows the latest occurrence with its stack trace.
  - Kinds are told apart by level, exception class, file and line, and the message with numbers, IDs and quoted values blanked out, so "order SH-49231" and "order SH-50117" are one kind.
  - Errors come before warnings; `o` orders by the latest instead of the count. `l` opens the log itself.
  - Entries whose message has line breaks (a `QueryException`'s SQL error) are read whole: their exception and location are found further down.
  - Grouped inside the container with awk over the last 64 MB (`a` reads the whole file): only the summary travels, even from a multi-GB log.
- **Server panel** above the container list, like the container screen's card: the OS, kernel and uptime, load against the number of cores, available memory and swap, the root disk and Docker's data disk, the Docker version, storage driver, Swarm role and Dokploy.
  - Warnings for a disk 85% / 95% full, memory under 10% / 5% available, a load above the number of cores, a pending reboot and a Swarm in error.
  - Read with one command that changes nothing on the server (`/etc/os-release`, `/proc`, `df`, `docker info`), alongside the list; `r` reads it again.
  - `p` switches between the full panel and one line and saves the choice; a short or narrow window gets the one line. The `server_panel` setting (full, compact, off) is also in Settings; off skips the query.
  - When Docker runs in a VM (Docker Desktop, OrbStack) or on another machine (`docker -H`, a context), only what docker info reports is shown: the connected machine's load and disks would describe another system.
- **Horizon dashboard.** In apps with Horizon, Queues opens a live dashboard updated every 3 seconds, read through Horizon's own repositories inside the app, so no Redis access is needed.
  - Dashboard tab: Horizon's stats, the workload per queue and the supervisors.
  - Pending, Completed, Failed and Silenced tabs with tags, times and runtime; `enter` opens a job with its payload and exception.
  - Metrics tab: throughput and runtime per queue and job.
  - Actions: retry and forget failed jobs (one or all), clear a queue, pause or continue Horizon, and terminate it so it restarts with the new code.
- **Image update check** (`u` on the Images tab). It compares the digest of each tag in its registry with the digests the image was pulled with, reading the registry without pulling (`docker buildx imagetools inspect`, six at a time).
  - The update column shows up to date, update available, local build, or can't check with the reason in plain words: the registry's rate limit (Docker Hub allows 100 anonymous requests an hour), no access to a private image, an unknown tag, no buildx.
  - The digests of a retagged image do not count as its own.
  - `p` pulls the selected image, with a reminder that containers keep the old image until they are redeployed.
- **Environment** (Laravel tab): the app's effective variables (`.env` plus the container environment, which wins), where each comes from, checked against `.env.example` (missing, empty, extra). `o` compares with another running container, secrets are masked until `v`, and `e` edits `.env`. Apps without a `.env` (Dokploy) work instead of failing with "No such file".
- **Editing files in containers** (`e` in the file viewer; `.env` from the Environment screen).
  - Saving shows the diff for confirmation. The write is refused if the file changed since it was opened, and the previous version goes to `/tmp/laraport/backups` in the container.
  - The file is written in place, so owner and mode are kept, and tabs and CRLF line ends are preserved. Files over 2 MB stay read-only.
  - For `.env`, the editor notes the Dokploy/environment override and `config:cache`.
- **`f` in the container list** opens the file browser of the selected container, at its configured `root_path`.
- **Images, volumes and networks** (`i` on the container list, or the palette): three tabs with tables, biggest first, showing sizes (and unique image size), what uses each item, dangling images, compose projects of volumes and attached containers of networks.
  - `D` removes the selected item; a tagged image is removed by name, so only that tag goes.
  - `P` prunes unused items; on the images tab, `A` prunes all unused images.
  - Every removal is confirmed with its docker command, used items and data loss are spelled out, and Docker's own networks are protected.
- **Adding rows in the table view** (`a`): a form with every column, showing its type, nullability, default, and whether it is generated or required. Empty fields keep their defaults, `ctrl+n` sets NULL. The `INSERT` is confirmed with its SQL, and on production apps editing must be enabled first. The table reloads afterwards.
- **Export from the table view**: `x` writes the loaded rows to CSV, `X` to JSON (column order and `null` kept), into the downloads folder.
- **Queues** (Laravel tab):
  - queue sizes of the default connection, Horizon status, and failed jobs with the exception summary and full details;
  - `r` retry, `D` forget, `A` retry all, `F` flush (via artisan, the last three confirmed);
  - read inside the booted app, so any driver and failed-job store work.
- **Schedule** (Laravel tab): the scheduled tasks as a table, soonest first. The columns are: time until the next run, next run, cron expression, the command or description, flags (no overlap, one server, background) and timezone. They are read from the app's Schedule, not parsed from `schedule:list`.
- **Table structure**: `i` in the table view or `tab` in the table list shows the columns with types, nullability, defaults, PK/UNIQUE/indexes and foreign keys (PostgreSQL, MySQL/MariaDB, SQLite).
- **Upload into containers** (file browser, `F6`/`u`): a local file or directory goes into the current directory.
  - The prompt has `tab` completion of local paths and shows sizes.
  - Data is streamed as a tar archive into `tar -x` in the container: no temporary files, with progress, symlinks kept, local ownership dropped.
  - Overwriting asks first, and the browser reloads afterwards.
- **Group logs**: `l` on a group header follows all running containers of the group in one stream, with coloured service labels. The backlog is merged in time order (docker `--timestamps`), up to 16 containers. Search and level filters apply.
- **Log search and level filter**:
  - `/` searches as you type, with highlighted matches, a `match 3/17` counter and `n`/`N`.
  - `l` filters to warnings+ or errors only; Laravel entries keep their stack traces, and other logs are classified by level words.
  - `]`/`[` jump between errors, and the header counts them. Loading earlier lines keeps the position under a filter.
- **Tinker** (Laravel tab): runs PHP in the booted app, Tinkerwell-style.
  - An editor with line numbers; the output, the last expression's value (VarDumper, through PsySH like `artisan tinker`) or the exception, plus time and memory.
  - Snippet history per server/container; `esc` stops a long run.
  - The code is passed via stdin (base64), so it never shows in process listings.
  - The first run in a production app is confirmed.
- **Container shortcuts in the list**: `l` follows the logs, `t` opens a shell, `s`/`x` start or stop, `R` restarts, `D` removes (`docker rm -f`). Stop, restart and remove ask first, with Cancel focused; for Swarm tasks the confirmation explains that Swarm starts a replacement. The list reloads afterwards and shows the result.
- **Editing in the table view**: `e` edits a cell (multi-line text, `ctrl+n` for NULL), `D` deletes a row. Each change shows its SQL and runs only after confirmation.
  - Rows are addressed by the primary key, and the result reports the rows changed (0 when the row is gone).
  - Values are written as literals that cannot break the statement: PostgreSQL dollar quoting with a random tag, MySQL `CONVERT(X'…' USING utf8mb4)`, SQLite strings. They convert to the column's type.
  - Tables without a primary key and query results stay read-only.
  - On a production app (`APP_ENV=production`), editing must be switched on explicitly per table view.
  - Integration tests run on real PostgreSQL, MariaDB and SQLite with hostile values (quotes, backslashes, `$$`, injection attempts, `\r\n`, Unicode, `'NULL'`, empty strings).
- **Table view for databases** (PostgreSQL, MySQL/MariaDB, SQLite):
  - **Browse tables** lists tables with estimated row counts. A table opens in a grid of 100-row pages (`n`/`p`), with sort by column (`s`) and a `WHERE` filter (`/`) run by the database.
  - Your own `SELECT`/`WITH` queries and the built-in stats queries open in the same grid, limited to 500 rows without transferring the rest.
  - The grid: framed with `┬┼┴` junctions, a cell cursor, horizontal scrolling, right-aligned numbers and a distinct `NULL`. `enter` shows the whole row, `y` copies a cell.
  - Results are read as structured output (psql `--csv`, mysql `--xml`, sqlite3 `-csv`), so `NULL`, `''` and `'NULL'` stay distinct, as do commas, quotes, newlines and Unicode.
- **Syntax highlighting in the file viewer** (chroma, laraport's palette). Files from the file browser open in a viewer with line numbers and highlighting:
  - languages: PHP, Blade, JSON, `.env`, YAML, JS/TS/Vue, SQL, XML, Markdown, Dockerfile, nginx, Caddy, INI and shell, plus shebang detection for `artisan` and scripts;
  - every line of a multi-line comment or string is coloured on its own;
  - keys: `c` colours, `n` line numbers, `F2` wrap (with the gutter kept), `g`/`G` top and bottom, `d` download;
  - files up to 2 MB are read whole; logs and bigger files still open in the log viewer; binary files are detected.
- **The app's Redis/database without a client in the app image**: when the app container has no `redis-cli` / `psql` / `mysql`, laraport runs them in the server's own container on the same server, found by the app's host name (container name, Swarm service, Compose service). The connection still uses the app's host, user and password. The Data entry shows `run in <service>`.
- **Laravel in the container card**: from `php artisan about --json`, run once when the container opens. It shows:
  - the Laravel and PHP versions, environment and URL;
  - a **DEBUG ON** badge (red in production) and a **DOWN** badge for maintenance mode;
  - the drivers;
  - which caches are built (uncached config/routes are highlighted in production);
  - package versions (Livewire, Filament…).

  If `about` fails, a lighter probe that only loads Laravel's env and config is used. The result reaches the card even if another screen was opened meanwhile.
- **Versions in the Stack row**: PHP, Node, nginx, Caddy, FrankenPHP, PostgreSQL, MySQL/MariaDB, Redis and MongoDB versions are read during the same container probe.
- **The Laravel app's database and Redis** (Data tab, **PostgreSQL / MySQL / MariaDB (app)** and **Redis (app)**): connections are read from the app's configuration, by loading Laravel's env and config only (honours `config:cache`, `DB_URL`, the real environment) with a fallback to the container env and `.env`. They are run with the clients in the app container, so databases in other containers on the internal network (Dokploy) are reachable. Passwords go via stdin. The app's configured database opens directly: other databases on the same server are not listed, so you cannot reach another app's database by accident. The screens show `via app · host:port/db`.
- **`usage_columns` setting** (Settings → CPU / MEM columns) for the container list's CPU/MEM columns. `auto` (the default) shows them only when the window is at least 120 columns wide, so narrow windows keep room for names. The other values are `always` and `never`; `never` also skips the `docker stats` query.
- **Container groups**: the container list is grouped by project, taken from the Swarm stack, the Dokploy project, the Compose project, or a per-container `group` (a **Group** field in the editor). Dokploy deploys plain Swarm services without project labels, so its `<project>-<app>-<random>` names are used, and Dokploy's own services form a `dokploy` group. Group headers show running/total counts, and the containers under them are indented by two cells. `enter`/`space` collapses a group and `v` switches grouping off. Groups with favourites come first; ungrouped containers go under **Other**.
- **Container settings survive redeploys**: an entry can be bound to a Swarm service (`service: swarm/<service>`) or a Compose service (`service: compose/<project>/<service>`) instead of a container name, which changes on every Swarm redeploy. The service labels come from the existing `docker ps` call. The editor (`e`) has an **Applies to** field (*service*, *exact name* or *pattern*), defaults to *service* for Swarm and Compose containers, and moves existing exact-name entries to it, keeping their commands and logs.
- **Clean up container settings** (command palette on the container list): finds entries that match no container, running or stopped, and removes them after confirmation. Patterns are kept.
- **Container summary card** on the container screen: health and OOM badges, uptime and start time (or exit code and when it stopped), restart count and policy, live CPU and memory bars against their limits, ports, networks and IPs, Compose or Swarm project/service, start command and working directory, mounts, and the detected stack. It refreshes every 5 seconds while the container runs and shrinks to one line on short terminals.
- **New interface** - a design system on Lip Gloss in Laravel red (with light-terminal variants) used by every screen:
  - **Home screen** with the laraport logo (gradient; large / medium / compact depending on the terminal size), tagline and version above the server list
  - **Frame** on every other screen: breadcrumb header (`▲ laraport › server › container › screen`) with the connection, and a segmented status bar with the current server/container and context key hints
  - **Container screen with tabs** - Overview · Laravel · Logs · Data · Docker · Files (`←/→`, `1`–`6`), a state/image/uptime/ports summary, and only the tabs and actions the container supports
  - **Container list as a table** - status dots, ★ favourites, name / image / status / ports / CPU+memory columns, `/` filter
  - **Modal windows** for confirmations (Yes / Cancel buttons, Cancel focused), the server form, settings and the container form, drawn over the dimmed screen
  - **`?` help overlay** with all keys of the current screen, and a **`ctrl+k` command palette** to run any action by typing (servers on the home screen, every tab action on a container)
- **Jump host (bastion) support** - `jump_host: "[user@]host[:port]"` (and optional `jump_key`) on a server, also editable in the server form. laraport connects to the bastion, tunnels to the server over a `direct-tcpip` channel (like `ssh -J`) and verifies both host keys with the configured policy; a dropped bastion or tunnel is re-established on the next action. Terminal goes through the bastion too (`ssh -J`, or a `ProxyCommand` with `-i` when a bastion key is set). The server list shows "via <bastion>"
- **Server management in the UI** - on the server list: `a` add, `e` edit, `d` delete (with confirmation, naming the container settings removed with it). The form validates input (unique name, host/user for SSH, port range, key file exists) and `ctrl+t` tests the connection (`docker version`). Adding a server keeps the implicit Local server in the config; deleting the last server brings it back
- **Per-server `docker_cmd`** - e.g. `sudo -n docker` for users outside the `docker` group, `docker --context …` or `podman`. Applied to every Docker call on that server, including Terminal and Docker Logs, via a shell function in front of each host command, so data inside containers or quoted arguments is never rewritten
- **Per-server `root_path`** - default app root for containers without their own
- **Settings screen** (`s` on the server list) and `settings:` block in the config - downloads folder, SSH host key policy (`accept-new` / `strict`), default line wrapping, show/hide stopped containers, Stats refresh interval, SQL history on/off and size, plus a button to clear SQL history
- **`host_key_check: strict`** - refuses servers not already in `~/.ssh/known_hosts`; the error shows the key fingerprint and the `ssh-keyscan` command to add it
- The first time laraport rewrites `config.yaml` it keeps the original as `config.yaml.bak` (comments are not preserved by rewrites)
- **Test suite and CI** - unit tests for every package (in-process SSH server, fake `docker` CLI, TUI orchestration and screens) plus Docker integration tests across busybox/dash/bash shells and PostgreSQL, MariaDB, Redis and MongoDB; GitHub Actions runs them with the race detector on Linux and macOS together with `govulncheck`, and releases are gated on them
- **Edit container config from the UI** - press `e` on a container in the list to open a form for its display name, root path, favorite and hidden flags
  - Saves back to `config.yaml` (`Config.Save`), creating an exact-name entry that takes precedence over any glob rule
  - The list refreshes immediately; `~` in SSH key paths is preserved on write
- **Redis & MongoDB backups** - the Redis and MongoDB inspection screens gained a **Backup** action
  - Redis: **Download RDB snapshot** via `redis-cli --rdb` → `~/Downloads/redis_<timestamp>.rdb`
  - MongoDB: **Download dump** via `mongodump --archive --gzip` → `~/Downloads/mongo_<timestamp>.archive.gz`
  - (SQL engines already supported dumps; this brings the NoSQL engines to parity)
- **Terminal** - new container-menu entry that opens an interactive shell inside the container
  - Suspends the TUI and attaches the real terminal (full PTY) via `tea.ExecProcess`, resuming on shell exit
  - Local servers run `docker exec -it`; SSH servers shell out to `ssh -t` with the server's host/port/key
  - Clears the screen on entry so no TUI remnants are left behind
  - Prefers `bash`, falling back to `sh`
- **Live Stats screen** - new **Stats** entry in the container menu with real-time charts that refresh every 2s
  - Sparkline graphs for CPU %, memory, network rate, and disk I/O rate (network/disk derived as per-second deltas from cumulative `docker stats` totals)
  - Top-processes table underneath (from `ps` inside the container) with `c` / `m` to sort by CPU or memory
  - Polling stops automatically when you leave the screen
- **Container Info screen** - new **Info** entry in the container menu showing parsed `docker inspect` data: name, ID, image, status, networks/IP, mounts (source → destination), and labels - in a scrollable, sectioned view
- **Global Docker cleanup** - press `g` in the container list to open server-level Docker commands not tied to a container
  - Disk usage (`docker system df`, verbose) and prune commands for stopped containers, images, volumes, networks, build cache, and full `system prune`
  - Destructive commands require a `y`/`n` confirmation screen showing the exact command; cleanup entries are highlighted in red
- **Stopped containers in the list** - the container browser now uses `docker ps -a`, so stopped/exited containers are shown too
  - Non-running containers are dimmed and tagged with their state (e.g. `myapp (exited)`)
  - Sorting puts running containers first, then favorites, then alphabetical - stopped containers group at the bottom
- **Container list stats** - each container now shows its status (uptime/health), published host ports, and live CPU / memory usage
  - Status and ports come free from the existing `docker ps` call
  - CPU/memory are fetched lazily with a single background `docker stats --no-stream` after the list is shown, so the list never blocks on the slower stats call
  - Refreshing the list (`r`) re-fetches both
- **MySQL support** - the **Database** menu now works with MySQL containers in addition to PostgreSQL
  - Engine auto-detected via the container probe (`mysql` client → MySQL; `psql` still wins if both are present)
  - Credentials read from `MYSQL_ROOT_PASSWORD` (preferred, as `root`) or `MYSQL_USER` / `MYSQL_PASSWORD`
  - Lists non-system databases (excludes `information_schema`, `performance_schema`, `mysql`, `sys`)
  - Per-database action menu mirroring the PostgreSQL one: Info & Stats, Schema, Indexes, Maintenance, Run SQL query, and SQL dump download
  - **Download SQL dump** via `mysqldump --single-transaction --no-tablespaces | gzip` → `~/Downloads/<db>_<timestamp>.sql.gz`
- **MariaDB & Percona support** - both are detected from the `mysql --version` string and reuse the full MySQL pipeline (client, `information_schema`, `mysqldump`); only the engine label differs
- **SQLite support** - file-based databases via the `sqlite3` client (used when no server DB client is present)
  - Scans the app root (`database/`, `storage/`, root, `/data`) for `*.sqlite` / `*.sqlite3` / `*.db` files and lists each as a database
  - No credentials needed; actions use `sqlite_master` and `PRAGMA` (version, size, row counts, schema, indexes, integrity/foreign-key checks, `VACUUM`)
  - **Download SQL dump** via `sqlite3 <file> .dump | gzip` → `~/Downloads/<file>_<timestamp>.sql.gz`
- **Redis inspection** - new **Redis** menu item (shown when `redis-cli` is present)
  - Auto-detects the password from `REDIS_PASSWORD` / `REDIS_URL` and runs commands as `docker exec … redis-cli [-a …]`
  - Curated read-only commands: `INFO` sections, `DBSIZE`, `CLIENT LIST`, key sampling/counting via non-blocking `SCAN`, `SLOWLOG`, `CONFIG GET`, `PING`, `LASTSAVE`, `LATENCY DOCTOR`
- **MongoDB inspection** - new **MongoDB** menu item (shown when `mongosh` or legacy `mongo` is present)
  - Auto-detects root credentials from `MONGO_INITDB_ROOT_USERNAME` / `MONGO_INITDB_ROOT_PASSWORD` / `MONGO_URL` / `MONGODB_URI` and runs `docker exec … <shell> --eval '<js>'`
  - Curated commands: server/DB stats, `listDatabases`, collection names/counts/sizes, indexes, `ping`, `replSetGetStatus`, profiling status
- **File Browser** - walk the container filesystem from the main menu (any container, rooted at `/`)
  - Lists directories first then files, each with its size (`du -sb` for dirs, byte size for files)
  - Navigate with `↑↓`, `enter`/`→` to open a directory or view a file in the log viewer, `←`/`backspace` to go up, `esc` to leave
  - Press `d` to archive and download any file or directory to `~/Downloads/<container>_<name>_<timestamp>.tar.gz` via streamed `tar | base64` (no temp files left on the server)

### Changed
- **New logo: an anchor** (from Phosphor Icons, MIT) on Laravel red, in the README and on laraport.app. The terminal UI keeps its ▲ in the header: an anchor character is an emoji in most terminals and would break the layout.
- **README rewritten.** A shorter tour with a recorded clip for each main feature (`docs/media/`), install, quick start, a config example and security notes; the full reference moved to the documentation at [laraport.app/docs](https://laraport.app/docs/).
- **The table view uses the whole width**: when every column fits, long columns get their full content width beyond the 40-cell cap, and the rest is spread over all columns.
- **File browser redesigned after Midnight Commander**: a framed panel with Name / Size / Modify time columns, plus permissions and owner on wide terminals. It has mc's type marks (`/` `~` `@` `*`) instead of emoji icons, which ran into the names. Column rules meet the frame with `┬`/`┴` junctions, rows keep a cell of margin from the frame, and the frame uses a brighter colour that stays visible on black terminals. There is an `..` entry, a line about the selected entry and directory totals.
  - Keys: `/` quick filter, `s`/`S` sort by name, size or time, `.` hides dotfiles, `F3`/`v` view, `F5`/`d` download, `~` app root, `r` refresh.
  - Going up puts the cursor on the directory you left. The browser opens at the Laravel app root.
  - `esc` now leaves the browser; `backspace`/`←` go up, even while a directory loads.
- **Directory listings are fast**: one `stat` call instead of running `du` on every subdirectory before showing anything (slow on `/`, which walked `/proc` and `/usr`). Sizes are measured in the background with a timeout, skipping `/proc`, `/sys` and `/dev`.
- **The container card adapts to the height**: its rows have priorities, and the least important ones (mounts, command, packages…) are dropped first on short terminals instead of the whole card collapsing to one line.
- **Stats charts fill the window** - the four panels share the full width and height (the process table gets up to a third of the screen); charts are multi-row bar charts with ⅛-row resolution, and history keeps 600 samples so wide charts are filled
- **Selected row is highlighted across its full width** in every list (servers, containers, tab actions, palette, command lists, files, databases), so the selection is obvious on wide terminals; the left selection bar stays for colourless terminals
- **Container list uses the full terminal width** - columns are sized to their content and the remaining space is spread across them, so the table always spans the window (long names are no longer cut on wide terminals); ports and CPU/memory columns drop out first on narrow ones
- **Compact container status** - `Up 3 months (healthy)` → `3mo ✓`, `Up 4 minutes (health: starting)` → `4m …`, unhealthy → `✗` with a red status dot, `Exited (137) 8 hours ago` → `exit 137 · 8h`, `Up About an hour` → `1h`; memory as `327M` / `1.2G`
- Output, log and custom-command screens show their state as badges (RUNNING / DONE, LIVE / PAUSED / ENDED) next to the title, with position, line count and wrap state on the same line
- Confirmations: `enter` now presses the focused button (Cancel by default); `y` still confirms directly
- Stats network/disk panels fit the terminal width
- CI runs once per change (pull requests, plus pushes to `main`); unit tests no longer pick up tools installed on the CI image (e.g. `composer`)
- **Renamed laradok → laraport** to avoid confusion with the well-known Laradock project: binary `laraport`, Go module `github.com/alexbabintsev/laraport`, Homebrew formula `alexbabintsev/tap/laraport`, config directory `~/.config/laraport/`, environment variable `LARAPORT_CONFIG`
- README feature list covers server management, bastion, per-server docker command, settings and Terminal
- Stats network/disk rates are computed over the time actually elapsed between samples, so they stay correct with any refresh interval
- **Log viewer reads by byte offset** - opening a log reads the last 256 KB with a seek (`dd skip`) and follows from that exact offset (`tail -c +N -f`); scrolling up loads the 256 KB before the earliest loaded byte. Previously opening counted every line (`wc -l`) and each scroll-back chunk was read with `sed -n 'a,bp'`, which scans the file from the start - both grew with the file size. Opening and paging a 1 GB log now takes the same ~0.2 s as a 1 MB one. The status bar shows `position / size  percent` in bytes
- **Log pickers list instantly** - files are listed from metadata only (size, dates); line counts are computed in the background and filled in as each finishes, and counting stops when you leave the picker
- Saved `config.yaml` omits empty optional fields; `port` defaults to 22 only for SSH servers
- Download progress shows bytes below 1 KB instead of "0 KB"
- Saving the config no longer writes the implicit "Local" server (added when the config defines none) unless it gained container settings; a symlinked `config.yaml` (dotfiles) keeps its link
- `~user/…` key paths are no longer mangled (only `~` / `~/…` are expanded)
- Removed the unused `internal/dbg` package
- **Commands run in the app root** - custom commands (config `commands:`) and the Artisan/Composer/Npm pickers now `cd` into the container's app root (configured or detected) before running, falling back to the image `WORKDIR` when it does not exist
- **Log file list in one round-trip** - size, timestamps and line counts are gathered by a single in-container script instead of two
- **Faster container list** - `docker ps` is no longer always run twice; it is retried only when the output looks truncated. Fields are emitted with `{{json …}}`, so names/statuses containing quotes no longer drop the container
- **Laravel detection** - the capability probe now also looks for `artisan` (and `composer.phar`) in `/app`, not just the configured root / `/var/www/html`. When `artisan` is found at `/app` and no `root_path` is set in config, laraport adopts that root so artisan, logs and storage commands target the right directory.
- **Download progress** - archive downloads (File Browser + Download Storage) now show a single `received N MB` counter that updates in place instead of flooding the output with one line per chunk
- **PostgreSQL management** - full database browser accessible from the main menu
  - Auto-detects `POSTGRES_USER` / `POSTGRES_PASSWORD` from container environment
  - Lists non-system databases (excludes `postgres`, `template0`, `template1`)
  - Per-database actions menu with 20+ built-in queries across 4 categories
- **Info & Stats queries** - DB size, version/uptime, table sizes (top 20), row counts, cache hit ratio, active connections, long-running queries (>5 s)
- **Schema queries** - list tables, schemas, views, sequences, and user-defined functions
- **Index queries** - unused indexes, duplicate indexes, index usage statistics
- **Maintenance queries** - table bloat / dead tuples, active locks, replication status, VACUUM ANALYZE
- **SQL query input screen** - freeform SQL with `↑↓` history navigation
- **Persistent SQL history** - stored per-database in `~/.config/laraport/sql_history.json`, key `serverName/containerName/dbName`, up to 200 entries, deduplicated
- **Database dump download** - two formats available:
  - **SQL dump** - `pg_dump --no-owner --no-acl | gzip` → `~/Downloads/<db>_<timestamp>.sql.gz`; `\restrict` and `\unrestrict` lines stripped after decompression
  - **SQL dump (inserts)** - same but with `--inserts --column-inserts` (slower, more portable INSERT-based format)
  - **Custom dump** - `pg_dump --no-owner --no-acl -Fc` → `~/Downloads/<db>_<timestamp>.dump` (binary format for `pg_restore` with selective restore)
- `docker.ShellQuote` exported for safe shell argument construction
- `config.LoadSQLHistory` / `config.SaveSQLHistory` for persistent per-key history
- **Automatic menu filtering** - main menu probes the container on open (one `docker exec` round-trip) and shows only relevant items: Artisan/Composer/Npm/Laravel Logs/Download Storage hidden for non-Laravel containers; Database hidden when `psql` is absent; spinner shown during detection; menu items not shown at all until detection completes (no flash of irrelevant items)
- **Download Storage** - new main menu item archives `storage/` inside the container via `tar | gzip | base64 -w 76`, shows directory size before transfer, saves to `~/Downloads/<container>_storage_<timestamp>.tar.gz`; no temporary files created on the server

### Fixed
- **Image update check on Docker 29**: its containerd image store gives every image a digest under its own name, built ones too, so images built on the server showed "can't check: pull access denied". A registry answering that the repository does not exist now means a local build.
- The status bar shows a container's display name, as the header does, instead of its full Swarm task name.
- The Environment screen and its list of containers to compare with use display names too.
- The Laravel logs picker no longer lists `storage/logs/.gitignore` (and other dotfiles) or compressed rotations as logs.
- SQL and Tinker history of Swarm containers (Dokploy) is kept per service, so it survives redeploys, which give the container a new name. History saved under the old names is not carried over.
- Info, Stats, Tinker, Docker, Redis, MongoDB and download titles use the container's display name.
- Images, volumes and networks: pressing a key after a failed load (e.g. Docker unreachable) no longer crashes laraport; the error stays, and `r` reloads.
- The image update check honours the server's `docker_cmd` (e.g. `sudo -n docker`); it used to call plain `docker`.
- **Uploads failed with "Permission denied"** in containers that run as an unprivileged user (the app directory belongs to root). Uploads are now unpacked as root, like `docker cp`, and handed to the owner of the target directory.
- **Composer failed with "installer signature mismatch" in containers without composer**: a failed download of the installer (no network access, TLS, `allow_url_fopen`) went unnoticed and was reported as a signature mismatch. Each step now reports its real error. `composer.phar` is also downloaded into `/tmp/laraport` instead of the app directory, which is deployed code and may not be writable. A `composer.phar` shipped with the app is still used.
- **Values with `\r\n` lost the `\r` in the table view** (Go's CSV and XML readers normalise line breaks). Results are now read byte for byte, so a value opened in the editor and saved is not altered.
- **Table view rows with emoji were misaligned**: symbols that terminals draw at different widths (`‼`, `❤️`, flags, ZWJ sequences, variation selectors) shifted the columns. In cells they now show as `◇`, and invisible characters are dropped. The record view keeps the original text.
- **Data tab offered a database that was not there**: a client in an app image (e.g. `psql` installed for `schema:dump`) was taken for a database server, and opening it failed with "connection to server on socket … failed". Database, Redis and MongoDB entries now need the server in the container (`postgres`, `mysqld`/`mariadbd`, `redis-server`, `mongod`); a client alone enables the *(app)* entries instead.
- **Hidden containers could not be brought back**: the container list counts them in its header (`· 3 hidden`), `.` (or the palette) lists them dimmed with a `hidden` badge, and `e` → Hidden off unhides them
- **Old Swarm tasks filled the container list**: Swarm keeps up to 5 stopped tasks per service slot (`task-history-limit`). Tasks replaced by a newer one are now collapsed, and the header counts them (`· 4 old tasks`). `.` lists them with an `old task` badge, together with hidden containers. A service with no running task keeps its newest stopped task visible.
- A log line still being written when the viewer opened was shown split in two; it now arrives whole
- Scrolling up in Docker Logs no longer leaves a permanent "loading…" spinner (there is no file to page through)
- File sizes of 1 GB and more are shown in GB
- **Crash on small terminal windows** - the output, log and command screens could compute negative or too-small viewport sizes, and bubbles' viewport then panicked on scroll; sizes are now clamped and the scroll offset is kept in range after content changes
- **Wrapped lines no longer cut multi-byte characters** - line wrapping (F2) counted bytes, splitting Cyrillic and other UTF-8 text mid-character; it now measures display cells and keeps ANSI colours
- **File browser: `esc` works while a directory is loading** - a slow listing (`du` over a large tree) no longer traps you on the screen
- **Exiting the app stops the running stream** and closes the connection, so nothing (e.g. a local `docker exec … tail -f`) is left running after quit
- Artisan / Composer / npm pickers show why listing the commands failed (e.g. a PHP fatal error) instead of an empty list; manual input still works
- Typing into a finished Custom Command no longer leaks a goroutine per line
- Job-control notices (`[1]+ Done …`) from containers whose `/bin/sh` is bash no longer appear in command output
- **Incomplete / corrupted output from remote servers** (container list missing entries, partial database lists, stray NUL bytes) - the SSH runner wrote stdout and stderr into one unsynchronised `bytes.Buffer` from two goroutines; whenever the remote side printed anything on stderr (shell rc files, CLI warnings) the buffer was corrupted. Output is now collected safely, and parsed commands read stdout only, so stderr noise cannot leak into lists. The retry-on-empty / NUL-stripping workarounds are gone
- **Dropped SSH connections are re-established** - after a network change, sleep or server restart the next action reconnects transparently instead of failing until the app is restarted. Dead connections are detected via keepalives (every 30 s) and a 10 s limit on opening a session; connecting and the SSH handshake time out after 15 s
- **UI freeze after leaving running commands** - output streams were started synchronously inside the UI loop and their stop function was discarded, so leaving an artisan/SQL/dump screen early leaked an SSH session slot; after six such exits the whole UI hung. Every stream now starts in the background and is stopped when its screen closes, and waiting for a session slot times out after 30 s
- **Leaving a screen now stops the command on the server** - cancelled streams (tail, `docker logs`, artisan, SQL, dumps) run as a background job inside the container that is killed when laraport closes stdin; previously closing a remote `docker exec` left e.g. `queue:work` or `tail -f` running in the container
- **Log tail no longer drops lines** appended between counting the file and starting to follow it (`tail -n +<N+1> -f`)
- **Storage download failed on live Laravel apps** - tar's "file changed as we read it" went into the data stream and broke decoding; it is now reported as a warning and the archive is kept
- **Large downloads no longer load into memory** - dumps and archives were collected as base64 text, then decoded (≈4× the file size in RAM). They now stream raw bytes straight to a private temp file that is renamed into place only on success; failed or cancelled downloads leave nothing behind, and existing files are never overwritten
- **Dump failures are reported** - `pg_dump | gzip` reported gzip's (successful) exit status, so a failed dump could be saved as a valid-looking empty file; the producer's status is now propagated
- **MariaDB 11+ containers** (which ship only `mariadb` / `mariadb-dump`) are detected and fully supported; `MARIADB_*` credential variables are honoured
- PostgreSQL database list is read from `pg_database` (one name per line) instead of parsing `psql -l`, so names containing `|` or spaces are listed correctly
- Credentials embedded in `REDIS_URL` / `MONGO_URL` / `MONGODB_URI` are percent-decoded; multi-host Mongo URIs are supported
- The capability probe reports errors (e.g. stopped container) in the menu instead of silently showing an empty menu
- Background loaders no longer read the active container from a goroutine, so switching containers quickly can't send a command to the wrong one
- Switching servers closes the previous SSH connection (it previously leaked); a connection that finishes after you left the screen is closed
- Local commands run in their own process group, so stopping a stream kills the whole pipeline
- Shell quoting bug in all psql/pg_dump commands: replaced `sh -c "PGPASSWORD='...' psql ..."` (broken double-quote nesting) with `docker exec -e PGPASSWORD=...` to inject credentials as environment variables - no shell interpolation of user-controlled values
- `ListDatabases` parsing: `psql -lqt` ACL entries (e.g. `postgres=CTc/postgres`) were incorrectly parsed as database names; now requires `len(parts) >= 2` and rejects names containing `=` or `/`
- `base64 -w 0` replaced with `base64 -w 76` in storage and pg_dump pipelines - `bufio.Scanner` has a 64 KB token limit and silently dropped single-line base64 output from large archives, causing "no data received" errors

### Security
- **Dependencies patched** - `golang.org/x/crypto` v0.49.0 → v0.57.0 (9 advisories reachable through the SSH client) and the toolchain pinned to `go1.26.10` (standard-library advisories, the latest GO-2026-6809 in `crypto/x509` and GO-2026-6617 in `net/http`); `govulncheck` now reports no reachable vulnerabilities and runs in CI and before every release
- **SSH host keys are verified** against `~/.ssh/known_hosts` with `accept-new` semantics - unknown hosts are recorded on first use, changed keys are rejected. Previously a missing `known_hosts` file silently disabled verification (`InsecureIgnoreHostKey`)
- **Passwords no longer appear in process listings** - PostgreSQL, MySQL and Redis passwords were passed as `docker exec -e PGPASSWORD=…` / `-a …` arguments, readable by any user on the host via `ps`. They are now written to the command's stdin and exported inside the container only; `mongosh` authenticates from the environment and `mongodump` uses a private `--config` file
- Downloaded dumps/archives are created with mode `0600`; `config.yaml` (which may hold key passphrases) and the SQL history are written atomically with mode `0600`
- Redis RDB snapshots use a private `mktemp` file instead of a fixed `/tmp` path
- **Host command injection via container-controlled names** - log file paths, files picked in the file browser, SQLite paths, DB/user names and `root_path` were interpolated into `docker exec … sh -c "…"` without quoting, so a file named e.g. `x$(cmd).log` inside a container ran `cmd` on the host (or the SSH server) when the log list was opened. Every in-container script is now built by `docker.ExecShCmd`, which single-quotes both the container ID and the script, and every embedded value is quoted individually
- **Raw / custom / config commands ran `$…` on the host** - they were wrapped with Go's `%q`, which does not escape `$` or backticks, so `echo $HOME` or `$(…)` expanded on the host. They now run verbatim in the container's shell
- Artisan arguments are passed to the container's shell instead of the host shell (pipes and `;` now apply inside the container)
- `composer.phar` auto-install verifies the installer's SHA-384 signature before running it
- Terminal over SSH passes `--` before the destination so a host value cannot be read as an `ssh` option
- Removed the unused `TailFile` runner method (it interpolated an unquoted path)


---

## [0.1.0] - 2026-06-22

First public release.

---

## Before the public release

Versions numbered while the project was still called doklar and then laradok, before 0.1.0 was published. They were never released.

### Internal 0.5.0 - 2026-04-29

#### Added
- Project renamed from **doklar** to **laradok** across all configuration, logging, and UI components


### Internal 0.4.0 - 2026-04-28

#### Added
- **Lazy log loading** - log files load in chunks (`LogChunkSize = 1000` lines); scrolling to the top triggers async loading of earlier chunks
- `LogTailInitMsg` / `LogChunkLoadedMsg` message types for chunk-based log state management
- `CountFileLines` and `LoadLogChunk` functions in the docker package

#### Changed
- `LogTailScreen` state management refactored for chunk-based position tracking (`topLine`, `totalLines`)
- Chunk loading stops retrying when a full chunk is obtained or the top of the file is reached


### Internal 0.3.0 - 2026-04-27

#### Added
- **Line wrapping toggle** (`F2`) in `LogTailScreen` and `OutputScreen`
- `wrapLines` helper for soft-wrapping long output lines at word boundaries

#### Changed
- `StreamCommand` now returns a `stop` function for graceful termination in both `LocalClient` and `SSHClient`
- SSH stop closes stdin pipe first, triggering `read; kill $PID` inside the container before closing the session


### Internal 0.2.0 - 2026-04-26

#### Added
- **Docker Commands screen** - hardcoded groups (Container, Filesystem, Network, Image) accessible from the main menu
- `PushDockerCmdMsg` navigation message
- `HostCmd` field on `PushOutputMsg` for commands that run directly on the host (not inside the container via `docker exec`)

#### Changed
- `startCommand` in `model.go` now routes `HostCmd` directly to `runner.StreamCommand`, bypassing `docker exec`
- Screen rendering uses `PinToBottom` layout helper for consistent status bar placement


### Internal 0.1.0 - 2026-04-25

#### Added
- Initial release
- Multi-server config with SSH and local Docker support
- Container list with favorites, custom display names, glob-pattern matching, hidden flag
- **Artisan Commands** screen - loads full `php artisan list` with descriptions, fuzzy-navigable
- **Composer Commands** screen - resolves `composer` binary or downloads `composer.phar` automatically
- **npm Commands** screen - parses `npm run` output for script names and bodies
- **Custom Command** screen - interactive shell with live stdin/stdout via `InteractiveCommand`
- **Laravel Logs** browser - lists `storage/logs/*.log` with size, line count, and timestamps
- **Docker Logs** - streams `docker logs -f --tail 100` for any container
- **Server Logs** - discovers `/var/log/**/*.log` files inside the container with service grouping
- `RawCmdScreen` for per-container `custom_logs` and freeform shell commands
- Config-driven global and per-container command groups
- SSH key auth with ssh-agent fallback and standard key auto-discovery
- Session ID mechanism to discard output from stale (navigated-away) sessions
- `OutputScreen` with scrollable viewport, spinner during execution, checkmark on completion
