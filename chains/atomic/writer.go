// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package atomic

import "github.com/ava-labs/avalanchego/database"

// WriteAll writes baseBatch and each additional batch to their respective
// underlying databases. Each batch is written independently to ensure data
// goes to the correct database when per-chain databases are enabled.
//
// Previously, all batches were replayed onto baseBatch and written as one
// atomic operation. This assumed all batches shared the same underlying
// database. With per-chain databases (e.g., Firewood), VM batches target a
// different database than shared memory, so replaying them onto baseBatch
// would write VM data to the wrong database.
func WriteAll(baseBatch database.Batch, batches ...database.Batch) error {
	// Write the base batch (e.g., shared memory operations) to its database.
	if err := baseBatch.Inner().Write(); err != nil {
		return err
	}
	// Write each additional batch to its own underlying database.
	for _, batch := range batches {
		if err := batch.Inner().Write(); err != nil {
			return err
		}
	}
	return nil
}
