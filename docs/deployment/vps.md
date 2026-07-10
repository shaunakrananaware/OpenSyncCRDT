# Deploying on a VPS (systemd)

OpenSyncCRDT runs comfortably on a small VPS — the static binary has no runtime
dependencies and the default SQLite backend needs no external services. It runs
fine on a 512 MB VPS.

## One-line install

The [`install.sh`](../../deploy/scripts/install.sh) script downloads the correct
binary for the machine, installs it, and sets up a systemd service:

```bash
curl -fsSL https://raw.githubusercontent.com/shaunakrananaware/OpenSyncCRDT/main/deploy/scripts/install.sh | sudo bash
```

Pin a version with `OPENSYNCCRDT_VERSION`:

```bash
curl -fsSL .../install.sh | sudo OPENSYNCCRDT_VERSION=v1.2.3 bash
```

The installer:

1. Detects the platform (`linux`/`darwin`, `amd64`/`arm64`) and downloads the
   matching release binary to `/usr/local/bin/opensynccrdt`.
2. Creates an unprivileged `opensync` system user and a data directory at
   `/var/lib/opensynccrdt`.
3. Writes a default config at `/etc/opensynccrdt/config.yaml` and an environment
   file at `/etc/opensynccrdt/opensynccrdt.env` (only if they don't already
   exist).
4. Installs, enables, and starts the `opensynccrdt` systemd service, then prints
   its status.

The service listens on port 8080 (`ws://<host>:8080/sync`).

## Configuration

Two files drive configuration; environment variables win over the config file:

- `/etc/opensynccrdt/config.yaml` — the YAML config (see
  [configuration reference](../configuration.md) and
  [`config.example.yaml`](../../config.example.yaml)).
- `/etc/opensynccrdt/opensynccrdt.env` — environment overrides, loaded by the
  unit's `EnvironmentFile`. Put secrets (database URLs, signing keys, the
  management API key) here; it is created mode `0640`.

After editing either file:

```bash
sudo systemctl restart opensynccrdt
sudo systemctl status opensynccrdt
sudo journalctl -u opensynccrdt -f     # follow logs
```

## The systemd unit

A reference unit is in
[`deploy/systemd/opensynccrdt.service`](../../deploy/systemd/opensynccrdt.service).
It runs as the unprivileged `opensync` user, sets `DATA_DIR=/var/lib/opensynccrdt`,
restarts on failure, and applies hardening (`NoNewPrivileges`, `ProtectSystem=strict`,
`ProtectHome`, `PrivateTmp`, `PrivateDevices`, with `ReadWritePaths` limited to the
data directory).

## TLS

For a public deployment, terminate TLS one of two ways:

- **Reverse proxy** (recommended): put Nginx/Caddy in front, terminate TLS
  there, and proxy `wss://` → `ws://127.0.0.1:8080`. Remember to forward the
  `Upgrade`/`Connection` headers for WebSockets.
- **In the binary**: set `TLS_ENABLED=true`, `TLS_CERT_FILE`, and `TLS_KEY_FILE`
  in the env file.

## Upgrading

Re-run the installer (optionally with a new `OPENSYNCCRDT_VERSION`); it replaces
the binary and reloads the service. Your config and data are left untouched.

## Uninstall

```bash
sudo systemctl disable --now opensynccrdt
sudo rm /etc/systemd/system/opensynccrdt.service /usr/local/bin/opensynccrdt
sudo systemctl daemon-reload
# optionally remove data and config:
# sudo rm -rf /etc/opensynccrdt /var/lib/opensynccrdt && sudo userdel opensync
```
