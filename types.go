package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// ChallengeIDBytes is the size of a challenge identifier.
const ChallengeIDBytes = 16

// AccountID identifies an authentication account.
type AccountID string

// BrowserSessionID identifies the browser-side login transaction holder.
type BrowserSessionID string

// DeviceID identifies a registered authenticator.
type DeviceID string

// TransactionID identifies a server-side login transaction.
type TransactionID string

// SessionID identifies an issued authenticated session.
type SessionID string

// ChallengeID is a 128-bit server-generated challenge identifier.
type ChallengeID [ChallengeIDBytes]byte

// NewChallengeID reads a challenge identifier from r.
func NewChallengeID(r io.Reader) (ChallengeID, error) {
	var id ChallengeID
	if _, err := io.ReadFull(r, id[:]); err != nil {
		return ChallengeID{}, err
	}
	return id, nil
}

// ParseChallengeID parses a 32-character hexadecimal challenge identifier.
func ParseChallengeID(s string) (ChallengeID, error) {
	var id ChallengeID
	raw, err := hex.DecodeString(s)
	if err != nil {
		return id, err
	}
	if len(raw) != ChallengeIDBytes {
		return id, fmt.Errorf("netsec: challenge id must be %d bytes", ChallengeIDBytes)
	}
	copy(id[:], raw)
	return id, nil
}

func (id ChallengeID) String() string { return hex.EncodeToString(id[:]) }

// IsZero reports whether the identifier is all zeroes.
func (id ChallengeID) IsZero() bool { return id == ChallengeID{} }

func (id ChallengeID) MarshalText() ([]byte, error) { return []byte(id.String()), nil }

func (id *ChallengeID) UnmarshalText(text []byte) error {
	parsed, err := ParseChallengeID(string(text))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// RequestDigest is the SHA-256 digest of the immutable login request.
type RequestDigest [sha256.Size]byte

// DigestRequest returns the SHA-256 digest of a login request.
func DigestRequest(request []byte) RequestDigest { return sha256.Sum256(request) }

func (d RequestDigest) String() string { return hex.EncodeToString(d[:]) }

func (d RequestDigest) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

func (d *RequestDigest) UnmarshalText(text []byte) error {
	raw, err := hex.DecodeString(string(text))
	if err != nil {
		return err
	}
	if len(raw) != sha256.Size {
		return fmt.Errorf("netsec: request digest must be %d bytes", sha256.Size)
	}
	copy(d[:], raw)
	return nil
}

// RiskLevel is the server-assessed risk attached to a login request.
type RiskLevel uint8

const (
	RiskLow RiskLevel = iota
	RiskMedium
	RiskHigh
	RiskCritical
)

func (r RiskLevel) valid() bool { return r <= RiskCritical }

func (r RiskLevel) String() string {
	switch r {
	case RiskLow:
		return "low"
	case RiskMedium:
		return "medium"
	case RiskHigh:
		return "high"
	case RiskCritical:
		return "critical"
	default:
		return fmt.Sprintf("risk(%d)", uint8(r))
	}
}

// MonoTime is a monotonic server instant in nanoseconds. It is deliberately
// not a wall-clock time: signatures and policy decisions use the same stable
// numeric value for the lifetime of a challenge.
type MonoTime int64

func (t MonoTime) Add(d time.Duration) MonoTime { return t + MonoTime(d) }
func (t MonoTime) Sub(u MonoTime) time.Duration { return time.Duration(t - u) }
func (t MonoTime) Before(u MonoTime) bool       { return t < u }
func (t MonoTime) After(u MonoTime) bool        { return t > u }
func (t MonoTime) IsZero() bool                 { return t == 0 }

func (t MonoTime) String() string {
	return fmt.Sprintf("mono(%dns)", int64(t))
}

// Clock supplies monotonic server time.
type Clock interface {
	Now() MonoTime
}

// ClockFunc adapts a function to Clock.
type ClockFunc func() MonoTime

func (f ClockFunc) Now() MonoTime { return f() }

// MonotonicClock is backed by time.Since, which uses the runtime monotonic
// clock on supported platforms.
type MonotonicClock struct {
	start time.Time
}

// NewMonotonicClock returns a clock whose zero is the current instant.
func NewMonotonicClock() *MonotonicClock {
	return &MonotonicClock{start: time.Now()}
}

func (c *MonotonicClock) Now() MonoTime { return MonoTime(time.Since(c.start)) }

// ManualClock is a deterministic clock intended for tests and the audit
// scheduler.
type ManualClock struct {
	mu  sync.Mutex
	now MonoTime
}

// NewManualClock returns a manual clock at now.
func NewManualClock(now MonoTime) *ManualClock { return &ManualClock{now: now} }

func (c *ManualClock) Now() MonoTime {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d.
func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now += MonoTime(d)
	c.mu.Unlock()
}

// Set changes the current instant.
func (c *ManualClock) Set(now MonoTime) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

// Clone returns an independent copy.
func (c *ManualClock) Clone() *ManualClock {
	c.mu.Lock()
	defer c.mu.Unlock()
	return &ManualClock{now: c.now}
}

func cloneClock(c Clock) Clock {
	switch v := c.(type) {
	case *ManualClock:
		return v.Clone()
	case nil:
		return ClockFunc(func() MonoTime { return 0 })
	default:
		now := v.Now()
		return ClockFunc(func() MonoTime { return now })
	}
}

// DisplayCode derives the three-digit code exactly as specified:
//
//	HMAC-SHA256(serviceKey, challengeID || requestDigest) mod 1000
//
// The HMAC output is interpreted as a big-endian 256-bit integer modulo 1000.
// The result is always zero-padded to three digits. Clients cannot supply or
// override this value.
func DisplayCode(serviceKey []byte, challengeID ChallengeID, digest RequestDigest) string {
	mac := hmac.New(sha256.New, serviceKey)
	_, _ = mac.Write(challengeID[:])
	_, _ = mac.Write(digest[:])
	sum := mac.Sum(nil)

	// Compute the full 256-bit integer modulo 1000 without big.Int.
	var n uint32
	for _, b := range sum {
		n = (n*256 + uint32(b)) % 1000
	}
	return fmt.Sprintf("%03d", n)
}

// Decision is a device vote.
type Decision string

const (
	DecisionNone    Decision = ""
	DecisionApprove Decision = "approve"
	DecisionDeny    Decision = "deny"
)

func (d Decision) valid() bool { return d == DecisionApprove || d == DecisionDeny }

func decisionTag(d Decision) (byte, error) {
	switch d {
	case DecisionApprove:
		return 1, nil
	case DecisionDeny:
		return 2, nil
	default:
		return 0, ErrInvalidDecision
	}
}

// ChallengeState is the server-side state of a challenge.
type ChallengeState string

const (
	// ChallengeSending is a reservation while the push provider is called.
	ChallengeSending ChallengeState = "sending"
	// ChallengePending is waiting for a device response.
	ChallengePending ChallengeState = "pending"
	// ChallengeApproved is approved but not yet consumed by its browser.
	ChallengeApproved ChallengeState = "approved"
	// ChallengeDenied is a terminal denial.
	ChallengeDenied ChallengeState = "denied"
	// ChallengeCancelled is an explicit cancellation.
	ChallengeCancelled ChallengeState = "cancelled"
	// ChallengeExpired is a timeout.
	ChallengeExpired ChallengeState = "expired"
	// ChallengeInvalidated covers password, risk, and device policy changes.
	ChallengeInvalidated ChallengeState = "invalidated"
	// ChallengeCompleted was consumed and is terminal.
	ChallengeCompleted ChallengeState = "completed"
	// ChallengeSendFailed means the push could not be delivered.
	ChallengeSendFailed ChallengeState = "send_failed"
)

// Active reports whether the challenge still participates in policy decisions.
func (s ChallengeState) Active() bool {
	return s == ChallengeSending || s == ChallengePending || s == ChallengeApproved
}

// Challenge is an immutable snapshot of a push challenge.
type Challenge struct {
	AccountID        AccountID        `json:"accountID"`
	BrowserSessionID BrowserSessionID `json:"browserSessionID"`
	DeviceID         DeviceID         `json:"deviceID"`
	ID               ChallengeID      `json:"challengeID"`
	DisplayCode      string           `json:"displayCode"`
	RequestDigest    RequestDigest    `json:"requestDigest"`
	Risk             RiskLevel        `json:"risk"`
	CreatedAt        MonoTime         `json:"createdAt"`
	ExpiresAt        MonoTime         `json:"expiresAt"`
	State            ChallengeState   `json:"state"`
	Decision         Decision         `json:"decision,omitempty"`
}

// ChallengeRequest is the only input accepted when creating a challenge.
// Notice that neither the challenge identifier nor the display code is present.
type ChallengeRequest struct {
	AccountID        AccountID
	BrowserSessionID BrowserSessionID
	DeviceID         DeviceID
	RequestDigest    RequestDigest
	Risk             RiskLevel
	// TTL is optional. Zero uses Config.ChallengeTTL.
	TTL time.Duration
}

// CreateChallengeRequest is an alias retained for callers that prefer the
// longer name.
type CreateChallengeRequest = ChallengeRequest

const responsePayloadDomain = "netsec/push-challenge-response/v1"

func appendString(buf *bytes.Buffer, s string) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(s)))
	_, _ = buf.Write(n[:])
	_, _ = buf.WriteString(s)
}

func responsePayload(
	account AccountID,
	browser BrowserSessionID,
	device DeviceID,
	id ChallengeID,
	code string,
	digest RequestDigest,
	risk RiskLevel,
	createdAt, expiresAt MonoTime,
	decision Decision,
) ([]byte, error) {
	tag, err := decisionTag(decision)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	appendString(&buf, responsePayloadDomain)
	appendString(&buf, string(account))
	appendString(&buf, string(browser))
	appendString(&buf, string(device))
	_, _ = buf.Write(id[:])
	appendString(&buf, code)
	_, _ = buf.Write(digest[:])
	_ = buf.WriteByte(byte(risk))
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(createdAt))
	_, _ = buf.Write(n[:])
	binary.BigEndian.PutUint64(n[:], uint64(expiresAt))
	_, _ = buf.Write(n[:])
	_ = buf.WriteByte(tag)
	return buf.Bytes(), nil
}

// SigningMessage returns the canonical bytes a device must sign to approve or
// deny this challenge. Every challenge binding field is covered.
func (c Challenge) SigningMessage(decision Decision, displayCode string) ([]byte, error) {
	return responsePayload(
		c.AccountID,
		c.BrowserSessionID,
		c.DeviceID,
		c.ID,
		displayCode,
		c.RequestDigest,
		c.Risk,
		c.CreatedAt,
		c.ExpiresAt,
		decision,
	)
}

// DeviceResponse is a signed device decision. The server looks up the
// challenge by ID, then requires every field to match before verifying the
// signature and applying the transition.
type DeviceResponse struct {
	AccountID        AccountID
	BrowserSessionID BrowserSessionID
	DeviceID         DeviceID
	ChallengeID      ChallengeID
	DisplayCode      string
	RequestDigest    RequestDigest
	Risk             RiskLevel
	CreatedAt        MonoTime
	ExpiresAt        MonoTime
	Decision         Decision
	Signature        []byte
}

// SigningMessage returns the canonical signed bytes for this response.
func (r DeviceResponse) SigningMessage() ([]byte, error) {
	return responsePayload(
		r.AccountID,
		r.BrowserSessionID,
		r.DeviceID,
		r.ChallengeID,
		r.DisplayCode,
		r.RequestDigest,
		r.Risk,
		r.CreatedAt,
		r.ExpiresAt,
		r.Decision,
	)
}

// SignResponse builds and signs a DeviceResponse for ch.
func SignResponse(privateKey ed25519.PrivateKey, ch Challenge, decision Decision, displayCode string) (DeviceResponse, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return DeviceResponse{}, ErrInvalidDeviceKey
	}
	resp := DeviceResponse{
		AccountID:        ch.AccountID,
		BrowserSessionID: ch.BrowserSessionID,
		DeviceID:         ch.DeviceID,
		ChallengeID:      ch.ID,
		DisplayCode:      displayCode,
		RequestDigest:    ch.RequestDigest,
		Risk:             ch.Risk,
		CreatedAt:        ch.CreatedAt,
		ExpiresAt:        ch.ExpiresAt,
		Decision:         decision,
	}
	msg, err := resp.SigningMessage()
	if err != nil {
		return DeviceResponse{}, err
	}
	resp.Signature = ed25519.Sign(privateKey, msg)
	return resp, nil
}

// SignApproval signs an approve response carrying the server-derived code.
func SignApproval(privateKey ed25519.PrivateKey, ch Challenge) (DeviceResponse, error) {
	return SignResponse(privateKey, ch, DecisionApprove, ch.DisplayCode)
}

// SignDenial signs a deny response.
func SignDenial(privateKey ed25519.PrivateKey, ch Challenge) (DeviceResponse, error) {
	return SignResponse(privateKey, ch, DecisionDeny, "")
}

// Device is a registered authenticator snapshot.
type Device struct {
	ID           DeviceID
	AccountID    AccountID
	PublicKey    ed25519.PublicKey
	Revoked      bool
	RegisteredAt MonoTime
	RevokedAt    MonoTime
}

// TransactionState is the state of a browser-held login transaction.
type TransactionState string

const (
	TransactionOpen        TransactionState = "open"
	TransactionConsumed    TransactionState = "consumed"
	TransactionInvalidated TransactionState = "invalidated"
	TransactionExpired     TransactionState = "expired"
)

// LoginTransaction binds a login attempt to exactly one browser session.
type LoginTransaction struct {
	ID               TransactionID
	AccountID        AccountID
	BrowserSessionID BrowserSessionID
	State            TransactionState
	ChallengeID      ChallengeID
	SessionID        SessionID
	CreatedAt        MonoTime
	ExpiresAt        MonoTime
	CompletedAt      MonoTime
}

// Session is issued only after a browser consumes its own approved challenge.
type Session struct {
	ID               SessionID
	Token            string
	AccountID        AccountID
	BrowserSessionID BrowserSessionID
	DeviceID         DeviceID
	ChallengeID      ChallengeID
	RequestDigest    RequestDigest
	Risk             RiskLevel
	IssuedAt         MonoTime
	ExpiresAt        MonoTime
}

// AccountStatus is an audit snapshot of server-side counters.
type AccountStatus struct {
	AccountID          AccountID
	PendingChallenge   *ChallengeID
	InFlightChallenge  *ChallengeID
	CooldownUntil      MonoTime
	HourlyWindowStart  MonoTime
	HourlyCreations    int
	ConsecutiveDenials int
	LockedUntil        MonoTime
	Risk               RiskLevel
	PasswordVersion    uint64
}

// Errors returned by the state machine. Callers should use errors.Is.
var (
	ErrInvalidConfig             = errors.New("netsec: invalid configuration")
	ErrServiceKeyRequired        = errors.New("netsec: service key is required")
	ErrInvalidRisk               = errors.New("netsec: invalid risk level")
	ErrInvalidDecision           = errors.New("netsec: invalid decision")
	ErrInvalidDeviceKey          = errors.New("netsec: invalid Ed25519 public or private key")
	ErrChallengeNotFound         = errors.New("netsec: challenge not found")
	ErrChallengeNotActive        = errors.New("netsec: challenge is not active")
	ErrChallengeNotPending       = errors.New("netsec: challenge is not pending")
	ErrChallengeNotApproved      = errors.New("netsec: challenge is not approved")
	ErrChallengeExpired          = errors.New("netsec: challenge expired")
	ErrChallengeCancelled        = errors.New("netsec: challenge was cancelled")
	ErrChallengeInvalidated      = errors.New("netsec: challenge was invalidated")
	ErrChallengeDenied           = errors.New("netsec: challenge was denied")
	ErrChallengeCompleted        = errors.New("netsec: challenge was already completed")
	ErrChallengeInFlight         = errors.New("netsec: another challenge is being sent")
	ErrPushSendFailed            = errors.New("netsec: push delivery failed")
	ErrPendingExists             = errors.New("netsec: account already has a pending challenge")
	ErrCooldown                  = errors.New("netsec: cancellation cooldown is active")
	ErrHourlyLimit               = errors.New("netsec: hourly challenge creation limit reached")
	ErrAccountLocked             = errors.New("netsec: account is locked after consecutive denials")
	ErrDeviceNotFound            = errors.New("netsec: device not found")
	ErrDeviceRevoked             = errors.New("netsec: device is revoked")
	ErrDeviceAccountMismatch     = errors.New("netsec: device does not belong to account")
	ErrDeviceAlreadyRegistered   = errors.New("netsec: device is already registered with a different key or account")
	ErrBindingMismatch           = errors.New("netsec: response does not match challenge binding")
	ErrInvalidSignature          = errors.New("netsec: invalid device signature")
	ErrDisplayCodeMismatch       = errors.New("netsec: display code mismatch")
	ErrTransactionNotFound       = errors.New("netsec: login transaction not found")
	ErrTransactionClosed         = errors.New("netsec: login transaction is not open")
	ErrTransactionExpired        = errors.New("netsec: login transaction expired")
	ErrBrowserMismatch           = errors.New("netsec: challenge belongs to a different browser session")
	ErrAccountMismatch           = errors.New("netsec: challenge belongs to a different account")
	ErrSessionNotFound           = errors.New("netsec: session not found")
	ErrSearchRequiresManualClock = errors.New("netsec: audit search requires a ManualClock")
	ErrDuplicateChallengeID      = errors.New("netsec: duplicate challenge id")
)

func stateError(state ChallengeState) error {
	switch state {
	case ChallengeExpired:
		return ErrChallengeExpired
	case ChallengeCancelled:
		return ErrChallengeCancelled
	case ChallengeInvalidated:
		return ErrChallengeInvalidated
	case ChallengeDenied:
		return ErrChallengeDenied
	case ChallengeCompleted:
		return ErrChallengeCompleted
	case ChallengePending, ChallengeApproved:
		return ErrChallengeNotPending
	default:
		return fmt.Errorf("%w: state=%s", ErrChallengeNotPending, state)
	}
}

func normalize(value string) string { return strings.TrimSpace(value) }
