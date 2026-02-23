#!/bin/bash
# P-Chain Bootstrap Progress Tracker
# Monitors P-Chain sync progress with ETA calculations

API_HOST="${AVALANCHE_API_HOST:-localhost:9650}"
DB_DIR="${AVALANCHE_DB_DIR:-/root/.avalanchego/db}"
STATE_FILE="/tmp/p-chain-progress.state"

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

get_p_chain_height() {
    curl -s -X POST "http://${API_HOST}/ext/bc/P" \
        -H 'Content-Type: application/json' \
        -d '{"jsonrpc":"2.0","method":"platform.getHeight","params":{},"id":1}' \
        2>/dev/null | jq -r '.result.height // 0'
}

get_bootstrap_status() {
    curl -s -X POST "http://${API_HOST}/ext/info" \
        -H 'Content-Type: application/json' \
        -d '{"jsonrpc":"2.0","method":"info.isBootstrapped","params":{"chain":"P"},"id":1}' \
        2>/dev/null | jq -r '.result.isBootstrapped // false'
}

get_db_size() {
    if [ -d "$DB_DIR" ]; then
        du -sh "$DB_DIR" 2>/dev/null | awk '{print $1}'
    else
        echo "N/A"
    fi
}

format_number() {
    echo "$1" | sed ':a;s/\B[0-9]\{3\}\>/,&/;ta'
}

calculate_eta() {
    local current=$1
    local rate=$2
    local target=24500000

    if (( $(echo "$rate > 0" | bc -l) )); then
        local remaining=$((target - current))
        local seconds=$(echo "$remaining / $rate" | bc -l)
        local hours=$(echo "$seconds / 3600" | bc -l)
        local days=$(echo "$hours / 24" | bc -l)

        if (( $(echo "$days >= 1" | bc -l) )); then
            printf "%.1f days" "$days"
        else
            printf "%.1f hours" "$hours"
        fi
    else
        echo "calculating..."
    fi
}

main() {
    local current_height=$(get_p_chain_height)
    local is_bootstrapped=$(get_bootstrap_status)
    local db_size=$(get_db_size)
    local current_time=$(date +%s)

    # Check if node is responding
    if [ "$current_height" -eq 0 ]; then
        echo -e "${RED}✗${NC} Node not responding at http://${API_HOST}"
        exit 1
    fi

    # Display header
    echo -e "${BLUE}═══════════════════════════════════════════════════════${NC}"
    echo -e "${BLUE}  P-Chain Bootstrap Progress${NC}"
    echo -e "${BLUE}═══════════════════════════════════════════════════════${NC}"
    echo ""

    # Bootstrap status
    if [ "$is_bootstrapped" = "true" ]; then
        echo -e "${GREEN}✓ BOOTSTRAPPED${NC}"
        echo ""
        echo -e "Final Height: $(format_number $current_height)"
        echo -e "Database Size: $db_size"
        [ -f "$STATE_FILE" ] && rm "$STATE_FILE"
        exit 0
    fi

    # Current progress
    local progress=$(echo "scale=2; $current_height / 24500000 * 100" | bc -l)
    echo -e "Height:    $(format_number $current_height) / 24,500,000 (${progress}%)"
    echo -e "Database:  $db_size / ~250GB"

    # Calculate sync rate from previous run
    if [ -f "$STATE_FILE" ]; then
        local prev_height=$(cat "$STATE_FILE" | cut -d',' -f1)
        local prev_time=$(cat "$STATE_FILE" | cut -d',' -f2)
        local time_diff=$((current_time - prev_time))
        local height_diff=$((current_height - prev_height))

        if [ "$time_diff" -gt 0 ] && [ "$height_diff" -gt 0 ]; then
            local rate=$(echo "scale=2; $height_diff / $time_diff" | bc -l)
            local eta=$(calculate_eta "$current_height" "$rate")

            echo ""
            echo -e "Sync Rate: ${rate} blocks/sec"
            echo -e "ETA:       ${eta}"
        fi
    fi

    # Save state for next run
    echo "${current_height},${current_time}" > "$STATE_FILE"

    echo ""
    echo -e "${BLUE}═══════════════════════════════════════════════════════${NC}"
}

# Run main
main
