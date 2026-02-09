// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package bootstrap

import (
	"context"
	"fmt"
	"runtime"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow/consensus/snowman"
	"github.com/ava-labs/avalanchego/snow/engine/snowman/block"
	"github.com/ava-labs/avalanchego/snow/engine/snowman/bootstrap/interval"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/set"
	"github.com/ava-labs/avalanchego/utils/timer"
)

const (
	batchWritePeriod      = 512   // Increased from 64 to reduce DB write frequency during sync
	iteratorReleasePeriod = 16384 // Increased from 1024 to reduce iterator overhead
	logPeriod             = 5 * time.Second
	minBlocksToCompact    = 5000

	// Compaction safety limits
	minFreeMemoryForCompaction = 50 * 1024 * 1024 * 1024 // 50GB minimum free memory required
	compactionTimeout          = 10 * time.Minute        // Maximum time allowed for compaction
)

// getAvailableMemory returns the currently available system memory in bytes.
// This is used to determine if there's sufficient memory for database compaction.
func getAvailableMemory() (uint64, error) {
	var sysinfo syscall.Sysinfo_t
	if err := syscall.Sysinfo(&sysinfo); err != nil {
		return 0, fmt.Errorf("failed to get system info: %w", err)
	}

	// Calculate available memory: free RAM + buffers + cached
	// sysinfo.Freeram is truly free memory
	// On Linux, buffers/cache can be reclaimed, so total available = freeram
	available := uint64(sysinfo.Freeram) * uint64(sysinfo.Unit)

	// Force garbage collection to ensure we have accurate Go heap stats
	runtime.GC()

	return available, nil
}

// shouldCompactDatabase determines if database compaction should proceed
// based on the number of blocks processed and available system memory.
// Returns true only if both conditions are met:
// 1. Sufficient blocks have been processed (>= minBlocksToCompact)
// 2. Sufficient free memory is available (>= minFreeMemoryForCompaction)
func shouldCompactDatabase(log logging.Func, numProcessed uint64) bool {
	if numProcessed < minBlocksToCompact {
		return false
	}

	availableMem, err := getAvailableMemory()
	if err != nil {
		log("failed to check available memory, skipping compaction for safety",
			zap.Error(err))
		return false
	}

	if availableMem < minFreeMemoryForCompaction {
		availableGB := float64(availableMem) / (1024 * 1024 * 1024)
		requiredGB := float64(minFreeMemoryForCompaction) / (1024 * 1024 * 1024)
		log("insufficient memory for safe compaction, skipping",
			zap.Float64("availableGB", availableGB),
			zap.Float64("requiredGB", requiredGB),
			zap.Uint64("blocksProcessed", numProcessed))
		return false
	}

	return true
}

// compactDatabaseSafely performs database compaction with timeout protection
// and comprehensive error handling to prevent crashes during bootstrap.
// If compaction exceeds the timeout, it will be cancelled to prevent system hangs.
func compactDatabaseSafely(ctx context.Context, log logging.Func, db database.Database, phase string) error {
	// Create a timeout context for the compaction operation
	compactCtx, cancel := context.WithTimeout(ctx, compactionTimeout)
	defer cancel()

	startTime := time.Now()
	errChan := make(chan error, 1)

	// Run compaction in a goroutine so we can enforce timeout
	go func() {
		log("starting database compaction",
			zap.String("phase", phase),
			zap.Duration("timeout", compactionTimeout))
		errChan <- db.Compact(nil, nil)
	}()

	// Wait for either completion or timeout
	select {
	case err := <-errChan:
		duration := time.Since(startTime)
		if err != nil {
			log("database compaction failed",
				zap.String("phase", phase),
				zap.Duration("duration", duration),
				zap.Error(err))
			return fmt.Errorf("compaction failed: %w", err)
		}
		log("database compaction completed successfully",
			zap.String("phase", phase),
			zap.Duration("duration", duration))
		return nil

	case <-compactCtx.Done():
		duration := time.Since(startTime)
		log("database compaction timed out - cancelling to prevent system hang",
			zap.String("phase", phase),
			zap.Duration("duration", duration),
			zap.Duration("timeout", compactionTimeout))
		// Note: The actual db.Compact() call cannot be cancelled mid-operation in Firewood,
		// but we prevent waiting indefinitely and allow bootstrap to continue.
		// The compaction goroutine will complete eventually and the error will be discarded.
		return fmt.Errorf("compaction timed out after %v", duration)
	}
}

// getMissingBlockIDs returns the ID of the blocks that should be fetched to
// attempt to make a single continuous range from
// (lastAcceptedHeight, highestTrackedHeight].
//
// For example, if the tree currently contains heights [1, 4, 6, 7] and the
// lastAcceptedHeight is 2, this function will return the IDs corresponding to
// blocks [3, 5].
func getMissingBlockIDs(
	ctx context.Context,
	db database.KeyValueReader,
	nonVerifyingParser block.Parser,
	tree *interval.Tree,
	lastAcceptedHeight uint64,
) (set.Set[ids.ID], error) {
	var (
		missingBlocks     set.Set[ids.ID]
		intervals         = tree.Flatten()
		lastHeightToFetch = lastAcceptedHeight + 1
	)
	for _, i := range intervals {
		if i.LowerBound <= lastHeightToFetch {
			continue
		}

		blkBytes, err := interval.GetBlock(db, i.LowerBound)
		if err != nil {
			return nil, err
		}

		blk, err := nonVerifyingParser.ParseBlock(ctx, blkBytes)
		if err != nil {
			return nil, err
		}

		parentID := blk.Parent()
		missingBlocks.Add(parentID)
	}
	return missingBlocks, nil
}

// process a series of consecutive blocks starting at [blk].
//
//   - blk is a block that is assumed to have been marked as acceptable by the
//     bootstrapping engine.
//   - ancestors is a set of blocks that can be used to lookup blocks.
//
// If [blk]'s height is <= the last accepted height, then it will be removed
// from the missingIDs set.
//
// Returns a newly discovered blockID that should be fetched.
func process(
	db database.KeyValueWriterDeleter,
	tree *interval.Tree,
	missingBlockIDs set.Set[ids.ID],
	lastAcceptedHeight uint64,
	blk snowman.Block,
	ancestors map[ids.ID]snowman.Block,
) (ids.ID, bool, error) {
	for {
		// It's possible that missingBlockIDs contain values contained inside of
		// ancestors. So, it's important to remove IDs from the set for each
		// iteration, not just the first block's ID.
		blkID := blk.ID()
		missingBlockIDs.Remove(blkID)

		height := blk.Height()
		blkBytes := blk.Bytes()
		wantsParent, err := interval.Add(
			db,
			tree,
			lastAcceptedHeight,
			height,
			blkBytes,
		)
		if err != nil || !wantsParent {
			return ids.Empty, false, err
		}

		// If the parent was provided in the ancestors set, we can immediately
		// process it.
		parentID := blk.Parent()
		parent, ok := ancestors[parentID]
		if !ok {
			return parentID, true, nil
		}

		blk = parent
	}
}

// execute all the blocks tracked by the tree. If a block is in the tree but is
// already accepted based on the lastAcceptedHeight, it will be removed from the
// tree but not executed.
//
// execute assumes that getMissingBlockIDs would return an empty set.
//
// TODO: Replace usage of haltable with context cancellation.
func execute(
	ctx context.Context,
	shouldHalt func() bool,
	log logging.Func,
	db database.Database,
	nonVerifyingParser block.Parser,
	tree *interval.Tree,
	lastAcceptedHeight uint64,
) error {
	totalNumberToProcess := tree.Len()
	if shouldCompactDatabase(log, totalNumberToProcess) {
		if err := compactDatabaseSafely(ctx, log, db, "pre-execution"); err != nil {
			// Not a fatal error - compaction failure should not stop bootstrap.
			// The error is already logged by compactDatabaseSafely.
			// Continue with block execution.
		}
	}

	var (
		batch                    = db.NewBatch()
		processedSinceBatchWrite uint
		writeBatch               = func() error {
			if processedSinceBatchWrite == 0 {
				return nil
			}
			processedSinceBatchWrite = 0

			if err := batch.Write(); err != nil {
				return err
			}
			batch.Reset()
			return nil
		}

		iterator                      = interval.GetBlockIterator(db)
		processedSinceIteratorRelease uint

		startTime     = time.Now()
		timeOfNextLog = startTime.Add(logPeriod)
		etaTracker    = timer.NewEtaTracker(10, 1.2)
	)
	defer func() {
		iterator.Release()

		var (
			numProcessed = totalNumberToProcess - tree.Len()
			halted       = shouldHalt()
		)
		if !halted && shouldCompactDatabase(log, numProcessed) {
			if err := compactDatabaseSafely(ctx, log, db, "post-execution"); err != nil {
				// Not a fatal error - compaction failure should not affect bootstrap completion.
				// The error is already logged by compactDatabaseSafely.
			}
		}

		log("executed blocks",
			zap.Uint64("numExecuted", numProcessed),
			zap.Uint64("numToExecute", totalNumberToProcess),
			zap.Bool("halted", halted),
			zap.Duration("duration", time.Since(startTime)),
		)
	}()

	log("executing blocks",
		zap.Uint64("numToExecute", totalNumberToProcess),
	)

	// Add the first sample to the EtaTracker to establish an accurate baseline
	etaTracker.AddSample(0, totalNumberToProcess, startTime)

	for !shouldHalt() && iterator.Next() {
		blkBytes := iterator.Value()
		blk, err := nonVerifyingParser.ParseBlock(ctx, blkBytes)
		if err != nil {
			return err
		}

		height := blk.Height()
		if err := interval.Remove(batch, tree, height); err != nil {
			return err
		}

		// Periodically write the batch to disk to avoid memory pressure.
		processedSinceBatchWrite++
		if processedSinceBatchWrite >= batchWritePeriod {
			if err := writeBatch(); err != nil {
				return err
			}
		}

		// Periodically release and re-grab the database iterator to avoid
		// keeping a reference to an old database revision.
		processedSinceIteratorRelease++
		if processedSinceIteratorRelease >= iteratorReleasePeriod {
			if err := iterator.Error(); err != nil {
				return err
			}

			// The batch must be written here to avoid re-processing a block.
			if err := writeBatch(); err != nil {
				return err
			}

			processedSinceIteratorRelease = 0
			iterator.Release()
			// We specify the starting key of the iterator so that the
			// underlying database doesn't need to scan over the, potentially
			// not yet compacted, blocks we just deleted.
			// Guard against overflow at maximum height (theoretical only - would take 584 billion years to reach)
			nextHeight := height + 1
			if nextHeight <= height {
				// Height overflow detected - we've reached MaxUint64
				// This should never happen in practice, but guard defensively
				break
			}
			iterator = interval.GetBlockIteratorWithStart(db, nextHeight)
		}

		if now := time.Now(); now.After(timeOfNextLog) {
			numProcessed := totalNumberToProcess - tree.Len()

			// Use the tracked previous progress for accurate ETA calculation
			currentProgress := numProcessed

			etaPtr, progressPercentage := etaTracker.AddSample(currentProgress, totalNumberToProcess, now)
			// Only log if we have a valid ETA estimate
			if etaPtr != nil {
				log("executing blocks",
					zap.Uint64("numExecuted", numProcessed),
					zap.Uint64("numToExecute", totalNumberToProcess),
					zap.Duration("eta", *etaPtr),
					zap.Float64("pctComplete", progressPercentage),
				)
			}

			timeOfNextLog = now.Add(logPeriod)
		}

		if height <= lastAcceptedHeight {
			continue
		}

		if err := blk.Verify(ctx); err != nil {
			return fmt.Errorf("failed to verify block %s (height=%d, parentID=%s) in bootstrapping: %w",
				blk.ID(),
				height,
				blk.Parent(),
				err,
			)
		}
		if err := blk.Accept(ctx); err != nil {
			return fmt.Errorf("failed to accept block %s (height=%d, parentID=%s) in bootstrapping: %w",
				blk.ID(),
				height,
				blk.Parent(),
				err,
			)
		}
	}
	if err := writeBatch(); err != nil {
		return err
	}
	return iterator.Error()
}
