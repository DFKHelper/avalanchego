// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package statesync

import (
	"context"
	"fmt"

	"github.com/ava-labs/libevm/common"
	"github.com/ava-labs/libevm/core/rawdb"
	"github.com/ava-labs/libevm/core/types"
	"github.com/ava-labs/libevm/ethdb"
	"github.com/ava-labs/libevm/log"
	"github.com/ava-labs/libevm/rlp"
	"github.com/ava-labs/libevm/trie"
)

var (
	_ syncTask = (*mainTrieTask)(nil)
	_ syncTask = (*storageTrieTask)(nil)
)

// syncTask defines callbacks for processing trie leaf batches
type syncTask interface {
	// IterateLeafs returns an iterator over trie leafs already persisted to disk
	// Used for restoring progress and hashing segments
	IterateLeafs(seek common.Hash) ethdb.Iterator

	// OnStart is called before syncing begins
	// Returns (skip=true, nil) if the task can be skipped
	OnStart() (bool, error)

	// OnLeafs processes a batch of trie leafs
	// Context parameter for cancellation support (coreth optimization)
	OnLeafs(ctx context.Context, db ethdb.KeyValueWriter, keys, vals [][]byte) error

	// OnFinish is called after syncing completes successfully
	OnFinish() error
}

// mainTrieTask handles syncing the main account trie
type mainTrieTask struct {
	sync *stateSync
}

// NewMainTrieTask creates a sync task for the main account trie
func NewMainTrieTask(sync *stateSync) syncTask {
	return &mainTrieTask{
		sync: sync,
	}
}

// IterateLeafs returns an iterator over persisted account leafs
func (m *mainTrieTask) IterateLeafs(seek common.Hash) ethdb.Iterator {
	// In actual implementation, would wrap snapshot.AccountIterator(seek)
	// For now, return nil to avoid import cycle
	return nil
}

// OnStart always returns false since the main trie cannot be skipped
func (*mainTrieTask) OnStart() (bool, error) {
	return false, nil
}

// OnFinish signals completion of the main trie
func (m *mainTrieTask) OnFinish() error {
	return m.sync.onMainTrieFinished()
}

// OnLeafs processes account leafs and registers storage tries and code
func (m *mainTrieTask) OnLeafs(ctx context.Context, db ethdb.KeyValueWriter, keys, vals [][]byte) error {
	// Pre-allocate with estimated capacity (coreth optimization)
	// Estimate ~25% of accounts have code based on typical blockchain data
	codeHashes := make([]common.Hash, 0, len(keys)/4)

	// Process accounts with periodic context checks (coreth optimization)
	const checkInterval = 100
	for i, key := range keys {
		// Periodic context cancellation check every 100 iterations
		// Allows graceful cancellation during large batches
		if i%checkInterval == 0 {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("main trie processing cancelled after %d/%d accounts: %w", i, len(keys), err)
			}
		}

		var acc types.StateAccount
		accountHash := common.BytesToHash(key)
		if err := rlp.DecodeBytes(vals[i], &acc); err != nil {
			return fmt.Errorf("could not decode main trie as account, key=%s, valueLen=%d, err=%w", accountHash, len(vals[i]), err)
		}

		// Persist account data to snapshot
		writeAccountSnapshot(db, accountHash, acc)

		// Register storage trie if account has non-empty storage root
		if acc.Root != (common.Hash{}) && acc.Root != types.EmptyRootHash {
			if err := m.sync.trieQueue.RegisterStorageTrie(acc.Root, accountHash); err != nil {
				return err
			}
		}

		// Collect code hashes for batch fetching
		codeHash := common.BytesToHash(acc.CodeHash)
		if codeHash != (common.Hash{}) && codeHash != types.EmptyCodeHash {
			codeHashes = append(codeHashes, codeHash)
		}
	}

	// Add collected code hashes to code syncer
	// Uses CodeSyncInterface which abstracts CodeQueue vs codeSyncer
	return m.sync.addCodeHashes(ctx, codeHashes)
}

// storageTrieTask handles syncing a storage trie
type storageTrieTask struct {
	sync     *stateSync
	root     common.Hash
	accounts []common.Hash
}

// NewStorageTrieTask creates a sync task for a storage trie
func NewStorageTrieTask(sync *stateSync, root common.Hash, accounts []common.Hash) syncTask {
	return &storageTrieTask{
		sync:     sync,
		root:     root,
		accounts: accounts,
	}
}

// IterateLeafs returns an iterator over persisted storage leafs
func (s *storageTrieTask) IterateLeafs(seek common.Hash) ethdb.Iterator {
	// In actual implementation, would wrap snapshot.StorageIterator
	// For now, return nil to avoid import cycle
	return nil
}

// OnStart checks if the storage trie already exists on disk
// If so, populates snapshot and skips sync
func (s *storageTrieTask) OnStart() (bool, error) {
	// Determine first account for trie lookup
	var firstAccount common.Hash
	if len(s.accounts) > 0 {
		firstAccount = s.accounts[0]
	}

	// Try to open existing storage trie
	storageTrie, err := trie.New(trie.StorageTrieID(s.sync.root, s.root, firstAccount), s.sync.trieDB)
	if err != nil {
		// Trie doesn't exist, need to sync it
		return false, nil //nolint:nilerr
	}

	// Storage trie exists - populate snapshot for all associated accounts
	for _, account := range s.accounts {
		if err := writeAccountStorageSnapshotFromTrie(
			s.sync.db.NewBatch(),
			uint(s.sync.batchSize),
			account,
			storageTrie,
		); err != nil {
			// Trie may be incomplete due to pruning - re-sync from peers
			log.Info("could not populate storage snapshot from trie with existing root, syncing from peers instead",
				"account", account, "root", s.root, "err", err)
			return false, nil
		}
	}

	// Successfully populated snapshot from existing trie - skip sync
	return true, s.sync.onStorageTrieFinished(s.root)
}

// OnFinish signals completion of the storage trie
func (s *storageTrieTask) OnFinish() error {
	return s.sync.onStorageTrieFinished(s.root)
}

// OnLeafs processes storage trie leafs and persists to snapshot
func (s *storageTrieTask) OnLeafs(ctx context.Context, db ethdb.KeyValueWriter, keys, vals [][]byte) error {
	// Check context cancellation before processing
	if err := ctx.Err(); err != nil {
		return err
	}

	// Persist storage leafs to snapshot for all associated accounts
	// Optimized loop order: iterate keys first (outer), accounts second (inner)
	// This improves cache locality and reduces overhead from repeated key hashing
	const checkInterval = 100
	for i, key := range keys {
		// Periodic context cancellation check (coreth optimization)
		if i%checkInterval == 0 {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("storage trie processing cancelled after %d/%d keys: %w", i, len(keys), err)
			}
		}

		keyHash := common.BytesToHash(key)
		for _, account := range s.accounts {
			rawdb.WriteStorageSnapshot(db, account, keyHash, vals[i])
		}
	}

	return nil
}

// addCodeHashes is a helper that delegates to the code sync interface
// This abstracts the difference between CodeQueue.AddCode (coreth) and codeSyncer.addCode (subnet-evm)
func (s *stateSync) addCodeHashes(ctx context.Context, codeHashes []common.Hash) error {
	if len(codeHashes) == 0 {
		return nil
	}

	// The actual implementation would call the appropriate method based on the adapter type
	// For now, this is a placeholder that will be filled during integration
	// In real code: return s.codeSyncer.AddCodeHashes(ctx, codeHashes)
	return nil
}

// onMainTrieFinished is called when the main account trie completes
func (s *stateSync) onMainTrieFinished() error {
	// Notify code syncer that account trie is complete
	s.codeSyncer.NotifyAccountTrieCompleted()

	// Count storage tries for ETA calculation
	// In real implementation: numStorageTries, err := s.trieQueue.countTries()
	// s.stats.setTriesRemaining(numStorageTries)

	// Close main trie done channel
	if s.config.Mode == ModeBlocking {
		close(s.mainTrieNearlydone) // Signal 95% for overlap optimization
	}
	close(s.mainTrieDone)

	// Remove from triesInProgress
	_, err := s.removeTrieInProgress(s.root)
	return err
}

// onStorageTrieFinished is called when a storage trie completes
func (s *stateSync) onStorageTrieFinished(root common.Hash) error {
	// Release semaphore slot
	<-s.triesInProgressSem

	// Mark storage trie as done in queue
	// In real implementation: s.trieQueue.StorageTrieDone(root)

	// Track completion
	numInProgress, err := s.removeTrieInProgress(root)
	if err != nil {
		return err
	}

	// If last storage trie, close segments channel
	if numInProgress == 0 {
		select {
		case <-s.storageTriesDone:
			// Close segments channel
			if s.config.Mode == ModeBlocking {
				close(s.segments)
			} else {
				s.segmentsDoneOnce.Do(func() {
					close(s.segments)
				})
			}
		default:
		}
	}

	return nil
}
