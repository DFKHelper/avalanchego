#!/bin/bash
# Deploy database crash fixes to production server
#
# This script:
# 1. Copies modified files to server
# 2. Builds the binary on server (Linux CGO required)
# 3. Stops avalanchego service
# 4. Backs up current binary
# 5. Replaces binary
# 6. Starts service
# 7. Monitors logs for stability

set -e

SERVER="rpc"
REMOTE_DIR="/root/avalanchego-dev/avalanchego"
BINARY_PATH="/usr/local/bin/avalanchego"
BACKUP_DIR="/root/avalanchego-backups"

echo "==================================="
echo "Deploying Database Crash Fixes"
echo "==================================="
echo ""
echo "Changes:"
echo "  - Reduce memory: 8GB→2GB cache, 512MB→256MB buffer"
echo "  - Add memory pressure monitoring"
echo "  - Add config validation"
echo "  - Enhanced corruption recovery"
echo "  - Database health checks"
echo ""

# Step 1: Copy modified files
echo "[1/7] Copying modified files to server..."
scp database/leveldb/db.go $SERVER:$REMOTE_DIR/database/leveldb/
scp database/leveldb/validate.go $SERVER:$REMOTE_DIR/database/leveldb/
scp database/leveldb/validate_test.go $SERVER:$REMOTE_DIR/database/leveldb/
scp database/corruptabledb/db.go $SERVER:$REMOTE_DIR/database/corruptabledb/
scp node/node.go $SERVER:$REMOTE_DIR/node/

echo "✓ Files copied"
echo ""

# Step 2: Build on server
echo "[2/7] Building binary on server..."
ssh $SERVER "cd $REMOTE_DIR && /usr/local/go/bin/go build -o build/avalanchego ./main"

if [ $? -ne 0 ]; then
    echo "✗ Build failed!"
    exit 1
fi

echo "✓ Build successful"
echo ""

# Step 3: Run tests
echo "[3/7] Running validation tests..."
ssh $SERVER "cd $REMOTE_DIR && /usr/local/go/bin/go test -v ./database/leveldb/... -run TestValidate"

if [ $? -ne 0 ]; then
    echo "✗ Tests failed!"
    echo "Aborting deployment"
    exit 1
fi

echo "✓ Tests passed"
echo ""

# Step 4: Stop service
echo "[4/7] Stopping avalanchego service..."
ssh $SERVER "systemctl stop avalanchego"
sleep 5
echo "✓ Service stopped"
echo ""

# Step 5: Backup current binary
echo "[5/7] Backing up current binary..."
BACKUP_NAME="avalanchego-$(date +%Y%m%d-%H%M%S)"
ssh $SERVER "mkdir -p $BACKUP_DIR && cp $BINARY_PATH $BACKUP_DIR/$BACKUP_NAME"
echo "✓ Backup created: $BACKUP_DIR/$BACKUP_NAME"
echo ""

# Step 6: Replace binary
echo "[6/7] Installing new binary..."
ssh $SERVER "cp $REMOTE_DIR/build/avalanchego $BINARY_PATH"
echo "✓ Binary installed"
echo ""

# Step 7: Start service
echo "[7/7] Starting avalanchego service..."
ssh $SERVER "systemctl start avalanchego"
sleep 10

# Verify service started
ssh $SERVER "systemctl is-active avalanchego" > /dev/null 2>&1
if [ $? -eq 0 ]; then
    echo "✓ Service started successfully"
else
    echo "✗ Service failed to start!"
    echo "Rolling back to previous binary..."
    ssh $SERVER "cp $BACKUP_DIR/$BACKUP_NAME $BINARY_PATH && systemctl start avalanchego"
    exit 1
fi

echo ""
echo "==================================="
echo "Deployment Complete!"
echo "==================================="
echo ""
echo "Monitoring logs for 60 seconds..."
echo ""

# Monitor logs
ssh $SERVER "timeout 60 journalctl -u avalanchego -f --since '1 minute ago'" || true

echo ""
echo "==================================="
echo "Next Steps:"
echo "==================================="
echo "1. Monitor logs: ssh $SERVER 'journalctl -u avalanchego -f'"
echo "2. Check memory: ssh $SERVER 'free -h'"
echo "3. Verify no swap: ssh $SERVER 'free -h | grep Swap'"
echo "4. Monitor for 24-48 hours before considering stable"
echo ""
echo "Rollback if needed:"
echo "  ssh $SERVER 'systemctl stop avalanchego && cp $BACKUP_DIR/$BACKUP_NAME $BINARY_PATH && systemctl start avalanchego'"
echo ""
