// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package interval

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/utils/timer"
)

const (
	intervalPrefixByte byte = iota
	blockPrefixByte
	checkpointPrefixByte
	executeCheckpointPrefixByte

	prefixLen = 1
)

var (
	intervalPrefix          = []byte{intervalPrefixByte}
	blockPrefix             = []byte{blockPrefixByte}
	checkpointPrefix        = []byte{checkpointPrefixByte}
	executeCheckpointPrefix = []byte{executeCheckpointPrefixByte}

	errInvalidKeyLength = errors.New("invalid key length")
)

// FetchCheckpoint stores bootstrap FETCH phase progress
type FetchCheckpoint struct {
	Height              uint64         `json:"height"`
	TipHeight           uint64         `json:"tipHeight"`
	StartingHeight      uint64         `json:"startingHeight"`
	NumBlocksFetched    uint64         `json:"numBlocksFetched"`
	Timestamp           time.Time      `json:"timestamp"`
	MissingBlockIDCount int            `json:"missingBlockIDCount"`
	ETASamples          []timer.Sample `json:"etaSamples"`
}

// ExecuteCheckpoint stores bootstrap EXECUTE phase progress
// This checkpoint is created periodically during block execution to prevent
// progress loss if the node crashes during the long execution phase
type ExecuteCheckpoint struct {
	NumExecuted        uint64         `json:"numExecuted"`        // Blocks executed so far
	TotalToExecute     uint64         `json:"totalToExecute"`     // Total blocks to execute
	LastAcceptedHeight uint64         `json:"lastAcceptedHeight"` // Height of last accepted block
	StartingHeight     uint64         `json:"startingHeight"`     // Starting height (for validation)
	Timestamp          time.Time      `json:"timestamp"`          // Checkpoint creation time
	ETASamples         []timer.Sample `json:"etaSamples"`         // ETA tracker samples
}

func GetIntervals(db database.Iteratee) ([]*Interval, error) {
	it := db.NewIteratorWithPrefix(intervalPrefix)
	defer it.Release()

	var intervals []*Interval
	for it.Next() {
		dbKey := it.Key()
		if len(dbKey) < prefixLen {
			return nil, errInvalidKeyLength
		}

		intervalKey := dbKey[prefixLen:]
		upperBound, err := database.ParseUInt64(intervalKey)
		if err != nil {
			return nil, err
		}

		value := it.Value()
		lowerBound, err := database.ParseUInt64(value)
		if err != nil {
			return nil, err
		}

		intervals = append(intervals, &Interval{
			LowerBound: lowerBound,
			UpperBound: upperBound,
		})
	}
	return intervals, it.Error()
}

func PutInterval(db database.KeyValueWriter, upperBound uint64, lowerBound uint64) error {
	return database.PutUInt64(db, makeIntervalKey(upperBound), lowerBound)
}

func DeleteInterval(db database.KeyValueDeleter, upperBound uint64) error {
	return db.Delete(makeIntervalKey(upperBound))
}

// makeIntervalKey uses the upperBound rather than the lowerBound because blocks
// are fetched from tip towards genesis. This means that it is more common for
// the lowerBound to change than the upperBound. Modifying the lowerBound only
// requires a single write rather than a write and a delete when modifying the
// upperBound.
func makeIntervalKey(upperBound uint64) []byte {
	intervalKey := database.PackUInt64(upperBound)
	return append(intervalPrefix, intervalKey...)
}

// GetBlockIterator returns a block iterator that will produce values
// corresponding to persisted blocks in order of increasing height.
func GetBlockIterator(db database.Iteratee) database.Iterator {
	return db.NewIteratorWithPrefix(blockPrefix)
}

// GetBlockIteratorWithStart returns a block iterator that will produce values
// corresponding to persisted blocks in order of increasing height starting at
// [height].
func GetBlockIteratorWithStart(db database.Iteratee, height uint64) database.Iterator {
	return db.NewIteratorWithStartAndPrefix(
		makeBlockKey(height),
		blockPrefix,
	)
}

func GetBlock(db database.KeyValueReader, height uint64) ([]byte, error) {
	return db.Get(makeBlockKey(height))
}

func PutBlock(db database.KeyValueWriter, height uint64, bytes []byte) error {
	return db.Put(makeBlockKey(height), bytes)
}

func DeleteBlock(db database.KeyValueDeleter, height uint64) error {
	return db.Delete(makeBlockKey(height))
}

// makeBlockKey ensures that the returned key maintains the same sorted order as
// the height. This ensures that database iteration of block keys will iterate
// from lower height to higher height.
func makeBlockKey(height uint64) []byte {
	blockKey := database.PackUInt64(height)
	return append(blockPrefix, blockKey...)
}

// GetFetchCheckpoint retrieves the saved checkpoint from the database
func GetFetchCheckpoint(db database.KeyValueReader) (*FetchCheckpoint, error) {
	data, err := db.Get(checkpointPrefix)
	if err != nil {
		if err == database.ErrNotFound {
			// Checkpoint doesn't exist, return nil without error
			return nil, nil
		}
		return nil, err
	}

	var checkpoint FetchCheckpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return nil, err
	}

	return &checkpoint, nil
}

// PutFetchCheckpoint saves a checkpoint to the database
func PutFetchCheckpoint(db database.KeyValueWriter, checkpoint *FetchCheckpoint) error {
	data, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}

	return db.Put(checkpointPrefix, data)
}

// DeleteFetchCheckpoint removes the checkpoint from the database
func DeleteFetchCheckpoint(db database.KeyValueDeleter) error {
	return db.Delete(checkpointPrefix)
}

// GetExecuteCheckpoint retrieves the saved execute checkpoint from the database
func GetExecuteCheckpoint(db database.KeyValueReader) (*ExecuteCheckpoint, error) {
	data, err := db.Get(executeCheckpointPrefix)
	if err != nil {
		if err == database.ErrNotFound {
			// Checkpoint doesn't exist, return nil without error
			return nil, nil
		}
		return nil, err
	}

	var checkpoint ExecuteCheckpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return nil, err
	}

	return &checkpoint, nil
}

// PutExecuteCheckpoint saves an execute checkpoint to the database
func PutExecuteCheckpoint(db database.KeyValueWriter, checkpoint *ExecuteCheckpoint) error {
	data, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}

	return db.Put(executeCheckpointPrefix, data)
}

// DeleteExecuteCheckpoint removes the execute checkpoint from the database
func DeleteExecuteCheckpoint(db database.KeyValueDeleter) error {
	return db.Delete(executeCheckpointPrefix)
}
