// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package handlers

import (
	"context"
	"time"

	"github.com/ava-labs/libevm/common"
	"github.com/ava-labs/libevm/core/rawdb"
	"github.com/ava-labs/libevm/ethdb"
	"github.com/ava-labs/libevm/log"

	"github.com/ava-labs/avalanchego/codec"
	"github.com/ava-labs/avalanchego/ids"
)

// CodeRequestStats defines all statistics tracked by the code request handler.
type CodeRequestStats interface {
	IncCodeRequest()
	IncTooManyHashesRequested()
	IncDuplicateHashesRequested()
	IncMissingCodeHash()
	UpdateCodeReadTime(duration time.Duration)
	UpdateCodeBytesReturned(bytes uint32)
}

// CodeRequestHandler is a peer.RequestHandler for CodeRequest messages,
// serving requested contract code bytes. This unified implementation supports
// both coreth and subnet-evm.
type CodeRequestHandler struct {
	codeReader ethdb.KeyValueReader
	codec      codec.Manager
	stats      CodeRequestStats
}

// NewCodeRequestHandler creates a new code request handler.
func NewCodeRequestHandler(codeReader ethdb.KeyValueReader, codec codec.Manager, stats CodeRequestStats) *CodeRequestHandler {
	return &CodeRequestHandler{
		codeReader: codeReader,
		codec:      codec,
		stats:      stats,
	}
}

// OnCodeRequest handles requests to retrieve contract code by hash.
// Never returns error.
// Returns nothing if code hash is not found.
// Expects returned errors to be treated as FATAL.
// Assumes ctx is active.
func (c *CodeRequestHandler) OnCodeRequest(_ context.Context, nodeID ids.NodeID, requestID uint32, codeRequest interface{}) ([]byte, error) {
	startTime := time.Now()
	c.stats.IncCodeRequest()

	// Always report code read time metric
	defer func() {
		c.stats.UpdateCodeReadTime(time.Since(startTime))
	}()

	// Extract fields from request (handles both coreth and subnet-evm message types)
	hashes, maxHashes := extractCodeRequestFields(codeRequest)

	// Validate request
	if len(hashes) > maxHashes {
		c.stats.IncTooManyHashesRequested()
		log.Debug("too many hashes requested, dropping request", "nodeID", nodeID, "requestID", requestID, "numHashes", len(hashes))
		return nil, nil
	}
	if !isUnique(hashes) {
		c.stats.IncDuplicateHashesRequested()
		log.Debug("duplicate code hashes requested, dropping request", "nodeID", nodeID, "requestID", requestID)
		return nil, nil
	}

	// Read code bytes for each hash
	codeBytes := make([][]byte, len(hashes))
	totalBytes := 0
	for i, hash := range hashes {
		codeBytes[i] = rawdb.ReadCode(c.codeReader, hash)
		if len(codeBytes[i]) == 0 {
			c.stats.IncMissingCodeHash()
			log.Debug("requested code not found, dropping request", "nodeID", nodeID, "requestID", requestID, "hash", hash)
			return nil, nil
		}
		totalBytes += len(codeBytes[i])
	}

	// Marshal response
	responseBytes, err := marshalCodeResponse(c.codec, codeBytes)
	if err != nil {
		log.Error("could not marshal CodeResponse, dropping request", "nodeID", nodeID, "requestID", requestID, "err", err)
		return nil, nil
	}

	c.stats.UpdateCodeBytesReturned(uint32(totalBytes))
	return responseBytes, nil
}

// isUnique checks if all hashes in the slice are unique.
func isUnique(hashes []common.Hash) bool {
	seen := make(map[common.Hash]struct{}, len(hashes))
	for _, hash := range hashes {
		if _, found := seen[hash]; found {
			return false
		}
		seen[hash] = struct{}{}
	}
	return true
}

// extractCodeRequestFields extracts fields from both coreth and subnet-evm message types.
func extractCodeRequestFields(req interface{}) (hashes []common.Hash, maxHashes int) {
	// Use type assertion to handle both message formats
	type codeRequest interface {
		GetHashes() []common.Hash
	}

	if cr, ok := req.(codeRequest); ok {
		return cr.GetHashes(), getMaxCodeHashesPerRequest()
	}

	// Fallback: direct field access
	type directAccess struct {
		Hashes []common.Hash
	}

	if cr, ok := req.(*directAccess); ok {
		return cr.Hashes, getMaxCodeHashesPerRequest()
	}

	// Should not reach here in normal operation
	log.Warn("unable to extract code request fields", "type", req)
	return nil, 0
}

// marshalCodeResponse marshals the code response using the appropriate message format.
func marshalCodeResponse(codec codec.Manager, codeBytes [][]byte) ([]byte, error) {
	// Create response struct that matches both coreth and subnet-evm message formats
	type codeResponse struct {
		Data [][]byte
	}

	response := codeResponse{
		Data: codeBytes,
	}

	// Marshal using the codec (version will be determined by the codec manager)
	return codec.Marshal(0, response)
}

// getMaxCodeHashesPerRequest returns the maximum number of code hashes allowed per request.
// This is defined in both coreth and subnet-evm message packages as MaxCodeHashesPerRequest.
const maxCodeHashesPerRequest = 1024

func getMaxCodeHashesPerRequest() int {
	return maxCodeHashesPerRequest
}
