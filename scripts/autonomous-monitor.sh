#!/bin/bash
# Autonomous Node Monitoring Agent
# Handles issues automatically without user intervention

REPORT_LOG="/tmp/autonomous-monitor-report.log"
ALERT_LOG="/tmp/node-health-alerts.log"
MONITOR_LOG="/tmp/node-health-monitor.log"
CHECK_INTERVAL=300  # 5 minutes
REPORT_INTERVAL=1800  # 30 minutes
LAST_REPORT_TIME=$(date +%s)
LAST_PROGRESS=""

echo "=== Autonomous Monitor Started: $(date) ===" >> "$REPORT_LOG"
echo "Check interval: ${CHECK_INTERVAL}s, Report interval: ${REPORT_INTERVAL}s" >> "$REPORT_LOG"

log_action() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" | tee -a "$REPORT_LOG"
}

handle_critical_alert() {
    local alert="$1"
    log_action "CRITICAL ALERT DETECTED: $alert"

    # Check if service crashed
    if echo "$alert" | grep -q "Node restarted\|not running"; then
        log_action "ACTION: Service appears to have crashed. Checking status..."

        if ! systemctl is-active --quiet avalanchego; then
            log_action "ACTION: Service is down. Attempting restart..."
            systemctl start avalanchego
            sleep 10

            if systemctl is-active --quiet avalanchego; then
                log_action "SUCCESS: Service restarted successfully"
            else
                log_action "FAILED: Could not restart service. Manual intervention required."
            fi
        else
            log_action "INFO: Service is running (likely recovered automatically)"
        fi
    fi

    # Check for health check failures
    if echo "$alert" | grep -q "Health check fail"; then
        log_action "ACTION: Health check failure detected. Analyzing..."

        # Get recent logs
        recent_logs=$(journalctl -u avalanchego --since "5 minutes ago" | tail -20)

        if echo "$recent_logs" | grep -q "database temporarily closed"; then
            log_action "ANALYSIS: Temporary database closure (registry save). This is expected with our fix."
        else
            log_action "ANALYSIS: Non-standard health check failure. Recent logs:"
            echo "$recent_logs" >> "$REPORT_LOG"
        fi
    fi

    # Check for database corruption
    if echo "$alert" | grep -q "corruption\|Database error"; then
        log_action "CRITICAL: Database corruption detected!"
        log_action "ACTION: Collecting diagnostic information..."

        # Collect diagnostics
        df -h /root/.avalanchego/db >> "$REPORT_LOG"
        ls -lh /root/.avalanchego/db/mainnet/db/11111111111111111111111111111111LpoYY/ >> "$REPORT_LOG"

        log_action "RECOMMENDATION: Manual inspection required. Database may need restoration from backup."
    fi
}

check_disk_space() {
    local usage=$(df /root/.avalanchego/db | tail -1 | awk '{print $5}' | sed 's/%//')

    if [ "$usage" -gt 90 ]; then
        log_action "CRITICAL: Disk usage at ${usage}%. Taking action..."

        # Find old log files
        old_logs=$(find /root/.avalanchego/logs -name "*.log" -mtime +7 -size +100M)
        if [ -n "$old_logs" ]; then
            log_action "ACTION: Compressing old logs..."
            echo "$old_logs" | while read logfile; do
                gzip "$logfile" 2>/dev/null && log_action "Compressed: $logfile"
            done
        fi

        usage_after=$(df /root/.avalanchego/db | tail -1 | awk '{print $5}' | sed 's/%//')
        log_action "Disk usage after cleanup: ${usage_after}%"
    fi
}

check_progress() {
    # Get latest progress
    local current_progress=$(grep "Progress:" "$MONITOR_LOG" 2>/dev/null | tail -1)

    if [ -z "$current_progress" ]; then
        return
    fi

    if [ -n "$LAST_PROGRESS" ] && [ "$current_progress" = "$LAST_PROGRESS" ]; then
        log_action "WARNING: No progress detected since last check"
        log_action "Last known: $LAST_PROGRESS"

        # Check if process is hung
        if ! systemctl is-active --quiet avalanchego; then
            log_action "CRITICAL: Service is not running!"
            handle_critical_alert "Service not running"
        else
            # Check CPU usage
            cpu_usage=$(ps aux | grep "avalanchego --config" | grep -v grep | awk '{print $3}')
            log_action "Process CPU usage: ${cpu_usage}%"

            if (( $(echo "$cpu_usage < 5" | bc -l) )); then
                log_action "WARNING: Low CPU usage suggests process may be stuck"
            fi
        fi
    else
        LAST_PROGRESS="$current_progress"
    fi
}

generate_status_report() {
    log_action "=== STATUS REPORT ==="

    # Service status
    if systemctl is-active --quiet avalanchego; then
        log_action "Service: RUNNING"
        local uptime=$(systemctl show avalanchego --property=ActiveEnterTimestamp | cut -d= -f2)
        log_action "Uptime: Since $uptime"
    else
        log_action "Service: NOT RUNNING"
    fi

    # Latest progress
    local progress=$(grep "Progress:" "$MONITOR_LOG" 2>/dev/null | tail -1)
    if [ -n "$progress" ]; then
        log_action "Latest: $progress"
    else
        log_action "Progress: No data yet"
    fi

    # Memory usage
    local mem=$(free -h | grep Mem | awk '{print "Used: " $3 " / " $2 " (Available: " $7 ")"}')
    log_action "Memory: $mem"

    # Disk usage
    local disk=$(df -h /root/.avalanchego/db | tail -1 | awk '{print "Used: " $3 " / " $2 " (" $5 ")"}')
    log_action "Disk: $disk"

    # Alert summary
    local critical_count=$(grep -c "CRITICAL" "$ALERT_LOG" 2>/dev/null || echo 0)
    local warning_count=$(grep -c "WARNING" "$ALERT_LOG" 2>/dev/null || echo 0)
    log_action "Alerts: $critical_count critical, $warning_count warnings"

    log_action "Next check: $(date -d "+${CHECK_INTERVAL} seconds" '+%H:%M:%S')"
    log_action "====================="
}

# Main monitoring loop
while true; do
    # Check for critical alerts
    if [ -f "$ALERT_LOG" ]; then
        new_criticals=$(grep "CRITICAL" "$ALERT_LOG" | tail -5)
        if [ -n "$new_criticals" ]; then
            echo "$new_criticals" | while read alert; do
                handle_critical_alert "$alert"
            done
        fi
    fi

    # Check execution progress
    check_progress

    # Check disk space
    check_disk_space

    # Generate status report every 30 minutes
    current_time=$(date +%s)
    time_since_report=$((current_time - LAST_REPORT_TIME))

    if [ $time_since_report -ge $REPORT_INTERVAL ]; then
        generate_status_report
        LAST_REPORT_TIME=$current_time
    fi

    sleep $CHECK_INTERVAL
done
