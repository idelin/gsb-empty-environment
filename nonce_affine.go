package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"errors"
	"fmt"
	"math/big"
)

// Signature is an ECDSA (r, s) pair. It is a small local type because recent
// versions of crypto/ecdsa expose signing and verification operations without
// an exported signature struct.
type Signature struct {
	R *big.Int
	S *big.Int
}

// Input is the complete evidence supplied by the signing service.
//
// Digests are the message digests passed to ECDSA. They are interpreted with
// ECDSA's left truncation rule for the 256-bit order of P-256. Signatures and
// scalars are never modified by Analyze.
type Input struct {
	PublicKey  *ecdsa.PublicKey
	Digest1    []byte
	Digest2    []byte
	Signature1 Signature
	Signature2 Signature
	A          *big.Int
	B          *big.Int
}

// ScalarSign records which signed scalar was used in an equation. The raw
// signature scalar S is either used as-is (+1) or replaced by n-S (-1).
type ScalarSign int

const (
	// SignPositive uses the signature scalar exactly as supplied.
	SignPositive ScalarSign = 1
	// SignNegative uses n-S, the other valid ECDSA scalar sign.
	SignNegative ScalarSign = -1
)

func (s ScalarSign) String() string {
	if s == SignNegative {
		return "-"
	}
	return "+"
}

// Status is the conclusion of a completed analysis. Input errors and failed
// signature verification are returned as errors instead of statuses.
type Status string

const (
	// StatusRecovered means at least one sign interpretation produced a
	// candidate that passed every independent check.
	StatusRecovered Status = "recovered"
	// StatusInsufficientInformation means at least one sign interpretation
	// reduced to a consistent but degenerate linear equation. In that case the
	// evidence does not determine the private key or nonce pair.
	StatusInsufficientInformation Status = "insufficient_information"
	// StatusNoCandidate means both signatures verified, but none of the four
	// scalar-sign interpretations produced a candidate consistent with the
	// supplied affine relation. This is not a safety finding.
	StatusNoCandidate Status = "no_candidate"
)

// AttemptOutcome describes what happened for one of the four scalar-sign
// interpretations.
type AttemptOutcome string

const (
	AttemptCandidate    AttemptOutcome = "candidate"
	AttemptInconsistent AttemptOutcome = "inconsistent"
	AttemptDegenerate   AttemptOutcome = "degenerate"
	AttemptRejected     AttemptOutcome = "candidate_rejected"
)

// DigestDetails records how a digest was converted to an ECDSA integer.
type DigestDetails struct {
	Length    int
	Z         *big.Int
	Truncated bool
	Equation  string
}

// SignatureDetails records verification and normalization facts for one raw
// signature.
type SignatureDetails struct {
	R        *big.Int
	S        *big.Int
	LowS     bool
	Verified bool
}

// Derivation is the reproducible arithmetic for one scalar-sign choice.
//
// EquationPrivateKey, NonceEquation1, NonceEquation2, and AffineEquation
// describe the formulas used. The scalar values needed to replay them are
// present as fields, and RecheckCandidate independently recomputes every
// check rather than trusting the strings or the stored result.
type Derivation struct {
	Signature1Sign ScalarSign
	Signature2Sign ScalarSign
	S1             *big.Int
	S2             *big.Int
	Determinant    *big.Int
	RHS            *big.Int

	EquationPrivateKey string
	NonceEquation1     string
	NonceEquation2     string
	AffineEquation     string

	Outcome AttemptOutcome
	Reason  string
}

// Candidate is one valid private-key/nonce interpretation. Multiple candidates
// are possible because either valid sign of s can be selected independently for
// each signature.
type Candidate struct {
	D              *big.Int
	Nonce1         *big.Int
	Nonce2         *big.Int
	Signature1Sign ScalarSign
	Signature2Sign ScalarSign
	// Derivation is the exact arithmetic snapshot that produced this
	// candidate. RecheckCandidate recomputes from Input and does not trust it.
	Derivation Derivation
}

// Report is the audit output. Candidates contains every interpretation that
// passed the modular equations, public-key comparison, both nonce base-point
// multiplications, and the affine relation.
type Report struct {
	Status  Status
	Summary string
	Order   *big.Int
	A       *big.Int
	B       *big.Int

	Digests    [2]DigestDetails
	Signatures [2]SignatureDetails

	Candidates []Candidate
	Attempts   []Derivation

	// Ambiguous is true when more than one candidate was returned. Because the
	// public key fixes d, the ambiguity can be in the nonce signs or, if
	// multiple distinct private keys were reported, in the key itself.
	Ambiguous bool
	// Degenerate is true if any sign interpretation reduced to 0*d = 0.
	Degenerate bool
	// DistinctPrivateKeys counts the distinct d values among Candidates.
	DistinctPrivateKeys int
	Notes               []string
}

var (
	// ErrInvalidPublicKey identifies a public-key validation failure.
	ErrInvalidPublicKey = errors.New("invalid public key")
	// ErrP256Required identifies a curve other than P-256.
	ErrP256Required = errors.New("P-256 public key required")
	// ErrPointAtInfinity identifies the conventional (0, 0) infinity encoding.
	ErrPointAtInfinity = errors.New("public key is the point at infinity")
	// ErrPointNotOnCurve identifies coordinates that are not on P-256.
	ErrPointNotOnCurve = errors.New("public key is not on P-256")
	// ErrScalarOutOfRange identifies r, s, a, or b outside its required range.
	ErrScalarOutOfRange = errors.New("scalar outside required range")
	// ErrSignatureVerification identifies a raw ECDSA verification failure.
	ErrSignatureVerification = errors.New("ECDSA signature verification failed")
	// ErrCandidateInvalid identifies a failed independent candidate recheck.
	ErrCandidateInvalid = errors.New("candidate failed independent recheck")
)

// InputError adds the offending field to a sentinel validation error.
type InputError struct {
	Field  string
	Detail string
	Kind   error
}

func (e *InputError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("netsec: %s: %v", e.Field, e.Kind)
	}
	return fmt.Sprintf("netsec: %s: %s: %v", e.Field, e.Detail, e.Kind)
}

func (e *InputError) Unwrap() error { return e.Kind }

// Analyze validates the input, verifies both signatures as supplied, and then
// checks all four choices of (s1, s2) and (n-s1, s2), and so on.
//
// A nil error with StatusNoCandidate is not a safety conclusion. It only means
// that the supplied affine relation did not produce any valid candidate.
func Analyze(in Input) (*Report, error) {
	work, err := validateAndVerify(in)
	if err != nil {
		return nil, err
	}

	report := &Report{
		Status: StatusNoCandidate,
		Order:  new(big.Int).Set(work.order),
		A:      new(big.Int).Set(in.A),
		B:      new(big.Int).Set(in.B),
		Digests: [2]DigestDetails{
			{Length: len(in.Digest1), Z: new(big.Int).Set(work.z1), Truncated: len(in.Digest1) > work.orderBytes,
				Equation: "z1 = integer formed by the leftmost min(256, 8*len(digest1)) bits of digest1"},
			{Length: len(in.Digest2), Z: new(big.Int).Set(work.z2), Truncated: len(in.Digest2) > work.orderBytes,
				Equation: "z2 = integer formed by the leftmost min(256, 8*len(digest2)) bits of digest2"},
		},
		Signatures: [2]SignatureDetails{
			{R: new(big.Int).Set(in.Signature1.R), S: new(big.Int).Set(in.Signature1.S), Verified: true},
			{R: new(big.Int).Set(in.Signature2.R), S: new(big.Int).Set(in.Signature2.S), Verified: true},
		},
		Candidates: []Candidate{},
		Attempts:   make([]Derivation, 0, 4),
	}
	halfN := new(big.Int).Rsh(new(big.Int).Set(work.order), 1)
	report.Signatures[0].LowS = in.Signature1.S.Cmp(halfN) <= 0
	report.Signatures[1].LowS = in.Signature2.S.Cmp(halfN) <= 0

	degenerate := 0
	for _, sign1 := range []ScalarSign{SignPositive, SignNegative} {
		for _, sign2 := range []ScalarSign{SignPositive, SignNegative} {
			attempt, candidate, err := derive(work, sign1, sign2)
			if err != nil {
				return nil, err
			}
			report.Attempts = append(report.Attempts, attempt)
			switch attempt.Outcome {
			case AttemptCandidate:
				withDerivation := *candidate
				withDerivation.Derivation = attempt
				report.Candidates = append(report.Candidates, withDerivation)
			case AttemptDegenerate:
				degenerate++
			}
		}
	}

	report.Degenerate = degenerate > 0
	report.DistinctPrivateKeys = countDistinctPrivateKeys(report.Candidates)
	report.Ambiguous = len(report.Candidates) > 1

	switch {
	case len(report.Candidates) > 0:
		report.Status = StatusRecovered
		report.Summary = fmt.Sprintf("recovered %d valid candidate interpretation(s), representing %d distinct private key(s)",
			len(report.Candidates), report.DistinctPrivateKeys)
		if report.Ambiguous {
			report.Notes = append(report.Notes,
				"multiple sign interpretations satisfy the supplied relation; the nonce sign choice is not unique")
		}
		if report.Degenerate {
			report.Notes = append(report.Notes,
				"at least one other sign interpretation reduced to a consistent degenerate equation; the candidate list may be incomplete")
		}
	case degenerate > 0:
		report.Status = StatusInsufficientInformation
		report.Summary = "the supplied data reduce to a degenerate consistent linear equation; private key and nonces are not determined"
		report.Notes = append(report.Notes,
			"information is insufficient; no private-key space was searched")
	default:
		report.Status = StatusNoCandidate
		report.Summary = "both signatures verified, but no scalar-sign choice satisfies the supplied affine nonce relation"
		report.Notes = append(report.Notes,
			"absence of a candidate does not establish nonce safety or independence")
	}

	return report, nil
}

// VerifyInput performs the mandatory pre-analysis checks: public-key shape,
// scalar ranges, and verification of both signatures exactly as supplied.
func VerifyInput(in Input) error {
	_, err := validateAndVerify(in)
	return err
}

// RecheckCandidate independently verifies a candidate against the original
// input. It recomputes the modular equations, compares d*G with the supplied
// public key, and multiplies both recovered nonces by G to check r1 and r2.
func RecheckCandidate(in Input, candidate Candidate) error {
	work, err := validateAndVerify(in)
	if err != nil {
		return err
	}
	return checkCandidateWithWork(work, candidate)
}

func checkCandidateWithWork(work *verifiedInput, candidate Candidate) error {
	if err := validCandidateScalars(work, candidate); err != nil {
		return err
	}

	s1, err := applySign(work.sig1S, candidate.Signature1Sign, work.order)
	if err != nil {
		return candidateError("signature1 sign", err)
	}
	s2, err := applySign(work.sig2S, candidate.Signature2Sign, work.order)
	if err != nil {
		return candidateError("signature2 sign", err)
	}
	if candidate.Derivation.Outcome != "" {
		if err := checkDerivationSnapshot(work, candidate, s1, s2); err != nil {
			return err
		}
	}

	left1 := new(big.Int).Mul(s1, candidate.Nonce1)
	left1.Mod(left1, work.order)
	right1 := new(big.Int).Mul(work.sig1R, candidate.D)
	right1.Add(right1, work.z1)
	right1.Mod(right1, work.order)
	if left1.Cmp(right1) != 0 {
		return candidateError("nonce equation 1", errors.New("s1*k1 != z1 + r1*d (mod n)"))
	}

	left2 := new(big.Int).Mul(s2, candidate.Nonce2)
	left2.Mod(left2, work.order)
	right2 := new(big.Int).Mul(work.sig2R, candidate.D)
	right2.Add(right2, work.z2)
	right2.Mod(right2, work.order)
	if left2.Cmp(right2) != 0 {
		return candidateError("nonce equation 2", errors.New("s2*k2 != z2 + r2*d (mod n)"))
	}

	expected := new(big.Int).Mul(work.a, candidate.Nonce1)
	expected.Add(expected, work.b)
	expected.Mod(expected, work.order)
	if candidate.Nonce2.Cmp(expected) != 0 {
		return candidateError("affine relation", errors.New("k2 != a*k1 + b (mod n)"))
	}

	qx, qy := work.curve.ScalarBaseMult(candidate.D.Bytes())
	if qx.Cmp(work.pub.X) != 0 || qy.Cmp(work.pub.Y) != 0 {
		return candidateError("public key", errors.New("d*G does not equal the supplied public key"))
	}

	rx1, _ := work.curve.ScalarBaseMult(candidate.Nonce1.Bytes())
	rx1.Mod(rx1, work.order)
	if rx1.Cmp(work.sig1R) != 0 {
		return candidateError("signature 1 r", errors.New("x-coordinate of k1*G does not equal r1"))
	}

	rx2, _ := work.curve.ScalarBaseMult(candidate.Nonce2.Bytes())
	rx2.Mod(rx2, work.order)
	if rx2.Cmp(work.sig2R) != 0 {
		return candidateError("signature 2 r", errors.New("x-coordinate of k2*G does not equal r2"))
	}

	return nil
}

// CandidateCallback is invoked only after RecheckCandidate has independently
// accepted a report candidate. A non-nil error stops the callback loop.
type CandidateCallback func(Candidate) error

// Recheck replays every candidate in the report with RecheckCandidate and then
// optionally hands the verified candidate to an external callback.
func (r *Report) Recheck(in Input, callback CandidateCallback) error {
	if r == nil {
		return errors.New("netsec: nil report")
	}
	for i, candidate := range r.Candidates {
		if err := RecheckCandidate(in, candidate); err != nil {
			return fmt.Errorf("netsec: candidate %d: %w", i, err)
		}
		if callback != nil {
			if err := callback(candidate); err != nil {
				return fmt.Errorf("netsec: candidate %d callback: %w", i, err)
			}
		}
	}
	return nil
}

type verifiedInput struct {
	curve      elliptic.Curve
	order      *big.Int
	orderBytes int
	pub        *ecdsa.PublicKey
	sig1R      *big.Int
	sig1S      *big.Int
	sig2R      *big.Int
	sig2S      *big.Int
	a          *big.Int
	b          *big.Int
	z1         *big.Int
	z2         *big.Int
}

func validateAndVerify(in Input) (*verifiedInput, error) {
	if in.PublicKey == nil {
		return nil, &InputError{Field: "public key", Kind: ErrInvalidPublicKey, Detail: "nil"}
	}
	if !isP256(in.PublicKey.Curve) {
		return nil, &InputError{Field: "public key curve", Kind: ErrP256Required}
	}
	if in.PublicKey.X == nil || in.PublicKey.Y == nil {
		return nil, &InputError{Field: "public key", Kind: ErrInvalidPublicKey, Detail: "nil coordinate"}
	}
	if in.PublicKey.X.Sign() == 0 && in.PublicKey.Y.Sign() == 0 {
		return nil, &InputError{Field: "public key", Kind: ErrPointAtInfinity}
	}

	curve := elliptic.P256()
	if !curve.IsOnCurve(in.PublicKey.X, in.PublicKey.Y) {
		return nil, &InputError{Field: "public key", Kind: ErrPointNotOnCurve}
	}

	order := curve.Params().N
	if err := checkScalar("signature1.r", in.Signature1.R, order, false); err != nil {
		return nil, err
	}
	if err := checkScalar("signature1.s", in.Signature1.S, order, false); err != nil {
		return nil, err
	}
	if err := checkScalar("signature2.r", in.Signature2.R, order, false); err != nil {
		return nil, err
	}
	if err := checkScalar("signature2.s", in.Signature2.S, order, false); err != nil {
		return nil, err
	}
	if err := checkScalar("a", in.A, order, true); err != nil {
		return nil, err
	}
	if err := checkScalar("b", in.B, order, true); err != nil {
		return nil, err
	}

	refPub := &ecdsa.PublicKey{
		Curve: curve,
		X:     new(big.Int).Set(in.PublicKey.X),
		Y:     new(big.Int).Set(in.PublicKey.Y),
	}
	if !ecdsa.Verify(refPub, in.Digest1, in.Signature1.R, in.Signature1.S) {
		return nil, &InputError{Field: "signature1", Kind: ErrSignatureVerification}
	}
	if !ecdsa.Verify(refPub, in.Digest2, in.Signature2.R, in.Signature2.S) {
		return nil, &InputError{Field: "signature2", Kind: ErrSignatureVerification}
	}

	return &verifiedInput{
		curve:      curve,
		order:      order,
		orderBytes: (order.BitLen() + 7) / 8,
		pub:        refPub,
		sig1R:      new(big.Int).Set(in.Signature1.R),
		sig1S:      new(big.Int).Set(in.Signature1.S),
		sig2R:      new(big.Int).Set(in.Signature2.R),
		sig2S:      new(big.Int).Set(in.Signature2.S),
		a:          new(big.Int).Set(in.A),
		b:          new(big.Int).Set(in.B),
		z1:         digestScalar(in.Digest1, order),
		z2:         digestScalar(in.Digest2, order),
	}, nil
}

func checkScalar(field string, value, order *big.Int, allowZero bool) error {
	if value == nil {
		return &InputError{Field: field, Kind: ErrScalarOutOfRange, Detail: "nil"}
	}
	lower := 1
	if allowZero {
		lower = 0
	}
	if value.Sign() < lower || value.Cmp(order) >= 0 {
		return &InputError{
			Field:  field,
			Kind:   ErrScalarOutOfRange,
			Detail: fmt.Sprintf("must be in [%d, n-1]", lower),
		}
	}
	return nil
}

func isP256(curve elliptic.Curve) bool {
	if curve == nil || curve.Params() == nil {
		return false
	}
	got := curve.Params()
	want := elliptic.P256().Params()
	return got.Name == want.Name &&
		got.BitSize == want.BitSize &&
		got.P.Cmp(want.P) == 0 &&
		got.N.Cmp(want.N) == 0 &&
		got.B.Cmp(want.B) == 0 &&
		got.Gx.Cmp(want.Gx) == 0 &&
		got.Gy.Cmp(want.Gy) == 0
}

func digestScalar(digest []byte, order *big.Int) *big.Int {
	orderBits := order.BitLen()
	orderBytes := (orderBits + 7) / 8
	truncated := digest
	if len(truncated) > orderBytes {
		truncated = truncated[:orderBytes]
	}
	z := new(big.Int).SetBytes(truncated)
	if excess := len(truncated)*8 - orderBits; excess > 0 {
		z.Rsh(z, uint(excess))
	}
	return z
}

func derive(work *verifiedInput, sign1, sign2 ScalarSign) (Derivation, *Candidate, error) {
	order := work.order
	s1, err := applySign(work.sig1S, sign1, order)
	if err != nil {
		return Derivation{}, nil, err
	}
	s2, err := applySign(work.sig2S, sign2, order)
	if err != nil {
		return Derivation{}, nil, err
	}

	// The two nonce equations are
	//   s1*k1 = z1 + r1*d,  s2*k2 = z2 + r2*d  (mod n),
	// together with k2 = a*k1+b. Eliminating the nonces gives
	//   d*(s1*r2-a*s2*r1) = a*s2*z1-s1*z2+b*s1*s2  (mod n).
	det, rhs := equationTerms(work, s1, s2)

	attempt := Derivation{
		Signature1Sign:     sign1,
		Signature2Sign:     sign2,
		S1:                 new(big.Int).Set(s1),
		S2:                 new(big.Int).Set(s2),
		Determinant:        new(big.Int).Set(det),
		RHS:                new(big.Int).Set(rhs),
		EquationPrivateKey: "d = (a*s2*z1 - s1*z2 + b*s1*s2) * (s1*r2 - a*s2*r1)^(-1) mod n",
		NonceEquation1:     "s1*k1 = z1 + r1*d mod n",
		NonceEquation2:     "s2*k2 = z2 + r2*d mod n",
		AffineEquation:     "k2 = a*k1 + b mod n",
	}

	if det.Sign() == 0 {
		if rhs.Sign() == 0 {
			attempt.Outcome = AttemptDegenerate
			attempt.Reason = "0*d = 0: every d satisfies this sign interpretation"
			return attempt, nil, nil
		}
		attempt.Outcome = AttemptInconsistent
		attempt.Reason = "0*d != nonzero RHS: this sign interpretation has no solution"
		return attempt, nil, nil
	}

	d := new(big.Int).Mul(rhs, new(big.Int).ModInverse(det, order))
	d.Mod(d, order)
	nonce1 := new(big.Int).Add(work.z1, new(big.Int).Mul(work.sig1R, d))
	nonce1.Mul(nonce1, new(big.Int).ModInverse(s1, order))
	nonce1.Mod(nonce1, order)
	nonce2 := new(big.Int).Add(work.z2, new(big.Int).Mul(work.sig2R, d))
	nonce2.Mul(nonce2, new(big.Int).ModInverse(s2, order))
	nonce2.Mod(nonce2, order)

	candidate := &Candidate{
		D:              d,
		Nonce1:         nonce1,
		Nonce2:         nonce2,
		Signature1Sign: sign1,
		Signature2Sign: sign2,
	}
	if err := checkCandidateWithWork(work, *candidate); err != nil {
		attempt.Outcome = AttemptRejected
		attempt.Reason = err.Error()
		return attempt, nil, nil
	}

	attempt.Outcome = AttemptCandidate
	attempt.Reason = "candidate passed modular equations, d*G, k1*G, k2*G, and the affine relation"
	return attempt, candidate, nil
}

func equationTerms(work *verifiedInput, s1, s2 *big.Int) (*big.Int, *big.Int) {
	order := work.order
	det := new(big.Int).Mul(s1, work.sig2R)
	det.Sub(det, new(big.Int).Mul(work.a, new(big.Int).Mul(s2, work.sig1R)))
	det.Mod(det, order)

	rhs := new(big.Int).Mul(work.a, new(big.Int).Mul(s2, work.z1))
	rhs.Sub(rhs, new(big.Int).Mul(s1, work.z2))
	rhs.Add(rhs, new(big.Int).Mul(work.b, new(big.Int).Mul(s1, s2)))
	rhs.Mod(rhs, order)
	return det, rhs
}

func checkDerivationSnapshot(work *verifiedInput, candidate Candidate, s1, s2 *big.Int) error {
	derivation := candidate.Derivation
	if derivation.Outcome != AttemptCandidate {
		return candidateError("derivation outcome", fmt.Errorf("got %q, want %q", derivation.Outcome, AttemptCandidate))
	}
	if derivation.Signature1Sign != candidate.Signature1Sign ||
		derivation.Signature2Sign != candidate.Signature2Sign {
		return candidateError("derivation signs", errors.New("snapshot sign choices disagree with candidate"))
	}
	if derivation.S1 == nil || derivation.S1.Cmp(s1) != 0 {
		return candidateError("derivation s1", errors.New("snapshot s1 does not match the selected scalar"))
	}
	if derivation.S2 == nil || derivation.S2.Cmp(s2) != 0 {
		return candidateError("derivation s2", errors.New("snapshot s2 does not match the selected scalar"))
	}
	if derivation.EquationPrivateKey == "" ||
		derivation.NonceEquation1 == "" ||
		derivation.NonceEquation2 == "" ||
		derivation.AffineEquation == "" {
		return candidateError("derivation equations", errors.New("snapshot is missing an equation"))
	}

	det, rhs := equationTerms(work, s1, s2)
	if derivation.Determinant == nil || derivation.Determinant.Cmp(det) != 0 {
		return candidateError("derivation determinant", errors.New("snapshot determinant does not match recomputation"))
	}
	if derivation.RHS == nil || derivation.RHS.Cmp(rhs) != 0 {
		return candidateError("derivation rhs", errors.New("snapshot RHS does not match recomputation"))
	}
	return nil
}

func applySign(raw *big.Int, sign ScalarSign, order *big.Int) (*big.Int, error) {
	if raw == nil || raw.Sign() <= 0 || raw.Cmp(order) >= 0 {
		return nil, errors.New("raw signature scalar must be in [1, n-1]")
	}
	if sign != SignPositive && sign != SignNegative {
		return nil, fmt.Errorf("invalid scalar sign %d", int(sign))
	}
	used := new(big.Int).Set(raw)
	if sign == SignNegative {
		used.Sub(order, used)
		used.Mod(used, order)
	}
	if used.Sign() == 0 {
		return nil, errors.New("effective signature scalar is zero")
	}
	return used, nil
}

func validCandidateScalars(work *verifiedInput, candidate Candidate) error {
	for name, value := range map[string]*big.Int{
		"d":      candidate.D,
		"nonce1": candidate.Nonce1,
		"nonce2": candidate.Nonce2,
	} {
		if value == nil || value.Sign() <= 0 || value.Cmp(work.order) >= 0 {
			return candidateError(name, errors.New("must be in [1, n-1]"))
		}
	}
	if candidate.Signature1Sign != SignPositive && candidate.Signature1Sign != SignNegative {
		return candidateError("signature1 sign", errors.New("must be +1 or -1"))
	}
	if candidate.Signature2Sign != SignPositive && candidate.Signature2Sign != SignNegative {
		return candidateError("signature2 sign", errors.New("must be +1 or -1"))
	}
	return nil
}

func candidateError(field string, err error) error {
	return fmt.Errorf("%w: %s: %v", ErrCandidateInvalid, field, err)
}

func countDistinctPrivateKeys(candidates []Candidate) int {
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		seen[candidate.D.String()] = struct{}{}
	}
	return len(seen)
}
