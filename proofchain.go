// Package netsec implements a hop-by-hop client-certificate verification
// proof chain for layered TLS termination points.
//
// Every TLS termination hop that verifies a peer certificate emits an
// Attestation. Each attestation is a real standard-library signature over a
// deterministic byte string that binds:
//
//   - the digest of the previous attestation (or a genesis marker),
//   - the identity verified at that hop,
//   - the final target service (audience),
//   - a per-connection challenge nonce,
//   - a validity window, and
//   - the assurance level actually achieved plus the minimum achieved
//     upstream.
//
// A hop may only append its own verification fact. It cannot raise the
// upstream guarantee level: the verifier requires UpstreamMin to equal the
// running minimum of the preceding attestations, and the chain's effective
// assurance is the minimum over every hop. Cross-connection splicing fails
// because the challenge and audience are bound into every signature, and
// chain reordering or segment swapping fails because PrevDigest links each
// signed payload to the exact previous attestation.
package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Version identifies the attestation canonical-encoding and signing scheme.
const Version = 1

// Signature algorithms supported by Signer and KeyRing.
const (
	AlgECDSAP256SHA256 = "ECDSA-P256-SHA256"
	AlgEd25519         = "Ed25519"
	AlgRSAPSSSHA256    = "RSA-PSS-SHA256"
)

// Assurance is the strength of a certificate verification performed by a hop.
// The zero value is None; values are ordered from weakest to strongest.
type Assurance int

const (
	AssuranceNone Assurance = iota
	AssurancePresent
	AssuranceValidChain
	AssuranceValidChainAndIdentity
)

// Valid reports whether a is one of the defined assurance levels.
func (a Assurance) Valid() bool {
	return a >= AssuranceNone && a <= AssuranceValidChainAndIdentity
}

func (a Assurance) String() string {
	switch a {
	case AssuranceNone:
		return "none"
	case AssurancePresent:
		return "cert-present"
	case AssuranceValidChain:
		return "valid-chain"
	case AssuranceValidChainAndIdentity:
		return "valid-chain-and-identity"
	default:
		return fmt.Sprintf("unknown(%d)", int(a))
	}
}

// Sentinel errors returned by chain construction and verification. Callers
// can use errors.Is to distinguish failure classes.
var (
	ErrEmptyChain          = errors.New("netsec: proof chain is empty")
	ErrInvalidFact         = errors.New("netsec: invalid verification fact")
	ErrBrokenChain         = errors.New("netsec: attestation not bound to previous attestation")
	ErrUnknownKey          = errors.New("netsec: unknown attestation key")
	ErrBadSignature        = errors.New("netsec: attestation signature verification failed")
	ErrHopIdentityMismatch = errors.New("netsec: key does not belong to attesting hop")
	ErrChallengeMismatch   = errors.New("netsec: connection challenge mismatch")
	ErrAudienceMismatch    = errors.New("netsec: target service audience mismatch")
	ErrIdentityMismatch    = errors.New("netsec: verified subject mismatch")
	ErrNotYetValid         = errors.New("netsec: attestation not yet valid")
	ErrExpired             = errors.New("netsec: attestation expired")
	ErrAssuranceElevation  = errors.New("netsec: upstream assurance level cannot be modified")
	ErrInvalidAttestation  = errors.New("netsec: malformed attestation")
	ErrUnsupportedAlg      = errors.New("netsec: unsupported signature algorithm")
)

// Fact is the verification observation a hop signs.
type Fact struct {
	// HopID uniquely identifies the attesting TLS termination hop.
	HopID string
	// KeyID identifies the hop's signing key inside a KeyRing.
	KeyID string
	// VerifiedSubject is the identity this hop actually verified.
	// The root hop places the client identity here; every later hop must
	// place the immediately upstream hop's HopID here.
	VerifiedSubject string
	// Audience is the final target service the connection is bound to.
	Audience string
	// Challenge is the per-connection nonce issued by the target service.
	Challenge string
	// Assurance is the verification strength this hop itself achieved.
	Assurance Assurance
	// UpstreamMin is the minimum assurance achieved by all upstream hops.
	// The root must report AssuranceNone; later hops must report the running
	// minimum. A hop therefore cannot promote the upstream guarantee.
	UpstreamMin Assurance
	// NotBefore and NotAfter define the signed validity window.
	NotBefore time.Time
	NotAfter  time.Time
}

// Attestation is one signed verification fact embedded in a proof chain.
type Attestation struct {
	Version         int       `json:"version"`
	Index           int       `json:"index"`
	HopID           string    `json:"hop_id"`
	KeyID           string    `json:"key_id"`
	VerifiedSubject string    `json:"verified_subject"`
	Audience        string    `json:"audience"`
	Challenge       string    `json:"challenge"`
	Assurance       Assurance `json:"assurance"`
	UpstreamMin     Assurance `json:"upstream_min"`
	NotBefore       time.Time `json:"not_before"`
	NotAfter        time.Time `json:"not_after"`
	// PrevDigest is the AttestationDigest of the previous attestation, or the
	// genesis digest for the root attestation.
	PrevDigest []byte `json:"prev_digest"`
	// SigAlg names the standard-library signature scheme used for Signature.
	SigAlg string `json:"sig_alg"`
	// Signature is over the deterministic canonical bytes of the other fields.
	Signature []byte `json:"signature"`
}

// Canonical TLV tags. The fixed, domain-separated layout makes the signed
// byte string unambiguous and independent of map iteration or JSON spacing.
const (
	tagVersion         byte = 1
	tagIndex           byte = 2
	tagHopID           byte = 3
	tagKeyID           byte = 4
	tagVerifiedSubject byte = 5
	tagAudience        byte = 6
	tagChallenge       byte = 7
	tagAssurance       byte = 8
	tagUpstreamMin     byte = 9
	tagNotBefore       byte = 10
	tagNotAfter        byte = 11
	tagPrevDigest      byte = 12
	tagSigAlg          byte = 13
)

func appendTLV(buf []byte, tag byte, payload []byte) []byte {
	buf = append(buf, tag)
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(payload)))
	buf = append(buf, length[:]...)
	return append(buf, payload...)
}

func appendStringTLV(buf []byte, tag byte, value string) []byte {
	return appendTLV(buf, tag, []byte(value))
}

func appendUint64TLV(buf []byte, tag byte, value uint64) []byte {
	var payload [8]byte
	binary.BigEndian.PutUint64(payload[:], value)
	return appendTLV(buf, tag, payload[:])
}

func appendTimeTLV(buf []byte, tag byte, value time.Time) []byte {
	return appendStringTLV(buf, tag, value.UTC().Format(time.RFC3339Nano))
}

// canonicalBytes returns the deterministic byte string that is signed.
func (a *Attestation) canonicalBytes() []byte {
	buf := make([]byte, 0, 256)
	buf = appendUint64TLV(buf, tagVersion, uint64(a.Version))
	buf = appendUint64TLV(buf, tagIndex, uint64(a.Index))
	buf = appendStringTLV(buf, tagHopID, a.HopID)
	buf = appendStringTLV(buf, tagKeyID, a.KeyID)
	buf = appendStringTLV(buf, tagVerifiedSubject, a.VerifiedSubject)
	buf = appendStringTLV(buf, tagAudience, a.Audience)
	buf = appendStringTLV(buf, tagChallenge, a.Challenge)
	buf = appendUint64TLV(buf, tagAssurance, uint64(a.Assurance))
	buf = appendUint64TLV(buf, tagUpstreamMin, uint64(a.UpstreamMin))
	buf = appendTimeTLV(buf, tagNotBefore, a.NotBefore)
	buf = appendTimeTLV(buf, tagNotAfter, a.NotAfter)
	buf = appendTLV(buf, tagPrevDigest, a.PrevDigest)
	buf = appendStringTLV(buf, tagSigAlg, a.SigAlg)
	return buf
}

// AttestationDigest is the SHA-256 digest of the full attestation including
// its signature. It is the value later hops bind with PrevDigest.
func AttestationDigest(a *Attestation) ([32]byte, error) {
	raw, err := json.Marshal(a)
	if err != nil {
		return [32]byte{}, fmt.Errorf("netsec: encode attestation for digest: %w", err)
	}
	return sha256.Sum256(raw), nil
}

// GenesisDigest is the PrevDigest value used by the root attestation. It binds
// the chain's audience and connection challenge before any hop exists.
func GenesisDigest(audience, challenge string) [32]byte {
	buf := make([]byte, 0, 64)
	buf = appendStringTLV(buf, tagAudience, audience)
	buf = appendStringTLV(buf, tagChallenge, challenge)
	prefix := []byte("netsec-proofchain-v1-genesis")
	buf = append(prefix, buf...)
	return sha256.Sum256(buf)
}

func (f Fact) validate() error {
	if f.HopID == "" || f.KeyID == "" || f.VerifiedSubject == "" ||
		f.Audience == "" || f.Challenge == "" {
		return fmt.Errorf("%w: identifiers, subject, audience and challenge are required", ErrInvalidFact)
	}
	if !f.Assurance.Valid() || !f.UpstreamMin.Valid() {
		return fmt.Errorf("%w: assurance levels out of range", ErrInvalidFact)
	}
	if f.NotBefore.IsZero() || f.NotAfter.IsZero() {
		return fmt.Errorf("%w: validity window is required", ErrInvalidFact)
	}
	if !f.NotAfter.After(f.NotBefore) {
		return fmt.Errorf("%w: NotAfter must be after NotBefore", ErrInvalidFact)
	}
	return nil
}

// Signer signs verification facts for one hop using a standard-library key.
type Signer struct {
	hopID  string
	keyID  string
	alg    string
	signer crypto.Signer
}

// NewSigner builds a Signer. key must be *ecdsa.PrivateKey, ed25519.PrivateKey
// or *rsa.PrivateKey; the algorithm label is derived from the key type.
func NewSigner(hopID, keyID string, key crypto.Signer) (*Signer, error) {
	if hopID == "" || keyID == "" {
		return nil, fmt.Errorf("%w: hop id and key id are required", ErrInvalidFact)
	}
	if key == nil {
		return nil, fmt.Errorf("%w: nil signing key", ErrInvalidFact)
	}
	var alg string
	switch key.(type) {
	case *ecdsa.PrivateKey:
		alg = AlgECDSAP256SHA256
	case ed25519.PrivateKey:
		alg = AlgEd25519
	case *rsa.PrivateKey:
		alg = AlgRSAPSSSHA256
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedAlg, key)
	}
	return &Signer{hopID: hopID, keyID: keyID, alg: alg, signer: key}, nil
}

// HopID reports the hop this signer is bound to.
func (s *Signer) HopID() string { return s.hopID }

// KeyID reports the key identifier embedded in attestations.
func (s *Signer) KeyID() string { return s.keyID }

// Alg reports the signature algorithm label.
func (s *Signer) Alg() string { return s.alg }

// Public returns the hop's public verification key.
func (s *Signer) Public() crypto.PublicKey { return s.signer.Public() }

// Sign produces a signed attestation for a fact at the given chain index,
// binding prev. The signer must be bound to fact.HopID and fact.KeyID.
func (s *Signer) Sign(fact Fact, index int, prev [32]byte) (*Attestation, error) {
	if err := fact.validate(); err != nil {
		return nil, err
	}
	if index < 0 {
		return nil, fmt.Errorf("%w: negative attestation index", ErrInvalidAttestation)
	}
	if fact.HopID != s.hopID {
		return nil, fmt.Errorf("%w: signer is bound to hop %q, cannot attest for %q",
			ErrHopIdentityMismatch, s.hopID, fact.HopID)
	}
	if fact.KeyID != s.keyID {
		return nil, fmt.Errorf("%w: fact key id %q does not match signer key id %q",
			ErrInvalidFact, fact.KeyID, s.keyID)
	}
	a := &Attestation{
		Version:         Version,
		Index:           index,
		HopID:           fact.HopID,
		KeyID:           fact.KeyID,
		VerifiedSubject: fact.VerifiedSubject,
		Audience:        fact.Audience,
		Challenge:       fact.Challenge,
		Assurance:       fact.Assurance,
		UpstreamMin:     fact.UpstreamMin,
		NotBefore:       fact.NotBefore.UTC(),
		NotAfter:        fact.NotAfter.UTC(),
		PrevDigest:      append([]byte(nil), prev[:]...),
		SigAlg:          s.alg,
	}
	signature, err := s.signPayload(a.canonicalBytes())
	if err != nil {
		return nil, err
	}
	a.Signature = signature
	return a, nil
}

func (s *Signer) signPayload(payload []byte) ([]byte, error) {
	switch s.alg {
	case AlgECDSAP256SHA256:
		digest := sha256.Sum256(payload)
		key := s.signer.(*ecdsa.PrivateKey)
		return ecdsa.SignASN1(rand.Reader, key, digest[:])
	case AlgEd25519:
		return s.signer.Sign(rand.Reader, payload, crypto.Hash(0))
	case AlgRSAPSSSHA256:
		digest := sha256.Sum256(payload)
		key := s.signer.(*rsa.PrivateKey)
		return rsa.SignPSS(rand.Reader, key, crypto.SHA256, digest[:], &rsa.PSSOptions{
			SaltLength: rsa.PSSSaltLengthEqualsHash,
		})
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlg, s.alg)
	}
}
