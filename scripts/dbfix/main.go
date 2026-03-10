package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"

	"github.com/cockroachdb/pebble"
)

// Keys from libevm/core/rawdb/schema.go
var (
	headBlockKey  = []byte("LastBlock")
	headHeaderKey = []byte("LastHeader")
)

var ethDBPrefix = []byte("ethdb")

func avalanchePrefix(prefix []byte) []byte {
	h := sha256.Sum256(prefix)
	return h[:]
}

func chainDBKey(logicalKey []byte) []byte {
	prefix := avalanchePrefix(ethDBPrefix)
	fullKey := make([]byte, len(prefix)+len(logicalKey))
	copy(fullKey, prefix)
	copy(fullKey[len(prefix):], logicalKey)
	return fullKey
}

func main() {
	dbPath := "/root/.avalanchego/chainData/q2aTwKuyzgs8pynF7UXBZCU7DejbZbZ6EUyHr3JQzYgwNPUPi/db/pebbledb"

	// Block 59,981,824 hash — the state sync target block
	targetHashHex := "32d1bd11dfa914ebf62f71b76324ae2d830a4655578e272b2631364cf7e442ea"
	targetHash, err := hex.DecodeString(targetHashHex)
	if err != nil {
		log.Fatalf("Invalid hash: %v", err)
	}

	db, err := pebble.Open(dbPath, &pebble.Options{})
	if err != nil {
		log.Fatalf("Failed to open PebbleDB: %v", err)
	}
	defer db.Close()

	// Read current head
	curHead, closer, err := db.Get(chainDBKey(headBlockKey))
	if err == nil {
		fmt.Printf("Current HeadBlock: %x\n", curHead)
		closer.Close()
	} else {
		fmt.Printf("HeadBlock key not found: %v\n", err)
	}

	if len(os.Args) > 1 && os.Args[1] == "--fix" {
		// Restore the block 59,981,824 hash as the chain head
		batch := db.NewBatch()
		if err := batch.Set(chainDBKey(headBlockKey), targetHash, nil); err != nil {
			log.Fatalf("Failed to set HeadBlock: %v", err)
		}
		if err := batch.Set(chainDBKey(headHeaderKey), targetHash, nil); err != nil {
			log.Fatalf("Failed to set HeadHeader: %v", err)
		}
		if err := batch.Commit(pebble.Sync); err != nil {
			log.Fatalf("Failed to commit: %v", err)
		}
		fmt.Printf("Fixed: HeadBlock and HeadHeader restored to block 59,981,824 (%s)\n", targetHashHex)

		verHead, closer2, err := db.Get(chainDBKey(headBlockKey))
		if err == nil {
			fmt.Printf("Verified: %x\n", verHead)
			closer2.Close()
		}
	} else {
		fmt.Printf("Target hash to restore: %s\n", targetHashHex)
		fmt.Println("Dry run. Pass --fix to apply.")
	}
}
