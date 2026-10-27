#!/bin/sh
# Hook agent installer.
#
# Downloads the agent from your Hook hub and runs it as an unprivileged
# systemd service. The agent only dials out to the hub and only reports
# metrics; it opens no ports and runs no commands from the hub.
#
#   curl -fsSL https://your-hub/install.sh | sudo sh -s -- --url https://your-hub --token hk_...
#   curl -fsSL https://your-hub/install.sh | sudo sh -s -- --uninstall
#
# Docker containers (optional, pick one):
#   --docker-host tcp://127.0.0.1:2375   read through a read-only socket proxy (recommended)
#   --docker                             add hook-agent to the docker group (root-equivalent)
#
# Multi-location confirmation (optional):
#   --probes   let the hub ask this server to re-check failing sites (public addresses only)
set -eu

URL=""
TOKEN=""
UNINSTALL=0
DOCKER_GROUP=0
DOCKER_HOST_VALUE=""
PROBES=0
while [ $# -gt 0 ]; do
	case "$1" in
	--url) URL="$2"; shift 2 ;;
	--token) TOKEN="$2"; shift 2 ;;
	--uninstall) UNINSTALL=1; shift ;;
	--docker) DOCKER_GROUP=1; shift ;;
	--docker-host) DOCKER_HOST_VALUE="$2"; shift 2 ;;
	--probes) PROBES=1; shift ;;
	*) echo "unknown option: $1" >&2; exit 2 ;;
	esac
done

[ "$(id -u)" -eq 0 ] || { echo "Please run as root (sudo)." >&2; exit 1; }
command -v systemctl >/dev/null 2>&1 || { echo "systemd is required." >&2; exit 1; }

BIN=/usr/local/bin/hook-agent
UNIT=/etc/systemd/system/hook-agent.service
ENVF=/etc/hook-agent.env

if [ "$UNINSTALL" -eq 1 ]; then
	systemctl disable --now hook-agent 2>/dev/null || true
	rm -f "$BIN" "$UNIT" "$ENVF"
	systemctl daemon-reload
	if id hook-agent >/dev/null 2>&1; then userdel hook-agent || true; fi
	echo "Hook agent removed."
	exit 0
fi

if [ -z "$URL" ] || [ -z "$TOKEN" ]; then
	echo "usage: install.sh --url https://your-hub --token hk_..." >&2
	exit 2
fi
case "$TOKEN" in hk_*) ;; *) echo "That doesn't look like a Hook agent token." >&2; exit 2 ;; esac

case "$(uname -m)" in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
*) echo "Unsupported CPU architecture: $(uname -m)" >&2; exit 1 ;;
esac

URL="${URL%/}"
TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT
echo "Downloading hook-agent ($ARCH) from $URL ..."
if command -v curl >/dev/null 2>&1; then
	curl -fsSL "$URL/download/hook-agent-linux-$ARCH" -o "$TMP"
else
	wget -qO "$TMP" "$URL/download/hook-agent-linux-$ARCH"
fi
install -m 0755 "$TMP" "$BIN"
"$BIN" -version >/dev/null || { echo "Downloaded agent doesn't run on this machine." >&2; exit 1; }

if ! id hook-agent >/dev/null 2>&1; then
	useradd --system --no-create-home --shell /usr/sbin/nologin hook-agent
fi

if [ "$DOCKER_GROUP" -eq 1 ]; then
	if getent group docker >/dev/null 2>&1; then
		usermod -aG docker hook-agent
		echo "Note: the docker group can control every container (root-equivalent)."
		echo "      A read-only socket proxy with --docker-host is the safer choice."
	else
		echo "No docker group on this server; skipping container monitoring." >&2
	fi
fi

umask 077
printf 'HOOK_URL=%s\nHOOK_TOKEN=%s\n' "$URL" "$TOKEN" >"$ENVF"
if [ -n "$DOCKER_HOST_VALUE" ]; then
	printf 'DOCKER_HOST=%s\n' "$DOCKER_HOST_VALUE" >>"$ENVF"
fi
if [ "$PROBES" -eq 1 ]; then
	printf 'HOOK_PROBES=1\n' >>"$ENVF"
fi
chmod 600 "$ENVF"

cat >"$UNIT" <<'UNIT'
[Unit]
Description=Hook monitoring agent
After=network-online.target
Wants=network-online.target

[Service]
User=hook-agent
Group=hook-agent
EnvironmentFile=/etc/hook-agent.env
ExecStart=/usr/local/bin/hook-agent
Restart=always
RestartSec=5

# Read-only view of the system; it needs nothing more.
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=read-only
PrivateTmp=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
RestrictNamespaces=true
LockPersonality=true
MemoryDenyWriteExecute=true
CapabilityBoundingSet=
MemoryMax=64M
CPUWeight=20

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable hook-agent >/dev/null 2>&1
systemctl restart hook-agent
echo "Hook agent installed and running. The server should show as online within a few seconds."
echo "Logs: journalctl -u hook-agent -f"
