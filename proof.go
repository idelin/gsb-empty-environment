package netsec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
)

// MembershipProof proves knowledge of W such that W^x = A. Identity is the
// host namespace the proof was minted for. The proof deliberately contains no
// root: verification obtains A from the trusted verifier configuration.
type MembershipProof struct {
	Identity
	X uint32
	W *big.Int
}

// NonMembershipProof proves D^x*A^b = g for a*x+b*P = 1 and 0 <= b < x.
// As with MembershipProof, the root used for verification is trusted
// configuration, not data supplied by the proof.
type NonMembershipProof struct {
	Identity
	X uint32
	D *big.Int
	B *big.Int
}

// ProveMembership generates W = g^(P/x) for a member x. It refuses to create
// a proof for an absent or non-prime value.
func (a *Accumulator) ProveMembership(x uint32) (MembershipProof, error) {
	if a == nil {
		return MembershipProof{}, errors.New("netsec: nil accumulator")
	}
	if !IsPrime32(x) {
		return MembershipProof{}, fmt.Errorf("%w: %d is not a 32-bit prime", ErrInvalidElement, x)
	}
	index := searchElement(a.elements, x)
	if index < 0 {
		return MembershipProof{}, fmt.Errorf("%w: %d", ErrNotMember, x)
	}

	exponent := new(big.Int).Quo(a.product, new(big.Int).SetUint64(uint64(x)))
	witness := new(big.Int).Exp(a.params.G, exponent, a.params.N)
	return MembershipProof{
		Identity: a.identity,
		X:        x,
		W:        witness,
	}, nil
}

// GenerateMembershipProof is an alias for ProveMembership.
func (a *Accumulator) GenerateMembershipProof(x uint32) (MembershipProof, error) {
	return a.ProveMembership(x)
}

// ProveNonMembership generates a non-membership proof for an absent prime.
// It explicitly refuses to generate one for a set member: in that case
// gcd(x, P) = x, so no Bezout coefficients with gcd 1 exist.
func (a *Accumulator) ProveNonMembership(x uint32) (NonMembershipProof, error) {
	if a == nil {
		return NonMembershipProof{}, errors.New("netsec: nil accumulator")
	}
	if !IsPrime32(x) {
		return NonMembershipProof{}, fmt.Errorf("%w: %d is not a 32-bit prime", ErrInvalidElement, x)
	}
	if a.Contains(x) {
		return NonMembershipProof{}, fmt.Errorf("%w: %d", ErrMember, x)
	}

	xBig := new(big.Int).SetUint64(uint64(x))
	gcd := new(big.Int)
	coefX := new(big.Int)
	coefP := new(big.Int)
	gcd.GCD(coefX, coefP, xBig, a.product)
	if gcd.Cmp(big.NewInt(1)) != 0 {
		return NonMembershipProof{}, fmt.Errorf("%w: gcd(%d, P) != 1", ErrMember, x)
	}

	// Normalize b into [0, x). Since x is prime and absent, the result is
	// actually in [1, x-1]. Recompute a exactly so a*x + b*P = 1.
	b := new(big.Int).Mod(coefP, xBig)
	bP := new(big.Int).Mul(b, a.product)
	numerator := new(big.Int).Sub(big.NewInt(1), bP)
	aCoefficient := new(big.Int).Quo(numerator, xBig)
	check := new(big.Int).Add(
		new(big.Int).Mul(aCoefficient, xBig),
		new(big.Int).Mul(b, a.product),
	)
	if check.Cmp(big.NewInt(1)) != 0 {
		return NonMembershipProof{}, errors.New("netsec: internal Bezout normalization error")
	}
	if b.Sign() < 0 || b.Cmp(xBig) >= 0 {
		return NonMembershipProof{}, invalidProof(ErrInvalidRange)
	}

	d, err := expSigned(a.params.G, aCoefficient, a.params.N)
	if err != nil {
		return NonMembershipProof{}, err
	}
	return NonMembershipProof{
		Identity: a.identity,
		X:        x,
		D:        d,
		B:        b,
	}, nil
}

// GenerateNonMembershipProof is an alias for ProveNonMembership.
func (a *Accumulator) GenerateNonMembershipProof(x uint32) (NonMembershipProof, error) {
	return a.ProveNonMembership(x)
}

// VerifyMembership checks the proof only against this accumulator's trusted
// identity and root. It never uses identity data from the proof to select the
// root.
func (a *Accumulator) VerifyMembership(proof MembershipProof) error {
	if a == nil {
		return invalidProof(errors.New("netsec: nil accumulator"))
	}
	v, err := NewVerifierFromParams(a.params, a.root, a.identity)
	if err != nil {
		return invalidProof(err)
	}
	return v.VerifyMembership(proof)
}

// VerifyMembershipProof is an alias for VerifyMembership.
func (a *Accumulator) VerifyMembershipProof(proof MembershipProof) error {
	return a.VerifyMembership(proof)
}

// VerifyMembershipFor additionally binds the requested credential identifier
// to proof.X, preventing a caller from accidentally accepting a proof for a
// different member.
func (a *Accumulator) VerifyMembershipFor(x uint32, proof MembershipProof) error {
	if proof.X != x {
		return invalidProof(fmt.Errorf("%w: expected x=%d, proof has x=%d", ErrInvalidElement, x, proof.X))
	}
	return a.VerifyMembership(proof)
}

// VerifyNonMembership checks the proof only against this accumulator's trusted
// identity and root.
func (a *Accumulator) VerifyNonMembership(proof NonMembershipProof) error {
	if a == nil {
		return invalidProof(errors.New("netsec: nil accumulator"))
	}
	v, err := NewVerifierFromParams(a.params, a.root, a.identity)
	if err != nil {
		return invalidProof(err)
	}
	return v.VerifyNonMembership(proof)
}

// VerifyNonMembershipProof is an alias for VerifyNonMembership.
func (a *Accumulator) VerifyNonMembershipProof(proof NonMembershipProof) error {
	return a.VerifyNonMembership(proof)
}

// VerifyNonMembershipFor binds the requested credential identifier to proof.X.
func (a *Accumulator) VerifyNonMembershipFor(x uint32, proof NonMembershipProof) error {
	if proof.X != x {
		return invalidProof(fmt.Errorf("%w: expected x=%d, proof has x=%d", ErrInvalidElement, x, proof.X))
	}
	return a.VerifyNonMembership(proof)
}

// Verifier checks proofs without possessing the full set. Its root and
// identity come entirely from trusted host configuration.
type Verifier struct {
	params   Params
	root     *big.Int
	identity Identity
}

// NewVerifierFromParams builds a verifier and verifies that expected names the
// supplied trusted root. The proof is not consulted to establish any of this
// context.
func NewVerifierFromParams(params Params, root *big.Int, expected Identity) (*Verifier, error) {
	n, g, err := validateParams(params.N, params.G)
	if err != nil {
		return nil, err
	}
	if root == nil {
		return nil, invalidProof(errors.New("netsec: nil root"))
	}
	if err := checkUnit(root, n); err != nil {
		return nil, invalidProof(err)
	}
	digest, err := ComputeRootDigest(n, g, root)
	if err != nil {
		return nil, err
	}
	if digest != expected.RootDigest {
		return nil, identityError(ErrRootDigestMismatch, "trusted root does not match expected identity")
	}
	return &Verifier{
		params:   Params{N: n, G: g},
		root:     new(big.Int).Set(root),
		identity: expected,
	}, nil
}

// NewVerifier is a convenience wrapper around NewVerifierFromParams.
func NewVerifier(n, g, root *big.Int, expected Identity) (*Verifier, error) {
	params, err := NewParams(n, g)
	if err != nil {
		return nil, err
	}
	return NewVerifierFromParams(params, root, expected)
}

// VerifyMembership verifies W^x = A for the verifier's trusted snapshot.
func (v *Verifier) VerifyMembership(proof MembershipProof) error {
	if v == nil {
		return invalidProof(errors.New("netsec: nil verifier"))
	}
	if err := v.checkIdentity(proof.Identity); err != nil {
		return err
	}
	if !IsPrime32(proof.X) {
		return invalidProof(fmt.Errorf("%w: %d is not a 32-bit prime", ErrInvalidElement, proof.X))
	}
	if err := checkProofUnit(proof.W, v.params.N, "W"); err != nil {
		return err
	}

	lhs := new(big.Int).Exp(proof.W, new(big.Int).SetUint64(uint64(proof.X)), v.params.N)
	if lhs.Cmp(v.root) != 0 {
		return invalidProof(ErrEquationFailed)
	}
	return nil
}

// VerifyNonMembership verifies D^x*A^b = g for 0 <= b < x. b is rejected if
// negative or equal to x; D must be a unit.
func (v *Verifier) VerifyNonMembership(proof NonMembershipProof) error {
	if v == nil {
		return invalidProof(errors.New("netsec: nil verifier"))
	}
	if err := v.checkIdentity(proof.Identity); err != nil {
		return err
	}
	if !IsPrime32(proof.X) {
		return invalidProof(fmt.Errorf("%w: %d is not a 32-bit prime", ErrInvalidElement, proof.X))
	}
	if proof.B == nil {
		return invalidProof(errors.New("netsec: nil b"))
	}
	xBig := new(big.Int).SetUint64(uint64(proof.X))
	if proof.B.Sign() < 0 {
		return invalidProof(fmt.Errorf("%w: b must be nonnegative", ErrInvalidRange))
	}
	if proof.B.Cmp(xBig) >= 0 {
		return invalidProof(fmt.Errorf("%w: b must be less than x", ErrInvalidRange))
	}
	if err := checkProofUnit(proof.D, v.params.N, "D"); err != nil {
		return err
	}

	dx := new(big.Int).Exp(proof.D, xBig, v.params.N)
	ab := new(big.Int).Exp(v.root, proof.B, v.params.N)
	lhs := new(big.Int).Mul(dx, ab)
	lhs.Mod(lhs, v.params.N)
	if lhs.Cmp(new(big.Int).Mod(v.params.G, v.params.N)) != 0 {
		return invalidProof(ErrEquationFailed)
	}
	return nil
}

func (v *Verifier) checkIdentity(got Identity) error {
	if got.SetID != v.identity.SetID {
		return identityError(ErrSetIDMismatch, fmt.Sprintf("set ID %q does not match %q", got.SetID, v.identity.SetID))
	}
	if got.Generation != v.identity.Generation {
		return identityError(ErrGenerationMismatch, fmt.Sprintf("generation %d does not match %d", got.Generation, v.identity.Generation))
	}
	if got.RootDigest != v.identity.RootDigest {
		return identityError(ErrRootDigestMismatch, "proof root digest does not match trusted snapshot")
	}
	return nil
}

func checkProofUnit(value, n *big.Int, name string) error {
	if value == nil {
		return invalidProof(fmt.Errorf("%w: nil %s", ErrNotUnit, name))
	}
	if err := checkUnit(value, n); err != nil {
		return invalidProof(fmt.Errorf("%w: %s", err, name))
	}
	return nil
}

// VerifyMembershipProof verifies a membership proof using only trusted host
// configuration. It is safe against proof-supplied root selection: root is an
// explicit argument, while the proof's identity must match expected.
func VerifyMembershipProof(n, g, root *big.Int, expected Identity, proof MembershipProof) error {
	v, err := NewVerifier(n, g, root, expected)
	if err != nil {
		return err
	}
	return v.VerifyMembership(proof)
}

// VerifyNonMembershipProof is the analogous stateless entry point for a
// non-membership proof.
func VerifyNonMembershipProof(n, g, root *big.Int, expected Identity, proof NonMembershipProof) error {
	v, err := NewVerifier(n, g, root, expected)
	if err != nil {
		return err
	}
	return v.VerifyNonMembership(proof)
}

// VerifyMembershipAt derives the trusted identity digest from the host's
// set ID, generation, and root. This avoids asking a caller to copy a digest
// from a proof.
func VerifyMembershipAt(n, g, root *big.Int, setID string, generation uint64, proof MembershipProof) error {
	id, err := HostIdentity(n, g, root, setID, generation)
	if err != nil {
		return err
	}
	return VerifyMembershipProof(n, g, root, id, proof)
}

// VerifyNonMembershipAt is the non-membership counterpart to
// VerifyMembershipAt.
func VerifyNonMembershipAt(n, g, root *big.Int, setID string, generation uint64, proof NonMembershipProof) error {
	id, err := HostIdentity(n, g, root, setID, generation)
	if err != nil {
		return err
	}
	return VerifyNonMembershipProof(n, g, root, id, proof)
}

// HostIdentity constructs the identity tuple from trusted host state.
func HostIdentity(n, g, root *big.Int, setID string, generation uint64) (Identity, error) {
	params, err := NewParams(n, g)
	if err != nil {
		return Identity{}, err
	}
	if root == nil || checkUnit(root, params.N) != nil {
		return Identity{}, invalidProof(errors.New("netsec: invalid trusted root"))
	}
	digest, err := ComputeRootDigest(params.N, params.G, root)
	if err != nil {
		return Identity{}, err
	}
	return Identity{SetID: setID, Generation: generation, RootDigest: digest}, nil
}

func searchElement(sorted []uint32, x uint32) int {
	lo, hi := 0, len(sorted)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if sorted[mid] < x {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(sorted) && sorted[lo] == x {
		return lo
	}
	return -1
}

func expSigned(base, exponent, modulus *big.Int) (*big.Int, error) {
	if base == nil || exponent == nil || modulus == nil {
		return nil, errors.New("netsec: nil exponentiation input")
	}
	abs := new(big.Int).Abs(exponent)
	effectiveBase := new(big.Int).Set(base)
	if exponent.Sign() < 0 {
		inverse := new(big.Int).ModInverse(base, modulus)
		if inverse == nil {
			return nil, ErrNotUnit
		}
		effectiveBase = inverse
	}
	return new(big.Int).Exp(effectiveBase, abs, modulus), nil
}

// NewMembershipProofFromStrings builds a proof from canonical decimal
// witnesses. Non-canonical integers such as "01" or "+7" are rejected.
func NewMembershipProofFromStrings(id Identity, x uint32, w string) (MembershipProof, error) {
	if !IsPrime32(x) {
		return MembershipProof{}, invalidProof(fmt.Errorf("%w: %d is not a 32-bit prime", ErrInvalidElement, x))
	}
	witness, err := parseCanonicalPositive(w, "W")
	if err != nil {
		return MembershipProof{}, invalidProof(err)
	}
	return MembershipProof{Identity: id, X: x, W: witness}, nil
}

// NewNonMembershipProofFromStrings is the non-membership counterpart. b is
// parsed as a canonical signed integer; verification enforces 0 <= b < x.
func NewNonMembershipProofFromStrings(id Identity, x uint32, d, b string) (NonMembershipProof, error) {
	if !IsPrime32(x) {
		return NonMembershipProof{}, invalidProof(fmt.Errorf("%w: %d is not a 32-bit prime", ErrInvalidElement, x))
	}
	dValue, err := parseCanonicalPositive(d, "D")
	if err != nil {
		return NonMembershipProof{}, invalidProof(err)
	}
	bValue, err := ParseCanonicalInt(b)
	if err != nil {
		return NonMembershipProof{}, invalidProof(err)
	}
	return NonMembershipProof{Identity: id, X: x, D: dValue, B: bValue}, nil
}

type membershipProofWire struct {
	SetID      string `json:"set_id"`
	Generation uint64 `json:"generation"`
	RootDigest string `json:"root_digest"`
	X          uint32 `json:"x"`
	W          string `json:"w"`
}

// MarshalJSON emits canonical decimal witnesses and a lowercase digest hex.
func (p MembershipProof) MarshalJSON() ([]byte, error) {
	if p.W == nil {
		return nil, invalidProof(errors.New("netsec: nil W"))
	}
	return json.Marshal(membershipProofWire{
		SetID:      p.SetID,
		Generation: p.Generation,
		RootDigest: p.RootDigestHex(),
		X:          p.X,
		W:          p.W.String(),
	})
}

// UnmarshalJSON enforces canonical integer and digest encodings.
func (p *MembershipProof) UnmarshalJSON(data []byte) error {
	if p == nil {
		return invalidProof(errors.New("netsec: nil proof"))
	}
	var wire membershipProofWire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return invalidProof(err)
	}
	if !IsPrime32(wire.X) {
		return invalidProof(fmt.Errorf("%w: %d is not a 32-bit prime", ErrInvalidElement, wire.X))
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return invalidProof(ErrNonCanonical)
		}
		return invalidProof(err)
	}
	digest, err := parseRootDigest(wire.RootDigest)
	if err != nil {
		return invalidProof(err)
	}
	witness, err := parseCanonicalPositive(wire.W, "W")
	if err != nil {
		return invalidProof(err)
	}
	p.Identity = Identity{SetID: wire.SetID, Generation: wire.Generation, RootDigest: digest}
	p.X = wire.X
	p.W = witness
	return nil
}

type nonMembershipProofWire struct {
	SetID      string `json:"set_id"`
	Generation uint64 `json:"generation"`
	RootDigest string `json:"root_digest"`
	X          uint32 `json:"x"`
	D          string `json:"d"`
	B          string `json:"b"`
}

// MarshalJSON emits canonical decimal witnesses and a lowercase digest hex.
func (p NonMembershipProof) MarshalJSON() ([]byte, error) {
	if p.D == nil || p.B == nil {
		return nil, invalidProof(errors.New("netsec: nil non-membership witness"))
	}
	return json.Marshal(nonMembershipProofWire{
		SetID:      p.SetID,
		Generation: p.Generation,
		RootDigest: p.RootDigestHex(),
		X:          p.X,
		D:          p.D.String(),
		B:          p.B.String(),
	})
}

// UnmarshalJSON enforces canonical integer and digest encodings.
func (p *NonMembershipProof) UnmarshalJSON(data []byte) error {
	if p == nil {
		return invalidProof(errors.New("netsec: nil proof"))
	}
	var wire nonMembershipProofWire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return invalidProof(err)
	}
	if !IsPrime32(wire.X) {
		return invalidProof(fmt.Errorf("%w: %d is not a 32-bit prime", ErrInvalidElement, wire.X))
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return invalidProof(ErrNonCanonical)
		}
		return invalidProof(err)
	}
	digest, err := parseRootDigest(wire.RootDigest)
	if err != nil {
		return invalidProof(err)
	}
	dValue, err := parseCanonicalPositive(wire.D, "D")
	if err != nil {
		return invalidProof(err)
	}
	bValue, err := ParseCanonicalInt(wire.B)
	if err != nil {
		return invalidProof(err)
	}
	p.Identity = Identity{SetID: wire.SetID, Generation: wire.Generation, RootDigest: digest}
	p.X = wire.X
	p.D = dValue
	p.B = bValue
	return nil
}
