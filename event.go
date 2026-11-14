package netsec

import (
	"fmt"
)

// EventKind classifies a source-chain bridge event.
type EventKind string

const (
	// EventLock locks source-chain assets and authorizes a target-chain release.
	EventLock EventKind = "lock"
	// EventBurn burns source-chain assets as part of a burn-and-mint bridge.
	EventBurn EventKind = "burn"
)

const (
	maxAssetIDLength  = 128
	maxAddressLength  = 256
	maxBridgeIDLength = 128
	maxEventKindBytes = 32
)

// Event is the authenticated source-chain action that a proof commits to.
// TxIndex is the transaction's position in the block; EventIndex is the
// event/log position in the block's event list. Every field is hashed into the
// event leaf, so changing any of them invalidates the event-inclusion proof.
type Event struct {
	SourceChain      ChainID   `json:"source_chain"`
	DestinationChain ChainID   `json:"destination_chain"`
	BridgeID         string    `json:"bridge_id,omitempty"`
	BlockHeight      uint64    `json:"block_height"`
	TxHash           Hash      `json:"tx_hash"`
	TxIndex          uint32    `json:"tx_index"`
	EventIndex       uint32    `json:"event_index"`
	Kind             EventKind `json:"kind"`
	Sender           []byte    `json:"sender"`
	AssetID          string    `json:"asset_id"`
	Amount           uint64    `json:"amount"`
	Recipient        []byte    `json:"recipient"`
	Nonce            uint64    `json:"nonce"`
}

// Validate checks event invariants. It deliberately accepts both lock and
// burn events; the verifier's policy decides which kinds may release assets.
func (e Event) Validate() error {
	if err := e.SourceChain.Validate(); err != nil {
		return fmt.Errorf("%w: source chain: %v", ErrInvalidEvent, err)
	}
	if err := e.DestinationChain.Validate(); err != nil {
		return fmt.Errorf("%w: destination chain: %v", ErrInvalidEvent, err)
	}
	if e.SourceChain == e.DestinationChain {
		return fmt.Errorf("%w: source and destination chains are identical", ErrInvalidEvent)
	}
	if len(e.BridgeID) > maxBridgeIDLength {
		return fmt.Errorf("%w: bridge id exceeds %d bytes", ErrInvalidEvent, maxBridgeIDLength)
	}
	if e.TxHash.IsZero() {
		return fmt.Errorf("%w: zero transaction hash", ErrInvalidEvent)
	}
	if len(e.Kind) == 0 || len(e.Kind) > maxEventKindBytes {
		return fmt.Errorf("%w: invalid event kind", ErrInvalidEvent)
	}
	if e.Kind != EventLock && e.Kind != EventBurn {
		return fmt.Errorf("%w: %q", ErrUnknownEventKind, e.Kind)
	}
	if len(e.Sender) == 0 || len(e.Sender) > maxAddressLength {
		return fmt.Errorf("%w: sender length must be in [1,%d]", ErrInvalidEvent, maxAddressLength)
	}
	if len(e.Recipient) == 0 || len(e.Recipient) > maxAddressLength {
		return fmt.Errorf("%w: recipient length must be in [1,%d]", ErrInvalidEvent, maxAddressLength)
	}
	if len(e.AssetID) == 0 || len(e.AssetID) > maxAssetIDLength {
		return fmt.Errorf("%w: asset id length must be in [1,%d]", ErrInvalidEvent, maxAssetIDLength)
	}
	if e.Amount == 0 {
		return fmt.Errorf("%w: zero amount", ErrInvalidEvent)
	}
	return nil
}

// LeafHash computes the domain-separated event leaf. Call Validate first when
// handling untrusted input.
func (e Event) LeafHash() Hash {
	return hashDomainSeparated("event-leaf/v1",
		[]byte(e.SourceChain),
		[]byte(e.DestinationChain),
		[]byte(e.BridgeID),
		uint64Bytes(e.BlockHeight),
		e.TxHash[:],
		uint32Bytes(e.TxIndex),
		uint32Bytes(e.EventIndex),
		[]byte(e.Kind),
		e.Sender,
		[]byte(e.AssetID),
		uint64Bytes(e.Amount),
		e.Recipient,
		uint64Bytes(e.Nonce),
	)
}

func uint32Bytes(v uint32) []byte {
	return []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
}

// MessageKey is the stable, cross-chain-bound idempotency key.
//
// It intentionally excludes block height and block hash: a source-chain
// reorganization that re-includes the same transaction must not create a new
// release. It includes the destination chain so a proof cannot be replayed
// against another destination even if a caller shares one store between
// verifiers.
func MessageKey(source, destination ChainID, txHash Hash, eventIndex uint32) Hash {
	return hashDomainSeparated("message-key/v1",
		[]byte(source),
		[]byte(destination),
		txHash[:],
		uint32Bytes(eventIndex),
	)
}

// MessageKey returns the event's idempotency key.
func (e Event) MessageKey() Hash {
	return MessageKey(e.SourceChain, e.DestinationChain, e.TxHash, e.EventIndex)
}

// String renders a concise, non-consensus event description.
func (e Event) String() string {
	return fmt.Sprintf("%s:%s/%d/%d/%d/%s/%d", e.SourceChain, e.DestinationChain,
		e.BlockHeight, e.TxIndex, e.EventIndex, e.AssetID, e.Amount)
}
