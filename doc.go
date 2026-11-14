// Package netsec implements source-chain event proofs for a Go cross-chain
// bridge.
//
// The package binds an event to its source chain, block, transaction, event
// index, and destination chain. It verifies validator quorum signatures, a
// light-client header chain, Merkle inclusion against the signed transaction
// and event roots, finality depth, and exactly-once message consumption.
//
// Security boundary:
//   - NewLightClient's trusted checkpoint and validator set must be obtained
//     out of band.
//   - Verify is side-effect free; Process atomically consumes the unique
//     message key through the configured Store.
//   - Events are released only from finalized canonical headers by default.
//     A reorganization that would replace a finalized header freezes the
//     light client instead of releasing again.
package netsec
