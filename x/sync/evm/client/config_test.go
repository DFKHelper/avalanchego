// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package client

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDefaultCorethConfig(t *testing.T) {
	config := DefaultCorethConfig()

	require.Equal(t, RetryModeSimple, config.RetryMode)
	require.Equal(t, PeerModeScoring, config.PeerMode)
	require.Equal(t, 10*time.Millisecond, config.BaseRetryInterval)
	require.Equal(t, 1*time.Second, config.MaxRetryInterval)
	require.Equal(t, 30*time.Second, config.StickyPeerDuration)
	require.Equal(t, 5, config.MinRequestsForScoring)
	require.Equal(t, 0.9, config.ScoringDecayFactor)

	// Validate
	require.NoError(t, config.Validate())
}

func TestDefaultSubnetEVMConfig(t *testing.T) {
	config := DefaultSubnetEVMConfig()

	require.Equal(t, RetryModeAdvanced, config.RetryMode)
	require.Equal(t, PeerModeBlacklisting, config.PeerMode)
	require.Equal(t, 10*time.Millisecond, config.BaseRetryInterval)
	require.Equal(t, 5*time.Second, config.MaxRetryInterval)
	require.Equal(t, 50, config.MaxRetriesPerRequest)
	require.Equal(t, 2*time.Minute, config.PeerBlacklistDuration)
	require.Equal(t, 3, config.MaxConsecutiveFailures)
	require.Equal(t, 45*time.Second, config.CodeRequestTimeout)

	// Validate
	require.NoError(t, config.Validate())
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name        string
		config      ClientConfig
		expectError error
	}{
		{
			name: "valid coreth config",
			config: ClientConfig{
				RetryMode:              RetryModeSimple,
				BaseRetryInterval:      10 * time.Millisecond,
				MaxRetryInterval:       1 * time.Second,
				MaxRetriesPerRequest:   100,
				PeerMode:               PeerModeScoring,
				MinRequestsForScoring:  5,
				ScoringDecayFactor:     0.9,
				DefaultRequestTimeout:  30 * time.Second,
			},
			expectError: nil,
		},
		{
			name: "invalid retry interval",
			config: ClientConfig{
				RetryMode:             RetryModeSimple,
				BaseRetryInterval:     0,
				MaxRetryInterval:      1 * time.Second,
				MaxRetriesPerRequest:  100,
				PeerMode:              PeerModeScoring,
				MinRequestsForScoring: 5,
				ScoringDecayFactor:    0.9,
				DefaultRequestTimeout: 30 * time.Second,
			},
			expectError: ErrInvalidRetryInterval,
		},
		{
			name: "max retry less than base",
			config: ClientConfig{
				RetryMode:             RetryModeSimple,
				BaseRetryInterval:     1 * time.Second,
				MaxRetryInterval:      10 * time.Millisecond,
				MaxRetriesPerRequest:  100,
				PeerMode:              PeerModeScoring,
				MinRequestsForScoring: 5,
				ScoringDecayFactor:    0.9,
				DefaultRequestTimeout: 30 * time.Second,
			},
			expectError: ErrInvalidRetryInterval,
		},
		{
			name: "invalid max retries",
			config: ClientConfig{
				RetryMode:             RetryModeSimple,
				BaseRetryInterval:     10 * time.Millisecond,
				MaxRetryInterval:      1 * time.Second,
				MaxRetriesPerRequest:  0,
				PeerMode:              PeerModeScoring,
				MinRequestsForScoring: 5,
				ScoringDecayFactor:    0.9,
				DefaultRequestTimeout: 30 * time.Second,
			},
			expectError: ErrInvalidMaxRetries,
		},
		{
			name: "invalid timeout",
			config: ClientConfig{
				RetryMode:             RetryModeSimple,
				BaseRetryInterval:     10 * time.Millisecond,
				MaxRetryInterval:      1 * time.Second,
				MaxRetriesPerRequest:  100,
				PeerMode:              PeerModeScoring,
				MinRequestsForScoring: 5,
				ScoringDecayFactor:    0.9,
				DefaultRequestTimeout: 0,
			},
			expectError: ErrInvalidTimeout,
		},
		{
			name: "invalid scoring config - min requests",
			config: ClientConfig{
				RetryMode:             RetryModeSimple,
				BaseRetryInterval:     10 * time.Millisecond,
				MaxRetryInterval:      1 * time.Second,
				MaxRetriesPerRequest:  100,
				PeerMode:              PeerModeScoring,
				MinRequestsForScoring: 0,
				ScoringDecayFactor:    0.9,
				DefaultRequestTimeout: 30 * time.Second,
			},
			expectError: ErrInvalidScoringConfig,
		},
		{
			name: "invalid scoring config - decay factor",
			config: ClientConfig{
				RetryMode:             RetryModeSimple,
				BaseRetryInterval:     10 * time.Millisecond,
				MaxRetryInterval:      1 * time.Second,
				MaxRetriesPerRequest:  100,
				PeerMode:              PeerModeScoring,
				MinRequestsForScoring: 5,
				ScoringDecayFactor:    1.5,
				DefaultRequestTimeout: 30 * time.Second,
			},
			expectError: ErrInvalidScoringConfig,
		},
		{
			name: "invalid blacklist config - consecutive failures",
			config: ClientConfig{
				RetryMode:              RetryModeAdvanced,
				BaseRetryInterval:      10 * time.Millisecond,
				MaxRetryInterval:       5 * time.Second,
				MaxRetriesPerRequest:   50,
				PeerMode:               PeerModeBlacklisting,
				MaxConsecutiveFailures: 0,
				PeerBlacklistDuration:  2 * time.Minute,
				DefaultRequestTimeout:  30 * time.Second,
			},
			expectError: ErrInvalidBlacklistConfig,
		},
		{
			name: "invalid blacklist config - duration",
			config: ClientConfig{
				RetryMode:              RetryModeAdvanced,
				BaseRetryInterval:      10 * time.Millisecond,
				MaxRetryInterval:       5 * time.Second,
				MaxRetriesPerRequest:   50,
				PeerMode:               PeerModeBlacklisting,
				MaxConsecutiveFailures: 3,
				PeerBlacklistDuration:  0,
				DefaultRequestTimeout:  30 * time.Second,
			},
			expectError: ErrInvalidBlacklistConfig,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectError != nil {
				require.ErrorIs(t, err, tt.expectError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
