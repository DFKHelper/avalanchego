#!/usr/bin/env python3
"""Install dfk-* helper commands on the server."""
import os

scripts = {}

scripts["/usr/local/bin/dfk-peers"] = """\
#!/bin/bash
DFK_SUBNET="Vn3aX6hNRstj5VHHm63TCgPNaeGnRSqCYXQqemSqDd2TQH4qJ"
NODE_API="http://localhost:9650"
echo "📡 DFK SUBNET PEERS"
echo "═══════════════════════════════════════════════════════════"
echo ""
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

curl -sf --max-time 10 -X POST "${NODE_API}/ext/bc/P" \\
    -H "Content-Type: application/json" \\
    -d '{"jsonrpc":"2.0","id":1,"method":"platform.getCurrentValidators","params":{"subnetID":"'"${DFK_SUBNET}"'"}}' \\
    2>/dev/null > "$TMP/validators.json"

curl -sf --max-time 10 -X POST "${NODE_API}/ext/info" \\
    -H "Content-Type: application/json" \\
    -d '{"jsonrpc":"2.0","id":1,"method":"info.peers","params":{"nodeIDs":[]}}' \\
    2>/dev/null > "$TMP/peers.json"

if [ ! -s "$TMP/validators.json" ] || [ ! -s "$TMP/peers.json" ]; then
    echo "   ⚠️  Could not reach node API"; exit 1
fi

python3 - "$TMP/validators.json" "$TMP/peers.json" << 'PY'
import json, sys
from datetime import datetime, timezone
with open(sys.argv[1]) as f: v_data = json.load(f)
with open(sys.argv[2]) as f: p_data = json.load(f)
validators = v_data.get("result", {}).get("validators", [])
peers = {p["nodeID"]: p for p in p_data.get("result", {}).get("peers", [])}
connected = 0
total = len(validators)
for v in sorted(validators, key=lambda x: x.get("nodeID", "")):
    nid = v.get("nodeID", "?")
    end_ts = int(v.get("endTime", 0))
    end_dt = datetime.fromtimestamp(end_ts, tz=timezone.utc).strftime("%Y-%m-%d") if end_ts else "?"
    stake = int(v.get("weight", v.get("stakeAmount", 0)))
    stake_avax = stake / 1e9
    if nid in peers:
        p = peers[nid]
        ip = p.get("ip", "?")
        version = p.get("version", "?")
        print(f"   \\u2705  {nid}")
        print(f"       IP: {ip}  version: {version}  stake: {stake_avax:.0f} AVAX  expires: {end_dt}")
        connected += 1
    else:
        print(f"   \\u274c  {nid}")
        print(f"       (not connected)  stake: {stake_avax:.0f} AVAX  expires: {end_dt}")
    print()
print(f"   Connected: {connected}/{total} DFK validators")
PY
echo ""
echo "═══════════════════════════════════════════════════════════"
echo "Updated: $(date '+%Y-%m-%d %H:%M:%S')"
"""

scripts["/usr/local/bin/dfk-block"] = """\
#!/bin/bash
DFK_ID="q2aTwKuyzgs8pynF7UXBZCU7DejbZbZ6EUyHr3JQzYgwNPUPi"
RPC="http://localhost:9650/ext/bc/${DFK_ID}/rpc"
echo "🔗 DFK CURRENT BLOCK"
echo "═══════════════════════════════════════════════════════════"
echo ""
TMP=$(mktemp)
trap 'rm -f "$TMP"' EXIT

curl -sf --max-time 5 -X POST "$RPC" \\
    -H "Content-Type: application/json" \\
    -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' \\
    2>/dev/null > "$TMP"

BLOCK_HEX=$(python3 -c "import json,sys; d=json.load(open('$TMP')); print(d['result'])" 2>/dev/null)
if [ -z "$BLOCK_HEX" ]; then echo "   ⚠️  Could not reach RPC"; exit 1; fi
BLOCK_NUM=$(( $BLOCK_HEX ))

curl -sf --max-time 5 -X POST "$RPC" \\
    -H "Content-Type: application/json" \\
    --data-raw '{"jsonrpc":"2.0","method":"eth_getBlockByNumber","params":["'"$BLOCK_HEX"'",false],"id":1}' \\
    2>/dev/null > "$TMP"

python3 - "$BLOCK_NUM" "$TMP" << 'PY'
import json, sys
from datetime import datetime, timezone
block_num = sys.argv[1]
with open(sys.argv[2]) as f: data = json.load(f)
b = data.get("result") or {}
ts = int(b.get("timestamp", "0x0"), 16)
dt = datetime.fromtimestamp(ts, tz=timezone.utc).strftime("%Y-%m-%d %H:%M:%S UTC") if ts else "?"
txcount = len(b.get("transactions", []))
gu = int(b.get("gasUsed", "0x0"), 16)
gl = int(b.get("gasLimit", "0x1"), 16)
pct = gu / gl * 100
h = b.get("hash", "?")
print(f"   Height:     {block_num}")
print(f"   Timestamp:  {dt}")
print(f"   Tx count:   {txcount}")
print(f"   Gas used:   {gu:,} / {gl:,} ({pct:.1f}%)")
print(f"   Hash:       {h[:20]}...")
PY
echo ""
echo "═══════════════════════════════════════════════════════════"
echo "Updated: $(date '+%Y-%m-%d %H:%M:%S')"
"""

scripts["/usr/local/bin/dfk-errors"] = """\
#!/bin/bash
DFK_LOG="/root/.avalanchego/logs/q2aTwKuyzgs8pynF7UXBZCU7DejbZbZ6EUyHr3JQzYgwNPUPi.log"
MAIN_LOG="/root/.avalanchego/logs/main.log"
LINES="${1:-200}"
echo "⚠️  RECENT ERRORS & WARNINGS (last ${LINES} lines per log)"
echo "═══════════════════════════════════════════════════════════"
echo ""
echo "--- DFK chain log ---"
tail -"${LINES}" "$DFK_LOG" 2>/dev/null \\
    | grep -E '\\b(ERROR|WARN)\\b' \\
    | grep -v "execution reverted\\|ERC20\\|insufficient funds" \\
    | tail -30 || echo "   (none)"
echo ""
echo "--- Main node log ---"
tail -"${LINES}" "$MAIN_LOG" 2>/dev/null \\
    | grep -E '\\b(ERROR|WARN)\\b' \\
    | tail -20 || echo "   (none)"
echo ""
echo "═══════════════════════════════════════════════════════════"
echo "Updated: $(date '+%Y-%m-%d %H:%M:%S')"
echo "Tip: dfk-errors 500  — scan last 500 lines"
"""

scripts["/usr/local/bin/dfk-metrics"] = """\
#!/bin/bash
DFK_ID="q2aTwKuyzgs8pynF7UXBZCU7DejbZbZ6EUyHr3JQzYgwNPUPi"
DFK_SUBNET="Vn3aX6hNRstj5VHHm63TCgPNaeGnRSqCYXQqemSqDd2TQH4qJ"
VM_PREFIX="mDV3QWRXfwgKUWb9sggkv4vQxAQR4y2CyKrt5pLZ5SzQ7EHBv"
echo "📊 DFK NODE METRICS"
echo "═══════════════════════════════════════════════════════════"
echo ""
TMP=$(mktemp)
trap 'rm -f "$TMP"' EXIT
curl -sf --max-time 10 http://localhost:9650/ext/metrics 2>/dev/null > "$TMP"
if [ ! -s "$TMP" ]; then echo "   ⚠️  Metrics endpoint unreachable"; exit 1; fi

python3 - "$TMP" "$DFK_ID" "$DFK_SUBNET" "$VM_PREFIX" << 'PY'
import sys, re

with open(sys.argv[1]) as f:
    lines = f.read().splitlines()
dfk_id = sys.argv[2]
dfk_subnet = sys.argv[3]
vm_pfx = sys.argv[4]

def get(name):
    for l in lines:
        if l.startswith(name + " ") and not l.startswith("#"):
            return l.split()[-1]
    return None

def getl(name, label):
    for l in lines:
        if l.startswith(name + "{") and label in l:
            return l.split()[-1]
    return None

def getl_vm(suffix, label):
    name = f"avalanche_{vm_pfx}_process_{suffix}"
    return getl(name, label)

def sum_msgs(io_val):
    total = 0
    for l in lines:
        if l.startswith("avalanche_network_msgs{") and f'io="{io_val}"' in l:
            try: total += float(l.split()[-1])
            except: pass
    return total

def fmt_num(v):
    try: return f"{float(v):,.0f}"
    except: return str(v) if v else "?"

def fmt_mb(v):
    try: return f"{float(v)/1024/1024:.1f} MB"
    except: return "?"

print("📦 CHAIN STATUS")
bs = getl("avalanche_snowman_bootstrap_finished", dfk_id)
print(f"   Bootstrap:       {'✅ complete' if bs == '1' else '⏳ in progress'}")
print()

print("🌐 NETWORK")
print(f"   Total peers:     {get('avalanche_network_peers') or '?'}")
print(f"   DFK subnet:      {getl('avalanche_network_peers_subnet', dfk_subnet) or '?'}/6")
print(f"   Tracked peers:   {get('avalanche_network_tracked_peers') or '?'}")
print()

print("📨 MESSAGES (since start)")
sent = sum_msgs("sent")
recv = sum_msgs("received")
print(f"   Sent:            {sent:,.0f}")
print(f"   Received:        {recv:,.0f}")
print()

print("💾 MEMORY  (DFK VM process)")
print(f"   Heap alloc:      {fmt_mb(getl_vm('go_memstats_alloc_bytes', dfk_id))}")
sys_bytes = getl_vm('go_memstats_sys_bytes', dfk_id) or getl_vm('go_memstats_heap_sys_bytes', dfk_id)
print(f"   System total:    {fmt_mb(sys_bytes)}")
print()

print("⚙️  GOROUTINES  (DFK VM process)")
print(f"   Active:          {getl_vm('go_goroutines', dfk_id) or '?'}")
PY
echo ""
echo "═══════════════════════════════════════════════════════════"
echo "Updated: $(date '+%Y-%m-%d %H:%M:%S')"
"""

scripts["/usr/local/bin/dfk-restart"] = """\
#!/bin/bash
echo "🔄 DFK NODE SAFE RESTART"
echo "═══════════════════════════════════════════════════════════"
echo ""
echo "   This will restart avalanchego via systemd."
echo "   The watchdog will resume monitoring automatically."
echo ""
read -rp "   Confirm restart? [y/N] " CONFIRM
echo ""
if [[ "$CONFIRM" != "y" && "$CONFIRM" != "Y" ]]; then
    echo "   Cancelled."; exit 0
fi
echo "   Stopping node..."
systemctl stop avalanchego
echo "   Waiting for process to exit..."
for i in $(seq 1 15); do pgrep -f avalanchego > /dev/null || break; sleep 1; done
echo "   Starting node..."
systemctl start avalanchego
echo "   Waiting for API to come up..."
for i in $(seq 1 60); do
    if curl -sf --max-time 2 http://localhost:9650/ext/metrics > /dev/null 2>&1; then
        echo "   ✅ Node is up (${i}s)"; echo ""; dfk-block; exit 0
    fi
    sleep 1
done
echo "   ⚠️  API did not respond within 60s — check: dfk-logs"
exit 1
"""

for path, content in scripts.items():
    fname = path.split("/")[-1]
    with open(fname, "w", newline="\n", encoding="utf-8") as f:
        f.write(content)
    os.chmod(fname, 0o755)
    print(f"wrote {fname}")
