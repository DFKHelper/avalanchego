// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package client

import (
	"context"
	"time"

	"github.com/ava-labs/avalanchego/ids"
)

// Client synchronously fetches data from the network to fulfill state sync requests.
// Supports both coreth and subnet-evm request/response patterns.
type Client interface {
	// GetLeafs synchronously sends the given request, returning a parsed response or error.
	// Verifies the response including range proofs.
	GetLeafs(ctx context.Context, request interface{}) (interface{}, error)

	// GetBlocks synchronously sends the given request, returning blocks or error.
	GetBlocks(ctx context.Context, request interface{}) (interface{}, error)

	// GetCode synchronously sends the given request, returning code bytes or error.
	GetCode(ctx context.Context, request interface{}) (interface{}, error)
}

// NetworkClient provides network communication for the sync client.
// This interface abstracts the underlying p2p network layer.
type NetworkClient interface {
	// SendAppRequest sends a request to a specific node and returns the response.
	SendAppRequest(
		ctx context.Context,
		nodeID ids.NodeID,
		requestBytes []byte,
	) ([]byte, error)

	// GetPeers returns the list of available peer node IDs.
	GetPeers() []ids.NodeID
}

// ClientStats tracks statistics for the sync client.
// Allows different implementations (coreth vs subnet-evm) to record metrics.
type ClientStats interface {
	// IncLeafsRequest increments the count of leafs requests sent
	IncLeafsRequest()

	// IncLeafsRequestSuccess increments successful leafs requests
	IncLeafsRequestSuccess()

	// IncLeafsRequestFailure increments failed leafs requests
	IncLeafsRequestFailure()

	// UpdateLeafsRequestTime updates the time spent on leafs requests
	UpdateLeafsRequestTime(duration time.Duration)

	// IncBlockRequest increments the count of block requests sent
	IncBlockRequest()

	// IncBlockRequestSuccess increments successful block requests
	IncBlockRequestSuccess()

	// IncBlockRequestFailure increments failed block requests
	IncBlockRequestFailure()

	// UpdateBlockRequestTime updates the time spent on block requests
	UpdateBlockRequestTime(duration time.Duration)

	// IncCodeRequest increments the count of code requests sent
	IncCodeRequest()

	// IncCodeRequestSuccess increments successful code requests
	IncCodeRequestSuccess()

	// IncCodeRequestFailure increments failed code requests
	IncCodeRequestFailure()

	// UpdateCodeRequestTime updates the time spent on code requests
	UpdateCodeRequestTime(duration time.Duration)

	// IncRetry increments the retry counter
	IncRetry()

	// IncPeerBlacklisted increments blacklisted peer counter
	IncPeerBlacklisted()
}

// PeerManager manages peer selection and tracking.
// Supports both scoring (coreth) and blacklisting (subnet-evm) strategies.
type PeerManager interface {
	// SelectPeer selects the best peer from available peers.
	// Returns zero NodeID if no suitable peer found.
	SelectPeer(availablePeers []ids.NodeID) ids.NodeID

	// RecordSuccess records a successful request to a peer.
	RecordSuccess(nodeID ids.NodeID, duration time.Duration)

	// RecordFailure records a failed request to a peer.
	RecordFailure(nodeID ids.NodeID)

	// IsBlacklisted returns true if the peer is currently blacklisted.
	IsBlacklisted(nodeID ids.NodeID) bool
}

// NoOpClientStats is a client stats recorder that does nothing.
// Useful for testing or when stats are not needed.
type NoOpClientStats struct{}

func (n *NoOpClientStats) IncLeafsRequest()                       {}
func (n *NoOpClientStats) IncLeafsRequestSuccess()                {}
func (n *NoOpClientStats) IncLeafsRequestFailure()                {}
func (n *NoOpClientStats) UpdateLeafsRequestTime(time.Duration)   {}
func (n *NoOpClientStats) IncBlockRequest()                       {}
func (n *NoOpClientStats) IncBlockRequestSuccess()                {}
func (n *NoOpClientStats) IncBlockRequestFailure()                {}
func (n *NoOpClientStats) UpdateBlockRequestTime(time.Duration)   {}
func (n *NoOpClientStats) IncCodeRequest()                        {}
func (n *NoOpClientStats) IncCodeRequestSuccess()                 {}
func (n *NoOpClientStats) IncCodeRequestFailure()                 {}
func (n *NoOpClientStats) UpdateCodeRequestTime(time.Duration)    {}
func (n *NoOpClientStats) IncRetry()                              {}
func (n *NoOpClientStats) IncPeerBlacklisted()                    {}
