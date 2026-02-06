// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package firewood

import "encoding/hex"

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// isASCII checks if all bytes are printable ASCII
func isASCII(data []byte) bool {
	for _, b := range data {
		if b < 32 || b > 126 {
			return false
		}
	}
	return true
}

// tryHexDecode attempts to decode hex-encoded data from Firewood.
// Returns the decoded bytes and true if successful, or nil and false if not hex.
func tryHexDecode(data []byte) ([]byte, bool) {
	// Quick sanity check: hex encoding roughly doubles the size
	// Also check if all bytes are valid hex characters (0-9, a-f, A-F)
	if len(data) < 2 {
		return nil, false
	}

	// Check if data looks like hex by examining first few bytes
	for i := 0; i < len(data) && i < 10; i++ {
		b := data[i]
		if !((b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')) {
			// Not hex ASCII, return original
			return nil, false
		}
	}

	// Try to decode as hex
	decoded, err := hex.DecodeString(string(data))
	if err != nil {
		// Not valid hex, return original
		return nil, false
	}

	return decoded, true
}
