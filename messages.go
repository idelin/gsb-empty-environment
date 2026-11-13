package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

const (
	LabelCommit = "commit"
	LabelReveal = "reveal"
	LabelRho    = "rho"
	LabelSigma  = "sigma"
	LabelTau    = "tau"
	LabelD      = "d"
	LabelE      = "e"
)

// RoundID identifies one protocol round. It is part of every signed message.
type RoundID uint64

// TriplePair is the ordered pair (candidate T, sacrifice check T').
type TriplePair struct {
	First  string
	Second string
}

// SignedMessage is a party broadcast. Value is either a 32-byte commitment or
// seed, or a 4-byte big-endian field element, depending on Label.
type SignedMessage struct {
	Round  RoundID
	Pair   TriplePair
	Sender uint8
	Label  string
	Value  []byte
	Sig    []byte
}

// EvidenceKind classifies an observed broadcast violation.
type EvidenceKind string

const (
	EvidenceDuplicate       EvidenceKind = "duplicate_sender"
	EvidenceConflict        EvidenceKind = "conflicting_values"
	EvidenceBadSignature    EvidenceKind = "bad_signature"
	EvidenceWrongContext    EvidenceKind = "wrong_context"
	EvidenceMalformed       EvidenceKind = "malformed_value"
	EvidenceOpeningMismatch EvidenceKind = "opening_share_mismatch"
	EvidenceCommitMismatch  EvidenceKind = "commit_reveal_mismatch"
)

// Evidence retains the offending signed messages so a verifier can audit the
// claimed misbehavior without trusting the host.
type Evidence struct {
	Round    RoundID
	Pair     TriplePair
	Label    string
	Sender   uint8
	Kind     EvidenceKind
	Messages []SignedMessage
	Detail   string
}

func writeLengthPrefixed(w *bytes.Buffer, b []byte) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(len(b)))
	w.Write(buf[:])
	w.Write(b)
}

func canonicalMessage(f Field, round RoundID, pair TriplePair, sender uint8, label string, value []byte) []byte {
	var b bytes.Buffer
	b.WriteString("example.org/netsec/q0106/message\x00")
	var buf [8]byte
	binary.BigEndian.PutUint32(buf[:4], f.P)
	b.Write(buf[:4])
	binary.BigEndian.PutUint64(buf[:], uint64(round))
	b.Write(buf[:])
	writeLengthPrefixed(&b, []byte(pair.First))
	writeLengthPrefixed(&b, []byte(pair.Second))
	b.WriteByte(sender)
	writeLengthPrefixed(&b, []byte(label))
	writeLengthPrefixed(&b, value)
	return b.Bytes()
}

// SignMessage signs a context-bound broadcast with the party's Ed25519 key.
func SignMessage(priv ed25519.PrivateKey, f Field, round RoundID, pair TriplePair, sender uint8, label string, value []byte) (SignedMessage, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return SignedMessage{}, fmt.Errorf("%w: bad private key", ErrMalformedMessage)
	}
	if sender > 2 {
		return SignedMessage{}, fmt.Errorf("%w: sender=%d", ErrMalformedMessage, sender)
	}
	if label == "" {
		return SignedMessage{}, fmt.Errorf("%w: empty label", ErrMalformedMessage)
	}
	v := append([]byte(nil), value...)
	sig := ed25519.Sign(priv, canonicalMessage(f, round, pair, sender, label, v))
	return SignedMessage{
		Round:  round,
		Pair:   pair,
		Sender: sender,
		Label:  label,
		Value:  v,
		Sig:    sig,
	}, nil
}

// VerifyMessage checks only the Ed25519 signature over the message's own
// context. Callers must also check Round, Pair, Label, and value range.
func VerifyMessage(pub ed25519.PublicKey, f Field, m SignedMessage) error {
	if m.Sender > 2 {
		return fmt.Errorf("%w: sender=%d", ErrMalformedMessage, m.Sender)
	}
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: bad public key", ErrBadSignature)
	}
	if len(m.Sig) != ed25519.SignatureSize {
		return fmt.Errorf("%w: bad signature length", ErrBadSignature)
	}
	if !ed25519.Verify(pub, canonicalMessage(f, m.Round, m.Pair, m.Sender, m.Label, m.Value), m.Sig) {
		return ErrBadSignature
	}
	return nil
}

// SeedCommitment hashes a party seed together with its round, triple pair, and
// sender index.
func SeedCommitment(f Field, round RoundID, pair TriplePair, sender uint8, seed []byte) [sha256.Size]byte {
	h := sha256.Sum256(canonicalMessage(f, round, pair, sender, "seed-commitment", seed))
	return h
}

// DeriveSacrificeRandom derives the public nonzero challenge r from the three
// revealed seeds.
func DeriveSacrificeRandom(f Field, round RoundID, pair TriplePair, seeds [3][]byte) (uint32, error) {
	if len(seeds[0]) == 0 && len(seeds[1]) == 0 && len(seeds[2]) == 0 {
		return 0, fmt.Errorf("%w: no seeds", ErrMalformedMessage)
	}
	h := sha256.New()
	h.Write([]byte("example.org/netsec/q0106/sacrifice-random\x00"))
	var buf [8]byte
	binary.BigEndian.PutUint32(buf[:4], f.P)
	h.Write(buf[:4])
	binary.BigEndian.PutUint64(buf[:], uint64(round))
	h.Write(buf[:])
	writeLengthPrefixedBuffer(h, []byte(pair.First))
	writeLengthPrefixedBuffer(h, []byte(pair.Second))
	for _, seed := range seeds {
		writeLengthPrefixedBuffer(h, seed)
	}
	sum := h.Sum(nil)
	v := binary.BigEndian.Uint64(sum[:8]) % uint64(f.P-1)
	return 1 + uint32(v), nil
}

// SacrificeTau evaluates the public sacrifice test:
//
//	rho   = a - r*a'
//	sigma = b - b'
//	tau   = c - r*c' - rho*b' - r*sigma*a' - rho*sigma
//
// Acceptance means tau == 0. The caller is responsible for ensuring r != 0.
func SacrificeTau(f Field, a, b, c, aCheck, bCheck, cCheck, r uint32) (uint32, error) {
	for _, v := range []uint32{a, b, c, aCheck, bCheck, cCheck, r} {
		if !f.Valid(v) {
			return 0, fmt.Errorf("%w: %d", ErrRange, v)
		}
	}
	rho := f.Sub(a, f.Mul(r, aCheck))
	sigma := f.Sub(b, bCheck)
	tau := f.Sub(
		f.Sub(f.Sub(c, f.Mul(r, cCheck)), f.Mul(rho, bCheck)),
		f.Add(f.Mul(r, f.Mul(sigma, aCheck)), f.Mul(rho, sigma)),
	)
	return tau, nil
}

// SacrificeAccepted reports the acceptance decision of the sacrifice check.
func SacrificeAccepted(f Field, a, b, c, aCheck, bCheck, cCheck, r uint32) (bool, error) {
	tau, err := SacrificeTau(f, a, b, c, aCheck, bCheck, cCheck, r)
	if err != nil {
		return false, err
	}
	return tau == 0, nil
}

func writeLengthPrefixedBuffer(h interface{ Write([]byte) (int, error) }, b []byte) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(len(b)))
	_, _ = h.Write(buf[:])
	_, _ = h.Write(b)
}

type messageCollector struct {
	f        Field
	pubs     [3]ed25519.PublicKey
	round    RoundID
	pair     TriplePair
	label    string
	valueLen int
	messages []SignedMessage
	evidence []Evidence
}

// collectExact requires exactly one valid signed value from each of the three
// parties for the given context. Any duplicate sender, conflicting value, bad
// signature, wrong context, or malformed value is retained as evidence.
func (c *messageCollector) collectExact() ([3][]byte, []Evidence, error) {
	var values [3][]byte
	var seen [3]bool
	var first [3]SignedMessage

	for _, raw := range c.messages {
		m := cloneMessage(raw)
		if m.Sender > 2 {
			c.evidence = append(c.evidence, Evidence{
				Round: m.Round, Pair: m.Pair, Label: m.Label, Sender: m.Sender,
				Kind: EvidenceWrongContext, Messages: []SignedMessage{m},
				Detail: fmt.Sprintf("sender index %d", m.Sender),
			})
			continue
		}
		if m.Round != c.round || m.Pair != c.pair || m.Label != c.label {
			c.evidence = append(c.evidence, Evidence{
				Round: c.round, Pair: c.pair, Label: c.label, Sender: m.Sender,
				Kind: EvidenceWrongContext, Messages: []SignedMessage{m},
				Detail: "message context differs from the requested context",
			})
			continue
		}
		if err := VerifyMessage(c.pubs[m.Sender], c.f, m); err != nil {
			c.evidence = append(c.evidence, Evidence{
				Round: c.round, Pair: c.pair, Label: c.label, Sender: m.Sender,
				Kind: EvidenceBadSignature, Messages: []SignedMessage{m},
				Detail: err.Error(),
			})
			continue
		}
		if seen[m.Sender] {
			kind := EvidenceDuplicate
			detail := "more than one broadcast from the same sender"
			if !bytes.Equal(first[m.Sender].Value, m.Value) {
				kind = EvidenceConflict
				detail = "same sender broadcast different values"
			}
			c.evidence = append(c.evidence, Evidence{
				Round: c.round, Pair: c.pair, Label: c.label, Sender: m.Sender,
				Kind: kind, Messages: []SignedMessage{first[m.Sender], m},
				Detail: detail,
			})
			continue
		}
		if len(m.Value) != c.valueLen {
			c.evidence = append(c.evidence, Evidence{
				Round: c.round, Pair: c.pair, Label: c.label, Sender: m.Sender,
				Kind: EvidenceMalformed, Messages: []SignedMessage{m},
				Detail: fmt.Sprintf("value length %d, want %d", len(m.Value), c.valueLen),
			})
			continue
		}
		seen[m.Sender] = true
		first[m.Sender] = m
		values[m.Sender] = append([]byte(nil), m.Value...)
	}

	if len(c.evidence) > 0 {
		return values, c.evidence, fmt.Errorf("%w: %v", evidenceError(c.evidence[0].Kind), c.evidence[0].Kind)
	}
	for i := 0; i < 3; i++ {
		if !seen[i] {
			return values, c.evidence, fmt.Errorf("%w: sender %d, label %s", ErrMissingMessage, i, c.label)
		}
	}
	return values, c.evidence, nil
}

func evidenceError(kind EvidenceKind) error {
	switch kind {
	case EvidenceBadSignature:
		return ErrBadSignature
	case EvidenceWrongContext:
		return ErrWrongContext
	case EvidenceMalformed:
		return ErrMalformedMessage
	case EvidenceDuplicate:
		return ErrDuplicateSender
	case EvidenceConflict:
		return ErrConflictingValues
	case EvidenceOpeningMismatch:
		return ErrOpeningMismatch
	default:
		return ErrMalformedMessage
	}
}

func cloneMessage(m SignedMessage) SignedMessage {
	m.Value = append([]byte(nil), m.Value...)
	m.Sig = append([]byte(nil), m.Sig...)
	return m
}

// AggregateByteMessages collects exactly one byte value per party.
func AggregateByteMessages(f Field, pubs [3]ed25519.PublicKey, round RoundID, pair TriplePair, label string, valueLen int, msgs []SignedMessage) ([3][]byte, []Evidence, error) {
	c := &messageCollector{f: f, pubs: pubs, round: round, pair: pair, label: label, valueLen: valueLen, messages: msgs}
	return c.collectExact()
}

// AggregateShareMessages collects three field-element shares and sums them.
func AggregateShareMessages(f Field, pubs [3]ed25519.PublicKey, round RoundID, pair TriplePair, label string, msgs []SignedMessage) (uint32, []Evidence, error) {
	values, evidence, err := AggregateByteMessages(f, pubs, round, pair, label, 4, msgs)
	if err != nil {
		return 0, evidence, err
	}
	var shares AdditiveShares
	for i := range values {
		v := binary.BigEndian.Uint32(values[i])
		if !f.Valid(v) {
			if offending, ok := signedValueInContext(f, pubs, round, pair, label, uint8(i), msgs); ok {
				evidence = append(evidence, Evidence{
					Round: round, Pair: pair, Label: label, Sender: uint8(i),
					Kind: EvidenceMalformed, Messages: []SignedMessage{offending},
					Detail: fmt.Sprintf("share %d is outside F_%d", v, f.P),
				})
			}
			return 0, evidence, fmt.Errorf("%w: sender %d, label %s", ErrRange, i, label)
		}
		shares[i] = v
	}
	return f.Sum(shares[0], shares[1], shares[2]), evidence, nil
}

func signedValueInContext(f Field, pubs [3]ed25519.PublicKey, round RoundID, pair TriplePair, label string, sender uint8, msgs []SignedMessage) (SignedMessage, bool) {
	for _, raw := range msgs {
		m := cloneMessage(raw)
		if m.Sender != sender || m.Round != round || m.Pair != pair || m.Label != label {
			continue
		}
		if VerifyMessage(pubs[sender], f, m) != nil {
			continue
		}
		return m, true
	}
	return SignedMessage{}, false
}
