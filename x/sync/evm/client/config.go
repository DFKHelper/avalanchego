// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package client

import (
	"time"
)

// RetryMode defines the retry strategy for failed requests.
type RetryMode int

const (
	// RetryModeSimple uses basic exponential backoff with a cap.
	// Used by coreth for straightforward retry logic.
	RetryModeSimple RetryMode = iota

	// RetryModeAdvanced uses sophisticated exponential backoff with attempt tracking.
	// Used by subnet-evm for more complex retry scenarios.
	RetryModeAdvanced
)

// PeerMode defines the peer selection and management strategy.
type PeerMode int

const (
	// PeerModeScoring uses peer scoring based on success rate and response time.
	// Prefers better-performing peers but doesn't blacklist.
	// Used by coreth.
	PeerModeScoring PeerMode = iota

	// PeerModeBlacklisting temporarily blacklists peers after consecutive failures.
	// Used by subnet-evm for more aggressive failure handling.
	PeerModeBlacklisting
)

// ClientConfig configures the sync client behavior.
// Supports both coreth (simple) and subnet-evm (advanced) patterns.
type ClientConfig struct {
	// Retry configuration
	RetryMode            RetryMode
	BaseRetryInterval    time.Duration
	MaxRetryInterval     time.Duration
	MaxRetriesPerRequest int
	RetryWarningThreshold int // Log warning when retries exceed this

	// Peer management
	PeerMode                  PeerMode
	PeerBlacklistDuration     time.Duration
	MaxConsecutiveFailures    int
	StickyPeerDuration        time.Duration // How long to prefer the same peer
	MinRequestsForScoring     int           // Minimum requests before using peer scoring
	ScoringDecayFactor        float64       // Exponential moving average weight

	// Timeouts
	DefaultRequestTimeout time.Duration
	CodeRequestTimeout    time.Duration // Longer timeout for code requests
	CodeRequestMaxRetries int           // Separate retry limit for code

	// Code request blacklisting (subnet-evm specific)
	CodeRequestBlacklistDuration time.Duration
}

// DefaultCorethConfig returns the default configuration for coreth.
// Uses simple retry strategy and peer scoring.
func DefaultCorethConfig() ClientConfig {
	return ClientConfig{
		// Retry: simple exponential backoff
		RetryMode:            RetryModeSimple,
		BaseRetryInterval:    10 * time.Millisecond,
		MaxRetryInterval:     1 * time.Second,
		MaxRetriesPerRequest: 100, // Effectively unlimited for coreth

		// Peer management: scoring
		PeerMode:               PeerModeScoring,
		StickyPeerDuration:     30 * time.Second,
		MinRequestsForScoring:  5,
		ScoringDecayFactor:     0.9,

		// Timeouts: uniform
		DefaultRequestTimeout: 30 * time.Second,
		CodeRequestTimeout:    30 * time.Second,
	}
}

// DefaultSubnetEVMConfig returns the default configuration for subnet-evm.
// Uses advanced retry strategy and peer blacklisting.
func DefaultSubnetEVMConfig() ClientConfig {
	return ClientConfig{
		// Retry: advanced exponential backoff with limits
		RetryMode:             RetryModeAdvanced,
		BaseRetryInterval:     10 * time.Millisecond,
		MaxRetryInterval:      5 * time.Second,
		MaxRetriesPerRequest:  50,
		RetryWarningThreshold: 20,

		// Peer management: blacklisting
		PeerMode:                     PeerModeBlacklisting,
		PeerBlacklistDuration:        2 * time.Minute,
		MaxConsecutiveFailures:       3,
		CodeRequestBlacklistDuration: 5 * time.Minute,

		// Timeouts: type-specific
		DefaultRequestTimeout: 30 * time.Second,
		CodeRequestTimeout:    45 * time.Second, // Longer for large code
		CodeRequestMaxRetries: 10,               // Fewer retries for code
	}
}

// Validate checks if the configuration is valid.
func (c *ClientConfig) Validate() error {
	if c.BaseRetryInterval <= 0 {
		return ErrInvalidRetryInterval
	}
	if c.MaxRetryInterval < c.BaseRetryInterval {
		return ErrInvalidRetryInterval
	}
	if c.MaxRetriesPerRequest <= 0 {
		return ErrInvalidMaxRetries
	}
	if c.DefaultRequestTimeout <= 0 {
		return ErrInvalidTimeout
	}
	if c.PeerMode == PeerModeScoring {
		if c.MinRequestsForScoring <= 0 {
			return ErrInvalidScoringConfig
		}
		if c.ScoringDecayFactor <= 0 || c.ScoringDecayFactor >= 1 {
			return ErrInvalidScoringConfig
		}
	}
	if c.PeerMode == PeerModeBlacklisting {
		if c.MaxConsecutiveFailures <= 0 {
			return ErrInvalidBlacklistConfig
		}
		if c.PeerBlacklistDuration <= 0 {
			return ErrInvalidBlacklistConfig
		}
	}
	return nil
}

// Common errors
var (
	ErrInvalidRetryInterval   = &ClientError{"invalid retry interval configuration"}
	ErrInvalidMaxRetries      = &ClientError{"max retries must be positive"}
	ErrInvalidTimeout         = &ClientError{"timeout must be positive"}
	ErrInvalidScoringConfig   = &ClientError{"invalid peer scoring configuration"}
	ErrInvalidBlacklistConfig = &ClientError{"invalid peer blacklist configuration"}
)

// ClientError represents a client configuration or operation error.
type ClientError struct {
	message string
}

func (e *ClientError) Error() string {
	return e.message
}
