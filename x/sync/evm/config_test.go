// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package evmsync

import (
	"testing"
)

func TestValidateConfig_Valid(t *testing.T) {
	tests := []struct {
		name   string
		config SyncConfig
	}{
		{
			name:   "default blocking config",
			config: DefaultConfig(ModeBlocking),
		},
		{
			name:   "default async config",
			config: DefaultConfig(ModeAsync),
		},
		{
			name: "custom valid config",
			config: SyncConfig{
				Mode:                ModeBlocking,
				WorkerCount:         16,
				RequestSize:         2048,
				BatchSize:           500,
				MaxOutstandingCodes: 2048,
				NumCodeWorkers:      8,
				RetryPolicy: RetryConfig{
					MaxAttempts:     5,
					EnableBackoff:   true,
					InitialInterval: 500,
					MaxInterval:     10000,
					Multiplier:      1.5,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateConfig(tt.config); err != nil {
				t.Errorf("Expected valid config, got error: %v", err)
			}
		})
	}
}

func TestValidateConfig_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		config  SyncConfig
		wantErr bool
	}{
		{
			name: "negative worker count",
			config: SyncConfig{
				Mode:                ModeBlocking,
				WorkerCount:         -1,
				RequestSize:         1024,
				BatchSize:           1000,
				MaxOutstandingCodes: 1024,
				NumCodeWorkers:      4,
			},
			wantErr: true,
		},
		{
			name: "zero request size",
			config: SyncConfig{
				Mode:                ModeBlocking,
				WorkerCount:         8,
				RequestSize:         0,
				BatchSize:           1000,
				MaxOutstandingCodes: 1024,
				NumCodeWorkers:      4,
			},
			wantErr: true,
		},
		{
			name: "zero batch size",
			config: SyncConfig{
				Mode:                ModeBlocking,
				WorkerCount:         8,
				RequestSize:         1024,
				BatchSize:           0,
				MaxOutstandingCodes: 1024,
				NumCodeWorkers:      4,
			},
			wantErr: true,
		},
		{
			name: "invalid retry multiplier",
			config: SyncConfig{
				Mode:                ModeBlocking,
				WorkerCount:         8,
				RequestSize:         1024,
				BatchSize:           1000,
				MaxOutstandingCodes: 1024,
				NumCodeWorkers:      4,
				RetryPolicy: RetryConfig{
					MaxAttempts:     3,
					EnableBackoff:   true,
					InitialInterval: 1000,
					MaxInterval:     5000,
					Multiplier:      0.5, // Invalid: must be > 1.0
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConfig(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestMergeConfigs(t *testing.T) {
	base := DefaultConfig(ModeBlocking)
	override := SyncConfig{
		WorkerCount: 16,
		RequestSize: 2048,
	}

	merged := MergeConfigs(base, override)

	if merged.WorkerCount != 16 {
		t.Errorf("Expected WorkerCount=16, got %d", merged.WorkerCount)
	}
	if merged.RequestSize != 2048 {
		t.Errorf("Expected RequestSize=2048, got %d", merged.RequestSize)
	}
	// Base values should be preserved for non-overridden fields
	if merged.BatchSize != base.BatchSize {
		t.Errorf("Expected BatchSize=%d (from base), got %d", base.BatchSize, merged.BatchSize)
	}
}

func TestSyncMode_String(t *testing.T) {
	tests := []struct {
		mode SyncMode
		want string
	}{
		{ModeBlocking, "blocking"},
		{ModeAsync, "async"},
		{SyncMode(99), "unknown(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.mode.String(); got != tt.want {
				t.Errorf("SyncMode.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSyncConfig_String(t *testing.T) {
	cfg := DefaultConfig(ModeBlocking)
	str := cfg.String()

	if str == "" {
		t.Error("Expected non-empty string representation")
	}

	// Should contain key config values
	expectedSubstrings := []string{
		"blocking",
		"workers=",
		"requestSize=",
		"batchSize=",
	}

	for _, substr := range expectedSubstrings {
		if !contains(str, substr) {
			t.Errorf("Expected string to contain %q, got: %s", substr, str)
		}
	}
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[:len(substr)] == substr ||
		   (len(s) > len(substr) && contains(s[1:], substr))
}
