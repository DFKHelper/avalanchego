//go:build !cgo
// +build !cgo

// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package bls

import "errors"

// SignatureLen is the length of a compressed BLS signature (96 bytes for BLS12-381 G2)
const SignatureLen = 96

var (
	ErrFailedSignatureDecompress  = errors.New("couldn't decompress signature")
	ErrInvalidSignature           = errors.New("invalid signature")
	ErrNoSignatures               = errors.New("no signatures")
	ErrFailedSignatureAggregation = errors.New("couldn't aggregate signatures")
)

// Stub types for non-CGO builds
type (
	Signature          struct{}
	AggregateSignature struct{}
)

// SignatureToBytes returns the compressed big-endian format of the signature.
func SignatureToBytes(sig *Signature) []byte {
	panic("BLS operations require CGO - build with CGO_ENABLED=1")
}

// SignatureFromBytes parses the compressed big-endian format of the signature
// into a signature.
func SignatureFromBytes(sigBytes []byte) (*Signature, error) {
	panic("BLS operations require CGO - build with CGO_ENABLED=1")
}

// AggregateSignatures aggregates a non-zero number of signatures into a single
// aggregated signature.
// Invariant: all [sigs] have been validated.
func AggregateSignatures(sigs []*Signature) (*Signature, error) {
	panic("BLS operations require CGO - build with CGO_ENABLED=1")
}
