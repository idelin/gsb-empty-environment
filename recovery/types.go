// Package recovery implements an embedded, crash-safe, encrypted index
// recovery protocol.
//
// The package deliberately separates the storage host from the protocol
// engine. The host only sees fixed-size ciphertext frames and a small public
// I/O event containing the region, slot, length, phase, attempt, and retry
// number. Plaintext blocks and tenant keys never cross the StableStore
// boundary.
package recovery

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// Region identifies one of the two fixed-capacity stable log regions.
type Region uint8

const (
	RegionA Region = 0
	RegionB Region = 1
)

func (r Region) String() string {
	if r == RegionA {
		return "A"
	}
	if r == RegionB {
		return "B"
	}
	return fmt.Sprintf("region(%d)", uint8(r))
}

// TenantID is an opaque tenant identifier. Lanes are keyed by this value and
// every authenticated frame is bound to it.
type TenantID [16]byte

// TxID is an opaque transaction identifier.
type TxID [16]byte

// LeaseID distinguishes successive grants with the same numeric epoch.
type LeaseID [16]byte

// BlockID is the logical index key.
type BlockID [16]byte

// Hash is a SHA-256 digest.
type Hash [32]byte

func idFromString(s string) [16]byte {
	sum := sha256.Sum256([]byte(s))
	var out [16]byte
	copy(out[:], sum[:16])
	return out
}

func TenantIDFromString(s string) TenantID { return TenantID(idFromString(s)) }
func TxIDFromString(s string) TxID         { return TxID(idFromString(s)) }
func LeaseIDFromString(s string) LeaseID   { return LeaseID(idFromString(s)) }
func BlockIDFromString(s string) BlockID   { return BlockID(idFromString(s)) }

func (id TenantID) String() string { return hex.EncodeToString(id[:]) }
func (id TxID) String() string     { return hex.EncodeToString(id[:]) }
func (id LeaseID) String() string  { return hex.EncodeToString(id[:]) }
func (id BlockID) String() string  { return hex.EncodeToString(id[:]) }
func (h Hash) String() string      { return hex.EncodeToString(h[:]) }

// Lease is the externally supplied fencing epoch for one tenant. A lease is
// not a capability; the host must supply the current lease to recovery. The
// exact (tenant, epoch, id) tuple is authenticated in every commit.
type Lease struct {
	Tenant TenantID
	Epoch  uint64
	ID     LeaseID
}

func (l Lease) validate(tenant TenantID) error {
	if l.Tenant != tenant {
		return fmt.Errorf("%w: lease tenant mismatch", ErrLease)
	}
	if l.ID == (LeaseID{}) {
		return fmt.Errorf("%w: zero lease id", ErrLease)
	}
	return nil
}

// Block is one versioned index block. Tenant is repeated in the block so a
// verifier can reject a result that contains a foreign tenant even if block
// identifiers alias.
type Block struct {
	Tenant  TenantID
	ID      BlockID
	Version uint64
	Data    []byte
}

// SignedTxn is both the optimistic-concurrency token and the authorization
// token for an unacknowledged prepared batch. Signature is an HMAC-SHA-256
// over the canonical transaction fields using the tenant's signing key.
type SignedTxn struct {
	ID             TxID
	Tenant         TenantID
	LeaseEpoch     uint64
	LeaseID        LeaseID
	BaseGeneration uint64
	Nonce          [16]byte
	Signature      [32]byte
}

func (t SignedTxn) lease() Lease {
	return Lease{Tenant: t.Tenant, Epoch: t.LeaseEpoch, ID: t.LeaseID}
}

func (t SignedTxn) validate() error {
	if t.ID == (TxID{}) {
		return fmt.Errorf("%w: zero transaction id", ErrTransaction)
	}
	if t.Nonce == ([16]byte{}) {
		return fmt.Errorf("%w: zero transaction nonce", ErrTransaction)
	}
	if t.LeaseID == (LeaseID{}) {
		return fmt.Errorf("%w: zero lease id", ErrTransaction)
	}
	return nil
}

// Snapshot is a complete replacement snapshot for one tenant.
type Snapshot struct {
	Tenant     TenantID
	Lease      Lease
	Generation uint64
	Txn        TxID
	Blocks     []Block
}

// CommitReceipt is the public, non-secret description of authenticated commit
// evidence. Its fields are copied from the signed receipt frame.
type CommitReceipt struct {
	Tenant       TenantID
	Lease        Lease
	Generation   uint64
	Txn          TxID
	BlockCount   int
	Deleted      bool
	SnapshotHash Hash
	Region       Region
	FirstSlot    int
	FrameCount   int
}

// RecoveryRequest fences recovery by tenant, exact lease, and an optional
// lower bound on generation. MinGeneration is the host's monotonic rollback
// anchor for a lease; zero disables the check.
type RecoveryRequest struct {
	Tenant        TenantID
	Lease         Lease
	MinGeneration uint64
}

// ResolveState describes what Resolve found or completed.
type ResolveState uint8

const (
	// ResolveRolledBack means no complete signed decision existed. The partial
	// batch is invisible and may be overwritten by a future transaction.
	ResolveRolledBack ResolveState = iota
	// ResolveCommitted means authenticated commit evidence (a receipt) already
	// existed.
	ResolveCommitted
	// ResolveRetrieved means a complete signed decision existed without a
	// receipt, and Resolve durably completed the same signed transaction.
	ResolveRetrieved
)

// ResolveResult is returned by Resolve.
type ResolveResult struct {
	State    ResolveState
	Snapshot *Snapshot
	Receipt  *CommitReceipt
}

// KeyPair contains independent encryption and signing keys for one tenant.
type KeyPair struct {
	Encryption [32]byte
	Signing    [32]byte
}

// NewKeyPair derives a domain-separated key pair from master material.
func NewKeyPair(master []byte, tenant TenantID) KeyPair {
	var pair KeyPair
	mac := hmac.New(sha256.New, master)
	mac.Write([]byte("recovery/encryption/v1\x00"))
	mac.Write(tenant[:])
	copy(pair.Encryption[:], mac.Sum(nil))

	mac = hmac.New(sha256.New, master)
	mac.Write([]byte("recovery/signing/v1\x00"))
	mac.Write(tenant[:])
	copy(pair.Signing[:], mac.Sum(nil))
	return pair
}

// Keyring maps tenants to their independent key material.
type Keyring map[TenantID]KeyPair

func (k Keyring) pair(tenant TenantID) (KeyPair, bool) {
	pair, ok := k[tenant]
	return pair, ok
}

// SignTransaction signs the canonical transaction fields and returns a copy
// with Signature populated.
func (k Keyring) SignTransaction(txn SignedTxn) (SignedTxn, error) {
	pair, ok := k.pair(txn.Tenant)
	if !ok {
		return SignedTxn{}, fmt.Errorf("%w: no key for tenant %s", ErrKey, txn.Tenant)
	}
	txn.Signature = signBytes(pair.Signing, txnCanonical(txn))
	return txn, nil
}

// VerifyTransaction verifies a signed transaction without retaining any
// engine state.
func (k Keyring) VerifyTransaction(txn SignedTxn) bool {
	pair, ok := k.pair(txn.Tenant)
	if !ok {
		return false
	}
	want := signBytes(pair.Signing, txnCanonical(txn))
	return hmac.Equal(want[:], txn.Signature[:])
}

// LaneSpec gives one tenant a fixed, non-overlapping slot range in both
// regions. Capacity is therefore bounded and independent of other tenants.
type LaneSpec struct {
	Tenant TenantID
	Start  int
	Slots  int
}

// Layout partitions both regions into fixed tenant lanes.
type Layout struct {
	RegionSlots int
	Lanes       []LaneSpec
}

// SingleTenantLayout is a convenience for one tenant lane starting at slot 0.
func SingleTenantLayout(tenant TenantID, slots int) Layout {
	return Layout{
		RegionSlots: slots,
		Lanes:       []LaneSpec{{Tenant: tenant, Slots: slots}},
	}
}

func (l Layout) lane(tenant TenantID) (LaneSpec, error) {
	for _, lane := range l.Lanes {
		if lane.Tenant == tenant {
			return lane, nil
		}
	}
	return LaneSpec{}, fmt.Errorf("%w: tenant %s", ErrUnknownTenant, tenant)
}

func (l Layout) validate(frameSize int) error {
	if frameSize < minFrameSize {
		return fmt.Errorf("%w: frame size %d < %d", ErrConfig, frameSize, minFrameSize)
	}
	if l.RegionSlots <= 0 {
		return fmt.Errorf("%w: RegionSlots must be positive", ErrConfig)
	}
	used := make([]bool, l.RegionSlots)
	seen := make(map[TenantID]bool)
	for _, lane := range l.Lanes {
		if lane.Tenant == (TenantID{}) {
			return fmt.Errorf("%w: zero tenant in lane", ErrConfig)
		}
		if seen[lane.Tenant] {
			return fmt.Errorf("%w: duplicate lane for tenant %s", ErrConfig, lane.Tenant)
		}
		seen[lane.Tenant] = true
		if lane.Slots < minLaneSlots {
			return fmt.Errorf("%w: lane %s has %d slots, need at least %d", ErrConfig, lane.Tenant, lane.Slots, minLaneSlots)
		}
		if lane.Start < 0 || lane.Slots < 0 || lane.Start+lane.Slots > l.RegionSlots {
			return fmt.Errorf("%w: lane %s [%d,%d) outside region", ErrConfig, lane.Tenant, lane.Start, lane.Start+lane.Slots)
		}
		for i := lane.Start; i < lane.Start+lane.Slots; i++ {
			if used[i] {
				return fmt.Errorf("%w: overlapping lane at slot %d", ErrConfig, i)
			}
			used[i] = true
		}
	}
	if len(l.Lanes) == 0 {
		return fmt.Errorf("%w: no lanes", ErrConfig)
	}
	return nil
}

// Config controls protocol framing and bounded retries.
type Config struct {
	Layout     Layout
	FrameSize  int
	MaxRetries int
	Rand       io.Reader
}

const (
	defaultFrameSize  = 2048
	defaultMaxRetries = 2
	minFrameSize      = 512
	minLaneSlots      = 4
)

// Phase is a public protocol phase. It intentionally carries no plaintext.
type Phase string

const (
	PhaseBegin       Phase = "begin"
	PhaseStage       Phase = "stage"
	PhasePrepare     Phase = "prepare"
	PhaseDecision    Phase = "decision"
	PhaseAck         Phase = "ack"
	PhaseRecoverScan Phase = "recover-scan"
	PhaseResolveScan Phase = "resolve-scan"
	PhaseResolveAck  Phase = "resolve-ack"
)

// Op is a public storage operation.
type Op string

const (
	OpRead  Op = "read"
	OpWrite Op = "write"
)

// Event is the complete information visible to the storage host for one I/O
// attempt. Data is deliberately absent.
type Event struct {
	Op      Op
	Region  Region
	Slot    int
	Length  int
	Phase   Phase
	Attempt int
	Retry   int
	Failed  bool
}

// Observer receives public I/O events synchronously while the engine lock is
// held and must not call back into the same Engine.
type Observer interface {
	Observe(Event)
}

// ObserverFunc adapts a function to Observer.
type ObserverFunc func(Event)

func (f ObserverFunc) Observe(ev Event) { f(ev) }

// RecoveryPlan is a bounded description of one tenant's recovery scan.
type RecoveryPlan struct {
	Tenant          TenantID
	LaneStart       int
	LaneSlots       int
	FrameSize       int
	Regions         int
	MaxReads        int
	MaxReadAttempts int
	MaxWrites       int
	MaxRetries      int
	MaxBytes        int
}

// Errors are stable enough for callers to classify failures.
var (
	ErrConfig        = errors.New("recovery: invalid configuration")
	ErrUnknownTenant = errors.New("recovery: unknown tenant")
	ErrKey           = errors.New("recovery: missing key")
	ErrLease         = errors.New("recovery: invalid lease")
	ErrTransaction   = errors.New("recovery: invalid transaction")
	ErrSignature     = errors.New("recovery: invalid signature")
	ErrConflict      = errors.New("recovery: generation conflict")
	ErrCapacity      = errors.New("recovery: lane capacity exhausted")
	ErrBlockTooLarge = errors.New("recovery: block does not fit a frame")
	ErrBlockTenant   = errors.New("recovery: block tenant mismatch")
	ErrDuplicateID   = errors.New("recovery: duplicate block id")
	ErrBatchDone     = errors.New("recovery: batch already committed or aborted")
	ErrTxnMismatch   = errors.New("recovery: transaction token mismatch")
	ErrAmbiguous     = errors.New("recovery: ambiguous authenticated commits")
	ErrIO            = errors.New("recovery: storage i/o failure")
	ErrGeometry      = errors.New("recovery: store geometry mismatch")
)
