#!/bin/bash
# =============================================================================
# Avalanche Node Self-Healing Watchdog v4
# =============================================================================
# Fully automated recovery — no manual intervention required for any
# automatable failure mode.
#
# Monitors:
#   1. Node process (systemd)
#   2. HTTP API health (hung process detection)
#   3. Peer connectivity (network partition detection)
#   4. OOM kill events
#   5. Main DB (Firewood) corruption
#   6. Critical errors in main.log
#   7. Plugin subprocess failure (vm-factory.log)
#   8. DFK chain Firewood corruption (wipe + resync)
#   9. DFK chain state sync errors (graduated: restart → block sync)
#  10. DFK chain progress / stall detection
#  11. P-chain health (action, not just logging)
#  12. Disk space (cleanup)
#  13. Crash loops (break the loop)
#
# What still requires humans:
#   - Physical hardware failure
#   - ISP/datacenter network partition (restart can't fix the wire)
#
# Design: set -uo pipefail (no set -e), all state on disk, no in-memory
# counters, content from each log file read once per iteration.
# =============================================================================

set -uo pipefail

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------
readonly NODE_SERVICE="avalanchego"
readonly LOG_DIR="/root/.avalanchego/logs"
readonly CHAIN_DATA_DIR="/root/.avalanchego/chainData"
readonly MAIN_DB_DIR="/root/.avalanchego/db/mainnet"
readonly DFK_CHAIN_ID="q2aTwKuyzgs8pynF7UXBZCU7DejbZbZ6EUyHr3JQzYgwNPUPi"
readonly DFK_LOG="${LOG_DIR}/${DFK_CHAIN_ID}.log"
readonly DFK_CONFIG="/root/.avalanchego/configs/chains/${DFK_CHAIN_ID}/config.json"
readonly MAIN_LOG="${LOG_DIR}/main.log"
readonly VM_FACTORY_LOG="${LOG_DIR}/vm-factory.log"
readonly WATCHDOG_LOG="${LOG_DIR}/watchdog.log"
readonly STATE_DIR="/var/lib/avalanche-watchdog"
readonly NODE_API="http://localhost:9650"

# Thresholds
readonly MAX_STATE_SYNC_RETRIES=3      # Switch to block sync after this many failures
readonly MAX_CORRUPTION_RETRIES=2      # Wipe + resync after this many corruption restarts
readonly STALL_TIMEOUT_MINUTES=20      # Progress must change within this window; leaf-count bypasses this for large tries
readonly STALL_CHECKS_BEFORE_ACTION=3  # Consecutive stall checks required before restart
readonly API_FAIL_THRESHOLD=3          # Consecutive API failures before restart
readonly PEER_FAIL_THRESHOLD=5         # Consecutive 0-peer checks (5 min) before restart
readonly PCHAIN_FAIL_THRESHOLD=20      # Consecutive P-chain unhealthy checks (20 min)
readonly MIN_STALL_RESTART_INTERVAL_MINUTES=45  # Minimum minutes between stall-triggered restarts (crash/API-down path)
readonly DFK_SUBNET_ID="Vn3aX6hNRstj5VHHm63TCgPNaeGnRSqCYXQqemSqDd2TQH4qJ"
readonly MAX_PEER_OUTAGE_HOURS=12   # Force restart only after 12h with no DFK peers
readonly DISK_WARN_PERCENT=85
readonly DISK_CRITICAL_PERCENT=95
readonly CHECK_INTERVAL_SECONDS=60
readonly CRASH_LOOP_WINDOW_SECONDS=300
readonly CRASH_LOOP_THRESHOLD=3
readonly STATUS_INTERVAL_SECONDS=600

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------
log() {
    local level="$1"; shift
    local ts
    ts=$(date '+%Y-%m-%d %H:%M:%S') || ts="unknown"
    local line="${ts} [${level}] $*"
    echo "${line}" >> "${WATCHDOG_LOG}" 2>/dev/null || true
    echo "${line}"
}
log_info()  { log "INFO"  "$@"; }
log_warn()  { log "WARN"  "$@"; }
log_error() { log "ERROR" "$@"; }

# ---------------------------------------------------------------------------
# State persistence — all counters on disk, survives watchdog restart
# ---------------------------------------------------------------------------
mkdir -p "${STATE_DIR}" 2>/dev/null || true

state_get() {
    local key="$1" default="${2:-}"
    local file="${STATE_DIR}/${key}"
    [[ -f "${file}" ]] && cat "${file}" 2>/dev/null || echo "${default}"
}
state_set() { echo "$2" > "${STATE_DIR}/$1" 2>/dev/null || true; }
state_increment() {
    local cur; cur=$(state_get "$1" "0")
    local new=$(( cur + 1 ))
    state_set "$1" "${new}"
    echo "${new}"
}
state_reset() { state_set "$1" "0"; }

# ---------------------------------------------------------------------------
# Node management
# ---------------------------------------------------------------------------
is_node_running() { systemctl is-active --quiet "${NODE_SERVICE}" 2>/dev/null; }

restart_node() {
    local reason="$1"
    log_warn "Restarting node: ${reason}"
    state_set "last_restart_time" "$(date '+%s')"
    # Reset progress baseline so stall detection starts fresh after restart
    state_set "dfk_progress_value" "INIT"
    # Reset leaf counter so post-restart leaf growth (from 0) correctly
    # triggers the secondary stall check on the very first non-zero reading,
    # rather than being compared against the pre-restart accumulated value.
    state_set "dfk_leafs_value" "0"
    # Advance log offsets to skip pre-restart content — prevents false positives
    # from old error entries that remain in append-only log files
    _advance_log_offsets
    systemctl restart "${NODE_SERVICE}" 2>/dev/null || true
    sleep 15
}

# Record current file size for each log so error checks only see new content
_advance_log_offsets() {
    local f
    for f in "${DFK_LOG}" "${MAIN_LOG}" "${VM_FACTORY_LOG}"; do
        local key="offset_$(basename "${f}" .log | tr -cd 'a-zA-Z0-9_')"
        local size; size=$(stat -c '%s' "${f}" 2>/dev/null) || size=0
        state_set "${key}" "${size}"
    done
}

# Read only content appended since the last offset update.
# Returns new content on stdout; updates the offset state.
_read_since_offset() {
    local file="$1"
    local key="offset_$(basename "${file}" .log | tr -cd 'a-zA-Z0-9_')"
    [[ ! -f "${file}" ]] && return 0
    local size; size=$(stat -c '%s' "${file}" 2>/dev/null) || size=0
    local offset; offset=$(state_get "${key}" "${size}")  # Default: skip existing content
    state_set "${key}" "${size}"
    [[ "${size}" -le "${offset}" ]] && return 0
    tail -c "+$(( offset + 1 ))" "${file}" 2>/dev/null | head -c 131072 || true
}

stop_node() {
    log_warn "Stopping node"
    systemctl stop "${NODE_SERVICE}" 2>/dev/null || true
    local i; for i in $(seq 1 6); do
        sleep 5
        is_node_running || return 0
    done
    systemctl kill --signal=SIGKILL "${NODE_SERVICE}" 2>/dev/null || true
    sleep 3
}

start_node() {
    log_info "Starting node"
    # Sync log offsets to current file sizes before starting the node.
    # This handles truncation side effects: if any log was truncated to 0 before
    # this call (e.g. Level 2 DFK wipe), the stored offset is reset to 0 so
    # _read_since_offset can see content written by the newly starting node.
    # Mirrors the same call in restart_node().
    _advance_log_offsets
    systemctl start "${NODE_SERVICE}" 2>/dev/null || true
    sleep 15
}

# ---------------------------------------------------------------------------
# DFK chain config modification (jq preferred, python3 fallback)
# ---------------------------------------------------------------------------
dfk_config_set() {
    local key="$1" value="$2"
    [[ ! -f "${DFK_CONFIG}" ]] && { log_error "DFK config missing: ${DFK_CONFIG}"; return 1; }

    local tmpfile; tmpfile=$(mktemp 2>/dev/null) || { log_error "mktemp failed"; return 1; }
    local ok=false

    if command -v jq >/dev/null 2>&1; then
        jq --arg k "${key}" --argjson v "${value}" '.[$k] = $v' \
            "${DFK_CONFIG}" > "${tmpfile}" 2>/dev/null \
            && mv "${tmpfile}" "${DFK_CONFIG}" 2>/dev/null \
            && ok=true
    fi

    if [[ "${ok}" == "false" ]] && command -v python3 >/dev/null 2>&1; then
        python3 - "${DFK_CONFIG}" "${key}" "${value}" "${tmpfile}" <<'PYEOF' 2>/dev/null
import json, sys
cfg_path, k, v_str, out = sys.argv[1:]
with open(cfg_path) as f: cfg = json.load(f)
v = True if v_str=='true' else False if v_str=='false' else json.loads(v_str)
cfg[k] = v
with open(out, 'w') as f: json.dump(cfg, f, indent=2)
PYEOF
        [[ -s "${tmpfile}" ]] \
            && mv "${tmpfile}" "${DFK_CONFIG}" 2>/dev/null \
            && ok=true
    fi

    rm -f "${tmpfile}" 2>/dev/null || true
    if [[ "${ok}" == "false" ]]; then
        log_error "CRITICAL: Cannot modify DFK config — neither jq nor python3 available"
        return 1
    fi
    log_info "Set ${key}=${value} in DFK config"
}

# ---------------------------------------------------------------------------
# Health checks
# All content-based checks accept pre-read log content as $1 so each log
# file is read exactly once per loop iteration.
# ---------------------------------------------------------------------------

# 1. HTTP API responding?
check_api_health() {
    local rc=0
    curl -sf --max-time 10 "${NODE_API}/ext/metrics" >/dev/null 2>&1 || rc=$?
    return ${rc}
}

# 2. Peer count > 0?
check_peer_count() {
    local resp count
    resp=$(curl -sf --max-time 10 -X POST "${NODE_API}/ext/info" \
        -H 'Content-Type: application/json' \
        -d '{"jsonrpc":"2.0","id":1,"method":"info.peers","params":{"nodeIDs":[]}}' \
        2>/dev/null) || true
    [[ -z "${resp}" ]] && return 0  # API down — let check_api_health handle it
    count=$(echo "${resp}" | python3 -c \
        "import json,sys; print(json.load(sys.stdin).get('result',{}).get('numPeers',1))" \
        2>/dev/null) || return 0
    [[ "${count:-1}" -gt 0 ]]
}

# 3. Count connected DFK subnet validators (0–6).
#    Cross-references platform.getCurrentValidators with info.peers.
#    Returns 0 on any API failure — conservative fallback (suppress restart).
check_dfk_peer_count() {
    # Use temp files: the validators response can be 100KB+ (too large for env vars).
    local tmpdir; tmpdir=$(mktemp -d 2>/dev/null) || { echo "0"; return; }
    local tmpv="${tmpdir}/v.json" tmpp="${tmpdir}/p.json"

    curl -sf --max-time 10 -X POST "${NODE_API}/ext/bc/P" \
        -H 'Content-Type: application/json' \
        -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"platform.getCurrentValidators\",\"params\":{\"subnetID\":\"${DFK_SUBNET_ID}\"}}" \
        -o "${tmpv}" 2>/dev/null || true

    if [[ ! -s "${tmpv}" ]]; then rm -rf "${tmpdir}" 2>/dev/null; echo "0"; return; fi

    curl -sf --max-time 10 -X POST "${NODE_API}/ext/info" \
        -H 'Content-Type: application/json' \
        -d '{"jsonrpc":"2.0","id":1,"method":"info.peers","params":{"nodeIDs":[]}}' \
        -o "${tmpp}" 2>/dev/null || true

    if [[ ! -s "${tmpp}" ]]; then rm -rf "${tmpdir}" 2>/dev/null; echo "0"; return; fi

    python3 - "${tmpv}" "${tmpp}" <<'PYEOF' 2>/dev/null || echo "0"
import json, sys
try:
    with open(sys.argv[1]) as f: validators = json.load(f)
    with open(sys.argv[2]) as f: peers_data = json.load(f)
    validator_ids = {v["nodeID"] for v in validators.get("result", {}).get("validators", [])}
    connected_ids = {p["nodeID"] for p in peers_data.get("result", {}).get("peers", [])}
    print(len(validator_ids & connected_ids))
except:
    print(0)
PYEOF
    rm -rf "${tmpdir}" 2>/dev/null || true
}

# 4. OOM kill of avalanchego process?
check_oom_events() {
    local found
    # Window = check interval + 5s jitter so each OOM event triggers exactly one
    # restart. A 5-minute window re-triggers this check on the same kernel entry
    # for up to 4 subsequent iterations, causing 3-4 spurious restarts per OOM
    # and risking a false crash-loop detection.
    found=$(journalctl -k --since "-$(( CHECK_INTERVAL_SECONDS + 5 ))s" 2>/dev/null \
        | grep -ciE 'oom.*(avalanche|rpcchainvm)|killed process.*(avalanche|rpcchainvm)|avalanche.*oom' \
        2>/dev/null) || found=0
    [[ "${found:-0}" -gt 0 ]]
}

# 4. Main DB (Firewood) corruption — detected in main.log new content.
#    Corruption = data integrity error that restart cannot fix.
check_main_db_corruption() {
    local content="${1:-}"
    [[ -z "${content}" ]] && return 1
    local found
    found=$(echo "${content}" | grep -cE \
        'corrupt(ed|ion)?|hash.?mismatch|invalid.?root.?hash|trie.*(corrupt|invalid)|firewood.*(invalid|unexpected|corrupt)|database.*(corrupt|invalid.?hash)|checksum.*(fail|mismatch)' \
        2>/dev/null) || found=0
    [[ "${found:-0}" -gt 0 ]]
}

# 5. FATAL or panic in main node log (non-corruption critical errors).
check_main_log_errors() {
    local content="${1:-}"
    [[ -z "${content}" ]] && return 1
    local found
    found=$(echo "${content}" | grep -cE \
        'FATAL|panic:|runtime error:' \
        2>/dev/null) || found=0
    [[ "${found:-0}" -gt 0 ]]
}

# 6. Plugin subprocess failure.
check_vm_factory_errors() {
    local content="${1:-}"
    [[ -z "${content}" ]] && return 1
    local found
    found=$(echo "${content}" | grep -cE \
        'FATAL|panic:|plugin.*failed|subprocess.*crashed|handshake.*failed' \
        2>/dev/null) || found=0
    [[ "${found:-0}" -gt 0 ]]
}

# 7. DFK chain Firewood corruption — data integrity error, resync required.
check_dfk_corruption() {
    local content="${1:-}"
    [[ -z "${content}" ]] && return 1
    local found
    found=$(echo "${content}" | grep -cE \
        'corrupt(ed|ion)?|hash.?mismatch|invalid.?root.?hash|trie.*(corrupt|invalid)|firewood.*(invalid|unexpected|corrupt)|database.*(corrupt|invalid.?hash)|checksum.*(fail|mismatch)' \
        2>/dev/null) || found=0
    [[ "${found:-0}" -gt 0 ]]
}

# 8. DFK state sync / plugin errors (transient — may recover with restart).
check_dfk_errors() {
    local content="${1:-}"
    [[ -z "${content}" ]] && return 1
    local found
    found=$(echo "${content}" | grep -cE \
        'FATAL|panic:|runtime error:|ERROR.*(state.?sync|sync.?state)|state.?sync.*(ERROR|failed|not supported)|cannot start state sync|VM.*failed to initialize' \
        2>/dev/null) || found=0
    [[ "${found:-0}" -gt 0 ]]
}

# 9. DFK chain making measurable progress?
#    Uses persistent progress marker — no fragile timestamp parsing.
check_dfk_progress() {
    local now; now=$(date '+%s') || return 0

    # While P-chain is still bootstrapping, DFK chain cannot start at all.
    # Any DFK silence during this period is expected — reset the stall timer
    # so we don't fire false stall incidents while waiting for P-chain.
    local pchain_done
    pchain_done=$(curl -sf --max-time 3 "http://localhost:9650/ext/metrics" 2>/dev/null \
        | grep 'avalanche_snowman_bootstrap_finished{chain="P"}' \
        | awk '{print $2}' | head -1 | cut -d'.' -f1) || true
    if [[ "${pchain_done:-0}" == "0" ]]; then
        state_set "dfk_progress_time" "${now}"
        return 0  # P-chain still bootstrapping — DFK can't run, not a real stall
    fi

    local current=""
    if [[ -f "${DFK_LOG}" ]]; then
        # 1. Block fetch count from "fetching blocks" log entries — HIGHEST PRIORITY.
        #    These appear every ~18s during the long block-download phase and are the
        #    only reliable liveness signal. Must come before checkpoint height because
        #    checkpoint height only changes in rapid bursts at startup, then stays static
        #    for hours while the node is actively downloading — causing false stall fires.
        local fetched
        fetched=$(tail -100 "${DFK_LOG}" 2>/dev/null \
            | grep '"numFetchedBlocks"' 2>/dev/null \
            | grep -oP '"numFetchedBlocks":\s*\K[0-9]+' 2>/dev/null | tail -1) || true
        [[ -n "${fetched}" ]] && current="fetched:${fetched}"

        # 2. Checkpoint height — indicator during block execution bursts
        if [[ -z "${current}" ]]; then
            local chkpt_height
            chkpt_height=$(tail -100 "${DFK_LOG}" 2>/dev/null \
                | grep -oP '"height":\s*\K[0-9]+' 2>/dev/null | tail -1) || true
            [[ -n "${chkpt_height}" ]] && [[ "${chkpt_height}" != "0" ]] && current="chkpt:${chkpt_height}"
        fi

        # 3. State sync: triesRemaining
        if [[ -z "${current}" ]]; then
            local tries
            tries=$(tail -100 "${DFK_LOG}" 2>/dev/null \
                | grep -oP 'triesRemaining=\K[0-9]+' 2>/dev/null | tail -1) || true
            [[ -n "${tries}" ]] && current="sync:${tries}"
        fi

        # 4. Block fetch count from checkpoint lines (fallback when no fetching-blocks entries)
        if [[ -z "${current}" ]]; then
            local blocksfetched
            blocksfetched=$(tail -100 "${DFK_LOG}" 2>/dev/null \
                | grep -oP '"blocksFetched":\s*\K[0-9]+' 2>/dev/null | tail -1) || true
            [[ -n "${blocksfetched}" ]] && current="fetched:${blocksfetched}"
        fi

        # 5. Fallback: key=value height (skip 0, init artifact)
        if [[ -z "${current}" ]]; then
            local height
            height=$(tail -100 "${DFK_LOG}" 2>/dev/null \
                | grep -oP '(?:height|blkHeight|blockHeight)=\K[0-9]+' 2>/dev/null | tail -1) || true
            [[ -n "${height}" ]] && [[ "${height}" != "0" ]] && current="block:${height}"
        fi
    fi

    local stored; stored=$(state_get "dfk_progress_value" "INIT")
    local stored_time; stored_time=$(state_get "dfk_progress_time" "${now}")

    if [[ "${stored}" == "INIT" ]]; then
        state_set "dfk_progress_value" "${current:-none}"
        state_set "dfk_progress_time" "${now}"
        return 0
    fi
    if [[ -n "${current}" ]] && [[ "${current}" != "${stored}" ]]; then
        state_set "dfk_progress_value" "${current}"
        state_set "dfk_progress_time" "${now}"
        return 0
    fi
    # Secondary: Prometheus leaf count tracks active large-trie sync.
    # Large storage tries (millions of leaves) take 40-60min each; trie count
    # stays frozen while leaves are being downloaded. Leaf increase = not stalled.
    local leafs stored_leafs
    leafs=$(curl -sf --max-time 3 "http://localhost:9650/ext/metrics" 2>/dev/null \
        | grep "state_sync_total_leafs{" | awk '{print $2}' | head -1) || true
    stored_leafs=$(state_get "dfk_leafs_value" "0")
    if [[ -n "${leafs}" ]] && [[ "${leafs}" != "0" ]]; then
        local li sl
        li=$(printf "%.0f" "${leafs}" 2>/dev/null) || li=0
        sl=$(printf "%.0f" "${stored_leafs}" 2>/dev/null) || sl=0
        state_set "dfk_leafs_value" "${leafs}"
        if [[ "${li}" -gt "${sl}" ]]; then
            state_set "dfk_progress_time" "${now}"
            return 0  # leaf progress = active large-trie sync, not stalled
        fi
    fi
    # Secondary: Prometheus bs_fetched tracks block bootstrap download progress.
    # During the long block-download phase, DFK log may not update for minutes even
    # when the bootstrapper is actively fetching. bs_fetched growing = not stalled.
    local bs_fetched stored_bs
    bs_fetched=$(curl -sf --max-time 3 "http://localhost:9650/ext/metrics" 2>/dev/null \
        | grep 'snowman_bs_fetched{chain="q2aTwKuyzgs8pynF7UXBZCU7DejbZbZ6EUyHr3JQzYgwNPUPi"}' \
        | awk '{print $2}' | head -1) || true
    stored_bs=$(state_get "dfk_bs_fetched" "0")
    if [[ -n "${bs_fetched}" ]] && [[ "${bs_fetched}" != "0" ]]; then
        local bf sb
        bf=$(printf "%.0f" "${bs_fetched}" 2>/dev/null) || bf=0
        sb=$(printf "%.0f" "${stored_bs}" 2>/dev/null) || sb=0
        state_set "dfk_bs_fetched" "${bs_fetched}"
        if [[ "${bf}" -gt "${sb}" ]]; then
            state_set "dfk_progress_time" "${now}"
            return 0  # block fetch progress = active bootstrap download, not stalled
        fi
    fi
    local elapsed=$(( now - stored_time ))
    [[ ${elapsed} -le $(( STALL_TIMEOUT_MINUTES * 60 )) ]]
}

# 10. DFK chain reached normal consensus (sync complete)?
check_dfk_synced() {
    [[ ! -f "${DFK_LOG}" ]] && return 1
    local found
    found=$(tail -100 "${DFK_LOG}" 2>/dev/null \
        | grep -cE 'normal operations started|bootstrapping finished|transitioning to normal operations' \
        2>/dev/null) || found=0
    [[ "${found:-0}" -gt 0 ]]
}

# 11. P-chain recent activity?
check_pchain_health() {
    [[ ! -f "${P_CHAIN_LOG:-${LOG_DIR}/P.log}" ]] && return 0

    # Prometheus bypass: snowman_bs_fetched{chain="P"} growing = actively fetching blocks.
    # During bulk block download (24M+ blocks at 14K/s) the P-chain log is full of
    # "fetching blocks" entries but the old grep missed them, causing false restarts.
    # bs_fetched growing is the most reliable liveness signal during download phase.
    local bs_fetched stored_bs
    bs_fetched=$(curl -sf --max-time 3 "http://localhost:9650/ext/metrics" 2>/dev/null \
        | grep 'avalanche_snowman_bs_fetched{chain="P"}' \
        | awk '{print $2}' | head -1) || true
    stored_bs=$(state_get "pchain_bs_fetched" "0")
    if [[ -n "${bs_fetched}" ]] && [[ "${bs_fetched}" != "0" ]]; then
        local bf sb
        bf=$(printf "%.0f" "${bs_fetched}" 2>/dev/null) || bf=0
        sb=$(printf "%.0f" "${stored_bs}" 2>/dev/null) || sb=0
        state_set "pchain_bs_fetched" "${bs_fetched}"
        if [[ "${bf}" -gt "${sb}" ]]; then
            return 0  # P-chain actively fetching = healthy
        fi
    fi

    # Log-based check: covers both block download and block execution phases.
    # "fetching blocks" appears every 5s during bulk download.
    # "executed blocks"/"executing blocks" appear during block execution phase.
    local found
    found=$(tail -200 "${P_CHAIN_LOG:-${LOG_DIR}/P.log}" 2>/dev/null \
        | grep -cE 'writeTXs|executing blocks|executed blocks|block accepted|consensus|fetching blocks' \
        2>/dev/null) || found=0
    [[ "${found:-0}" -gt 0 ]]
}

# 12. Crash loop?
check_crash_loop() {
    local n
    n=$(journalctl -u "${NODE_SERVICE}" --since "-${CRASH_LOOP_WINDOW_SECONDS}s" 2>/dev/null \
        | grep -c 'Started\|Active: active' 2>/dev/null) || n=0
    [[ "${n:-0}" -ge "${CRASH_LOOP_THRESHOLD}" ]]
}

# 13. Disk space — returns 0=ok 1=warn 2=critical
check_disk_space() {
    local pct
    pct=$(df / 2>/dev/null | tail -1 | awk '{print $5}' | tr -d '%') || return 0
    [[ "${pct:-0}" -ge "${DISK_CRITICAL_PERCENT}" ]] && return 2
    [[ "${pct:-0}" -ge "${DISK_WARN_PERCENT}" ]] && return 1
    return 0
}

# ---------------------------------------------------------------------------
# Recovery actions
# ---------------------------------------------------------------------------

# DFK state sync graduated recovery: restart → restart → block sync + wipe
recover_state_sync() {
    local retries; retries=$(state_increment "dfk_state_sync_retries")
    log_warn "State sync failure #${retries} (threshold: ${MAX_STATE_SYNC_RETRIES})"

    if [[ "${retries}" -le "${MAX_STATE_SYNC_RETRIES}" ]]; then
        log_info "Level 1: restart (state sync resumes from saved progress)"
        restart_node "state sync error attempt ${retries}/${MAX_STATE_SYNC_RETRIES}"
    else
        log_warn "Level 2: switching DFK to block-by-block sync after ${retries} failures"
        stop_node
        # Truncate DFK log so check_dfk_synced cannot see stale "normal operations started"
        # from a previous sync run — same guard as in recover_dfk_corruption Level 2.
        truncate -s 0 "${DFK_LOG}" 2>/dev/null || true

        if ! dfk_config_set "state-sync-enabled" "false"; then
            log_error "Cannot modify DFK config — staying at level 1"
            state_set "dfk_state_sync_retries" "${MAX_STATE_SYNC_RETRIES}"
            start_node
            return
        fi

        local dfk_data="${CHAIN_DATA_DIR}/${DFK_CHAIN_ID}"
        if [[ -d "${dfk_data}" ]]; then
            local backup="${dfk_data}.bak.$(date '+%s')"
            log_info "Moving DFK data to ${backup}"
            mv "${dfk_data}" "${backup}" 2>/dev/null || log_error "Could not move DFK data"
            state_set "pending_backup" "${backup}"
            state_set "pending_backup_after" "$(( $(date '+%s') + 86400 ))"
        fi

        state_set "dfk_progress_value" "INIT"
        state_reset "dfk_state_sync_retries"
        state_reset "stall_count"
        state_reset "consecutive_stalls"
        state_set "dfk_mode" "block_sync"
        start_node
        log_info "DFK chain will now sync block-by-block"
    fi
}

# DFK Firewood corruption recovery: restart×2 → wipe chainData + resync
recover_dfk_corruption() {
    local retries; retries=$(state_increment "dfk_corruption_retries")
    log_error "DFK Firewood corruption detected — attempt #${retries}/${MAX_CORRUPTION_RETRIES}"

    if [[ "${retries}" -le "${MAX_CORRUPTION_RETRIES}" ]]; then
        log_info "Level 1: restart (Firewood may self-recover from transient error)"
        restart_node "DFK corruption restart ${retries}/${MAX_CORRUPTION_RETRIES}"
        return
    fi

    log_error "Level 2: corruption persisted — wiping DFK chain data for full resync"
    log_info "DFK chain data (~3.7GB) will be wiped; P-chain and main DB unaffected"
    stop_node
    # Truncate DFK log so check_dfk_synced (tail-based) cannot see stale
    # "normal operations started" from the corrupted chain's previous sync run.
    # Without this, dfk_mode would be set to "synced" on the next iteration,
    # permanently disabling stall detection during the resync.
    truncate -s 0 "${DFK_LOG}" 2>/dev/null || true

    local dfk_data="${CHAIN_DATA_DIR}/${DFK_CHAIN_ID}"
    if [[ -d "${dfk_data}" ]]; then
        local backup="${dfk_data}.corrupt.$(date '+%s')"
        log_info "Moving corrupted DFK data to ${backup} (auto-deleted in 24h)"
        if mv "${dfk_data}" "${backup}" 2>/dev/null; then
            state_set "pending_backup" "${backup}"
            state_set "pending_backup_after" "$(( $(date '+%s') + 86400 ))"
        else
            log_warn "mv failed — wiping directly"
            rm -rf "${dfk_data}" 2>/dev/null || log_error "Cannot wipe DFK data — manual intervention needed"
        fi
    else
        log_warn "DFK chain data dir not found at ${dfk_data} — already missing?"
    fi

    # Reset all DFK sync state — node will resync from genesis
    state_reset "dfk_corruption_retries"
    state_set "dfk_progress_value" "INIT"
    state_reset "dfk_state_sync_retries"
    state_reset "stall_count"
    state_reset "consecutive_stalls"
    state_set "dfk_mode" "unknown"

    start_node
    log_info "DFK chain wiped — will resync from network (state sync if configured, else block sync)"
}

# Main DB (Firewood) corruption recovery: restart×2 → wipe main DB + resync
# DFK chain data in chainData/ is NOT affected — only P-chain/main DB state is lost.
# P-chain resyncs quickly via partial-sync.
recover_main_db_corruption() {
    local retries; retries=$(state_increment "main_corruption_retries")
    log_error "Main DB (Firewood) corruption detected — attempt #${retries}/${MAX_CORRUPTION_RETRIES}"

    if [[ "${retries}" -le "${MAX_CORRUPTION_RETRIES}" ]]; then
        log_info "Level 1: restart (Firewood may self-recover from transient error)"
        restart_node "main DB corruption restart ${retries}/${MAX_CORRUPTION_RETRIES}"
        return
    fi

    log_error "Level 2: corruption persisted — wiping main DB at ${MAIN_DB_DIR}"
    log_info "Main DB is ~95GB — wiping directly (too large to backup)"
    log_info "DFK chain data in ${CHAIN_DATA_DIR}/ is separate and will NOT be wiped"
    log_info "P-chain will re-sync via partial-sync (fast)"
    stop_node

    if [[ -d "${MAIN_DB_DIR}" ]]; then
        rm -rf "${MAIN_DB_DIR}" 2>/dev/null
        if [[ -d "${MAIN_DB_DIR}" ]]; then
            log_error "CRITICAL: Cannot wipe ${MAIN_DB_DIR} — manual intervention needed"
            log_error "Run: rm -rf ${MAIN_DB_DIR} && systemctl start ${NODE_SERVICE}"
            # Don't start node — DB is still corrupted
            state_reset "main_corruption_retries"
            return 1
        fi
        log_info "Main DB wiped successfully"
    else
        log_warn "Main DB directory ${MAIN_DB_DIR} not found — already missing?"
    fi

    state_reset "main_corruption_retries"
    state_set "dfk_progress_value" "INIT"
    state_reset "stall_count"
    state_reset "consecutive_stalls"

    start_node
    log_info "Node starting — P-chain resync beginning (fast via partial-sync)"
}

recover_stalled() {
    local now; now=$(date '+%s')

    # -----------------------------------------------------------------------
    # Smart restart gate: use actual DFK peer count for accurate diagnosis.
    #
    # DFK has only 6 validators. When they go offline for maintenance, DFK
    # block download stalls but the main avalanchego node is perfectly healthy.
    # Restarting the node during a peer outage:
    #   (a) loses all in-memory downloaded blocks (numFetchedBlocks → 0)
    #   (b) doesn't fix the peer issue (node immediately stalls again)
    #   (c) wastes 8+ hours of accumulated download progress
    #
    # Policy:
    #   API down                       → genuine crash; apply cooldown then restart
    #   API up + 0 DFK peers + < 12h   → peer outage; wait for validators to return
    #   API up + 0 DFK peers + ≥ 12h   → force restart (failsafe)
    #   API up + DFK peers > 0         → restart (peers present but download stalled)
    # -----------------------------------------------------------------------
    local api_ok=0
    if curl -s --max-time 5 "http://localhost:9650/ext/health" >/dev/null 2>&1; then
        api_ok=1
    fi

    if [[ "${api_ok}" == "0" ]]; then
        # Node API unreachable — genuine crash; apply cooldown then restart
        local last_restart; last_restart=$(state_get "last_restart_time" "0")
        local since=$(( now - last_restart ))
        local min_interval=$(( MIN_STALL_RESTART_INTERVAL_MINUTES * 60 ))
        if [[ "${since}" -lt "${min_interval}" ]]; then
            log_warn "Stall restart suppressed — API down but cooldown active (${since}s / ${min_interval}s)"
            state_reset "consecutive_stalls"
            return 0
        fi
        local n; n=$(state_increment "stall_count")
        log_warn "Stall recovery #${n} (node API down)"
        state_reset "consecutive_stalls"
        restart_node "progress stalled — node API down (incident #${n})"
        return
    fi

    # API is up — query actual DFK peer count for accurate diagnosis
    local dfk_peers; dfk_peers=$(check_dfk_peer_count)

    # Track when this peer outage started (persistent across node restarts —
    # NOT reset in restart_node because a restart doesn't bring validators back)
    local outage_start; outage_start=$(state_get "peer_outage_start_time" "0")
    if [[ "${dfk_peers}" -eq 0 ]] && [[ "${outage_start}" == "0" ]]; then
        state_set "peer_outage_start_time" "${now}"
        outage_start="${now}"
    fi

    local outage_secs=$(( now - outage_start ))
    local outage_min=$(( outage_secs / 60 ))
    local max_outage_secs=$(( MAX_PEER_OUTAGE_HOURS * 3600 ))
    local max_outage_min=$(( max_outage_secs / 60 ))

    if [[ "${dfk_peers}" -eq 0 ]] && [[ "${outage_secs}" -lt "${max_outage_secs}" ]]; then
        # No DFK validators connected — restart won't help, wait for them to return
        log_warn "DFK stall: 0/6 DFK peers connected — waiting for validators (outage ${outage_min}m / max ${max_outage_min}m)"
        state_reset "consecutive_stalls"
        return 0
    fi

    # Either peers are present (but stalled) or outage exceeded 12h limit
    local reason
    if [[ "${dfk_peers}" -gt 0 ]]; then
        reason="DFK peers present (${dfk_peers}/6) but no download progress"
    else
        reason="DFK peer outage exceeded ${max_outage_min}m limit (${dfk_peers}/6 peers)"
    fi
    local n; n=$(state_increment "stall_count")
    log_warn "Stall recovery #${n} (${reason})"
    state_reset "consecutive_stalls"
    restart_node "progress stalled — ${reason} (incident #${n})"
}

recover_crash_loop() {
    log_error "Crash loop detected — breaking loop with 5-minute pause"
    stop_node
    sleep 300
    # Read recent log tails (not offset-based — one-time diagnostic during crash loop recovery)
    # This path is critical: if corruption causes crash-on-startup, the normal log-read path
    # in the main loop is unreachable (node always down → continue before _read_since_offset).
    local _dfk_tail _main_tail
    _dfk_tail=$(tail -c 65536 "${DFK_LOG}" 2>/dev/null)  || _dfk_tail=""
    _main_tail=$(tail -c 65536 "${MAIN_LOG}" 2>/dev/null) || _main_tail=""

    # Advance offsets AFTER reading tails, BEFORE delegating to sub-recovery.
    # Sub-recovery Level 2 paths use stop_node+start_node (not restart_node), so they
    # do not call _advance_log_offsets. Without this, the next iteration's
    # _read_since_offset would re-read crash content and spuriously re-trigger recovery.
    _advance_log_offsets

    if check_main_db_corruption "${_main_tail}" 2>/dev/null; then
        log_error "Crash loop caused by main DB corruption — escalating to DB recovery"
        recover_main_db_corruption
    elif check_dfk_corruption "${_dfk_tail}" 2>/dev/null; then
        log_error "Crash loop caused by DFK chain corruption — escalating to chain recovery"
        recover_dfk_corruption
    elif check_dfk_errors "${_dfk_tail}" 2>/dev/null; then
        log_warn "Crash loop appears state-sync related"
        start_node; sleep 10
        recover_state_sync
    else
        start_node
    fi
}

recover_disk_space() {
    local level="$1"
    if [[ "${level}" == "critical" ]]; then
        log_error "CRITICAL disk — cleaning logs and backups"
        find "${LOG_DIR}" -name "*.log" -mtime +2 -exec truncate -s 0 {} \; 2>/dev/null || true
        local b; b=$(state_get "pending_backup" "")
        if [[ -n "${b}" ]]; then
            [[ -d "${b}" ]] && rm -rf "${b}" 2>/dev/null && log_info "Deleted backup in disk emergency: ${b}"
            state_set "pending_backup" ""  # clear even if dir was already gone
            state_set "pending_backup_after" "0"
        fi
        find "${CHAIN_DATA_DIR}" -maxdepth 1 \( -name "*.bak.*" -o -name "*.corrupt.*" \) -type d \
            -exec rm -rf {} \; 2>/dev/null || true
    else
        log_warn "Disk usage above ${DISK_WARN_PERCENT}%"
    fi
}

# Deferred backup cleanup (avoids background subshell leaks)
run_maintenance() {
    local b; b=$(state_get "pending_backup" "")
    [[ -z "${b}" ]] && return
    local after; after=$(state_get "pending_backup_after" "0")
    local now; now=$(date '+%s') || return
    if [[ "${now}" -ge "${after}" ]]; then
        [[ -d "${b}" ]] && rm -rf "${b}" 2>/dev/null && log_info "Deleted old backup: ${b}"
        state_set "pending_backup" ""
        state_set "pending_backup_after" "0"
    fi
}

# ---------------------------------------------------------------------------
# Main monitoring loop
# ---------------------------------------------------------------------------
readonly P_CHAIN_LOG="${LOG_DIR}/P.log"

main() {
    log_info "=========================================="
    log_info "Avalanche Watchdog v4 starting"
    log_info "Checks: process api peers oom main-corruption main-errors vm-factory dfk-corruption dfk-errors dfk-stall pchain disk crash-loop"
    log_info "Check interval: ${CHECK_INTERVAL_SECONDS}s | Stall timeout: ${STALL_TIMEOUT_MINUTES}m | Corruption retries: ${MAX_CORRUPTION_RETRIES}"
    log_info "=========================================="

    # Initialize progress baseline
    [[ "$(state_get 'dfk_progress_value' 'INIT')" == "INIT" ]] && {
        state_set "dfk_progress_value" "INIT"
        state_set "dfk_progress_time" "$(date '+%s')"
    }

    # Initialize log offsets to current file sizes — skip all existing log history.
    # Error detection only looks at content written after this point.
    _advance_log_offsets

    while true; do
        sleep "${CHECK_INTERVAL_SECONDS}"
        run_maintenance || true

        # --- 1. Process running? ---
        if ! is_node_running 2>/dev/null; then
            log_warn "Node process down"
            state_reset "consecutive_stalls"
            if check_crash_loop 2>/dev/null; then
                recover_crash_loop || true
            else
                start_node || true
            fi
            continue
        fi

        # --- 2. API responding? ---
        if ! check_api_health 2>/dev/null; then
            local api_fails; api_fails=$(state_increment "api_fail_count")
            if [[ "${api_fails}" -ge "${API_FAIL_THRESHOLD}" ]]; then
                log_error "API unresponsive for ${api_fails} consecutive checks"
                state_reset "api_fail_count"
                restart_node "API health check failed" || true
                continue
            else
                log_warn "API unresponsive (${api_fails}/${API_FAIL_THRESHOLD})"
            fi
        else
            state_reset "api_fail_count"
        fi

        # --- 3. Peers connected? ---
        if ! check_peer_count 2>/dev/null; then
            local peer_fails; peer_fails=$(state_increment "peer_fail_count")
            if [[ "${peer_fails}" -ge "${PEER_FAIL_THRESHOLD}" ]]; then
                log_error "Zero peers for ${peer_fails} consecutive checks ($(( peer_fails * CHECK_INTERVAL_SECONDS / 60 )) min)"
                state_reset "peer_fail_count"
                restart_node "no peers" || true
                continue
            else
                log_warn "Zero peers (${peer_fails}/${PEER_FAIL_THRESHOLD})"
            fi
        else
            state_reset "peer_fail_count"
        fi

        # --- 4. OOM kill? ---
        if check_oom_events 2>/dev/null; then
            log_error "OOM kill detected in kernel log"
            restart_node "OOM kill" || true
            continue
        fi

        # Read new content from each log once per iteration.
        # All content-based checks receive this pre-read data so each file is
        # only read once and the offset is advanced exactly once per loop.
        local _main_new _vmf_new _dfk_new
        _main_new=$(_read_since_offset "${MAIN_LOG}") || _main_new=""
        _vmf_new=$(_read_since_offset "${VM_FACTORY_LOG}") || _vmf_new=""
        _dfk_new=$(_read_since_offset "${DFK_LOG}") || _dfk_new=""

        # --- 5. Main DB Firewood corruption? ---
        if check_main_db_corruption "${_main_new}" 2>/dev/null; then
            log_error "Main DB (Firewood) corruption detected in main.log"
            recover_main_db_corruption || true
            continue
        fi

        # --- 6. Other critical errors in main.log? ---
        if check_main_log_errors "${_main_new}" 2>/dev/null; then
            log_error "FATAL/panic detected in main.log"
            restart_node "critical error in main log" || true
            continue
        fi

        # --- 7. Plugin failure in vm-factory.log? ---
        if check_vm_factory_errors "${_vmf_new}" 2>/dev/null; then
            log_error "Plugin failure detected in vm-factory.log"
            restart_node "plugin failure" || true
            continue
        fi

        # --- 8. DFK chain Firewood corruption? ---
        if check_dfk_corruption "${_dfk_new}" 2>/dev/null; then
            log_error "DFK chain Firewood corruption detected in chain log"
            state_reset "consecutive_stalls"
            recover_dfk_corruption || true
            continue
        fi

        # --- 9. DFK state sync error (fast path, non-corruption) ---
        if check_dfk_errors "${_dfk_new}" 2>/dev/null; then
            log_error "DFK state sync error in chain log"
            state_reset "consecutive_stalls"
            recover_state_sync || true
            continue
        fi

        # --- 10. DFK chain progress / stall ---
        local dfk_mode; dfk_mode=$(state_get "dfk_mode" "unknown")
        if [[ "${dfk_mode}" != "synced" ]]; then
            if check_dfk_synced 2>/dev/null; then
                log_info "DFK chain in normal consensus — sync complete!"
                state_set "dfk_mode" "synced"
                state_reset "dfk_state_sync_retries"
                state_reset "stall_count"
                state_reset "consecutive_stalls"
            elif ! check_dfk_progress 2>/dev/null; then
                local cs; cs=$(state_increment "consecutive_stalls")
                log_warn "DFK stalled (${cs}/${STALL_CHECKS_BEFORE_ACTION})"
                if [[ "${cs}" -ge "${STALL_CHECKS_BEFORE_ACTION}" ]]; then
                    recover_stalled || true
                fi
            else
                local prev_cs; prev_cs=$(state_get "consecutive_stalls" "0")
                if [[ "${prev_cs}" -gt 0 ]]; then
                    local outage_start; outage_start=$(state_get "peer_outage_start_time" "0")
                    if [[ "${outage_start}" != "0" ]]; then
                        local resume_now; resume_now=$(date '+%s')
                        local outage_secs=$(( resume_now - outage_start ))
                        local outage_min=$(( outage_secs / 60 ))
                        local bf_raw; bf_raw=$(state_get "dfk_bs_fetched" "0")
                        local bf_int; bf_int=$(printf "%.0f" "${bf_raw}" 2>/dev/null) || bf_int=0
                        log_info "DFK peer outage ended — duration ${outage_min}m, download preserved at ~${bf_int} fetched blocks"
                        state_set "peer_outage_start_time" "0"
                    else
                        log_info "DFK progress resumed (transient stall)"
                    fi
                fi
                state_reset "consecutive_stalls"
            fi
        fi

        # --- 11. P-chain health (action, not just logging) ---
        if ! check_pchain_health 2>/dev/null; then
            local pf; pf=$(state_increment "pchain_fail_count")
            if [[ "${pf}" -ge "${PCHAIN_FAIL_THRESHOLD}" ]]; then
                log_error "P-chain no activity for ${pf} checks — restarting"
                state_reset "pchain_fail_count"
                restart_node "P-chain unhealthy" || true
            else
                log_warn "P-chain quiet (${pf}/${PCHAIN_FAIL_THRESHOLD})"
            fi
        else
            state_reset "pchain_fail_count"
        fi

        # --- 12. Disk space ---
        local disk_rc=0; check_disk_space 2>/dev/null || disk_rc=$?
        case "${disk_rc}" in
            2) recover_disk_space "critical" || true ;;
            1) recover_disk_space "warning"  || true ;;
        esac

        # --- Periodic status (every 10 min) ---
        local last; last=$(state_get "last_status_log" "0")
        local now; now=$(date '+%s') || now=0
        if [[ $(( now - last )) -ge ${STATUS_INTERVAL_SECONDS} ]]; then
            state_set "last_status_log" "${now}"
            local disk_pct; disk_pct=$(df / 2>/dev/null | tail -1 | awk '{print $5}') || disk_pct="?"
            local progress; progress=$(state_get "dfk_progress_value" "?")
            local retries; retries=$(state_get "dfk_state_sync_retries" "0")
            local mode; mode=$(state_get "dfk_mode" "unknown")
            local dfk_corrupt; dfk_corrupt=$(state_get "dfk_corruption_retries" "0")
            local main_corrupt; main_corrupt=$(state_get "main_corruption_retries" "0")
            local outage_start; outage_start=$(state_get "peer_outage_start_time" "0")
            local outage_info=""
            if [[ "${outage_start}" != "0" ]]; then
                local outage_secs=$(( now - outage_start ))
                outage_info=" peer_outage=$(( outage_secs / 60 ))m"
            fi
            log_info "Status: disk=${disk_pct} dfk=${mode} progress=${progress} sync_retries=${retries} dfk_corrupt=${dfk_corrupt} main_corrupt=${main_corrupt}${outage_info}"
        fi
    done
}

trap 'log_info "Watchdog shutting down"; exit 0' SIGTERM SIGINT

main "$@"
