//go:build !cgo
// +build !cgo

// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package bls

import "errors"

// PublicKeyLen is the length of a compressed BLS public key (48 bytes for BLS12-381 G1)
const PublicKeyLen = 48

var (
	ErrNoPublicKeys               = errors.New("no public keys")
	ErrFailedPublicKeyDecompress  = errors.New("couldn't decompress public key")
	errInvalidPublicKey           = errors.New("invalid public key")
	errFailedPublicKeyAggregation = errors.New("couldn't aggregate public keys")
)

// Stub types for non-CGO builds
type (
	PublicKey          struct{}
	AggregatePublicKey struct{}
)

// PublicKeyToCompressedBytes returns the compressed big-endian format of the
// public key.
func PublicKeyToCompressedBytes(pk *PublicKey) []byte {
	panic("BLS operations require CGO - build with CGO_ENABLED=1")
}

// PublicKeyFromCompressedBytes parses the compressed big-endian format of the
// public key into a public key.
func PublicKeyFromCompressedBytes(pkBytes []byte) (*PublicKey, error) {
	panic("BLS operations require CGO - build with CGO_ENABLED=1")
}

// PublicKeyToUncompressedBytes returns the uncompressed big-endian format of
// the public key.
func PublicKeyToUncompressedBytes(key *PublicKey) []byte {
	panic("BLS operations require CGO - build with CGO_ENABLED=1")
}

// PublicKeyFromValidUncompressedBytes parses the uncompressed big-endian format
// of the public key into a public key. It is assumed that the provided bytes
// are valid.
func PublicKeyFromValidUncompressedBytes(pkBytes []byte) *PublicKey {
	panic("BLS operations require CGO - build with CGO_ENABLED=1")
}

// AggregatePublicKeys aggregates a non-zero number of public keys into a single
// aggregated public key.
// Invariant: all [pks] have been validated.
func AggregatePublicKeys(pks []*PublicKey) (*PublicKey, error) {
	panic("BLS operations require CGO - build with CGO_ENABLED=1")
}

// Verify the [sig] of [msg] against the [pk].
// The [sig] and [pk] may have been an aggregation of other signatures and keys.
// Invariant: [pk] and [sig] have both been validated.
func Verify(pk *PublicKey, sig *Signature, msg []byte) bool {
	panic("BLS operations require CGO - build with CGO_ENABLED=1")
}

// Verify the possession of the secret pre-image of [sk] by verifying a [sig] of
// [msg] against the [pk].
// The [sig] and [pk] may have been an aggregation of other signatures and keys.
// Invariant: [pk] and [sig] have both been validated.
func VerifyProofOfPossession(pk *PublicKey, sig *Signature, msg []byte) bool {
	panic("BLS operations require CGO - build with CGO_ENABLED=1")
}
