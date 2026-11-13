package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"testing"
)

// The test host below is an algebraic stand-in, not a cryptographic pairing.
// Both source groups are the additive group Z_q, and the target group is also
// written additively, with e(aG1, bG2) = (a*b mod q). This models bilinearity
// exactly, which is what the interpolating and commitment equations depend on.
// It deliberately does not model elliptic-curve point validation, subgroup
// checks, or a real pairing; those remain the production host's responsibility.

type standInPoint int64
type standInGT int64

type standInGroup struct {
	q           int64
	allowPadded bool
}

func (g standInGroup) Generator() standInPoint { return 1 }
func (g standInGroup) Identity() standInPoint  { return 0 }

func (g standInGroup) Add(a, b standInPoint) standInPoint {
	return standInPoint((int64(a) + int64(b)) % g.q)
}

func (g standInGroup) Sub(a, b standInPoint) standInPoint {
	return standInPoint((int64(a) - int64(b) + g.q) % g.q)
}

func (g standInGroup) Neg(a standInPoint) standInPoint {
	return standInPoint((g.q - int64(a)) % g.q)
}

func (g standInGroup) ScalarMul(a standInPoint, scalar *big.Int) standInPoint {
	s := new(big.Int).Mod(scalar, big.NewInt(g.q)).Int64()
	return standInPoint((int64(a) * s) % g.q)
}

func (g standInGroup) Equal(a, b standInPoint) bool   { return a == b }
func (g standInGroup) IsIdentity(a standInPoint) bool { return a == 0 }

func (g standInGroup) Encode(a standInPoint) ([]byte, error) {
	if a < 0 || int64(a) >= g.q {
		return nil, fmt.Errorf("point outside group")
	}
	out := make([]byte, 2)
	binary.BigEndian.PutUint16(out, uint16(a))
	return out, nil
}

func (g standInGroup) Decode(encoded []byte) (standInPoint, error) {
	data := encoded
	if g.allowPadded && len(encoded) == 3 && encoded[0] == 0 {
		data = encoded[1:]
	}
	if len(data) != 2 {
		return 0, fmt.Errorf("bad point encoding length")
	}
	value := int64(binary.BigEndian.Uint16(data))
	if value >= g.q {
		return 0, fmt.Errorf("point outside group")
	}
	return standInPoint(value), nil
}

func (g standInGroup) Validate(a standInPoint) error {
	if a < 0 || int64(a) >= g.q {
		return fmt.Errorf("point outside group")
	}
	return nil
}

type standInHost struct {
	q           int64
	allowPadded bool
	pairCalls   int
}

func (h *standInHost) PrimeModulus() *big.Int { return big.NewInt(h.q) }

func (h *standInHost) ScalarEncode(v *big.Int) ([]byte, error) {
	if v.Sign() < 0 || v.Cmp(big.NewInt(h.q)) >= 0 {
		return nil, fmt.Errorf("scalar outside field")
	}
	out := make([]byte, 2)
	binary.BigEndian.PutUint16(out, uint16(v.Int64()))
	return out, nil
}

func (h *standInHost) ScalarDecode(encoded []byte) (*big.Int, error) {
	data := encoded
	if h.allowPadded && len(encoded) == 3 && encoded[0] == 0 {
		data = encoded[1:]
	}
	if len(data) != 2 {
		return nil, fmt.Errorf("bad scalar encoding length")
	}
	// The test codec intentionally does not range-check here. The package
	// must enforce [0, q-1] independently of host behavior.
	return big.NewInt(int64(binary.BigEndian.Uint16(data))), nil
}

func (h *standInHost) G1Group() Group[standInPoint] {
	return standInGroup{q: h.q, allowPadded: h.allowPadded}
}

func (h *standInHost) G2Group() Group[standInPoint] {
	return standInGroup{q: h.q, allowPadded: h.allowPadded}
}

func (h *standInHost) Pairing() Pairing[standInPoint, standInPoint, standInGT] {
	return standInPairing{host: h}
}

type standInPairing struct {
	host *standInHost
}

func (p standInPairing) Pair(a, b standInPoint) (standInGT, error) {
	p.host.pairCalls++
	return standInGT((int64(a) * int64(b)) % p.host.q), nil
}

func (p standInPairing) Equal(a, b standInGT) bool { return a == b }

func newStandInVerifier(t *testing.T, q, tau int64, maxDegree int) (*Verifier[standInPoint, standInPoint, standInGT], *standInHost) {
	t.Helper()
	host := &standInHost{q: q}

	g1Powers := make([]standInPoint, maxDegree+1)
	g2Powers := make([]standInPoint, maxDegree+1)
	power := int64(1)
	for i := 0; i <= maxDegree; i++ {
		g1Powers[i] = standInPoint(power)
		g2Powers[i] = standInPoint(power)
		power = (power * tau) % q
	}

	setup, err := NewSRS(fmt.Sprintf("standin-q%d-tau%d-d%d", q, tau, maxDegree), g1Powers, g2Powers)
	if err != nil {
		t.Fatalf("NewSRS: %v", err)
	}
	verifier, err := NewVerifier(host, setup)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return verifier, host
}

func encodeScalar(t *testing.T, host *standInHost, value int64) []byte {
	t.Helper()
	encoded, err := host.ScalarEncode(big.NewInt(value))
	if err != nil {
		t.Fatalf("ScalarEncode(%d): %v", value, err)
	}
	return encoded
}

func makeEncodedRequest(t *testing.T, verifier *Verifier[standInPoint, standInPoint, standInGT], polynomial []*big.Int, proofPoints []Evaluation) EncodedRequest {
	t.Helper()
	q := verifier.q
	host := verifier.host.(*standInHost)

	interpolant, err := interpolate(proofPoints, q)
	if err != nil {
		t.Fatalf("interpolate: %v", err)
	}
	zPolynomial := vanishing(proofPoints, q)
	quotient, err := divideExact(polySub(polynomial, interpolant, q), zPolynomial, q)
	if err != nil {
		t.Fatalf("divideExact: %v", err)
	}

	commitment, err := commitGroup(verifier.host.G1Group(), verifier.srs.g1, polynomial, q)
	if err != nil {
		t.Fatalf("commit C: %v", err)
	}
	proof, err := commitGroup(verifier.host.G1Group(), verifier.srs.g1, quotient, q)
	if err != nil {
		t.Fatalf("commit pi: %v", err)
	}
	commitmentBytes, err := verifier.host.G1Group().Encode(commitment)
	if err != nil {
		t.Fatalf("encode C: %v", err)
	}
	proofBytes, err := verifier.host.G1Group().Encode(proof)
	if err != nil {
		t.Fatalf("encode pi: %v", err)
	}

	evaluations := make([]EncodedEvaluation, len(proofPoints))
	for i, point := range proofPoints {
		evaluations[i] = EncodedEvaluation{
			Z: encodeScalar(t, host, point.Z.Int64()),
			Y: encodeScalar(t, host, point.Y.Int64()),
		}
	}
	return EncodedRequest{Commitment: commitmentBytes, Proof: proofBytes, Evaluations: evaluations}
}

func valuesPoly(coefficients ...int64) []*big.Int {
	out := make([]*big.Int, len(coefficients))
	for i, coefficient := range coefficients {
		out[i] = big.NewInt(coefficient)
	}
	return out
}

func TestExhaustiveLowDegreePolynomials(t *testing.T) {
	// q=7 and degree <= 3 means every polynomial is enumerated. tau=3 is
	// known to the test generator, while the evaluation domain excludes it.
	const q int64 = 7
	const tau int64 = 3
	verifier, _ := newStandInVerifier(t, q, tau, 3)
	domain := []int64{1, 2, 5}

	checked := 0
	for a0 := int64(0); a0 < q; a0++ {
		for a1 := int64(0); a1 < q; a1++ {
			for a2 := int64(0); a2 < q; a2++ {
				for a3 := int64(0); a3 < q; a3++ {
					polynomial := valuesPoly(a0, a1, a2, a3)
					for mask := 1; mask < 1<<len(domain); mask++ {
						points := make([]Evaluation, 0, len(domain))
						for i, z := range domain {
							if mask&(1<<i) == 0 {
								continue
							}
							points = append(points, Evaluation{
								Z: big.NewInt(z),
								Y: polyEval(polynomial, big.NewInt(z), big.NewInt(q)),
							})
						}
						request := makeEncodedRequest(t, verifier, polynomial, points)
						result, err := verifier.Verify(request)
						if err != nil {
							t.Fatalf("poly=%v points=%v: %v", polynomial, points, err)
						}
						if result.SRSIdentity != verifier.srs.Identity() {
							t.Fatalf("SRS identity = %q", result.SRSIdentity)
						}
						if len(result.Evaluations) != len(points) {
							t.Fatalf("normalized %d points, want %d", len(result.Evaluations), len(points))
						}
						for i := range points {
							if result.Evaluations[i].Z.Cmp(points[i].Z) != 0 ||
								result.Evaluations[i].Y.Cmp(points[i].Y) != 0 {
								t.Fatalf("normalized point %d = (%v,%v), want (%v,%v)",
									i, result.Evaluations[i].Z, result.Evaluations[i].Y,
									points[i].Z, points[i].Y)
							}
							interpolant, err := interpolate(points, big.NewInt(q))
							if err != nil {
								t.Fatalf("interpolate: %v", err)
							}
							if polyEval(interpolant, points[i].Z, big.NewInt(q)).Cmp(points[i].Y) != 0 {
								t.Fatalf("interpolant does not match point %d", i)
							}
						}
						checked++
					}
				}
			}
		}
	}
	t.Logf("verified %d exhaustive polynomial/evaluation-set combinations", checked)
}

func TestDuplicateEvaluationMergingAndSorting(t *testing.T) {
	verifier, host := newStandInVerifier(t, 101, 7, 4)
	polynomial := valuesPoly(3, 4, 0, 9)
	distinct := []Evaluation{
		{Z: big.NewInt(2), Y: polyEval(polynomial, big.NewInt(2), big.NewInt(101))},
		{Z: big.NewInt(5), Y: polyEval(polynomial, big.NewInt(5), big.NewInt(101))},
		{Z: big.NewInt(8), Y: polyEval(polynomial, big.NewInt(8), big.NewInt(101))},
	}
	request := makeEncodedRequest(t, verifier, polynomial, distinct)

	// Repeat exact pairs and scramble the order. They must merge to the same
	// three normalized, z-sorted evaluations.
	request.Evaluations = append(request.Evaluations,
		EncodedEvaluation{Z: encodeScalar(t, host, 5), Y: encodeScalar(t, host, distinct[1].Y.Int64())},
		EncodedEvaluation{Z: encodeScalar(t, host, 2), Y: encodeScalar(t, host, distinct[0].Y.Int64())},
		EncodedEvaluation{Z: encodeScalar(t, host, 5), Y: encodeScalar(t, host, distinct[1].Y.Int64())},
	)

	result, err := verifier.Verify(request)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got, want := len(result.Evaluations), 3; got != want {
		t.Fatalf("normalized evaluations = %d, want %d", got, want)
	}
	for i, z := range []int64{2, 5, 8} {
		if result.Evaluations[i].Z.Int64() != z {
			t.Fatalf("evaluation %d z = %v, want %d", i, result.Evaluations[i].Z, z)
		}
	}
}

func TestConflictingRepeatedZRejected(t *testing.T) {
	verifier, host := newStandInVerifier(t, 101, 7, 4)
	polynomial := valuesPoly(1, 2, 3)
	points := []Evaluation{
		{Z: big.NewInt(2), Y: polyEval(polynomial, big.NewInt(2), big.NewInt(101))},
		{Z: big.NewInt(5), Y: polyEval(polynomial, big.NewInt(5), big.NewInt(101))},
	}
	request := makeEncodedRequest(t, verifier, polynomial, points)
	request.Evaluations = append(request.Evaluations, EncodedEvaluation{
		Z: encodeScalar(t, host, 5),
		Y: encodeScalar(t, host, points[1].Y.Int64()+1),
	})

	if _, err := verifier.Verify(request); !errors.Is(err, ErrConflictingEvaluation) {
		t.Fatalf("Verify error = %v, want ErrConflictingEvaluation", err)
	}
}

func TestZeroPolynomialCanonicalIdentityCommitments(t *testing.T) {
	verifier, _ := newStandInVerifier(t, 101, 7, 2)
	zeroPolynomial := valuesPoly(0, 0, 0)
	points := []Evaluation{
		{Z: big.NewInt(1), Y: big.NewInt(0)},
		{Z: big.NewInt(4), Y: big.NewInt(0)},
	}
	request := makeEncodedRequest(t, verifier, zeroPolynomial, points)

	// Both C and pi are canonically encoded identity elements. They are valid
	// for the zero polynomial and must not be rejected as malformed points.
	identity, err := verifier.host.G1Group().Encode(verifier.host.G1Group().Identity())
	if err != nil {
		t.Fatalf("encode identity: %v", err)
	}
	if !bytes.Equal(request.Commitment, identity) || !bytes.Equal(request.Proof, identity) {
		t.Fatalf("zero polynomial opening is not using identity commitments")
	}
	result, err := verifier.Verify(request)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.SRSIdentity != verifier.srs.Identity() {
		t.Fatalf("SRS identity = %q", result.SRSIdentity)
	}
}

func TestWrongProofAndIdentityProofAreRejected(t *testing.T) {
	verifier, host := newStandInVerifier(t, 101, 7, 4)
	polynomial := valuesPoly(2, 5, 1, 4)
	points := []Evaluation{
		{Z: big.NewInt(3), Y: polyEval(polynomial, big.NewInt(3), big.NewInt(101))},
		{Z: big.NewInt(6), Y: polyEval(polynomial, big.NewInt(6), big.NewInt(101))},
	}
	request := makeEncodedRequest(t, verifier, polynomial, points)

	validProof, err := verifier.host.G1Group().Decode(request.Proof)
	if err != nil {
		t.Fatalf("Decode proof: %v", err)
	}
	wrongProof := verifier.host.G1Group().Add(validProof, verifier.host.G1Group().Generator())
	request.Proof, err = verifier.host.G1Group().Encode(wrongProof)
	if err != nil {
		t.Fatalf("Encode wrong proof: %v", err)
	}
	if _, err := verifier.Verify(request); !errors.Is(err, ErrProofRejected) {
		t.Fatalf("wrong proof error = %v, want ErrProofRejected", err)
	}

	identityProof, err := verifier.host.G1Group().Encode(verifier.host.G1Group().Identity())
	if err != nil {
		t.Fatalf("encode identity proof: %v", err)
	}
	request.Proof = identityProof
	if _, err := verifier.Verify(request); !errors.Is(err, ErrProofRejected) {
		t.Fatalf("identity proof error = %v, want ErrProofRejected", err)
	}
	_ = host
}

func TestDegreeInsufficientSRSRejected(t *testing.T) {
	fullVerifier, _ := newStandInVerifier(t, 101, 7, 3)
	polynomial := valuesPoly(1, 2, 3, 4)
	points := []Evaluation{
		{Z: big.NewInt(2), Y: polyEval(polynomial, big.NewInt(2), big.NewInt(101))},
		{Z: big.NewInt(9), Y: polyEval(polynomial, big.NewInt(9), big.NewInt(101))},
	}
	request := makeEncodedRequest(t, fullVerifier, polynomial, points)

	shortVerifier, shortHost := newStandInVerifier(t, 101, 7, 1)
	if _, err := shortVerifier.Verify(request); !errors.Is(err, ErrSRSDegree) {
		t.Fatalf("short SRS error = %v, want ErrSRSDegree", err)
	}
	if shortHost.pairCalls != 0 {
		t.Fatalf("pairing called %d times before SRS rejection", shortHost.pairCalls)
	}
}

func TestIdentityVanishingCommitmentIsDegenerate(t *testing.T) {
	verifier, host := newStandInVerifier(t, 101, 7, 2)
	polynomial := valuesPoly(4, 3, 1)
	// z equals tau, so Z(tau)=0 and CommitG2(Z) is the identity.
	points := []Evaluation{
		{Z: big.NewInt(7), Y: polyEval(polynomial, big.NewInt(7), big.NewInt(101))},
	}
	request := makeEncodedRequest(t, verifier, polynomial, points)

	if _, err := verifier.Verify(request); !errors.Is(err, ErrDegenerateCommitment) {
		t.Fatalf("Verify error = %v, want ErrDegenerateCommitment", err)
	}
	if host.pairCalls != 0 {
		t.Fatalf("pairing called %d times before degeneration rejection", host.pairCalls)
	}
}

func TestInvalidPointsRejectedBeforePairing(t *testing.T) {
	verifier, host := newStandInVerifier(t, 101, 7, 3)
	polynomial := valuesPoly(1, 2, 3, 4)
	points := []Evaluation{
		{Z: big.NewInt(2), Y: polyEval(polynomial, big.NewInt(2), big.NewInt(101))},
		{Z: big.NewInt(9), Y: polyEval(polynomial, big.NewInt(9), big.NewInt(101))},
	}
	request := makeEncodedRequest(t, verifier, polynomial, points)

	badPoint := []byte{0x00, 0x65} // 101, outside q=101
	request.Commitment = badPoint
	if _, err := verifier.Verify(request); !errors.Is(err, ErrInvalidPoint) {
		t.Fatalf("bad point error = %v, want ErrInvalidPoint", err)
	}
	if host.pairCalls != 0 {
		t.Fatalf("pairing called %d times for invalid point", host.pairCalls)
	}
}

func TestNonCanonicalAndOutOfRangeScalarsRejectedBeforePairing(t *testing.T) {
	verifier, host := newStandInVerifier(t, 101, 7, 3)
	host.allowPadded = true
	// Rebuild the verifier's groups with the permissive codec.
	verifier.host = host

	polynomial := valuesPoly(1, 2, 3, 4)
	points := []Evaluation{
		{Z: big.NewInt(2), Y: polyEval(polynomial, big.NewInt(2), big.NewInt(101))},
		{Z: big.NewInt(9), Y: polyEval(polynomial, big.NewInt(9), big.NewInt(101))},
	}
	request := makeEncodedRequest(t, verifier, polynomial, points)

	// The permissive host decoder accepts a padded scalar, but the canonical
	// encoder emits two bytes. The package must compare the encodings.
	paddedZ := append([]byte{0}, request.Evaluations[0].Z...)
	request.Evaluations[0].Z = paddedZ
	if _, err := verifier.Verify(request); !errors.Is(err, ErrNonCanonicalScalar) {
		t.Fatalf("padded scalar error = %v, want ErrNonCanonicalScalar", err)
	}
	if host.pairCalls != 0 {
		t.Fatalf("pairing called %d times for non-canonical scalar", host.pairCalls)
	}

	request.Evaluations[0].Z = []byte{0x00, 0x65} // 101
	if _, err := verifier.Verify(request); !errors.Is(err, ErrScalarOutOfRange) {
		t.Fatalf("out-of-range scalar error = %v, want ErrScalarOutOfRange", err)
	}
	if host.pairCalls != 0 {
		t.Fatalf("pairing called %d times for out-of-range scalar", host.pairCalls)
	}
}

func TestNonCanonicalPointRejectedBeforePairing(t *testing.T) {
	verifier, host := newStandInVerifier(t, 101, 7, 3)
	host.allowPadded = true
	verifier.host = host

	polynomial := valuesPoly(1, 2, 3, 4)
	points := []Evaluation{
		{Z: big.NewInt(2), Y: polyEval(polynomial, big.NewInt(2), big.NewInt(101))},
	}
	request := makeEncodedRequest(t, verifier, polynomial, points)
	request.Commitment = append([]byte{0}, request.Commitment...)

	if _, err := verifier.Verify(request); !errors.Is(err, ErrNonCanonicalPoint) {
		t.Fatalf("padded point error = %v, want ErrNonCanonicalPoint", err)
	}
	if host.pairCalls != 0 {
		t.Fatalf("pairing called %d times for non-canonical point", host.pairCalls)
	}
}

func TestEmptyAndOversizedRequests(t *testing.T) {
	verifier, _ := newStandInVerifier(t, 101, 7, 32)

	if _, err := verifier.Verify(EncodedRequest{}); !errors.Is(err, ErrEmptyRequest) {
		t.Fatalf("empty request error = %v, want ErrEmptyRequest", err)
	}

	request := EncodedRequest{Evaluations: make([]EncodedEvaluation, 33)}
	if _, err := verifier.Verify(request); !errors.Is(err, ErrTooManyEvaluations) {
		t.Fatalf("oversized request error = %v, want ErrTooManyEvaluations", err)
	}
}

func TestMaximumThirtyTwoEvaluations(t *testing.T) {
	verifier, host := newStandInVerifier(t, 101, 7, 32)
	polynomial := valuesPoly(5, 1, 2, 6)
	points := make([]Evaluation, 0, 32)
	for i := int64(0); i < 32; i++ {
		z := 10 + i
		points = append(points, Evaluation{
			Z: big.NewInt(z),
			Y: polyEval(polynomial, big.NewInt(z), big.NewInt(101)),
		})
	}
	request := makeEncodedRequest(t, verifier, polynomial, points)

	result, err := verifier.Verify(request)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(result.Evaluations) != 32 {
		t.Fatalf("normalized = %d, want 32", len(result.Evaluations))
	}
	if host.pairCalls == 0 {
		t.Fatalf("pairing was not called for a valid 32-point request")
	}
}
