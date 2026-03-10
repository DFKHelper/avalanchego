//go:build cgo && !windows
// +build cgo,!windows

// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package firewood

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

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
// - ALSO flushes on periodic timer (default: 5 seconds) to prevent data loss on crash
// - Provides read-your-writes consistency by checking pending batch first
//
// Key Registry: Legacy tracking of committed keys (retained for health monitoring).
// - Iterators now use Firewood's native FFI trie iterator (Revision.Iter())
//   which reads key-value pairs directly from the persisted trie
// - The registry is no longer required for iteration correctness
//
// Root Hash Tracking:
// - Firewood's Get() uses fwd_get_latest which requires an in-memory revision.
// - After restart, no revision exists in memory, so Get() returns nil for ALL keys.
// - Fix: We track the current root hash and use GetFromRoot() which reads directly
//   from the persisted trie, bypassing the revision system entirely.
// - currentRoot is updated after every Propose+Commit under pendingMu lock.
//
// See ARCHITECTURE_NOTES.md for detailed design rationale.
type Database struct {
	fw          *ffi.Database
	log         logging.Logger
	dbPath      string // Path to this database (for debug logging)
	closed      atomic.Bool
	currentRoot ffi.Hash // Current trie root hash for GetFromRoot reads (protected by pendingMu)

	// readCacheGen is incremented on every flush/commit so that an in-flight Get()
	// that already snapshotted a (possibly stale) root can detect the flush and skip
	// caching its result, preventing a stale entry from entering the read cache.
	readCacheGen atomic.Uint64

	// Pending batch tracking for auto-flush
	// pendingMu is an RWMutex: reads (Get/Has/NewIterator) use RLock; writes (Put/Delete/flush) use Lock.
	pendingMu    sync.RWMutex
	pending      *pendingBatch // Accumulates writes until flush
	flushSize    int           // Auto-flush threshold
	flushOnClose bool          // Whether to flush pending writes on close

	// Periodic flush to prevent data loss on crash
	flushTicker *time.Ticker
	flushDone   chan struct{}

	// Go-level read cache: stores recently-read committed key-value pairs in Go
	// memory to avoid repeated FFI trie traversals for hot keys.
	// Access is protected by readCacheMu.  The cache is cleared on every
	// flush/batch-commit (readCacheGen is bumped at the same time).
	// Pending-batch entries always take priority in Get/Has, so the cache can
	// never hide an uncommitted write.
	readCacheMu  sync.RWMutex
	readCache    map[string][]byte
	readCacheMax int // 0 = cache disabled

	// Key registry: Legacy tracking of committed keys for health monitoring.
	// Iterators now use Firewood's native FFI trie iterator directly.
	registryMu        sync.RWMutex
	registry          map[string]bool // Set of all committed keys (string key, bool always true)
	registryFile      string          // Path to registry persistence file
	lastRegistrySave  time.Time       // Last time registry was saved (rate limiting)
	registrySaveMu    sync.Mutex      // Protects lastRegistrySave
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
	// Start with defaults, then overlay config from JSON.
	// This ensures critical fields like RootStore default to true
	// even if the config file doesn't mention them.
	cfg := DefaultConfig()
	if len(configBytes) > 0 {
		// The configBytes contains the full db-config.json structure like:
		// {"leveldb": {...}, "firewood": {...}, "pruning": {...}}
		// We need to extract just the "firewood" section
		var fullConfig map[string]json.RawMessage
		if err := json.Unmarshal(configBytes, &fullConfig); err != nil {
			return nil, fmt.Errorf("failed to parse database config: %w", err)
		}

		// Extract the "firewood" section if it exists and overlay onto defaults
		if firewoodSection, exists := fullConfig["firewood"]; exists {
			if err := json.Unmarshal(firewoodSection, &cfg); err != nil {
				return nil, fmt.Errorf("failed to parse firewood config section: %w", err)
			}
		}
	}

	// Build FFI options from config
	options := []ffi.Option{
		ffi.WithNodeCacheEntries(cfg.CacheSizeBytes / 256), // ~256 bytes per node
		ffi.WithFreeListCacheEntries(cfg.FreeListCacheEntries),
		ffi.WithRevisions(cfg.RevisionsInMemory),
		ffi.WithReadCacheStrategy(cfg.CacheStrategy),
	}

	// Enable root store for historical revision access.
	// Without this, old revisions are freed via the freelist and space is reused.
	// The latest state is always persisted regardless of this setting.
	if cfg.RootStore {
		options = append(options, ffi.WithRootStore())
	}

	// Open Firewood database
	fw, err := ffi.New(file, options...)
	if err != nil {
		return nil, fmt.Errorf("failed to open firewood database: %w", err)
	}

	// Get initial root hash for read operations.
	// CRITICAL: Firewood's Get() uses fwd_get_latest which needs an in-memory revision.
	// After restart, no revision exists, so Get() returns nil for ALL keys - causing
	// the P-Chain to think lastAcceptedHeight=0 and re-fetch everything from genesis.
	// GetFromRoot() bypasses the revision system and reads directly from the persisted trie.
	initialRoot, err := fw.Root()
	if err != nil {
		fw.Close(context.Background())
		return nil, fmt.Errorf("failed to get initial root hash: %w", err)
	}

	if initialRoot != ffi.EmptyRoot {
		log.Info("Firewood database opened with existing data",
			zap.Bool("rootStore", cfg.RootStore),
			zap.Uint("revisionsInMemory", cfg.RevisionsInMemory),
			zap.Uint("cacheSizeBytes", cfg.CacheSizeBytes),
			zap.String("rootHash", fmt.Sprintf("%x", initialRoot[:8])),
		)
	} else {
		log.Info("Firewood database opened (empty/new instance)",
			zap.Bool("rootStore", cfg.RootStore),
			zap.Uint("revisionsInMemory", cfg.RevisionsInMemory),
			zap.Uint("cacheSizeBytes", cfg.CacheSizeBytes),
		)
	}

	flushSize := cfg.FlushSize
	if flushSize == 0 {
		flushSize = DefaultFlushSize
	}

	readCacheMax := cfg.ReadCacheSize
	if readCacheMax < 0 {
		readCacheMax = 0
	}

	db := &Database{
		fw:           fw,
		log:          log,
		dbPath:       file,
		closed:       atomic.Bool{},
		currentRoot:  initialRoot,
		pending:      newPendingBatch(),
		flushSize:    flushSize,
		flushOnClose: true,
		flushTicker:  time.NewTicker(5 * time.Second), // Flush every 5 seconds
		flushDone:    make(chan struct{}),
		readCacheMax: readCacheMax,
		readCache:    make(map[string][]byte, readCacheMax),
		registry:     make(map[string]bool), // Initialize key registry
	}

	// Start periodic flush goroutine to prevent data loss on crash
	go db.periodicFlush()

	// Load registry from disk to preserve bootstrap progress across restarts
	registryPath := filepath.Join(file, ".registry")
	if err := db.loadRegistry(registryPath); err != nil {
		// Registry load failure is non-fatal, but log it
		log.Warn("Failed to load registry from disk, starting with empty registry",
			zap.String("path", registryPath),
			zap.Error(err))
	}

	return db, nil
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

	// Update current root hash after successful commit.
	// This is critical: GetFromRoot reads from a specific root, so we must
	// track the latest root to see newly committed data.
	if newRoot, err := db.fw.Root(); err == nil {
		db.currentRoot = newRoot
	}

	// Write-back verification: spot-check that committed data is readable
	// This catches Firewood FFI bugs where Propose+Commit succeeds but data is lost
	verifyCount := 0
	for _, op := range db.pending.ops {
		if verifyCount >= 3 {
			break
		}
		if op.delete {
			continue
		}
		readBack, err := db.fw.GetFromRoot(db.currentRoot, op.key)
		if err != nil || readBack == nil {
			db.log.Error("WRITE-BACK VERIFICATION FAILED: committed key not readable",
				zap.Int("keyLen", len(op.key)),
				zap.Error(err),
			)
			// Retry the entire proposal once
			retryProposal, retryErr := db.fw.Propose(keys, values)
			if retryErr != nil {
				return fmt.Errorf("firewood retry propose failed after verification failure: %w", retryErr)
			}
			if retryErr = retryProposal.Commit(); retryErr != nil {
				return fmt.Errorf("firewood retry commit failed after verification failure: %w", retryErr)
			}
			if newRoot, err := db.fw.Root(); err == nil {
				db.currentRoot = newRoot
			}
			db.log.Warn("Firewood write-back verification: retry commit succeeded")
			break
		}
		verifyCount++
	}

	// Registry updates DISABLED: iterators now use native FFI trie iterator
	// (Revision.Iter()) which reads key-value pairs directly from the persisted trie.
	// The registry was causing severe performance degradation during bootstrap execution
	// by growing to millions of keys and blocking all DB operations during JSON serialization.

	// Clear read cache: the trie root changed so all cached values are stale.
	// Bump the generation counter first so in-flight Gets that already snapshotted
	// the old root will notice the change and skip caching their (stale) results.
	if db.readCacheMax > 0 {
		db.readCacheGen.Add(1)
		db.readCacheMu.Lock()
		clear(db.readCache)
		db.readCacheMu.Unlock()
	}

	// Clear pending batch
	db.pending = newPendingBatch()

	db.registryMu.RLock()
	registrySize := len(db.registry)
	db.registryMu.RUnlock()
	db.log.Debug("Flushed pending batch",
		zap.Int("registrySize", registrySize),
	)

	return nil
}

// saveRegistryLocked persists the registry to disk using a chunked format to handle unlimited size.
// MUST be called with registryMu held (at least read lock).
// This ensures bootstrap progress is preserved across restarts.
//
// Format: Registry is split into chunks of 50K keys each to avoid gob encoder limits.
// Each chunk is saved as a separate JSON file for robustness and debuggability.
// A manifest file tracks all chunks and metadata.
func (db *Database) saveRegistryLocked() error {
	if db.registryFile == "" {
		return nil // Registry persistence disabled
	}

	registrySize := len(db.registry)

	// Log warning if registry is getting large (helps with monitoring)
	if registrySize > 500000 {
		db.log.Warn("Registry is large - consider enabling auto-compaction",
			zap.Int("size", registrySize),
			zap.String("sizeMB", fmt.Sprintf("%.1f", float64(registrySize*32)/1024/1024)))
	}

	// CHUNKED FORMAT: Split registry into manageable chunks
	const keysPerChunk = 50000

	// Convert registry map to sorted slice for deterministic chunking
	keys := make([]string, 0, registrySize)
	for key := range db.registry {
		keys = append(keys, key)
	}
	sort.Strings(keys) // Deterministic order for stable chunks

	// Calculate number of chunks needed
	numChunks := (len(keys) + keysPerChunk - 1) / keysPerChunk

	// Create chunks directory if it doesn't exist
	chunksDir := filepath.Join(filepath.Dir(db.registryFile), "registry-chunks") // BUG #EDGE1 fix: use filepath.Join
	if err := os.MkdirAll(chunksDir, 0755); err != nil {
		return fmt.Errorf("failed to create chunks directory: %w", err)
	}

	// BUG #2 fix: Clean up old chunk files to prevent orphans
	// Remove all existing .json chunk files (but not .tmp files from failed saves)
	oldChunks, err := filepath.Glob(filepath.Join(chunksDir, "chunk.*.json"))
	if err == nil && len(oldChunks) > 0 {
		db.log.Debug("Cleaning up old chunk files",
			zap.Int("count", len(oldChunks)))
		for _, oldChunk := range oldChunks {
			os.Remove(oldChunk) // Best-effort removal, ignore errors
		}
	}

	// Save each chunk atomically
	chunkFiles := make([]string, numChunks)
	for i := 0; i < numChunks; i++ {
		start := i * keysPerChunk
		end := start + keysPerChunk
		if end > len(keys) {
			end = len(keys)
		}

		chunk := keys[start:end]
		chunkFile := filepath.Join(chunksDir, fmt.Sprintf("chunk.%06d.json", i))
		chunkTmp := chunkFile + ".tmp"

		// Write chunk to temp file
		f, err := os.Create(chunkTmp)
		if err != nil {
			return fmt.Errorf("failed to create chunk file %d: %w", i, err)
		}

		encoder := json.NewEncoder(f)
		if err := encoder.Encode(chunk); err != nil {
			f.Close()
			os.Remove(chunkTmp)
			return fmt.Errorf("failed to encode chunk %d: %w", i, err)
		}

		if err := f.Sync(); err != nil {
			f.Close()
			os.Remove(chunkTmp)
			return fmt.Errorf("failed to sync chunk %d: %w", i, err)
		}

		f.Close()

		// Atomic rename
		if err := os.Rename(chunkTmp, chunkFile); err != nil {
			os.Remove(chunkTmp)
			return fmt.Errorf("failed to rename chunk %d: %w", i, err)
		}

		chunkFiles[i] = chunkFile
	}

	// Save manifest file (tracks all chunks + metadata)
	manifest := map[string]interface{}{
		"version":    2,
		"totalKeys":  registrySize,
		"numChunks":  numChunks,
		"chunkSize":  keysPerChunk,
		"timestamp":  time.Now().Unix(),
		"chunks":     chunkFiles,
	}

	manifestTmp := db.registryFile + ".manifest.tmp"
	f, err := os.Create(manifestTmp)
	if err != nil {
		// BUG #3 fix: Clean up chunks if manifest creation fails
		db.cleanupChunkFiles(chunkFiles)
		return fmt.Errorf("failed to create manifest: %w", err)
	}

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(manifest); err != nil {
		f.Close()
		os.Remove(manifestTmp)
		// BUG #3 fix: Clean up chunks if manifest encoding fails
		db.cleanupChunkFiles(chunkFiles)
		return fmt.Errorf("failed to encode manifest: %w", err)
	}

	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(manifestTmp)
		// BUG #3 fix: Clean up chunks if manifest sync fails
		db.cleanupChunkFiles(chunkFiles)
		return fmt.Errorf("failed to sync manifest: %w", err)
	}

	f.Close()

	manifestFile := db.registryFile + ".manifest"
	if err := os.Rename(manifestTmp, manifestFile); err != nil {
		os.Remove(manifestTmp)
		// BUG #3 fix: Clean up chunks if manifest rename fails
		db.cleanupChunkFiles(chunkFiles)
		return fmt.Errorf("failed to rename manifest: %w", err)
	}

	// Also save legacy gob format as fallback (but only if small enough)
	// This provides backward compatibility during transition
	if registrySize < 100000 {
		db.saveRegistryLegacyGob()
	}

	db.log.Info("Saved registry to disk",
		zap.Int("totalKeys", registrySize),
		zap.Int("numChunks", numChunks),
		zap.String("format", "chunked-json"))

	return nil
}

// cleanupChunkFiles removes chunk files (best-effort cleanup for error recovery).
// Used when manifest write fails after chunks were written (BUG #3 fix).
func (db *Database) cleanupChunkFiles(chunkFiles []string) {
	for _, chunkFile := range chunkFiles {
		if err := os.Remove(chunkFile); err != nil {
			db.log.Debug("Failed to remove chunk file during cleanup (non-fatal)",
				zap.String("file", chunkFile),
				zap.Error(err))
		}
	}
}

// saveRegistryLegacyGob saves using old gob format for backward compatibility (best-effort).
// Only works for small registries (<100K keys). Failures are logged but not returned.
func (db *Database) saveRegistryLegacyGob() {
	if db.registryFile == "" {
		return
	}

	tmpFile := db.registryFile + ".tmp"
	f, err := os.Create(tmpFile)
	if err != nil {
		db.log.Debug("Failed to create legacy gob file (non-fatal)", zap.Error(err))
		return
	}

	encoder := gob.NewEncoder(f)
	if err := encoder.Encode(db.registry); err != nil {
		os.Remove(tmpFile)
		db.log.Debug("Failed to encode legacy gob (non-fatal, registry too large)", zap.Error(err))
		return
	}

	if err := f.Sync(); err != nil {
		os.Remove(tmpFile)
		return
	}

	f.Close()

	if err := os.Rename(tmpFile, db.registryFile); err != nil {
		os.Remove(tmpFile)
		return
	}

	db.log.Debug("Saved legacy gob format for backward compatibility")
}

// saveRegistryIfNeeded saves the registry to disk if 5 minutes have passed since last save.
// This prevents excessive disk I/O while ensuring progress is preserved frequently.
func (db *Database) saveRegistryIfNeeded() error {
	db.registrySaveMu.Lock()
	defer db.registrySaveMu.Unlock()

	// Check if 5 minutes have passed since last save
	if time.Since(db.lastRegistrySave) < 5*time.Minute {
		return nil // Too soon, skip save
	}

	// Save registry
	db.registryMu.RLock()
	err := db.saveRegistryLocked()
	db.registryMu.RUnlock()

	if err == nil {
		db.lastRegistrySave = time.Now()
	}

	return err
}

// loadRegistry loads the registry from disk.
// Supports both new chunked JSON format (v2) and legacy gob format (v1) for backward compatibility.
// Returns nil if file doesn't exist (first run).
func (db *Database) loadRegistry(registryFile string) error {
	if registryFile == "" {
		return nil // Registry persistence disabled
	}

	db.registryFile = registryFile

	// Try loading chunked format first (manifest file)
	manifestFile := registryFile + ".manifest"
	if _, err := os.Stat(manifestFile); err == nil {
		return db.loadRegistryChunked(manifestFile)
	}

	// Fall back to legacy gob format
	return db.loadRegistryLegacyGob(registryFile)
}

// loadRegistryChunked loads registry from chunked JSON format (unlimited size support).
func (db *Database) loadRegistryChunked(manifestFile string) error {
	// Load manifest
	f, err := os.Open(manifestFile)
	if err != nil {
		return fmt.Errorf("failed to open manifest: %w", err)
	}
	defer f.Close()

	var manifest map[string]interface{}
	decoder := json.NewDecoder(f)
	if err := decoder.Decode(&manifest); err != nil {
		return fmt.Errorf("failed to decode manifest: %w", err)
	}

	// Validate manifest fields with proper type checking (BUG #1 fix)
	totalKeysRaw, ok := manifest["totalKeys"]
	if !ok {
		return fmt.Errorf("manifest missing 'totalKeys' field")
	}
	totalKeysFloat, ok := totalKeysRaw.(float64)
	if !ok {
		return fmt.Errorf("manifest 'totalKeys' has invalid type (expected number, got %T)", totalKeysRaw)
	}
	totalKeys := int(totalKeysFloat)

	numChunksRaw, ok := manifest["numChunks"]
	if !ok {
		return fmt.Errorf("manifest missing 'numChunks' field")
	}
	numChunksFloat, ok := numChunksRaw.(float64)
	if !ok {
		return fmt.Errorf("manifest 'numChunks' has invalid type (expected number, got %T)", numChunksRaw)
	}
	numChunks := int(numChunksFloat)

	// Sanity check manifest values
	if totalKeys < 0 {
		return fmt.Errorf("manifest 'totalKeys' is negative: %d", totalKeys)
	}
	if numChunks < 0 {
		return fmt.Errorf("manifest 'numChunks' is negative: %d", numChunks)
	}
	if totalKeys > 0 && numChunks == 0 {
		return fmt.Errorf("manifest claims %d keys but 0 chunks", totalKeys)
	}

	db.log.Info("Loading chunked registry",
		zap.Int("totalKeys", totalKeys),
		zap.Int("numChunks", numChunks))

	// Load all chunks
	registry := make(map[string]bool, totalKeys)

	chunksDir := filepath.Join(filepath.Dir(db.registryFile), "registry-chunks") // Use filepath.Join for portability
	for i := 0; i < numChunks; i++ {
		chunkFile := filepath.Join(chunksDir, fmt.Sprintf("chunk.%06d.json", i))

		f, err := os.Open(chunkFile)
		if err != nil {
			return fmt.Errorf("failed to open chunk %d: %w", i, err)
		}

		var chunk []string
		decoder := json.NewDecoder(f)
		if err := decoder.Decode(&chunk); err != nil {
			f.Close()
			return fmt.Errorf("failed to decode chunk %d: %w", i, err)
		}
		f.Close()

		// Add chunk keys to registry
		for _, key := range chunk {
			registry[key] = true
		}

		if i%10 == 0 || i == numChunks-1 {
			db.log.Info("Loading chunks",
				zap.Int("loaded", i+1),
				zap.Int("total", numChunks),
				zap.Int("keys", len(registry)))
		}
	}

	db.registryMu.Lock()
	db.registry = registry
	db.registryMu.Unlock()

	db.log.Info("Loaded chunked registry successfully",
		zap.Int("totalKeys", len(registry)))

	return nil
}

// loadRegistryLegacyGob loads registry from legacy gob format (v1).
// Only supports registries that fit in gob encoding limits (<2GB).
func (db *Database) loadRegistryLegacyGob(registryFile string) error {
	// Check if registry file exists
	f, err := os.Open(registryFile)
	if err != nil {
		if os.IsNotExist(err) {
			db.log.Info("Registry file does not exist (first run), starting with empty registry",
				zap.String("path", registryFile))
			return nil
		}
		return fmt.Errorf("failed to open registry file: %w", err)
	}
	defer f.Close()

	// Decode registry from gob format
	decoder := gob.NewDecoder(f)
	var registry map[string]bool
	if err := decoder.Decode(&registry); err != nil {
		return fmt.Errorf("failed to decode registry (corruption?): %w", err)
	}

	db.registryMu.Lock()
	db.registry = registry
	db.registryMu.Unlock()

	db.log.Info("Loaded registry from disk (legacy gob format)",
		zap.String("path", registryFile),
		zap.Int("keys", len(registry)))

	return nil
}

// Has implements database.KeyValueReader
func (db *Database) Has(key []byte) (bool, error) {
	if db.closed.Load() {
		return false, database.ErrClosed
	}

	// Read pending batch and snapshot root under read lock (non-blocking for concurrent Gets).
	db.pendingMu.RLock()
	op, inPending := db.pending.ops[string(key)]
	root := db.currentRoot
	db.pendingMu.RUnlock()

	// Check pending batch first (read-your-writes for uncommitted ops).
	if inPending {
		return !op.delete, nil
	}

	// Check committed state using root hash (works after restart).
	// Lock is released so concurrent Has/Get/FFI calls can proceed in parallel.
	val, err := db.fw.GetFromRoot(root, key)
	if err != nil {
		return false, err
	}

	// GetFromRoot returns nil for missing keys (not an error)
	return val != nil, nil
}

// Get implements database.KeyValueReader
// Provides read-your-writes consistency by checking pending batch first.
//
// Hot path: pending → read cache → FFI trie traversal.
// The pending check and root snapshot require only a brief read lock.
// The read cache and FFI call are performed without holding any lock, allowing
// true parallelism for concurrent Gets on the same database instance.
func (db *Database) Get(key []byte) ([]byte, error) {
	if db.closed.Load() {
		return nil, database.ErrClosed
	}

	keyStr := string(key)

	// Phase 1: check pending batch and snapshot root under a brief read lock.
	// This is the only phase that requires synchronization with writers.
	db.pendingMu.RLock()
	op, inPending := db.pending.ops[keyStr]
	root := db.currentRoot
	db.pendingMu.RUnlock()

	if inPending {
		if op.delete {
			return nil, database.ErrNotFound // Pending delete
		}
		// Return copy to prevent caller from modifying pending batch.
		result := make([]byte, len(op.value))
		copy(result, op.value)
		return result, nil
	}

	// Phase 2: check the Go-level read cache (no FFI, no trie traversal).
	// Snapshot the cache generation before the lookup so we can safely skip
	// caching the FFI result if a flush raced between Phase 2 and Phase 3.
	var genBefore uint64
	if db.readCacheMax > 0 {
		db.readCacheMu.RLock()
		cached, inCache := db.readCache[keyStr]
		genBefore = db.readCacheGen.Load()
		db.readCacheMu.RUnlock()
		if inCache {
			if cached == nil {
				return nil, database.ErrNotFound
			}
			result := make([]byte, len(cached))
			copy(result, cached)
			return result, nil
		}
	}

	// Phase 3: FFI trie traversal — no locks held, true parallel reads.
	value, err := db.fw.GetFromRoot(root, key)
	if err != nil {
		return nil, err
	}

	// Populate read cache so the next Get of this key skips the FFI call.
	// Skip if the cache generation changed (a flush occurred while we were in
	// the FFI call), because our result is based on a now-superseded root.
	if db.readCacheMax > 0 && db.readCacheGen.Load() == genBefore {
		db.readCacheMu.Lock()
		// Re-check generation and capacity under write lock.
		if db.readCacheGen.Load() == genBefore && len(db.readCache) < db.readCacheMax {
			if value != nil {
				valCopy := make([]byte, len(value))
				copy(valCopy, value)
				db.readCache[keyStr] = valCopy
			}
			// We intentionally do NOT cache nil (missing key) to avoid
			// returning a stale "not found" after a concurrent Put+flush.
		}
		db.readCacheMu.Unlock()
	}

	// GetFromRoot returns nil for missing keys (not an error)
	if value == nil {
		return nil, database.ErrNotFound
	}

	// Return copy to prevent caller from modifying Firewood's internal state
	result := make([]byte, len(value))
	copy(result, value)
	return result, nil
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

	// Evict from read cache so a subsequent Get sees the pending value, not
	// the previously cached committed value.
	if db.readCacheMax > 0 {
		db.readCacheMu.Lock()
		delete(db.readCache, string(keyCopy))
		db.readCacheMu.Unlock()
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

	// Evict from read cache so a subsequent Get sees the pending delete.
	if db.readCacheMax > 0 {
		db.readCacheMu.Lock()
		delete(db.readCache, string(keyCopy))
		db.readCacheMu.Unlock()
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
// Returns native FFI trie iterator merging committed + pending operations
func (db *Database) NewIterator() database.Iterator {
	if db.closed.Load() {
		return newErrorIterator(database.ErrClosed)
	}

	db.pendingMu.RLock()
	defer db.pendingMu.RUnlock()

	pending := db.preparePendingOpsLocked(nil, nil)
	return newNativeIterator(db.fw, db.currentRoot, pending, nil, nil)
}

// NewIteratorWithStart implements database.Iteratee
func (db *Database) NewIteratorWithStart(start []byte) database.Iterator {
	if db.closed.Load() {
		return newErrorIterator(database.ErrClosed)
	}

	db.pendingMu.RLock()
	defer db.pendingMu.RUnlock()

	pending := db.preparePendingOpsLocked(start, nil)
	return newNativeIterator(db.fw, db.currentRoot, pending, start, nil)
}

// NewIteratorWithPrefix implements database.Iteratee
func (db *Database) NewIteratorWithPrefix(prefix []byte) database.Iterator {
	if db.closed.Load() {
		return newErrorIterator(database.ErrClosed)
	}

	db.pendingMu.RLock()
	defer db.pendingMu.RUnlock()

	pending := db.preparePendingOpsLocked(nil, prefix)
	return newNativeIterator(db.fw, db.currentRoot, pending, nil, prefix)
}

// NewIteratorWithStartAndPrefix implements database.Iteratee
func (db *Database) NewIteratorWithStartAndPrefix(start, prefix []byte) database.Iterator {
	if db.closed.Load() {
		return newErrorIterator(database.ErrClosed)
	}

	db.pendingMu.RLock()
	defer db.pendingMu.RUnlock()

	pending := db.preparePendingOpsLocked(start, prefix)
	return newNativeIterator(db.fw, db.currentRoot, pending, start, prefix)
}

// Compact implements database.Compacter
func (db *Database) Compact(start []byte, limit []byte) error {
	// Firewood is a merkle trie database - compaction may not be applicable
	// or could trigger internal optimization routines if available
	// TODO: Check if Firewood has compaction support
	return nil
}

// emergencyRegistryCompaction performs automatic registry cleanup when size becomes critical.
// This is a SELF-HEALING mechanism that runs asynchronously to prevent database crashes.
//
// Strategy: Since the registry tracks ALL keys ever written (for iteration support),
// it can grow unbounded during long-running operations like P-Chain bootstrap.
// This function implements graceful degradation by switching to a "recent-only" mode
// where we keep only the most recently used keys in memory.
func (db *Database) emergencyRegistryCompaction() {
	// BUG #5 fix: Add panic recovery since this runs in a goroutine
	defer func() {
		if r := recover(); r != nil {
			db.log.Error("Emergency registry compaction panicked (recovered)",
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
	}()

	db.log.Info("Starting emergency registry compaction (self-healing)")

	startTime := time.Now()

	db.registryMu.Lock()
	defer db.registryMu.Unlock()

	originalSize := len(db.registry)

	// STRATEGY: For P-Chain bootstrap, the registry contains millions of historical keys
	// that will never be queried again (old validator transactions, etc.).
	// We can safely discard the registry since:
	// 1. The actual data is still in Firewood database
	// 2. Registry is only needed for iteration, which is rarely used during bootstrap
	// 3. We can rebuild registry on-demand if iteration is needed
	//
	// This allows bootstrap to continue without registry save failures.

	// Clear the registry to prevent further gob encoding failures
	db.registry = make(map[string]bool)

	db.log.Info("Emergency registry compaction completed (self-healing)",
		zap.Int("originalSize", originalSize),
		zap.Int("newSize", len(db.registry)),
		zap.Duration("duration", time.Since(startTime)),
		zap.String("strategy", "cleared-for-bootstrap"),
		zap.String("note", "Registry cleared to prevent encoding failures; data remains in Firewood"))

	// Force a registry save attempt with the now-empty registry
	// This ensures checkpoint mechanism continues working
	if err := db.saveRegistryLocked(); err != nil {
		db.log.Warn("Failed to save compacted registry", zap.Error(err))
	}
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

	// Stop periodic flush goroutine
	db.flushTicker.Stop()
	close(db.flushDone)

	// Close Firewood database
	ctx := context.Background()
	if err := db.fw.Close(ctx); err != nil {
		return fmt.Errorf("failed to close firewood database: %w", err)
	}

	db.log.Info("Firewood database closed")
	return nil
}

// periodicFlush runs in a background goroutine and flushes pending writes periodically
// This prevents data loss if the process crashes before the batch size threshold is reached
func (db *Database) periodicFlush() {
	for {
		select {
		case <-db.flushTicker.C:
			db.pendingMu.Lock()
			// Guard against a race where the timer fires just before Close()
			// stops the ticker, causing flushLocked() to call fw.Propose() on
			// an already-closed FFI database.
			if db.closed.Load() {
				db.pendingMu.Unlock()
				return
			}
			if len(db.pending.ops) > 0 {
				opsCount := len(db.pending.ops)
				if err := db.flushLocked(); err != nil {
					if db.log != nil {
						db.log.Error("Periodic flush failed",
							zap.Int("pendingOps", opsCount),
							zap.Error(err),
						)
					}
				} else if db.log != nil {
					db.log.Debug("Periodic flush committed pending writes",
						zap.Int("opsCount", opsCount),
					)
				}
			}
			db.pendingMu.Unlock()

		case <-db.flushDone:
			// Graceful shutdown
			return
		}
	}
}

// HealthCheck implements health.Checker with comprehensive database health monitoring
func (db *Database) HealthCheck(ctx context.Context) (interface{}, error) {
	if db.closed.Load() {
		return nil, database.ErrClosed
	}

	db.pendingMu.RLock()
	pendingOps := len(db.pending.ops)
	db.pendingMu.RUnlock()

	db.registryMu.RLock()
	registrySize := len(db.registry)
	db.registryMu.RUnlock()

	// SELF-HEALING: Check registry health and trigger auto-recovery if needed
	if registrySize > 5000000 {
		// Registry is VERY large (>5M keys) - trigger emergency compaction
		db.log.Error("Registry size critical - auto-triggering emergency cleanup",
			zap.Int("size", registrySize),
			zap.String("action", "emergency-compaction"))

		// Trigger async cleanup to avoid blocking health check
		go db.emergencyRegistryCompaction()
	} else if registrySize > 2000000 {
		// Registry is large (>2M keys) - warn and suggest cleanup
		db.log.Warn("Registry size approaching limits - consider enabling auto-compaction",
			zap.Int("size", registrySize),
			zap.String("recommendation", "enable-periodic-cleanup"))
	}

	// Try a simple read operation to verify database is responsive
	// Use GetFromRoot to work correctly after restart (no revision needed)
	testKey := []byte("__health_check__")
	root, err := db.fw.Root()
	if err != nil {
		return nil, fmt.Errorf("health check failed (root hash): %w", err)
	}
	_, err = db.fw.GetFromRoot(root, testKey)
	if err != nil {
		return nil, fmt.Errorf("health check failed (read): %w", err)
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

	// Update current root hash after successful commit
	if newRoot, err := b.db.fw.Root(); err == nil {
		b.db.currentRoot = newRoot
	}

	// Clear read cache: trie root changed so all previously cached values are stale.
	if b.db.readCacheMax > 0 {
		b.db.readCacheGen.Add(1)
		b.db.readCacheMu.Lock()
		clear(b.db.readCache)
		b.db.readCacheMu.Unlock()
	}

	// Write-back verification: spot-check that batch data is readable after commit
	verifyCount := 0
	for _, op := range b.ops {
		if verifyCount >= 3 {
			break
		}
		if op.delete {
			continue
		}
		readBack, err := b.db.fw.GetFromRoot(b.db.currentRoot, op.key)
		if err != nil || readBack == nil {
			b.db.log.Error("BATCH WRITE-BACK VERIFICATION FAILED: committed key not readable",
				zap.Int("keyLen", len(op.key)),
				zap.Int("batchSize", len(b.ops)),
				zap.Error(err),
			)
			// Retry the entire batch proposal once
			retryProposal, retryErr := b.db.fw.Propose(keys, values)
			if retryErr != nil {
				return fmt.Errorf("firewood batch retry propose failed: %w", retryErr)
			}
			if retryErr = retryProposal.Commit(); retryErr != nil {
				return fmt.Errorf("firewood batch retry commit failed: %w", retryErr)
			}
			if newRoot, err := b.db.fw.Root(); err == nil {
				b.db.currentRoot = newRoot
			}
			b.db.log.Warn("Firewood batch write-back verification: retry commit succeeded",
				zap.Int("batchSize", len(b.ops)),
			)
			break
		}
		verifyCount++
	}

	// Registry updates DISABLED: iterators use native FFI trie iterator.
	// The previous comment "CRITICAL: blocks won't be iterable without this" was outdated.
	// Native iterators (Revision.Iter()) read directly from the persisted trie.

	b.db.log.Debug("Batch write committed", zap.Int("keysWritten", len(b.ops)))

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
