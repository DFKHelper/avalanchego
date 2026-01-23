// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package handlers

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHandlerVersion_String(t *testing.T) {
	tests := []struct {
		version HandlerVersion
		want    string
	}{
		{VersionCoreth, "coreth"},
		{VersionSubnetEVM, "subnet-evm"},
		{HandlerVersion(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.version.String()
			require.Equal(t, tt.want, got)
		})
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     HandlerConfig
		wantErr error
	}{
		{
			name: "nil trie db",
			cfg: HandlerConfig{
				TrieDB:        nil,
				Codec:         "mock-codec",
				TrieKeyLength: 32,
			},
			wantErr: ErrNilTrieDB,
		},
		{
			name: "nil codec",
			cfg: HandlerConfig{
				TrieDB:        "mock-triedb",
				Codec:         nil,
				TrieKeyLength: 32,
			},
			wantErr: ErrNilCodec,
		},
		{
			name: "invalid trie key length",
			cfg: HandlerConfig{
				TrieDB:        "mock-triedb",
				Codec:         "mock-codec",
				TrieKeyLength: 0,
			},
			wantErr: ErrInvalidTrieKeyLength,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConfig(tt.cfg)
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestNoOpStatsRecorder(t *testing.T) {
	// Verify NoOpStatsRecorder doesn't panic when called
	stats := &NoOpStatsRecorder{}

	require.NotPanics(t, func() {
		stats.IncLeafsRequest()
		stats.IncInvalidLeafsRequest()
		stats.IncMissingRoot()
		stats.UpdateLeafsRequestProcessingTime(100)
		stats.UpdateLeafsReturned(10)
		stats.UpdateRangeProofValsReturned(5)
		stats.UpdateGenerateRangeProofTime(50)
		stats.UpdateReadLeafsTime(75)
	})
}
