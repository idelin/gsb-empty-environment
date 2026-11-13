package netsec

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const (
	TicketIDSize    = 16
	ClaimDigestSize = sha256.Size
	BlindDigestSize = sha256.Size
	MinRedemptions  = 1
	MaxRedemptions  = 16
)

// TicketID is the 128-bit identifier of an authorization ticket.
type TicketID [TicketIDSize]byte

// ClaimDigest is a SHA-256-sized digest of the authorization claim.
type ClaimDigest [ClaimDigestSize]byte

// BlindDigest is an internal commitment to a canonical big-endian blind value.
type BlindDigest [BlindDigestSize]byte

func TicketIDFromBytes(value []byte) (TicketID, error) {
	var id TicketID
	if len(value) != TicketIDSize {
		return id, fmt.Errorf("%w: expected %d bytes, got %d", ErrInvalidTicketID, TicketIDSize, len(value))
	}
	copy(id[:], value)
	return id, nil
}

// ParseTicketID accepts a 16-byte value, a 32-character hexadecimal value,
// or a hexadecimal value prefixed with 0x.
func ParseTicketID(value string) (TicketID, error) {
	var id TicketID
	raw := strings.TrimPrefix(value, "0x")
	if len(raw) == TicketIDSize {
		copy(id[:], raw)
		return id, nil
	}
	if len(raw) != 2*TicketIDSize {
		return id, fmt.Errorf("%w: expected 16 raw bytes or 32 hexadecimal characters", ErrInvalidTicketID)
	}
	decoded, err := hex.DecodeString(raw)
	if err != nil {
		return id, fmt.Errorf("%w: %v", ErrInvalidTicketID, err)
	}
	copy(id[:], decoded)
	return id, nil
}

func (id TicketID) String() string {
	return hex.EncodeToString(id[:])
}

func (id TicketID) Bytes() []byte {
	return append([]byte(nil), id[:]...)
}

func (id TicketID) IsZero() bool {
	var zero TicketID
	return subtle.ConstantTimeCompare(id[:], zero[:]) == 1
}

func ClaimDigestFromBytes(value []byte) (ClaimDigest, error) {
	var digest ClaimDigest
	if len(value) != ClaimDigestSize {
		return digest, fmt.Errorf("%w: expected %d bytes, got %d", ErrInvalidClaimDigest, ClaimDigestSize, len(value))
	}
	copy(digest[:], value)
	return digest, nil
}

func ClaimDigestOf(value []byte) ClaimDigest {
	return ClaimDigest(sha256.Sum256(value))
}

func (digest ClaimDigest) Bytes() []byte {
	return append([]byte(nil), digest[:]...)
}

func (digest ClaimDigest) BigInt() *big.Int {
	return new(big.Int).SetBytes(digest[:])
}

// DigestBlind commits to the canonical minimal big-endian encoding of b.
func DigestBlind(blind *big.Int) BlindDigest {
	if blind == nil {
		return BlindDigest{}
	}
	return BlindDigest(sha256.Sum256(blind.Bytes()))
}

// BlindDigestEqual compares two blind-value commitments in constant time.
func BlindDigestEqual(a, b BlindDigest) bool {
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

// TicketPolicy is the authorization carried by a ticket and confirmed by the
// injected verifier.
type TicketPolicy struct {
	ExpiresAt      time.Time
	MaxRedemptions int
}

// RedemptionRequest is the complete client submission.
type RedemptionRequest struct {
	TicketID    TicketID
	ClaimDigest ClaimDigest
	Blind       *big.Int
	RequestID   string
}

// RedemptionResult is returned only after the signature has been committed.
type RedemptionResult struct {
	TicketID    TicketID
	ClaimDigest ClaimDigest
	RequestID   string
	Sequence    int
	BlindDigest BlindDigest
	Signature   *big.Int
	Reused      bool
}

// Reservation is the first atomic store commit. It intentionally contains no
// signature: the signature is committed separately after the private key
// operation completes.
type Reservation struct {
	TicketID       TicketID
	RequestID      string
	ClaimDigest    ClaimDigest
	BlindDigest    BlindDigest
	MaxRedemptions int
}

// Record is the durable state for one committed request ID.
type Record struct {
	TicketID    TicketID
	RequestID   string
	ClaimDigest ClaimDigest
	BlindDigest BlindDigest
	Sequence    int
	Signature   []byte
}

// ReserveResult reports the reservation selected by the first atomic commit.
type ReserveResult struct {
	Record  Record
	Created bool
}

// Completion is the second atomic store commit. The store must verify that it
// refers to the same reservation and blind-value commitment.
type Completion struct {
	TicketID    TicketID
	RequestID   string
	BlindDigest BlindDigest
	Signature   []byte
}

// RedemptionStore is the host's durable transaction boundary. Reserve and
// CommitSignature must each be atomic and are never treated as one transaction.
type RedemptionStore interface {
	Reserve(ctx context.Context, reservation Reservation) (ReserveResult, error)
	CommitSignature(ctx context.Context, completion Completion) error
	Lookup(ctx context.Context, ticketID TicketID, requestID string) (Record, bool, error)
}

// TicketVerifier is injected by the signing host. It must authenticate the
// ticket and confirm that it binds the supplied ticket ID and claim digest.
type TicketVerifier interface {
	VerifyTicket(ctx context.Context, ticketID TicketID, claimDigest ClaimDigest, now time.Time) (TicketPolicy, error)
}

// TicketVerifierFunc adapts a function to TicketVerifier.
type TicketVerifierFunc func(ctx context.Context, ticketID TicketID, claimDigest ClaimDigest, now time.Time) (TicketPolicy, error)

func (f TicketVerifierFunc) VerifyTicket(ctx context.Context, ticketID TicketID, claimDigest ClaimDigest, now time.Time) (TicketPolicy, error) {
	return f(ctx, ticketID, claimDigest, now)
}

type Config = IssuerConfig
type Request = RedemptionRequest
type Result = RedemptionResult
type Policy = TicketPolicy
