package netsec

import (
	"crypto/sha3"
	"crypto/subtle"
	"errors"
	"fmt"
)

// ML-KEM-768 sizes from FIPS 203.
const (
	MLKEM768CiphertextSize       = 1088
	MLKEM768EncapsulationKeySize = 1184
	MLKEM768DecapsulationKeySize = 2400
	MLKEM768PKEKeySize           = 1152
	MLKEM768SharedKeySize        = 32
	MLKEM768HashSize             = 32

	CiphertextSize       = MLKEM768CiphertextSize
	EncapsulationKeySize = MLKEM768EncapsulationKeySize
	DecapsulationKeySize = MLKEM768DecapsulationKeySize
	PKEKeySize           = MLKEM768PKEKeySize
	SharedKeySize        = MLKEM768SharedKeySize
	HashSize             = MLKEM768HashSize

	mlKEM768PKEDecapsulationKeyOffset = 0
	mlKEM768EncapsulationKeyOffset    = mlKEM768PKEDecapsulationKeyOffset + MLKEM768PKEKeySize
	mlKEM768HashOffset                = mlKEM768EncapsulationKeyOffset + MLKEM768EncapsulationKeySize
	mlKEM768SeedOffset                = mlKEM768HashOffset + MLKEM768HashSize
)

var (
	// ErrInvalidCiphertextLength means the ciphertext is not ML-KEM-768 sized.
	ErrInvalidCiphertextLength = errors.New("netsec: invalid ML-KEM-768 ciphertext length")
	// ErrInvalidDecapsulationKey means the decapsulation key is malformed or
	// its embedded H(ek) does not match ek.
	ErrInvalidDecapsulationKey = errors.New("netsec: invalid ML-KEM-768 decapsulation key")
	// ErrEmbeddedHashMismatch means H(ek) stored in the decapsulation key
	// does not match the embedded ek. It also matches
	// ErrInvalidDecapsulationKey through errors.Is.
	ErrEmbeddedHashMismatch = fmt.Errorf("%w: embedded H(ek) does not match ek", ErrInvalidDecapsulationKey)
	// ErrInvalidPrimitives means a required injected primitive is nil.
	ErrInvalidPrimitives = errors.New("netsec: invalid ML-KEM primitives")
	// ErrInvalidPRFOutput means the injected PRF did not produce a 32-byte key.
	ErrInvalidPRFOutput = errors.New("netsec: invalid PRF output")
)

// KPKEOperations are the caller-supplied K-PKE operations used by
// decapsulation. Decrypt must accept every correctly sized ciphertext and be
// free of ciphertext-dependent errors; Encrypt must be deterministic for the
// supplied encapsulation key, message, and randomness. Both operations should
// run in constant time with respect to secret inputs.
type KPKEOperations interface {
	Encrypt(encapsulationKey, message, randomness []byte) ([]byte, error)
	Decrypt(decapsulationKey, ciphertext []byte) ([]byte, error)
}

// KPKE is an alias for KPKEOperations.
type KPKE = KPKEOperations

// PRF derives the implicit-rejection key from z and the received ciphertext.
// It must return MLKEM768SharedKeySize bytes and must be deterministic.
type PRF func(z, ciphertext []byte) []byte

// Decapsulator performs ML-KEM-768 decapsulation with caller-supplied K-PKE
// and PRF implementations. It is safe for concurrent use when the injected
// implementations are safe for concurrent use.
type Decapsulator struct {
	pke KPKEOperations
	prf PRF
}

// NewDecapsulator binds the K-PKE operations and implicit-rejection PRF used
// by the handshake handler.
func NewDecapsulator(pke KPKEOperations, prf PRF) (*Decapsulator, error) {
	if pke == nil || prf == nil {
		return nil, ErrInvalidPrimitives
	}
	return &Decapsulator{pke: pke, prf: prf}, nil
}

// Decapsulate consumes an ML-KEM-768 decapsulation key and ciphertext.
//
// A malformed key or ciphertext length is rejected with an error. Once those
// public, structural checks and the embedded H(ek) integrity check pass, a
// tampered ciphertext is never reported as an error: it produces the
// deterministic implicit-rejection key PRF(z, ciphertext) instead.
//
// The received ciphertext and the re-encrypted ciphertext are compared without
// a data-dependent byte-wise early exit, and the success/rejection key choice
// is made with a constant-time select.
func (d *Decapsulator) Decapsulate(decapsulationKey, ciphertext []byte) ([]byte, error) {
	if d == nil || d.pke == nil || d.prf == nil {
		return nil, ErrInvalidPrimitives
	}
	if len(ciphertext) != MLKEM768CiphertextSize {
		return nil, ErrInvalidCiphertextLength
	}
	if len(decapsulationKey) != MLKEM768DecapsulationKeySize {
		return nil, ErrInvalidDecapsulationKey
	}

	dkPKE := decapsulationKey[mlKEM768PKEDecapsulationKeyOffset:mlKEM768EncapsulationKeyOffset]
	ek := decapsulationKey[mlKEM768EncapsulationKeyOffset:mlKEM768HashOffset]
	embeddedHash := decapsulationKey[mlKEM768HashOffset:mlKEM768SeedOffset]
	z := decapsulationKey[mlKEM768SeedOffset:]

	computedHash := sha3.Sum256(ek)
	if subtle.ConstantTimeCompare(embeddedHash, computedHash[:]) != 1 {
		return nil, ErrEmbeddedHashMismatch
	}

	mPrime, decryptErr := d.pke.Decrypt(dkPKE, ciphertext)

	// G(m || H(ek)) yields the candidate shared key and K-PKE randomness.
	gInput := make([]byte, len(mPrime)+len(embeddedHash))
	copy(gInput, mPrime)
	copy(gInput[len(mPrime):], embeddedHash)
	g := sha3.Sum512(gInput)
	successKey := g[:MLKEM768SharedKeySize]
	randomness := g[MLKEM768SharedKeySize:]

	ciphertextPrime, encryptErr := d.pke.Encrypt(ek, mPrime, randomness)

	// subtle.ConstantTimeCompare scans all bytes of two equal-length inputs;
	// it has no data-dependent early exit. The length check inside it only
	// depends on public, algorithm-defined sizes.
	equal := subtle.ConstantTimeCompare(ciphertext, ciphertextPrime)
	if decryptErr != nil || encryptErr != nil {
		equal = 0
	}

	// Derive the rejection key even on success so both outcomes take the same
	// PRF path; selection below is data-independent.
	rejectKey := d.prf(z, ciphertext)
	if len(rejectKey) != MLKEM768SharedKeySize {
		return nil, ErrInvalidPRFOutput
	}

	sharedKey := make([]byte, MLKEM768SharedKeySize)
	for i := range sharedKey {
		sharedKey[i] = byte(subtle.ConstantTimeSelect(equal, int(successKey[i]), int(rejectKey[i])))
	}
	return sharedKey, nil
}

// Decapsulate performs one-shot ML-KEM-768 decapsulation with explicitly
// supplied primitives.
func Decapsulate(decapsulationKey, ciphertext []byte, pke KPKEOperations, prf PRF) ([]byte, error) {
	d, err := NewDecapsulator(pke, prf)
	if err != nil {
		return nil, err
	}
	return d.Decapsulate(decapsulationKey, ciphertext)
}

// DecapsulateMLKEM768 is an explicit alias for Decapsulate.
func DecapsulateMLKEM768(decapsulationKey, ciphertext []byte, pke KPKEOperations, prf PRF) ([]byte, error) {
	return Decapsulate(decapsulationKey, ciphertext, pke, prf)
}
