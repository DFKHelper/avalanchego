// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package firewood

import (
	"encoding/binary"
	"encoding/hex"
	"time"

	"github.com/ava-labs/avalanchego/utils/logging"
	"go.uber.org/zap"
)

const (
	// ExpiryEntry = 8-byte timestamp + 32-byte validation ID
	expectedExpiryEntryLength = 40

	// Reasonable timestamp bounds for validation
	// 2020-01-01 00:00:00 UTC
	minReasonableTimestamp = 1577836800
	// 2100-01-01 00:00:00 UTC
	maxReasonableTimestamp = 4102444800
)

// transformIteratorKey extracts the actual 40-byte ExpiryEntry from Firewood's iterator key.
//
// Problem: Firewood's iterator returns keys with internal metadata (97, 124, 129 bytes)
// instead of the expected 40-byte ExpiryEntry (8-byte timestamp + 32-byte ID).
//
// Solution: Extract the 40-byte payload from the wrapped key structure.
//
// Strategy:
//  1. If key is already 40 bytes → return as-is
//  2. If key is longer → try extracting last 40 bytes (common wrapping pattern)
//  3. Validate extracted data looks like a valid ExpiryEntry
//  4. If validation fails, try other offsets or return original
func transformIteratorKey(rawKey []byte, log logging.Logger) []byte {
	keyLen := len(rawKey)

	// Fast path: already correct size
	if keyLen == expectedExpiryEntryLength {
		if log != nil {
			log.Debug("Iterator key already correct size",
				zap.Int("keyLen", keyLen),
			)
		}
		return rawKey
	}

	// If shorter than expected, can't extract - return as-is
	if keyLen < expectedExpiryEntryLength {
		if log != nil {
			log.Warn("Iterator key shorter than expected",
				zap.Int("keyLen", keyLen),
				zap.Int("expected", expectedExpiryEntryLength),
				zap.String("keyHex", hex.EncodeToString(rawKey)),
			)
		}
		return rawKey
	}

	// Key is longer than expected - try to extract the 40-byte payload
	if log != nil {
		log.Debug("Iterator key longer than expected - attempting extraction",
			zap.Int("keyLen", keyLen),
			zap.Int("expected", expectedExpiryEntryLength),
			zap.Int("extraBytes", keyLen-expectedExpiryEntryLength),
		)
	}

	// Strategy 1: Try extracting last 40 bytes (common pattern for metadata prefix)
	candidate := rawKey[keyLen-expectedExpiryEntryLength:]
	if looksLikeExpiryEntry(candidate, log) {
		if log != nil {
			log.Debug("Successfully extracted ExpiryEntry from last 40 bytes",
				zap.Int("originalLen", keyLen),
				zap.String("extractedHex", hex.EncodeToString(candidate)),
			)
		}
		return candidate
	}

	// Strategy 2: Try extracting first 40 bytes (alternative wrapping pattern)
	candidate = rawKey[:expectedExpiryEntryLength]
	if looksLikeExpiryEntry(candidate, log) {
		if log != nil {
			log.Debug("Successfully extracted ExpiryEntry from first 40 bytes",
				zap.Int("originalLen", keyLen),
				zap.String("extractedHex", hex.EncodeToString(candidate)),
			)
		}
		return candidate
	}

	// Strategy 3: Try sliding window through the key to find valid ExpiryEntry
	// This handles cases where the payload might be in the middle
	for offset := 0; offset <= keyLen-expectedExpiryEntryLength; offset++ {
		candidate = rawKey[offset : offset+expectedExpiryEntryLength]
		if looksLikeExpiryEntry(candidate, log) {
			if log != nil {
				log.Debug("Successfully extracted ExpiryEntry via sliding window",
					zap.Int("originalLen", keyLen),
					zap.Int("offset", offset),
					zap.String("extractedHex", hex.EncodeToString(candidate)),
				)
			}
			return candidate
		}
	}

	// No valid ExpiryEntry found - return original key
	// Caller will handle the error (likely "invalid hash length")
	if log != nil {
		log.Warn("Could not extract valid ExpiryEntry from iterator key",
			zap.Int("keyLen", keyLen),
			zap.String("keyHex", hex.EncodeToString(rawKey[:min(keyLen, 64)])),
		)
	}
	return rawKey
}

// transformAndValidateIteratorKey extracts and validates a 40-byte ExpiryEntry from Firewood's iterator key.
//
// This function is similar to transformIteratorKey but returns a boolean indicating
// whether a valid ExpiryEntry was found. This allows the iterator to skip invalid
// internal trie nodes instead of attempting to process them.
//
// Returns (extractedKey, true) if a valid ExpiryEntry is found.
// Returns (originalKey, false) if no valid ExpiryEntry could be extracted.
func transformAndValidateIteratorKey(rawKey []byte, log logging.Logger) ([]byte, bool) {
	keyLen := len(rawKey)

	// Fast path: already correct size
	if keyLen == expectedExpiryEntryLength {
		valid := looksLikeExpiryEntry(rawKey, log)
		if valid && log != nil {
			log.Debug("Iterator key already correct size and valid",
				zap.Int("keyLen", keyLen),
			)
		}
		return rawKey, valid
	}

	// If shorter than expected, can't extract - return as invalid
	if keyLen < expectedExpiryEntryLength {
		if log != nil {
			log.Debug("Iterator key shorter than expected (invalid)",
				zap.Int("keyLen", keyLen),
				zap.Int("expected", expectedExpiryEntryLength),
			)
		}
		return rawKey, false
	}

	// Key is longer than expected - try to extract the 40-byte payload
	if log != nil {
		log.Debug("Iterator key longer than expected - attempting extraction",
			zap.Int("keyLen", keyLen),
			zap.Int("expected", expectedExpiryEntryLength),
			zap.Int("extraBytes", keyLen-expectedExpiryEntryLength),
		)
	}

	// Strategy 1: Try extracting last 40 bytes (common pattern for metadata prefix)
	candidate := rawKey[keyLen-expectedExpiryEntryLength:]
	if looksLikeExpiryEntry(candidate, log) {
		if log != nil {
			log.Debug("Successfully extracted valid ExpiryEntry from last 40 bytes",
				zap.Int("originalLen", keyLen),
			)
		}
		return candidate, true
	}

	// Strategy 2: Try extracting first 40 bytes (alternative wrapping pattern)
	candidate = rawKey[:expectedExpiryEntryLength]
	if looksLikeExpiryEntry(candidate, log) {
		if log != nil {
			log.Debug("Successfully extracted valid ExpiryEntry from first 40 bytes",
				zap.Int("originalLen", keyLen),
			)
		}
		return candidate, true
	}

	// Strategy 3: Sliding window (DISABLED for performance)
	// OPTIMIZATION: If both last-40 and first-40 failed, this is almost certainly
	// an internal trie node with no valid ExpiryEntry. Doing a full sliding window
	// search (50+ validations per key) is extremely wasteful when iterating through
	// thousands of trie nodes during genesis bootstrap.
	//
	// Performance impact: Reduced from ~59 validations/key to 2 validations/key
	// This gives a ~30x speedup for filtering trie nodes.
	//
	// If we encounter false negatives later (valid keys with middle offset),
	// we can re-enable this loop selectively.

	// No valid ExpiryEntry found - this is likely an internal trie node
	if log != nil {
		log.Debug("Could not extract valid ExpiryEntry (likely internal trie node)",
			zap.Int("keyLen", keyLen),
		)
	}
	return rawKey, false
}

// looksLikeExpiryEntry validates that 40-byte data has the structure of an ExpiryEntry.
//
// ExpiryEntry structure:
// - First 8 bytes: Unix timestamp (big-endian uint64)
// - Last 32 bytes: Validation ID (should not be all zeros or all ones)
//
// Returns true if data passes validation checks.
func looksLikeExpiryEntry(data []byte, log logging.Logger) bool {
	if len(data) != expectedExpiryEntryLength {
		return false
	}

	// Extract timestamp (first 8 bytes, big-endian)
	timestamp := binary.BigEndian.Uint64(data[:8])

	// Check timestamp is within reasonable bounds (2020-2100)
	// This prevents false positives from random binary data
	if timestamp < minReasonableTimestamp || timestamp > maxReasonableTimestamp {
		if log != nil {
			log.Debug("Timestamp out of reasonable range",
				zap.Uint64("timestamp", timestamp),
				zap.String("timestampDate", time.Unix(int64(timestamp), 0).Format(time.RFC3339)),
				zap.Uint64("minTimestamp", minReasonableTimestamp),
				zap.Uint64("maxTimestamp", maxReasonableTimestamp),
			)
		}
		return false
	}

	// Extract validation ID (last 32 bytes)
	validationID := data[8:]

	// Check validation ID is not all zeros
	allZeros := true
	for _, b := range validationID {
		if b != 0 {
			allZeros = false
			break
		}
	}
	if allZeros {
		if log != nil {
			log.Debug("Validation ID is all zeros")
		}
		return false
	}

	// Check validation ID is not all ones (0xFF)
	allOnes := true
	for _, b := range validationID {
		if b != 0xFF {
			allOnes = false
			break
		}
	}
	if allOnes {
		if log != nil {
			log.Debug("Validation ID is all ones")
		}
		return false
	}

	// Passed all validation checks
	if log != nil {
		log.Debug("Data looks like valid ExpiryEntry",
			zap.Uint64("timestamp", timestamp),
			zap.String("timestampDate", time.Unix(int64(timestamp), 0).Format(time.RFC3339)),
			zap.String("validationIDHex", hex.EncodeToString(validationID[:16])+"..."),
		)
	}

	return true
}
