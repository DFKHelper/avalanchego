// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

// Package main provides a command-line tool for migrating databases to Firewood
//
// Usage:
//   migrate -source /path/to/leveldb -dest /path/to/firewood [options]
//
// Options:
//   -source string    Path to source database (required)
//   -dest string      Path to destination Firewood database (required)
//   -batch int        Batch size for migration (default: 10000)
//   -verify           Verify data after migration (default: false)
//   -estimate         Estimate migration time and exit (default: false)
//   -sample int       Sample size for estimation (default: 1000)
//
// Example:
//   # Migrate LevelDB to Firewood
//   migrate -source /data/leveldb -dest /data/firewood -verify
//
//   # Estimate migration time
//   migrate -source /data/leveldb -dest /data/firewood -estimate
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ava-labs/avalanchego/database/firewood"
	"github.com/ava-labs/avalanchego/database/leveldb"
	"github.com/ava-labs/avalanchego/utils/logging"
)

func main() {
	// Parse command-line flags
	var (
		sourcePath = flag.String("source", "", "Path to source database (required)")
		destPath   = flag.String("dest", "", "Path to destination Firewood database (required)")
		batchSize  = flag.Int("batch", 10000, "Batch size for migration")
		verify     = flag.Bool("verify", false, "Verify data after migration")
		estimate   = flag.Bool("estimate", false, "Estimate migration time and exit")
		sampleSize = flag.Int("sample", 1000, "Sample size for estimation")
	)
	flag.Parse()

	// Validate required flags
	if *sourcePath == "" {
		fmt.Fprintf(os.Stderr, "Error: -source is required\n")
		flag.Usage()
		os.Exit(1)
	}
	if *destPath == "" {
		fmt.Fprintf(os.Stderr, "Error: -dest is required\n")
		flag.Usage()
		os.Exit(1)
	}

	// Create logger
	logFactory := logging.NewFactory(logging.Config{
		DisplayLevel: logging.Info,
		LogLevel:     logging.Info,
	})
	log, err := logFactory.Make("migrate")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create logger: %v\n", err)
		os.Exit(1)
	}

	// Create context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Warn("received shutdown signal, cancelling migration")
		cancel()
	}()

	// Open source database (LevelDB)
	log.Info("opening source database", "path", *sourcePath)
	sourceDB, err := leveldb.New(*sourcePath, nil, log, nil)
	if err != nil {
		log.Fatal("failed to open source database", "error", err)
		os.Exit(1)
	}
	defer sourceDB.Close()

	// Estimate migration time if requested
	if *estimate {
		log.Info("estimating migration time", "sampleSize", *sampleSize)
		keys, bytes, duration, err := firewood.EstimateMigrationTime(ctx, sourceDB, *sampleSize, log)
		if err != nil {
			log.Fatal("estimation failed", "error", err)
			os.Exit(1)
		}

		fmt.Printf("\nMigration Estimate:\n")
		fmt.Printf("  Total Keys:     %d\n", keys)
		fmt.Printf("  Total Size:     %.2f GB\n", float64(bytes)/(1024*1024*1024))
		fmt.Printf("  Est. Duration:  %s\n", duration.Round(time.Second))
		fmt.Printf("  Est. Keys/sec:  %.0f\n", float64(keys)/duration.Seconds())
		fmt.Printf("\n")

		os.Exit(0)
	}

	// Open destination database (Firewood)
	// TODO: Uncomment when Firewood fork is ready
	log.Warn("Firewood migration tool is placeholder - awaiting fork with iterator support")
	log.Info("would open destination database", "path", *destPath)

	// destDB, err := firewood.New(*destPath, nil, log)
	// if err != nil {
	//     log.Fatal("failed to open destination database", "error", err)
	//     os.Exit(1)
	// }
	// defer destDB.Close()

	// Configure migration
	config := firewood.DefaultMigrationConfig()
	config.BatchSize = *batchSize
	config.VerifyAfterMigration = *verify

	log.Info("migration configuration",
		"batchSize", config.BatchSize,
		"verify", config.VerifyAfterMigration,
	)

	// Perform migration
	// TODO: Uncomment when Firewood fork is ready
	// stats, err := firewood.MigrateDatabase(ctx, sourceDB, destDB, config, log)
	// if err != nil {
	//     log.Fatal("migration failed", "error", err)
	//     os.Exit(1)
	// }

	// Print final statistics
	// duration := stats.EndTime.Sub(stats.StartTime)
	// fmt.Printf("\nMigration Complete:\n")
	// fmt.Printf("  Total Keys:     %d\n", stats.TotalKeys)
	// fmt.Printf("  Total Bytes:    %d (%.2f GB)\n", stats.TotalBytes, float64(stats.TotalBytes)/(1024*1024*1024))
	// fmt.Printf("  Duration:       %s\n", duration.Round(time.Second))
	// fmt.Printf("  Keys/sec:       %.0f\n", stats.KeysPerSecond())
	// fmt.Printf("  MB/sec:         %.2f\n", stats.BytesPerSecond()/(1024*1024))
	// fmt.Printf("  Errors:         %d\n", stats.ErrorCount)
	// fmt.Printf("\n")

	log.Info("migration tool placeholder execution complete")
}
