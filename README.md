# Hook

Private uptime monitoring for your websites, APIs, databases and servers. It's one Go binary with the dashboard built in and SQLite for storage.

This covers **Phases 0–6** of [PLAN.md](PLAN.md): the hub, sign-in with 2FA, uptime checks, alerts, the VPS agent with live server pages, the bento overview home page, integrations (Vercel, Render, databases, push monitors, Docker and systemd), multi-location confirmation, maintenance windows, public status pages, forecasts, and hardening (backups, a dead-man switch, browser push notifications and kiosk mode).

## What works today

- **Check types:**
  - **HTTP(S):** checks the status code and measures each step of the request (DNS, connect, TLS, server wait, download).
  - **Keyword:** the page must contain (or must not contain) some text.
  - **TCP port:** the port accepts connections.
  - **DNS:** a record resolves, optionally to an expected value, with an optional custom resolver.
  - **TLS certificate:** the certificate is valid and not about to expire.
- **Status logic:** each check is Up, Slow, Retrying or Down. A monitor only goes Down after the number of failed retries you set, and every outage is recorded as an incident.
- **Alerts:** Telegram, Discord, Slack, email (SMTP), ntfy, browser push, and a webhook signed with HMAC. You get messages for down, recovered, and certificates expiring in 14/7/3/1 days. Every channel has a "Send test" button.
- **Live dashboard:**
  - A heartbeat bar per monitor.
  - Response-time charts for 1h, 24h, 7d, 30d and 90d.
  - Uptime for 24h, 7d, 30d and 90d.
  - Incident history and a table of recent checks.
- **Security:**
  - Passwords hashed with argon2id, plus TOTP 2FA that rejects a reused code.
  - Login rate limiting.
  - Session cookies that are HttpOnly and SameSite=Strict, with CSRF protection.
  - A strict CSP.
  - Secrets (channel tokens, monitor headers) encrypted at rest with AES-256-GCM and never sent back to the browser.
- **Retention:** raw checks are kept 30 days and hourly summaries 2 years.

### Hardening (Phase 6)

- **Backups:** two layers, both covered in [Backups and restore](#backups-and-restore).
  - Litestream streams every change to S3 or R2 within about 10 seconds.
  - `hook-hub backup <dir>` writes a consistent snapshot plus the encryption key, for a nightly cron job.
- **Self-check:** `GET /healthz` returns 200 or 503 for load balancers and external monitors. It checks three things:
  - The database accepts writes.
  - The check scheduler has run in the last 10 minutes.
  - The data disk has more than 200 MB free and is under 98% full.
- **Dead-man switch:** the hub pings `HOOK_DEADMAN_URL` (a heartbeat from Healthchecks.io, Better Stack or Cronitor) every minute while healthy. If the hub dies, the pings stop and that service alerts you. When the hub is running but unhealthy, it pings `HOOK_DEADMAN_FAIL_URL` instead, if set. **Settings → Hub health** shows the checks and the last ping.
- **Browser push:** turn on notifications per device in **Settings → Push notifications**, then add a **Browser push** alert channel. Delivery is standard Web Push, encrypted per device and signed with the hub's own key, so no third-party account is needed.
  - It needs HTTPS (or localhost).
  - On iPhone and iPad, add Hook to the Home Screen first; iOS only allows push for installed web apps.
  - Hook installs as an app (manifest and icons). Its service worker caches nothing, so the dashboard never shows stale data offline.
- **Kiosk mode:** **Settings → Kiosk mode** creates a link to a full-screen, read-only board for a wall TV or spare tablet.
  - Problems sort first. Each monitor shows its last 30 checks, and each server shows CPU, memory and disk gauges, plus a large clock.
  - The page keeps the screen awake, hides the cursor and refreshes every 10 seconds.
  - The link works without signing in and returns names and states only, never URLs, hosts or IPs. Revoking it blanks the screen at its next refresh.

### Reliability and publishing (Phase 5)

- **Multi-location confirmation:** when a network monitor (HTTP, keyword, TCP, DNS, TLS) fails from the hub, up to three servers that opted in re-check it before it counts as down. The rule is that two locations must agree.
  - If every other location reaches the target, the check shows **Slow** with "Failed from the hub but reachable from …" and doesn't page you.
  - If any of them agrees, the down message names who confirmed it.
  - Opt-in happens on the server: install the agent with `--probes` (sets `HOOK_PROBES=1`). The hub can't turn it on remotely.
  - Agents only receive the check type and target: never headers, bodies or database credentials. Monitors with custom headers aren't confirmed remotely.
  - Agents refuse to probe private, loopback, link-local and CGNAT addresses. The check runs at connect time, so redirects and DNS tricks can't get around it. A compromised hub therefore can't use your servers to scan their own networks.
  - Refusals count as "no answer", never as confirmation. Turn confirmation off per monitor if you don't want it.
- **Maintenance windows** (one-off, daily or weekly, covering everything or chosen monitors and servers):
  - Covered monitors keep checking but show **Maintenance**.
  - Every alert for them is muted, server alerts included.
  - Maintenance time counts neither up nor down for uptime.
  - An outage that was open when the window started is closed.
  - Windows also appear on status pages.
- **Public status pages** at `/status/<address>`. You choose the monitors and their order, and response times are optional.
  - Visitors see an overall banner, 90 daily uptime bars per monitor (30 on phones), planned maintenance and 14 days of incidents.
  - The public API returns names, groups and states only: no targets, URLs, IDs or error messages.
  - Responses are cached for 30 seconds, and the page follows the visitor's light or dark preference.
- **Forecasts**, re-computed hourly:
  - A straight-line projection of the fullest disk from 7 days of hourly data. It gives a date only with at least a day of history, real growth (more than 0.05 points a day) and a good fit (R² ≥ 0.6). An alert fires once when the disk is projected to fill within 7 days, and clears quietly past 14 days.
  - A right-sizing verdict after a week of data: *oversized* (CPU peak under 25% and memory under 40%), *running tight* (95th-percentile CPU over 80% or memory over 90%), or *about right*.

### Integrations (Phase 4)

- **Push monitors** for cron jobs, backups and workers: the job calls `https://your-hub/api/push/<token>` when it finishes, and Hook alerts if no call arrives within the expected period plus 10% (at least 10 s). Periods range from 1 minute to 30 days.
  - Plain `curl` GET or POST works, with optional `?status=down&msg=…&ping=ms`.
  - The token is issued by the hub (a client can't choose it) and can be replaced from the monitor page.
- **Database checks:** PostgreSQL, MySQL/MariaDB, Redis and MongoDB. Each check opens a fresh connection, runs the cheapest possible query (`SELECT 1`, `SELECT VERSION()`, `PING`, a primary ping), reports the server version and latency, and closes the connection.
  - Connection strings are encrypted at rest and masked in the UI and API.
  - The displayed target has the credentials removed, and passwords are scrubbed from every error message.
  - Use a read-only user.
- **Vercel and Render** (the **Platforms** page): connect an account with an API token. Hook verifies the token before saving it, and never shows it again.
  - Every project or service is listed with its latest production deploy state.
  - **Watch deploys** creates a monitor that goes down when the latest production deploy fails or a service is suspended. The message points out that the previous deploy may still be serving.
  - **Monitor uptime** adds an HTTP check for the project's URL, preferring a verified custom domain.
  - Render free-plan services get an hourly check with a 90 s timeout. Any check wakes a sleeping free instance for 15 minutes, so checking more often would keep it awake and use up the free hours.
- **Docker containers** (through the agent): name, image, state, health, restarts, CPU and memory (page cache excluded), refreshed every 30 s.
  - Alerts when a container that was running stops, crash-loops or turns unhealthy, and when it comes back.
  - A separate alert when a running container crashed and restarted between snapshots.
  - Long-dead containers never trigger alerts.
- **systemd services** (through the agent): choose units to watch per server (for example `nginx postgresql`), and Hook alerts when one isn't active and again when it recovers.
  - Every failed unit on the server is also listed.
  - Unit names are validated and passed to `systemctl show` after `--`. The agent still only reads.

### Overview (Phase 3)

The home page is a bento built from the finance and analytics references, with one 24h / 7d / 30d period switch:

- **Uptime:** fleet uptime in the dot-matrix counter, the change from the previous period, and a stream band of average response time with red markers for buckets that had failed checks.
- **Monitors:** a half-gauge of ticks split by status (up, slow or retrying, down, paused).
- **Tiles:**
  - Mean time to recover.
  - Certificate runway for the soonest expiry.
  - The next certificates to expire.
  - Fleet CPU, memory and fullest disk as tick bars.
- **Network:** traffic in and out across all servers, drawn as mirrored waves.
- **Servers:** memory in use, each server's share of it, and per-server CPU sparklines.
- **Incidents:** capsule columns showing incidents per bucket across the period.
- **Memory by process:** a treemap of the top processes on every online server, coloured by server.
- **When incidents start:** a radar of incident start times by hour of day, in your time zone.
- **Recent incidents** for monitors and servers, and a compact heartbeat grid of every monitor.

Live numbers (status counts, server metrics) update through the event stream; history refreshes every 30 seconds.

### Servers (Phase 2)

- **Agent:** one static Go binary, about 8 MB on disk and about 15 MB of RAM. It runs as an unprivileged systemd service, only connects out to the hub, opens no ports, and never runs anything the hub sends.
- **Every 5 seconds:**
  - CPU used, I/O wait and steal (steal is time your VPS provider gave to other customers).
  - Load average, memory and swap.
  - Disk read/write and network in/out. Partitions and virtual interfaces are excluded so nothing is counted twice.
  - Established TCP connections, process count, and the fullest disk.
- **Every 30 seconds:**
  - Per-core CPU, mounts with inode usage, and network interfaces with errors.
  - Top processes by CPU and by memory. Only names and users are sent, never command lines.
  - Temperatures, and whether a reboot is required.
- **If the hub is unreachable,** the agent keeps up to an hour of data and sends it on reconnect, so short outages leave no gap. Old data that arrives late is stored but never triggers alerts.
- **Alerts:**
  - Offline after 60 seconds of silence. After a hub restart, servers get 90 seconds to reconnect before anything is reported.
  - CPU, memory or disk at or above a limit for N minutes.
  - Matching "back online" and "back to normal" messages. An alert only clears once the value has stayed 5 points below the limit for a minute, so a value hovering at the limit doesn't send repeated alerts.
- **Charts:** 1h, 6h, 24h, 7d and 30d. Raw data is kept 48 hours, per-minute summaries 14 days and hourly summaries 2 years. Summaries also keep the peak inside each bucket.

## Run locally

Requires Go 1.27+ and Node 24+.

```bash
cd web && npm install && npm run build && cd ..
```

```bash
go run ./cmd/hub
```

Open http://localhost:8080 and create the admin account. Data goes to `./data`. That folder holds the `secret.key` that encrypts stored tokens, so back it up with the database.

For frontend work with hot reload, run the hub with `make dev` and, in a second terminal, `cd web && npm run dev`. Then open http://localhost:5173 (Vite proxies `/api` to the hub).

## Deploy to a VPS

```bash
make linux
```

1. Copy `bin/hub-linux-amd64` to the server as `/usr/local/bin/hook-hub`.
2. Install [deploy/hook-hub.service](deploy/hook-hub.service) and set `HOOK_PUBLIC_URL`.
3. Put Caddy in front using [deploy/Caddyfile](deploy/Caddyfile). It handles HTTPS automatically.

Or build the container image:

```bash
docker build -t hook .
```

Run it with a volume mounted at `/data`.

### Connecting a server

1. Build the agents with `make agents` (or `make linux`).
2. Copy `bin/agents/*` to the hub's `HOOK_AGENT_DIR` (default `<HOOK_DATA>/agents`). The hub serves them to `install.sh` from there.
3. In Hook, go to **Servers → Add server** and run the command it shows, as root, on the VPS:

```bash
curl -fsSL https://your-hub/install.sh | sudo sh -s -- --url https://your-hub --token hk_...
```

The token is shown once and stored on the hub only as a hash. **Edit & alerts → New install command** replaces it, and the old agent is disconnected immediately. To remove the agent from a server:

```bash
curl -fsSL https://your-hub/install.sh | sudo sh -s -- --uninstall
```

To make the server a **probe location** for multi-location confirmation, add `--probes` to the install command.

**Docker containers** need the agent to read the Docker API. Pick one:

```bash
curl -fsSL https://your-hub/install.sh | sudo sh -s -- --url https://your-hub --token hk_... --docker-host tcp://127.0.0.1:2375
```

The command above points the agent at a read-only socket proxy (recommended), for example `tecnativa/docker-socket-proxy` with `CONTAINERS=1`. The alternative below adds the agent to the `docker` group, which is root-equivalent:

```bash
curl -fsSL https://your-hub/install.sh | sudo sh -s -- --url https://your-hub --token hk_... --docker
```

To check what the agent would send without connecting (set `HOOK_UNITS="nginx postgresql"` to include services):

```bash
hook-agent -once
```

Keep the hub off the servers it watches, and set `HOOK_DEADMAN_URL` so you hear about it when the hub itself goes down. You can also point an external monitor at `https://your-hook/healthz`.

## Configuration

| Variable | Default | |
|---|---|---|
| `HOOK_ADDR` | `:8080` | Listen address |
| `HOOK_DATA` | `./data` | Database and `secret.key` |
| `HOOK_PUBLIC_URL` | — | Used for links in alerts; `https://` turns on Secure cookies |
| `HOOK_TRUST_PROXY` | `false` | Read the client IP from `X-Forwarded-For` (set when behind Caddy) |
| `HOOK_SECRET` | generated | Base64 32-byte key; overrides `secret.key` |
| `HOOK_RAW_RETENTION_DAYS` | `30` | Days of raw check history |
| `HOOK_AGENT_DIR` | `<HOOK_DATA>/agents` | Agent binaries served to `install.sh` |
| `HOOK_VERCEL_API` / `HOOK_RENDER_API` | official APIs | Override the platform API base URL (egress proxy or testing) |
| `HOOK_DEADMAN_URL` | — | Heartbeat URL pinged while the hub is healthy |
| `HOOK_DEADMAN_FAIL_URL` | — | Pinged instead when the hub is running but unhealthy (for example `<url>/fail` on Healthchecks.io) |
| `HOOK_DEADMAN_EVERY` | `60` | Seconds between pings |
| `HOOK_PUSH_CONTACT` | `HOOK_PUBLIC_URL` | Email address or https URL that push services (Apple, Google, Mozilla) can use to reach you |

## Backups and restore

The database is `hook.db` in `HOOK_DATA`. Every stored secret (channel tokens, connection strings, 2FA secrets, the Web Push key) is encrypted with `secret.key` from the same folder, or with `HOOK_SECRET` if you set it. **A backup without the key can't decrypt any of them.** Keep a copy of the key somewhere other than the backup bucket.

**Continuous (Litestream):**

1. Install [Litestream](https://litestream.io).
2. Copy [deploy/litestream.yml](deploy/litestream.yml) to `/etc/litestream.yml` and set your bucket.
3. Put the credentials in `/etc/hook/litestream.env` (see [deploy/litestream.env.example](deploy/litestream.env.example)).
4. Install the drop-in [deploy/hook-hub.service.d/litestream.conf](deploy/hook-hub.service.d/litestream.conf), which makes systemd start the hub through `litestream replicate -exec`, so replication runs exactly while the hub does.

**Nightly snapshot:** add [deploy/backup-cron.example](deploy/backup-cron.example) to cron. It runs the following and keeps 14 days of copies:

```bash
hook-hub backup /var/backups/hook
```

The command writes `hook-<timestamp>.db` and `secret-<timestamp>.key` (mode 0600). It's safe to run while the hub is up.

**Restore:** stop the hub. Then put the database back, either with a snapshot copied to `/var/lib/hook/hook.db` or with this command:

```bash
litestream restore -config /etc/litestream.yml /var/lib/hook/hook.db
```

Next, put the matching key at `/var/lib/hook/secret.key` (or set `HOOK_SECRET`) and start the hub.

## Recovery

Locked out of 2FA? Run either command on the server:

```bash
hook-hub reset-2fa <username>
```

```bash
hook-hub set-password <username>
```

## Webhook payload

```json
{
  "event": "down",
  "title": "🔴 API is DOWN",
  "message": "…",
  "reason": "Connection refused",
  "time": "2026-10-27T09:30:00Z",
  "url": "https://hook.example.com/monitors/3",
  "monitor": { "id": 3, "name": "API", "type": "http", "target": "https://api.example.com" }
}
```

`event` is one of `down`, `up`, `cert`, `test`, `offline`, `online`, `threshold`, `recovered`, `container`, `container_ok`, `restart`, `service`, `service_ok` or `forecast`. Server events carry a `server` object instead of `monitor`. Container and service events add `subject` (the container or unit name), and threshold events add `metric` (`{name, value, peak, limit, minutes}`). Recovery events add `downtimeSeconds`, and certificate events add `certDaysLeft`. When a secret is set, the `X-Hook-Signature: sha256=<hex>` header is an HMAC-SHA256 of the raw body.
