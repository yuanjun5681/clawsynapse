#!/usr/bin/env bash
#
# ClawSynapse installer for CLI and daemon service.
#
# Usage:
#   CLI only:
#     ./scripts/install.sh --cli
#     curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | bash -s -- --cli
#
#   CLI + daemon service (default):
#     ./scripts/install.sh --all
#     curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | \
#       bash -s -- --daemon
#
set -euo pipefail

CLI_BINARY="clawsynapse"
DAEMON_BINARY="clawsynapsed"
DEFAULT_REPO="yuanjun5681/clawsynapse"
DEFAULT_INSTALL_DIR="/usr/local/bin"
FALLBACK_INSTALL_DIR="${HOME}/.local/bin"
DEFAULT_CONFIG_ROOT="${HOME}/.clawsynapse"
DEFAULT_MANIFEST_PATH="${DEFAULT_CONFIG_ROOT}/manifest.json"
DEFAULT_DATA_DIR="${DEFAULT_CONFIG_ROOT}"
DEFAULT_NATS_SERVERS="nats://220.168.146.21:9414"
DEFAULT_LOCAL_API_ADDR="127.0.0.1:18080"
DEFAULT_LOG_LEVEL="info"
DEFAULT_LOG_FORMAT="json"
SYSTEMD_UNIT_NAME="clawsynapsed.service"
LAUNCHD_LABEL="io.github.yuanjun5681.clawsynapse.clawsynapsed"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m'

info()  { printf "${GREEN}[info]${NC}  %s\n" "$*"; }
warn()  { printf "${YELLOW}[warn]${NC}  %s\n" "$*"; }
error() { printf "${RED}[error]${NC} %s\n" "$*" >&2; exit 1; }

is_interactive_shell() {
    [ -t 1 ] && [ -r /dev/tty ]
}

usage() {
    cat <<'EOF'
ClawSynapse installer

Usage:
  ./scripts/install.sh [options]
  curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | bash -s -- [options]

Install modes:
  (default)            Install both CLI and daemon service
  --cli                Install CLI only
  --daemon             Install daemon binary and register the service
  --all                Install both CLI and daemon service

Common options:
  --install-dir DIR    Install binaries into DIR
  --version VERSION    Install a specific release tag instead of latest
  --repo OWNER/REPO    Override GitHub repo (default: yuanjun5681/clawsynapse)
  --uninstall          Uninstall the selected components
  --purge              With --daemon --uninstall, also remove config/data files
  --no-start           Install the daemon service but do not start it
  -h, --help           Show this help

Daemon bootstrap options (no Bot is created):
  --nats-servers URLS
  --local-api-addr ADDR
  --config PATH         Version 3 daemon JSON config (default: DATA_DIR/config.json)
  --data-dir PATH       Daemon state directory (default: ~/.clawsynapse)
  --log-level LEVEL
  --log-format FORMAT

Existing configs are not implicitly migrated. Version 2 layouts and legacy
single-Bot state must be migrated explicitly while the daemon is stopped.
Use --cli to install the migration command without starting a daemon.

Examples:
  Install CLI and daemon as a service:
    ./scripts/install.sh --daemon --nats-servers nats://127.0.0.1:4222
    curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | bash

  Install CLI only:
    ./scripts/install.sh --cli

  Install daemon only from GitHub Release:
    curl -fsSL https://raw.githubusercontent.com/yuanjun5681/clawsynapse/main/scripts/install.sh | \
      bash -s -- --daemon

  Uninstall the daemon service:
    ./scripts/install.sh --daemon --uninstall
EOF
}

trim() {
    local value="$1"
    value="${value#"${value%%[![:space:]]*}"}"
    value="${value%"${value##*[![:space:]]}"}"
    printf '%s' "$value"
}

json_escape() {
    local value="$1"
    value="${value//\\/\\\\}"
    value="${value//\"/\\\"}"
    value="${value//$'\n'/\\n}"
    value="${value//$'\r'/\\r}"
    value="${value//$'\t'/\\t}"
    printf '%s' "$value"
}

append_unique_path() {
    local current="$1"
    local candidate="$2"

    candidate="$(trim "$candidate")"
    if [ -z "$candidate" ]; then
        printf '%s' "$current"
        return 0
    fi

    case ":$current:" in
        *":$candidate:"*)
            printf '%s' "$current"
            ;;
        *)
            if [ -n "$current" ]; then
                printf '%s:%s' "$current" "$candidate"
            else
                printf '%s' "$candidate"
            fi
            ;;
    esac
}

detect_openclaw_bin() {
    local openclaw_path
    openclaw_path="$(command -v openclaw 2>/dev/null || true)"
    if [ -n "$openclaw_path" ]; then
        printf '%s\n' "$openclaw_path"
    fi
}

detect_openclaw_dir() {
    local openclaw_path
    openclaw_path="$(detect_openclaw_bin)"
    if [ -n "$openclaw_path" ]; then
        dirname "$openclaw_path"
    fi
}

detect_opencode_bin() {
    local opencode_path
    opencode_path="$(command -v opencode 2>/dev/null || true)"
    if [ -n "$opencode_path" ]; then
        printf '%s\n' "$opencode_path"
    fi
}

detect_opencode_dir() {
    local opencode_path
    opencode_path="$(detect_opencode_bin)"
    if [ -n "$opencode_path" ]; then
        dirname "$opencode_path"
    fi
}

build_service_path() {
    local path_value=""
    local candidate
    local openclaw_dir
    local opencode_dir
    local pi_bin omp_bin

    openclaw_dir="$(detect_openclaw_dir)"
    opencode_dir="$(detect_opencode_dir)"
    pi_bin="$(command -v pi 2>/dev/null || true)"
    omp_bin="$(command -v omp 2>/dev/null || true)"

    for candidate in \
        "$INSTALL_DIR" \
        "$HOME/.local/bin" \
        "$HOME/.npm-global/bin" \
        ${openclaw_dir:+"$openclaw_dir"} \
        ${opencode_dir:+"$opencode_dir"} \
        ${pi_bin:+"$(dirname "$pi_bin")"} \
        ${omp_bin:+"$(dirname "$omp_bin")"} \
        "$HOME/.bun/bin" \
        "/opt/homebrew/bin" \
        "/opt/homebrew/sbin" \
        "/usr/local/bin" \
        "/usr/local/sbin" \
        "/usr/bin" \
        "/bin" \
        "/usr/sbin" \
        "/sbin"
    do
        path_value="$(append_unique_path "$path_value" "$candidate")"
    done

    printf '%s\n' "$path_value"
}

expand_path() {
    local path="$1"

    if [ -z "$path" ]; then
        printf '%s' "$path"
        return 0
    fi

    case "$path" in
        "~")
            printf '%s\n' "$HOME"
            ;;
        "~/"*)
            printf '%s\n' "${HOME}/${path#~/}"
            ;;
        /*)
            printf '%s\n' "$path"
            ;;
        *)
            printf '%s\n' "${PWD}/${path}"
            ;;
    esac
}

prompt_value() {
    local label="$1"
    local current="${2:-}"
    local answer

    if [ -n "$current" ]; then
        printf "%s [%s]: " "$label" "$current" >&2
    else
        printf "%s: " "$label" >&2
    fi

    if [ -t 0 ]; then
        IFS= read -r answer || true
    else
        IFS= read -r answer </dev/tty || true
    fi
    answer="$(trim "$answer")"
    if [ -n "$answer" ]; then
        printf '%s' "$answer"
        return 0
    fi
    printf '%s' "$current"
}

reject_legacy_layout() {
    local root marker

    if [ "$ACTION" != "install" ] || [ "$INSTALL_DAEMON" -ne 1 ]; then
        return 0
    fi
    case "$CONFIG_PATH" in
        *.yaml|*.yml)
            legacy_migration_error "$CONFIG_PATH"
            ;;
    esac
    for root in "$CONFIG_ROOT" "$DATA_DIR"; do
        for marker in config.yaml config.yml identity.key identity.pub trust.json replay-cache.json peer-attrs.json; do
            if [ -e "${root}/${marker}" ] || [ -L "${root}/${marker}" ]; then
                legacy_migration_error "${root}/${marker}"
            fi
        done
    done
}

legacy_migration_error() {
    warn "legacy single-Bot configuration/state detected: $1"
    warn "nothing has been migrated or replaced; stop the old daemon and back up its configuration and data first"
    warn "install only the CLI with --cli, then run:"
    warn "  clawsynapse migrate --legacy-config /absolute/path/to/config.yaml --data-dir /absolute/path/to/daemon-root"
    error "explicit migration is required; rerun the daemon installer with --config <daemon-root>/config.json --data-dir <daemon-root> after migration"
}

maybe_prompt_daemon_config() {
    local existing_config_path

    if [ "$ACTION" != "install" ] || [ "$INSTALL_DAEMON" -ne 1 ]; then
        return 0
    fi
    if ! is_interactive_shell; then
        return 0
    fi
    existing_config_path="$(expand_path "$CONFIG_PATH")"
    if [ -f "$existing_config_path" ]; then
        info "existing daemon config detected, skipping interactive prompts: ${existing_config_path}"
        return 0
    fi
    info "interactive daemon configuration"
    info "reading answers from your terminal"
    NATS_SERVERS="$(prompt_value "NATS servers (comma-separated)" "$NATS_SERVERS")"
    LOCAL_API_ADDR="$(prompt_value "Local API address" "$LOCAL_API_ADDR")"
}

detect_platform() {
    local os arch

    case "$(uname -s)" in
        Darwin) os="darwin" ;;
        Linux)  os="linux" ;;
        *)      error "unsupported operating system: $(uname -s)" ;;
    esac

    case "$(uname -m)" in
        x86_64|amd64)  arch="amd64" ;;
        arm64|aarch64) arch="arm64" ;;
        *)             error "unsupported architecture: $(uname -m)" ;;
    esac

    printf '%s-%s\n' "$os" "$arch"
}

require_sudo() {
    if [ -n "${SUDO:-}" ]; then
        return 0
    fi
    if ! command -v sudo >/dev/null 2>&1; then
        error "this operation requires sudo, but sudo is not available"
    fi
    SUDO="sudo"
}

detect_install_dir() {
    if [ -n "${INSTALL_DIR:-}" ]; then
        INSTALL_DIR="$(expand_path "$INSTALL_DIR")"
        return 0
    fi

    if [ -w "$DEFAULT_INSTALL_DIR" ] || { [ ! -e "$DEFAULT_INSTALL_DIR" ] && [ -w "$(dirname "$DEFAULT_INSTALL_DIR")" ]; }; then
        INSTALL_DIR="$DEFAULT_INSTALL_DIR"
        return 0
    fi

    if command -v sudo >/dev/null 2>&1; then
        INSTALL_DIR="$DEFAULT_INSTALL_DIR"
        require_sudo
        return 0
    fi

    INSTALL_DIR="$FALLBACK_INSTALL_DIR"
}

ensure_install_dir() {
    local dir="$1"
    if [ -w "$(dirname "$dir")" ] || { [ -d "$dir" ] && [ -w "$dir" ]; }; then
        install -d -m 755 "$dir"
        return 0
    fi

    require_sudo
    ${SUDO} install -d -m 755 "$dir"
}

download_to() {
    local url="$1"
    local dst="$2"

    if command -v curl >/dev/null 2>&1; then
        curl -fsSL -o "$dst" "$url" || error "download failed: $url"
        return 0
    fi
    if command -v wget >/dev/null 2>&1; then
        wget -qO "$dst" "$url" || error "download failed: $url"
        return 0
    fi
    error "curl or wget is required"
}

find_local_artifact() {
    local binary="$1"
    local platform="$2"
    local candidate

    for candidate in "dist/${binary}-${platform}" "./dist/${binary}-${platform}"; do
        if [ -f "$candidate" ]; then
            printf '%s\n' "$candidate"
            return 0
        fi
    done

    return 1
}

install_binary() {
    local binary="$1"
    local platform="$2"
    local dest="${INSTALL_DIR}/${binary}"
    local src
    local tmpfile
    local url

    if src="$(find_local_artifact "$binary" "$platform")"; then
        info "installing ${binary} from local artifact: ${src}"
        if [ -n "${SUDO:-}" ]; then
            ${SUDO} install -m 755 "$src" "$dest"
        else
            install -m 755 "$src" "$dest"
        fi
        info "installed: ${dest}"
        return 0
    fi

    tmpfile="$(mktemp)"

    if [ "$VERSION" = "latest" ]; then
        url="https://github.com/${GITHUB_REPO}/releases/latest/download/${binary}-${platform}"
    else
        url="https://github.com/${GITHUB_REPO}/releases/download/${VERSION}/${binary}-${platform}"
    fi

    info "downloading ${binary}: ${url}"
    download_to "$url" "$tmpfile"

    if [ -n "${SUDO:-}" ]; then
        ${SUDO} install -m 755 "$tmpfile" "$dest"
    else
        install -m 755 "$tmpfile" "$dest"
    fi
    rm -f "$tmpfile"
    info "installed: ${dest}"
}


read_installed_version() {
    local binary="$1"
    local target="${INSTALL_DIR}/${binary}"

    if [ ! -x "$target" ]; then
        return 1
    fi
    "$target" version 2>/dev/null | tr -d '\r' | awk 'NR == 1 { print; exit }'
}

sync_manifest() {
    local manifest_path="$DEFAULT_MANIFEST_PATH"
    local cli_present=0
    local daemon_present=0
    local detected_version=""
    local service_manager_name=""
    local tmpfile

    if [ -x "${INSTALL_DIR}/${CLI_BINARY}" ]; then
        cli_present=1
        detected_version="$(read_installed_version "$CLI_BINARY" || true)"
    fi
    if [ -x "${INSTALL_DIR}/${DAEMON_BINARY}" ]; then
        daemon_present=1
        if [ -z "$detected_version" ]; then
            detected_version="$(read_installed_version "$DAEMON_BINARY" || true)"
        fi
        service_manager_name="$(service_manager)"
    fi

    if [ "$cli_present" -eq 0 ] && [ "$daemon_present" -eq 0 ]; then
        rm -f "$manifest_path"
        return 0
    fi

    install -d -m 700 "$DEFAULT_CONFIG_ROOT"
    tmpfile="$(mktemp)"
    {
        printf '{\n'
        printf '  "product": "clawsynapse",\n'
        printf '  "repo": "%s",\n' "$(json_escape "$GITHUB_REPO")"
        printf '  "version": "%s",\n' "$(json_escape "$detected_version")"
        printf '  "installDir": "%s",\n' "$(json_escape "$INSTALL_DIR")"
        printf '  "components": ['
        if [ "$cli_present" -eq 1 ]; then
            printf '"cli"'
        fi
        if [ "$daemon_present" -eq 1 ]; then
            if [ "$cli_present" -eq 1 ]; then
                printf ', '
            fi
            printf '"daemon"'
        fi
        printf ']'
        if [ -n "$service_manager_name" ]; then
            printf ',\n  "serviceManager": "%s"\n' "$(json_escape "$service_manager_name")"
        else
            printf '\n'
        fi
        printf '}\n'
    } >"$tmpfile"
    install -m 600 "$tmpfile" "$manifest_path"
    rm -f "$tmpfile"
}

initialize_daemon_config() {
    local platform="$1"
    local cli_path="${INSTALL_DIR}/${CLI_BINARY}"

    if [ -e "$CONFIG_PATH" ] || [ -L "$CONFIG_PATH" ]; then
        info "keeping existing daemon config: ${CONFIG_PATH}"
        return 0
    fi

    info "creating version 3 daemon config (no Bot or identity is created)"
    if [ "$INSTALL_CLI" -eq 1 ]; then
        "$cli_path" init --config "$CONFIG_PATH" --data-dir "$DATA_DIR" \
            --nats-servers "$NATS_SERVERS" --local-api-addr "$LOCAL_API_ADDR" \
            --log-level "$LOG_LEVEL" --log-format "$LOG_FORMAT" </dev/null
    else
        # Daemon-only installs still use the matching release's init contract.
        # The bootstrap CLI is temporary and is not added to the manifest.
        (
            local bootstrap_dir
            bootstrap_dir="$(mktemp -d)"
            trap 'rm -rf "$bootstrap_dir"' EXIT
            INSTALL_DIR="$bootstrap_dir"
            SUDO=""
            install_binary "$CLI_BINARY" "$platform"
            "${bootstrap_dir}/${CLI_BINARY}" init --config "$CONFIG_PATH" --data-dir "$DATA_DIR" \
                --nats-servers "$NATS_SERVERS" --local-api-addr "$LOCAL_API_ADDR" \
                --log-level "$LOG_LEVEL" --log-format "$LOG_FORMAT" </dev/null
        )
    fi
}

validate_daemon_config() {
    local daemon_path="${INSTALL_DIR}/${DAEMON_BINARY}"
    info "validating daemon config"
    "$daemon_path" --config "$CONFIG_PATH" --check-config >/dev/null
}

service_manager() {
    case "$(uname -s)" in
        Linux)
            command -v systemctl >/dev/null 2>&1 || error "systemctl is required to install the daemon service on Linux"
            printf '%s\n' "systemd"
            ;;
        Darwin)
            command -v launchctl >/dev/null 2>&1 || error "launchctl is required to install the daemon service on macOS"
            printf '%s\n' "launchd"
            ;;
        *)
            error "unsupported operating system: $(uname -s)"
            ;;
    esac
}

install_systemd_service() {
    local daemon_path="${INSTALL_DIR}/${DAEMON_BINARY}"
    local unit_path="/etc/systemd/system/${SYSTEMD_UNIT_NAME}"
    local log_dir="${DATA_DIR}/log"
    local log_path="${log_dir}/clawsynapsed.log"
    local service_path
    local tmpfile
    local service_user
    local service_group
    local was_active=0

    service_user="$(id -un)"
    service_group="$(id -gn)"
    service_path="$(build_service_path)"
    install -d -m 755 "$log_dir"
    require_sudo

    if ${SUDO} systemctl is-active --quiet "${SYSTEMD_UNIT_NAME}"; then
        was_active=1
    fi

    tmpfile="$(mktemp)"

    cat >"$tmpfile" <<EOF
[Unit]
Description=ClawSynapse Daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${service_user}
Group=${service_group}
Environment=HOME=${HOME}
Environment=PATH=${service_path}
WorkingDirectory=${HOME}
ExecStart=${daemon_path} --config ${CONFIG_PATH} --log-file-path ${log_path}
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF

    ${SUDO} install -m 644 "$tmpfile" "$unit_path"
    rm -f "$tmpfile"
    ${SUDO} systemctl daemon-reload
    if [ "$AUTO_START" -eq 1 ]; then
        ${SUDO} systemctl enable "${SYSTEMD_UNIT_NAME}"
        ${SUDO} systemctl restart "${SYSTEMD_UNIT_NAME}"
        if [ "$was_active" -eq 1 ]; then
            info "systemd service enabled and restarted: ${SYSTEMD_UNIT_NAME}"
        else
            info "systemd service enabled and started: ${SYSTEMD_UNIT_NAME}"
        fi
    else
        ${SUDO} systemctl enable "${SYSTEMD_UNIT_NAME}"
        info "systemd service enabled: ${SYSTEMD_UNIT_NAME}"
    fi
}

launchd_domain() {
    local uid
    uid="$(id -u)"
    if launchctl print "gui/${uid}" >/dev/null 2>&1; then
        printf 'gui/%s\n' "$uid"
        return 0
    fi
    printf 'user/%s\n' "$uid"
}

install_launchd_service() {
    local daemon_path="${INSTALL_DIR}/${DAEMON_BINARY}"
    local plist_dir="${HOME}/Library/LaunchAgents"
    local plist_path="${plist_dir}/${LAUNCHD_LABEL}.plist"
    local log_dir="${DATA_DIR}/log"
    local log_path="${log_dir}/clawsynapsed.log"
    local service_path
    local tmpfile
    local domain

    install -d -m 755 "$plist_dir"
    install -d -m 755 "$log_dir"
    service_path="$(build_service_path)"
    tmpfile="$(mktemp)"

    cat >"$tmpfile" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "https://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>${LAUNCHD_LABEL}</string>
  <key>ProgramArguments</key>
  <array>
    <string>${daemon_path}</string>
    <string>--config</string>
    <string>${CONFIG_PATH}</string>
    <string>--log-file-path</string>
    <string>${log_path}</string>
  </array>
  <key>WorkingDirectory</key>
  <string>${HOME}</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>${service_path}</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>${log_path}</string>
  <key>StandardErrorPath</key>
  <string>${log_path}</string>
</dict>
</plist>
EOF

    install -m 644 "$tmpfile" "$plist_path"
    rm -f "$tmpfile"
    domain="$(launchd_domain)"

    launchctl bootout "$domain" "$plist_path" >/dev/null 2>&1 || true
    if [ "$AUTO_START" -eq 1 ]; then
        launchctl bootstrap "$domain" "$plist_path"
        launchctl enable "${domain}/${LAUNCHD_LABEL}" >/dev/null 2>&1 || true
        launchctl kickstart -k "${domain}/${LAUNCHD_LABEL}" >/dev/null 2>&1 || true
        info "launchd service loaded and started: ${LAUNCHD_LABEL}"
    else
        info "launchd plist installed: ${plist_path}"
        info "start it manually with: launchctl bootstrap ${domain} ${plist_path}"
    fi
}

install_daemon_service() {
    case "$(service_manager)" in
        systemd)
            install_systemd_service
            ;;
        launchd)
            install_launchd_service
            ;;
    esac
}

uninstall_systemd_service() {
    local unit_path="/etc/systemd/system/${SYSTEMD_UNIT_NAME}"
    require_sudo
    ${SUDO} systemctl disable --now "${SYSTEMD_UNIT_NAME}" >/dev/null 2>&1 || true
    if [ -f "$unit_path" ]; then
        ${SUDO} rm -f "$unit_path"
    fi
    ${SUDO} systemctl daemon-reload
    info "removed systemd service: ${SYSTEMD_UNIT_NAME}"
}

uninstall_launchd_service() {
    local plist_path="${HOME}/Library/LaunchAgents/${LAUNCHD_LABEL}.plist"
    local domain
    domain="$(launchd_domain)"
    launchctl bootout "$domain" "$plist_path" >/dev/null 2>&1 || true
    rm -f "$plist_path"
    info "removed launchd service: ${LAUNCHD_LABEL}"
}

uninstall_daemon_service() {
    case "$(service_manager)" in
        systemd)
            uninstall_systemd_service
            ;;
        launchd)
            uninstall_launchd_service
            ;;
    esac
}

remove_binary_if_present() {
    local binary="$1"
    local target="${INSTALL_DIR}/${binary}"

    if [ ! -f "$target" ]; then
        warn "binary not found, skipping: ${target}"
        return 0
    fi

    if [ -w "$target" ] || [ -w "$(dirname "$target")" ]; then
        rm -f "$target"
    else
        require_sudo
        ${SUDO} rm -f "$target"
    fi
    info "removed binary: ${target}"
}

purge_daemon_state() {
    rm -rf "$CONFIG_ROOT"
    info "removed daemon state: ${CONFIG_ROOT}"
}

print_post_install_notes() {
    if [ "$INSTALL_CLI" -eq 1 ]; then
        info "CLI command available at: ${INSTALL_DIR}/${CLI_BINARY}"
        if [[ ":${PATH}:" != *":${INSTALL_DIR}:"* ]]; then
            warn "add ${INSTALL_DIR} to PATH to call ${CLI_BINARY} directly"
        fi
    fi

    if [ "$INSTALL_DAEMON" -eq 1 ]; then
        info "daemon config: ${CONFIG_PATH}"
        case "$(service_manager)" in
            systemd)
                info "manage service with: sudo systemctl status ${SYSTEMD_UNIT_NAME}"
                info "view logs with: sudo journalctl -u ${SYSTEMD_UNIT_NAME} -f"
                ;;
            launchd)
                info "manage service with: launchctl print $(launchd_domain)/${LAUNCHD_LABEL}"
                info "view logs in: ${DATA_DIR}/log/"
                ;;
        esac
        if [ "$INSTALL_CLI" -eq 1 ]; then
            info "daemon health: ${INSTALL_DIR}/${CLI_BINARY} --api-addr http://${LOCAL_API_ADDR} daemon"
            info "management is loopback-only and needs no token; any local process can manage this daemon"
            info "create Bots explicitly with: ${INSTALL_DIR}/${CLI_BINARY} bot create --file /path/to/bot.json"
        fi
    fi
}

parse_args() {
    while [ $# -gt 0 ]; do
        case "$1" in
            --cli)
                MODE="cli"
                ;;
            --daemon)
                MODE="daemon"
                ;;
            --all)
                MODE="all"
                ;;
            --install-dir)
                [ $# -ge 2 ] || error "missing value for --install-dir"
                INSTALL_DIR="$2"
                shift
                ;;
            --version)
                [ $# -ge 2 ] || error "missing value for --version"
                VERSION="$2"
                shift
                ;;
            --repo)
                [ $# -ge 2 ] || error "missing value for --repo"
                GITHUB_REPO="$2"
                shift
                ;;
            --uninstall)
                ACTION="uninstall"
                ;;
            --purge)
                PURGE=1
                ;;
            --no-start)
                AUTO_START=0
                ;;
            --nats-servers)
                [ $# -ge 2 ] || error "missing value for --nats-servers"
                NATS_SERVERS="$2"
                shift
                ;;
            --local-api-addr)
                [ $# -ge 2 ] || error "missing value for --local-api-addr"
                LOCAL_API_ADDR="$2"
                shift
                ;;
            --config)
                [ $# -ge 2 ] || error "missing value for --config"
                CONFIG_PATH="$2"
                shift
                ;;
            --data-dir)
                [ $# -ge 2 ] || error "missing value for --data-dir"
                DATA_DIR="$2"
                shift
                ;;
            --log-level)
                [ $# -ge 2 ] || error "missing value for --log-level"
                LOG_LEVEL="$2"
                shift
                ;;
            --log-format)
                [ $# -ge 2 ] || error "missing value for --log-format"
                LOG_FORMAT="$2"
                shift
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            --node-id|--trust-mode|--agent-adapter|--webhook-url|--deliverable-prefixes|--transfer-dir|--heartbeat|--announce-ttl|--transfer-max-file-size|--transfer-ttl)
                error "$1 is not a daemon installer option; configure a Bot explicitly with clawsynapse bot create --file <bot.json>, or use clawsynapse migrate for legacy state"
                ;;
            *)
                error "unknown argument: $1"
                ;;
        esac
        shift
    done
}

normalize_paths() {
    if [ -z "$CONFIG_PATH" ]; then
        CONFIG_PATH="$(expand_path "$DATA_DIR")/config.json"
    fi
    CONFIG_PATH="$(expand_path "$CONFIG_PATH")"
    CONFIG_ROOT="$(dirname "$CONFIG_PATH")"
    DATA_DIR="$(expand_path "$DATA_DIR")"

}

select_components() {
    case "$MODE" in
        cli)
            INSTALL_CLI=1
            INSTALL_DAEMON=0
            ;;
        daemon)
            INSTALL_CLI=0
            INSTALL_DAEMON=1
            ;;
        all)
            INSTALL_CLI=1
            INSTALL_DAEMON=1
            ;;
        *)
            error "invalid install mode: ${MODE}"
            ;;
    esac
}

install_selected() {
    local platform="$1"

    ensure_install_dir "$INSTALL_DIR"
    if [ "$INSTALL_CLI" -eq 1 ]; then
        install_binary "$CLI_BINARY" "$platform"
    fi
    if [ "$INSTALL_DAEMON" -eq 1 ]; then
        install_binary "$DAEMON_BINARY" "$platform"
        initialize_daemon_config "$platform"
        validate_daemon_config
        install_daemon_service
    fi
}

uninstall_selected() {
    if [ "$INSTALL_DAEMON" -eq 1 ]; then
        uninstall_daemon_service
        remove_binary_if_present "$DAEMON_BINARY"
        if [ "$PURGE" -eq 1 ]; then
            purge_daemon_state
        fi
    fi
    if [ "$INSTALL_CLI" -eq 1 ]; then
        remove_binary_if_present "$CLI_BINARY"
    fi
    sync_manifest
}

main() {
    MODE="all"
    ACTION="install"
    INSTALL_CLI=0
    INSTALL_DAEMON=0
    AUTO_START=1
    PURGE=0
    SUDO=""
    VERSION="${VERSION:-latest}"
    GITHUB_REPO="${GITHUB_REPO:-$DEFAULT_REPO}"
    INSTALL_DIR="${INSTALL_DIR:-}"
    CONFIG_PATH="${CONFIG_PATH:-}"
    DATA_DIR="${DATA_DIR:-$DEFAULT_DATA_DIR}"
    NATS_SERVERS="${NATS_SERVERS:-$DEFAULT_NATS_SERVERS}"
    LOCAL_API_ADDR="${LOCAL_API_ADDR:-$DEFAULT_LOCAL_API_ADDR}"
    LOG_LEVEL="${LOG_LEVEL:-$DEFAULT_LOG_LEVEL}"
    LOG_FORMAT="${LOG_FORMAT:-$DEFAULT_LOG_FORMAT}"

    parse_args "$@"
    select_components
    normalize_paths
    reject_legacy_layout
    maybe_prompt_daemon_config
    detect_install_dir

    info "ClawSynapse installer"
    info "install dir: ${INSTALL_DIR}"
    info "platform: $(detect_platform)"

    local _openclaw_bin _opencode_bin
    _openclaw_bin="$(detect_openclaw_bin)"
    if [ -n "$_openclaw_bin" ]; then
        info "detected openclaw: ${_openclaw_bin}"
    fi
    _opencode_bin="$(detect_opencode_bin)"
    if [ -n "$_opencode_bin" ]; then
        info "detected opencode: ${_opencode_bin}"
    fi

    if [ "$ACTION" = "uninstall" ]; then
        uninstall_selected
        exit 0
    fi

    install_selected "$(detect_platform)"
    sync_manifest
    print_post_install_notes
}

main "$@"
