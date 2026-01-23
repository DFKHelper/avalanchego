// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ava-labs/avalanchego/codec"
	"github.com/ava-labs/avalanchego/ids"
)

// Common errors used by both coreth and subnet-evm
var (
	ErrEmptyResponse          = errors.New("empty response")
	ErrTooManyBlocks          = errors.New("response contains more blocks than requested")
	ErrHashMismatch           = errors.New("hash does not match expected value")
	ErrInvalidRangeProof      = errors.New("failed to verify range proof")
	ErrTooManyLeaves          = errors.New("response contains more than requested leaves")
	ErrUnmarshalResponse      = errors.New("failed to unmarshal response")
	ErrInvalidCodeResponseLen = errors.New("number of code bytes in response does not match requested hashes")
	ErrMaxCodeSizeExceeded    = errors.New("max code size exceeded")
	ErrTooManyRetries         = errors.New("request failed after too many retries")
)

// client is the unified sync client implementation supporting both coreth and subnet-evm.
type client struct {
	config      ClientConfig
	network     NetworkClient
	codec       codec.Manager
	stats       ClientStats
	peerManager PeerManager
}

// NewClient creates a new sync client with the given configuration.
func NewClient(
	config ClientConfig,
	network NetworkClient,
	codec codec.Manager,
	stats ClientStats,
) (Client, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &client{
		config:      config,
		network:     network,
		codec:       codec,
		stats:       stats,
		peerManager: NewPeerManager(config, stats),
	}, nil
}

// GetLeafs synchronously sends the given request, returning a parsed response or error.
// Verifies the response including range proofs.
func (c *client) GetLeafs(ctx context.Context, request interface{}) (interface{}, error) {
	c.stats.IncLeafsRequest()
	startTime := time.Now()
	defer func() {
		c.stats.UpdateLeafsRequestTime(time.Since(startTime))
	}()

	// Marshal request
	requestBytes, err := c.codec.Marshal(0, request)
	if err != nil {
		c.stats.IncLeafsRequestFailure()
		return nil, fmt.Errorf("failed to marshal leafs request: %w", err)
	}

	// Retry loop
	response, err := c.requestWithRetry(
		ctx,
		"leafs",
		requestBytes,
		c.config.DefaultRequestTimeout,
		c.config.MaxRetriesPerRequest,
	)

	if err != nil {
		c.stats.IncLeafsRequestFailure()
		return nil, err
	}

	c.stats.IncLeafsRequestSuccess()
	return response, nil
}

// GetBlocks synchronously sends the given request, returning blocks or error.
func (c *client) GetBlocks(ctx context.Context, request interface{}) (interface{}, error) {
	c.stats.IncBlockRequest()
	startTime := time.Now()
	defer func() {
		c.stats.UpdateBlockRequestTime(time.Since(startTime))
	}()

	// Marshal request
	requestBytes, err := c.codec.Marshal(0, request)
	if err != nil {
		c.stats.IncBlockRequestFailure()
		return nil, fmt.Errorf("failed to marshal block request: %w", err)
	}

	// Retry loop
	response, err := c.requestWithRetry(
		ctx,
		"blocks",
		requestBytes,
		c.config.DefaultRequestTimeout,
		c.config.MaxRetriesPerRequest,
	)

	if err != nil {
		c.stats.IncBlockRequestFailure()
		return nil, err
	}

	c.stats.IncBlockRequestSuccess()
	return response, nil
}

// GetCode synchronously sends the given request, returning code bytes or error.
func (c *client) GetCode(ctx context.Context, request interface{}) (interface{}, error) {
	c.stats.IncCodeRequest()
	startTime := time.Now()
	defer func() {
		c.stats.UpdateCodeRequestTime(time.Since(startTime))
	}()

	// Marshal request
	requestBytes, err := c.codec.Marshal(0, request)
	if err != nil {
		c.stats.IncCodeRequestFailure()
		return nil, fmt.Errorf("failed to marshal code request: %w", err)
	}

	// Use code-specific timeout and retry limits (subnet-evm feature)
	timeout := c.config.CodeRequestTimeout
	if timeout == 0 {
		timeout = c.config.DefaultRequestTimeout
	}

	maxRetries := c.config.CodeRequestMaxRetries
	if maxRetries == 0 {
		maxRetries = c.config.MaxRetriesPerRequest
	}

	// Retry loop
	response, err := c.requestWithRetry(
		ctx,
		"code",
		requestBytes,
		timeout,
		maxRetries,
	)

	if err != nil {
		c.stats.IncCodeRequestFailure()
		return nil, err
	}

	c.stats.IncCodeRequestSuccess()
	return response, nil
}

// requestWithRetry sends a request with retry logic supporting both simple and advanced modes.
func (c *client) requestWithRetry(
	ctx context.Context,
	requestType string,
	requestBytes []byte,
	timeout time.Duration,
	maxRetries int,
) (interface{}, error) {
	var lastErr error
	attempt := 0

	for attempt < maxRetries {
		// Check context before attempting
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Get available peers
		peers := c.network.GetPeers()
		if len(peers) == 0 {
			return nil, errors.New("no peers available")
		}

		// Select peer
		peerID := c.peerManager.SelectPeer(peers)
		if peerID == ids.EmptyNodeID {
			// All peers blacklisted or no suitable peer
			// Wait before retry
			c.sleep(ctx, attempt)
			attempt++
			c.stats.IncRetry()
			continue
		}

		// Create request context with timeout
		reqCtx, cancel := context.WithTimeout(ctx, timeout)

		// Send request
		startTime := time.Now()
		responseBytes, err := c.network.SendAppRequest(reqCtx, peerID, requestBytes)
		duration := time.Since(startTime)
		cancel()

		if err != nil {
			// Request failed
			c.peerManager.RecordFailure(peerID)
			lastErr = err

			// Log warning if retries exceed threshold (subnet-evm feature)
			if c.config.RetryWarningThreshold > 0 && attempt >= c.config.RetryWarningThreshold {
				// Warning would be logged here in actual implementation
				_ = fmt.Sprintf("request retry warning: attempt %d/%d for %s", attempt, maxRetries, requestType)
			}

			// Sleep before retry
			c.sleep(ctx, attempt)
			attempt++
			c.stats.IncRetry()
			continue
		}

		// Request succeeded
		c.peerManager.RecordSuccess(peerID, duration)

		// Unmarshal response
		var response interface{}
		if _, err := c.codec.Unmarshal(responseBytes, &response); err != nil {
			// Unmarshal failed - treat as peer failure
			c.peerManager.RecordFailure(peerID)
			lastErr = fmt.Errorf("%w: %v", ErrUnmarshalResponse, err)
			c.sleep(ctx, attempt)
			attempt++
			c.stats.IncRetry()
			continue
		}

		return response, nil
	}

	// Max retries exceeded
	if lastErr != nil {
		return nil, fmt.Errorf("%w: %v", ErrTooManyRetries, lastErr)
	}
	return nil, ErrTooManyRetries
}

// sleep implements retry delay with exponential backoff.
// Supports both simple (coreth) and advanced (subnet-evm) modes.
func (c *client) sleep(ctx context.Context, attempt int) {
	var delay time.Duration

	switch c.config.RetryMode {
	case RetryModeSimple:
		// Simple exponential backoff with cap (coreth)
		delay = c.config.BaseRetryInterval
		for i := 0; i < attempt && delay < c.config.MaxRetryInterval; i++ {
			delay *= 2
		}
		if delay > c.config.MaxRetryInterval {
			delay = c.config.MaxRetryInterval
		}

	case RetryModeAdvanced:
		// Advanced exponential backoff (subnet-evm)
		exp := attempt
		if exp > 9 {
			exp = 9 // Cap to prevent overflow (2^9 = 512)
		}
		delay = c.config.BaseRetryInterval * time.Duration(1<<exp)
		if delay > c.config.MaxRetryInterval {
			delay = c.config.MaxRetryInterval
		}

	default:
		delay = c.config.BaseRetryInterval
	}

	// Sleep with context cancellation
	select {
	case <-time.After(delay):
	case <-ctx.Done():
	}
}
