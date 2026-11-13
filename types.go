package netsec

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

type (
	AccountID      string
	ContactID      string
	PersonID       string
	StrongFactorID string
	SessionID      string
	RecoveryID     string
)

const (
	DefaultCoolingPeriod  = 24 * time.Hour
	DefaultApprovalWindow = 24 * time.Hour
	recoveryEpochInitial  = uint64(1)
	recoveryIDBytes       = 32
	credentialIDBytes     = 32
)

var (
	ErrAccountNotFound     = errors.New("account not found")
	ErrAccountExists       = errors.New("account already exists")
	ErrRecoveryNotFound    = errors.New("recovery not found")
	ErrInvalidConfig       = errors.New("invalid recovery config")
	ErrInvalidRecoveryID   = errors.New("invalid recovery id")
	ErrInvalidContactID    = errors.New("invalid contact id")
	ErrInvalidAccountID    = errors.New("invalid account id")
	ErrVersionConflict     = errors.New("account version conflict")
	ErrRecoveryExpired     = errors.New("recovery deadline expired")
	ErrRecoveryNotActive   = errors.New("recovery is not active")
	ErrRecoveryVetoed      = errors.New("recovery was vetoed")
	ErrRecoveryInvalidated = errors.New("recovery was invalidated")
	ErrRecoveryCompleted   = errors.New("recovery already completed")
	ErrCoolingNotStarted   = errors.New("recovery has not entered cooling")
	ErrCoolingNotFinished  = errors.New("recovery cooling period has not finished")
	ErrThresholdNotMet     = errors.New("recovery threshold not met")
	ErrMinPersonsNotMet    = errors.New("minimum distinct person count not met")
	ErrInvalidSignature    = errors.New("invalid ed25519 signature")
	ErrContactNotFound     = errors.New("contact not found")
	ErrContactRevoked      = errors.New("contact is revoked")
	ErrContactOutOfEpoch   = errors.New("contact is not valid at recovery epoch")
	ErrStaleEpoch          = errors.New("approval is from a stale recovery epoch")
	ErrVetoNotAllowed      = errors.New("veto is not allowed")
	ErrFactorNotFound      = errors.New("strong factor not found")
	ErrFactorRevoked       = errors.New("strong factor is revoked")
	ErrNoDivergence        = errors.New("no approval-set divergence found")
	ErrSearchLimit         = errors.New("approval-set search limit exceeded")
	ErrCommitFailure       = errors.New("store commit failed")
)

// Digest is a 32-byte domain-separated operation or device digest.
type Digest [32]byte

func HashDigest(data []byte) Digest {
	return Digest(sha256.Sum256(data))
}

func DigestFromHex(s string) (Digest, error) {
	var d Digest
	if len(s) != hex.EncodedLen(len(d)) {
		return d, fmt.Errorf("digest must be 64 hex characters")
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return d, err
	}
	copy(d[:], raw)
	return d, nil
}

func (d Digest) Hex() string { return hex.EncodeToString(d[:]) }

func (d Digest) String() string { return d.Hex() }

// Fingerprint is the SHA-256 digest of a contact or strong-factor public key.
type Fingerprint [32]byte

func PublicKeyFingerprint(pub ed25519.PublicKey) Fingerprint {
	return Fingerprint(sha256.Sum256(pub))
}

func (f Fingerprint) Hex() string { return hex.EncodeToString(f[:]) }

func (f Fingerprint) String() string { return f.Hex() }

func (f Fingerprint) IsZero() bool {
	var zero Fingerprint
	return f == zero
}

// Contact is one recovery contact entry. StartEpoch is inclusive and EndEpoch
// is exclusive; an EndEpoch of zero means the entry has no scheduled end.
type Contact struct {
	ContactID   ContactID         `json:"contactID"`
	PersonID    PersonID          `json:"personID"`
	PublicKey   ed25519.PublicKey `json:"publicKey"`
	Fingerprint Fingerprint       `json:"fingerprint"`
	Weight      uint32            `json:"weight"`
	StartEpoch  uint64            `json:"startEpoch"`
	EndEpoch    uint64            `json:"endEpoch"`
	Revoked     bool              `json:"revoked"`
}

func (c Contact) ValidAt(epoch uint64) bool {
	if c.Revoked {
		return false
	}
	if c.StartEpoch != 0 && epoch < c.StartEpoch {
		return false
	}
	if c.EndEpoch != 0 && epoch >= c.EndEpoch {
		return false
	}
	return true
}

func (c Contact) clone() Contact {
	out := c
	if c.PublicKey != nil {
		out.PublicKey = append(ed25519.PublicKey(nil), c.PublicKey...)
	}
	return out
}

// Config is an immutable recovery configuration snapshot. Mutating operations
// on a Host create a new snapshot with RecoveryEpoch incremented by one.
type Config struct {
	AccountID          AccountID     `json:"accountID"`
	RecoveryEpoch      uint64        `json:"recoveryEpoch"`
	Contacts           []Contact     `json:"contacts"`
	Threshold          uint32        `json:"threshold"`
	MinDistinctPersons uint32        `json:"minDistinctPersons"`
	CoolingPeriod      time.Duration `json:"coolingPeriod"`
	ApprovalWindow     time.Duration `json:"approvalWindow"`
}

func (c Config) clone() Config {
	out := c
	out.Contacts = make([]Contact, len(c.Contacts))
	for i := range c.Contacts {
		out.Contacts[i] = c.Contacts[i].clone()
	}
	return out
}

func (c Config) Contact(id ContactID) (Contact, bool) {
	for _, contact := range c.Contacts {
		if contact.ContactID == id {
			return contact.clone(), true
		}
	}
	return Contact{}, false
}

// StrongFactor is an existing primary-account factor that may veto a recovery
// while the request is still in its cooling period.
type StrongFactor struct {
	ID        StrongFactorID    `json:"id"`
	PublicKey ed25519.PublicKey `json:"publicKey"`
	Revoked   bool              `json:"revoked"`
}

func (f StrongFactor) clone() StrongFactor {
	out := f
	if f.PublicKey != nil {
		out.PublicKey = append(ed25519.PublicKey(nil), f.PublicKey...)
	}
	return out
}

// Credential identifies the current primary credential. The package never
// persists raw credential material; ID is an opaque random 256-bit value.
type Credential struct {
	ID         string `json:"id"`
	Generation uint64 `json:"generation"`
}

func (c Credential) clone() Credential { return c }

// Approval is a contact's signed assertion about one recovery request. All
// request fields are retained so completion can re-check the binding.
type Approval struct {
	ContactID          ContactID   `json:"contactID"`
	ContactFingerprint Fingerprint `json:"contactFingerprint"`
	AccountID          AccountID   `json:"accountID"`
	RecoveryID         RecoveryID  `json:"recoveryID"`
	Epoch              uint64      `json:"epoch"`
	TargetDigest       Digest      `json:"targetDigest"`
	DeviceDigest       Digest      `json:"deviceDigest"`
	Deadline           time.Time   `json:"deadline"`
	Signature          []byte      `json:"signature"`
	ReceivedAt         time.Time   `json:"receivedAt"`
}

func (a Approval) clone() Approval {
	out := a
	if a.Signature != nil {
		out.Signature = append([]byte(nil), a.Signature...)
	}
	return out
}

type RecoveryStatus string

const (
	StatusPending     RecoveryStatus = "pending"
	StatusCooling     RecoveryStatus = "cooling"
	StatusCompleted   RecoveryStatus = "completed"
	StatusVetoed      RecoveryStatus = "vetoed"
	StatusInvalidated RecoveryStatus = "invalidated"
	StatusExpired     RecoveryStatus = "expired"
)

// Completion is the durable, idempotent result of a successful recovery.
type Completion struct {
	RecoveryID      RecoveryID  `json:"recoveryID"`
	CompletedAt     time.Time   `json:"completedAt"`
	NewCredential   Credential  `json:"newCredential"`
	RevokedSessions []SessionID `json:"revokedSessions"`
	NewEpoch        uint64      `json:"newEpoch"`
}

func (c Completion) clone() Completion {
	out := c
	out.NewCredential = c.NewCredential.clone()
	out.RevokedSessions = append([]SessionID(nil), c.RevokedSessions...)
	return out
}

// Recovery is the full request state, including every accepted approval.
type Recovery struct {
	ID                 RecoveryID             `json:"id"`
	AccountID          AccountID              `json:"accountID"`
	Epoch              uint64                 `json:"epoch"`
	TargetDigest       Digest                 `json:"targetDigest"`
	DeviceDigest       Digest                 `json:"deviceDigest"`
	CreatedAt          time.Time              `json:"createdAt"`
	Deadline           time.Time              `json:"deadline"`
	Approvals          map[ContactID]Approval `json:"approvals"`
	Status             RecoveryStatus         `json:"status"`
	CoolingStartedAt   time.Time              `json:"coolingStartedAt"`
	CoolingEndsAt      time.Time              `json:"coolingEndsAt"`
	VetoedBy           StrongFactorID         `json:"vetoedBy"`
	VetoedAt           time.Time              `json:"vetoedAt"`
	InvalidationReason string                 `json:"invalidationReason"`
	CompletedAt        time.Time              `json:"completedAt"`
	Completion         *Completion            `json:"completion,omitempty"`
}

func (r Recovery) clone() Recovery {
	out := r
	out.Approvals = make(map[ContactID]Approval, len(r.Approvals))
	for id, approval := range r.Approvals {
		out.Approvals[id] = approval.clone()
	}
	if r.Completion != nil {
		completion := r.Completion.clone()
		out.Completion = &completion
	}
	return out
}

func (r Recovery) EffectiveStatus(now time.Time) RecoveryStatus {
	switch r.Status {
	case StatusPending:
		if !now.Before(r.Deadline) {
			return StatusExpired
		}
	case StatusCooling:
		return StatusCooling
	}
	return r.Status
}

// AccountState is the atomically persisted state of one identity host account.
type AccountState struct {
	Version        uint64                  `json:"version"`
	Config         Config                  `json:"config"`
	Credential     Credential              `json:"credential"`
	Sessions       map[SessionID]struct{}  `json:"sessions"`
	StrongFactors  []StrongFactor          `json:"strongFactors"`
	Recoveries     map[RecoveryID]Recovery `json:"recoveries"`
	ActiveRecovery RecoveryID              `json:"activeRecovery"`
}

func (s AccountState) Clone() AccountState {
	out := s
	out.Config = s.Config.clone()
	out.Credential = s.Credential.clone()
	out.Sessions = make(map[SessionID]struct{}, len(s.Sessions))
	for id := range s.Sessions {
		out.Sessions[id] = struct{}{}
	}
	out.StrongFactors = make([]StrongFactor, len(s.StrongFactors))
	for i := range s.StrongFactors {
		out.StrongFactors[i] = s.StrongFactors[i].clone()
	}
	out.Recoveries = make(map[RecoveryID]Recovery, len(s.Recoveries))
	for id, recovery := range s.Recoveries {
		out.Recoveries[id] = recovery.clone()
	}
	return out
}

// Account is the initial durable state passed to CreateAccount.
type Account struct {
	ID            AccountID
	Config        Config
	Credential    Credential
	StrongFactors []StrongFactor
	Sessions      []SessionID
}
