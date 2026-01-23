// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package statesync

import (
	"context"
)

// CodeQueueAdapter adapts Coreth's CodeQueue to the CodeSyncInterface
// Coreth's CodeQueue is started externally and doesn't use the Done() pattern
type CodeQueueAdapter struct {
	queue interface{} // *CodeQueue - avoid import cycle
}

// NewCodeQueueAdapter creates an adapter for Coreth's CodeQueue
func NewCodeQueueAdapter(queue interface{}) *CodeQueueAdapter {
	return &CodeQueueAdapter{
		queue: queue,
	}
}

// Start is a no-op since CodeQueue is already started externally in Coreth
func (c *CodeQueueAdapter) Start(ctx context.Context) {
	// No-op: CodeQueue is managed externally
}

// Done returns a dummy channel that never closes
// Coreth doesn't use the Done() pattern - code sync is managed differently
func (c *CodeQueueAdapter) Done() <-chan error {
	// Return a channel that never closes to indicate code sync is always "in progress"
	// The actual completion is handled by Finalize() when the account trie completes
	ch := make(chan error)
	return ch
}

// NotifyAccountTrieCompleted calls Finalize() on the CodeQueue
// This triggers the final code sync completion in Coreth
func (c *CodeQueueAdapter) NotifyAccountTrieCompleted() {
	// Type assert to the actual CodeQueue interface
	// In actual implementation, this would call queue.Finalize()
	// For now, we leave it as interface{} to avoid import cycles
	if finalizer, ok := c.queue.(interface{ Finalize() error }); ok {
		_ = finalizer.Finalize()
	}
}

// CodeSyncerAdapter adapts Subnet-EVM's codeSyncer to the CodeSyncInterface
// Subnet-EVM's codeSyncer uses the Start() + Done() pattern
type CodeSyncerAdapter struct {
	syncer interface{} // *codeSyncer - avoid import cycle
}

// NewCodeSyncerAdapter creates an adapter for Subnet-EVM's codeSyncer
func NewCodeSyncerAdapter(syncer interface{}) *CodeSyncerAdapter {
	return &CodeSyncerAdapter{
		syncer: syncer,
	}
}

// Start begins code fetching in the codeSyncer
func (c *CodeSyncerAdapter) Start(ctx context.Context) {
	// Type assert and call start(ctx)
	if starter, ok := c.syncer.(interface{ start(context.Context) }); ok {
		starter.start(ctx)
	}
}

// Done returns the channel from codeSyncer.Done()
func (c *CodeSyncerAdapter) Done() <-chan error {
	// Type assert and return Done() channel
	if doner, ok := c.syncer.(interface{ Done() <-chan error }); ok {
		return doner.Done()
	}

	// Fallback: return closed channel indicating immediate completion
	ch := make(chan error)
	close(ch)
	return ch
}

// NotifyAccountTrieCompleted notifies the codeSyncer that account trie finished
func (c *CodeSyncerAdapter) NotifyAccountTrieCompleted() {
	// Type assert and call notifyAccountTrieCompleted()
	if notifier, ok := c.syncer.(interface{ notifyAccountTrieCompleted() }); ok {
		notifier.notifyAccountTrieCompleted()
	}
}
