//go:build !cgo
// +build !cgo

// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package compression

import (
	"errors"
	"math"
)

var (
	ErrZstdRequiresCGO          = errors.New("zstd compression requires CGO - build with CGO_ENABLED=1")
	ErrInvalidMaxSizeCompressor = errors.New("invalid compressor max size")
	ErrDecompressedMsgTooLarge  = errors.New("decompressed msg too large")
	ErrMsgTooLarge              = errors.New("msg too large to be compressed")
)

func NewZstdCompressor(maxSize int64) (Compressor, error) {
	return nil, ErrZstdRequiresCGO
}

func NewZstdCompressorWithLevel(maxSize int64, level int) (Compressor, error) {
	if maxSize == math.MaxInt64 {
		return nil, ErrInvalidMaxSizeCompressor
	}
	return nil, ErrZstdRequiresCGO
}
