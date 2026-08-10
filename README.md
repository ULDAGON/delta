# DELTA

DELTA is a local-first, encrypted diary and habit tracker shipped as one Go
binary. `delta serve` serves the REST API and the embedded dark, monospace web
shell from localhost, and from your local network as well once you turn that on
(see [Serve on the local network](#serve-on-the-local-network)).

## A day in DELTA

Everything starts at the pixel grid: a year per row, a pixel per day, colored
by your day rating or your daily habit score. It is the map of your diary —
click any pixel to open that day, including empty past days to backfill:

![The pixel grid across multiple years](docs/screenshots/grid.png)

Two hold-modes reshape the grid while their key is down: `p` paints each entry's
phase marker and lifts every journaled day off the empty base, `j` drops the
ratings and shows only which dates carry journal text. The footer lists both.

Inside an entry, `<` and `>` beside the date step to the previous and next
entry, so a stretch of days reads without going back to the grid; in the edit
wizard the same pair steps one day at a time. Save at the bottom right closes
the entry — everything typed is already autosaved.

Closing the day is a short guided wizard. It opens with freeform prose — write
as much or as little as you like:

![Entry wizard: freeform text](docs/screenshots/entry-freeform.png)

Next come the goals you set for today the night before — check off what
happened — plus three gratitudes and the three Ws: what went well, what could
have gone better, and your goal for tomorrow:

![Entry wizard: goals, gratitudes, and the three Ws](docs/screenshots/entry-goals.png)

Then rate the day on four 1–5 scales — Total is the felt verdict, never an
average — log hours worked on personal projects, and check off your habits:

![Entry wizard: ratings and habits](docs/screenshots/entry-ratings.png)

Saving rolls straight into setting tomorrow's five goals, and the grid grows by
one pixel. Over time, Stats turns the entries into streaks, per-habit
completion rates, and monthly trends:

![Stats: streaks, habit completion, and monthly averages](docs/screenshots/stats.png)

## Install

### GitHub Release

Download the archive for your platform from the [latest GitHub Release](https://github.com/ferriskleier/delta/releases), extract `delta`, and put it on your `PATH`. Releases contain macOS and Linux binaries for both `amd64` and `arm64`, plus a checksum file.

Verify the downloaded archive before extracting it with the checksum file and the checksum tool available on your platform, for example:

```sh
sha256sum -c checksums.txt --ignore-missing
# macOS: shasum -a 256 -c checksums.txt
```

On macOS, Gatekeeper may quarantine a downloaded unsigned binary. If macOS blocks it after you have verified the download, remove that quarantine attribute once:

```sh
xattr -d com.apple.quarantine ./delta
```

DELTA does not require Homebrew and does not currently publish Windows builds or signed/notarized macOS builds.

### Go install

The CGO-free build can also be installed directly with Go:

```sh
go install github.com/ferriskleier/delta/cmd/delta@latest
```

Make sure `$(go env GOPATH)/bin` is on your `PATH`, then verify the install:

```sh
delta --version
```

`delta --version` reports the version, commit, and build date. Release builds receive all three values from ldflags; `go install` reports the module version and omits the commit and build date, which are not available in module-proxy builds.

## Build

The Vite output in `web/dist` is committed so `go install` builds embed the
frontend. When frontend sources change, rebuild before compiling Go so
`go:embed` includes the current static assets:

```sh
cd web
npm install
npm run build
cd ..
go build ./cmd/delta
```


## First run

Run `delta serve` even before initialization. With no config it starts a
localhost-only setup server, prints a `setup:` URL, and best-effort opens that
URL with `open` on macOS (`xdg-open` elsewhere). The browser wizard has both
doors: create a new encrypted diary or open an existing one. After either door
finishes, the same process atomically switches to the authenticated normal
handler; the setup endpoints are no longer available. The final screen shows
the per-machine API token needed by CLI and agent clients.

For headless setup, `delta init --path <p>` and
`delta init --open <p> --key-stdin` remain available.

The database path and the backups folder can be changed later in Settings.
Changing the database path copies the encrypted file to a fresh location, or
adopts a diary already sitting there once the configured key opens it; the old
file stays where it is either way, so a half-finished move is never the only
copy of a diary. The new path is opened by the next start: until you restart,
the instance keeps reading the file it already has open and refuses diary
writes. Changing the backups folder needs no restart — the next snapshot lands
in the new directory.

## Run in the background

`delta service install` registers `delta serve` as a login service so it runs
in the background and restarts after crashes:

```sh
delta service install
```

On Linux this writes a systemd user unit to
`~/.config/systemd/user/delta.service` and runs
`systemctl --user enable --now delta.service`; logs go to
`journalctl --user -u delta.service`. On macOS it writes a launchd agent to
`~/Library/LaunchAgents/com.ferriskleier.delta.plist`; logs go to
`~/Library/Logs/delta/serve.log`. `delta service status`, `stop`, `start`, and
`uninstall` manage the service afterwards.

To manage the Linux unit with systemctl directly:

```sh
systemctl --user status delta.service    # running?
systemctl --user restart delta.service   # e.g. after replacing the binary
systemctl --user disable --now delta.service
```

User services stop at logout; on an always-on machine, run
`loginctl enable-linger $USER` once to keep the service running without an
active session.

## Serve on the local network

DELTA is reachable only from its own machine by default. Settings → API has a
LAN access toggle, persisted as `lan` in `config.toml` and applied on the next
restart. With it on, `delta serve` binds IPv4 `0.0.0.0` — an IPv4-only socket,
never the dual-stack wildcard, so none of the machine's routable IPv6 addresses
start answering — and Settings → API lists the `http://<lan-ip>:7331` URLs to
open from another machine on the same network.

Everything off the local network is rejected rather than served. A request is
answered only when its peer address is loopback, link-local, or on one of the
subnets this machine's own broadcast interfaces are attached to; a private
address that reaches DELTA through a VPN or another tunnel is not on such a
subnet and is refused, and tunnel addresses are never advertised as a way in
either. The `Host` header must be localhost or an IP literal that passes the
same test — a name never qualifies — so the diary does not leave the network
even if the port is forwarded or a public name points at it.

A browser on another machine has to log in: pages served to LAN peers carry no
API token, and unlocking with the diary's encryption key issues a session
cookie that idles out after 30 days of disuse and dies with the process.
Failed logins are throttled, and the key crosses the network only during that
login — over plain HTTP, so log in on networks you trust. The config-changing
surfaces — revealing the key or token, regenerating the token, and changing
the storage, backups, or LAN settings — answer only from the machine DELTA
runs on, so even a leaked session or token cannot extract the key or repoint
the diary from the LAN.

CLI and MCP clients keep dialing the loopback `api_address` with the bearer
token; nothing changes for them.

## Colors

Settings → Colors overrides the pixel palette: one color per Total rating, and
twenty colors for the habit score, one per 5% bucket from 0–4.99% up to
95–100%. Habit pixels are those buckets rather than a continuous gradient. The
palette is stored in the diary, and reset-to-defaults restores the built-in
colors.

## Frontend development

Initialize a local diary, start `delta serve` on its default
`127.0.0.1:7331`, then run the Vite server:

```sh
cd web
npm run dev
```

Vite proxies `/api` to `http://127.0.0.1:7331`. Set `DELTA_API_TOKEN` to the
token from the local DELTA config so the dev proxy can authenticate API calls:

```sh
DELTA_API_TOKEN=<api-token> npm run dev
```

Set `DELTA_API_URL` as well to proxy a different local serve address, for
example `DELTA_API_URL=http://127.0.0.1:7444 DELTA_API_TOKEN=<api-token> npm run dev`.

## REST API contract

Entry `checkoffs` values are stable, stringified habit IDs, never habit names.
The API hides stored check-offs whose habit is outside its validity range for
the entry date, but never deletes those stored values. `PUT /api/entries/:date`
rejects `checkoffs`; use the dedicated check-off endpoints. Human-mode
`delta entry show` resolves visible IDs to names with one `/api/habits` request,
while `--json` preserves the IDs.

Habit `PATCH` replaces `ranges` before applying `archived`, so an archive in
the same request is evaluated against the new range set. `position` is clamped
at both edges: negative values move the habit to position `0`, and values at or
above the habit count move it to the last position.

## Habit CLI

With `delta serve` running, habits can be managed through the authenticated
HTTP API from the thin CLI client:

```sh
delta habit list --json
delta habit add "Read" --json
delta habit check "Read" 2026-08-02 --json
delta habit uncheck 2026-08-02 "Read" --json
delta habit archive "Read" --json
```

Habit check/uncheck/archive identifiers accept either a numeric habit ID or an
exact habit name. Check/uncheck dates default to the server's local today and
can also be supplied with `--date`.

## Backups

DELTA writes encrypted snapshots beside the live database in its `backups/`
directory. It keeps every snapshot; there is no automatic retention or
deletion. `backups_path` in `config.toml` overrides that default location and is
editable in Settings → Backups; the next snapshot uses it, no restart needed.

- The automatic daily snapshot is taken before the first mutation of the
  local calendar day, so `delta-YYYY-MM-DD.db` contains the previous day-end
  state. It is never overwritten.
- A manual snapshot is available from `POST /api/backup` and `delta backup`
  (or `delta backup --json`). Manual files always use
  `delta-YYYY-MM-DD-HHMMSS.db`, with `-N` added for same-second collisions.
- Before pending schema migrations, DELTA writes a
  `pre-migrate-vN-YYYYMMDD-HHMMSS.db` snapshot.

To restore, either adopt the snapshot where it lies or replace the live file.
Pointing Settings → Storage at the snapshot adopts it: DELTA checks that the
file opens with the configured key, repoints `config.toml` at it without copying
anything, and refuses diary writes until you restart — from that restart on, the
snapshot is the live diary and takes every new write. To keep the live path
instead, stop DELTA, copy the chosen snapshot over the live database file, and
start DELTA again. On a machine with no config yet, the open-existing setup flow
takes the snapshot path and the same encryption key.

## MCP

`delta mcp` is a native MCP server over stdin/stdout. Start `delta serve`
first; MCP reads the API address and bearer token from the same config as the
CLI and forwards every tool call to that running server. It never opens the
database directly, so the one-writer rule remains intact.

For an MCP client that accepts a `mcpServers` configuration, add:

```json
{
  "mcpServers": {
    "delta": {
      "command": "/absolute/path/to/delta",
      "args": ["mcp"]
    }
  }
}
```

The server exposes `entry_get`, `entry_set`, `entry_delete`, `entries_range`,
`habit_list`, `habit_add`, `habit_patch`, `habit_check`, `habit_uncheck`,
`habit_archive`, `grid`, `stats`, `search`, and `backup`. Dates are always
`YYYY-MM-DD`, and tool errors include the same stable `code` and human
`message` fields as the REST API. If `delta serve` is unavailable, tool calls
return a structured `server_unavailable` error pointing at `delta serve`.
