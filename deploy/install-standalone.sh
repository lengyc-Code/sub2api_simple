#!/usr/bin/env bash

set -euo pipefail

SERVICE_NAME="sub2api-standalone"
SERVICE_USER="sub2api"
INSTALL_DIR="/opt/sub2api-standalone"
CONFIG_DIR="/etc/sub2api-standalone"
DATA_DIR="/var/lib/sub2api-standalone"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"

BIN_PATH=""
SOURCE_PATH=""
CONFIG_TEMPLATE=""
FORCE_CONFIG=0

print_help() {
  cat <<'EOF'
Usage:
  install-standalone.sh [--source <repo_path>] [--binary <binary_path>] [--config-template <config_path>] [--force-config]

Options:
  --source <repo_path>        Build from source repository (expects main.go in repo root).
  --binary <binary_path>      Use an existing sub2api-standalone binary.
  --config-template <path>    Custom config template file to install as /etc/sub2api-standalone/config.json.
  --force-config              Overwrite /etc/sub2api-standalone/config.json even if it exists.
  -h, --help                  Show this help message.

Examples:
  sudo bash ./deploy/install-standalone.sh
  sudo bash ./deploy/install-standalone.sh --source /opt/sub2api_simple
  sudo bash ./deploy/install-standalone.sh --binary /tmp/sub2api-standalone
EOF
}

print_step() {
  echo "[install-standalone] $*"
}

fail() {
  echo "[install-standalone] ERROR: $*" >&2
  exit 1
}

require_root() {
  if [[ "$(id -u)" -ne 0 ]]; then
    fail "Please run as root (for example: sudo bash ./deploy/install-standalone.sh)."
  fi
}

detect_default_source() {
  local script_dir repo_root
  script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  repo_root="$(cd "${script_dir}/.." && pwd)"
  if [[ -f "${repo_root}/main.go" && -f "${repo_root}/go.mod" ]]; then
    SOURCE_PATH="${repo_root}"
  fi
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --source)
        SOURCE_PATH="${2:-}"
        shift 2
        ;;
      --binary)
        BIN_PATH="${2:-}"
        shift 2
        ;;
      --config-template)
        CONFIG_TEMPLATE="${2:-}"
        shift 2
        ;;
      --force-config)
        FORCE_CONFIG=1
        shift
        ;;
      -h|--help)
        print_help
        exit 0
        ;;
      *)
        fail "Unknown argument: $1"
        ;;
    esac
  done
}

ensure_prerequisites() {
  command -v systemctl >/dev/null 2>&1 || fail "systemctl not found. This script requires systemd."
}

build_from_source_if_needed() {
  if [[ -n "${BIN_PATH}" && -n "${SOURCE_PATH}" ]]; then
    fail "--binary and --source cannot be used together."
  fi

  if [[ -z "${BIN_PATH}" && -z "${SOURCE_PATH}" ]]; then
    detect_default_source
  fi

  if [[ -n "${SOURCE_PATH}" ]]; then
    [[ -d "${SOURCE_PATH}" ]] || fail "Source path does not exist: ${SOURCE_PATH}"
    [[ -f "${SOURCE_PATH}/main.go" ]] || fail "Invalid source path: ${SOURCE_PATH} (missing main.go)"
    command -v go >/dev/null 2>&1 || fail "Go is required to build from source. Please install Go first."

    print_step "Building standalone binary from source..."
    (cd "${SOURCE_PATH}" && mkdir -p output && go build -o output/sub2api_simple .)
    BIN_PATH="${SOURCE_PATH}/output/sub2api_simple"

    if [[ -z "${CONFIG_TEMPLATE}" && -f "${SOURCE_PATH}/config.example.json" ]]; then
      CONFIG_TEMPLATE="${SOURCE_PATH}/config.example.json"
    fi
  fi

  [[ -n "${BIN_PATH}" ]] || fail "No binary source specified. Use --source or --binary."
  [[ -f "${BIN_PATH}" ]] || fail "Binary not found: ${BIN_PATH}"
}

create_service_user() {
  if id "${SERVICE_USER}" >/dev/null 2>&1; then
    print_step "System user ${SERVICE_USER} already exists."
    return
  fi

  print_step "Creating system user ${SERVICE_USER}..."
  useradd --system --home "${DATA_DIR}" --shell /usr/sbin/nologin "${SERVICE_USER}"
}

install_binary() {
  print_step "Installing binary to ${INSTALL_DIR}..."
  mkdir -p "${INSTALL_DIR}"
  install -m 0755 "${BIN_PATH}" "${INSTALL_DIR}/${SERVICE_NAME}"
}

install_config() {
  local target_config
  target_config="${CONFIG_DIR}/config.json"

  print_step "Preparing config directory ${CONFIG_DIR}..."
  mkdir -p "${CONFIG_DIR}"

  if [[ -f "${target_config}" && "${FORCE_CONFIG}" -eq 0 ]]; then
    print_step "Existing config found at ${target_config}, keeping it."
  else
    if [[ -n "${CONFIG_TEMPLATE}" ]]; then
      [[ -f "${CONFIG_TEMPLATE}" ]] || fail "Config template not found: ${CONFIG_TEMPLATE}"
      print_step "Installing config from template ${CONFIG_TEMPLATE}..."
      install -m 0640 "${CONFIG_TEMPLATE}" "${target_config}"
    else
      print_step "Writing minimal config to ${target_config}..."
      cat > "${target_config}" <<'EOF'
{
  "listen_addr": "127.0.0.1:8080",
  "auth_tokens": [
    "replace-with-your-client-token"
  ],
  "accounts": [
    {
      "name": "openai-oauth-primary",
      "platform": "openai",
      "type": "oauth",
      "concurrency": 3,
      "priority": 10
    }
  ],
  "enable_request_log": true,
  "enable_stream_debug_log": false,
  "max_account_switches": 5,
  "sticky_session_ttl": "1h",
  "stream_read_timeout": "5m"
}
EOF
      chmod 0640 "${target_config}"
    fi
  fi
}

prepare_runtime_dirs() {
  print_step "Preparing runtime directory ${DATA_DIR}..."
  mkdir -p "${DATA_DIR}"
  chown -R "${SERVICE_USER}:${SERVICE_USER}" "${INSTALL_DIR}" "${CONFIG_DIR}" "${DATA_DIR}"
  chmod 0750 "${CONFIG_DIR}" "${DATA_DIR}"
}

install_service() {
  local script_dir template
  script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  template="${script_dir}/${SERVICE_NAME}.service"
  [[ -f "${template}" ]] || fail "Service template not found: ${template}"

  print_step "Installing systemd service ${SERVICE_NAME}..."
  install -m 0644 "${template}" "${SERVICE_FILE}"
  systemctl daemon-reload
  systemctl enable --now "${SERVICE_NAME}"
}

show_result() {
  cat <<EOF

Install completed.

Service:      ${SERVICE_NAME}
Binary:       ${INSTALL_DIR}/${SERVICE_NAME}
Config:       ${CONFIG_DIR}/config.json
Working dir:  ${DATA_DIR}

Useful commands:
  sudo systemctl status ${SERVICE_NAME}
  sudo journalctl -u ${SERVICE_NAME} -f
  sudo systemctl restart ${SERVICE_NAME}

EOF

  systemctl --no-pager --full status "${SERVICE_NAME}" || true
}

main() {
  require_root
  parse_args "$@"
  ensure_prerequisites
  build_from_source_if_needed
  create_service_user
  install_binary
  install_config
  prepare_runtime_dirs
  install_service
  show_result
}

main "$@"
