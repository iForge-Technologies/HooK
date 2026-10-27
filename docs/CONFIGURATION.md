# Configuration

[← Back to README](../README.md) · [Documentation index](DOCUMENTATION.md)

Hook is configured with environment variables.

## Hub

| Variable | Default | Description |
|---|---|---|
| `HOOK_ADDR` | `:8080` | Listen address |
| `HOOK_DATA` | `./data` | Folder for the database and `secret.key` |
| `HOOK_PUBLIC_URL` | — | Used for links in alerts and install commands; `https://` turns on Secure cookies |
| `HOOK_SECURE_COOKIES` | from `HOOK_PUBLIC_URL` | Force the Secure flag on session cookies |
| `HOOK_TRUST_PROXY` | `false` | Read the client IP from `X-Forwarded-For` (set when behind Caddy) |
| `HOOK_SECRET` | generated | Base64 32-byte key; overrides `secret.key` |
| `HOOK_RAW_RETENTION_DAYS` | `30` | Days of raw check history |
| `HOOK_AGENT_DIR` | `<HOOK_DATA>/agents` | Agent binaries served to `install.sh` |
| `HOOK_VERCEL_API` / `HOOK_RENDER_API` | official APIs | Override the platform API base URL (egress proxy or testing) |
| `HOOK_DEADMAN_URL` | — | Heartbeat URL pinged while the hub is healthy |
| `HOOK_DEADMAN_FAIL_URL` | — | Pinged instead while the hub is running but unhealthy (for example `<url>/fail` on Healthchecks.io) |
| `HOOK_DEADMAN_EVERY` | `60` | Seconds between dead-man pings |
| `HOOK_PUSH_CONTACT` | `HOOK_PUBLIC_URL` | Email address or https URL that push services (Apple, Google, Mozilla) can use to reach you |

## Agent

The install script writes these to the agent's environment file. You can also set them by hand.

| Variable | Default | Description |
|---|---|---|
| `HOOK_URL` | — | The hub's address |
| `HOOK_TOKEN` | — | The server's agent token (`hk_…`) |
| `HOOK_PROBES` | `0` | `1` lets the hub send confirmation probes. The server still needs to be enabled as a location in Hook. |
| `HOOK_SPEEDTEST` | on | `0` makes the agent refuse speed tests |
| `HOOK_UNITS` | — | systemd units to include with `hook-agent -once` |
