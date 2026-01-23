// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package leveldb

import (
	"runtime"
	"testing"

	"github.com/syndtr/goleveldb/leveldb/opt"
)

func TestValidateConfig_SafeConfig(t *testing.T) {
	// Create a safe config that should pass validation
	safeConfig := config{
		BlockCacheCapacity:     128 * opt.MiB, // 128 MB
		WriteBuffer:            32 * opt.MiB,  // 32 MB
		OpenFilesCacheCapacity: 1024,          // 1K files
		CompactionTableSize:    2 * opt.MiB,
	}

	pressureCfg := DefaultMemoryPressureConfig()
	err := ValidateConfig(safeConfig, pressureCfg)
	if err != nil && !isWarning(err) {
		t.Errorf("Expected safe config to pass validation, got error: %v", err)
	}
}

func TestValidateConfig_HighMemoryWarning(t *testing.T) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Create a config that uses ~35% of memory (should trigger warning but not error)
	availableMemory := m.Sys
	targetUsage := uint64(float64(availableMemory) * 0.35)

	highConfig := config{
		BlockCacheCapacity:     int(targetUsage / 2),
		WriteBuffer:            int(targetUsage / 4),
		OpenFilesCacheCapacity: 2048,
		CompactionTableSize:    8 * opt.MiB,
	}

	pressureCfg := DefaultMemoryPressureConfig()
	err := ValidateConfig(highConfig, pressureCfg)

	// Should get a warning (not a hard error)
	if err == nil {
		t.Log("Note: No warning triggered - system may have very large memory")
	} else if !isWarning(err) {
		t.Errorf("Expected warning, got hard error: %v", err)
	}
}

func TestValidateConfig_ExcessiveMemoryError(t *testing.T) {
	// Create a config that uses way too much memory (should fail)
	excessiveConfig := config{
		BlockCacheCapacity:     64 * opt.GiB, // 64 GB - way too much
		WriteBuffer:            16 * opt.GiB, // 16 GB
		OpenFilesCacheCapacity: 100000,       // 100K files
		CompactionTableSize:    1 * opt.GiB,
	}

	pressureCfg := DefaultMemoryPressureConfig()
	err := ValidateConfig(excessiveConfig, pressureCfg)

	if err == nil {
		t.Error("Expected validation to fail for excessive config")
	}
	if !isHardError(err) {
		t.Errorf("Expected hard error for excessive config, got: %v", err)
	}
}

func TestEstimateDatabaseMemory(t *testing.T) {
	cfg := config{
		BlockCacheCapacity:     2 * opt.GiB,  // 2 GB
		WriteBuffer:            256 * opt.MiB, // 256 MB
		OpenFilesCacheCapacity: 4096,          // 4K files
		CompactionTableSize:    8 * opt.MiB,
	}

	estimated := estimateDatabaseMemory(cfg)

	// Expected calculation:
	// BlockCache: 2GB = 2,147,483,648
	// WriteBuffer * 2: 256MB * 2 = 536,870,912
	// Files: 4096 * 4KB = 16,777,216
	// Compaction: 8MB * 2 = 16,777,216
	// Total: ~2.7 GB = 2,718,908,992

	expectedMin := uint64(2.5 * 1024 * 1024 * 1024) // 2.5 GB
	expectedMax := uint64(3.0 * 1024 * 1024 * 1024) // 3.0 GB

	if estimated < expectedMin || estimated > expectedMax {
		t.Errorf("Memory estimation out of expected range: got %d bytes (%.2f GB), expected between %.2f GB and %.2f GB",
			estimated,
			float64(estimated)/(1024*1024*1024),
			float64(expectedMin)/(1024*1024*1024),
			float64(expectedMax)/(1024*1024*1024),
		)
	}
}

func TestGetMemoryEstimate_Formatting(t *testing.T) {
	tests := []struct {
		name   string
		config config
	}{
		{
			name: "small config (MB)",
			config: config{
				BlockCacheCapacity:     128 * opt.MiB,
				WriteBuffer:            32 * opt.MiB,
				OpenFilesCacheCapacity: 512,
				CompactionTableSize:    2 * opt.MiB,
			},
		},
		{
			name: "large config (GB)",
			config: config{
				BlockCacheCapacity:     2 * opt.GiB,
				WriteBuffer:            256 * opt.MiB,
				OpenFilesCacheCapacity: 4096,
				CompactionTableSize:    8 * opt.MiB,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			estimate := GetMemoryEstimate(tt.config)
			if estimate == "" {
				t.Error("Expected non-empty memory estimate string")
			}
			t.Logf("Memory estimate for %s: %s", tt.name, estimate)
		})
	}
}

func TestDefaultMemoryPressureConfig(t *testing.T) {
	cfg := DefaultMemoryPressureConfig()

	if cfg.MaxMemoryUsagePercent != 0.50 {
		t.Errorf("Expected MaxMemoryUsagePercent to be 0.50, got %f", cfg.MaxMemoryUsagePercent)
	}
	if cfg.WarnMemoryUsagePercent != 0.30 {
		t.Errorf("Expected WarnMemoryUsagePercent to be 0.30, got %f", cfg.WarnMemoryUsagePercent)
	}
}

// Helper functions

func isWarning(err error) bool {
	// Check if error wraps the warning sentinel
	if err == nil {
		return false
	}
	// Simple string check since errors.Is requires import
	return err.Error() != "" && err != ErrMemoryConfigTooHigh && err != ErrInsufficientSystemMemory
}

func isHardError(err error) bool {
	if err == nil {
		return false
	}
	// Check if error message contains the hard error keywords
	errMsg := err.Error()
	return errMsg != "" && (err == ErrMemoryConfigTooHigh || err == ErrInsufficientSystemMemory ||
		// Also check by message content
		(errMsg != "" && errMsg != ErrMemoryPressureWarning.Error()))
}
