#!/bin/bash
# Firewood PR Evidence Collector v3
# Captures time-series data to support the Firewood PR submission
# Runs every 5 minutes via cron; writes CSV to /root/firewood-pr-data/
set -uo pipefail

DATA_DIR="/root/firewood-pr-data"
METRICS_CSV="${DATA_DIR}/metrics.csv"
NODE_API="http://localhost:9650"
DFK_CHAIN="q2aTwKuyzgs8pynF7UXBZCU7DejbZbZ6EUyHr3JQzYgwNPUPi"
DFK_VM_PREFIX="mDV3QWRXfwgKUWb9sggkv4vQxAQR4y2CyKrt5pLZ5SzQ7EHBv"

mkdir -p "${DATA_DIR}"

# --- Header on first run ---
if [[ ! -f "${METRICS_CSV}" ]]; then
    echo "timestamp,unix_ts,dfk_tries_remaining,dfk_mode,peers,pchain_height,mem_used_mb,db_mainnet_gb,db_chaindata_gb,disk_used_pct,fw_commit_count,fw_commit_time_ns,fw_read_count,fw_read_time_ns,fw_propose_count,fw_hash_count,fw_cache_hit,fw_cache_miss,fw_io_read,fw_io_read_ms,fw_read_node_file,fw_read_node_cache,fw_propose_outstanding,node_up" \
        >> "${METRICS_CSV}"
fi

# --- Helpers ---
# Prometheus text format: "metric_name{labels} value [timestamp]"
# Value is always field $2 (labels are part of $1, no spaces in label block)
prom_val() {
    local name="$1" raw="$2"
    echo "${raw}" | grep "^${name}" | awk '{print $2}' | head -1
}

prom_label_val() {
    local name="$1" lkey="$2" lval="$3" raw="$4"
    echo "${raw}" | grep "^${name}{" | grep "${lkey}=\"${lval}\"" | awk '{print $2}' | head -1
}

# --- Collect ---
TS=$(date '+%Y-%m-%d %H:%M:%S')
UNIX_TS=$(date '+%s')

# Node metrics
NODE_UP=0
RAW_METRICS=$(curl -sf --max-time 10 "${NODE_API}/ext/metrics" 2>/dev/null) \
    && NODE_UP=1 || RAW_METRICS=""

# Peers
PEERS=0
if [[ "${NODE_UP}" == "1" ]]; then
    PEERS=$(prom_val "avalanche_network_peers " "${RAW_METRICS}") || true
fi
PEERS=${PEERS:-0}

# P-chain height (KEY EVIDENCE for rootStore fix: must never reset to 0 after restart)
# With rootStore bug: Get() returns nil after restart -> P-chain resets to height 0
# With rootStore fix: persisted revision loaded on open -> P-chain resumes correctly
PCHAIN_HEIGHT=0
if [[ "${NODE_UP}" == "1" ]]; then
    _ph=$(curl -sf --max-time 10 -X POST "${NODE_API}/ext/bc/P" \
        -H 'Content-Type: application/json' \
        -d '{"jsonrpc":"2.0","method":"platform.getHeight","params":{},"id":1}' 2>/dev/null \
        | python3 -c "import sys,json; print(json.load(sys.stdin)['result']['height'])" \
        2>/dev/null) || true
    PCHAIN_HEIGHT=${_ph:-0}
fi

# DFK sync progress
DFK_TRIES=0
DFK_MODE=$(cat /var/lib/avalanche-watchdog/dfk_mode 2>/dev/null || echo "unknown")
DFK_LOG="/root/.avalanchego/logs/${DFK_CHAIN}.log"
if [[ -f "${DFK_LOG}" ]]; then
    _tries=$(tail -100 "${DFK_LOG}" 2>/dev/null \
        | grep -oP 'triesRemaining=\K[0-9]+' 2>/dev/null | tail -1) || true
    DFK_TRIES=${_tries:-0}
fi

# Memory (MB used)
MEM_USED_MB=$(free -m 2>/dev/null | awk '/^Mem:/{print $3}') || MEM_USED_MB=0

# DB sizes (bytes -> GB)
DB_MAINNET_GB=$(du -sb /root/.avalanchego/db/mainnet/ 2>/dev/null \
    | awk '{printf "%.1f", $1/1073741824}') || DB_MAINNET_GB=0
DB_CHAINDATA_GB=$(du -sb /root/.avalanchego/chainData/ 2>/dev/null \
    | awk '{printf "%.1f", $1/1073741824}') || DB_CHAINDATA_GB=0
DISK_PCT=$(df / 2>/dev/null | tail -1 | awk '{print $5}' | tr -d '%') || DISK_PCT=0

# Firewood EVM triedb layer (commit/read/propose/hash)
# These metrics become non-zero once DFK chain enters normal block processing
EPREFIX="avalanche_${DFK_VM_PREFIX}_vm_eth_firewood_triedb_"
FW_COMMIT_COUNT=$(prom_val "${EPREFIX}commit_count{" "${RAW_METRICS}")
FW_COMMIT_TIME=$(prom_val "${EPREFIX}commit_time{" "${RAW_METRICS}")
FW_READ_COUNT=$(prom_val "${EPREFIX}read_count{" "${RAW_METRICS}")
FW_READ_TIME=$(prom_val "${EPREFIX}read_time{" "${RAW_METRICS}")
FW_PROPOSE_COUNT=$(prom_val "${EPREFIX}propose_count{" "${RAW_METRICS}")
FW_PROPOSE_OUTSTANDING=$(prom_val "${EPREFIX}propose_outstanding{" "${RAW_METRICS}")
FW_HASH_COUNT=$(prom_val "${EPREFIX}hash_count{" "${RAW_METRICS}")

# Firewood native IO/cache layer
FPREFIX="avalanche_${DFK_VM_PREFIX}_vm_firewood_firewood_"
FW_CACHE_HIT=$(prom_label_val "${FPREFIX}cache_node" "type" "hit" "${RAW_METRICS}")
FW_CACHE_MISS=$(prom_label_val "${FPREFIX}cache_node" "type" "miss" "${RAW_METRICS}")
FW_IO_READ=$(prom_val "${FPREFIX}io_read{" "${RAW_METRICS}")
FW_IO_READ_MS=$(prom_val "${FPREFIX}io_read_ms{" "${RAW_METRICS}")
FW_READ_NODE_FILE=$(prom_label_val "${FPREFIX}read_node" "from" "file" "${RAW_METRICS}")
FW_READ_NODE_CACHE=$(prom_label_val "${FPREFIX}read_node" "from" "cache" "${RAW_METRICS}")

# Defaults
FW_COMMIT_COUNT=${FW_COMMIT_COUNT:-0}; FW_COMMIT_TIME=${FW_COMMIT_TIME:-0}
FW_READ_COUNT=${FW_READ_COUNT:-0};     FW_READ_TIME=${FW_READ_TIME:-0}
FW_PROPOSE_COUNT=${FW_PROPOSE_COUNT:-0}; FW_PROPOSE_OUTSTANDING=${FW_PROPOSE_OUTSTANDING:-0}
FW_HASH_COUNT=${FW_HASH_COUNT:-0}
FW_CACHE_HIT=${FW_CACHE_HIT:-0};      FW_CACHE_MISS=${FW_CACHE_MISS:-0}
FW_IO_READ=${FW_IO_READ:-0};           FW_IO_READ_MS=${FW_IO_READ_MS:-0}
FW_READ_NODE_FILE=${FW_READ_NODE_FILE:-0}; FW_READ_NODE_CACHE=${FW_READ_NODE_CACHE:-0}

# --- Write row ---
echo "${TS},${UNIX_TS},${DFK_TRIES},${DFK_MODE},${PEERS},${PCHAIN_HEIGHT},${MEM_USED_MB},${DB_MAINNET_GB},${DB_CHAINDATA_GB},${DISK_PCT},${FW_COMMIT_COUNT},${FW_COMMIT_TIME},${FW_READ_COUNT},${FW_READ_TIME},${FW_PROPOSE_COUNT},${FW_HASH_COUNT},${FW_CACHE_HIT},${FW_CACHE_MISS},${FW_IO_READ},${FW_IO_READ_MS},${FW_READ_NODE_FILE},${FW_READ_NODE_CACHE},${FW_PROPOSE_OUTSTANDING},${NODE_UP}" \
    >> "${METRICS_CSV}"
