package netsec

import (
	"context"
	"crypto/rand"
	"io"
	"sync"
	"time"
)

// Audit event types emitted by the unseal state machine.
const (
	EventInitialized   = "initialized"
	EventRotated       = "rotated"
	EventShareAccepted = "share_accepted"
	EventShareRejected = "share_rejected"
	EventUnsealed      = "unsealed"
	EventImported      = "imported"
)

const (
	phaseUninitialized = "uninitialized"
	phaseSealed        = "sealed"
	phaseUnsealed      = "unsealed"

	snapshotVersion    = 1
	nonceLength        = 32
	unsealKeyLength    = 32
	minHolderKeyLength = 16
	maxHolders         = 255

	sealDigestVersion  = "netsec/seal-config/v1"
	shareBindingDomain = "netsec/unseal-share/v1"
	snapshotDomain     = "netsec/unseal-snapshot/v1"
)

// Holder identifies one threshold-share holder and carries the symmetric key
// used to authenticate that holder's shares.
type Holder struct {
	ID  string `json:"id"`
	Key []byte `json:"key"`
}

// SealConfig describes one sealing: the seal identifier, the threshold number
// of shares required to unseal, and the authorized holder set.
type SealConfig struct {
	SealID    string   `json:"seal_id"`
	Threshold int      `json:"threshold"`
	Holders   []Holder `json:"holders"`
}

// Share is one holder's unseal share. Every field participates in the
// authenticated binding so a share cannot be replayed into another
// generation, seal configuration, nonce or holder context.
type Share struct {
	Generation uint64 `json:"generation"`
	SealID     string `json:"seal_id"`
	Nonce      []byte `json:"nonce"`
	HolderID   string `json:"holder_id"`
	Index      int    `json:"index"`
	Fragment   []byte `json:"fragment"`
	Tag        []byte `json:"tag"`
}

// SealedState is returned when a sealing is created or rotated. The shares map
// is keyed by holder ID and is intended to be distributed out of band.
type SealedState struct {
	Generation uint64           `json:"generation"`
	Seal       SealConfig       `json:"seal"`
	Nonce      []byte           `json:"nonce"`
	Shares     map[string]Share `json:"shares"`
}

// AuditEvent records a state machine transition or a rejected submission.
type AuditEvent struct {
	Seq        uint64    `json:"seq"`
	Type       string    `json:"type"`
	Generation uint64    `json:"generation"`
	SealID     string    `json:"seal_id"`
	HolderID   string    `json:"holder_id"`
	Phase      string    `json:"phase"`
	ShareCount int       `json:"share_count"`
	Threshold  int       `json:"threshold"`
	Time       time.Time `json:"time"`
	Cause      string    `json:"cause,omitempty"`
}

// AuditSink persists audit events. Implementations must be safe for
// concurrent use.
type AuditSink interface {
	Record(ctx context.Context, event AuditEvent) error
}

// Notifier is informed of every audit event, allowing the key service to push
// unseal lifecycle alerts. Implementations must be safe for concurrent use.
type Notifier interface {
	Notify(ctx context.Context, event AuditEvent) error
}

// StateConfig configures an unseal State.
type StateConfig struct {
	// StateKey authenticates exported snapshots so restarted or replaced
	// instances cannot be tricked into importing rolled-back state.
	StateKey  []byte
	AuditSink AuditSink
	Notifier  Notifier
	// Rand is used for nonces, secrets and polynomials. Defaults to
	// crypto/rand.Reader and is mainly a testing hook.
	Rand io.Reader
}

// State is the importable unseal state machine used by the key service. It is
// safe for concurrent use.
type State struct {
	mu sync.Mutex

	stateKey []byte
	sink     AuditSink
	notifier Notifier
	rng      io.Reader

	phase      string
	generation uint64
	// maxObservedGeneration is the high-water mark that rejects rollback
	// attempts, including snapshot imports after a restart.
	maxObservedGeneration uint64

	seal          SealConfig
	sealDigest    []byte
	nonce         []byte
	keyLen        int
	keyCommitment []byte

	holderIndex map[string]int
	holderKeys  map[string][]byte

	received   map[int]sharePart
	holderSeen map[string]bool

	unsealKey []byte

	auditSeq uint64
	events   []AuditEvent
}

// NewState creates an uninitialized unseal state machine.
func NewState(cfg StateConfig) *State {
	rng := cfg.Rand
	if rng == nil {
		rng = rand.Reader
	}
	return &State{
		stateKey: append([]byte(nil), cfg.StateKey...),
		sink:     cfg.AuditSink,
		notifier: cfg.Notifier,
		rng:      rng,
		phase:    phaseUninitialized,
	}
}
