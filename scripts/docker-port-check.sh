#!/usr/bin/env bash

# Emit only real host bindings. Ports present in Config.ExposedPorts or as a
# null entry in NetworkSettings.Ports deliberately produce no output.
pulse_container_host_bindings() {
  local container_id=$1
  [[ -n "$container_id" ]] || return 0
  docker inspect --format \
    '{{range $port, $bindings := .NetworkSettings.Ports}}{{range $bindings}}{{printf "%s\t%s\t%s\n" $port .HostIp .HostPort}}{{end}}{{end}}' \
    "$container_id"
}

pulse_port_is_published() {
  local bindings=$1 container_port=$2
  awk -F '\t' -v port="$container_port" '
    $1 == port { found = 1 }
    END { exit(found ? 0 : 1) }
  ' <<<"$bindings"
}

pulse_port_uses_only_host_port() {
  local bindings=$1 container_port=$2 expected_host_port=$3
  awk -F '\t' -v port="$container_port" -v expected="$expected_host_port" '
    $1 == port {
      found = 1
      if ($3 != expected) bad = 1
    }
    END { exit(found && !bad ? 0 : 1) }
  ' <<<"$bindings"
}

pulse_port_binding_summary() {
  local bindings=$1 container_port=$2
  awk -F '\t' -v port="$container_port" '
    $1 == port {
      host = ($2 == "" ? "*" : $2)
      if (host ~ /:/) host = "[" host "]"
      printf "%s%s:%s", (seen++ ? "," : ""), host, $3
    }
  ' <<<"$bindings"
}
