package netsec

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// MonoTime is a monotonically non-decreasing timestamp. Deployments should
// source it from a clock that cannot move backwards after a reboot (for
// example, a boot-relative clock persisted alongside a boot identifier).
type MonoTime int64

// Clock supplies MonoTime values to an Engine.
type Clock interface {
	Now() MonoTime
}

type systemClock struct{}

func (systemClock) Now() MonoTime { return MonoTime(time.Now().UnixNano()) }

// ManualClock is a deterministic clock for tests and simulations.
type ManualClock struct {
	mu  sync.Mutex
	now MonoTime
}

// NewManualClock creates a clock at start.
func NewManualClock(start MonoTime) *ManualClock {
	return &ManualClock{now: start}
}

// Now implements Clock.
func (c *ManualClock) Now() MonoTime {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d. Negative durations are ignored.
func (c *ManualClock) Advance(d time.Duration) MonoTime {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d > 0 {
		c.now += MonoTime(d)
	}
	return c.now
}

// MethodKind identifies a recovery channel category. The category is also the
// confirmation class used by high-risk two-factor rules.
type MethodKind string

const (
	MethodEmail           MethodKind = "Email"
	MethodDeviceKey       MethodKind = "DeviceKey"
	MethodRecoveryContact MethodKind = "RecoveryContact"
)

// Short aliases for callers that prefer them.
const (
	Email           = MethodEmail
	DeviceKey       = MethodDeviceKey
	RecoveryContact = MethodRecoveryContact
)

// Valid reports whether k is one of the supported recovery methods.
func (k MethodKind) Valid() bool {
	switch k {
	case MethodEmail, MethodDeviceKey, MethodRecoveryContact:
		return true
	default:
		return false
	}
}

// Status is the lifecycle state of a recovery-method entry.
type Status string

const (
	StatusPending Status = "Pending"
	StatusActive  Status = "Active"
	StatusRevoked Status = "Revoked"
)

// RiskLevel controls how many independent confirmations a change needs.
type RiskLevel string

const (
	RiskLow  RiskLevel = "Low"
	RiskHigh RiskLevel = "High"
)

// Action is the requested operation on the account's recovery configuration.
type Action string

const (
	ActionAdd     Action = "Add"
	ActionReplace Action = "Replace"
	ActionDelete  Action = "Delete"
)

func (a Action) valid() bool {
	switch a {
	case ActionAdd, ActionReplace, ActionDelete:
		return true
	default:
		return false
	}
}

// Entry is one version of a recovery method. Entry IDs are unique even when a
// replacement keeps the same public method ID.
type Entry struct {
	ID                string     `json:"id"`
	MethodID          string     `json:"methodID"`
	Kind              MethodKind `json:"kind"`
	MaterialDigest    string     `json:"materialDigest"`
	CreatedGeneration uint64     `json:"createdGeneration"`
	Status            Status     `json:"status"`
	EffectiveAt       MonoTime   `json:"effectiveAt"`
	NotBefore         MonoTime   `json:"notBefore,omitempty"`
	Risk              RiskLevel  `json:"risk"`
	Strong            bool       `json:"strong"`
}

// Clone makes a deep copy of an entry.
func (e Entry) Clone() Entry { return e }

// Target describes the recovery method named by a change request.
type Target struct {
	MethodID       string     `json:"methodID"`
	Kind           MethodKind `json:"kind"`
	MaterialDigest string     `json:"materialDigest"`
}

// ChangeRequest is the immutable signed object for adding, replacing, or
// deleting a recovery method.
type ChangeRequest struct {
	AccountID       string `json:"accountID"`
	ConfigDigest    string `json:"configDigest"`
	SessionID       string `json:"sessionID"`
	Action          Action `json:"action"`
	Target          Target `json:"target"`
	ReplaceMethodID string `json:"replaceMethodID,omitempty"`
	RequestID       string `json:"requestID"`
}

// Validate checks the self-contained fields of a request. Account-dependent
// checks are performed by Engine.Propose.
func (r ChangeRequest) Validate() error {
	switch {
	case strings.TrimSpace(r.AccountID) == "":
		return fmt.Errorf("%w: accountID is required", ErrInvalidRequest)
	case strings.TrimSpace(r.ConfigDigest) == "":
		return fmt.Errorf("%w: config digest is required", ErrInvalidRequest)
	case strings.TrimSpace(r.SessionID) == "":
		return fmt.Errorf("%w: session ID is required", ErrInvalidRequest)
	case !r.Action.valid():
		return fmt.Errorf("%w: unknown action %q", ErrInvalidRequest, r.Action)
	case strings.TrimSpace(r.RequestID) == "":
		return fmt.Errorf("%w: request ID is required", ErrInvalidRequest)
	case strings.TrimSpace(r.Target.MethodID) == "":
		return fmt.Errorf("%w: target methodID is required", ErrInvalidRequest)
	case !r.Target.Kind.Valid():
		return fmt.Errorf("%w: unsupported target method kind %q", ErrInvalidRequest, r.Target.Kind)
	}
	if r.Action != ActionDelete && strings.TrimSpace(r.Target.MaterialDigest) == "" {
		return fmt.Errorf("%w: target material digest is required", ErrInvalidRequest)
	}
	if r.Action == ActionReplace && strings.TrimSpace(r.ReplaceMethodID) == "" {
		return fmt.Errorf("%w: replaceMethodID is required", ErrInvalidRequest)
	}
	return nil
}

// Confirmation is one factor's signature over CanonicalRequest.
type Confirmation struct {
	MethodID  string `json:"methodID"`
	Signature []byte `json:"signature"`
}

// SignatureVerifier verifies a confirmation signature. message is the output
// of RequestDigest, so every signed confirmation covers all request fields.
type SignatureVerifier interface {
	Verify(accountID, methodID string, message, signature []byte) bool
}

// VerifyFunc adapts a function to SignatureVerifier.
type VerifyFunc func(accountID, methodID string, message, signature []byte) bool

// Verify implements SignatureVerifier.
func (f VerifyFunc) Verify(accountID, methodID string, message, signature []byte) bool {
	return f(accountID, methodID, message, signature)
}

// RejectAllVerifier rejects every confirmation. It is the safe default when no
// verifier is configured.
type RejectAllVerifier struct{}

// Verify implements SignatureVerifier.
func (RejectAllVerifier) Verify(string, string, []byte, []byte) bool { return false }

// HMACVerifier is a small symmetric verifier useful for tests and trusted
// single-process hosts. Production hosts should prefer asymmetric keys held by
// each factor.
type HMACVerifier struct {
	Secret []byte
}

// Sign produces a confirmation for req using methodID.
func (v HMACVerifier) Sign(req ChangeRequest, methodID string) (Confirmation, error) {
	if len(v.Secret) == 0 {
		return Confirmation{}, fmt.Errorf("%w: empty HMAC secret", ErrInvalidRequest)
	}
	digest := RequestDigest(req)
	mac := hmac.New(sha256.New, v.methodKey(req.AccountID, methodID))
	_, _ = mac.Write(digest[:])
	return Confirmation{MethodID: methodID, Signature: mac.Sum(nil)}, nil
}

// Verify implements SignatureVerifier.
func (v HMACVerifier) Verify(accountID, methodID string, message, signature []byte) bool {
	if len(v.Secret) == 0 {
		return false
	}
	mac := hmac.New(sha256.New, v.methodKey(accountID, methodID))
	_, _ = mac.Write(message)
	expected := mac.Sum(nil)
	return hmac.Equal(expected, signature)
}

func (v HMACVerifier) methodKey(accountID, methodID string) []byte {
	h := hmac.New(sha256.New, v.Secret)
	_, _ = h.Write([]byte("q0124-method-key\x00"))
	writeField(h, []byte(accountID))
	writeField(h, []byte(methodID))
	return h.Sum(nil)
}

// ConfirmationRecord is a verified confirmation retained with a pending
// change. EntryID pins the confirmation to the exact active factor version.
type ConfirmationRecord struct {
	MethodID  string     `json:"methodID"`
	EntryID   string     `json:"entryID"`
	Kind      MethodKind `json:"kind"`
	Signature []byte     `json:"signature"`
}

func (c ConfirmationRecord) clone() ConfirmationRecord {
	c.Signature = append([]byte(nil), c.Signature...)
	return c
}

// PendingChange is a confirmed change waiting for its delay to elapse.
type PendingChange struct {
	Request         ChangeRequest        `json:"request"`
	Entry           Entry                `json:"entry"`
	Risk            RiskLevel            `json:"risk"`
	Confirmations   []ConfirmationRecord `json:"confirmations"`
	SessionEpoch    uint64               `json:"sessionEpoch"`
	SessionMethodID string               `json:"sessionMethodID,omitempty"`
	CreatedAt       MonoTime             `json:"createdAt"`
	NotBefore       MonoTime             `json:"notBefore"`
}

func (p PendingChange) clone() PendingChange {
	p.Confirmations = cloneConfirmations(p.Confirmations)
	p.Entry = p.Entry.Clone()
	return p
}

// Policy controls delay, risk classification, and which method kinds count as
// strong factors. Any nil function falls back to the secure default.
type Policy struct {
	// Delay returns the mandatory delay between confirmation and activation.
	Delay func(ChangeRequest) time.Duration
	// Risk classifies the requested method. The default treats every built-in
	// recovery channel as high risk because each can recover an account.
	Risk func(ChangeRequest) RiskLevel
	// Strong reports whether a method kind is a strong factor. The default
	// treats all built-in recovery channels as strong.
	Strong func(MethodKind) bool
}

// DefaultPolicy is deliberately conservative: 24 hours, all built-in methods
// high risk and strong.
func DefaultPolicy() Policy {
	return Policy{
		Delay:  func(ChangeRequest) time.Duration { return 24 * time.Hour },
		Risk:   func(ChangeRequest) RiskLevel { return RiskHigh },
		Strong: func(MethodKind) bool { return true },
	}
}

func (p Policy) withDefaults() Policy {
	d := DefaultPolicy()
	if p.Delay == nil {
		p.Delay = d.Delay
	}
	if p.Risk == nil {
		p.Risk = d.Risk
	}
	if p.Strong == nil {
		p.Strong = d.Strong
	}
	return p
}

//-----------------------------------------------------------------------------
// Errors
//-----------------------------------------------------------------------------

var (
	ErrAccountNotFound          = errors.New("netsec: account not found")
	ErrAccountExists            = errors.New("netsec: account already exists")
	ErrRequestNotFound          = errors.New("netsec: change request not found")
	ErrRequestExists            = errors.New("netsec: change request already used")
	ErrInvalidRequest           = errors.New("netsec: invalid request")
	ErrConfigChanged            = errors.New("netsec: account configuration changed")
	ErrSessionRevoked           = errors.New("netsec: session revoked")
	ErrDelayNotElapsed          = errors.New("netsec: change delay has not elapsed")
	ErrInsufficientConfirmation = errors.New("netsec: insufficient confirmations")
	ErrPendingNotUsable         = errors.New("netsec: pending method cannot be used")
	ErrMethodNotActive          = errors.New("netsec: method is not active")
	ErrMethodExists             = errors.New("netsec: method already exists")
	ErrLastStrongFactor         = errors.New("netsec: refusing to remove the last strong factor")
	ErrStorage                  = errors.New("netsec: storage failure")
)

//-----------------------------------------------------------------------------
// Canonical encoding and digests
//-----------------------------------------------------------------------------

// CanonicalRequest returns an unambiguous byte encoding of every bound request
// field. Confirmations must sign exactly these bytes.
func CanonicalRequest(req ChangeRequest) []byte {
	var b strings.Builder
	b.WriteString("q0124/recovery-change/v1\x00")
	writeCanonicalString(&b, req.AccountID)
	writeCanonicalString(&b, req.ConfigDigest)
	writeCanonicalString(&b, req.SessionID)
	writeCanonicalString(&b, string(req.Action))
	writeCanonicalString(&b, req.Target.MethodID)
	writeCanonicalString(&b, string(req.Target.Kind))
	writeCanonicalString(&b, req.Target.MaterialDigest)
	writeCanonicalString(&b, req.ReplaceMethodID)
	writeCanonicalString(&b, req.RequestID)
	return []byte(b.String())
}

func writeCanonicalString(b *strings.Builder, s string) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(len(s)))
	b.Write(buf[:])
	b.WriteString(s)
}

// RequestDigest returns the SHA-256 digest that confirmations sign.
func RequestDigest(req ChangeRequest) [32]byte {
	return sha256.Sum256(CanonicalRequest(req))
}

// ConfigDigest computes the digest of the account's active recovery
// configuration. Pending and revoked entries do not participate.
func ConfigDigest(accountID string, entries []Entry) string {
	active := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.Status == StatusActive {
			active = append(active, e)
		}
	}
	sort.Slice(active, func(i, j int) bool {
		if active[i].MethodID != active[j].MethodID {
			return active[i].MethodID < active[j].MethodID
		}
		return active[i].ID < active[j].ID
	})

	h := sha256.New()
	writeField(h, []byte("q0124/config/v1"))
	writeField(h, []byte(accountID))
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(len(active)))
	_, _ = h.Write(buf[:])
	for _, e := range active {
		writeField(h, []byte(e.MethodID))
		writeField(h, []byte(e.Kind))
		writeField(h, []byte(e.MaterialDigest))
		binary.BigEndian.PutUint64(buf[:], e.CreatedGeneration)
		_, _ = h.Write(buf[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writeField(w interface{ Write([]byte) (int, error) }, value []byte) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(len(value)))
	_, _ = w.Write(buf[:])
	_, _ = w.Write(value)
}

// RandomRequestID returns a cryptographically random 128-bit request ID.
func RandomRequestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("netsec: generate request ID: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// RandomEntryID returns a cryptographically random entry version ID.
func RandomEntryID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("netsec: generate entry ID: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// ConstantTimeEqualString compares two strings without leaking their contents
// through timing. It is used for IDs on diagnostic paths.
func ConstantTimeEqualString(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
