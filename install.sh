#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ENV_FILE="$ROOT/.env"
cd "$ROOT"

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
info() { printf '==> %s\n' "$*"; }

[[ ${EUID:-$(id -u)} -eq 0 ]] || die "run this installer with sudo"
[[ -r /etc/os-release ]] || die "cannot identify this Linux distribution"
# shellcheck disable=SC1091
source /etc/os-release
[[ ${ID:-} == ubuntu ]] || die "supported platform is Ubuntu; detected ${ID:-unknown}"
case $(uname -m) in
  x86_64|aarch64) ;;
  *) die "unsupported architecture: $(uname -m)" ;;
esac

command -v docker >/dev/null 2>&1 || die "Docker Engine is required"
docker info >/dev/null 2>&1 || die "Docker daemon is unavailable"
docker compose version >/dev/null 2>&1 || die "Docker Compose v2 is required"
command -v openssl >/dev/null 2>&1 || die "openssl is required"
command -v curl >/dev/null 2>&1 || die "curl is required"
command -v ss >/dev/null 2>&1 || die "iproute2/ss is required"

available_kb=$(df -Pk "$ROOT" | awk 'NR==2 {print $4}')
(( available_kb >= 5 * 1024 * 1024 )) || die "at least 5 GiB of free disk space is required"

if [[ ! -f "$ENV_FILE" ]]; then
  info "creating .env"
  cp "$ROOT/.env.example" "$ENV_FILE"
  chmod 0600 "$ENV_FILE"
else
  info "preserving existing .env"
  chmod 0600 "$ENV_FILE"
fi

random_secret() { openssl rand -hex 32; }
replace_placeholder() {
  local key=$1 current tmp
  current=$(sed -n "s/^${key}=//p" "$ENV_FILE" | tail -n 1)
  [[ -n "$current" ]] || die "$key is missing from .env"
  [[ "$current" == GENERATE_ON_INSTALL ]] || return 0
  tmp=$(mktemp "$ROOT/.env.tmp.XXXXXX")
  awk -v key="$key" -v value="$(random_secret)" 'BEGIN{FS=OFS="="} $1==key {$0=key OFS value} {print}' "$ENV_FILE" > "$tmp"
  chmod 0600 "$tmp"
  mv "$tmp" "$ENV_FILE"
}
replace_placeholder CLICKHOUSE_ADMIN_PASSWORD
replace_placeholder CLICKHOUSE_API_PASSWORD
replace_placeholder CLICKHOUSE_INGEST_PASSWORD

set -a
# shellcheck disable=SC1090
source "$ENV_FILE"
set +a

[[ ${PULSE_WEB_PORT:-} =~ ^[0-9]+$ ]] && (( PULSE_WEB_PORT > 0 && PULSE_WEB_PORT < 65536 )) || die "invalid PULSE_WEB_PORT"
[[ ${PULSE_DNSTAP_PORT:-} =~ ^[0-9]+$ ]] && (( PULSE_DNSTAP_PORT > 0 && PULSE_DNSTAP_PORT < 65536 )) || die "invalid PULSE_DNSTAP_PORT"
valid_ipv4() {
  local ip=$1 part
  local -a parts
  [[ $ip =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || return 1
  IFS=. read -r -a parts <<<"$ip"
  for part in "${parts[@]}"; do (( 10#$part <= 255 )) || return 1; done
}
valid_ipv4 "${PULSE_BIND_ADDRESS:-}" || die "PULSE_BIND_ADDRESS must be an IPv4 address"
[[ ${PULSE_PUBLIC_NAME:-} =~ ^[A-Za-z0-9.-]+$ ]] || die "invalid PULSE_PUBLIC_NAME"
[[ ${PULSE_ADMIN_USERNAME:-} =~ ^[A-Za-z0-9._-]{1,64}$ ]] || die "invalid PULSE_ADMIN_USERNAME"

COMPOSE=(docker compose --project-directory "$ROOT" --env-file "$ENV_FILE" -f "$ROOT/compose.yaml")
compose() { "${COMPOSE[@]}" "$@"; }

owned_listener() {
  local service=$1 container_port=$2 host_port=$3 id binding expected
  id=$(compose ps -q "$service" 2>/dev/null || true)
  [[ -n "$id" && $(docker inspect --format '{{.State.Running}}' "$id" 2>/dev/null || true) == true ]] || return 1
  binding=$(compose port "$service" "$container_port" 2>/dev/null || true)
  expected="${PULSE_BIND_ADDRESS}:${host_port}"
  [[ "$binding" == "$expected" ]]
}
check_port() {
  local port=$1 service=$2 container_port=$3
  if ss -H -ltn "sport = :$port" | grep -q .; then
    owned_listener "$service" "$container_port" "$port" || die "host TCP port $port is already in use"
  fi
}
check_port "$PULSE_WEB_PORT" pulse-web 8443
check_port "$PULSE_DNSTAP_PORT" pulse-collector 6000

tls_dir="$ROOT/.docker/tls"
install -d -m 0700 "$tls_dir"
if [[ ! -s ${PULSE_TLS_CERT_FILE:-} || ! -s ${PULSE_TLS_KEY_FILE:-} ]]; then
  info "creating a self-signed TLS certificate"
  san="DNS:${PULSE_PUBLIC_NAME},DNS:localhost,IP:127.0.0.1"
  if [[ "$PULSE_BIND_ADDRESS" != 0.0.0.0 ]]; then
    san+=",IP:${PULSE_BIND_ADDRESS}"
  fi
  openssl req -x509 -newkey rsa:3072 -sha256 -days 825 -nodes \
    -subj "/CN=${PULSE_PUBLIC_NAME}" -addext "subjectAltName=${san}" \
    -keyout "$PULSE_TLS_KEY_FILE" -out "$PULSE_TLS_CERT_FILE" >/dev/null 2>&1
fi
chmod 0600 "$PULSE_TLS_KEY_FILE"
chmod 0644 "$PULSE_TLS_CERT_FILE"

info "validating Compose configuration"
compose config --quiet
info "pulling pinned infrastructure images"
compose pull clickhouse state-init pulse-migrate
info "building Pulse images"
compose build --pull pulse-api pulse-collector pulse-web

info "starting ClickHouse"
compose up -d clickhouse
source "$ROOT/scripts/lib.sh"
wait_healthy clickhouse 240

info "initializing persistent state permissions"
compose run --rm state-init
info "applying idempotent schema migrations"
compose run --rm pulse-migrate

if ! compose run --rm --no-deps --entrypoint /bin/sh pulse-api -ec 'test -s /var/lib/pulse-api/auth.json'; then
  [[ -t 0 ]] || die "first install requires an interactive terminal to create the Admin password"
  while true; do
    read -r -s -p "Initial Pulse Admin password: " admin_password; printf '\n'
    read -r -s -p "Repeat password: " admin_password_confirm; printf '\n'
    [[ ${#admin_password} -ge 12 ]] || { echo "Password must contain at least 12 characters" >&2; continue; }
    [[ "$admin_password" == "$admin_password_confirm" ]] || { echo "Passwords do not match" >&2; continue; }
    break
  done
  printf '%s\n' "$admin_password" | compose run --rm -T --no-deps pulse-api \
    -auth-state /var/lib/pulse-api/auth.json -bootstrap-admin "$PULSE_ADMIN_USERNAME"
  unset admin_password admin_password_confirm
else
  info "preserving existing Admin state"
fi

info "starting Pulse Core"
compose up -d
wait_healthy clickhouse 180
wait_healthy pulse-collector 180
wait_healthy pulse-api 180
wait_healthy pulse-web 180

info "running smoke checks"
smoke_host=$PULSE_BIND_ADDRESS
[[ "$smoke_host" == 0.0.0.0 ]] && smoke_host=127.0.0.1
curl --fail --silent --show-error --insecure "https://${smoke_host}:${PULSE_WEB_PORT}/healthz" >/dev/null
api_status=$(curl --silent --insecure --output /dev/null --write-out '%{http_code}' "https://${smoke_host}:${PULSE_WEB_PORT}/api/v1/health")
[[ "$api_status" == 401 ]] || die "unauthenticated API returned HTTP $api_status instead of 401"

cat <<EOF

Pulse installation completed.
Web:    https://${PULSE_BIND_ADDRESS}:${PULSE_WEB_PORT}
DNStap: ${PULSE_BIND_ADDRESS}:${PULSE_DNSTAP_PORT} (TCP)
Admin:  ${PULSE_ADMIN_USERNAME}

The generated certificate is self-signed. Trust $PULSE_TLS_CERT_FILE locally
or replace the certificate and key paths in .env with your production TLS files.
Configure each Unbound resolver to send CLIENT_QUERY and CLIENT_RESPONSE DNStap
messages to the DNStap endpoint. No Pulse agent is required for observability.
EOF
