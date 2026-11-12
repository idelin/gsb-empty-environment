package spake2plus

import (
	"bytes"
	"crypto/subtle"
	"errors"
	"math/big"
)

var (
	errInvalidPointEncoding = errors.New("spake2plus: invalid point encoding")

	// ErrInvalidW0 is returned when the registration scalar w0 is not an
	// integer in [1, n-1], where n is the P-256 group order.
	ErrInvalidW0 = errors.New("spake2plus: w0 must be an integer in [1, n-1]")
	// ErrInvalidL is returned when the registration record L is not a
	// valid non-identity P-256 point.
	ErrInvalidL = errors.New("spake2plus: invalid registration record L")
	// ErrInvalidShare is returned when the prover's share is not a valid
	// non-identity P-256 point (this includes the SEC 1 identity encoding
	// 0x00, off-curve points, and malformed encodings).
	ErrInvalidShare = errors.New("spake2plus: invalid prover share")
	// ErrDegenerateShare is returned when the prover's share equals w0*M,
	// i.e. the unmasked element X - w0*M is the identity. Accepting such a
	// share would make the secret element Z independent of the verifier's
	// ephemeral secret, so it is rejected.
	ErrDegenerateShare = errors.New("spake2plus: prover share unmasks to the identity element")
	// ErrConfirmation is returned when the prover's confirmation MAC does
	// not match, indicating a wrong password, a mismatched context or
	// identity, or a tampered/reflected transcript.
	ErrConfirmation = errors.New("spake2plus: prover confirmation MAC mismatch")
	// ErrOutOfOrder is returned when Verifier methods are called out of
	// sequence. Each Verifier runs exactly one handshake: Start once,
	// then Finish once.
	ErrOutOfOrder = errors.New("spake2plus: verifier method called out of order")
)

// VerifierConfig carries the registration record and the handshake
// parameters for one verifier instance. All byte slices are copied.
type VerifierConfig struct {
	// Context is an application-specific string bound into the protocol
	// transcript (e.g. protocol and version names). Both peers must use
	// the same value.
	Context []byte
	// ProverID and VerifierID are the endpoint identities bound into the
	// transcript. They must match the values used at registration time.
	ProverID   []byte
	VerifierID []byte
	// W0 is the big-endian registration scalar w0, an integer in
	// [1, n-1] where n is the P-256 group order.
	W0 []byte
	// L is the registration record L = w1*B as a SEC 1 compressed or
	// uncompressed P-256 point.
	L []byte
}

// Verifier runs the verifier (server) side of one SPAKE2+ handshake.
// It is not safe for concurrent use.
type Verifier struct {
	context []byte
	idP     []byte
	idV     []byte

	w0      *big.Int
	w0Bytes []byte // fixed-length encoding bound into the transcript
	l       point

	y      *big.Int // ephemeral secret
	shareV []byte   // uncompressed encoding of y*B + w0*N

	started  bool
	finished bool
}

// NewVerifier validates the registration record and returns a Verifier
// ready to run a single handshake.
func NewVerifier(cfg VerifierConfig) (*Verifier, error) {
	w0 := new(big.Int).SetBytes(cfg.W0)
	if w0.Sign() <= 0 || w0.Cmp(p256Order) >= 0 {
		return nil, ErrInvalidW0
	}
	l, err := decodePoint(cfg.L)
	if err != nil {
		return nil, ErrInvalidL
	}
	return &Verifier{
		context: bytes.Clone(cfg.Context),
		idP:     bytes.Clone(cfg.ProverID),
		idV:     bytes.Clone(cfg.VerifierID),
		w0:      w0,
		w0Bytes: paddedScalar(w0),
		l:       l,
	}, nil
}

// Start generates the ephemeral secret and returns the verifier's share
// shareV = y*B + w0*N (SEC 1 uncompressed, ShareLength bytes) to send to
// the prover. It must be called exactly once, before Finish.
func (v *Verifier) Start() ([]byte, error) {
	if v.started {
		return nil, ErrOutOfOrder
	}
	v.started = true

	y, err := randomScalar()
	if err != nil {
		return nil, err
	}
	v.y = y
	share := addPoints(scalarBaseMult(y), scalarMult(pointN, v.w0))
	v.shareV = encodePoint(share)
	return bytes.Clone(v.shareV), nil
}

// Finish validates the prover's share and confirmation MAC. On success it
// returns the verifier's confirmation MAC (to send back to the prover) and
// the shared session key (SharedKeyLength bytes). On failure it returns a
// non-nil error and no key material; the Verifier cannot be reused
// afterwards either way.
func (v *Verifier) Finish(shareP, confirmP []byte) (confirmV, sharedKey []byte, err error) {
	if !v.started || v.finished {
		return nil, nil, ErrOutOfOrder
	}
	v.finished = true
	defer v.wipe()

	p, err := decodePoint(shareP)
	if err != nil {
		return nil, nil, ErrInvalidShare
	}
	// Unmask the share: s = shareP - w0*M. Reject the identity so that Z
	// always depends on the verifier's ephemeral secret y.
	s := subPoints(p, scalarMult(pointM, v.w0))
	if isInfinity(s) {
		return nil, nil, ErrDegenerateShare
	}
	zEl := scalarMult(s, v.y)
	vEl := scalarMult(v.l, v.y)

	kConfirmP, kConfirmV, kShared := deriveKeys(v.context, v.idP, v.idV, shareP, v.shareV, zEl, vEl, v.w0Bytes)

	// The prover authenticates with K_confirmP over the verifier's share.
	expected := macTag(kConfirmP, v.shareV)
	if subtle.ConstantTimeCompare(expected, confirmP) != 1 {
		return nil, nil, ErrConfirmation
	}
	// The verifier authenticates with the independent key K_confirmV over
	// the prover's share, so a reflected confirmV can never validate as
	// confirmP.
	confirmV = macTag(kConfirmV, shareP)
	return confirmV, kShared, nil
}

// wipe clears the long-term and ephemeral secrets from memory.
func (v *Verifier) wipe() {
	wipeScalar(v.w0)
	wipeScalar(v.y)
	for i := range v.w0Bytes {
		v.w0Bytes[i] = 0
	}
	v.w0, v.y, v.w0Bytes = nil, nil, nil
}
