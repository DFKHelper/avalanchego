// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package firewood

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/firewood-go-ethhash/ffi"
	"go.uber.org/zap"
)

const (
	// Name is the name of this database for database switches
	Name = "firewood"

	// DefaultFlushSize is the default number of operations before auto-flush
	DefaultFlushSize = 1000
)

// Database implements the database.Database interface using Firewood.
//
// Architecture: Batch-based adapter with auto-flush
// - Firewood uses proposal/commit pattern (batch operations)
// - database.Database expects immediate Put/Get operations
// - Adapter accumulates writes in pending batch
// - Auto-flushes when batch reaches threshold (default: 1000 ops)
// - Provides read-your-writes consistency by checking pending batch first
//
// See ARCHITECTURE_NOTES.md for detailed design rationale.
type Database struct {
	fw     *ffi.Database
	log    logging.Logger
	closed atomic.Bool

	// Pending batch tracking for auto-flush
	pendingMu    sync.Mutex
	pending      *pendingBatch // Accumulates writes until flush
	flushSize    int           // Auto-flush threshold
	flushOnClose bool          // Whether to flush pending writes on close
}

// pendingBatch tracks writes that haven't been committed to Firewood yet
type pendingBatch struct {
	ops map[string]*pendingOp // key -> operation (using string key for map)
}

type pendingOp struct {
	key    []byte
	value  []byte // nil for delete
	delete bool
}

func newPendingBatch() *pendingBatch {
	return &pendingBatch{
		ops: make(map[string]*pendingOp),
	}
}

// New creates a new Firewood database instance.
//
// Parameters:
//   - file: Path to database directory
//   - configBytes: JSON-encoded Config (see config.go)
//   - log: Logger instance
//
// Returns database.Database implementation or error if initialization fails.
func New(file string, configBytes []byte, log logging.Logger) (database.Database, error) {
	// Parse configuration
	var cfg Config
	if len(configBytes) > 0 {
		// The configBytes contains the full db-config.json structure like:
		// {"leveldb": {...}, "firewood": {...}, "pruning": {...}}
		// We need to extract just the "firewood" section
		var fullConfig map[string]json.RawMessage
		if err := json.Unmarshal(configBytes, &fullConfig); err != nil {
			return nil, fmt.Errorf("failed to parse database config: %w", err)
		}

		// Extract the "firewood" section if it exists
		if firewoodSection, exists := fullConfig["firewood"]; exists {
			if err := json.Unmarshal(firewoodSection, &cfg); err != nil {
				return nil, fmt.Errorf("failed to parse firewood config section: %w", err)
			}
		} else {
			// No firewood section, use defaults
			cfg = DefaultConfig()
		}
	} else {
		// Use default config if none provided
		cfg = DefaultConfig()
	}

	// Build FFI options from config
	options := []ffi.Option{
		ffi.WithNodeCacheEntries(cfg.CacheSizeBytes / 256), // ~256 bytes per node
		ffi.WithFreeListCacheEntries(cfg.FreeListCacheEntries),
		ffi.WithRevisions(cfg.RevisionsInMemory),
		ffi.WithReadCacheStrategy(cfg.CacheStrategy),
	}

	// Open Firewood database
	fw, err := ffi.New(file, options...)
	if err != nil {
		return nil, fmt.Errorf("failed to open firewood database: %w", err)
	}

	log.Info("Firewood database opened successfully")

	flushSize := cfg.FlushSize
	if flushSize == 0 {
		flushSize = DefaultFlushSize
	}

	return &Database{
		fw:           fw,
		log:          log,
		pending:      newPendingBatch(),
		flushSize:    flushSize,
		flushOnClose: true,
	}, nil
}

// flushLocked commits pending writes to Firewood.
// Caller must hold pendingMu lock.
func (db *Database) flushLocked() error {
	if len(db.pending.ops) == 0 {
		return nil
	}

	// Collect keys and values for proposal
	keys := make([][]byte, 0, len(db.pending.ops))
	values := make([][]byte, 0, len(db.pending.ops))

	for _, op := range db.pending.ops {
		keys = append(keys, op.key)
		if op.delete {
			values = append(values, nil) // nil value = delete
		} else {
			values = append(values, op.value)
		}
	}

	// Create proposal
	proposal, err := db.fw.Propose(keys, values)
	if err != nil {
		return fmt.Errorf("firewood propose failed: %w", err)
	}

	// Commit proposal
	if err := proposal.Commit(); err != nil {
		return fmt.Errorf("firewood commit failed: %w", err)
	}

	// Clear pending batch
	db.pending = newPendingBatch()

	db.log.Debug("Flushed pending batch")

	return nil
}

// Has implements database.KeyValueReader
func (db *Database) Has(key []byte) (bool, error) {
	if db.closed.Load() {
		return false, database.ErrClosed
	}

	db.pendingMu.Lock()
	defer db.pendingMu.Unlock()

	// Check pending batch first
	if op, exists := db.pending.ops[string(key)]; exists {
		return !op.delete, nil // exists if not a delete operation
	}

	// Check committed state in Firewood
	val, err := db.fw.Get(key)
	if err != nil {
		return false, err
	}

	// Firewood Get() returns nil for missing keys (not an error)
	return val != nil, nil
}

// Get implements database.KeyValueReader
// Provides read-your-writes consistency by checking pending batch first.
func (db *Database) Get(key []byte) ([]byte, error) {
	if db.closed.Load() {
		return nil, database.ErrClosed
	}

	db.pendingMu.Lock()
	defer db.pendingMu.Unlock()

	// Check pending batch first (read-your-writes consistency)
	if op, exists := db.pending.ops[string(key)]; exists {
		if op.delete {
			return nil, database.ErrNotFound // Pending delete
		}
		// Return copy to prevent caller from modifying pending batch
		result := make([]byte, len(op.value))
		copy(result, op.value)
		return result, nil
	}

	// Check committed state in Firewood
	value, err := db.fw.Get(key)
	if err != nil {
		return nil, err
	}

	// Firewood Get() returns nil for missing keys (not an error)
	if value == nil {
		return nil, database.ErrNotFound
	}

	// DEBUG: Log what Firewood returns before transformation
	if db.log != nil {
		valueLen := len(value)
		hexPreview := hex.EncodeToString(value[:min(valueLen, 32)])
		asciiCheck := isASCII(value[:min(valueLen, 32)])
		db.log.Debug("Firewood Get() raw data",
			zap.String("keyHex", hex.EncodeToString(key)),
			zap.Int("valueLen", valueLen),
			zap.String("valueHexPreview", hexPreview),
			zap.Bool("looksASCII", asciiCheck),
		)
	}

	// Try to decode hex-encoded data from Firewood
	if decoded, ok := tryHexDecode(value); ok {
		// DEBUG: Log successful hex decode
		if db.log != nil {
			db.log.Debug("Firewood Get() hex decode SUCCESS",
				zap.Int("originalLen", len(value)),
				zap.Int("decodedLen", len(decoded)),
			)
		}
		return decoded, nil
	}

	// DEBUG: Log that we're returning raw data
	if db.log != nil {
		db.log.Debug("Firewood Get() returning raw (not hex)")
	}

	return value, nil
}

// Put implements database.KeyValueWriter
// Adds operation to pending batch and auto-flushes when threshold reached.
func (db *Database) Put(key []byte, value []byte) error {
	if db.closed.Load() {
		return database.ErrClosed
	}

	db.pendingMu.Lock()
	defer db.pendingMu.Unlock()

	// Make copies to prevent caller from modifying our internal state
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)
	valueCopy := make([]byte, len(value))
	copy(valueCopy, value)

	// Add to pending batch
	db.pending.ops[string(keyCopy)] = &pendingOp{
		key:    keyCopy,
		value:  valueCopy,
		delete: false,
	}

	// Auto-flush if threshold reached
	if len(db.pending.ops) >= db.flushSize {
		return db.flushLocked()
	}

	return nil
}

// Delete implements database.KeyValueDeleter
// Adds delete operation to pending batch and auto-flushes when threshold reached.
func (db *Database) Delete(key []byte) error {
	if db.closed.Load() {
		return database.ErrClosed
	}

	db.pendingMu.Lock()
	defer db.pendingMu.Unlock()

	// Make copy to prevent caller from modifying our internal state
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)

	// Add to pending batch as delete operation
	db.pending.ops[string(keyCopy)] = &pendingOp{
		key:    keyCopy,
		value:  nil,
		delete: true,
	}

	// Auto-flush if threshold reached
	if len(db.pending.ops) >= db.flushSize {
		return db.flushLocked()
	}

	return nil
}

// NewBatch implements database.Batcher
// Returns a batch that accumulates operations and commits them atomically on Write().
// Note: Explicit batches do NOT auto-flush - only Write() commits them.
func (db *Database) NewBatch() database.Batch {
	return &batch{
		db:  db,
		ops: make(map[string]*pendingOp),
	}
}

// preparePendingOps converts pending batch to sorted slice for merge iteration
// Caller must hold pendingMu lock
func (db *Database) preparePendingOpsLocked(start, prefix []byte) []pendingKV {
	if len(db.pending.ops) == 0 {
		return nil
	}

	// Convert map to slice
	pending := make([]pendingKV, 0, len(db.pending.ops))
	for _, op := range db.pending.ops {
		// Filter by prefix if specified
		if len(prefix) > 0 && !bytes.HasPrefix(op.key, prefix) {
			continue
		}
		// Filter by start if specified
		if len(start) > 0 && bytes.Compare(op.key, start) < 0 {
			continue
		}
		pending = append(pending, pendingKV{
			key:    op.key,
			value:  op.value,
			delete: op.delete,
		})
	}

	// Sort by key for merge iteration
	sort.Slice(pending, func(i, j int) bool {
		return bytes.Compare(pending[i].key, pending[j].key) < 0
	})

	return pending
}

// NewIterator implements database.Iteratee
// Returns merge iterator combining committed state + pending writes
func (db *Database) NewIterator() database.Iterator {
	if db.closed.Load() {
		return newErrorIterator(database.ErrClosed)
	}

	db.pendingMu.Lock()
	defer db.pendingMu.Unlock()

	// Get latest revision from Firewood
	rev, err := db.fw.LatestRevision()
	if err != nil {
		return newErrorIterator(fmt.Errorf("failed to get latest revision: %w", err))
	}

	// Create FFI iterator starting from beginning (empty slice)
	fwIter, err := rev.Iter([]byte{})
	if err != nil {
		return newErrorIterator(fmt.Errorf("failed to create iterator: %w", err))
	}

	// Prepare pending operations
	pending := db.preparePendingOpsLocked(nil, nil)

	return newIterator(fwIter, pending, db.log)
}

// NewIteratorWithStart implements database.Iteratee
func (db *Database) NewIteratorWithStart(start []byte) database.Iterator {
	if db.closed.Load() {
		return newErrorIterator(database.ErrClosed)
	}

	db.pendingMu.Lock()
	defer db.pendingMu.Unlock()

	// Get latest revision from Firewood
	rev, err := db.fw.LatestRevision()
	if err != nil {
		return newErrorIterator(fmt.Errorf("failed to get latest revision: %w", err))
	}

	// Create FFI iterator starting from start key
	fwIter, err := rev.Iter(start)
	if err != nil {
		return newErrorIterator(fmt.Errorf("failed to create iterator: %w", err))
	}

	// Prepare pending operations (filtered by start)
	pending := db.preparePendingOpsLocked(start, nil)

	return newIterator(fwIter, pending, db.log)
}

// NewIteratorWithPrefix implements database.Iteratee
func (db *Database) NewIteratorWithPrefix(prefix []byte) database.Iterator {
	if db.closed.Load() {
		return newErrorIterator(database.ErrClosed)
	}

	db.pendingMu.Lock()
	defer db.pendingMu.Unlock()

	// Get latest revision from Firewood
	rev, err := db.fw.LatestRevision()
	if err != nil {
		return newErrorIterator(fmt.Errorf("failed to get latest revision: %w", err))
	}

	// Create FFI iterator starting from prefix
	fwIter, err := rev.Iter(prefix)
	if err != nil {
		return newErrorIterator(fmt.Errorf("failed to create iterator: %w", err))
	}

	// Prepare pending operations (filtered by prefix)
	pending := db.preparePendingOpsLocked(nil, prefix)

	return newIterator(fwIter, pending, db.log)
}

// NewIteratorWithStartAndPrefix implements database.Iteratee
func (db *Database) NewIteratorWithStartAndPrefix(start, prefix []byte) database.Iterator {
	if db.closed.Load() {
		return newErrorIterator(database.ErrClosed)
	}

	db.pendingMu.Lock()
	defer db.pendingMu.Unlock()

	// Get latest revision from Firewood
	rev, err := db.fw.LatestRevision()
	if err != nil {
		return newErrorIterator(fmt.Errorf("failed to get latest revision: %w", err))
	}

	// Create FFI iterator starting from start key
	// Note: Firewood iterator doesn't have native prefix+start support,
	// so we filter in the merge iterator
	fwIter, err := rev.Iter(start)
	if err != nil {
		return newErrorIterator(fmt.Errorf("failed to create iterator: %w", err))
	}

	// Prepare pending operations (filtered by both start and prefix)
	pending := db.preparePendingOpsLocked(start, prefix)

	return newIterator(fwIter, pending, db.log)
}

// Compact implements database.Compacter
func (db *Database) Compact(start []byte, limit []byte) error {
	// Firewood is a merkle trie database - compaction may not be applicable
	// or could trigger internal optimization routines if available
	// TODO: Check if Firewood has compaction support
	return nil
}

// Close implements io.Closer
// Flushes pending writes and closes the underlying Firewood database.
func (db *Database) Close() error {
	if !db.closed.CompareAndSwap(false, true) {
		return database.ErrClosed
	}

	db.pendingMu.Lock()
	defer db.pendingMu.Unlock()

	// Flush any pending writes if configured to do so
	if db.flushOnClose && len(db.pending.ops) > 0 {
		db.log.Info("Flushing pending writes before close")
		if err := db.flushLocked(); err != nil {
			db.log.Error("Failed to flush pending writes on close")
			// Continue with close despite flush error
		}
	}

	// Close Firewood database
	ctx := context.Background()
	if err := db.fw.Close(ctx); err != nil {
		return fmt.Errorf("failed to close firewood database: %w", err)
	}

	db.log.Info("Firewood database closed")
	return nil
}

// HealthCheck implements health.Checker
func (db *Database) HealthCheck(ctx context.Context) (interface{}, error) {
	if db.closed.Load() {
		return nil, database.ErrClosed
	}

	db.pendingMu.Lock()
	pendingOps := len(db.pending.ops)
	db.pendingMu.Unlock()

	// Try a simple read operation to verify database is responsive
	testKey := []byte("__health_check__")
	_, err := db.fw.Get(testKey)
	if err != nil {
		return nil, fmt.Errorf("health check failed: %w", err)
	}

	return map[string]interface{}{
		"database":       "firewood",
		"status":         "healthy",
		"pendingOps":     pendingOps,
		"flushThreshold": db.flushSize,
	}, nil
}

// batch implements database.Batch for Firewood
// Operations are buffered in memory and committed atomically on Write().
type batch struct {
	db  *Database
	ops map[string]*pendingOp
}

func (b *batch) Put(key []byte, value []byte) error {
	// Make copies to prevent caller from modifying our internal state
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)
	valueCopy := make([]byte, len(value))
	copy(valueCopy, value)

	b.ops[string(keyCopy)] = &pendingOp{
		key:    keyCopy,
		value:  valueCopy,
		delete: false,
	}
	return nil
}

func (b *batch) Delete(key []byte) error {
	// Make copy to prevent caller from modifying our internal state
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)

	b.ops[string(keyCopy)] = &pendingOp{
		key:    keyCopy,
		value:  nil,
		delete: true,
	}
	return nil
}

func (b *batch) Size() int {
	total := 0
	for _, op := range b.ops {
		total += len(op.key) + len(op.value)
	}
	return total
}

func (b *batch) Write() error {
	if b.db.closed.Load() {
		return database.ErrClosed
	}

	if len(b.ops) == 0 {
		return nil
	}

	// IMPORTANT: Flush database pending batch first to maintain consistency
	// This ensures batch operations see the latest state and don't conflict
	b.db.pendingMu.Lock()
	defer b.db.pendingMu.Unlock()

	if len(b.db.pending.ops) > 0 {
		if err := b.db.flushLocked(); err != nil {
			return fmt.Errorf("failed to flush pending before batch: %w", err)
		}
	}

	// Collect keys and values for proposal
	keys := make([][]byte, 0, len(b.ops))
	values := make([][]byte, 0, len(b.ops))

	for _, op := range b.ops {
		keys = append(keys, op.key)
		if op.delete {
			values = append(values, nil) // nil value = delete
		} else {
			values = append(values, op.value)
		}
	}

	// Create proposal
	proposal, err := b.db.fw.Propose(keys, values)
	if err != nil {
		return fmt.Errorf("firewood batch propose failed: %w", err)
	}

	// Commit proposal atomically
	if err := proposal.Commit(); err != nil {
		return fmt.Errorf("firewood batch commit failed: %w", err)
	}

	b.db.log.Debug("Batch write committed")

	return nil
}

func (b *batch) Reset() {
	b.ops = make(map[string]*pendingOp)
}

func (b *batch) Replay(w database.KeyValueWriterDeleter) error {
	for _, op := range b.ops {
		if op.delete {
			if err := w.Delete(op.key); err != nil {
				return err
			}
		} else {
			if err := w.Put(op.key, op.value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *batch) Inner() database.Batch {
	return b
}
