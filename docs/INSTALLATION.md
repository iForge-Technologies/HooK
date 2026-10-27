# Installation

[← Back to README](../README.md) · [Documentation index](DOCUMENTATION.md)

- [Run locally](#run-locally)
- [Deploy to a VPS](#deploy-to-a-vps)
- [Run with Docker](#run-with-docker)
- [Connect a server](#connect-a-server)

## Run locally

Requires Go 1.27+ and Node 24+.

```bash
cd web && npm install && npm run build && cd ..
```

```bash
go run ./cmd/hub
```

Open http://localhost:8080 and create the admin account. Data goes to `./data`. That folder also holds `secret.key`, which encrypts stored tokens, so back it up with the database.

For frontend work with hot reload:

1. Run the hub with `make dev`.
2. In a second terminal, run `cd web && npm run dev`.
3. Open http://localhost:5173. Vite proxies `/api` to the hub.

## Deploy to a VPS

```bash
make linux
```

1. Copy `bin/hub-linux-amd64` to the server as `/usr/local/bin/hook-hub`.
2. Install [deploy/hook-hub.service](../deploy/hook-hub.service) and set `HOOK_PUBLIC_URL`.
3. Put Caddy in front using [deploy/Caddyfile](../deploy/Caddyfile). It handles HTTPS automatically.

Keep the hub off the servers it watches. Then set `HOOK_DEADMAN_URL` (see [Operations](OPERATIONS.md#dead-man-switch)) so you hear about it when the hub itself goes down.

## Run with Docker

```bash
docker build -t hook .
```

```bash
docker run -d -p 8080:8080 -v hook-data:/data hook
```

Behind a TLS proxy, set `HOOK_PUBLIC_URL=https://…` and `HOOK_TRUST_PROXY=true`.

## Connect a server

1. Build the agents with `make agents` (or `make linux`).
2. Copy `bin/agents/*` to the hub's `HOOK_AGENT_DIR` (default `<HOOK_DATA>/agents`). The hub serves them to `install.sh`.
3. In Hook, go to **Servers → Add server** and run the command it shows, as root, on the VPS:

```bash
curl -fsSL https://your-hub/install.sh | sudo sh -s -- --url https://your-hub --token hk_...
```

The token is shown once and stored on the hub only as a hash. **Edit & alerts → New install command** replaces it, and the old agent is disconnected immediately.

To remove the agent from a server:

```bash
curl -fsSL https://your-hub/install.sh | sudo sh -s -- --uninstall
```

### Probe locations

A server can re-check other monitors' failures from its own network (see [multi-location confirmation](FEATURES.md#reliability-and-publishing)). It takes two steps:

1. Add `--probes` to the install command on that server.
2. In Hook, open the server's **Edit & alerts** and turn on **Confirm other monitors' failures from this server**.

### Docker containers

The agent needs read access to the Docker API. The recommended way is a read-only socket proxy:

```bash
curl -fsSL https://your-hub/install.sh | sudo sh -s -- --url https://your-hub --token hk_... --docker-host tcp://127.0.0.1:2375
```

For example, run `tecnativa/docker-socket-proxy` with `CONTAINERS=1 IMAGES=1 VOLUMES=1 SYSTEM=1`. All four are read-only; the last three add images, volumes and disk usage.

The alternative below adds the agent to the `docker` group, which is root-equivalent:

```bash
curl -fsSL https://your-hub/install.sh | sudo sh -s -- --url https://your-hub --token hk_... --docker
```

### Dry run

To see what the agent would send without connecting (set `HOOK_UNITS="nginx postgresql"` to include services):

```bash
hook-agent -once
```
