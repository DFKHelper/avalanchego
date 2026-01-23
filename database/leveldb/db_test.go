// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package leveldb

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/database/dbtest"
	"github.com/ava-labs/avalanchego/utils/logging"
)

func TestInterface(t *testing.T) {
	for name, test := range dbtest.Tests {
		t.Run(name, func(t *testing.T) {
			folder := t.TempDir()
			db, err := New(folder, testConfig(), logging.NoLog{}, prometheus.NewRegistry())
			require.NoError(t, err)

			test(t, db)

			_ = db.Close()
		})
	}
}

// testConfig returns a minimal configuration suitable for testing
// that won't trigger memory validation errors in test environments
func testConfig() []byte {
	cfg := map[string]interface{}{
		"blockCacheCapacity":      12 * 1024 * 1024,  // 12 MB (minimal)
		"writeBuffer":             4 * 1024 * 1024,   // 4 MB
		"openFilesCacheCapacity":  128,               // Minimal file cache
		"filterBitsPerKey":        DefaultBitsPerKey,
	}
	configBytes, _ := json.Marshal(cfg)
	return configBytes
}

func newDB(t testing.TB) database.Database {
	folder := t.TempDir()
	db, err := New(folder, testConfig(), logging.NoLog{}, prometheus.NewRegistry())
	require.NoError(t, err)
	return db
}

func FuzzKeyValue(f *testing.F) {
	db := newDB(f)
	defer db.Close()

	dbtest.FuzzKeyValue(f, db)
}

func FuzzNewIteratorWithPrefix(f *testing.F) {
	db := newDB(f)
	defer db.Close()

	dbtest.FuzzNewIteratorWithPrefix(f, db)
}

func FuzzNewIteratorWithStartAndPrefix(f *testing.F) {
	db := newDB(f)
	defer db.Close()

	dbtest.FuzzNewIteratorWithStartAndPrefix(f, db)
}

func BenchmarkInterface(b *testing.B) {
	for _, size := range dbtest.BenchmarkSizes {
		keys, values := dbtest.SetupBenchmark(b, size[0], size[1], size[2])
		for name, bench := range dbtest.Benchmarks {
			b.Run(fmt.Sprintf("leveldb_%d_pairs_%d_keys_%d_values_%s", size[0], size[1], size[2], name), func(b *testing.B) {
				db := newDB(b)

				bench(b, db, keys, values)

				// The database may have been closed by the test, so we don't care if it
				// errors here.
				_ = db.Close()
			})
		}
	}
}
