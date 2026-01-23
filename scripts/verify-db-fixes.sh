#!/bin/bash
# Verification script for database crash fixes
# Run on Linux server after deployment

set -e

echo "=== Database Crash Fix Verification ==="
echo "Date: $(date)"
echo ""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Check if running as root (required for systemctl)
if [ "$EUID" -ne 0 ]; then
   echo -e "${RED}ERROR: Please run as root${NC}"
   exit 1
fi

# Function to print status
print_status() {
    local status=$1
    local message=$2
    if [ "$status" = "OK" ]; then
        echo -e "${GREEN}✓${NC} $message"
    elif [ "$status" = "WARN" ]; then
        echo -e "${YELLOW}⚠${NC} $message"
    else
        echo -e "${RED}✗${NC} $message"
    fi
}

# 1. Check node is running
echo "1. Checking node status..."
if systemctl is-active --quiet avalanchego; then
    print_status "OK" "Node is running"
    UPTIME=$(systemctl show avalanchego -p ActiveEnterTimestamp --value)
    echo "   Uptime: $UPTIME"
else
    print_status "FAIL" "Node is not running"
    echo "   Run: systemctl start avalanchego"
    exit 1
fi

echo ""

# 2. Check memory usage
echo "2. Checking memory usage..."
TOTAL_MEM=$(free -m | awk '/^Mem:/{print $2}')
USED_MEM=$(free -m | awk '/^Mem:/{print $3}')
MEM_PERCENT=$(awk "BEGIN {printf \"%.1f\", ($USED_MEM/$TOTAL_MEM)*100}")

echo "   Total memory: ${TOTAL_MEM}MB"
echo "   Used memory: ${USED_MEM}MB (${MEM_PERCENT}%)"

if (( $(echo "$MEM_PERCENT < 80" | bc -l) )); then
    print_status "OK" "Memory usage is healthy (<80%)"
elif (( $(echo "$MEM_PERCENT < 90" | bc -l) )); then
    print_status "WARN" "Memory usage is elevated (80-90%)"
else
    print_status "FAIL" "Memory usage is critical (>90%)"
fi

echo ""

# 3. Check swap usage
echo "3. Checking swap usage..."
SWAP_USED=$(free -m | awk '/^Swap:/{print $3}')
if [ "$SWAP_USED" -eq 0 ]; then
    print_status "OK" "No swap usage detected"
else
    print_status "WARN" "Swap is being used: ${SWAP_USED}MB"
fi

echo ""

# 4. Check for crash indicators in logs
echo "4. Checking for crash indicators..."
CRASH_COUNT=$(journalctl -u avalanchego --since "24 hours ago" | grep -i "exit code" | wc -l)
if [ "$CRASH_COUNT" -eq 0 ]; then
    print_status "OK" "No crashes in last 24 hours"
else
    print_status "WARN" "Found $CRASH_COUNT crash(es) in last 24 hours"
    echo "   Recent crashes:"
    journalctl -u avalanchego --since "24 hours ago" | grep -i "exit code" | tail -3
fi

echo ""

# 5. Check for corruption indicators
echo "5. Checking for corruption indicators..."
CORRUPTION_COUNT=$(journalctl -u avalanchego --since "24 hours ago" | grep -i "corruption" | wc -l)
if [ "$CORRUPTION_COUNT" -eq 0 ]; then
    print_status "OK" "No corruption detected in last 24 hours"
else
    print_status "FAIL" "Found corruption indicators: $CORRUPTION_COUNT occurrences"
    echo "   Recent corruption events:"
    journalctl -u avalanchego --since "24 hours ago" | grep -i "corruption" | tail -3
fi

echo ""

# 6. Check for memory pressure warnings
echo "6. Checking for memory pressure warnings..."
PRESSURE_COUNT=$(journalctl -u avalanchego --since "24 hours ago" | grep -i "memory pressure" | wc -l)
if [ "$PRESSURE_COUNT" -eq 0 ]; then
    print_status "OK" "No memory pressure warnings"
else
    print_status "WARN" "Found $PRESSURE_COUNT memory pressure warning(s)"
    echo "   Recent warnings:"
    journalctl -u avalanchego --since "24 hours ago" | grep -i "memory pressure" | tail -3
fi

echo ""

# 7. Check database health check status
echo "7. Checking database health checks..."
HEALTH_PASS=$(journalctl -u avalanchego --since "1 hour ago" | grep -i "database health check passed" | wc -l)
HEALTH_FAIL=$(journalctl -u avalanchego --since "1 hour ago" | grep -i "database health check failed" | wc -l)

if [ "$HEALTH_FAIL" -eq 0 ]; then
    if [ "$HEALTH_PASS" -gt 0 ]; then
        print_status "OK" "Database health checks passing ($HEALTH_PASS checks in last hour)"
    else
        print_status "WARN" "No health check logs found in last hour"
    fi
else
    print_status "FAIL" "Database health checks failing ($HEALTH_FAIL failures in last hour)"
fi

echo ""

# 8. Check node metrics
echo "8. Checking node metrics..."
if command -v curl &> /dev/null; then
    METRICS=$(curl -s http://localhost:9650/ext/metrics 2>/dev/null)
    if [ $? -eq 0 ]; then
        print_status "OK" "Metrics endpoint responding"

        # Check for database metrics
        DB_METRICS=$(echo "$METRICS" | grep -E "leveldb_|db_" | head -5)
        if [ -n "$DB_METRICS" ]; then
            echo "   Sample database metrics:"
            echo "$DB_METRICS" | sed 's/^/     /'
        fi
    else
        print_status "WARN" "Metrics endpoint not responding"
    fi
else
    print_status "WARN" "curl not installed, skipping metrics check"
fi

echo ""

# 9. Summary and recommendations
echo "=== Summary ==="
echo ""

ALL_OK=true

# Check critical conditions
if (( $(echo "$MEM_PERCENT >= 90" | bc -l) )); then
    ALL_OK=false
    echo -e "${RED}CRITICAL: Memory usage critical (>90%)${NC}"
fi

if [ "$SWAP_USED" -gt 100 ]; then
    ALL_OK=false
    echo -e "${RED}CRITICAL: Swap usage detected (${SWAP_USED}MB)${NC}"
fi

if [ "$CRASH_COUNT" -gt 0 ]; then
    ALL_OK=false
    echo -e "${RED}CRITICAL: Node crashes detected in last 24 hours${NC}"
fi

if [ "$CORRUPTION_COUNT" -gt 0 ]; then
    ALL_OK=false
    echo -e "${RED}CRITICAL: Database corruption detected${NC}"
fi

if [ "$ALL_OK" = true ]; then
    echo -e "${GREEN}✓ All checks passed! Database fixes are working correctly.${NC}"
    echo ""
    echo "Recommendations:"
    echo "  - Continue monitoring for 24-48 hours"
    echo "  - Check this script output daily"
    echo "  - Monitor memory usage trends"
else
    echo -e "${RED}✗ Issues detected! Immediate attention required.${NC}"
    echo ""
    echo "Recommendations:"
    echo "  - Check detailed logs: journalctl -u avalanchego -n 1000"
    echo "  - Monitor memory: watch -n 5 'free -h'"
    echo "  - Consider rollback if issues persist"
fi

echo ""
echo "=== End of Verification ==="
