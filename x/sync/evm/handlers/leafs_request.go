// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package handlers

import (
	"bytes"
	"context"
	"reflect"
	"sync"
	"time"

	"github.com/ava-labs/libevm/common"
	"github.com/ava-labs/libevm/core/types"
	"github.com/ava-labs/libevm/ethdb"
	"github.com/ava-labs/libevm/ethdb/memorydb"
	"github.com/ava-labs/libevm/log"
	"github.com/ava-labs/libevm/trie"
	"github.com/ava-labs/libevm/triedb"

	"github.com/ava-labs/avalanchego/codec"
	"github.com/ava-labs/avalanchego/graft/evm/core/state/snapshot"
	"github.com/ava-labs/avalanchego/graft/evm/utils"
	"github.com/ava-labs/avalanchego/ids"
)

// LeafsRequestStats defines all statistics tracked by the leafs request handler.
// Implementations can use this interface to record metrics in their preferred system.
type LeafsRequestStats interface {
	IncLeafsRequest()
	IncInvalidLeafsRequest()
	IncMissingRoot()
	IncSnapshotReadAttempt()
	IncSnapshotReadSuccess()
	IncSnapshotReadError()
	IncSnapshotSegmentValid()
	IncSnapshotSegmentInvalid()
	IncTrieError()
	IncProofError()
	UpdateLeafsRequestProcessingTime(duration time.Duration)
	UpdateLeafsReturned(count uint16)
	UpdateRangeProofValsReturned(count int64)
	UpdateGenerateRangeProofTime(duration time.Duration)
	UpdateReadLeafsTime(duration time.Duration)
	UpdateSnapshotReadTime(duration time.Duration)
}

// LeafsRequest represents the request message for leafs.
// This interface allows both coreth and subnet-evm message types.
type LeafsRequest interface {
	GetRoot() common.Hash
	GetAccount() common.Hash
	GetStart() []byte
	GetEnd() []byte
	GetLimit() uint16
}

// LeafsResponse represents the response message for leafs.
// This interface allows both coreth and subnet-evm message types.
type LeafsResponse interface {
	SetKeys(keys [][]byte)
	SetVals(vals [][]byte)
	SetProofVals(proofVals [][]byte)
	GetKeys() [][]byte
	GetVals() [][]byte
	GetProofVals() [][]byte
}

// proofDBPool manages a pool of reusable memorydb.Database instances for proof generation.
// Reduces GC pressure by reusing databases instead of creating new ones for each request.
// Only used when Version is VersionCoreth.
type proofDBPool struct {
	pool    sync.Pool
	enabled bool
}

// newProofDBPool creates a new pool for proof databases.
func newProofDBPool(enabled bool) *proofDBPool {
	if !enabled {
		return &proofDBPool{enabled: false}
	}
	return &proofDBPool{
		enabled: true,
		pool: sync.Pool{
			New: func() interface{} {
				return memorydb.New()
			},
		},
	}
}

// get returns a proof database (either from pool or newly created).
func (p *proofDBPool) get() *memorydb.Database {
	// Note: We don't actually pool these anymore because memorydb.New() is very cheap (just map allocation),
	// while clearing an existing database is O(n). Simpler to create new and let GC reclaim old ones.
	return memorydb.New()
}

// put closes the proof database and allows GC to reclaim it.
// We don't return it to the pool since clearing is more expensive than creating new.
func (p *proofDBPool) put(db *memorydb.Database) {
	if db == nil {
		return
	}
	_ = db.Close()
}

// LeafRequestHandler handles incoming leafs requests and generates responses.
type LeafRequestHandler interface {
	OnLeafsRequest(ctx context.Context, nodeID ids.NodeID, requestID uint32, leafsRequest interface{}) ([]byte, error)
}

// leafsRequestHandler is a unified handler for types.LeafsRequest
// serving requested trie data for both coreth and subnet-evm.
type leafsRequestHandler struct {
	trieDB           *triedb.Database
	snapshotProvider interface{} // *snapshot.Tree at runtime
	codec            codec.Manager
	stats            LeafsRequestStats
	trieKeyLength    int
	version          HandlerVersion
	proofDBPool      *proofDBPool // Pool for reusing proof databases (VersionCoreth only)
}

// NewLeafsRequestHandler creates a new leafs request handler.
func NewLeafsRequestHandler(
	trieDB *triedb.Database,
	trieKeyLength int,
	snapshotProvider interface{},
	codec codec.Manager,
	stats LeafsRequestStats,
	version HandlerVersion,
) *leafsRequestHandler {
	return &leafsRequestHandler{
		trieDB:           trieDB,
		snapshotProvider: snapshotProvider,
		codec:            codec,
		stats:            stats,
		trieKeyLength:    trieKeyLength,
		version:          version,
		proofDBPool:      newProofDBPool(version == VersionCoreth),
	}
}

// OnLeafsRequest returns encoded LeafsResponse for a given LeafsRequest.
// Returns leaves with proofs for specified (Start-End) (both inclusive) ranges.
// Returned LeafsResponse may contain partial leaves within requested Start and End range if:
// - ctx expired while fetching leafs
// - number of leaves read is greater than Limit (LeafsRequest)
// Specified Limit in LeafsRequest is overridden to MaxLeavesLimit if it is greater than MaxLeavesLimit.
// Expects returned errors to be treated as FATAL.
// Never returns errors.
// Returns nothing if NodeType is invalid or requested trie root is not found.
// Assumes ctx is active.
func (lrh *leafsRequestHandler) OnLeafsRequest(ctx context.Context, nodeID ids.NodeID, requestID uint32, leafsRequest interface{}) ([]byte, error) {
	startTime := time.Now()
	lrh.stats.IncLeafsRequest()

	// Extract fields from leafsRequest (handles both coreth and subnet-evm message types)
	root, account, start, end, limit := extractLeafsRequestFields(leafsRequest)

	// Validate request
	if (len(end) > 0 && bytes.Compare(start, end) > 0) ||
		root == (common.Hash{}) ||
		root == types.EmptyRootHash ||
		limit == 0 {
		log.Debug("invalid leafs request, dropping request", "nodeID", nodeID, "requestID", requestID)
		lrh.stats.IncInvalidLeafsRequest()
		return nil, nil
	}
	if (len(start) != 0 && len(start) != lrh.trieKeyLength) ||
		(len(end) != 0 && len(end) != lrh.trieKeyLength) {
		log.Debug("invalid length for leafs request range, dropping request", "startLen", len(start), "endLen", len(end), "expected", lrh.trieKeyLength)
		lrh.stats.IncInvalidLeafsRequest()
		return nil, nil
	}

	// Open trie
	t, err := trie.New(trie.TrieID(root), lrh.trieDB)
	if err != nil {
		log.Debug("error opening trie when processing request, dropping request", "nodeID", nodeID, "requestID", requestID, "root", root, "err", err)
		lrh.stats.IncMissingRoot()
		return nil, nil
	}

	// Override limit if it is greater than the configured MaxLeavesLimit
	if limit > MaxLeavesLimit {
		limit = MaxLeavesLimit
	}

	// Build response
	keys := make([][]byte, 0, limit)
	vals := make([][]byte, 0, limit)
	var proofVals [][]byte

	rb := &responseBuilder{
		root:        root,
		account:     account,
		start:       start,
		end:         end,
		limit:       limit,
		keys:        &keys,
		vals:        &vals,
		proofVals:   &proofVals,
		t:           t,
		snap:        lrh.getSnapshot(),
		keyLength:   lrh.trieKeyLength,
		stats:       lrh.stats,
		proofDBPool: lrh.proofDBPool,
	}

	err = rb.handleRequest(ctx)

	// Ensure metrics are captured properly on all return paths
	defer func() {
		lrh.stats.UpdateLeafsRequestProcessingTime(time.Since(startTime))
		lrh.stats.UpdateLeafsReturned(uint16(len(keys)))
		lrh.stats.UpdateRangeProofValsReturned(int64(len(proofVals)))
		lrh.stats.UpdateGenerateRangeProofTime(rb.proofTime)
		lrh.stats.UpdateReadLeafsTime(rb.trieReadTime)
	}()

	if err != nil {
		log.Debug("failed to serve leafs request", "nodeID", nodeID, "requestID", requestID, "err", err)
		return nil, nil
	}
	if len(keys) == 0 && ctx.Err() != nil {
		log.Debug("context err set before any leafs were iterated", "nodeID", nodeID, "requestID", requestID, "ctxErr", ctx.Err())
		return nil, nil
	}

	// Marshal response using the appropriate message type
	responseBytes, err := marshalLeafsResponse(lrh.codec, keys, vals, proofVals)
	if err != nil {
		log.Debug("failed to marshal LeafsResponse, dropping request", "nodeID", nodeID, "requestID", requestID, "err", err)
		return nil, nil
	}

	log.Debug("handled leafsRequest", "time", time.Since(startTime), "leafs", len(keys), "proofLen", len(proofVals))
	return responseBytes, nil
}

// getSnapshot returns the snapshot tree if available.
func (lrh *leafsRequestHandler) getSnapshot() *snapshot.Tree {
	if lrh.snapshotProvider == nil {
		return nil
	}
	// Type assert to the concrete snapshot provider types used by coreth and subnet-evm
	type snapGetter interface {
		Snapshots() *snapshot.Tree
	}
	if sg, ok := lrh.snapshotProvider.(snapGetter); ok {
		return sg.Snapshots()
	}
	return nil
}

// responseBuilder builds a leafs response by reading from snapshot and/or trie.
type responseBuilder struct {
	// Request fields
	root    common.Hash
	account common.Hash
	start   []byte
	end     []byte
	limit   uint16

	// Response fields (pointers to allow modification)
	keys      *[][]byte
	vals      *[][]byte
	proofVals *[][]byte

	// Resources
	t           *trie.Trie
	snap        *snapshot.Tree
	keyLength   int
	proofDBPool *proofDBPool

	// Stats
	trieReadTime time.Duration
	proofTime    time.Duration
	stats        LeafsRequestStats
}

// closeAndReturnProof closes the proof database and returns it to the pool for reuse.
func (rb *responseBuilder) closeAndReturnProof(proof *memorydb.Database) {
	if proof != nil {
		_ = proof.Close() // closing memdb does not error
		if rb.proofDBPool.enabled {
			rb.proofDBPool.put(proof)
		}
	}
}

func (rb *responseBuilder) handleRequest(ctx context.Context) error {
	// Read from snapshot if a snapshot.Tree was provided
	if rb.snap != nil {
		if done, err := rb.fillFromSnapshot(ctx); err != nil {
			return err
		} else if done {
			return nil
		}
		// Reset the proof if we will iterate the trie further
		*rb.proofVals = nil
	}

	if len(*rb.keys) < int(rb.limit) {
		// more indicates whether there are more leaves in the trie
		more, err := rb.fillFromTrie(ctx, rb.end)
		if err != nil {
			rb.stats.IncTrieError()
			return err
		}
		if len(rb.start) == 0 && !more {
			// omit proof via early return
			return nil
		}
	}

	// Generate the proof and add it to the response
	proof, err := rb.generateRangeProof(rb.start, *rb.keys)
	if err != nil {
		rb.stats.IncProofError()
		return err
	}
	defer rb.closeAndReturnProof(proof)

	proofVals, err := iterateVals(proof)
	if err != nil {
		rb.stats.IncProofError()
		return err
	}
	*rb.proofVals = proofVals
	return nil
}

// fillFromSnapshot reads data from snapshot and returns true if the response is complete.
func (rb *responseBuilder) fillFromSnapshot(ctx context.Context) (bool, error) {
	snapshotReadStart := time.Now()
	rb.stats.IncSnapshotReadAttempt()

	// Optimistically read leafs from the snapshot with reduced timeout
	snapCtx := ctx
	if deadline, ok := ctx.Deadline(); ok {
		timeTillDeadline := time.Until(deadline)
		bufferedDeadline := time.Now().Add(timeTillDeadline * MaxSnapshotReadTimePercent / 100)

		var cancel context.CancelFunc
		snapCtx, cancel = context.WithDeadline(ctx, bufferedDeadline)
		defer cancel()
	}

	snapKeys, snapVals, err := rb.readLeafsFromSnapshot(snapCtx)
	rb.stats.UpdateSnapshotReadTime(time.Since(snapshotReadStart))
	if err != nil {
		rb.stats.IncSnapshotReadError()
		return false, err
	}

	// Check if the entire range read from the snapshot is valid
	proof, ok, more, err := rb.isRangeValid(snapKeys, snapVals, false)
	if err != nil {
		rb.stats.IncProofError()
		return false, err
	}
	defer rb.closeAndReturnProof(proof)

	if ok {
		*rb.keys, *rb.vals = snapKeys, snapVals
		if len(rb.start) == 0 && !more {
			// omit proof via early return
			rb.stats.IncSnapshotReadSuccess()
			return true, nil
		}
		proofVals, err := iterateVals(proof)
		if err != nil {
			rb.stats.IncProofError()
			return false, err
		}
		*rb.proofVals = proofVals
		rb.stats.IncSnapshotReadSuccess()
		return !more, nil
	}

	// Validate smaller segments with smart early termination
	const maxConsecutiveFailures = 3
	consecutiveFailures := 0
	hasGap := false

	for i := 0; i < len(snapKeys); i += SegmentLen {
		segmentEnd := min(i+SegmentLen, len(snapKeys))
		proof, ok, _, err := rb.isRangeValid(snapKeys[i:segmentEnd], snapVals[i:segmentEnd], hasGap)
		if err != nil {
			rb.stats.IncProofError()
			return false, err
		}
		rb.closeAndReturnProof(proof)

		if !ok {
			rb.stats.IncSnapshotSegmentInvalid()
			consecutiveFailures++
			if consecutiveFailures >= maxConsecutiveFailures {
				// Snapshot is clearly stale - stop validating
				break
			}
			hasGap = true
			continue
		}

		// Segment is valid - reset failure counter
		consecutiveFailures = 0
		rb.stats.IncSnapshotSegmentValid()

		if hasGap {
			// Fill the gap with data from the trie
			_, err := rb.fillFromTrie(ctx, snapKeys[i])
			if err != nil {
				rb.stats.IncTrieError()
				return false, err
			}
			if len(*rb.keys) >= int(rb.limit) || ctx.Err() != nil {
				break
			}
			// Remove the last key since it is snapKeys[i] and will be added back
			*rb.keys = (*rb.keys)[:len(*rb.keys)-1]
			*rb.vals = (*rb.vals)[:len(*rb.vals)-1]
		}
		hasGap = false

		// Shorten segmentEnd to respect limit
		segmentEnd = min(segmentEnd, i+int(rb.limit)-len(*rb.keys))
		*rb.keys = append(*rb.keys, snapKeys[i:segmentEnd]...)
		*rb.vals = append(*rb.vals, snapVals[i:segmentEnd]...)

		if len(*rb.keys) >= int(rb.limit) {
			break
		}
	}
	return false, nil
}

// generateRangeProof returns a range proof for the range specified by [start] and [keys].
func (rb *responseBuilder) generateRangeProof(start []byte, keys [][]byte) (*memorydb.Database, error) {
	proof := rb.proofDBPool.get()
	startTime := time.Now()
	defer func() { rb.proofTime += time.Since(startTime) }()

	// If [start] is empty, populate it with the appropriate length key starting at 0
	if len(start) == 0 {
		start = bytes.Repeat([]byte{0x00}, rb.keyLength)
	}

	if err := rb.t.Prove(start, proof); err != nil {
		rb.proofDBPool.put(proof)
		return nil, err
	}
	if len(keys) > 0 {
		// Set [end] for the range proof to the last key
		end := keys[len(keys)-1]
		if err := rb.t.Prove(end, proof); err != nil {
			rb.proofDBPool.put(proof)
			return nil, err
		}
	}
	return proof, nil
}

// verifyRangeProof verifies the provided range proof with [keys/vals], starting at [start].
func (rb *responseBuilder) verifyRangeProof(keys, vals [][]byte, start []byte, proof *memorydb.Database) (bool, error) {
	startTime := time.Now()
	defer func() { rb.proofTime += time.Since(startTime) }()

	if len(start) == 0 {
		start = bytes.Repeat([]byte{0x00}, rb.keyLength)
	}
	return trie.VerifyRangeProof(rb.root, start, keys, vals, proof)
}

// iterateVals returns the values contained in [db].
func iterateVals(db *memorydb.Database) ([][]byte, error) {
	if db == nil {
		return nil, nil
	}
	it := db.NewIterator(nil, nil)
	defer it.Release()

	vals := make([][]byte, 0, db.Len())
	for it.Next() {
		vals = append(vals, it.Value())
	}

	return vals, it.Error()
}

// isRangeValid generates and verifies a range proof, returning true if keys/vals are part of the trie.
func (rb *responseBuilder) isRangeValid(keys, vals [][]byte, hasGap bool) (*memorydb.Database, bool, bool, error) {
	var startKey []byte
	if hasGap {
		startKey = keys[0]
	} else {
		startKey = rb.nextKey()
	}

	proof, err := rb.generateRangeProof(startKey, keys)
	if err != nil {
		return nil, false, false, err
	}
	more, proofErr := rb.verifyRangeProof(keys, vals, startKey, proof)
	return proof, proofErr == nil, more, nil
}

// nextKey returns the next key that could potentially be part of the response.
func (rb *responseBuilder) nextKey() []byte {
	if len(*rb.keys) == 0 {
		return rb.start
	}
	nextKey := common.CopyBytes((*rb.keys)[len(*rb.keys)-1])
	utils.IncrOne(nextKey)
	return nextKey
}

// fillFromTrie iterates key/values from the trie and appends them to the response.
func (rb *responseBuilder) fillFromTrie(ctx context.Context, end []byte) (bool, error) {
	startTime := time.Now()
	defer func() { rb.trieReadTime += time.Since(startTime) }()

	nodeIt, err := rb.t.NodeIterator(rb.nextKey())
	if err != nil {
		return false, err
	}
	it := trie.NewIterator(nodeIt)
	more := false
	for it.Next() {
		if len(end) > 0 && bytes.Compare(it.Key, end) > 0 {
			more = true
			break
		}

		if len(*rb.keys) >= int(rb.limit) || ctx.Err() != nil {
			more = true
			break
		}

		*rb.keys = append(*rb.keys, it.Key)
		*rb.vals = append(*rb.vals, it.Value)
	}
	return more, it.Err
}

// readLeafsFromSnapshot iterates the storage snapshot of the requested account.
func (rb *responseBuilder) readLeafsFromSnapshot(ctx context.Context) ([][]byte, [][]byte, error) {
	var (
		snapIt    ethdb.Iterator
		startHash = common.BytesToHash(rb.start)
		keys      = make([][]byte, 0, rb.limit)
		vals      = make([][]byte, 0, rb.limit)
	)

	// Get an iterator into the storage or the main account snapshot
	if rb.account == (common.Hash{}) {
		snapIt = rb.snap.DiskAccountIterator(startHash).(ethdb.Iterator)
	} else {
		snapIt = rb.snap.DiskStorageIterator(rb.account, startHash).(ethdb.Iterator)
	}
	defer snapIt.Release()

	for snapIt.Next() {
		if len(rb.end) > 0 && bytes.Compare(snapIt.Key(), rb.end) > 0 {
			break
		}
		if len(keys) >= int(rb.limit) || ctx.Err() != nil {
			break
		}

		keys = append(keys, snapIt.Key())
		vals = append(vals, snapIt.Value())
	}
	return keys, vals, snapIt.Error()
}

// Helper functions to handle different message types (coreth vs subnet-evm)

func extractLeafsRequestFields(req interface{}) (root common.Hash, account common.Hash, start, end []byte, limit uint16) {
	// Use reflection to extract fields from both coreth and subnet-evm message types
	// This allows the same handler to work with both message formats
	type leafsReq interface {
		GetRoot() common.Hash
		GetAccount() common.Hash
		GetStart() []byte
		GetEnd() []byte
		GetLimit() uint16
	}

	if lr, ok := req.(leafsReq); ok {
		return lr.GetRoot(), lr.GetAccount(), lr.GetStart(), lr.GetEnd(), lr.GetLimit()
	}

	// Fallback using reflection for different field access patterns
	// Both coreth and subnet-evm have these fields, just access them directly
	v := reflect.ValueOf(req)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	root = v.FieldByName("Root").Interface().(common.Hash)
	account = v.FieldByName("Account").Interface().(common.Hash)
	start = v.FieldByName("Start").Bytes()
	end = v.FieldByName("End").Bytes()
	limit = v.FieldByName("Limit").Interface().(uint16)

	return root, account, start, end, limit
}

func marshalLeafsResponse(codec codec.Manager, keys, vals, proofVals [][]byte) ([]byte, error) {
	// Create response struct that matches both coreth and subnet-evm message formats
	type leafsResponse struct {
		Keys      [][]byte
		Vals      [][]byte
		ProofVals [][]byte
	}

	response := leafsResponse{
		Keys:      keys,
		Vals:      vals,
		ProofVals: proofVals,
	}

	// Marshal using the codec (version will be determined by the codec manager)
	return codec.Marshal(0, response)
}
