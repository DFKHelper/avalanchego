// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/codec"
	"github.com/ava-labs/avalanchego/ids"
)

// Mock implementations for testing

type mockNetworkClient struct {
	peers     []ids.NodeID
	responses map[ids.NodeID][]byte
	errors    map[ids.NodeID]error
	callCount map[ids.NodeID]int
}

func newMockNetworkClient() *mockNetworkClient {
	return &mockNetworkClient{
		peers:     make([]ids.NodeID, 0),
		responses: make(map[ids.NodeID][]byte),
		errors:    make(map[ids.NodeID]error),
		callCount: make(map[ids.NodeID]int),
	}
}

func (m *mockNetworkClient) GetPeers() []ids.NodeID {
	return m.peers
}

func (m *mockNetworkClient) SendAppRequest(ctx context.Context, nodeID ids.NodeID, requestBytes []byte) ([]byte, error) {
	m.callCount[nodeID]++
	if err, exists := m.errors[nodeID]; exists {
		return nil, err
	}
	if resp, exists := m.responses[nodeID]; exists {
		return resp, nil
	}
	return nil, errors.New("no response configured")
}

type mockCodec struct {
	marshalFunc   func(uint16, interface{}) ([]byte, error)
	unmarshalFunc func([]byte, interface{}) error
}

func (m *mockCodec) Marshal(version uint16, v interface{}) ([]byte, error) {
	if m.marshalFunc != nil {
		return m.marshalFunc(version, v)
	}
	return []byte("marshaled"), nil
}

func (m *mockCodec) Unmarshal(bytes []byte, v interface{}) error {
	if m.unmarshalFunc != nil {
		return m.unmarshalFunc(bytes, v)
	}
	return nil
}

func (m *mockCodec) Size(version uint16, v interface{}) (int, error) {
	return 0, nil
}

func TestNewClient(t *testing.T) {
	config := DefaultCorethConfig()
	network := newMockNetworkClient()
	codec := &mockCodec{}
	stats := &NoOpClientStats{}

	client, err := NewClient(config, network, codec, stats)
	require.NoError(t, err)
	require.NotNil(t, client)
}

func TestNewClient_InvalidConfig(t *testing.T) {
	config := ClientConfig{
		BaseRetryInterval: 0, // Invalid
	}
	network := newMockNetworkClient()
	codec := &mockCodec{}
	stats := &NoOpClientStats{}

	client, err := NewClient(config, network, codec, stats)
	require.Error(t, err)
	require.Nil(t, client)
}

func TestGetLeafs_Success(t *testing.T) {
	config := DefaultCorethConfig()
	network := newMockNetworkClient()

	peer1 := ids.GenerateTestNodeID()
	network.peers = []ids.NodeID{peer1}
	network.responses[peer1] = []byte("response")

	codec := &mockCodec{}
	stats := &NoOpClientStats{}

	client, err := NewClient(config, network, codec, stats)
	require.NoError(t, err)

	ctx := context.Background()
	request := struct{ Data string }{Data: "test"}

	response, err := client.GetLeafs(ctx, request)
	require.NoError(t, err)
	require.NotNil(t, response)

	// Verify peer was called
	require.Equal(t, 1, network.callCount[peer1])
}

func TestGetBlocks_Success(t *testing.T) {
	config := DefaultCorethConfig()
	network := newMockNetworkClient()

	peer1 := ids.GenerateTestNodeID()
	network.peers = []ids.NodeID{peer1}
	network.responses[peer1] = []byte("response")

	codec := &mockCodec{}
	stats := &NoOpClientStats{}

	client, err := NewClient(config, network, codec, stats)
	require.NoError(t, err)

	ctx := context.Background()
	request := struct{ Hash string }{Hash: "test"}

	response, err := client.GetBlocks(ctx, request)
	require.NoError(t, err)
	require.NotNil(t, response)

	require.Equal(t, 1, network.callCount[peer1])
}

func TestGetCode_Success(t *testing.T) {
	config := DefaultSubnetEVMConfig() // Test with subnet-evm config (has code-specific timeout)
	network := newMockNetworkClient()

	peer1 := ids.GenerateTestNodeID()
	network.peers = []ids.NodeID{peer1}
	network.responses[peer1] = []byte("response")

	codec := &mockCodec{}
	stats := &NoOpClientStats{}

	client, err := NewClient(config, network, codec, stats)
	require.NoError(t, err)

	ctx := context.Background()
	request := struct{ Hashes []string }{Hashes: []string{"hash1"}}

	response, err := client.GetCode(ctx, request)
	require.NoError(t, err)
	require.NotNil(t, response)

	require.Equal(t, 1, network.callCount[peer1])
}

func TestRequestWithRetry_NoPeers(t *testing.T) {
	config := DefaultCorethConfig()
	network := newMockNetworkClient()
	network.peers = []ids.NodeID{} // No peers

	codec := &mockCodec{}
	stats := &NoOpClientStats{}

	client, err := NewClient(config, network, codec, stats)
	require.NoError(t, err)

	ctx := context.Background()
	request := struct{ Data string }{Data: "test"}

	response, err := client.GetLeafs(ctx, request)
	require.Error(t, err)
	require.Nil(t, response)
	require.Contains(t, err.Error(), "no peers available")
}

func TestRequestWithRetry_PeerFailureWithRetry(t *testing.T) {
	config := DefaultCorethConfig()
	config.MaxRetriesPerRequest = 3
	config.BaseRetryInterval = 1 * time.Millisecond // Fast retry for test

	network := newMockNetworkClient()

	peer1 := ids.GenerateTestNodeID()
	peer2 := ids.GenerateTestNodeID()
	network.peers = []ids.NodeID{peer1, peer2}

	// Peer1 fails, peer2 succeeds
	network.errors[peer1] = errors.New("peer1 error")
	network.responses[peer2] = []byte("success")

	codec := &mockCodec{}
	stats := &NoOpClientStats{}

	client, err := NewClient(config, network, codec, stats)
	require.NoError(t, err)

	ctx := context.Background()
	request := struct{ Data string }{Data: "test"}

	response, err := client.GetLeafs(ctx, request)
	require.NoError(t, err)
	require.NotNil(t, response)

	// Should have retried and succeeded with peer2
	require.Greater(t, network.callCount[peer1]+network.callCount[peer2], 0)
}

func TestRequestWithRetry_MaxRetriesExceeded(t *testing.T) {
	config := DefaultCorethConfig()
	config.MaxRetriesPerRequest = 2
	config.BaseRetryInterval = 1 * time.Millisecond

	network := newMockNetworkClient()

	peer1 := ids.GenerateTestNodeID()
	network.peers = []ids.NodeID{peer1}
	network.errors[peer1] = errors.New("persistent error")

	codec := &mockCodec{}
	stats := &NoOpClientStats{}

	client, err := NewClient(config, network, codec, stats)
	require.NoError(t, err)

	ctx := context.Background()
	request := struct{ Data string }{Data: "test"}

	response, err := client.GetLeafs(ctx, request)
	require.Error(t, err)
	require.Nil(t, response)
	require.ErrorIs(t, err, ErrTooManyRetries)

	// Should have tried up to max retries
	require.Equal(t, 2, network.callCount[peer1])
}

func TestRequestWithRetry_ContextCancelled(t *testing.T) {
	config := DefaultCorethConfig()
	config.MaxRetriesPerRequest = 10
	config.BaseRetryInterval = 100 * time.Millisecond // Longer delay

	network := newMockNetworkClient()
	peer1 := ids.GenerateTestNodeID()
	network.peers = []ids.NodeID{peer1}
	network.errors[peer1] = errors.New("error")

	codec := &mockCodec{}
	stats := &NoOpClientStats{}

	client, err := NewClient(config, network, codec, stats)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	request := struct{ Data string }{Data: "test"}

	response, err := client.GetLeafs(ctx, request)
	require.Error(t, err)
	require.Nil(t, response)
}

func TestSleepBackoff_SimpleMode(t *testing.T) {
	config := DefaultCorethConfig()
	config.RetryMode = RetryModeSimple
	config.BaseRetryInterval = 10 * time.Millisecond
	config.MaxRetryInterval = 100 * time.Millisecond

	network := newMockNetworkClient()
	codec := &mockCodec{}
	stats := &NoOpClientStats{}

	c, err := NewClient(config, network, codec, stats)
	require.NoError(t, err)

	client := c.(*client)
	ctx := context.Background()

	// Test exponential backoff
	start := time.Now()
	client.sleep(ctx, 0)
	duration := time.Since(start)
	require.GreaterOrEqual(t, duration, 10*time.Millisecond)

	// Should cap at max
	start = time.Now()
	client.sleep(ctx, 10) // Large attempt number
	duration = time.Since(start)
	require.GreaterOrEqual(t, duration, 100*time.Millisecond)
	require.Less(t, duration, 150*time.Millisecond) // Should be capped
}

func TestSleepBackoff_AdvancedMode(t *testing.T) {
	config := DefaultSubnetEVMConfig()
	config.RetryMode = RetryModeAdvanced
	config.BaseRetryInterval = 10 * time.Millisecond
	config.MaxRetryInterval = 100 * time.Millisecond

	network := newMockNetworkClient()
	codec := &mockCodec{}
	stats := &NoOpClientStats{}

	c, err := NewClient(config, network, codec, stats)
	require.NoError(t, err)

	client := c.(*client)
	ctx := context.Background()

	// Test exponential backoff (2^attempt)
	start := time.Now()
	client.sleep(ctx, 0) // 10ms * 2^0 = 10ms
	duration := time.Since(start)
	require.GreaterOrEqual(t, duration, 10*time.Millisecond)

	start = time.Now()
	client.sleep(ctx, 1) // 10ms * 2^1 = 20ms
	duration = time.Since(start)
	require.GreaterOrEqual(t, duration, 20*time.Millisecond)
}
