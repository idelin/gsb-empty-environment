package kt

import (
	"crypto/ed25519"
)

// MaxEpochs bounds the number of consecutive epochs a client tracks in a
// single signed root chain (1..8).
const MaxEpochs = 8

// FirstEpoch is the mandated epoch number of the first chain entry.
const FirstEpoch uint32 = 1

// EpochRoot is the unsigned body committed by the log for one epoch. It binds
// the tree root, the directory-wide revocation counter, the migration-log
// commitment and the previous signed-root hash into one domain-tagged value.
type EpochRoot struct {
	Epoch         uint32
	TreeRoot      Hash
	RevocCounter  uint64
	MigrationHash Hash
	PrevRoot      Hash // hash of the previous SignedRoot; zero for epoch 1
}

// Hash returns the domain-tagged commitment to the epoch root body.
func (r EpochRoot) Hash() Hash {
	return taggedHash(domainEpochRoot,
		u32Bytes(r.Epoch),
		r.TreeRoot[:],
		u64Bytes(r.RevocCounter),
		r.MigrationHash[:],
		r.PrevRoot[:])
}

// signedRootContext is prepended to the signed message so signatures made in
// this protocol role cannot be confused with other Ed25519 uses.
var signedRootContext = []byte("KT-SIGNED-ROOT-v1")

// SignedRoot is an EpochRoot together with the directory key's Ed25519
// signature over its hash and the signer public key.
type SignedRoot struct {
	Body      EpochRoot
	Signer    ed25519.PublicKey
	Signature []byte
}

// SigningMessage returns the exact byte string that is signed.
func (r EpochRoot) SigningMessage() []byte {
	h := r.Hash()
	out := make([]byte, 0, len(signedRootContext)+len(h))
	out = append(out, signedRootContext...)
	out = append(out, h[:]...)
	return out
}

// SignSignedRoot produces a SignedRoot using an Ed25519 private key.
func SignSignedRoot(body EpochRoot, signer ed25519.PrivateKey) (*SignedRoot, error) {
	if len(signer) != ed25519.PrivateKeySize {
		return nil, ErrBadKey
	}
	sig := ed25519.Sign(signer, body.SigningMessage())
	pub := append(ed25519.PublicKey(nil), signer.Public().(ed25519.PublicKey)...)
	return &SignedRoot{Body: body, Signer: pub, Signature: sig}, nil
}

// VerifySignature checks the embedded Ed25519 signature against trustedKey
// (when non-nil the signer must also equal it).
func (s *SignedRoot) VerifySignature(trustedKey ed25519.PublicKey) error {
	if len(s.Signer) != ed25519.PublicKeySize || len(s.Signature) != ed25519.SignatureSize {
		return ErrBadSignature
	}
	if trustedKey != nil && !equalBytes(s.Signer, trustedKey) {
		return ErrUntrustedSigner
	}
	if !ed25519.Verify(s.Signer, s.Body.SigningMessage(), s.Signature) {
		return ErrBadSignature
	}
	return nil
}

// VerifyRootChain validates a consecutive signed root chain:
//
//   - length is within 1..MaxEpochs and starts at FirstEpoch;
//   - every entry carries a valid signature from trustedKey;
//   - epoch numbers are consecutive and each PrevRoot equals the previous
//     entry's hash;
//   - revocation counters are monotonically non-decreasing.
func VerifyRootChain(chain []*SignedRoot, trustedKey ed25519.PublicKey) error {
	if len(chain) == 0 || len(chain) > MaxEpochs {
		return ErrEpochRange
	}
	var zero Hash
	var prev Hash
	var prevCounter uint64
	for i, sr := range chain {
		if sr == nil {
			return ErrNilEntry
		}
		if err := sr.VerifySignature(trustedKey); err != nil {
			return err
		}
		body := sr.Body
		if body.Epoch != FirstEpoch+uint32(i) {
			return ErrEpochSequence
		}
		if i == 0 {
			if body.PrevRoot != zero {
				return ErrPrevRoot
			}
		} else if body.PrevRoot != prev {
			return ErrPrevRoot
		}
		if body.RevocCounter < prevCounter {
			return ErrCounterNotMonotonic
		}
		prev = body.Hash()
		prevCounter = body.RevocCounter
	}
	return nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

var (
	// ErrBadKey indicates a malformed cryptographic key.
	ErrBadKey = prefixError("kt: malformed cryptographic key")
	// ErrBadSignature indicates a signature that failed verification.
	ErrBadSignature = prefixError("kt: invalid signature")
	// ErrUntrustedSigner indicates the signer is not the trusted directory key.
	ErrUntrustedSigner = prefixError("kt: untrusted signer")
	// ErrEpochRange indicates the chain length is outside 1..MaxEpochs.
	ErrEpochRange = prefixError("kt: epoch chain length out of 1..8")
	// ErrEpochSequence indicates non-consecutive or mis-numbered epochs.
	ErrEpochSequence = prefixError("kt: non-consecutive epoch numbers")
	// ErrPrevRoot indicates a broken previous-root link.
	ErrPrevRoot = prefixError("kt: previous-root link mismatch")
	// ErrCounterNotMonotonic indicates a decreasing revocation counter.
	ErrCounterNotMonotonic = prefixError("kt: revocation counter not monotonic")
	// ErrNilEntry indicates a nil chain or migration entry.
	ErrNilEntry = prefixError("kt: nil entry")
)
