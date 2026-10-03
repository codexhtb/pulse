#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${PULSE_E2E_USERNAME:-}" ]]; then
  if [[ ! -t 0 ]]; then
    echo "PULSE_E2E_USERNAME is required when stdin is not interactive" >&2
    exit 2
  fi
  read -r -p "Pulse E2E Admin username: " PULSE_E2E_USERNAME
  [[ -n "$PULSE_E2E_USERNAME" ]] || { echo "Admin username cannot be empty" >&2; exit 2; }
  export PULSE_E2E_USERNAME
fi

if [[ -z "${PULSE_E2E_PASSWORD:-}" ]]; then
  if [[ ! -t 0 ]]; then
    echo "PULSE_E2E_PASSWORD is required when stdin is not interactive" >&2
    exit 2
  fi
  read -r -s -p "Pulse E2E Admin password: " PULSE_E2E_PASSWORD
  printf '\n'
  export PULSE_E2E_PASSWORD
fi

export LD_LIBRARY_PATH="$PWD/.playwright-libs/root/usr/lib/x86_64-linux-gnu${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
exec "$PWD/node_modules/.bin/playwright" test "$@"
