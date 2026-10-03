#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=scripts/install-ownership.sh
source "$ROOT/scripts/install-ownership.sh"

if [[ $(id -u) -ne 0 ]]; then
  echo "SKIP: ownership regression test requires root"
  exit 0
fi

operator=${PULSE_TEST_OPERATOR:-}
if [[ -z "$operator" ]]; then
  operator=$(getent passwd | awk -F: '$3 >= 1000 && $3 < 65534 && $7 !~ /(nologin|false)$/ {print $1; exit}')
fi
[[ -n "$operator" ]] || { echo "SKIP: no non-root operator account found"; exit 0; }

work=$(mktemp -d /tmp/pulse-ownership-test.XXXXXX)
cleanup() { find "$work" -depth -delete; }
trap cleanup EXIT
mkdir -p "$work/repo/.docker/tls" "$work/external"
chmod 0755 "$work" "$work/repo" "$work/external"
cp "$ROOT/.env.example" "$work/repo/.env"
touch "$work/repo/.docker/tls/pulse.crt" "$work/repo/.docker/tls/pulse.key"
touch "$work/repo/sentinel" "$work/repo/backup.tar.gz" "$work/external/external.crt" "$work/external/external.key"
chmod 0644 "$work/repo/sentinel"
chmod 0600 "$work/external/external.crt" "$work/external/external.key"

SUDO_USER=$operator
export SUDO_USER
pulse_resolve_operator
pulse_finalize_operator_files "$work/repo" "$work/repo/.env" \
  "$work/repo/.docker/tls/pulse.crt" "$work/repo/.docker/tls/pulse.key"
pulse_set_operator_path "$work/repo/backup.tar.gz" 0600

expected_owner="$PULSE_OPERATOR_UID:$PULSE_OPERATOR_GID"
check() {
  local path=$1 mode=$2 owner=$3
  [[ $(stat -c '%a' "$path") == "$mode" ]] || { echo "FAIL mode $path" >&2; exit 1; }
  [[ $(stat -c '%u:%g' "$path") == "$owner" ]] || { echo "FAIL owner $path" >&2; exit 1; }
}
check "$work/repo/.env" 600 "$expected_owner"
check "$work/repo/.docker" 700 "$expected_owner"
check "$work/repo/.docker/tls" 700 "$expected_owner"
check "$work/repo/.docker/tls/pulse.key" 600 "$expected_owner"
check "$work/repo/.docker/tls/pulse.crt" 644 "$expected_owner"
check "$work/repo/backup.tar.gz" 600 "$expected_owner"
check "$work/repo/sentinel" 644 "0:0"
check "$work/external/external.key" 600 "0:0"
check "$work/external/external.crt" 600 "0:0"
sudo -u "$operator" test -r "$work/repo/.env"
sudo -u "$operator" test -r "$work/repo/.docker/tls/pulse.key"
sudo -u "$operator" docker compose --project-directory "$ROOT" \
  --env-file "$work/repo/.env" -f "$ROOT/compose.yaml" config --quiet
if sudo -u "$operator" docker info >/dev/null 2>&1; then
  sudo -u "$operator" env COMPOSE_PROJECT_NAME=pulse-ownership-test docker compose \
    --project-directory "$ROOT" --env-file "$work/repo/.env" \
    -f "$ROOT/compose.yaml" ps >/dev/null
fi

unset SUDO_USER
pulse_resolve_operator
[[ "$PULSE_OPERATOR_USER" == root && "$PULSE_OPERATOR_UID:$PULSE_OPERATOR_GID" == 0:0 ]] || {
  echo "FAIL direct-root identity" >&2
  exit 1
}

echo "installer ownership regression: PASS (operator=$operator)"
