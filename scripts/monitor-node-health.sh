#!/bin/bash
# Comprehensive Node Health Monitor
# Watches for crashes, health check failures, progress stalls, and memory issues

MONITOR_LOG="/tmp/node-health-monitor.log"
ALERT_LOG="/tmp/node-health-alerts.log"
CHECK_INTERVAL=60  # Check every 60 seconds

# Initialize logs
echo "=== Node Health Monitor Started: $(date) ===" >> "$MONITOR_LOG"
echo "=== Alert Log: $(date) ===" >> "$ALERT_LOG"

# Track state between checks
LAST_RESTART_TIME=""
LAST_EXECUTION_BLOCKS=0
STALL_COUNT=0
LAST_PID=""

alert() {
    local severity="$1"
    local message="$2"
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] [$severity] $message" | tee -a "$ALERT_LOG" >> "$MONITOR_LOG"
}

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" >> "$MONITOR_LOG"
}

check_service_running() {
    if ! systemctl is-active --quiet avalanchego; then
        alert "CRITICAL" "Service is not running!"
        return 1
    fi
    return 0
}

check_for_restart() {
    local current_pid=$(pgrep -f "avalanchego --config")

    if [ -z "$current_pid" ]; then
        alert "CRITICAL" "No avalanchego process found!"
        return 1
    fi

    if [ -n "$LAST_PID" ] && [ "$current_pid" != "$LAST_PID" ]; then
        alert "CRITICAL" "Node restarted! Old PID: $LAST_PID, New PID: $current_pid"

        # Check journalctl for crash reason
        local crash_reason=$(journalctl -u avalanchego --since "5 minutes ago" | grep -i "fatal\|panic\|error" | tail -5)
        if [ -n "$crash_reason" ]; then
            alert "CRITICAL" "Crash reason: $crash_reason"
        fi
    fi

    LAST_PID="$current_pid"
    return 0
}

check_health_check_failures() {
    # Check for health check failures in last 2 minutes
    local health_failures=$(journalctl -u avalanchego --since "2 minutes ago" | grep -i "health check fail" | grep -v "temporarily closed")

    if [ -n "$health_failures" ]; then
        alert "CRITICAL" "Health check failure detected: $health_failures"
        return 1
    fi

    # Check for our new retry warnings (these are OK, but worth tracking)
    local retry_warnings=$(journalctl -u avalanchego --since "2 minutes ago" | grep "database temporarily closed")
    if [ -n "$retry_warnings" ]; then
        alert "INFO" "Health check retry triggered (this is OK): Database temporarily closed during registry save"
    fi

    return 0
}

check_execution_progress() {
    # Get latest execution progress
    local latest_progress=$(grep -a "executing blocks" /root/.avalanchego/logs/output.log 2>/dev/null | tail -1)

    if [ -z "$latest_progress" ]; then
        log "No execution progress found yet (node may be fetching blocks)"
        return 0
    fi

    # Extract block count
    local current_blocks=$(echo "$latest_progress" | grep -oP 'numExecuted":\s*\K\d+')

    if [ -z "$current_blocks" ]; then
        return 0
    fi

    # Check if progress stalled
    if [ "$current_blocks" -eq "$LAST_EXECUTION_BLOCKS" ]; then
        ((STALL_COUNT++))
        if [ "$STALL_COUNT" -ge 5 ]; then
            alert "WARNING" "Execution stalled for 5 minutes! Still at block $current_blocks"
        fi
    else
        if [ "$STALL_COUNT" -gt 0 ]; then
            log "Progress resumed: $LAST_EXECUTION_BLOCKS -> $current_blocks"
        fi
        STALL_COUNT=0
        LAST_EXECUTION_BLOCKS=$current_blocks

        # Log progress
        local progress_pct=$(echo "$latest_progress" | grep -oP 'pctComplete":\s*\K[\d.]+')
        log "Progress: $progress_pct% ($current_blocks blocks executed)"
    fi

    return 0
}

check_memory_usage() {
    local mem_info=$(free -g | grep Mem)
    local total=$(echo "$mem_info" | awk '{print $2}')
    local used=$(echo "$mem_info" | awk '{print $3}')
    local available=$(echo "$mem_info" | awk '{print $7}')

    # Alert if less than 10GB available
    if [ "$available" -lt 10 ]; then
        alert "WARNING" "Low memory: ${available}GB available (used: ${used}GB / ${total}GB)"
    fi

    # Check if avalanchego memory is excessive (>55GB)
    local process_mem=$(ps aux | grep "avalanchego --config" | grep -v grep | awk '{print $6}')
    if [ -n "$process_mem" ]; then
        local mem_gb=$((process_mem / 1024 / 1024))
        if [ "$mem_gb" -gt 55 ]; then
            alert "WARNING" "High process memory: ${mem_gb}GB"
        fi
    fi

    return 0
}

check_database_errors() {
    # Check for database corruption or serious errors
    local db_errors=$(journalctl -u avalanchego --since "2 minutes ago" | grep -i "corruption\|database.*error\|firewood.*error" | grep -v "temporarily closed")

    if [ -n "$db_errors" ]; then
        alert "CRITICAL" "Database error detected: $db_errors"
        return 1
    fi

    return 0
}

check_disk_space() {
    local disk_usage=$(df -h /root/.avalanchego/db | tail -1 | awk '{print $5}' | sed 's/%//')

    if [ "$disk_usage" -gt 90 ]; then
        alert "CRITICAL" "Disk usage critical: ${disk_usage}%"
    elif [ "$disk_usage" -gt 80 ]; then
        alert "WARNING" "Disk usage high: ${disk_usage}%"
    fi

    return 0
}

# Main monitoring loop
alert "INFO" "Health monitor started. Checking every ${CHECK_INTERVAL} seconds."

while true; do
    check_service_running
    check_for_restart
    check_health_check_failures
    check_execution_progress
    check_memory_usage
    check_database_errors
    check_disk_space

    sleep "$CHECK_INTERVAL"
done
