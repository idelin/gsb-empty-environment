package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
)

const (
	// ProofDomain is the fixed domain separator for Chaum-Pedersen challenges.
	ProofDomain = "netsec/q0054/p256-threshold-elgamal/chaum-pedersen/v1"
	// ManifestDomain is the fixed domain separator for manifest digests.
	ManifestDomain = "netsec/q0054/p256-threshold-elgamal/manifest/v1"
	// CiphertextDomain is the fixed domain separator for ciphertext digests.
	CiphertextDomain = "netsec/q0054/p256-threshold-elgamal/ciphertext/v1"
)

// Ciphertext is an additive ElGamal ciphertext (C1, C2) = (kG, M + kY).
type Ciphertext struct {
	C1 Point
	C2 Point
}

// Proof is a Chaum-Pedersen proof of log_G(Y_i) = log_C1(D_i).
type Proof struct {
	A Point
	B Point
	Z Scalar
}

// DecryptionShare is a member's contribution D_i = x_i C1 and its proof.
type DecryptionShare struct {
	ID    uint32
	D     Point
	Proof Proof
}

func validateCiphertext(ciphertext Ciphertext) error {
	if err := ciphertext.C1.valid(); err != nil {
		return fmt.Errorf("%w: C1: %v", ErrInvalidInput, err)
	}
	if err := ciphertext.C2.valid(); err != nil {
		return fmt.Errorf("%w: C2: %v", ErrInvalidInput, err)
	}
	return nil
}

// ManifestDigest hashes the fixed-length canonical public manifest encoding.
func ManifestDigest(manifest Manifest) ([32]byte, error) {
	var out [32]byte
	if err := validateManifestStructure(manifest); err != nil {
		return out, err
	}

	hash := sha256.New()
	hash.Write([]byte(ManifestDomain))
	writeUint32(hash, uint32(manifest.Threshold))
	writeUint32(hash, uint32(len(manifest.Members)))
	hash.Write(manifest.Y[:])
	for _, member := range manifest.Members {
		writeUint32(hash, member.ID)
		hash.Write(member.Y[:])
	}
	copy(out[:], hash.Sum(nil))
	return out, nil
}

// CiphertextDigest hashes the fixed-length canonical ciphertext encoding.
func CiphertextDigest(ciphertext Ciphertext) ([32]byte, error) {
	var out [32]byte
	if err := validateCiphertext(ciphertext); err != nil {
		return out, err
	}
	hash := sha256.New()
	hash.Write([]byte(CiphertextDomain))
	hash.Write(ciphertext.C1[:])
	hash.Write(ciphertext.C2[:])
	copy(out[:], hash.Sum(nil))
	return out, nil
}

// ComputeChallenge computes
//
//	SHA256(domain || H(manifest) || H(ciphertext) || i || Y_i || D_i || A || B) mod n
//
// using fixed-width encodings for every field.
func ComputeChallenge(
	manifest Manifest,
	ciphertext Ciphertext,
	id uint32,
	publicShare Point,
	shareValue Point,
	commitmentA Point,
	commitmentB Point,
) (Scalar, error) {
	var out Scalar
	manifestHash, err := ManifestDigest(manifest)
	if err != nil {
		return out, err
	}
	ciphertextHash, err := CiphertextDigest(ciphertext)
	if err != nil {
		return out, err
	}
	if id == 0 {
		return out, fmt.Errorf("%w: member identifier must be non-zero", ErrInvalidInput)
	}
	for _, field := range []struct {
		name  string
		point Point
	}{
		{name: "public share", point: publicShare},
		{name: "share value", point: shareValue},
		{name: "commitment A", point: commitmentA},
		{name: "commitment B", point: commitmentB},
	} {
		if err := field.point.valid(); err != nil {
			return out, fmt.Errorf("%w: %s: %v", ErrInvalidInput, field.name, err)
		}
	}

	hash := sha256.New()
	hash.Write([]byte(ProofDomain))
	hash.Write(manifestHash[:])
	hash.Write(ciphertextHash[:])
	writeUint32(hash, id)
	hash.Write(publicShare[:])
	hash.Write(shareValue[:])
	hash.Write(commitmentA[:])
	hash.Write(commitmentB[:])

	value := new(big.Int).SetBytes(hash.Sum(nil))
	value.Mod(value, curveOrder)
	return ScalarFromBig(value)
}

func writeUint32(writer interface{ Write([]byte) (int, error) }, value uint32) {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	writer.Write(encoded[:])
}

// Encrypt produces (kG, M + kY) for the manifest's aggregate public key.
func Encrypt(manifest Manifest, message Point, source io.Reader) (Ciphertext, error) {
	if err := ValidateManifest(manifest); err != nil {
		return Ciphertext{}, err
	}
	if err := message.valid(); err != nil {
		return Ciphertext{}, fmt.Errorf("%w: message: %v", ErrInvalidInput, err)
	}
	aggregate, err := pointFromCanonical(manifest.Y)
	if err != nil {
		return Ciphertext{}, err
	}
	messagePoint, err := pointFromCanonical(message)
	if err != nil {
		return Ciphertext{}, err
	}

	k, err := randomNonZeroScalar(source)
	if err != nil {
		return Ciphertext{}, err
	}
	c1, err := pointToCanonical(scalarBaseMultCurve(k))
	if err != nil {
		return Ciphertext{}, err
	}
	ky := scalarMultCurve(k, aggregate)
	c2, err := pointToCanonical(addCurvePoints(messagePoint, ky))
	if err != nil {
		return Ciphertext{}, fmt.Errorf("%w: C2 is the identity", ErrInvalidInput)
	}
	return Ciphertext{C1: c1, C2: c2}, nil
}

// Decrypt creates this member's decryption share and a proof bound to the
// manifest and ciphertext.
func (s SecretShare) Decrypt(
	manifest Manifest,
	ciphertext Ciphertext,
	source io.Reader,
) (DecryptionShare, error) {
	if err := ValidateManifest(manifest); err != nil {
		return DecryptionShare{}, err
	}
	if err := validateCiphertext(ciphertext); err != nil {
		return DecryptionShare{}, err
	}
	if !s.X.valid() || s.X.IsZero() {
		return DecryptionShare{}, fmt.Errorf("%w: secret share must be non-zero", ErrInvalidScalar)
	}

	member, found := findMember(manifest, s.ID)
	if !found {
		return DecryptionShare{}, fmt.Errorf("%w: member %d is not in the manifest", ErrInvalidInput, s.ID)
	}
	expectedY, err := pointToCanonical(scalarBaseMultCurve(s.X.Big()))
	if err != nil {
		return DecryptionShare{}, err
	}
	if expectedY != member.Y {
		return DecryptionShare{}, fmt.Errorf("%w: secret share does not match member %d public share", ErrInvalidInput, s.ID)
	}

	return createDecryptionShare(manifest, ciphertext, member, s.X, source)
}

func createDecryptionShare(
	manifest Manifest,
	ciphertext Ciphertext,
	member PublicShare,
	secret Scalar,
	source io.Reader,
) (DecryptionShare, error) {
	c1, err := pointFromCanonical(ciphertext.C1)
	if err != nil {
		return DecryptionShare{}, err
	}
	d, err := pointToCanonical(scalarMultCurve(secret.Big(), c1))
	if err != nil {
		return DecryptionShare{}, fmt.Errorf("%w: D_i is the identity", ErrInvalidInput)
	}

	r, err := randomNonZeroScalar(source)
	if err != nil {
		return DecryptionShare{}, err
	}
	a, err := pointToCanonical(scalarBaseMultCurve(r))
	if err != nil {
		return DecryptionShare{}, err
	}
	b, err := pointToCanonical(scalarMultCurve(r, c1))
	if err != nil {
		return DecryptionShare{}, err
	}
	challenge, err := ComputeChallenge(manifest, ciphertext, member.ID, member.Y, d, a, b)
	if err != nil {
		return DecryptionShare{}, err
	}
	response := modOrder(new(big.Int).Add(r, new(big.Int).Mul(challenge.Big(), secret.Big())))
	z, err := ScalarFromBig(response)
	if err != nil {
		return DecryptionShare{}, err
	}
	return DecryptionShare{
		ID: member.ID,
		D:  d,
		Proof: Proof{
			A: a,
			B: b,
			Z: z,
		},
	}, nil
}

// VerifyShare verifies a member's Chaum-Pedersen proof against the manifest
// and ciphertext.
func VerifyShare(manifest Manifest, ciphertext Ciphertext, share DecryptionShare) error {
	if err := ValidateManifest(manifest); err != nil {
		return err
	}
	if err := validateCiphertext(ciphertext); err != nil {
		return err
	}
	return verifyShareUnchecked(manifest, ciphertext, share)
}

func verifyShareUnchecked(manifest Manifest, ciphertext Ciphertext, share DecryptionShare) error {
	member, found := findMember(manifest, share.ID)
	if !found {
		return fmt.Errorf("%w: member %d is not in the manifest", ErrInvalidInput, share.ID)
	}
	if err := share.D.valid(); err != nil {
		return fmt.Errorf("%w: D_i: %v", ErrInvalidInput, err)
	}
	if err := share.Proof.A.valid(); err != nil {
		return fmt.Errorf("%w: A: %v", ErrInvalidInput, err)
	}
	if err := share.Proof.B.valid(); err != nil {
		return fmt.Errorf("%w: B: %v", ErrInvalidInput, err)
	}
	if !share.Proof.Z.valid() {
		return fmt.Errorf("%w: response is not canonical", ErrInvalidScalar)
	}

	challenge, err := ComputeChallenge(
		manifest,
		ciphertext,
		member.ID,
		member.Y,
		share.D,
		share.Proof.A,
		share.Proof.B,
	)
	if err != nil {
		return err
	}

	base, err := pointFromCanonical(BasePoint())
	if err != nil {
		return err
	}
	c1, err := pointFromCanonical(ciphertext.C1)
	if err != nil {
		return err
	}
	y, err := pointFromCanonical(member.Y)
	if err != nil {
		return err
	}
	d, err := pointFromCanonical(share.D)
	if err != nil {
		return err
	}
	a, err := pointFromCanonical(share.Proof.A)
	if err != nil {
		return err
	}
	b, err := pointFromCanonical(share.Proof.B)
	if err != nil {
		return err
	}
	c := challenge.Big()

	leftOne := scalarMultCurve(share.Proof.Z.Big(), base)
	rightOne := addCurvePoints(a, scalarMultCurve(c, y))
	if !curvePointsEqual(leftOne, rightOne) {
		return fmt.Errorf("%w: first Chaum-Pedersen equation failed", ErrInvalidInput)
	}

	leftTwo := scalarMultCurve(share.Proof.Z.Big(), c1)
	rightTwo := addCurvePoints(b, scalarMultCurve(c, d))
	if !curvePointsEqual(leftTwo, rightTwo) {
		return fmt.Errorf("%w: second Chaum-Pedersen equation failed", ErrInvalidInput)
	}
	return nil
}

func curvePointsEqual(a, b curvePoint) bool {
	if a.isIdentity() || b.isIdentity() {
		return a.isIdentity() && b.isIdentity()
	}
	return a.x.Cmp(b.x) == 0 && a.y.Cmp(b.y) == 0
}

func findMember(manifest Manifest, id uint32) (PublicShare, bool) {
	for _, member := range manifest.Members {
		if member.ID == id {
			return member, true
		}
	}
	return PublicShare{}, false
}
