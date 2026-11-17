package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

// Role identifies which side of a replacement confirmed a change.
type Role int

const (
	// RoleOld is the currently active recovery channel.
	RoleOld Role = iota + 1
	// RoleNew is the channel that wants to become the recovery channel.
	RoleNew
)

func (r Role) valid() bool {
	return r == RoleOld || r == RoleNew
}

func (r Role) byte() byte {
	switch r {
	case RoleOld:
		return 'o'
	case RoleNew:
		return 'n'
	default:
		return 0
	}
}

func (r Role) String() string {
	switch r {
	case RoleOld:
		return "old"
	case RoleNew:
		return "new"
	default:
		return fmt.Sprintf("role(%d)", int(r))
	}
}

// Status is the lifecycle state of a replacement transaction.
type Status int

const (
	// StatusPending waits for one or both channel confirmations.
	StatusPending Status = iota + 1
	// StatusCommitted finished with both proofs and the channel was swapped.
	StatusCommitted
	// StatusRevoked was cancelled explicitly before commit.
	StatusRevoked
	// StatusSuperseded was replaced by a newer Begin before it completed.
	StatusSuperseded
)

func (s Status) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusCommitted:
		return "committed"
	case StatusRevoked:
		return "revoked"
	case StatusSuperseded:
		return "superseded"
	default:
		return fmt.Sprintf("status(%d)", int(s))
	}
}

var (
	ErrNoManager         = errors.New("netsec: nil manager")
	ErrInvalidAccount    = errors.New("netsec: empty account id")
	ErrInvalidChannel    = errors.New("netsec: invalid channel")
	ErrInvalidRole       = errors.New("netsec: invalid confirmation role")
	ErrNoActiveChannel   = errors.New("netsec: account has no active recovery channel")
	ErrNoPendingChange   = errors.New("netsec: no pending change")
	ErrUnknownChange     = errors.New("netsec: unknown change id")
	ErrDuplicateChannel  = errors.New("netsec: new channel must differ from active channel")
	ErrChannelMismatch   = errors.New("netsec: old channel does not match active recovery channel")
	ErrExpired           = errors.New("netsec: change term has expired")
	ErrRevoked           = errors.New("netsec: change was revoked")
	ErrSuperseded        = errors.New("netsec: change was superseded by a newer change")
	ErrDeadlineMismatch  = errors.New("netsec: proof term does not match change deadline")
	ErrDigestMismatch    = errors.New("netsec: proof digest does not match change digest")
	ErrRoleMismatch      = errors.New("netsec: proof role does not match requested role")
	ErrInvalidProof      = errors.New("netsec: malformed proof token")
	ErrProofVerification = errors.New("netsec: proof signature verification failed")
	ErrRoleAlreadyDone   = errors.New("netsec: role already confirmed for this change")
)

var tokenEncoding = base64.RawURLEncoding

// proofPayload is the canonical byte string that every proof is signed over.
// Binding digest, role and deadline together means a proof can only ever
// attest to one specific confirmation of one specific transaction.
func proofPayload(digest Digest, role Role, deadline time.Time) []byte {
	dl := deadline.UnixNano()
	payload := make([]byte, 0, len(digest)+1+8)
	payload = append(payload, digest...)
	payload = append(payload, role.byte())
	for shift := 56; shift >= 0; shift -= 8 {
		payload = append(payload, byte(dl>>shift))
	}
	return payload
}

func signPayload(secret, payload []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	return mac.Sum(nil)
}

// EncodeProof mints the single-use token delivered to one channel. The token
// embeds the change digest, the channel role and the deadline and is signed
// with the manager's HMAC secret.
func EncodeProof(secret []byte, digest Digest, role Role, deadline time.Time) (string, error) {
	if len(secret) == 0 {
		return "", ErrNoManager
	}
	if !role.valid() {
		return "", ErrInvalidRole
	}
	signature := signPayload(secret, proofPayload(digest, role, deadline))
	raw := make([]byte, 0, len(signature)+len(digest)+1+8)
	raw = append(raw, digest...)
	raw = append(raw, role.byte())
	for shift := 56; shift >= 0; shift -= 8 {
		raw = append(raw, byte(deadline.UnixNano()>>shift))
	}
	raw = append(raw, signature...)
	return tokenEncoding.EncodeToString(raw), nil
}

// DecodeProof verifies a token's signature and returns the digest, role and
// deadline embedded in it. It performs purely cryptographic validation;
// lifecycle checks (digest match, term, status) are the manager's job.
func DecodeProof(secret []byte, token string) (Digest, Role, time.Time, error) {
	if len(secret) == 0 {
		return nil, 0, time.Time{}, ErrNoManager
	}
	raw, err := tokenEncoding.DecodeString(token)
	if err != nil {
		return nil, 0, time.Time{}, fmt.Errorf("%w: %v", ErrInvalidProof, err)
	}
	const digestLen = sha256.Size
	const minLen = digestLen + 1 + 8 + sha256.Size
	if len(raw) < minLen {
		return nil, 0, time.Time{}, ErrInvalidProof
	}

	signatureAt := len(raw) - sha256.Size
	digest := Digest(append([]byte(nil), raw[:digestLen]...))
	roleByte := raw[digestLen]

	var role Role
	switch roleByte {
	case 'o':
		role = RoleOld
	case 'n':
		role = RoleNew
	default:
		return nil, 0, time.Time{}, ErrInvalidRole
	}

	var deadlineNano int64
	for _, b := range raw[digestLen+1 : digestLen+1+8] {
		deadlineNano = (deadlineNano << 8) | int64(b)
	}
	deadline := time.Unix(0, deadlineNano)

	expected := signPayload(secret, proofPayload(digest, role, deadline))
	if !hmac.Equal(expected, raw[signatureAt:]) {
		return nil, 0, time.Time{}, ErrProofVerification
	}
	return digest, role, deadline, nil
}
