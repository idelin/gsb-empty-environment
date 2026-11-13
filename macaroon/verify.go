// Package macaroon implements an importable macaroon verifier for a Go
// resource service.
//
// Signatures follow the task contract:
//
//	sig0                    = HMAC-SHA256(rootKey, identifier)
//	first-party caveat sig  = HMAC(previousSignature, caveat)
//	third-party caveat sig  = HMAC(previousSignature, vid || cid)
//	binding signature       = HMAC-SHA256(32 zero bytes, primarySig || dischargeSig)
//
// The verifier enforces discharge binding, one-use discharges within a single
// verification, caller-supplied first-party predicates (unknown predicates
// fail), the earliest "time < T" deadline, and a maximum of 32 caveats.
package macaroon

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// MaxCaveats is the maximum number of caveats accepted across the primary
// macaroon and all of its discharge macaroons in a single verification.
const MaxCaveats = 32

var (
	// ErrMalformed is returned when a request or macaroon is structurally
	// invalid.
	ErrMalformed = errors.New("macaroon: malformed request")

	// ErrSignature is returned when a macaroon signature does not match the
	// value computed from its caveat chain.
	ErrSignature = errors.New("macaroon: signature mismatch")

	// ErrPredicate is returned when a first-party caveat predicate is unknown
	// or the caller's callback rejects it.
	ErrPredicate = errors.New("macaroon: caveat predicate not satisfied")

	// ErrDischarge is returned when a required discharge macaroon is missing
	// or does not correspond to a third-party caveat id.
	ErrDischarge = errors.New("macaroon: discharge macaroon unavailable or invalid")

	// ErrDischargeReused is returned when one discharge macaroon would be used
	// to satisfy more than one third-party caveat within a verification.
	ErrDischargeReused = errors.New("macaroon: discharge macaroon reused")

	// ErrBinding is returned when the request's discharge binding signature
	// differs from the value derived from the primary signature.
	ErrBinding = errors.New("macaroon: discharge binding signature mismatch")

	// ErrTime is returned when a "time < T" caveat has expired.
	ErrTime = errors.New("macaroon: time caveat expired")

	// ErrTooManyCaveats is returned when more than MaxCaveats caveats are
	// presented across the primary and discharge macaroons.
	ErrTooManyCaveats = errors.New("macaroon: too many caveats")
)

// Caveat is either a first-party or a third-party caveat. A caveat with an
// empty Location is treated as first-party and its Caveat field is the raw
// predicate; a non-empty Location marks a third-party caveat whose VID and
// CID protect the next signature.
type Caveat struct {
	Caveat   []byte // first-party predicate
	Location string // non-empty for third-party caveats
	VID      []byte // third-party verification id (opaque to the verifier)
	CID      []byte // third-party caveat id, also the discharge identifier
}

// IsThirdParty reports whether the caveat requires a discharge macaroon.
func (c Caveat) IsThirdParty() bool { return c.Location != "" }

// Macaroon is the on-the-wire representation of a macaroon. Signature must be
// the final signature after every caveat in Caveats has been applied.
type Macaroon struct {
	Identifier []byte
	Location   string
	Caveats    []Caveat
	Signature  []byte
}

// BoundDischarge pairs a discharge macaroon with the binding signature claimed
// by the request. The verifier recomputes the binding and rejects mismatches.
type BoundDischarge struct {
	Macaroon *Macaroon
	Binding  []byte
}

// Request is an authorization request carrying the primary macaroon, every
// discharge macaroon it needs, and the claimed binding signatures.
type Request struct {
	Primary    *Macaroon
	Discharges []BoundDischarge
}

// Verifier performs a single macaroon verification. All callbacks are
// caller-supplied: Check judges first-party predicates (returning false for an
// unknown predicate) and RecoverRootKey resolves a third-party VID to the root
// key used by the discharge service. Now defaults to time.Now when nil.
type Verifier struct {
	Check          func(predicate []byte) bool
	RecoverRootKey func(vid []byte) (rootKey []byte, err error)
	Now            func() time.Time
}

func hmacSHA256(key, msg []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(msg)
	return mac.Sum(nil)
}

var zeroKey = make([]byte, sha256.Size)

// bindingCheck records a claimed binding until the primary signature is known
// to be authentic.
type bindingCheck struct {
	claimed   []byte
	discharge []byte
}

type verifyContext struct {
	v           *Verifier
	req         *Request
	caveatN     int
	used        map[string]struct{}
	bindings    []bindingCheck
	earliest    time.Time
	hasDeadline bool
}

// Verify authenticates req against rootKey, which is the root key of the
// primary macaroon. A nil error means every caveat and discharge is satisfied
// according to the package contract.
func (v *Verifier) Verify(rootKey []byte, req *Request) error {
	if len(rootKey) == 0 {
		return fmt.Errorf("%w: empty root key", ErrMalformed)
	}
	if req == nil || req.Primary == nil {
		return fmt.Errorf("%w: missing primary macaroon", ErrMalformed)
	}
	for i := range req.Discharges {
		if req.Discharges[i].Macaroon == nil {
			return fmt.Errorf("%w: discharge %d is nil", ErrMalformed, i)
		}
	}
	if v.Check == nil || v.RecoverRootKey == nil {
		return fmt.Errorf("%w: verifier callbacks must be configured", ErrMalformed)
	}

	now := time.Now
	if v.Now != nil {
		now = v.Now
	}

	ctx := &verifyContext{
		v:    v,
		req:  req,
		used: make(map[string]struct{}),
	}

	sig, err := ctx.chain(req.Primary, rootKey, true)
	if err != nil {
		return err
	}
	if !hmac.Equal(sig, req.Primary.Signature) {
		return fmt.Errorf("%w: primary macaroon", ErrSignature)
	}

	for _, check := range ctx.bindings {
		msg := make([]byte, 0, len(sig)+len(check.discharge))
		msg = append(msg, sig...)
		msg = append(msg, check.discharge...)
		want := hmacSHA256(zeroKey, msg)
		if !hmac.Equal(want, check.claimed) {
			return ErrBinding
		}
	}

	if ctx.hasDeadline && !now().Before(ctx.earliest) {
		return fmt.Errorf("%w: deadline %s", ErrTime, ctx.earliest.Format(time.RFC3339))
	}
	return nil
}

func (ctx *verifyContext) chain(m *Macaroon, rootKey []byte, primary bool) ([]byte, error) {
	sig := hmacSHA256(rootKey, m.Identifier)
	for i := range m.Caveats {
		ctx.caveatN++
		if ctx.caveatN > MaxCaveats {
			return nil, ErrTooManyCaveats
		}
		caveat := m.Caveats[i]
		if caveat.IsThirdParty() {
			if !primary {
				return nil, fmt.Errorf("%w: nested third-party caveat in discharge", ErrMalformed)
			}
			msg := make([]byte, 0, len(caveat.VID)+len(caveat.CID))
			msg = append(msg, caveat.VID...)
			msg = append(msg, caveat.CID...)
			sig = hmacSHA256(sig, msg)
			if err := ctx.discharge(caveat, sig); err != nil {
				return nil, err
			}
		} else {
			sig = hmacSHA256(sig, caveat.Caveat)
			if err := ctx.applyFirstParty(caveat.Caveat); err != nil {
				return nil, err
			}
		}
	}
	return sig, nil
}

func (ctx *verifyContext) applyFirstParty(predicate []byte) error {
	if deadline, ok := parseTimeCaveat(predicate); ok {
		if !ctx.hasDeadline || deadline.Before(ctx.earliest) {
			ctx.earliest = deadline
			ctx.hasDeadline = true
		}
		return nil
	}
	if !ctx.v.Check(predicate) {
		return fmt.Errorf("%w: %q", ErrPredicate, string(predicate))
	}
	return nil
}

func (ctx *verifyContext) discharge(caveat Caveat, primarySig []byte) error {
	var entry *BoundDischarge
	for i := range ctx.req.Discharges {
		if hmac.Equal(ctx.req.Discharges[i].Macaroon.Identifier, caveat.CID) {
			entry = &ctx.req.Discharges[i]
			break
		}
	}
	if entry == nil {
		return fmt.Errorf("%w: no discharge for cid %q", ErrDischarge, string(caveat.CID))
	}

	rootKey, err := ctx.v.RecoverRootKey(caveat.VID)
	if err != nil {
		return fmt.Errorf("recover root key: %w", err)
	}
	if len(rootKey) == 0 {
		return fmt.Errorf("%w: empty discharge root key for cid %q", ErrMalformed, string(caveat.CID))
	}

	dischargeSig, err := ctx.chain(entry.Macaroon, rootKey, false)
	if err != nil {
		return err
	}
	if !hmac.Equal(dischargeSig, entry.Macaroon.Signature) {
		return fmt.Errorf("%w: discharge macaroon for cid %q", ErrSignature, string(caveat.CID))
	}

	key := hex.EncodeToString(dischargeSig)
	if _, used := ctx.used[key]; used {
		return fmt.Errorf("%w: cid %q", ErrDischargeReused, string(caveat.CID))
	}
	ctx.used[key] = struct{}{}

	ctx.bindings = append(ctx.bindings, bindingCheck{
		claimed:   entry.Binding,
		discharge: dischargeSig,
	})
	return nil
}

const timeCaveatPrefix = "time < "

// parseTimeCaveat parses a "time < RFC3339" first-party caveat. Malformed time
// caveats are not recognized, so they fall through to the caller's predicate
// callback and fail as unknown predicates.
func parseTimeCaveat(predicate []byte) (time.Time, bool) {
	if len(predicate) <= len(timeCaveatPrefix) || string(predicate[:len(timeCaveatPrefix)]) != timeCaveatPrefix {
		return time.Time{}, false
	}
	deadline, err := time.Parse(time.RFC3339, string(predicate[len(timeCaveatPrefix):]))
	if err != nil {
		return time.Time{}, false
	}
	return deadline, true
}
