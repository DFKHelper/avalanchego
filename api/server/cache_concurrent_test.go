// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/utils/logging"
)

func TestRPCCache_ConcurrentAccess(t *testing.T) {
	require := require.New(t)

	config := RPCCacheConfig{
		Enabled:  true,
		Size:     1000,
		TTL:      5 * time.Second,
		Readonly: true,
	}

	cache, err := newRPCCache(
		logging.NoLog{},
		config,
		prometheus.NewRegistry(),
	)
	require.NoError(err)
	require.NotNil(cache)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			Params []int  `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"result":%d}`, req.Params[0])
	})

	middleware := cache.Middleware(handler)

	// Warm up cache with all unique param values before concurrent load
	for param := 0; param < 5; param++ {
		body := fmt.Sprintf(`{"method":"eth_getBalance","params":[%d]}`, param)
		req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewBufferString(body))
		w := httptest.NewRecorder()
		middleware.ServeHTTP(w, req)
		require.Equal("MISS", w.Header().Get("X-Cache"))
	}

	const numGoroutines = 100
	const requestsPerGoroutine = 10

	var wg sync.WaitGroup
	var hits, misses atomic.Int64

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < requestsPerGoroutine; j++ {
				param := j % 5
				body := fmt.Sprintf(`{"method":"eth_getBalance","params":[%d]}`, param)

				req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewBufferString(body))
				w := httptest.NewRecorder()
				middleware.ServeHTTP(w, req)

				if w.Header().Get("X-Cache") == "HIT" {
					hits.Add(1)
				} else {
					misses.Add(1)
				}
				require.Equal(http.StatusOK, w.Code)
			}
		}(i)
	}

	wg.Wait()

	t.Logf("Hits: %d, Misses: %d", hits.Load(), misses.Load())
	// After warming up the cache, concurrent requests for the same 5 keys should almost all hit
	require.Greater(hits.Load(), misses.Load(), "Warmed-up cache should have more hits than misses")
}

func TestRPCCache_ConcurrentTTLExpiration(t *testing.T) {
	require := require.New(t)

	config := RPCCacheConfig{
		Enabled:  true,
		Size:     100,
		TTL:      100 * time.Millisecond,
		Readonly: true,
	}

	cache, err := newRPCCache(
		logging.NoLog{},
		config,
		prometheus.NewRegistry(),
	)
	require.NoError(err)
	require.NotNil(cache)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":"ok"}`))
	})

	middleware := cache.Middleware(handler)

	// Populate cache
	body := `{"method":"eth_getBalance","params":[1]}`
	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, req)
	require.Equal("MISS", w.Header().Get("X-Cache"))

	// Wait for TTL to expire
	time.Sleep(150 * time.Millisecond)

	// Hammer the expired entry with concurrent requests — verifies no panic or race condition
	const numGoroutines = 50
	var wg sync.WaitGroup
	errors := make([]error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewBufferString(body))
			w := httptest.NewRecorder()
			middleware.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				errors[id] = fmt.Errorf("unexpected status code: %d", w.Code)
			}
		}(i)
	}

	wg.Wait()

	for i, err := range errors {
		require.NoError(err, "Goroutine %d failed", i)
	}
}

func TestRPCCache_FlushDuringRead(t *testing.T) {
	require := require.New(t)

	config := RPCCacheConfig{
		Enabled:  true,
		Size:     1000,
		TTL:      1 * time.Minute,
		Readonly: true,
	}

	cache, err := newRPCCache(
		logging.NoLog{},
		config,
		prometheus.NewRegistry(),
	)
	require.NoError(err)
	require.NotNil(cache)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":"ok"}`))
	})

	middleware := cache.Middleware(handler)

	// Populate cache with multiple entries
	for i := 0; i < 10; i++ {
		body := fmt.Sprintf(`{"method":"eth_getBalance","params":[%d]}`, i)
		req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewBufferString(body))
		w := httptest.NewRecorder()
		middleware.ServeHTTP(w, req)
	}

	var wg sync.WaitGroup
	stopCh := make(chan struct{})

	// Reader goroutines run concurrently with the flush
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
					body := fmt.Sprintf(`{"method":"eth_getBalance","params":[%d]}`, id)
					req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewBufferString(body))
					w := httptest.NewRecorder()
					middleware.ServeHTTP(w, req)
				}
			}
		}(i)
	}

	// Flusher goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(50 * time.Millisecond)
		cache.Flush()
		time.Sleep(50 * time.Millisecond)
		close(stopCh)
	}()

	wg.Wait()
	// Verify no panics or data races occurred (race detector enforces this)
}

func TestRPCCache_NonSuccessStatusCodes(t *testing.T) {
	require := require.New(t)

	config := DefaultRPCCacheConfig()
	cache, err := newRPCCache(
		logging.NoLog{},
		config,
		prometheus.NewRegistry(),
	)
	require.NoError(err)

	testCases := []struct {
		name        string
		statusCode  int
		shouldCache bool
	}{
		{"200 OK", http.StatusOK, true},
		{"201 Created", http.StatusCreated, false},
		{"400 Bad Request", http.StatusBadRequest, false},
		{"404 Not Found", http.StatusNotFound, false},
		{"500 Internal Server Error", http.StatusInternalServerError, false},
		{"503 Service Unavailable", http.StatusServiceUnavailable, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				w.Write([]byte(`{"result":"ok"}`))
			})

			middleware := cache.Middleware(handler)

			// Use statusCode as unique param to avoid cross-subtest cache contamination
			body := fmt.Sprintf(`{"method":"eth_getBalance","params":[%d]}`, tc.statusCode)
			req1 := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewBufferString(body))
			w1 := httptest.NewRecorder()
			middleware.ServeHTTP(w1, req1)
			require.Equal("MISS", w1.Header().Get("X-Cache"))

			req2 := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewBufferString(body))
			w2 := httptest.NewRecorder()
			middleware.ServeHTTP(w2, req2)

			if tc.shouldCache {
				require.Equal("HIT", w2.Header().Get("X-Cache"),
					"Status %d should be cached", tc.statusCode)
			} else {
				require.Equal("MISS", w2.Header().Get("X-Cache"),
					"Status %d should NOT be cached", tc.statusCode)
			}
		})
	}
}
