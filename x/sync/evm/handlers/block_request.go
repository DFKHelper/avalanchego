// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package handlers

import (
	"bytes"
	"context"
	"time"

	"github.com/ava-labs/libevm/common"
	"github.com/ava-labs/libevm/log"

	"github.com/ava-labs/avalanchego/codec"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/units"
)

const (
	// ParentLimit specifies how many parents to retrieve and send given a starting hash.
	// This value overrides any specified limit in blockRequest.Parents if it is greater than this value.
	ParentLimit = uint16(64)

	// TargetMessageByteSize targets total block bytes slightly under original network codec max size of 1MB.
	TargetMessageByteSize = units.MiB - units.KiB
)

// BlockRequestStats defines all statistics tracked by the block request handler.
type BlockRequestStats interface {
	IncBlockRequest()
	IncMissingBlockHash()
	UpdateBlockRequestProcessingTime(duration time.Duration)
	UpdateBlocksReturned(count uint16)
}

// Block represents an EVM block with RLP encoding capability.
type Block interface {
	Hash() common.Hash
	ParentHash() common.Hash
	NumberU64() uint64
	EncodeRLP(w interface{}) error
}

// BlockProvider provides access to blocks by hash and height.
type BlockProvider interface {
	GetBlock(hash common.Hash, height uint64) Block
}

// BlockRequestHandler is a peer.RequestHandler for BlockRequest messages,
// serving requested blocks starting at a specified hash. This unified
// implementation supports both coreth and subnet-evm.
type BlockRequestHandler struct {
	stats         BlockRequestStats
	blockProvider BlockProvider
	codec         codec.Manager
}

// NewBlockRequestHandler creates a new block request handler.
func NewBlockRequestHandler(blockProvider BlockProvider, codec codec.Manager, handlerStats BlockRequestStats) *BlockRequestHandler {
	return &BlockRequestHandler{
		blockProvider: blockProvider,
		codec:         codec,
		stats:         handlerStats,
	}
}

// OnBlockRequest handles incoming BlockRequest, returning blocks as requested.
// Never returns error.
// Expects returned errors to be treated as FATAL.
// Returns empty response or subset of requested blocks if ctx expires during fetch.
// Assumes ctx is active.
func (b *BlockRequestHandler) OnBlockRequest(ctx context.Context, nodeID ids.NodeID, requestID uint32, blockRequest interface{}) ([]byte, error) {
	startTime := time.Now()
	b.stats.IncBlockRequest()

	// Extract fields from request (handles both coreth and subnet-evm message types)
	hash, height, parents := extractBlockRequestFields(blockRequest)

	// Override given Parents limit if it is greater than ParentLimit
	if parents > ParentLimit {
		parents = ParentLimit
	}

	blocks := make([][]byte, 0, parents)
	totalBytes := 0

	// Ensure metrics are captured properly on all return paths
	defer func() {
		b.stats.UpdateBlockRequestProcessingTime(time.Since(startTime))
		b.stats.UpdateBlocksReturned(uint16(len(blocks)))
	}()

	// Fetch blocks following the parent chain
	for i := 0; i < int(parents); i++ {
		// Return whatever we have until ctx errors, limit is exceeded, or we reach the genesis block
		if ctx.Err() != nil {
			break
		}

		if (hash == common.Hash{}) {
			break
		}

		block := b.blockProvider.GetBlock(hash, height)
		if block == nil {
			b.stats.IncMissingBlockHash()
			break
		}

		// Encode block to RLP
		buf := new(bytes.Buffer)
		if err := block.EncodeRLP(buf); err != nil {
			log.Error("failed to RLP encode block", "hash", block.Hash(), "height", block.NumberU64(), "err", err)
			return nil, nil
		}

		// Check if adding this block would exceed the target message size
		if buf.Len()+totalBytes > TargetMessageByteSize && len(blocks) > 0 {
			log.Debug("Skipping block due to max total bytes size", "totalBlockDataSize", totalBytes, "blockSize", buf.Len(), "maxTotalBytesSize", TargetMessageByteSize)
			break
		}

		blocks = append(blocks, buf.Bytes())
		totalBytes += buf.Len()
		hash = block.ParentHash()
		height--
	}

	if len(blocks) == 0 {
		// Drop this request
		log.Debug("no requested blocks found, dropping request", "nodeID", nodeID, "requestID", requestID)
		return nil, nil
	}

	// Marshal response
	responseBytes, err := marshalBlockResponse(b.codec, blocks)
	if err != nil {
		log.Error("failed to marshal BlockResponse, dropping request", "nodeID", nodeID, "requestID", requestID, "blocksLen", len(blocks), "err", err)
		return nil, nil
	}

	return responseBytes, nil
}

// extractBlockRequestFields extracts fields from both coreth and subnet-evm message types.
func extractBlockRequestFields(req interface{}) (hash common.Hash, height uint64, parents uint16) {
	// Use type assertion to handle both message formats
	type blockRequest interface {
		GetHash() common.Hash
		GetHeight() uint64
		GetParents() uint16
	}

	if br, ok := req.(blockRequest); ok {
		return br.GetHash(), br.GetHeight(), br.GetParents()
	}

	// Fallback: direct field access
	type directAccess struct {
		Hash    common.Hash
		Height  uint64
		Parents uint16
	}

	if br, ok := req.(*directAccess); ok {
		return br.Hash, br.Height, br.Parents
	}

	// Should not reach here in normal operation
	log.Warn("unable to extract block request fields", "type", req)
	return common.Hash{}, 0, 0
}

// marshalBlockResponse marshals the block response using the appropriate message format.
func marshalBlockResponse(codec codec.Manager, blocks [][]byte) ([]byte, error) {
	// Create response struct that matches both coreth and subnet-evm message formats
	type blockResponse struct {
		Blocks [][]byte
	}

	response := blockResponse{
		Blocks: blocks,
	}

	// Marshal using the codec (version will be determined by the codec manager)
	return codec.Marshal(0, response)
}
