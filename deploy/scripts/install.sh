#!/usr/bin/env bash
#
# OpenSyncCRDT VPS installer.
#
# Downloads the correct binary for this machine, installs it to
# /usr/local/bin/opensynccrdt, installs and enables the systemd service, and
# prints the service status.
#
#   curl -fsSL https://raw.githubusercontent.com/shaunakrananaware/OpenSyncCRDT/main/deploy/scripts/install.sh | sudo bash
#
# Override the version with OPENSYNCCRDT_VERSION=v1.2.3 (defaults to latest).
set -euo pipefail

REPO="shaunakrananaware/OpenSyncCRDT"
BINARY="opensynccrdt"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/opensynccrdt"
DATA_DIR="/var/lib/opensynccrdt"
SERVICE_USER="opensync"
VERSION="${OPENSYNCCRDT_VERSION:-latest}"

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
err()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || err "this installer must run as root (use sudo)"

# --- detect platform -------------------------------------------------------
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$os" in
  linux) ;;
  darwin) ;;
  *) err "unsupported OS: $os (this installer targets linux/darwin)" ;;
esac
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) err "unsupported architecture: $arch" ;;
esac
asset="${BINARY}-${os}-${arch}"
log "detected platform: ${os}/${arch}"

# --- resolve download URL --------------------------------------------------
if [ "$VERSION" = "latest" ]; then
  base="https://github.com/${REPO}/releases/latest/download"
else
  base="https://github.com/${REPO}/releases/download/${VERSION}"
fi
url="${base}/${asset}"

# --- download binary -------------------------------------------------------
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
log "downloading ${url}"
if command -v curl >/dev/null 2>&1; then
  curl -fSL "$url" -o "$tmp/$BINARY" || err "download failed"
elif command -v wget >/dev/null 2>&1; then
  wget -O "$tmp/$BINARY" "$url" || err "download failed"
else
  err "need curl or wget to download the binary"
fi
chmod +x "$tmp/$BINARY"

# --- install binary --------------------------------------------------------
log "installing to ${INSTALL_DIR}/${BINARY}"
install -m 0755 "$tmp/$BINARY" "${INSTALL_DIR}/${BINARY}"

# --- create user and directories ------------------------------------------
if ! id "$SERVICE_USER" >/dev/null 2>&1; then
  log "creating system user ${SERVICE_USER}"
  useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
fi
mkdir -p "$CONFIG_DIR" "$DATA_DIR"
chown "$SERVICE_USER:$SERVICE_USER" "$DATA_DIR"

# --- config + environment files (only if absent) --------------------------
if [ ! -f "${CONFIG_DIR}/config.yaml" ]; then
  log "writing default ${CONFIG_DIR}/config.yaml"
  cat > "${CONFIG_DIR}/config.yaml" <<'YAML'
# OpenSyncCRDT configuration. Environment variables (see opensynccrdt.env)
# override any value set here. See docs/configuration.md for every option.
server:
  host: 0.0.0.0
  port: 8080
  log_level: info
  log_format: json
storage:
  backend: sqlite
auth:
  mode: none
YAML
fi
if [ ! -f "${CONFIG_DIR}/opensynccrdt.env" ]; then
  log "writing default ${CONFIG_DIR}/opensynccrdt.env"
  cat > "${CONFIG_DIR}/opensynccrdt.env" <<'ENV'
# Environment overrides for OpenSyncCRDT. Uncomment and edit as needed.
# STORAGE_BACKEND=postgres
# STORAGE_URL=postgres://user:pass@host:5432/dbname
# AUTH_MODE=header
# MANAGEMENT_API_KEY=change-me
ENV
  chmod 0640 "${CONFIG_DIR}/opensynccrdt.env"
fi

# --- install systemd unit --------------------------------------------------
if command -v systemctl >/dev/null 2>&1; then
  log "installing systemd unit"
  cat > /etc/systemd/system/opensynccrdt.service <<UNIT
[Unit]
Description=OpenSyncCRDT local-first sync engine
Documentation=https://github.com/${REPO}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=-${CONFIG_DIR}/opensynccrdt.env
ExecStart=${INSTALL_DIR}/${BINARY} --config ${CONFIG_DIR}/config.yaml
Restart=on-failure
RestartSec=5s
User=${SERVICE_USER}
Group=${SERVICE_USER}
Environment=DATA_DIR=${DATA_DIR}
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=${DATA_DIR}

[Install]
WantedBy=multi-user.target
UNIT

  systemctl daemon-reload
  systemctl enable --now opensynccrdt.service
  log "service status:"
  systemctl --no-pager --full status opensynccrdt.service || true
else
  log "systemd not found; binary installed at ${INSTALL_DIR}/${BINARY}."
  log "run it manually: ${BINARY} --config ${CONFIG_DIR}/config.yaml"
fi

log "done. OpenSyncCRDT is listening on port 8080 (ws://<host>:8080/sync)."
