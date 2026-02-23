// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package bootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/database/memdb"
	"github.com/ava-labs/avalanchego/snow/engine/snowman/bootstrap/interval"
	"github.com/ava-labs/avalanchego/snow/snowtest"
	"github.com/ava-labs/avalanchego/utils/timer"
)

// TestHasProgressValidation tests that HasProgress() properly preserves
// bootstrap progress. The conservative approach: if blocks exist, preserve them.
func TestHasProgressValidation(t *testing.T) {
	setup := func() (*Bootstrapper, *memdb.Database) {
		ctx := snowtest.Context(t, snowtest.CChainID)
		db := memdb.New()

		bs := &Bootstrapper{
			Config: Config{
				Ctx: snowtest.ConsensusContext(ctx),
				DB:  db,
			},
		}

		return bs, db
	}

	t.Run("no_blocks_no_progress", func(t *testing.T) {
		bs, _ := setup()

		hasProgress, err := bs.HasProgress(context.Background())
		require.NoError(t, err)
		require.False(t, hasProgress, "should have no progress with empty database")
	})

	t.Run("blocks_without_checkpoint_preserved", func(t *testing.T) {
		bs, db := setup()

		// Add some blocks to the tree without a checkpoint
		tree, err := interval.NewTree(db)
		require.NoError(t, err)
		_, err = interval.Add(db, tree, 0, 100, []byte("block100"))
		require.NoError(t, err)
		_, err = interval.Add(db, tree, 0, 101, []byte("block101"))
		require.NoError(t, err)

		// HasProgress should preserve blocks even without checkpoint
		hasProgress, err := bs.HasProgress(context.Background())
		require.NoError(t, err)
		require.True(t, hasProgress, "should preserve blocks even without checkpoint")

		// Verify state was NOT cleared
		tree2, err := interval.NewTree(db)
		require.NoError(t, err)
		require.Equal(t, uint64(2), tree2.Len(), "blocks should be preserved")
	})

	t.Run("blocks_with_corrupted_checkpoint_preserved", func(t *testing.T) {
		bs, db := setup()

		// Add blocks using Add() to properly update the interval tree
		tree, err := interval.NewTree(db)
		require.NoError(t, err)
		_, err = interval.Add(db, tree, 0, 100, []byte("block100"))
		require.NoError(t, err)

		// Write invalid checkpoint data (not JSON)
		err = db.Put([]byte{2}, []byte("corrupted"))
		require.NoError(t, err)

		// HasProgress should preserve blocks even with corrupted checkpoint
		hasProgress, err := bs.HasProgress(context.Background())
		require.NoError(t, err)
		require.True(t, hasProgress, "should preserve blocks even with corrupted checkpoint")
	})

	t.Run("blocks_with_future_timestamp_preserved", func(t *testing.T) {
		bs, db := setup()

		// Create checkpoint with future timestamp
		checkpoint := &interval.FetchCheckpoint{
			Height:              100000,
			TipHeight:           5000000,
			StartingHeight:      0,
			NumBlocksFetched:    50000,
			Timestamp:           time.Now().Add(10 * time.Minute), // Future!
			MissingBlockIDCount: 0,
			ETASamples:          []timer.Sample{},
		}

		// Add blocks and checkpoint using Add() for proper tree updates
		tree, err := interval.NewTree(db)
		require.NoError(t, err)
		for i := uint64(1); i <= 100; i++ {
			_, err := interval.Add(db, tree, 0, i, []byte("block"))
			require.NoError(t, err)
		}
		err = interval.PutFetchCheckpoint(db, checkpoint)
		require.NoError(t, err)

		// HasProgress should preserve blocks (conservative approach)
		hasProgress, err := bs.HasProgress(context.Background())
		require.NoError(t, err)
		require.True(t, hasProgress, "should preserve blocks even with future timestamp")
	})

	t.Run("very_old_checkpoint_preserved", func(t *testing.T) {
		bs, db := setup()

		// Create checkpoint >7 days old
		checkpoint := &interval.FetchCheckpoint{
			Height:              100000,
			TipHeight:           5000000,
			StartingHeight:      0,
			NumBlocksFetched:    50000,
			Timestamp:           time.Now().Add(-8 * 24 * time.Hour), // 8 days old
			MissingBlockIDCount: 0,
			ETASamples:          []timer.Sample{},
		}

		// Add blocks and checkpoint using Add() for proper tree updates
		tree, err := interval.NewTree(db)
		require.NoError(t, err)
		for i := uint64(1); i <= 100; i++ {
			_, err := interval.Add(db, tree, 0, i, []byte("block"))
			require.NoError(t, err)
		}
		err = interval.PutFetchCheckpoint(db, checkpoint)
		require.NoError(t, err)

		// Even old checkpoint preserves blocks (conservative)
		hasProgress, err := bs.HasProgress(context.Background())
		require.NoError(t, err)
		require.True(t, hasProgress, "should preserve blocks even with old checkpoint")
	})

	t.Run("valid_checkpoint_is_preserved", func(t *testing.T) {
		bs, db := setup()

		// Create VALID checkpoint with reasonable values
		numBlocks := uint64(1000) // Use smaller number to keep test fast
		checkpoint := &interval.FetchCheckpoint{
			Height:              100000,
			TipHeight:           5000000,
			StartingHeight:      0,
			NumBlocksFetched:    numBlocks,
			Timestamp:           time.Now().Add(-1 * time.Hour),
			MissingBlockIDCount: 100,
			ETASamples: []timer.Sample{
				{Completed: 500, Timestamp: time.Now().Add(-2 * time.Hour)},
				{Completed: 1000, Timestamp: time.Now().Add(-1 * time.Hour)},
			},
		}

		// Add blocks using Add() for proper tree updates
		tree, err := interval.NewTree(db)
		require.NoError(t, err)
		for i := uint64(1); i <= numBlocks; i++ {
			_, err := interval.Add(db, tree, 0, i, []byte("block"))
			require.NoError(t, err)
		}
		err = interval.PutFetchCheckpoint(db, checkpoint)
		require.NoError(t, err)

		// HasProgress should ACCEPT valid checkpoint
		hasProgress, err := bs.HasProgress(context.Background())
		require.NoError(t, err)
		require.True(t, hasProgress, "should preserve valid checkpoint")

		// Verify state was NOT cleared
		tree2, err := interval.NewTree(db)
		require.NoError(t, err)
		require.Equal(t, numBlocks, tree2.Len(), "valid state should not be cleared")
	})

	// Test that partial execution (blocks removed from tree) still preserves progress
	t.Run("partial_execution_preserved", func(t *testing.T) {
		bs, db := setup()

		// Checkpoint says 10000 blocks were fetched
		checkpoint := &interval.FetchCheckpoint{
			Height:              100000,
			TipHeight:           5000000,
			StartingHeight:      0,
			NumBlocksFetched:    10000,
			Timestamp:           time.Now().Add(-12 * time.Hour), // 12 hours ago
			MissingBlockIDCount: 0,
			ETASamples:          []timer.Sample{},
		}

		// But only 100 blocks remain (rest were executed and removed)
		tree, err := interval.NewTree(db)
		require.NoError(t, err)
		for i := uint64(1); i <= 100; i++ {
			_, err := interval.Add(db, tree, 0, i, []byte("block"))
			require.NoError(t, err)
		}
		err = interval.PutFetchCheckpoint(db, checkpoint)
		require.NoError(t, err)

		// Should preserve even though tree.Len() << NumBlocksFetched
		hasProgress, err := bs.HasProgress(context.Background())
		require.NoError(t, err)
		require.True(t, hasProgress, "should preserve partial execution progress")
	})
}

// TestValidateCheckpointStartingHeight tests that validateCheckpoint
// properly rejects checkpoints with mismatched starting heights.
func TestValidateCheckpointStartingHeight(t *testing.T) {
	ctx := snowtest.Context(t, snowtest.CChainID)
	db := memdb.New()

	bs := &Bootstrapper{
		Config: Config{
			Ctx: snowtest.ConsensusContext(ctx),
			DB:  db,
		},
	}

	t.Run("stale_checkpoint_rejected", func(t *testing.T) {
		bs.startingHeight = 55000

		checkpoint := &interval.FetchCheckpoint{
			Height:              100000,
			TipHeight:           5000000,
			StartingHeight:      50000, // Old starting height
			NumBlocksFetched:    50000,
			Timestamp:           time.Now(),
			MissingBlockIDCount: 0,
			ETASamples:          []timer.Sample{},
		}

		valid := bs.validateCheckpoint(checkpoint)
		require.False(t, valid, "should reject stale checkpoint with mismatched StartingHeight")
	})

	t.Run("matching_starting_height_accepted", func(t *testing.T) {
		bs.startingHeight = 50000

		checkpoint := &interval.FetchCheckpoint{
			Height:              100000,
			TipHeight:           5000000,
			StartingHeight:      50000, // Matches current!
			NumBlocksFetched:    50000,
			Timestamp:           time.Now(),
			MissingBlockIDCount: 0,
			ETASamples:          []timer.Sample{},
		}

		valid := bs.validateCheckpoint(checkpoint)
		require.True(t, valid, "should accept checkpoint with matching StartingHeight")
	})

	t.Run("age_within_7_days_accepted", func(t *testing.T) {
		bs.startingHeight = 0

		checkpoint := &interval.FetchCheckpoint{
			Height:              100000,
			TipHeight:           5000000,
			StartingHeight:      0,
			NumBlocksFetched:    50000,
			Timestamp:           time.Now().Add(-48 * time.Hour), // 2 days old - OK
			MissingBlockIDCount: 0,
			ETASamples:          []timer.Sample{},
		}

		valid := bs.validateCheckpoint(checkpoint)
		require.True(t, valid, "should accept checkpoint within 7-day window")
	})

	t.Run("age_beyond_7_days_rejected", func(t *testing.T) {
		bs.startingHeight = 0

		checkpoint := &interval.FetchCheckpoint{
			Height:              100000,
			TipHeight:           5000000,
			StartingHeight:      0,
			NumBlocksFetched:    50000,
			Timestamp:           time.Now().Add(-8 * 24 * time.Hour), // 8 days old
			MissingBlockIDCount: 0,
			ETASamples:          []timer.Sample{},
		}

		valid := bs.validateCheckpoint(checkpoint)
		require.False(t, valid, "should reject checkpoint older than 7 days")
	})
}
