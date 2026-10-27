# Features

[← Back to README](../README.md) · [Documentation index](DOCUMENTATION.md)

- [Checks](#checks)
- [Status and alerts](#status-and-alerts)
- [Monitor pages](#monitor-pages)
- [Overview](#overview)
- [Servers](#servers)
- [GPU, network and Docker detail](#gpu-network-and-docker-detail)
- [Integrations](#integrations)
- [Reliability and publishing](#reliability-and-publishing)
- [Hardening](#hardening)

## Checks

| Type | What it checks |
|---|---|
| **HTTP(S)** | The status code, plus timing for each step of the request: DNS, connect, TLS, server wait and download. |
| **Keyword** | The page contains (or doesn't contain) some text. |
| **TCP port** | The port accepts connections. |
| **DNS** | A record resolves, optionally to an expected value, optionally through a custom resolver. |
| **TLS certificate** | The certificate is valid and not about to expire. |
| **Databases** | PostgreSQL, MySQL/MariaDB, Redis and MongoDB answer the cheapest possible query. |
| **Push** | A cron job, backup or worker checked in on time. |
| **Vercel / Render** | The latest production deploy succeeded and the service isn't suspended. |

## Status and alerts

- **Status logic:** each check is Up, Slow, Retrying or Down. A monitor only goes Down after the number of failed retries you set, and every outage is recorded as an incident.
- **Channels:**
  - Telegram, Discord, Slack, email (SMTP), ntfy and browser push.
  - A webhook signed with HMAC (see [Webhooks](WEBHOOKS.md)).
  - Every channel has a **Send test** button.
- **Events:** down, recovered, certificates expiring in 14/7/3/1 days, and server events (offline, thresholds, containers, services, disk forecasts).
- **Flap limit:** a monitor sends at most 6 down/up alerts per 10 minutes. Further changes are still recorded but not sent, so a flapping check can't flood your channels.
- **Retention:** raw checks are kept 30 days, hourly summaries 2 years, and resolved incidents 1 year.

## Monitor pages

- A heartbeat bar per monitor.
- Response-time charts for 1h, 24h, 7d, 30d and 90d.
- Uptime for 24h, 7d, 30d and 90d.
- Incident history and a table of recent checks.
- A request breakdown (DNS, connect, TLS, server wait, download) for HTTP checks.

## Overview

The home page is a bento grid with one 24h / 7d / 30d period switch.

- **Uptime:**
  - Fleet uptime in a dot-matrix counter, with the change from the previous period.
  - A band showing average response time, with red markers for buckets that had failed checks.
- **Monitors:** a half-gauge of ticks split by status (up, slow or retrying, down, paused).
- **Tiles:**
  - Mean time to recover.
  - Certificate runway for the soonest expiry, and the next certificates to expire.
  - Fleet CPU, memory and fullest disk, as tick bars.
- **Network:** traffic in and out across all servers, drawn as mirrored waves.
- **Servers:** memory in use, each server's share of it, and per-server CPU sparklines.
- **Incidents:**
  - Capsule columns with incidents per bucket.
  - A radar of the hours of day when incidents start, in your time zone.
  - Recent incidents for monitors and servers.
- **Memory by process:** a treemap of the top processes on every online server.

Live numbers update through the event stream; history refreshes every 30 seconds.

## Servers

- **Agent:** one static Go binary, about 8 MB on disk and about 15 MB of RAM.
  - It runs as an unprivileged systemd service and only connects out to the hub.
  - It opens no ports and never runs anything the hub sends.
- **Every 5 seconds:**
  - CPU used, I/O wait and steal (time your VPS provider gave to other customers).
  - Load average, memory and swap.
  - Disk read/write and network in/out. Partitions and virtual interfaces are excluded, so nothing is counted twice.
  - Established TCP connections, process count and the fullest disk.
- **Every 30 seconds:**
  - Per-core CPU, mounts with inode usage, and network interfaces with errors.
  - Top processes by CPU and by memory. Only names and users are sent, never command lines.
  - Temperatures, and whether a reboot is required.
- **Offline buffering:** if the hub is unreachable, the agent keeps up to an hour of data and sends it on reconnect. Data that arrives late is stored but never triggers alerts.
- **Alerts:**
  - Offline after 60 seconds of silence. After a hub restart, servers get 90 seconds to reconnect first.
  - CPU, memory or disk at or above a limit for N minutes.
  - Matching "back online" and "back to normal" messages.
  - An alert clears only after the value stays 5 points below the limit for a minute, so a value hovering at the limit doesn't send repeated alerts.
- **Charts:**
  - Ranges: 1h, 6h, 24h, 7d and 30d.
  - Retention: raw data 48 hours, per-minute summaries 14 days, hourly summaries 2 years.
  - Summaries also keep the peak inside each bucket.
- **Forecasts**, recomputed hourly:
  - **Disk full:** a straight-line projection of the fullest disk from 7 days of hourly data. It gives a date only with at least a day of history, real growth and a good fit. It alerts once when the disk is projected to fill within 7 days.
  - **Right-sizing:** after a week of data, a verdict of *oversized*, *running tight* or *about right*.

## GPU, network and Docker detail

- **GPU:**
  - NVIDIA cards are read with `nvidia-smi`; AMD and Intel cards from sysfs.
  - Per device: utilisation, VRAM, temperature, power against its limit, fan, clock and driver, plus which processes use the GPU.
  - A server without a GPU says **No GPU**; virtual display adapters don't count as GPUs.
- **Traffic:**
  - Exact bytes moved on real interfaces, kept as daily totals.
  - The server page shows this billing cycle's in, out and total, plus 30 days of daily bars.
  - Set the provider's monthly allowance and reset day to see usage against it and a projection to the end of the cycle.
- **Interfaces:** link state, addresses, link speed, MTU, current rates, totals since boot and errors.
- **Speed tests:**
  - **Run speed test** on a server page measures that server's own download, upload, latency and jitter against Cloudflare's speed test endpoints.
  - Each test is capped at about 8 seconds and 250 MB per direction, with at most one test per 5 minutes.
  - Optional automatic tests run every 6, 12 or 24 hours, or weekly. Set `HOOK_SPEEDTEST=0` on a server to turn tests off there.
- **Docker:**
  - Per container: state, health, CPU, memory, restarts, network and disk I/O, process count, uptime, ports, networks, mounts and writable-layer size.
  - **Images** and **volumes** are listed, with unused and dangling ones flagged.
  - Totals cover images, container layers, volumes, build cache and what `docker system prune` could reclaim.

## Integrations

- **Push monitors** for cron jobs, backups and workers:
  - The job calls `https://your-hub/api/push/<token>` when it finishes. Hook alerts if no call arrives within the expected period plus 10%.
  - Plain `curl` GET or POST works, with optional `?status=down&msg=…&ping=ms`.
  - At most one push per 10 seconds is accepted per monitor.
  - The token is issued by the hub and can be replaced from the monitor page.
- **Database checks:**
  - Each check opens a fresh connection, runs the cheapest possible query, reports the server version and latency, and closes the connection.
  - A remote PostgreSQL or MySQL server must present a valid certificate (`sslmode=verify-full`, or `tls=true` for MySQL), so nobody posing as it can learn the password. **Allow unverified TLS** opts out for self-signed servers on a network you trust.
  - Connection strings are encrypted at rest, masked in the UI and API, and scrubbed from every error message. Use a read-only user.
- **Vercel and Render** (the **Platforms** page):
  - Connect an account with an API token; Hook verifies it before saving and never shows it again.
  - **Watch deploys** goes down when the latest production deploy fails or a service is suspended.
  - **Monitor uptime** adds an HTTP check for the project's URL.
- **Docker containers:**
  - Alerts when a running container stops, crash-loops or turns unhealthy, when it comes back, and when it restarts between snapshots.
  - Each server gets 20 new problems per 10 minutes; past that, one summary alert stands in for the rest.
- **systemd services:**
  - Choose units to watch per server (for example `nginx postgresql`); Hook alerts when one isn't active and when it recovers.
  - Every failed unit on the server is also listed.

## Reliability and publishing

- **Multi-location confirmation:** when a network monitor fails from the hub, up to three probe locations re-check it.
  - If at least two locations reach the target and none fail, the check shows **Slow** with "Failed from the hub but reachable from …" instead of paging you.
  - Otherwise the down message names the locations that confirmed it.
  - A server becomes a probe location only when both are true: its agent was installed with `--probes`, and you turned on **Confirm other monitors' failures** in its settings.
  - Agents receive only the check type and target. Monitors with custom headers, credentials in the URL or a query string are checked from the hub only.
  - Agents refuse to probe private, loopback, link-local and CGNAT addresses. The check runs at connect time, so redirects and DNS tricks can't get around it.
- **Maintenance windows** (one-off, daily or weekly; for everything or for chosen monitors and servers):
  - Covered monitors keep checking but show **Maintenance**, and every alert for them is muted.
  - Maintenance time counts neither up nor down for uptime.
  - Windows also appear on status pages.
- **Public status pages** at `/status/<address>`:
  - You choose the monitors and their order; response times are optional.
  - Visitors see an overall banner, 90 daily uptime bars per monitor, planned maintenance and 14 days of incidents.
  - The public API returns names, groups and states only: no targets, URLs, IDs or error messages.

## Hardening

- **Backups:** continuous replication with Litestream, plus `hook-hub backup` snapshots. See [Operations](OPERATIONS.md#backups-and-restore).
- **Self-check:** `GET /healthz` returns 200 or 503 for load balancers and external monitors.
- **Dead-man switch:** the hub pings a heartbeat URL every minute while healthy, so you hear about it when the hub itself goes down.
- **Browser push:**
  - Turn on notifications per device in **Settings → Push notifications**, then add a **Browser push** channel.
  - Delivery is standard Web Push signed with the hub's own key; no third-party account is needed.
  - It needs HTTPS (or localhost). On iPhone and iPad, add Hook to the Home Screen first.
- **Kiosk mode:**
  - **Settings → Kiosk mode** creates a link to a full-screen, read-only board for a wall TV.
  - Problems sort first. Each monitor shows its last 30 checks, and each server shows CPU, memory and disk gauges.
  - The page keeps the screen awake and refreshes every 10 seconds. The link returns names and states only.
