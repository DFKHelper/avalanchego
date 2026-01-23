// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package evmsync

import "fmt"

// ValidateConfig checks if the provided sync configuration is valid.
// Returns an error if any required fields are missing or invalid.
func ValidateConfig(cfg SyncConfig) error {
	if cfg.WorkerCount < 0 {
		return fmt.Errorf("worker count must be non-negative, got %d", cfg.WorkerCount)
	}

	if cfg.RequestSize == 0 {
		return fmt.Errorf("request size must be positive")
	}

	if cfg.BatchSize <= 0 {
		return fmt.Errorf("batch size must be positive, got %d", cfg.BatchSize)
	}

	if cfg.MaxOutstandingCodes <= 0 {
		return fmt.Errorf("max outstanding codes must be positive, got %d", cfg.MaxOutstandingCodes)
	}

	if cfg.NumCodeWorkers <= 0 {
		return fmt.Errorf("number of code workers must be positive, got %d", cfg.NumCodeWorkers)
	}

	// Validate mode-specific settings
	if cfg.Mode == ModeAsync {
		if cfg.EnableRestart && cfg.MaxRestartAttempts < 0 {
			return fmt.Errorf("max restart attempts must be non-negative when restart is enabled, got %d", cfg.MaxRestartAttempts)
		}
	}

	// Validate retry policy
	if err := validateRetryConfig(cfg.RetryPolicy); err != nil {
		return fmt.Errorf("invalid retry policy: %w", err)
	}

	return nil
}

// validateRetryConfig checks if the retry configuration is valid.
func validateRetryConfig(cfg RetryConfig) error {
	if cfg.MaxAttempts < 0 {
		return fmt.Errorf("max attempts must be non-negative, got %d", cfg.MaxAttempts)
	}

	if cfg.EnableBackoff {
		if cfg.InitialInterval <= 0 {
			return fmt.Errorf("initial interval must be positive when backoff is enabled, got %d", cfg.InitialInterval)
		}
		if cfg.MaxInterval <= 0 {
			return fmt.Errorf("max interval must be positive when backoff is enabled, got %d", cfg.MaxInterval)
		}
		if cfg.MaxInterval < cfg.InitialInterval {
			return fmt.Errorf("max interval (%d) must be >= initial interval (%d)", cfg.MaxInterval, cfg.InitialInterval)
		}
		if cfg.Multiplier <= 1.0 {
			return fmt.Errorf("multiplier must be > 1.0 when backoff is enabled, got %f", cfg.Multiplier)
		}
	}

	return nil
}

// MergeConfigs merges a partial config with defaults.
// Fields in partial that are non-zero will override defaults.
func MergeConfigs(base, override SyncConfig) SyncConfig {
	result := base

	// Override mode if explicitly set
	if override.Mode != 0 {
		result.Mode = override.Mode
	}

	// Override worker settings if set
	if override.WorkerCount > 0 {
		result.WorkerCount = override.WorkerCount
	}
	if override.RequestSize > 0 {
		result.RequestSize = override.RequestSize
	}
	if override.BatchSize > 0 {
		result.BatchSize = override.BatchSize
	}
	if override.MaxOutstandingCodes > 0 {
		result.MaxOutstandingCodes = override.MaxOutstandingCodes
	}
	if override.NumCodeWorkers > 0 {
		result.NumCodeWorkers = override.NumCodeWorkers
	}

	// Override feature flags (these can be explicitly disabled)
	result.EnableStuckDetector = override.EnableStuckDetector
	result.EnableRestart = override.EnableRestart

	if override.MaxRestartAttempts > 0 {
		result.MaxRestartAttempts = override.MaxRestartAttempts
	}

	// Override retry policy if any field is set
	if override.RetryPolicy.MaxAttempts > 0 {
		result.RetryPolicy.MaxAttempts = override.RetryPolicy.MaxAttempts
	}
	if override.RetryPolicy.InitialInterval > 0 {
		result.RetryPolicy.InitialInterval = override.RetryPolicy.InitialInterval
	}
	if override.RetryPolicy.MaxInterval > 0 {
		result.RetryPolicy.MaxInterval = override.RetryPolicy.MaxInterval
	}
	if override.RetryPolicy.Multiplier > 0 {
		result.RetryPolicy.Multiplier = override.RetryPolicy.Multiplier
	}

	return result
}

// String returns a human-readable representation of the sync mode.
func (m SyncMode) String() string {
	switch m {
	case ModeBlocking:
		return "blocking"
	case ModeAsync:
		return "async"
	default:
		return fmt.Sprintf("unknown(%d)", m)
	}
}

// String returns a human-readable representation of the sync configuration.
func (c SyncConfig) String() string {
	return fmt.Sprintf(
		"SyncConfig{mode=%s, workers=%d, requestSize=%d, batchSize=%d, maxCodes=%d, codeWorkers=%d, stuckDetector=%v, restart=%v, maxRestarts=%d}",
		c.Mode,
		c.WorkerCount,
		c.RequestSize,
		c.BatchSize,
		c.MaxOutstandingCodes,
		c.NumCodeWorkers,
		c.EnableStuckDetector,
		c.EnableRestart,
		c.MaxRestartAttempts,
	)
}
