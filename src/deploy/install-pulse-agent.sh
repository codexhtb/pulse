#!/usr/bin/env bash

set -Eeuo pipefail
set +x
IFS=$'\n\t'

readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly AGENT_USER="pulse-agent"
readonly AGENT_GROUP="pulse-agent"
readonly CONFIG_DIR="/etc/pulse-agent"
readonly STATE_DIR="/var/lib/pulse-agent"
readonly AGENT_ENV="$CONFIG_DIR/agent.env"
readonly TOKEN_FILE="$CONFIG_DIR/api-token"
readonly HELPER_CONFIG="$CONFIG_DIR/helper.json"
readonly AGENT_BINARY="/opt/pulse/bin/pulse-agent"
readonly HELPER_BINARY="/usr/local/libexec/pulse-nft-helper"
readonly UNIT_FILE="/etc/systemd/system/pulse-agent.service"
readonly AGENT_SOURCE="$SCRIPT_DIR/pulse-agent"
readonly HELPER_SOURCE="$SCRIPT_DIR/pulse-nft-helper"
readonly UNIT_SOURCE="$SCRIPT_DIR/pulse-agent.service"
readonly SUDOERS_FILE="/etc/sudoers.d/pulse-agent"
readonly SUDOERS_SOURCE="$SCRIPT_DIR/sudoers-pulse-agent"
readonly CHECKSUM_FILE="$SCRIPT_DIR/SHA256SUMS"

CHECK_FAILURES=0

info() { printf 'INFO: %s\n' "$*"; }
pass() { printf 'PASS: %s\n' "$*"; }
warn() { printf 'WARN: %s\n' "$*" >&2; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

usage() {
    cat <<'EOF'
Usage:
  sudo ./install-pulse-agent.sh
  sudo ./install-pulse-agent.sh --check
  sudo ./install-pulse-agent.sh --uninstall

The install mode is interactive. Bearer tokens are read without echo and are
never accepted as command-line arguments.
EOF
}

verify_bundle() {
    local actual_layout expected_layout checksum_names payload owner_group mode
    expected_layout=$'SHA256SUMS\ninstall-pulse-agent.sh\npulse-agent\npulse-agent.service\npulse-nft-helper\nsudoers-pulse-agent'
    actual_layout=$(find "$SCRIPT_DIR" -mindepth 1 -maxdepth 1 -printf '%f\n' | LC_ALL=C sort)
    [[ "$actual_layout" == "$expected_layout" ]] || die "bundle directory must contain exactly the six documented files"
    owner_group=$(stat -c '%U:%G' "$SCRIPT_DIR")
    mode=$(stat -c '%a' "$SCRIPT_DIR")
    [[ "$owner_group" == "root:root" && $((8#$mode & 8#022)) -eq 0 ]] || \
        die "bundle directory must be root:root and not group/world writable"
    for payload in install-pulse-agent.sh pulse-agent pulse-nft-helper pulse-agent.service sudoers-pulse-agent SHA256SUMS; do
        [[ -f "$SCRIPT_DIR/$payload" && ! -L "$SCRIPT_DIR/$payload" ]] || die "bundle payload must be a regular non-symlink file: $payload"
        owner_group=$(stat -c '%U:%G' "$SCRIPT_DIR/$payload")
        mode=$(stat -c '%a' "$SCRIPT_DIR/$payload")
        [[ "$owner_group" == "root:root" && $((8#$mode & 8#022)) -eq 0 ]] || \
            die "bundle payload must be root:root and not group/world writable: $payload"
    done

    checksum_names=$(awk '
        NF != 2 || $1 !~ /^[0-9a-f]{64}$/ {exit 2}
        {name=$2; sub(/^\*/, "", name); print name}
    ' "$CHECKSUM_FILE") || die "SHA256SUMS has an invalid format"
    [[ $(printf '%s\n' "$checksum_names" | LC_ALL=C sort) == $'install-pulse-agent.sh\npulse-agent\npulse-agent.service\npulse-nft-helper\nsudoers-pulse-agent' ]] || \
        die "SHA256SUMS must cover exactly the five payload files"

    (
        cd "$SCRIPT_DIR"
        sha256sum --check --strict --quiet SHA256SUMS
    ) || die "bundle checksum verification failed"
    pass "bundle layout and SHA256SUMS verified"
}

require_root() {
    [[ ${EUID:-$(id -u)} -eq 0 ]] || die "run this command as root (use sudo)"
}

require_commands() {
    local command_name
    for command_name in "$@"; do
        command -v "$command_name" >/dev/null 2>&1 || die "required command is missing: $command_name"
    done
}

is_ipv4() {
    local value=$1 octet
    local -a octets

    [[ -n "$value" && "$value" != */* ]] || return 1
    [[ "$value" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1
    local IFS='.'
    read -r -a octets <<< "$value"
    [[ ${#octets[@]} -eq 4 ]] || return 1
    for octet in "${octets[@]}"; do
        [[ "$octet" =~ ^[0-9]+$ ]] || return 1
        [[ ${#octet} -eq 1 || ${octet:0:1} != 0 ]] || return 1
        (( 10#$octet >= 0 && 10#$octet <= 255 )) || return 1
    done
}

is_local_ipv4() {
    local expected=$1 current
    while read -r current; do
        [[ "$current" == "$expected" ]] && return 0
    done < <(ip -4 -o address show | awk '{split($4, address, "/"); print address[1]}')
    return 1
}

read_env_value() {
    local file=$1 key=$2
    [[ -r "$file" ]] || return 0
    awk -F= -v wanted="$key" '$1 == wanted {sub(/^[^=]*=/, ""); print; exit}' "$file"
}

read_helper_dns_ip() {
    [[ -r "$HELPER_CONFIG" ]] || return 0
    awk -F'"' '/"dns_ip"[[:space:]]*:/ {print $4; exit}' "$HELPER_CONFIG"
}

prompt_ipv4() {
    local __result_var=$1 label=$2 default_value=${3:-} value
    while true; do
        if [[ -n "$default_value" ]]; then
            printf '%s [%s]: ' "$label" "$default_value" >/dev/tty
        else
            printf '%s: ' "$label" >/dev/tty
        fi
        IFS= read -r value </dev/tty || die "unable to read $label"
        value=${value:-$default_value}
        if is_ipv4 "$value"; then
            printf -v "$__result_var" '%s' "$value"
            return
        fi
        printf 'Invalid IPv4 address. CIDR and IPv6 are not accepted.\n' >/dev/tty
    done
}

prompt_text() {
    local __result_var=$1 label=$2 default_value=${3:-} value
    if [[ -n "$default_value" ]]; then
        printf '%s [%s]: ' "$label" "$default_value" >/dev/tty
    else
        printf '%s: ' "$label" >/dev/tty
    fi
    IFS= read -r value </dev/tty || die "unable to read $label"
    printf -v "$__result_var" '%s' "${value:-$default_value}"
}

valid_token() {
    local token=$1
    (( ${#token} >= 32 && ${#token} <= 512 )) || return 1
    [[ "$token" =~ ^[A-Za-z0-9._~+/=-]+$ ]]
}

prompt_token() {
    local __result_var=$1 existing=${2:-} entered confirmation prompt
    while true; do
        if [[ -n "$existing" ]]; then
            prompt="Bearer token (Enter keeps the installed token): "
        else
            prompt="Bearer token: "
        fi
        printf '%s' "$prompt" >/dev/tty
        IFS= read -r -s entered </dev/tty || die "unable to read bearer token"
        printf '\n' >/dev/tty
        if [[ -z "$entered" && -n "$existing" ]]; then
            printf -v "$__result_var" '%s' "$existing"
            return
        fi
        if ! valid_token "$entered"; then
            printf 'Token must be 32-512 characters using letters, digits, or . _ ~ + / = -.\n' >/dev/tty
            continue
        fi
        printf 'Confirm bearer token: ' >/dev/tty
        IFS= read -r -s confirmation </dev/tty || die "unable to confirm bearer token"
        printf '\n' >/dev/tty
        if [[ "$entered" != "$confirmation" ]]; then
            printf 'Tokens do not match.\n' >/dev/tty
            entered=""
            confirmation=""
            continue
        fi
        printf -v "$__result_var" '%s' "$entered"
        entered=""
        confirmation=""
        return
    done
}

declare -a PROTECTED_IPS=()
declare -A PROTECTED_SEEN=()

add_protected_ip() {
    local address=$1
    is_ipv4 "$address" || die "invalid protected IPv4 address: $address"
    if [[ -z ${PROTECTED_SEEN[$address]+present} ]]; then
        PROTECTED_SEEN[$address]=1
        PROTECTED_IPS+=("$address")
    fi
}

add_protected_list() {
    local raw=$1 item
    local -a items
    raw=${raw//,/ }
    local IFS=$' \t'
    read -r -a items <<< "$raw"
    for item in "${items[@]}"; do
        [[ -n "$item" ]] || continue
        add_protected_ip "$item"
    done
}

join_by_comma() {
    local result="" item
    for item in "$@"; do
        if [[ -n "$result" ]]; then
            result+=","
        fi
        result+="$item"
    done
    printf '%s' "$result"
}

installed_defaults() {
    local listen callers candidate
    DEFAULT_MANAGEMENT_IP=""
    DEFAULT_DNS_IP=""
    DEFAULT_PULSE_IP=""
    DEFAULT_PROTECTED=""

    listen=$(read_env_value "$AGENT_ENV" PULSE_AGENT_LISTEN)
    if [[ "$listen" == *:9094 ]]; then
        candidate=${listen%:9094}
        is_ipv4 "$candidate" && DEFAULT_MANAGEMENT_IP=$candidate
    fi

    candidate=$(read_helper_dns_ip)
    is_ipv4 "$candidate" && DEFAULT_DNS_IP=$candidate

    callers=$(read_env_value "$AGENT_ENV" PULSE_AGENT_ALLOWED_CALLERS)
    local IFS=','
    for candidate in $callers; do
        if is_ipv4 "$candidate" && [[ "$candidate" != "127.0.0.1" && "$candidate" != "$DEFAULT_MANAGEMENT_IP" ]]; then
            DEFAULT_PULSE_IP=$candidate
        fi
    done
    DEFAULT_PROTECTED=$(read_env_value "$AGENT_ENV" PULSE_AGENT_PROTECTED_IPS)
}

existing_account_valid() {
    local passwd_entry group_name uid shell home sys_uid_max
    passwd_entry=$(getent passwd "$AGENT_USER") || return 0
    IFS=: read -r _ _ uid _ _ home shell <<< "$passwd_entry"
    group_name=$(id -gn "$AGENT_USER")
    sys_uid_max=$(awk '$1 == "SYS_UID_MAX" {print $2}' /etc/login.defs | tail -n1)
    sys_uid_max=${sys_uid_max:-999}
    (( uid <= sys_uid_max )) || return 1
    [[ "$group_name" == "$AGENT_GROUP" ]] || return 1
    [[ "$shell" == "/usr/sbin/nologin" || "$shell" == "/bin/false" ]] || return 1
    [[ "$home" == "$STATE_DIR" ]]
}

validate_existing_account() {
    existing_account_valid || die "$AGENT_USER exists with an unexpected UID, primary group, home, or login shell"
}

existing_group_valid() {
    local group_entry gid sys_gid_max
    group_entry=$(getent group "$AGENT_GROUP") || return 0
    IFS=: read -r _ _ gid _ <<< "$group_entry"
    sys_gid_max=$(awk '$1 == "SYS_GID_MAX" {print $2}' /etc/login.defs | tail -n1)
    sys_gid_max=${sys_gid_max:-999}
    (( gid <= sys_gid_max ))
}

validate_existing_group() {
    existing_group_valid || die "$AGENT_GROUP exists but is not a system group"
}

create_account_and_directories() {
    validate_existing_group
    if ! getent group "$AGENT_GROUP" >/dev/null; then
        groupadd --system "$AGENT_GROUP"
    fi
    if ! getent passwd "$AGENT_USER" >/dev/null; then
        useradd --system --gid "$AGENT_GROUP" --home-dir "$STATE_DIR" --shell /usr/sbin/nologin "$AGENT_USER"
    fi
    validate_existing_account

    install -d -o root -g "$AGENT_GROUP" -m 0750 "$CONFIG_DIR"
    install -d -o "$AGENT_USER" -g "$AGENT_GROUP" -m 0750 "$STATE_DIR"
    install -d -o root -g root -m 0755 /opt/pulse/bin /usr/local/libexec
}

atomic_install_content() {
    local destination=$1 owner=$2 group=$3 mode=$4 content=$5 temporary
    temporary=$(mktemp "${destination}.tmp.XXXXXX")
    chmod 0600 "$temporary"
    printf '%s' "$content" > "$temporary"
    chown "$owner:$group" "$temporary"
    chmod "$mode" "$temporary"
    mv -f -- "$temporary" "$destination"
}

atomic_install_file() {
    local source=$1 destination=$2 owner=$3 group=$4 mode=$5 temporary
    temporary=$(mktemp "${destination}.tmp.XXXXXX")
    install -o "$owner" -g "$group" -m "$mode" "$source" "$temporary"
    mv -f -- "$temporary" "$destination"
}

write_configuration() {
    local management_ip=$1 dns_ip=$2 pulse_ip=$3 protected_csv=$4 token=$5
    local env_content helper_content entry
    env_content=$(printf '%s\n' \
        "PULSE_AGENT_LISTEN=${management_ip}:9094" \
        "PULSE_AGENT_TOKEN_FILE=$TOKEN_FILE" \
        "PULSE_AGENT_ALLOWED_CALLERS=127.0.0.1,${management_ip},${pulse_ip}" \
        "PULSE_AGENT_PROTECTED_IPS=${protected_csv}")
    env_content+=$'\n'
    printf -v helper_content '{\n  "dns_ip": "%s",\n  "protected_ips": [\n' "$dns_ip"
    local index
    for (( index=0; index<${#PROTECTED_IPS[@]}; index++ )); do
        printf -v entry '    "%s"' "${PROTECTED_IPS[$index]}"
        if (( index + 1 < ${#PROTECTED_IPS[@]} )); then
            helper_content+="${entry},"$'\n'
        else
            helper_content+="${entry}"$'\n'
        fi
    done
    helper_content+=$'  ]\n}\n'

    atomic_install_content "$AGENT_ENV" root "$AGENT_GROUP" 0640 "$env_content"
    atomic_install_content "$HELPER_CONFIG" root root 0600 "$helper_content"
    atomic_install_content "$TOKEN_FILE" root "$AGENT_GROUP" 0640 "${token}"$'\n'
}

install_programs_and_policy() {
    info "installing prebuilt pulse-agent bundle"
    atomic_install_file "$AGENT_SOURCE" "$AGENT_BINARY" root root 0755
    atomic_install_file "$HELPER_SOURCE" "$HELPER_BINARY" root root 0755

    visudo -cf "$SUDOERS_SOURCE" >/dev/null
    atomic_install_file "$SUDOERS_SOURCE" "$SUDOERS_FILE" root root 0440
    visudo -cf "$SUDOERS_FILE" >/dev/null

    atomic_install_file "$UNIT_SOURCE" "$UNIT_FILE" root root 0644
    systemd-analyze verify "$UNIT_FILE" >/dev/null
}

check_result() {
    local description=$1
    shift
    if "$@"; then
        pass "$description"
    else
        printf 'FAIL: %s\n' "$description" >&2
        CHECK_FAILURES=$((CHECK_FAILURES + 1))
    fi
}

has_exact_mode_owner() {
    local path=$1 expected=$2
    [[ -e "$path" ]] || return 1
    [[ $(stat -c '%U:%G:%a' "$path") == "$expected" ]]
}

valid_installed_env() {
    local listen token_path callers protected address
    listen=$(read_env_value "$AGENT_ENV" PULSE_AGENT_LISTEN)
    token_path=$(read_env_value "$AGENT_ENV" PULSE_AGENT_TOKEN_FILE)
    callers=$(read_env_value "$AGENT_ENV" PULSE_AGENT_ALLOWED_CALLERS)
    protected=$(read_env_value "$AGENT_ENV" PULSE_AGENT_PROTECTED_IPS)
    [[ "$listen" == *:9094 ]] || return 1
    is_ipv4 "${listen%:9094}" || return 1
    [[ "$token_path" == "$TOKEN_FILE" ]] || return 1
    [[ -n "$callers" && -n "$protected" ]] || return 1
    local IFS=','
    for address in $callers; do is_ipv4 "$address" || return 1; done
    for address in $protected; do is_ipv4 "$address" || return 1; done
}

protected_contains_runtime_addresses() {
    local protected address gateway
    declare -A installed=()
    protected=$(read_env_value "$AGENT_ENV" PULSE_AGENT_PROTECTED_IPS)
    local IFS=','
    for address in $protected; do installed[$address]=1; done
    while read -r address; do
        [[ -n ${installed[$address]+present} ]] || return 1
    done < <(ip -4 -o address show | awk '{split($4, value, "/"); print value[1]}')
    while read -r gateway; do
        [[ -n ${installed[$gateway]+present} ]] || return 1
    done < <(ip -4 route show table all | awk '{for (i=1; i<=NF; i++) if ($i == "via") print $(i+1)}' | sort -u)
}

protected_contains_allowed_callers() {
    local protected callers address
    declare -A installed=()
    protected=$(read_env_value "$AGENT_ENV" PULSE_AGENT_PROTECTED_IPS)
    callers=$(read_env_value "$AGENT_ENV" PULSE_AGENT_ALLOWED_CALLERS)
    [[ -n "$protected" && -n "$callers" ]] || return 1
    local IFS=','
    for address in $protected; do installed[$address]=1; done
    for address in $callers; do
        [[ -n ${installed[$address]+present} ]] || return 1
    done
}

agent_and_helper_protected_lists_match() {
    local env_list json_list
    [[ -r "$AGENT_ENV" && -r "$HELPER_CONFIG" ]] || return 1
    env_list=$(read_env_value "$AGENT_ENV" PULSE_AGENT_PROTECTED_IPS | tr ',' '\n' | sort -u)
    json_list=$(awk -F'"' '/^[[:space:]]*"[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+"/ {print $2}' "$HELPER_CONFIG" | sort -u)
    [[ -n "$env_list" && "$env_list" == "$json_list" ]]
}

token_file_valid() {
    local token
    [[ -r "$TOKEN_FILE" ]] || return 1
    token=$(tr -d '\r\n' < "$TOKEN_FILE")
    valid_token "$token"
}

state_permissions_ok() {
    local path owner group mode
    [[ -d "$STATE_DIR" ]] || return 1
    while IFS= read -r -d '' path; do
        [[ -f "$path" && ! -L "$path" ]] || return 1
        owner=$(stat -c '%U' "$path")
        group=$(stat -c '%G' "$path")
        mode=$(stat -c '%a' "$path")
        [[ "$owner" == "$AGENT_USER" && "$group" == "$AGENT_GROUP" ]] || return 1
        (( (8#$mode & 8#077) == 0 )) || return 1
    done < <(find "$STATE_DIR" -mindepth 1 -maxdepth 1 -print0)
}

agent_process_is_unprivileged() {
    local pid process_uid expected_uid effective_caps
    pid=$(systemctl show pulse-agent.service -p MainPID --value 2>/dev/null)
    [[ "$pid" =~ ^[0-9]+$ && "$pid" -gt 1 ]] || return 1
    process_uid=$(awk '/^Uid:/ {print $2}' "/proc/$pid/status")
    expected_uid=$(id -u "$AGENT_USER")
    effective_caps=$(awk '/^CapEff:/ {print $2}' "/proc/$pid/status")
    [[ "$process_uid" == "$expected_uid" && "$effective_caps" == "0000000000000000" ]]
}

agent_health_ok() {
    local listen management_ip token response
    [[ -r "$AGENT_ENV" && -r "$TOKEN_FILE" ]] || return 1
    listen=$(read_env_value "$AGENT_ENV" PULSE_AGENT_LISTEN)
    management_ip=${listen%:9094}
    token=$(tr -d '\r\n' < "$TOKEN_FILE")
    response=$(printf 'header = "Authorization: Bearer %s"\n' "$token" | \
        curl --config - --noproxy '*' --fail --silent --show-error --max-time 5 "http://${management_ip}:9094/health" 2>/dev/null) || return 1
    token=""
    [[ "$response" == *'"service":"pulse-agent"'* && "$response" == *'"status":"ok"'* && "$response" == *'"mode":"enforcing"'* ]]
}

bind_is_correct() {
    local listen
    listen=$(read_env_value "$AGENT_ENV" PULSE_AGENT_LISTEN)
    ss -lntH | awk -v expected="$listen" '$4 == expected {found=1} END {exit !found}'
}

helper_status_ok() {
    runuser -u "$AGENT_USER" -- sudo -n "$HELPER_BINARY" status >/dev/null 2>&1
}

wait_for_agent() {
    local attempt
    for (( attempt=1; attempt<=20; attempt++ )); do
        if systemctl is-active --quiet pulse-agent.service && agent_health_ok && helper_status_ok; then
            return 0
        fi
        sleep 1
    done
    return 1
}

nftables_shape_ok() {
    [[ -x "$HELPER_SOURCE" && -r "$HELPER_CONFIG" ]] || return 1
    "$HELPER_SOURCE" status >/dev/null 2>&1
}

blocked_set_empty() {
    local set_output
    set_output=$(nft -nn list set inet pulse blocked_dns_v4 2>/dev/null) || return 1
    if [[ "$set_output" != *"elements ="* ]]; then
        return 0
    fi
    grep -Eq 'elements = \{[[:space:]]*\}' <<< "$set_output"
}

run_checks() {
    local account_ok=1 group_ok=1
    CHECK_FAILURES=0

    getent passwd "$AGENT_USER" >/dev/null || account_ok=0
    getent group "$AGENT_GROUP" >/dev/null || group_ok=0
    check_result "system user $AGENT_USER exists" test "$account_ok" -eq 1
    check_result "system group $AGENT_GROUP exists" test "$group_ok" -eq 1
    if (( group_ok == 1 )); then
        check_result "agent group is a system group" existing_group_valid
    fi
    if (( account_ok == 1 )); then
        check_result "agent account is non-login and structurally valid" existing_account_valid
    fi

    check_result "pulse-agent binary owner/mode" has_exact_mode_owner "$AGENT_BINARY" "root:root:755"
    check_result "pulse-nft-helper binary owner/mode" has_exact_mode_owner "$HELPER_BINARY" "root:root:755"
    check_result "installed pulse-agent matches bundle" cmp -s "$AGENT_SOURCE" "$AGENT_BINARY"
    check_result "installed pulse-nft-helper matches bundle" cmp -s "$HELPER_SOURCE" "$HELPER_BINARY"
    check_result "configuration directory owner/mode" has_exact_mode_owner "$CONFIG_DIR" "root:${AGENT_GROUP}:750"
    check_result "state directory owner/mode" has_exact_mode_owner "$STATE_DIR" "${AGENT_USER}:${AGENT_GROUP}:750"
    check_result "state files are agent-owned and not group/world accessible" state_permissions_ok
    check_result "agent.env owner/mode" has_exact_mode_owner "$AGENT_ENV" "root:${AGENT_GROUP}:640"
    check_result "helper.json owner/mode" has_exact_mode_owner "$HELPER_CONFIG" "root:root:600"
    check_result "token owner/mode" has_exact_mode_owner "$TOKEN_FILE" "root:${AGENT_GROUP}:640"
    check_result "token format and minimum length" token_file_valid
    check_result "agent.env contains valid individual IPv4 values" valid_installed_env
    check_result "all local IPv4 and route next-hops are protected" protected_contains_runtime_addresses
    check_result "every allowed caller is also protected" protected_contains_allowed_callers
    check_result "agent and helper protected-IP lists match" agent_and_helper_protected_lists_match
    check_result "management IP is assigned locally" is_local_ipv4 "$(read_env_value "$AGENT_ENV" PULSE_AGENT_LISTEN | sed 's/:9094$//')"
    check_result "DNS service IP is assigned locally" is_local_ipv4 "$(read_helper_dns_ip)"

    check_result "installed systemd unit matches repository" cmp -s "$UNIT_SOURCE" "$UNIT_FILE"
    check_result "systemd unit verifies" systemd-analyze verify "$UNIT_FILE"
    check_result "installed sudoers matches repository" cmp -s "$SUDOERS_SOURCE" "$SUDOERS_FILE"
    check_result "sudoers validates" visudo -cf "$SUDOERS_FILE"
    check_result "pulse-agent service is enabled" systemctl is-enabled --quiet pulse-agent.service
    check_result "pulse-agent service is active" systemctl is-active --quiet pulse-agent.service
    check_result "agent process is non-root with zero effective capabilities" agent_process_is_unprivileged
    check_result "agent listens on configured management IP:9094" bind_is_correct
    check_result "authenticated /health reports enforcing and ok" agent_health_ok
    check_result "narrow sudo helper status works" helper_status_ok
    check_result "nftables table has the expected Pulse-only shape" nftables_shape_ok

    if (( CHECK_FAILURES > 0 )); then
        printf 'CHECK RESULT: FAIL (%d checks failed)\n' "$CHECK_FAILURES" >&2
        return 1
    fi
    printf 'CHECK RESULT: PASS\n'
}

install_agent() {
    local management_ip dns_ip pulse_ip additional_protected existing_token token protected_csv
    local first_install=0 address

    require_commands awk cmp curl find getent groupadd install ip mktemp mv nft runuser sed sha256sum sort ss stat sudo systemctl systemd-analyze tr useradd visudo
    [[ -t 0 || -r /dev/tty ]] || die "install mode requires an interactive terminal"
    [[ -f "$UNIT_SOURCE" && -f "$SUDOERS_SOURCE" && -x "$AGENT_SOURCE" && -x "$HELPER_SOURCE" ]] || \
        die "bundle payload is incomplete or its binaries are not executable"
    [[ -x /usr/bin/sudo && -x /usr/sbin/nft ]] || die "the existing agent/helper contract requires /usr/bin/sudo and /usr/sbin/nft"

    if [[ ! -e "$UNIT_FILE" && ! -e "$TOKEN_FILE" && ! -e "$STATE_DIR/blocks.json" ]]; then
        first_install=1
    fi

    installed_defaults
    prompt_ipv4 management_ip "DNS management IPv4" "$DEFAULT_MANAGEMENT_IP"
    prompt_ipv4 dns_ip "DNS service IPv4" "$DEFAULT_DNS_IP"
    prompt_ipv4 pulse_ip "Pulse server IPv4" "$DEFAULT_PULSE_IP"
    prompt_text additional_protected "Additional protected IPv4 (comma/space separated; empty is allowed)" "$DEFAULT_PROTECTED"

    is_local_ipv4 "$management_ip" || die "management IP is not assigned to this server: $management_ip"
    is_local_ipv4 "$dns_ip" || die "DNS service IP is not assigned to this server: $dns_ip"
    ip -4 route get "$pulse_ip" >/dev/null 2>&1 || die "Pulse IP has no IPv4 route: $pulse_ip"

    existing_token=""
    if [[ -r "$TOKEN_FILE" ]]; then
        existing_token=$(tr -d '\r\n' < "$TOKEN_FILE")
        valid_token "$existing_token" || die "installed token has an invalid format; enter a replacement after moving the invalid file aside"
    fi
    prompt_token token "$existing_token"
    existing_token=""

    PROTECTED_IPS=()
    PROTECTED_SEEN=()
    add_protected_ip "$management_ip"
    add_protected_ip "$dns_ip"
    add_protected_ip "$pulse_ip"
    while read -r address; do
        [[ -n "$address" ]] && add_protected_ip "$address"
    done < <(ip -4 -o address show | awk '{split($4, value, "/"); print value[1]}')
    while read -r address; do
        [[ -n "$address" ]] && add_protected_ip "$address"
    done < <(ip -4 route show table all | awk '{for (i=1; i<=NF; i++) if ($i == "via") print $(i+1)}' | sort -u)
    add_protected_list "$additional_protected"
    protected_csv=$(join_by_comma "${PROTECTED_IPS[@]}")

    if nft list table inet pulse >/dev/null 2>&1; then
        [[ -r "$HELPER_CONFIG" ]] || die "table inet pulse already exists without an installed Pulse helper configuration"
        nftables_shape_ok || die "table inet pulse exists but does not have the expected Pulse agent shape"
        if (( first_install == 1 )) && ! blocked_set_empty; then
            die "refusing a first installation over a non-empty blocked_dns_v4 set"
        fi
    fi

    validate_existing_account
    validate_existing_group
    visudo -cf "$SUDOERS_SOURCE" >/dev/null

    create_account_and_directories
    install_programs_and_policy
    write_configuration "$management_ip" "$dns_ip" "$pulse_ip" "$protected_csv" "$token"
    token=""

    systemctl daemon-reload
    systemctl enable pulse-agent.service >/dev/null
    systemctl restart pulse-agent.service
    wait_for_agent || die "pulse-agent did not become healthy within 20 seconds; inspect journalctl -u pulse-agent"

    info "running post-install checks"
    run_checks
    if (( first_install == 1 )); then
        if blocked_set_empty; then
            pass "blocked_dns_v4 is empty after first installation"
        else
            die "blocked_dns_v4 is not empty after first installation"
        fi
    else
        info "existing desired block state was preserved; the installer did not require an empty set"
    fi
    printf 'Installation complete. control_enabled is managed on Pulse and was not changed.\n'
}

validate_uninstall_target() {
    if getent passwd "$AGENT_USER" >/dev/null; then
        validate_existing_account
    fi
    if [[ -e "$UNIT_FILE" && -e "$UNIT_SOURCE" ]]; then
        cmp -s "$UNIT_SOURCE" "$UNIT_FILE" || die "installed pulse-agent unit differs from this repository; review it before uninstalling"
    fi
    if [[ -e "$SUDOERS_FILE" && -e "$SUDOERS_SOURCE" ]]; then
        cmp -s "$SUDOERS_SOURCE" "$SUDOERS_FILE" || die "installed pulse-agent sudoers differs from this repository; review it before uninstalling"
    fi
    if nft list table inet pulse >/dev/null 2>&1; then
        [[ -r "$HELPER_CONFIG" ]] || die "table inet pulse exists but helper.json is absent; refusing to delete an unverified table"
        nftables_shape_ok || die "table inet pulse does not have the expected Pulse agent shape; refusing to delete it"
    fi
}

uninstall_agent() {
    local confirmation group_members
    require_commands getent groupdel nft rm runuser systemctl userdel visudo
    validate_uninstall_target
    printf 'Type UNINSTALL to remove only Pulse agent components and table inet pulse: ' >/dev/tty
    IFS= read -r confirmation </dev/tty || die "unable to read confirmation"
    [[ "$confirmation" == "UNINSTALL" ]] || die "uninstall cancelled"

    systemctl disable --now pulse-agent.service >/dev/null 2>&1 || true

    if [[ -x "$HELPER_BINARY" && -r "$HELPER_CONFIG" ]]; then
        printf '{"blocks":[]}\n' | "$HELPER_BINARY" reconcile >/dev/null 2>&1 || warn "helper could not reconcile an empty set before removal"
    fi
    if nft list table inet pulse >/dev/null 2>&1; then
        nft delete table inet pulse
    fi

    rm -f -- "$UNIT_FILE" "$SUDOERS_FILE" "$AGENT_BINARY" "$HELPER_BINARY"
    rm -rf -- "$CONFIG_DIR" "$STATE_DIR"
    systemctl daemon-reload

    if getent passwd "$AGENT_USER" >/dev/null; then
        userdel "$AGENT_USER"
    fi
    if getent group "$AGENT_GROUP" >/dev/null; then
        group_members=$(getent group "$AGENT_GROUP" | awk -F: '{print $4}')
        [[ -z "$group_members" ]] || die "group $AGENT_GROUP still has members: $group_members"
        groupdel "$AGENT_GROUP"
    fi

    pass "Pulse agent components removed"
    pass "table inet pulse is absent"
    printf 'Uninstall complete. No other nftables, UFW, iptables, Pulse API, or control-plane configuration was touched.\n'
}

main() {
    local mode=${1:-install}
    [[ $# -le 1 ]] || { usage >&2; exit 2; }

    case "$mode" in
        install)
            require_root
            require_commands awk find sha256sum sort
            verify_bundle
            install_agent
            ;;
        --check)
            require_root
            require_commands awk cmp curl find getent ip nft runuser sed sha256sum sort ss stat sudo systemctl systemd-analyze tr visudo
            verify_bundle
            run_checks
            ;;
        --uninstall)
            require_root
            require_commands awk find sha256sum sort
            verify_bundle
            uninstall_agent
            ;;
        -h|--help)
            usage
            ;;
        *)
            usage >&2
            exit 2
            ;;
    esac
}

if [[ ${BASH_SOURCE[0]} == "$0" ]]; then
    main "$@"
fi
