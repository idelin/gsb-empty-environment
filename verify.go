package netsec

import (
	"bytes"
	"fmt"
	"math/big"
	"sort"
)

// MaxEvaluations is the maximum number of input (z, y) pairs in one request.
const MaxEvaluations = 32

// EncodedEvaluation carries canonical scalar encodings supplied by a caller.
// Both fields are decoded and range-checked by the verifier.
type EncodedEvaluation struct {
	Z []byte
	Y []byte
}

// EncodedRequest is untrusted input. It never carries an SRS: the powers of
// tau come only from the Verifier's trusted host setup.
type EncodedRequest struct {
	// Commitment is a canonical G1 encoding of the polynomial commitment C.
	Commitment []byte
	// Proof is a canonical G1 encoding of the quotient commitment pi.
	Proof []byte
	// Evaluations contains 1..MaxEvaluations entries. Exact duplicate
	// (z, y) pairs are merged; equal z with different y values is rejected.
	Evaluations []EncodedEvaluation
}

// Evaluation is one normalized scalar pair. Values are in [0, q-1].
type Evaluation struct {
	Z *big.Int
	Y *big.Int
}

// VerificationResult reports the normalized, sorted, duplicate-free evaluation
// set and the identity of the only SRS used for verification.
type VerificationResult struct {
	Evaluations  []Evaluation
	SRSIdentity  string
	SRSMaxDegree int
}

// Verifier owns the trusted setup and host primitives. It is safe for
// concurrent Verify calls as long as the host implementation is safe for
// concurrent use.
type Verifier[G1, G2, GT any] struct {
	host Host[G1, G2, GT]
	q    *big.Int
	srs  *SRS[G1, G2]
}

// NewVerifier binds a trusted host to a host-owned power-of-tau setup. The
// setup is not part of any request, so callers cannot substitute their own SRS.
func NewVerifier[G1, G2, GT any](host Host[G1, G2, GT], setup *SRS[G1, G2]) (*Verifier[G1, G2, GT], error) {
	if host == nil {
		return nil, fmt.Errorf("%w: nil host", ErrInvalidHost)
	}
	if setup == nil {
		return nil, fmt.Errorf("%w: nil setup", ErrInvalidSRS)
	}
	if setup.Identity() == "" {
		return nil, fmt.Errorf("%w: empty identity", ErrInvalidSRS)
	}

	q := host.PrimeModulus()
	if q == nil || q.Sign() <= 0 || q.Cmp(big.NewInt(2)) < 0 {
		return nil, fmt.Errorf("%w: invalid prime modulus", ErrInvalidHost)
	}

	g1 := host.G1Group()
	g2 := host.G2Group()
	if g1 == nil || g2 == nil {
		return nil, fmt.Errorf("%w: nil group", ErrInvalidHost)
	}
	if host.Pairing() == nil {
		return nil, fmt.Errorf("%w: nil pairing", ErrInvalidHost)
	}

	g1Generator := g1.Generator()
	g2Generator := g2.Generator()
	if g1.IsIdentity(g1Generator) || g2.IsIdentity(g2Generator) {
		return nil, fmt.Errorf("%w: identity generator", ErrInvalidHost)
	}
	if err := g1.Validate(g1Generator); err != nil {
		return nil, fmt.Errorf("%w: G1 generator: %v", ErrInvalidHost, err)
	}
	if err := g2.Validate(g2Generator); err != nil {
		return nil, fmt.Errorf("%w: G2 generator: %v", ErrInvalidHost, err)
	}

	for i, p := range setup.g1 {
		if err := g1.Validate(p); err != nil {
			return nil, fmt.Errorf("%w: G1 power %d: %v", ErrInvalidSRS, i, err)
		}
	}
	for i, p := range setup.g2 {
		if err := g2.Validate(p); err != nil {
			return nil, fmt.Errorf("%w: G2 power %d: %v", ErrInvalidSRS, i, err)
		}
	}
	// tau^0 must be the same group generator the pairing equation uses.
	if !g1.Equal(setup.g1[0], g1Generator) {
		return nil, fmt.Errorf("%w: G1 tau^0 is not the generator", ErrInvalidSRS)
	}
	if !g2.Equal(setup.g2[0], g2Generator) {
		return nil, fmt.Errorf("%w: G2 tau^0 is not the generator", ErrInvalidSRS)
	}

	return &Verifier[G1, G2, GT]{
		host: host,
		q:    new(big.Int).Set(q),
		srs:  setup,
	}, nil
}

// Verify decodes and validates every request point and scalar before doing any
// group commitment or pairing work, normalizes the evaluations, constructs the
// unique interpolant I and Z(X), and checks
//
//	e(C - CommitG1(I), G2) = e(pi, CommitG2(Z)).
//
// An empty evaluation list is an error, never a successful empty conjunction.
func (v *Verifier[G1, G2, GT]) Verify(req EncodedRequest) (VerificationResult, error) {
	var empty VerificationResult
	if v == nil || v.host == nil {
		return empty, fmt.Errorf("%w: nil verifier", ErrInvalidHost)
	}
	if len(req.Evaluations) == 0 {
		return empty, ErrEmptyRequest
	}
	if len(req.Evaluations) > MaxEvaluations {
		return empty, fmt.Errorf("%w: got %d", ErrTooManyEvaluations, len(req.Evaluations))
	}

	g1 := v.host.G1Group()
	g2 := v.host.G2Group()
	pairing := v.host.Pairing()

	// Decode and canonicalize all request points before any pairing. A
	// canonical identity encoding is accepted here; only the Z commitment is
	// treated as a setup-degeneration condition later.
	commitment, err := decodePoint(g1, req.Commitment, "commitment")
	if err != nil {
		return empty, err
	}
	proof, err := decodePoint(g1, req.Proof, "proof")
	if err != nil {
		return empty, err
	}

	points := make([]Evaluation, len(req.Evaluations))
	for i, encoded := range req.Evaluations {
		z, err := v.decodeScalar(encoded.Z)
		if err != nil {
			return empty, fmt.Errorf("evaluation %d z: %w", i, err)
		}
		y, err := v.decodeScalar(encoded.Y)
		if err != nil {
			return empty, fmt.Errorf("evaluation %d y: %w", i, err)
		}
		points[i] = Evaluation{Z: z, Y: y}
	}

	normalized, err := normalizeEvaluations(points)
	if err != nil {
		return empty, err
	}
	distinct := len(normalized)

	// Supporting a k-point opening requires a degree-d setup with d >= k:
	// I has degree at most k-1 and Z has degree exactly k.
	if v.srs.MaxDegree() < distinct {
		return empty, fmt.Errorf("%w: degree %d, need %d", ErrSRSDegree, v.srs.MaxDegree(), distinct)
	}

	interpolant, err := interpolate(normalized, v.q)
	if err != nil {
		return empty, err
	}
	vanishingPolynomial := vanishing(normalized, v.q)

	commitI, err := commitGroup(g1, v.srs.g1, interpolant, v.q)
	if err != nil {
		return empty, err
	}
	commitZ, err := commitGroup(g2, v.srs.g2, vanishingPolynomial, v.q)
	if err != nil {
		return empty, err
	}

	// A canonical identity proof/commitment is legal (for example the zero
	// polynomial), but an identity commitment to Z makes the check vacuous.
	if g2.IsIdentity(commitZ) {
		return empty, ErrDegenerateCommitment
	}

	difference := g1.Sub(commitment, commitI)
	g2Generator := g2.Generator()

	left, err := pairing.Pair(difference, g2Generator)
	if err != nil {
		return empty, fmt.Errorf("%w: left pairing: %v", ErrInvalidHost, err)
	}
	right, err := pairing.Pair(proof, commitZ)
	if err != nil {
		return empty, fmt.Errorf("%w: right pairing: %v", ErrInvalidHost, err)
	}
	if !pairing.Equal(left, right) {
		return empty, ErrProofRejected
	}

	result := VerificationResult{
		Evaluations:  cloneEvaluations(normalized),
		SRSIdentity:  v.srs.Identity(),
		SRSMaxDegree: v.srs.MaxDegree(),
	}
	return result, nil
}

func (v *Verifier[G1, G2, GT]) decodeScalar(encoded []byte) (*big.Int, error) {
	if encoded == nil {
		return nil, fmt.Errorf("%w: missing scalar", ErrScalarOutOfRange)
	}
	value, err := v.host.ScalarDecode(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrScalarOutOfRange, err)
	}
	if value == nil {
		return nil, fmt.Errorf("%w: nil decoded scalar", ErrScalarOutOfRange)
	}
	if value.Sign() < 0 || value.Cmp(v.q) >= 0 {
		return nil, fmt.Errorf("%w: value %s", ErrScalarOutOfRange, value.String())
	}

	canonical, err := v.host.ScalarEncode(value)
	if err != nil {
		return nil, fmt.Errorf("%w: encode: %v", ErrNonCanonicalScalar, err)
	}
	if !bytes.Equal(canonical, encoded) {
		return nil, fmt.Errorf("%w: value %s", ErrNonCanonicalScalar, value.String())
	}
	return new(big.Int).Set(value), nil
}

func decodePoint[P any](group Group[P], encoded []byte, kind string) (P, error) {
	var zeroValue P
	if encoded == nil {
		return zeroValue, fmt.Errorf("%w: missing %s", ErrInvalidPoint, kind)
	}
	point, err := group.Decode(encoded)
	if err != nil {
		return zeroValue, fmt.Errorf("%w: %s: %v", ErrInvalidPoint, kind, err)
	}
	if err := group.Validate(point); err != nil {
		return zeroValue, fmt.Errorf("%w: %s: %v", ErrInvalidPoint, kind, err)
	}
	canonical, err := group.Encode(point)
	if err != nil {
		return zeroValue, fmt.Errorf("%w: %s encode: %v", ErrNonCanonicalPoint, kind, err)
	}
	if !bytes.Equal(canonical, encoded) {
		return zeroValue, fmt.Errorf("%w: %s", ErrNonCanonicalPoint, kind)
	}
	return point, nil
}

func normalizeEvaluations(points []Evaluation) ([]Evaluation, error) {
	normalized := make([]Evaluation, len(points))
	for i := range points {
		normalized[i] = Evaluation{
			Z: new(big.Int).Set(points[i].Z),
			Y: new(big.Int).Set(points[i].Y),
		}
	}
	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i].Z.Cmp(normalized[j].Z) < 0
	})

	out := make([]Evaluation, 0, len(normalized))
	for _, point := range normalized {
		if len(out) > 0 && out[len(out)-1].Z.Cmp(point.Z) == 0 {
			if out[len(out)-1].Y.Cmp(point.Y) != 0 {
				return nil, fmt.Errorf("%w: z=%s has multiple y values",
					ErrConflictingEvaluation, point.Z.String())
			}
			continue
		}
		out = append(out, point)
	}
	return out, nil
}

func cloneEvaluations(points []Evaluation) []Evaluation {
	out := make([]Evaluation, len(points))
	for i := range points {
		out[i] = Evaluation{
			Z: new(big.Int).Set(points[i].Z),
			Y: new(big.Int).Set(points[i].Y),
		}
	}
	return out
}

func commitGroup[P any](group Group[P], powers []P, coefficients []*big.Int, q *big.Int) (P, error) {
	if len(coefficients) > len(powers) {
		var zeroValue P
		return zeroValue, fmt.Errorf("%w: polynomial degree %d, powers %d",
			ErrSRSDegree, len(coefficients)-1, len(powers)-1)
	}
	accumulator := group.Identity()
	for i, coefficient := range coefficients {
		if coefficient.Sign() == 0 {
			continue
		}
		term := group.ScalarMul(powers[i], coefficient)
		accumulator = group.Add(accumulator, term)
	}
	return accumulator, nil
}
