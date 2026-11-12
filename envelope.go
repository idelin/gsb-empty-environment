package netsec

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// Stellar envelope type discriminants (XDR EnvelopeType).
const (
	EnvelopeTypeTxV0      uint32 = 0
	EnvelopeTypeTx        uint32 = 2
	EnvelopeTypeTxFeeBump uint32 = 5
)

const (
	maxSigs            = 200
	maxSigBytes        = 64 * 1024
	maxOperations      = 1000
	maxMuxedAccountLen = 32 + 8
)

// DecoratedSignature is one entry of an envelope's signature list.
type DecoratedSignature struct {
	Hint      [4]byte // last four bytes of the signer public key; preselection only
	Signature []byte
}

// Envelope is the parsed view of a Stellar transaction envelope. Fee bump
// envelopes carry the wrapped inner envelope in Inner.
type Envelope struct {
	Type           uint32
	SourceAccount  []byte // 32-byte ed25519 public key of the fee bump / tx source
	TransactionXDR []byte // canonical bytes of the transaction union value
	Signatures     []DecoratedSignature
	Inner          *Envelope
	raw            []byte // full envelope union bytes, including discriminant
}

var (
	// ErrMalformedEnvelope means the XDR could not be parsed or contained an
	// unsupported arm. Parsing fails closed rather than tolerating unknown data.
	ErrMalformedEnvelope = errors.New("netsec: malformed or unsupported transaction envelope")
	// ErrUnknownEnvelopeType means the top-level discriminant is not v0/v1/fee bump.
	ErrUnknownEnvelopeType = errors.New("netsec: unknown envelope type")
)

// ParseEnvelopeBase64 decodes and parses a base64 standard-alphabet envelope.
func ParseEnvelopeBase64(xdrB64 string) (*Envelope, error) {
	raw, err := base64.StdEncoding.DecodeString(xdrB64)
	if err != nil {
		return nil, ErrMalformedEnvelope
	}
	return ParseEnvelope(raw)
}

// ParseEnvelope parses the XDR union TransactionEnvelope.
func ParseEnvelope(raw []byte) (*Envelope, error) {
	env, err := parseEnvelope(raw)
	if err != nil {
		return nil, ErrMalformedEnvelope
	}
	return env, nil
}

func parseEnvelope(raw []byte) (*Envelope, error) {
	if len(raw) == 0 {
		return nil, errXDR
	}
	r := &xdrReader{buf: raw}
	disc, err := r.u32()
	if err != nil {
		return nil, err
	}

	env := &Envelope{Type: disc, raw: append([]byte(nil), raw...)}
	switch disc {
	case EnvelopeTypeTxV0:
		if err := parseV0Envelope(r, env); err != nil {
			return nil, err
		}
	case EnvelopeTypeTx:
		if err := parseV1Envelope(r, env); err != nil {
			return nil, err
		}
	case EnvelopeTypeTxFeeBump:
		if err := parseFeeBumpEnvelope(r, env); err != nil {
			return nil, err
		}
	default:
		return nil, ErrUnknownEnvelopeType
	}

	if r.pos != len(raw) {
		return nil, errXDR
	}
	return env, nil
}

// parseV1Envelope consumes ENVELOPE_TYPE_TX(2) -> TransactionV1Envelope.
func parseV1Envelope(r *xdrReader, env *Envelope) error {
	txStart := r.pos
	if err := parseV1Transaction(r, env); err != nil {
		return err
	}
	env.TransactionXDR = r.buf[txStart:r.pos]
	sigs, err := readSigs(r)
	if err != nil {
		return err
	}
	env.Signatures = sigs
	return nil
}

// parseV0Envelope consumes ENVELOPE_TYPE_TX_V0(0) -> TransactionV0Envelope.
func parseV0Envelope(r *xdrReader, env *Envelope) error {
	txStart := r.pos
	if err := parseV0Transaction(r, env); err != nil {
		return err
	}
	env.TransactionXDR = r.buf[txStart:r.pos]
	sigs, err := readSigs(r)
	if err != nil {
		return err
	}
	env.Signatures = sigs
	return nil
}

// parseV1Transaction parses the Transaction struct of an ENVELOPE_TYPE_TX.
func parseV1Transaction(r *xdrReader, env *Envelope) error {
	src, err := readMuxedEd25519(r)
	if err != nil {
		return err
	}
	env.SourceAccount = src
	if _, err := r.u32(); err != nil { // fee
		return err
	}
	if _, err := r.u64(); err != nil { // seqNum
		return err
	}
	if err := (refShape{"Preconditions"}).skip(r); err != nil {
		return err
	}
	if err := (refShape{"Memo"}).skip(r); err != nil {
		return err
	}
	if err := skipOperations(r); err != nil {
		return err
	}
	return shVoidUnion.skip(r) // ext
}

// parseV0Transaction parses the TransactionV0 struct of an ENVELOPE_TYPE_TX_V0.
func parseV0Transaction(r *xdrReader, env *Envelope) error {
	src, err := r.fixedOpaque(32)
	if err != nil {
		return err
	}
	env.SourceAccount = append([]byte(nil), src...)
	if _, err := r.u32(); err != nil { // fee
		return err
	}
	if _, err := r.u64(); err != nil { // seqNum
		return err
	}
	if err := skipOptional(r, refShape{"TimeBounds"}); err != nil {
		return err
	}
	if err := (refShape{"Memo"}).skip(r); err != nil {
		return err
	}
	if err := skipOperations(r); err != nil {
		return err
	}
	return shVoidUnion.skip(r) // ext
}

// parseFeeBumpEnvelope consumes ENVELOPE_TYPE_TX_FEE_BUMP(5).
func parseFeeBumpEnvelope(r *xdrReader, outer *Envelope) error {
	txStart := r.pos

	src, err := readMuxedEd25519(r)
	if err != nil {
		return err
	}
	outer.SourceAccount = src
	if _, err := r.i64(); err != nil { // fee
		return err
	}

	// innerTransaction is itself a TransactionEnvelope union limited to
	// ENVELOPE_TYPE_TX / ENVELOPE_TYPE_TX_V0.
	innerStart := r.pos
	innerDisc, err := r.u32()
	if err != nil {
		return err
	}
	inner := &Envelope{Type: innerDisc}
	switch innerDisc {
	case EnvelopeTypeTx:
		innerTxStart := r.pos
		if err := parseV1Transaction(r, inner); err != nil {
			return err
		}
		inner.TransactionXDR = r.buf[innerTxStart:r.pos]
	case EnvelopeTypeTxV0:
		innerTxStart := r.pos
		if err := parseV0Transaction(r, inner); err != nil {
			return err
		}
		inner.TransactionXDR = r.buf[innerTxStart:r.pos]
	default:
		return errXDR
	}
	innerSigs, err := readSigs(r)
	if err != nil {
		return err
	}
	inner.Signatures = innerSigs
	inner.raw = append([]byte(nil), r.buf[innerStart:r.pos]...)
	outer.Inner = inner

	if err := shVoidUnion.skip(r); err != nil { // FeeBumpTransaction.ext
		return err
	}
	outer.TransactionXDR = r.buf[txStart:r.pos]

	outerSigs, err := readSigs(r)
	if err != nil {
		return err
	}
	outer.Signatures = outerSigs
	return nil
}

func readMuxedEd25519(r *xdrReader) ([]byte, error) {
	disc, err := r.u32()
	if err != nil {
		return nil, err
	}
	switch disc {
	case 0: // KEY_TYPE_ED25519 -> AccountID union
		accountType, err := r.u32()
		if err != nil {
			return nil, err
		}
		if accountType != 0 { // PUBLIC_KEY_TYPE_ED25519
			return nil, errXDR
		}
		raw, err := r.fixedOpaque(32)
		if err != nil {
			return nil, err
		}
		return append([]byte(nil), raw...), nil
	case 1: // KEY_TYPE_MUXED_ED25519
		raw, err := r.fixedOpaque(maxMuxedAccountLen)
		if err != nil {
			return nil, err
		}
		return append([]byte(nil), raw[:32]...), nil
	default:
		return nil, errXDR
	}
}

func readSigs(r *xdrReader) ([]DecoratedSignature, error) {
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	if n > maxSigs {
		return nil, errXDR
	}
	out := make([]DecoratedSignature, n)
	for i := range out {
		hint, err := r.fixedOpaque(4)
		if err != nil {
			return nil, err
		}
		sig, err := r.varOpaque(maxSigBytes)
		if err != nil {
			return nil, err
		}
		copy(out[i].Hint[:], hint)
		out[i].Signature = append([]byte(nil), sig...)
	}
	return out, nil
}

func skipOptional(r *xdrReader, inner shape) error {
	present, err := r.boolean()
	if err != nil {
		return err
	}
	if present {
		return inner.skip(r)
	}
	return nil
}

func skipOperations(r *xdrReader) error {
	n, err := r.u32()
	if err != nil {
		return err
	}
	if n > maxOperations {
		return errXDR
	}
	for i := uint32(0); i < n; i++ {
		if err := skipOptional(r, refShape{"MuxedAccount"}); err != nil {
			return err
		}
		if err := (refShape{"OperationBody"}).skip(r); err != nil {
			return err
		}
	}
	return nil
}

// PayloadDigest computes the verification digest for one envelope layer:
// SHA-256(network passphrase hash || envelope type || transaction XDR).
//
// The passphrase hash binds the payload to one Stellar network, so a signature
// produced on the public network cannot verify on testnet (or vice versa).
// The four-byte envelope type binds v0/v1/fee bump layers independently, so a
// signature cannot be moved onto a fee bump wrapper.
func PayloadDigest(networkPassphrase string, envelopeType uint32, transactionXDR []byte) [32]byte {
	networkID := sha256.Sum256([]byte(networkPassphrase))
	h := sha256.New()
	h.Write(networkID[:])
	h.Write(u32Bytes(envelopeType))
	h.Write(transactionXDR)
	var out [32]byte
	h.Sum(out[:0])
	return out
}

func u32Bytes(v uint32) []byte {
	return []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
}
