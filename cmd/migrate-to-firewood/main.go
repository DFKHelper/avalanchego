// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

//go:build !windows
// +build !windows

package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ava-labs/avalanchego/database/firewood"
	"github.com/ava-labs/avalanchego/database/leveldb"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/prometheus/client_golang/prometheus"
)

func main() {
	var (
		sourcePath = flag.String("source", "", "Path to source LevelDB database")
		targetPath = flag.String("target", "", "Path to target Firewood database")
	)
	flag.Parse()

	if *sourcePath == "" || *targetPath == "" {
		fmt.Println("Usage: migrate-to-firewood -source <leveldb-path> -target <firewood-path>")
		os.Exit(1)
	}

	logger := logging.NewLogger("migrate", logging.NewWrappedCore(logging.Info, os.Stdout, logging.Plain.ConsoleEncoder()))

	logger.Info(fmt.Sprintf("Starting migration from %s to %s", *sourcePath, *targetPath))

	// Create metrics registry for LevelDB
	registry := prometheus.NewRegistry()

	// Open source LevelDB (read-only would be safer but may not be supported)
	source, err := leveldb.New(*sourcePath, nil, logger, registry)
	if err != nil {
		logger.Fatal(fmt.Sprintf("Failed to open source database: %v", err))
	}
	defer source.Close()

	// Create target Firewood database
	target, err := firewood.New(*targetPath, nil, logger)
	if err != nil {
		logger.Fatal(fmt.Sprintf("Failed to create target database: %v", err))
	}

	// Migrate data
	startTime := time.Now()
	iter := source.NewIterator()
	defer iter.Release()

	var (
		count     int
		batchSize = 10000
		batch     = target.NewBatch()
	)

	logger.Info("Beginning data copy...")

	for iter.Next() {
		key := iter.Key()
		value := iter.Value()

		// Copy to avoid iterator issues
		keyCopy := make([]byte, len(key))
		valueCopy := make([]byte, len(value))
		copy(keyCopy, key)
		copy(valueCopy, value)

		if err := batch.Put(keyCopy, valueCopy); err != nil {
			logger.Fatal(fmt.Sprintf("Failed to add to batch: %v", err))
		}

		count++

		if count%batchSize == 0 {
			if err := batch.Write(); err != nil {
				logger.Fatal(fmt.Sprintf("Failed to write batch: %v", err))
			}
			batch.Reset()

			elapsed := time.Since(startTime)
			rate := float64(count) / elapsed.Seconds()
			logger.Info(fmt.Sprintf("Migrated %d entries (%.0f/sec, elapsed: %s)", count, rate, elapsed))
		}
	}

	// Write remaining
	if batch.Size() > 0 {
		if err := batch.Write(); err != nil {
			logger.Fatal(fmt.Sprintf("Failed to write final batch: %v", err))
		}
	}

	if err := iter.Error(); err != nil {
		logger.Fatal(fmt.Sprintf("Iterator error: %v", err))
	}

	target.Close()

	elapsed := time.Since(startTime)
	rate := float64(count) / elapsed.Seconds()
	logger.Info(fmt.Sprintf("Migration complete! %d entries in %s (%.0f/sec)", count, elapsed, rate))
}
