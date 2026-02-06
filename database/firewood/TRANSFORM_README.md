# Firewood Iterator Key Transformation

## Quick Reference

### Purpose
Transform Firewood's wrapped iterator keys (97-129 bytes) into expected ExpiryEntry format (40 bytes).

### Files
- `transform_key.go` - Key extraction and validation logic
- `iterator.go` - Integration point (Next() method, line 126-128)

## Key Transformation Flow

```
Firewood Iterator
      ↓
Raw Key (97/124/129 bytes with metadata)
      ↓
transformIteratorKey()
      ↓
Try 3 Extraction Strategies:
  1. Last 40 bytes  ← Most common
  2. First 40 bytes
  3. Sliding window
      ↓
Validate with looksLikeExpiryEntry()
      ↓
Valid ExpiryEntry (40 bytes)
  [8-byte timestamp][32-byte validation ID]
```

## ExpiryEntry Structure

```go
type ExpiryEntry struct {
    Timestamp    uint64  // 8 bytes (big-endian)
    ValidationID ids.ID  // 32 bytes
}
// Total: 40 bytes
```

## Validation Rules

### Timestamp (first 8 bytes)
- ✓ Must be >= 1577836800 (2020-01-01)
- ✓ Must be <= 4102444800 (2100-01-01)
- ✗ Random bytes likely fail this check

### Validation ID (last 32 bytes)
- ✓ Must have at least one non-zero byte
- ✗ All zeros (0x00...) rejected
- ✗ All ones (0xFF...) rejected

## Example Transformation

### Input (97 bytes)
```
[57 bytes metadata][8 bytes timestamp][32 bytes validation ID]
└─────────────────┘└──────────────────────────────────────────┘
   Firewood internal        Expected ExpiryEntry (40 bytes)
```

### Output (40 bytes)
```
Timestamp:    1735516800 → "2024-12-30T00:00:00Z"
ValidationID: a1b2c3d4e5f6...0123456789abcdef (32 bytes)
```

## Debug Logging

Enable with `log-level: debug` in avalanchego config:

### Successful Extraction
```
"Iterator key longer than expected - attempting extraction"
  keyLen: 97, expected: 40, extraBytes: 57

"Successfully extracted ExpiryEntry from last 40 bytes"
  originalLen: 97
  extractedHex: "0000000067a2e00012345678..."

"Data looks like valid ExpiryEntry"
  timestamp: 1735516800
  timestampDate: "2024-12-30T00:00:00Z"
  validationIDHex: "12345678..."
```

### Failed Extraction
```
"Timestamp out of reasonable range"
  timestamp: 999999999999 (year 2286 - suspicious)

"Could not extract valid ExpiryEntry from iterator key"
  keyLen: 97
  keyHex: [raw hex for analysis]
```

## Performance

- **Fast path** (key == 40 bytes): Return immediately
- **Extraction** (~100 byte key): ~3 attempts max
- **Validation**: Simple numeric comparisons
- **Memory**: One 40-byte slice allocation

## Error Handling

If no valid ExpiryEntry found:
1. Returns original key unchanged
2. Logs warning with key hex
3. Caller receives original error (e.g., "expected expiry entry length 40")
4. Preserves existing error handling paths

## Testing

### Syntax Check (Windows)
```bash
cd /c/Projects/avalanchego
export PATH=$HOME/go/bin:$PATH
go fmt ./database/firewood/transform_key.go
go fmt ./database/firewood/iterator.go
```

### Full Compile (Linux)
```bash
ssh rpc
cd /root/avalanchego-dev/avalanchego
/usr/local/go/bin/go build -o build/avalanchego ./main
```

### Runtime Test
```bash
# Start avalanchego with debug logging
# Monitor for transformation logs
# Verify no "expected expiry entry length 40" errors
```

## Common Issues

### Issue: Keys still wrong length
**Check**: Are transformation logs appearing?
**Fix**: Verify transformIteratorKey() is being called

### Issue: "Timestamp out of reasonable range"
**Check**: Is the extracted data actually a timestamp?
**Fix**: May need to adjust extraction strategy or timestamp bounds

### Issue: All extractions fail
**Check**: Debug logs show what's being rejected
**Fix**: May need new extraction strategy for different metadata pattern

## Code References

### Main Functions
```go
// Extract 40-byte payload from wrapped key
func transformIteratorKey(rawKey []byte, log logging.Logger) []byte

// Validate extracted data looks like ExpiryEntry
func looksLikeExpiryEntry(data []byte, log logging.Logger) bool
```

### Integration Point
```go
// iterator.go, Next() method
rawKey := it.fw.Key()
it.currentKey = transformIteratorKey(rawKey, it.log)  // ← Transformation
```

### Constants
```go
const (
    expectedExpiryEntryLength = 40
    minReasonableTimestamp    = 1577836800  // 2020-01-01
    maxReasonableTimestamp    = 4102444800  // 2100-01-01
)
```

## Upstream Source

**ExpiryEntry Definition**:
- File: `vms/platformvm/state/expiry.go`
- Lines: 45-55
- Structure: timestamp (8 bytes) + validationID (32 bytes)

## Phase History

- **Phase 1**: Firewood adapter with pending batch and merge iterator
  - Status: ✅ Complete
  - Issue: Iterator returns wrong key length

- **Phase 2**: Iterator key transformation (this implementation)
  - Status: ✅ Implementation complete, awaiting testing
  - Fix: Extract 40-byte ExpiryEntry from wrapped keys
