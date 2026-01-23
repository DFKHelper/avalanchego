// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package statesync

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ava-labs/libevm/common"
	"github.com/ava-labs/libevm/ethdb"
	"github.com/ava-labs/libevm/triedb"
	"golang.org/x/sync/errgroup"
)

const (
	numStorageTrieSegments = 4
	numMainTrieSegments    = 8
)

var (
	errWaitBeforeStart = errors.New("cannot call Wait before Start")
	errAlreadyStarted  = errors.New("state sync already started")
	errRestartDisabled = errors.New("restart not enabled")
	errNotAsyncMode    = errors.New("method only available in async mode")
)

// stateSync is the unified state syncer supporting both Coreth and Subnet-EVM patterns
type stateSync struct {
	// Common fields (both modes)
	db         ethdb.Database
	root       common.Hash
	trieDB     *triedb.Database
	snapshot   interface{} // snapshot.SnapshotIterable - avoid import cycle
	config     StateSyncConfig
	workerCfg  WorkerConfig
	batchSize  int

	segments        chan interface{} // syncclient.LeafSyncTask - avoid import cycle
	syncer          interface{}      // *syncclient.CallbackLeafSyncer - avoid import cycle
	codeSyncer      CodeSyncInterface
	trieQueue       interface{} // *trieQueue - avoid import cycle

	mainTrie        interface{} // *trieToSync - avoid import cycle
	triesInProgress map[common.Hash]interface{} // map to *trieToSync

	// Synchronization
	lock                sync.RWMutex
	triesInProgressSem  chan struct{}
	mainTrieDone        chan struct{}
	mainTrieNearlydone  chan struct{} // Coreth: 95% overlap optimization
	storageTriesDone    chan struct{}

	stats interface{} // *trieSyncStats - avoid import cycle

	// Coreth-specific
	syncCompleted atomic.Bool // Finalize() uses this in blocking mode

	// Subnet-EVM specific (async mode)
	cancelFunc           context.CancelFunc
	done                 chan error
	stuckDetector        interface{} // *StuckDetector - avoid import cycle
	syncMutex            sync.Mutex  // Protects Start/Restart
	started              atomic.Bool
	waitStarted          atomic.Bool
	cachedResult         atomic.Value // stores *error
	resultReady          chan struct{}
	mainTrieDoneOnce     sync.Once
	segmentsDoneOnce     sync.Once
	storageTriesDoneOnce sync.Once
	codeSyncErr          atomic.Value // stores error
	codeSyncFailed       atomic.Bool
	restartCount         atomic.Uint32
}

// NewStateSyncer creates a unified state syncer based on configuration
func NewStateSyncer(config StateSyncConfig) (StateSyncer, error) {
	if err := validateConfig(config); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	workerCfg := CalculateWorkerConfig(config)

	ss := &stateSync{
		db:              config.DB,
		root:            config.Root,
		trieDB:          triedb.NewDatabase(config.DB, nil),
		config:          config,
		workerCfg:       workerCfg,
		batchSize:       config.BatchSize,
		codeSyncer:      config.CodeSyncer,
		triesInProgress: make(map[common.Hash]interface{}),

		// Channels
		triesInProgressSem: make(chan struct{}, workerCfg.NumWorkers),
		segments:           make(chan interface{}, workerCfg.NumWorkers*numMainTrieSegments),
		mainTrieDone:       make(chan struct{}),
		storageTriesDone:   make(chan struct{}),
	}

	// Mode-specific initialization
	if config.Mode == ModeBlocking {
		// Coreth: add mainTrieNearlydone for 95% overlap
		ss.mainTrieNearlydone = make(chan struct{})
	} else {
		// Subnet-EVM: async mode channels
		ss.done = make(chan error, 1)
		ss.resultReady = make(chan struct{})
	}

	// Initialize snapshot (would use actual snapshot.NewDiskLayer in real code)
	// ss.snapshot = snapshot.NewDiskLayer(config.DB)

	// Initialize trieQueue and clear if root mismatch
	// ss.trieQueue = NewTrieQueue(config.DB)
	// if err := ss.trieQueue.clearIfRootDoesNotMatch(ss.root); err != nil {
	//     return nil, err
	// }

	// Initialize syncer (would use actual CallbackLeafSyncer in real code)
	// ss.syncer = syncclient.NewCallbackLeafSyncer(config.Client, ss.segments, ...)

	// Create main trie and start syncing
	// ss.mainTrie, err = NewTrieToSync(ss, ss.root, common.Hash{}, NewMainTrieTask(ss))
	// ss.addTrieInProgress(ss.root, ss.mainTrie)
	// ss.mainTrie.startSyncing(context.Background())

	// Subnet-EVM: initialize stuck detector if enabled
	if config.Mode == ModeAsync && config.EnableStuckDetection {
		// ss.stuckDetector = NewStuckDetector(ss.stats)
	}

	return ss, nil
}

// Sync performs synchronization (works for both modes)
// For ModeBlocking: runs sync and blocks until complete
// For ModeAsync: calls Start() then Wait()
func (t *stateSync) Sync(ctx context.Context) error {
	if t.config.Mode == ModeBlocking {
		return t.doSyncBlocking(ctx)
	}

	// Async mode: Start then Wait
	if err := t.Start(ctx); err != nil {
		return err
	}
	return t.Wait(ctx)
}

// doSyncBlocking implements Coreth's simple blocking pattern
func (t *stateSync) doSyncBlocking(ctx context.Context) error {
	eg, egCtx := errgroup.WithContext(ctx)

	// Leaf syncer goroutine
	eg.Go(func() error {
		// In real implementation: t.syncer.Sync(egCtx)
		// For now, placeholder
		<-egCtx.Done()
		return egCtx.Err()
	})

	// Storage trie producer goroutine
	eg.Go(func() error {
		return t.storageTrieProducer(egCtx)
	})

	// Wait for completion
	if err := eg.Wait(); err != nil {
		return err
	}

	return t.onSyncComplete()
}

// Start begins async synchronization (ModeAsync only)
func (t *stateSync) Start(ctx context.Context) error {
	if t.config.Mode != ModeAsync {
		return errNotAsyncMode
	}

	// Acquire mutex to prevent concurrent Start/Restart
	t.syncMutex.Lock()
	defer t.syncMutex.Unlock()

	// Check if already started
	if !t.started.CompareAndSwap(false, true) {
		return errAlreadyStarted
	}

	return t.doStart(ctx)
}

// doStart contains the core Start() logic (called while holding syncMutex)
func (t *stateSync) doStart(ctx context.Context) error {
	// Create cancellable context
	syncCtx, cancel := context.WithCancel(ctx)
	t.cancelFunc = cancel

	// Start stuck detector if enabled
	if t.config.EnableStuckDetection && t.stuckDetector != nil {
		// In real implementation: t.stuckDetector.Start(syncCtx)
	}

	eg, egCtx := errgroup.WithContext(syncCtx)

	// Start code syncer
	t.codeSyncer.Start(egCtx)

	// Start leaf syncer
	// In real implementation: t.syncer.Start(egCtx, t.workerCfg.NumWorkers, t.onSyncFailure)

	// Leaf syncer completion goroutine (with panic recovery)
	eg.Go(func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				cancel()
				err = fmt.Errorf("panic in syncer: %v", r)
			}
		}()

		// In real implementation: syncErr := <-t.syncer.Done()
		// For now, wait for context
		<-egCtx.Done()
		syncErr := egCtx.Err()

		if err != nil {
			return err // Return panic error
		}

		if syncErr != nil {
			return syncErr
		}

		return t.onSyncComplete()
	})

	// Code syncer completion goroutine (with panic recovery)
	eg.Go(func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic in codeSyncer: %v", r)
			}
		}()

		codeSyncErr := <-t.codeSyncer.Done()

		if err != nil {
			return err // Return panic error
		}

		if codeSyncErr != nil {
			// Context cancelled is expected during shutdown
			if errors.Is(codeSyncErr, context.Canceled) {
				return codeSyncErr
			}

			// Code sync failure prevents commit
			t.storeCodeSyncError(codeSyncErr)
			return fmt.Errorf("code sync failed (state preserved for retry): %w", codeSyncErr)
		}

		return nil
	})

	// Storage trie producer goroutine (with panic recovery)
	eg.Go(func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				cancel()
				err = fmt.Errorf("panic in storageTrieProducer: %v", r)
			}
		}()

		producerErr := t.storageTrieProducer(egCtx)

		if err != nil {
			return err // Return panic error
		}

		return producerErr
	})

	// Stuck detection monitoring (if enabled)
	if t.config.EnableStuckDetection {
		eg.Go(func() error {
			// In real implementation:
			// select {
			// case <-t.stuckDetector.StuckChannel():
			//     cancel()
			//     return errStateSyncStuck
			// case <-egCtx.Done():
			//     return nil
			// }
			<-egCtx.Done()
			return nil
		})
	}

	// Launch background wait routine
	go func() {
		err := eg.Wait()

		// Stop stuck detector
		if t.config.EnableStuckDetection && t.stuckDetector != nil {
			// t.stuckDetector.Stop()
		}

		// Handle errors and fallback
		if err != nil && !errors.Is(err, context.Canceled) {
			// Error categorization would go here for Subnet-EVM
			// if shouldFallbackToBlockSync(err) {
			//     t.persistStateSyncFailure()
			//     err = errStateSyncStuck
			// }
		}

		// Clear sync mode markers on success
		if err == nil {
			// customrawdb.DeleteSyncMode(t.db)
		}

		t.done <- err
	}()

	return nil
}

// Wait blocks until async synchronization completes (ModeAsync only)
func (t *stateSync) Wait(ctx context.Context) error {
	if t.config.Mode != ModeAsync {
		return errNotAsyncMode
	}

	if t.cancelFunc == nil {
		return errWaitBeforeStart
	}

	// Check if Wait() already called - return cached result
	if !t.waitStarted.CompareAndSwap(false, true) {
		<-t.resultReady

		result := t.cachedResult.Load()
		if result == nil {
			return errors.New("internal error: resultReady closed but cachedResult is nil")
		}

		errPtr, ok := result.(*error)
		if !ok {
			return errors.New("internal error: cachedResult has wrong type")
		}
		return *errPtr
	}

	// First Wait() call - wait for completion
	var resultErr error
	select {
	case err := <-t.done:
		resultErr = err
	case <-ctx.Done():
		t.cancelFunc()
		<-t.done
		resultErr = ctx.Err()
	}

	// Cache result and signal other waiters
	t.cachedResult.Store(&resultErr)
	close(t.resultReady)
	return resultErr
}

// Restart reinitializes for another attempt (ModeAsync with EnableRestart only)
func (t *stateSync) Restart(ctx context.Context, startReqID uint32) error {
	if !t.config.EnableRestart {
		return errRestartDisabled
	}

	if t.config.Mode != ModeAsync {
		return errNotAsyncMode
	}

	// Acquire mutex to prevent concurrent Start/Restart
	t.syncMutex.Lock()
	defer t.syncMutex.Unlock()

	// Check restart limit
	currentRestartCount := t.restartCount.Add(1)
	if currentRestartCount > uint32(t.config.MaxRestartAttempts) {
		return fmt.Errorf("restart limit exceeded (%d > %d)",
			currentRestartCount, t.config.MaxRestartAttempts)
	}

	// Verify Wait() was called
	if !t.waitStarted.Load() {
		return errors.New("cannot restart: Wait() must be called before Restart()")
	}

	// Check if sync is running
	if t.started.Load() {
		return errors.New("cannot restart: state sync already running")
	}

	// Reset atomic flags
	t.started.Store(false)
	t.waitStarted.Store(false)

	// Cancel old context
	if t.cancelFunc != nil {
		t.cancelFunc()

		select {
		case <-t.done:
		case <-time.After(10 * time.Second):
		}
	}
	t.cancelFunc = nil

	// Recreate channels
	t.done = make(chan error, 1)
	t.resultReady = make(chan struct{})
	t.mainTrieDone = make(chan struct{})
	t.storageTriesDone = make(chan struct{})
	t.segments = make(chan interface{}, t.workerCfg.NumWorkers*numStorageTrieSegments)
	t.triesInProgressSem = make(chan struct{}, t.workerCfg.NumWorkers)

	// Reset Once guards
	t.mainTrieDoneOnce = sync.Once{}
	t.segmentsDoneOnce = sync.Once{}
	t.storageTriesDoneOnce = sync.Once{}

	// Recreate syncer and codeSyncer
	// t.syncer = syncclient.NewCallbackLeafSyncer(...)
	// t.codeSyncer = newCodeSyncer(...)

	// Clear triesInProgress
	t.lock.Lock()
	t.triesInProgress = make(map[common.Hash]interface{})
	t.lock.Unlock()

	// Reset stats
	// t.stats = newTrieSyncStats()

	// Verify trieQueue root
	// if err := t.trieQueue.clearIfRootDoesNotMatch(t.root); err != nil {
	//     return err
	// }

	// Recreate mainTrie
	// t.mainTrie, err = NewTrieToSync(...)
	// t.addTrieInProgress(t.root, t.mainTrie)
	// t.mainTrie.startSyncing()

	// Recreate stuck detector
	if t.config.EnableStuckDetection {
		// t.stuckDetector = NewStuckDetector(t.stats)
	}

	// Start the sync again
	return t.doStart(ctx)
}

// storageTrieProducer waits for main trie then produces storage trie tasks
func (t *stateSync) storageTrieProducer(ctx context.Context) error {
	// Choose wait channel based on mode
	var waitChan <-chan struct{}
	if t.config.Mode == ModeBlocking {
		// Coreth: wait for 95% completion for overlap
		waitChan = t.mainTrieNearlydone
	} else {
		// Subnet-EVM: wait for 100% completion
		waitChan = t.mainTrieDone
	}

	select {
	case <-waitChan:
	case <-ctx.Done():
		return ctx.Err()
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		// Get next trie from queue
		// In real implementation:
		// root, accounts, more, err := t.trieQueue.getNextTrie()
		// For now, exit immediately

		// Close segments channel when done
		if t.config.Mode == ModeBlocking {
			close(t.segments)
		} else {
			t.segmentsDoneOnce.Do(func() {
				close(t.segments)
			})
		}
		return nil

		// Real implementation would:
		// - Acquire semaphore
		// - Create storageTrie
		// - Add to triesInProgress
		// - Start syncing
		// - Loop until no more tries
	}
}

// onSyncComplete writes final batches
func (t *stateSync) onSyncComplete() error {
	// In real implementation: t.mainTrie.batch.Write()
	t.syncCompleted.Store(true)
	return nil
}

// Finalize writes buffered data to disk
func (t *stateSync) Finalize() error {
	if t.config.Mode == ModeBlocking {
		// Coreth: check syncCompleted flag
		if t.syncCompleted.Load() {
			return nil
		}
	}

	t.lock.RLock()
	defer t.lock.RUnlock()

	// Write all in-progress trie segment batches
	for _, trie := range t.triesInProgress {
		// In real implementation:
		// for _, segment := range trie.segments {
		//     segment.batch.Write()
		// }
	}

	return nil
}

// Helper methods

func (t *stateSync) addTrieInProgress(root common.Hash, trie interface{}) {
	t.lock.Lock()
	defer t.lock.Unlock()
	t.triesInProgress[root] = trie
}

func (t *stateSync) removeTrieInProgress(root common.Hash) (int, error) {
	t.lock.Lock()
	defer t.lock.Unlock()

	// t.stats.trieDone(root)

	if _, ok := t.triesInProgress[root]; !ok {
		return 0, fmt.Errorf("removeTrieInProgress for unexpected root: %s", root)
	}

	delete(t.triesInProgress, root)
	return len(t.triesInProgress), nil
}

func (t *stateSync) storeCodeSyncError(err error) {
	if err != nil {
		t.codeSyncErr.Store(err)
		t.codeSyncFailed.Store(true)
	}
}

func (t *stateSync) onSyncFailure(error) error {
	// Write all batches to preserve progress
	t.lock.RLock()
	defer t.lock.RUnlock()

	for _, trie := range t.triesInProgress {
		// In real implementation:
		// for _, segment := range trie.segments {
		//     segment.batch.Write()
		// }
	}

	return nil
}

// validateConfig checks configuration validity
func validateConfig(config StateSyncConfig) error {
	if config.DB == nil {
		return errors.New("database required")
	}

	if config.Client == nil {
		return errors.New("client required")
	}

	if config.CodeSyncer == nil {
		return errors.New("code syncer required")
	}

	if config.RequestSize == 0 {
		return errors.New("request size must be > 0")
	}

	if config.Mode == ModeAsync {
		if config.EnableRestart && config.MaxRestartAttempts == 0 {
			return errors.New("max restart attempts must be > 0 when restart enabled")
		}
	}

	return nil
}
