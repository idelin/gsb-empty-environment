package migsim

import "crypto/sha256"

// BlockID identifies a logical shared block. Objects hold references to
// blocks; a block's physical placement (replica) changes across generations
// as it is migrated between volumes.
type BlockID string

// ObjectID identifies a referencing object (file, snapshot, ...).
type ObjectID string

// Digest is the content digest of a block generation.
type Digest [sha256.Size]byte

// DigestOf computes the content digest of b.
func DigestOf(b []byte) Digest { return Digest(sha256.Sum256(b)) }

// ReplicaRef locates one physical copy of a block generation.
type ReplicaRef struct {
	Volume string // Volume.ID
	Key    string // key within the volume
}

// Receipt records a committed generation switch for a block. Receipts are
// idempotent: replaying a stale receipt is a no-op and never switches back.
type Receipt struct {
	Block   BlockID
	FromGen int
	ToGen   int
	Digest  Digest
}

// EventKind enumerates the observable state transitions of a Store.
type EventKind int

const (
	EventBlockCreated EventKind = iota
	EventRefAdded
	EventRefRemoved
	EventBlockDeleted
	EventHandleOpened
	EventHandleClosed
	EventMigrationBegan
	EventMigrationCancelled
	EventMigrationFailed
	EventSwitched
	EventReplicaFreed
)

func (k EventKind) String() string {
	switch k {
	case EventBlockCreated:
		return "BlockCreated"
	case EventRefAdded:
		return "RefAdded"
	case EventRefRemoved:
		return "RefRemoved"
	case EventBlockDeleted:
		return "BlockDeleted"
	case EventHandleOpened:
		return "HandleOpened"
	case EventHandleClosed:
		return "HandleClosed"
	case EventMigrationBegan:
		return "MigrationBegan"
	case EventMigrationCancelled:
		return "MigrationCancelled"
	case EventMigrationFailed:
		return "MigrationFailed"
	case EventSwitched:
		return "Switched"
	case EventReplicaFreed:
		return "ReplicaFreed"
	}
	return "Unknown"
}

// Event is emitted by the Store for every state transition. Subscribers
// (e.g. the Ledger) are invoked synchronously while the store lock is held
// and must not call back into the Store.
type Event struct {
	Kind    EventKind
	Block   BlockID
	Object  ObjectID
	Gen     int
	Replica ReplicaRef
	Receipt Receipt
	Digest  Digest
}
