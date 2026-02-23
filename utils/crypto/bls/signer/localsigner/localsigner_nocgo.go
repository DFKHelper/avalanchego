//go:build !cgo
// +build !cgo

// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package localsigner

import (
	"errors"

	"github.com/ava-labs/avalanchego/utils/crypto/bls"
)

var (
	ErrBLSRequiresCGO                = errors.New("BLS operations require CGO - build with CGO_ENABLED=1")
	ErrFailedSecretKeyDeserialize    = errors.New("couldn't deserialize secret key")
	ErrCantOpenFile                  = errors.New("couldn't open file")
	ErrInsufficientKeyData           = errors.New("insufficient key data")
	ErrFailedSecretKeyUnmarshal      = errors.New("couldn't unmarshal secret key")
	ErrFailedSecretKeyDecompress     = errors.New("couldn't decompress secret key")
	ErrInvalidSecretKey              = errors.New("invalid secret key")
	ErrCantCreateDirectory           = errors.New("couldn't create directory")
	ErrCantCreateFile                = errors.New("couldn't create file")
	ErrFailedSecretKeyMarshal        = errors.New("couldn't marshal secret key")
	ErrFailedSecretKeyCompress       = errors.New("couldn't compress secret key")
)

// Signer is a stub that will panic if used on non-CGO builds
type Signer struct{}

func New() (*Signer, error) {
	return nil, ErrBLSRequiresCGO
}

func NewFromReader(reader interface{}) (*Signer, error) {
	return nil, ErrBLSRequiresCGO
}

func Load(keyPath string) (*Signer, error) {
	return nil, ErrBLSRequiresCGO
}

func FromBytes(keyBytes []byte) (*Signer, error) {
	return nil, ErrBLSRequiresCGO
}

func FromFile(keyPath string) (*Signer, error) {
	return nil, ErrBLSRequiresCGO
}

func FromFileOrPersistNew(keyPath string) (*Signer, error) {
	return nil, ErrBLSRequiresCGO
}

func (s *Signer) Key() []byte {
	panic("BLS operations require CGO - build with CGO_ENABLED=1")
}

func (s *Signer) ToBytes() []byte {
	panic("BLS operations require CGO - build with CGO_ENABLED=1")
}

func (s *Signer) PublicKey() *bls.PublicKey {
	panic("BLS operations require CGO - build with CGO_ENABLED=1")
}

func (s *Signer) Sign(msg []byte) (*bls.Signature, error) {
	panic("BLS operations require CGO - build with CGO_ENABLED=1")
}

func (s *Signer) SignProofOfPossession(msg []byte) (*bls.Signature, error) {
	panic("BLS operations require CGO - build with CGO_ENABLED=1")
}

func (s *Signer) Shutdown() error {
	panic("BLS operations require CGO - build with CGO_ENABLED=1")
}

func (s *Signer) SaveInsecure(keyPath string) error {
	panic("BLS operations require CGO - build with CGO_ENABLED=1")
}
