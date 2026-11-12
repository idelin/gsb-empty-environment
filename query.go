package taskpkg

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"
)

var (
	// ErrZoneMismatch is returned when an answer does not come from the
	// authoritative zone the query was pinned to.
	ErrZoneMismatch = errors.New("taskpkg: answer zone does not match pinned authoritative zone")
	// ErrAnchorMismatch is returned when an answer does not carry the
	// trust-anchor version the query was pinned to.
	ErrAnchorMismatch = errors.New("taskpkg: answer anchor version does not match query pin")
	// ErrQuestionMismatch is returned when an answer names a different query.
	ErrQuestionMismatch = errors.New("taskpkg: answer does not respond to the pinned query")
	// ErrFingerprintNotRetained is returned when the signing key is not on the
	// retained allow-list of the pinned declaration.
	ErrFingerprintNotRetained = errors.New("taskpkg: signing fingerprint is not retained at pinned generation")
	// ErrBadSignature is returned when the signature fails Ed25519 verification.
	ErrBadSignature = errors.New("taskpkg: signature verification failed")
)

// Query is a resolver request bound to one authoritative zone and one
// trust-anchor generation for its entire lifetime.
type Query struct {
	ID     uint64
	QName  string
	Anchor TrustAnchor
}

// SignedAnswer is the wire-level response from an authoritative server.
type SignedAnswer struct {
	Zone      string
	QueryID   uint64
	QName     string
	Version   uint64
	RRData    []byte
	Signer    Fingerprint
	Signature []byte
}

// VerifiedAnswer is an answer whose signature was valid against the pinned
// generation. It is not yet authoritative: the cache publisher must still
// confirm that the signer survives the current generation before it may
// authorize new queries.
type VerifiedAnswer struct {
	Answer *SignedAnswer
	Pin    TrustAnchor
	Path   TrustPath
}

// Resolver verifies answers against the immutable query pin using the
// historical declarations in the trust store.
type Resolver struct {
	store *TrustStore
}

// NewResolver builds a Resolver backed by store.
func NewResolver(store *TrustStore) *Resolver {
	return &Resolver{store: store}
}

// Verify checks zone/version pinning and the Ed25519 signature against the
// declaration at the pinned version. It never consults the current
// generation: revocation is re-checked at publish time.
func (r *Resolver) Verify(q Query, ans *SignedAnswer) (*VerifiedAnswer, error) {
	if ans == nil {
		return nil, errors.New("taskpkg: nil answer")
	}
	if ans.Zone != q.Anchor.Zone {
		return nil, fmt.Errorf("%w: query=%q answer=%q", ErrZoneMismatch, q.Anchor.Zone, ans.Zone)
	}
	if ans.Version != q.Anchor.Version {
		return nil, fmt.Errorf("%w: query=v%d answer=v%d", ErrAnchorMismatch, q.Anchor.Version, ans.Version)
	}
	if ans.QueryID != q.ID || ans.QName != q.QName {
		return nil, fmt.Errorf("%w: query=(%d,%q) answer=(%d,%q)", ErrQuestionMismatch, q.ID, q.QName, ans.QueryID, ans.QName)
	}

	decl, err := r.store.At(q.Anchor.Zone, q.Anchor.Version)
	if err != nil {
		return nil, err
	}
	pub, ok := decl.PublicKey(ans.Signer)
	if !ok {
		return nil, fmt.Errorf("%w: zone=%q v%d fp=%s", ErrFingerprintNotRetained, q.Anchor.Zone, q.Anchor.Version, ans.Signer)
	}
	if !ed25519.Verify(pub, signedPayload(ans), ans.Signature) {
		return nil, fmt.Errorf("%w: zone=%q v%d fp=%s", ErrBadSignature, q.Anchor.Zone, q.Anchor.Version, ans.Signer)
	}

	path, err := BuildTrustPath(r.store, q.Anchor, ans.Signer)
	if err != nil {
		return nil, err
	}
	return &VerifiedAnswer{Answer: ans, Pin: q.Anchor, Path: path}, nil
}

// signedPayload is the deterministic byte string covered by the signature.
// Every field is length-prefixed so encodings cannot collide.
func signedPayload(ans *SignedAnswer) []byte {
	var buf []byte
	buf = appendLenPrefixed(buf, []byte(ans.Zone))
	var id [8]byte
	binary.BigEndian.PutUint64(id[:], ans.QueryID)
	buf = append(buf, id[:]...)
	buf = appendLenPrefixed(buf, []byte(ans.QName))
	var ver [8]byte
	binary.BigEndian.PutUint64(ver[:], ans.Version)
	buf = append(buf, ver[:]...)
	buf = appendLenPrefixed(buf, ans.RRData)
	buf = appendLenPrefixed(buf, []byte(ans.Signer))
	return buf
}

func appendLenPrefixed(dst, src []byte) []byte {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(src)))
	dst = append(dst, n[:]...)
	return append(dst, src...)
}
