#!/bin/bash
# Build and test script for AvalancheGo infrastructure improvements
# Run this on Linux server after transferring files

set -e

echo "=== AvalancheGo Build and Test Script ==="
echo "Date: $(date)"
echo ""

# Configuration
GO_BIN="/usr/local/go/bin/go"
PROJECT_ROOT="/root/avalanchego-dev/avalanchego"
BUILD_DIR="$PROJECT_ROOT/build"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

print_step() {
    echo -e "${BLUE}==>${NC} $1"
}

print_success() {
    echo -e "${GREEN}✓${NC} $1"
}

print_error() {
    echo -e "${RED}✗${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}⚠${NC} $1"
}

# Step 1: Verify Go installation
print_step "Verifying Go installation..."
if [ ! -f "$GO_BIN" ]; then
    print_error "Go binary not found at $GO_BIN"
    exit 1
fi

GO_VERSION=$($GO_BIN version)
print_success "Go installed: $GO_VERSION"
echo ""

# Step 2: Verify project directory
print_step "Verifying project directory..."
if [ ! -d "$PROJECT_ROOT" ]; then
    print_error "Project root not found: $PROJECT_ROOT"
    exit 1
fi

cd "$PROJECT_ROOT"
print_success "Project directory: $PROJECT_ROOT"
echo ""

# Step 3: Run unit tests for database fixes
print_step "Running database unit tests..."
$GO_BIN test ./database/leveldb/ -v -run TestValidateConfig
if [ $? -eq 0 ]; then
    print_success "LevelDB validation tests passed"
else
    print_error "LevelDB validation tests failed"
    exit 1
fi

$GO_BIN test ./database/leveldb/ -v -run TestEstimateDatabaseMemory
if [ $? -eq 0 ]; then
    print_success "LevelDB memory estimation tests passed"
else
    print_error "LevelDB memory estimation tests failed"
    exit 1
fi
echo ""

# Step 4: Run integration tests (if on Linux with CGO)
print_step "Running integration tests..."
if [ -f "database/leveldb/integration_test.go" ]; then
    $GO_BIN test ./database/leveldb/ -tags=integration -v -run TestDatabaseLifecycle
    if [ $? -eq 0 ]; then
        print_success "Integration tests passed"
    else
        print_warning "Integration tests failed (may require CGO)"
    fi
else
    print_warning "Integration tests not found (optional)"
fi
echo ""

# Step 5: Run sync package tests
print_step "Running sync package tests..."
if [ -d "x/sync/evm" ]; then
    $GO_BIN test ./x/sync/evm/... -v
    if [ $? -eq 0 ]; then
        print_success "Sync package tests passed"
    else
        print_error "Sync package tests failed"
        exit 1
    fi
else
    print_warning "Sync package not found (may not be merged yet)"
fi
echo ""

# Step 6: Build with race detector (test build)
print_step "Building with race detector (test)..."
$GO_BIN build -race -o "$BUILD_DIR/avalanchego-race" ./main
if [ $? -eq 0 ]; then
    print_success "Race detector build successful"
    ls -lh "$BUILD_DIR/avalanchego-race"
else
    print_error "Race detector build failed"
    exit 1
fi
echo ""

# Step 7: Build production binary
print_step "Building production binary..."
$GO_BIN build -o "$BUILD_DIR/avalanchego" ./main
if [ $? -eq 0 ]; then
    print_success "Production build successful"
    ls -lh "$BUILD_DIR/avalanchego"

    # Verify binary
    file "$BUILD_DIR/avalanchego"

    # Calculate checksum
    MD5SUM=$(md5sum "$BUILD_DIR/avalanchego" | awk '{print $1}')
    print_success "MD5: $MD5SUM"
else
    print_error "Production build failed"
    exit 1
fi
echo ""

# Step 8: Verify binary size is reasonable
print_step "Verifying binary size..."
SIZE=$(stat -f%z "$BUILD_DIR/avalanchego" 2>/dev/null || stat -c%s "$BUILD_DIR/avalanchego")
SIZE_MB=$((SIZE / 1024 / 1024))

if [ $SIZE_MB -gt 50 ] && [ $SIZE_MB -lt 150 ]; then
    print_success "Binary size reasonable: ${SIZE_MB}MB"
elif [ $SIZE_MB -le 50 ]; then
    print_warning "Binary size small: ${SIZE_MB}MB (expected ~90MB)"
elif [ $SIZE_MB -ge 150 ]; then
    print_warning "Binary size large: ${SIZE_MB}MB (expected ~90MB)"
fi
echo ""

# Step 9: Summary
echo "=== Build Summary ==="
echo "✓ Unit tests: PASSED"
echo "✓ Production binary: BUILT"
echo "  Location: $BUILD_DIR/avalanchego"
echo "  Size: ${SIZE_MB}MB"
echo "  MD5: $MD5SUM"
echo ""

print_success "Build and test completed successfully!"
echo ""
echo "Next steps:"
echo "  1. Backup current binary: cp /usr/local/bin/avalanchego /usr/local/bin/avalanchego.backup-\$(date +%Y%m%d)"
echo "  2. Stop node: systemctl stop avalanchego"
echo "  3. Deploy: cp $BUILD_DIR/avalanchego /usr/local/bin/avalanchego"
echo "  4. Start node: systemctl start avalanchego"
echo "  5. Verify: ./scripts/verify-db-fixes.sh"
