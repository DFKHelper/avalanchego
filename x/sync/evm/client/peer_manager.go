// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package client

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/ava-labs/avalanchego/ids"
)

const epsilon = 1e-6 // Small amount to avoid division by zero

// NewPeerManager creates a peer manager based on the configuration.
func NewPeerManager(config ClientConfig, stats ClientStats) PeerManager {
	switch config.PeerMode {
	case PeerModeScoring:
		return &scoringPeerManager{
			config: config,
			stats:  stats,
			peers:  make(map[ids.NodeID]*peerStats),
		}
	case PeerModeBlacklisting:
		return &blacklistingPeerManager{
			config:    config,
			stats:     stats,
			peers:     make(map[ids.NodeID]*blacklistedPeer),
			blacklist: make(map[ids.NodeID]time.Time),
		}
	default:
		return &scoringPeerManager{
			config: config,
			stats:  stats,
			peers:  make(map[ids.NodeID]*peerStats),
		}
	}
}

// scoringPeerManager implements peer scoring strategy (used by coreth).
// Tracks success rate and response time, prefers better-performing peers.
type scoringPeerManager struct {
	config ClientConfig
	stats  ClientStats
	mu     sync.RWMutex
	peers  map[ids.NodeID]*peerStats

	lastUsedPeer atomic.Value // ids.NodeID
	lastUsedTime atomic.Int64 // Unix nanoseconds
}

// peerStats tracks performance metrics for a single peer.
type peerStats struct {
	nodeID          ids.NodeID
	successCount    atomic.Uint64
	failureCount    atomic.Uint64
	avgResponseTime atomic.Uint64 // Exponential moving average in nanoseconds
	totalRequests   atomic.Uint64
}

func (p *peerStats) score(config ClientConfig) float64 {
	total := p.totalRequests.Load()
	if total < uint64(config.MinRequestsForScoring) {
		return 0 // Not enough data
	}

	success := p.successCount.Load()
	avgTime := p.avgResponseTime.Load()

	successRate := float64(success) / float64(total)

	if avgTime == 0 {
		return successRate
	}

	avgTimeSeconds := float64(avgTime) / 1e9
	return successRate / (avgTimeSeconds + epsilon)
}

func (s *scoringPeerManager) SelectPeer(availablePeers []ids.NodeID) ids.NodeID {
	if len(availablePeers) == 0 {
		return ids.EmptyNodeID
	}

	// Check if we should stick with the last used peer
	if lastPeer, ok := s.lastUsedPeer.Load().(ids.NodeID); ok {
		lastUsedNanos := s.lastUsedTime.Load()
		if time.Since(time.Unix(0, lastUsedNanos)) < s.config.StickyPeerDuration {
			// Check if last peer is still available
			for _, peer := range availablePeers {
				if peer == lastPeer {
					return lastPeer
				}
			}
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Score all available peers and select the best one
	bestPeer := availablePeers[0]
	bestScore := 0.0

	for _, peerID := range availablePeers {
		if stats, exists := s.peers[peerID]; exists {
			score := stats.score(s.config)
			if score > bestScore {
				bestScore = score
				bestPeer = peerID
			}
		} else {
			// Unscored peer - give it a chance
			if bestScore == 0 {
				bestPeer = peerID
			}
		}
	}

	return bestPeer
}

func (s *scoringPeerManager) RecordSuccess(nodeID ids.NodeID, duration time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stats, exists := s.peers[nodeID]
	if !exists {
		stats = &peerStats{nodeID: nodeID}
		s.peers[nodeID] = stats
	}

	stats.successCount.Add(1)
	stats.totalRequests.Add(1)

	// Update exponential moving average response time
	newTime := uint64(duration.Nanoseconds())
	oldAvg := stats.avgResponseTime.Load()
	if oldAvg == 0 {
		stats.avgResponseTime.Store(newTime)
	} else {
		// EMA: new_avg = alpha * new_value + (1 - alpha) * old_avg
		alpha := s.config.ScoringDecayFactor
		newAvg := uint64(alpha*float64(newTime) + (1-alpha)*float64(oldAvg))
		stats.avgResponseTime.Store(newAvg)
	}

	// Update sticky peer tracking
	s.lastUsedPeer.Store(nodeID)
	s.lastUsedTime.Store(time.Now().UnixNano())
}

func (s *scoringPeerManager) RecordFailure(nodeID ids.NodeID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stats, exists := s.peers[nodeID]
	if !exists {
		stats = &peerStats{nodeID: nodeID}
		s.peers[nodeID] = stats
	}

	stats.failureCount.Add(1)
	stats.totalRequests.Add(1)
}

func (s *scoringPeerManager) IsBlacklisted(nodeID ids.NodeID) bool {
	return false // Scoring mode doesn't blacklist
}

// blacklistingPeerManager implements peer blacklisting strategy (used by subnet-evm).
// Temporarily blacklists peers after consecutive failures.
type blacklistingPeerManager struct {
	config    ClientConfig
	stats     ClientStats
	mu        sync.RWMutex
	peers     map[ids.NodeID]*blacklistedPeer
	blacklist map[ids.NodeID]time.Time
}

type blacklistedPeer struct {
	consecutiveFailures atomic.Uint64
	lastFailureTime     atomic.Int64 // Unix nanoseconds
}

func (b *blacklistingPeerManager) SelectPeer(availablePeers []ids.NodeID) ids.NodeID {
	if len(availablePeers) == 0 {
		return ids.EmptyNodeID
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	now := time.Now()

	// Find first non-blacklisted peer
	for _, peerID := range availablePeers {
		if blacklistedUntil, exists := b.blacklist[peerID]; exists {
			if now.Before(blacklistedUntil) {
				continue // Still blacklisted
			}
			// Blacklist expired, this peer is available
		}
		return peerID
	}

	// All peers blacklisted - return first one anyway (blacklist will expire)
	return availablePeers[0]
}

func (b *blacklistingPeerManager) RecordSuccess(nodeID ids.NodeID, duration time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Reset consecutive failures on success
	if peer, exists := b.peers[nodeID]; exists {
		peer.consecutiveFailures.Store(0)
	}

	// Remove from blacklist if present
	delete(b.blacklist, nodeID)
}

func (b *blacklistingPeerManager) RecordFailure(nodeID ids.NodeID) {
	b.mu.Lock()
	defer b.mu.Unlock()

	peer, exists := b.peers[nodeID]
	if !exists {
		peer = &blacklistedPeer{}
		b.peers[nodeID] = peer
	}

	failures := peer.consecutiveFailures.Add(1)
	peer.lastFailureTime.Store(time.Now().UnixNano())

	// Blacklist if consecutive failures exceed threshold
	if failures >= uint64(b.config.MaxConsecutiveFailures) {
		blacklistUntil := time.Now().Add(b.config.PeerBlacklistDuration)
		b.blacklist[nodeID] = blacklistUntil
		b.stats.IncPeerBlacklisted()

		// Reset counter to avoid overflow
		peer.consecutiveFailures.Store(0)
	}
}

func (b *blacklistingPeerManager) IsBlacklisted(nodeID ids.NodeID) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if blacklistedUntil, exists := b.blacklist[nodeID]; exists {
		return time.Now().Before(blacklistedUntil)
	}
	return false
}
