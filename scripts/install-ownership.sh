#!/usr/bin/env bash

pulse_resolve_operator() {
  local candidate
  candidate=$(id -un)
  if [[ $(id -u) -eq 0 && -n ${SUDO_USER:-} && ${SUDO_USER} != root ]]; then
    candidate=$SUDO_USER
  fi
  id "$candidate" >/dev/null 2>&1 || {
    printf 'ERROR: invoking user %q does not exist\n' "$candidate" >&2
    return 1
  }
  PULSE_OPERATOR_USER=$candidate
  PULSE_OPERATOR_UID=$(id -u "$candidate")
  PULSE_OPERATOR_GID=$(id -g "$candidate")
  PULSE_OPERATOR_GROUP=$(id -gn "$candidate")
  export PULSE_OPERATOR_USER PULSE_OPERATOR_UID PULSE_OPERATOR_GID PULSE_OPERATOR_GROUP
}

pulse_path_is_within() {
  local parent path
  parent=$(realpath -m -- "$1")
  path=$(realpath -m -- "$2")
  [[ "$path" == "$parent"/* ]]
}

pulse_set_operator_path() {
  local path=$1 mode=$2 current_uid current_gid
  [[ -e "$path" ]] || return 0
  chmod "$mode" "$path"
  current_uid=$(stat -c '%u' "$path")
  current_gid=$(stat -c '%g' "$path")
  [[ "$current_uid" == "$PULSE_OPERATOR_UID" ]] || chown "$PULSE_OPERATOR_UID" "$path"
  [[ "$current_gid" == "$PULSE_OPERATOR_GID" ]] || chgrp "$PULSE_OPERATOR_GID" "$path"
}

pulse_finalize_operator_files() {
  local root=$1 env_file=$2 cert_file=$3 key_file=$4 path
  [[ -n ${PULSE_OPERATOR_UID:-} && -n ${PULSE_OPERATOR_GID:-} ]] || {
    printf 'ERROR: operator identity has not been resolved\n' >&2
    return 1
  }

  if [[ -e "$env_file" ]]; then
    pulse_set_operator_path "$env_file" 0600
  fi

  for path in "$root/.docker" "$root/.docker/tls"; do
    if [[ -d "$path" ]]; then
      pulse_set_operator_path "$path" 0700
    fi
  done

  if [[ -e "$key_file" ]] && pulse_path_is_within "$root/.docker" "$key_file"; then
    pulse_set_operator_path "$key_file" 0600
  fi
  if [[ -e "$cert_file" ]] && pulse_path_is_within "$root/.docker" "$cert_file"; then
    pulse_set_operator_path "$cert_file" 0644
  fi
}
