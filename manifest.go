package netsec

import (
	"fmt"
	"io"
	"math/big"
)

// PublicShare is one member's public share Y_i = x_i G and non-zero identifier.
type PublicShare struct {
	ID uint32
	Y  Point
}

// Manifest is the fixed public member list. Y is the aggregate public key.
type Manifest struct {
	Threshold int
	Members   []PublicShare
	Y         Point
}

// SecretShare is a member's Shamir evaluation x_i = f(i).
type SecretShare struct {
	ID uint32
	X  Scalar
}

// Public returns the member's public share.
func (s SecretShare) Public() (PublicShare, error) {
	if s.ID == 0 {
		return PublicShare{}, fmt.Errorf("%w: member identifier must be non-zero", ErrInvalidInput)
	}
	if !s.X.valid() {
		return PublicShare{}, fmt.Errorf("%w: secret share is not canonical", ErrInvalidScalar)
	}
	y, err := pointToCanonical(scalarBaseMultCurve(s.X.Big()))
	if err != nil {
		return PublicShare{}, fmt.Errorf("%w: secret share produces the identity", ErrInvalidInput)
	}
	return PublicShare{ID: s.ID, Y: y}, nil
}

func validateManifestStructure(m Manifest) error {
	if err := validateMembers(m.Threshold, m.Members); err != nil {
		return err
	}
	if err := m.Y.valid(); err != nil {
		return fmt.Errorf("%w: aggregate public key: %v", ErrInvalidManifest, err)
	}
	return nil
}

func validateMembers(threshold int, members []PublicShare) error {
	if threshold < 1 || threshold > MaxThreshold {
		return fmt.Errorf("%w: threshold must be in [1,%d]", ErrInvalidManifest, MaxThreshold)
	}
	if len(members) < threshold {
		return fmt.Errorf("%w: manifest has fewer members than the threshold", ErrInvalidManifest)
	}
	if len(members) > MaxMembers {
		return fmt.Errorf("%w: manifest has more than %d members", ErrInvalidManifest, MaxMembers)
	}

	seen := make(map[uint32]struct{}, len(members))
	for _, member := range members {
		if member.ID == 0 {
			return fmt.Errorf("%w: member identifier must be non-zero", ErrInvalidManifest)
		}
		if _, exists := seen[member.ID]; exists {
			return fmt.Errorf("%w: duplicate member identifier %d", ErrInvalidManifest, member.ID)
		}
		seen[member.ID] = struct{}{}
		if err := member.Y.valid(); err != nil {
			return fmt.Errorf("%w: member %d public share: %v", ErrInvalidManifest, member.ID, err)
		}
	}
	return nil
}

// ValidateManifest checks the member count, identifiers, canonical points, and
// that Y equals the Lagrange-at-zero combination of all listed public shares.
func ValidateManifest(m Manifest) error {
	if err := validateManifestStructure(m); err != nil {
		return err
	}
	aggregate, err := AggregatePublicKey(m)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if aggregate != m.Y {
		return fmt.Errorf("%w: aggregate public key does not match the member shares", ErrInvalidManifest)
	}
	return nil
}

// AggregatePublicKey computes the Lagrange-at-zero combination of every public
// share in the manifest. Honest Shamir shares all interpolate to f(0)G.
func AggregatePublicKey(m Manifest) (Point, error) {
	if err := validateMembers(m.Threshold, m.Members); err != nil {
		return Point{}, err
	}
	ids := make([]uint32, len(m.Members))
	points := make([]Point, len(m.Members))
	for i, member := range m.Members {
		ids[i] = member.ID
		points[i] = member.Y
	}
	return lagrangeCombination(ids, points)
}

func lagrangeCombination(ids []uint32, points []Point) (Point, error) {
	if len(ids) != len(points) || len(ids) == 0 {
		return Point{}, fmt.Errorf("%w: mismatched Lagrange inputs", ErrInvalidInput)
	}
	coefficients, err := lagrangeCoefficientsAtZero(ids)
	if err != nil {
		return Point{}, err
	}
	return linearCombination(points, coefficients)
}

func lagrangeCoefficientsAtZero(ids []uint32) ([]*big.Int, error) {
	if len(ids) == 0 || len(ids) > MaxMembers {
		return nil, fmt.Errorf("%w: invalid interpolation set size", ErrInvalidInput)
	}
	seen := make(map[uint32]struct{}, len(ids))
	values := make([]*big.Int, len(ids))
	for i, id := range ids {
		if id == 0 {
			return nil, fmt.Errorf("%w: zero member identifier", ErrInvalidInput)
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("%w: duplicate member identifier %d", ErrInvalidInput, id)
		}
		seen[id] = struct{}{}
		values[i] = new(big.Int).SetUint64(uint64(id))
	}

	out := make([]*big.Int, len(ids))
	for i, xi := range values {
		numerator := big.NewInt(1)
		denominator := big.NewInt(1)
		for j, xj := range values {
			if i == j {
				continue
			}
			numerator.Mul(numerator, modOrder(new(big.Int).Neg(xj)))
			denominator.Mul(denominator, modOrder(new(big.Int).Sub(xi, xj)))
		}
		inverse, err := invertScalar(denominator)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid interpolation point: %v", ErrInvalidInput, err)
		}
		out[i] = modOrder(new(big.Int).Mul(numerator, inverse))
	}
	return out, nil
}

func linearCombination(points []Point, coefficients []*big.Int) (Point, error) {
	if len(points) != len(coefficients) || len(points) == 0 {
		return Point{}, fmt.Errorf("%w: mismatched linear combination", ErrInvalidInput)
	}
	result := identityPoint()
	for i, point := range points {
		internal, err := pointFromCanonical(point)
		if err != nil {
			return Point{}, err
		}
		term := scalarMultCurve(coefficients[i], internal)
		result = addCurvePoints(result, term)
	}
	return pointToCanonical(result)
}

// GenerateDealer samples a degree-(threshold-1) polynomial with the supplied
// constant term and returns the matching public manifest and secret shares.
func GenerateDealer(secret Scalar, threshold int, ids []uint32, source io.Reader) (Manifest, []SecretShare, error) {
	if !secret.valid() || secret.IsZero() {
		return Manifest{}, nil, fmt.Errorf("%w: dealer secret must be non-zero and canonical", ErrInvalidScalar)
	}
	if threshold < 1 || threshold > MaxThreshold {
		return Manifest{}, nil, fmt.Errorf("%w: threshold must be in [1,%d]", ErrInvalidInput, MaxThreshold)
	}
	if len(ids) < threshold || len(ids) > MaxMembers {
		return Manifest{}, nil, fmt.Errorf("%w: member count must be in [%d,%d]", ErrInvalidInput, threshold, MaxMembers)
	}

	seen := make(map[uint32]struct{}, len(ids))
	ordered := append([]uint32(nil), ids...)
	for _, id := range ordered {
		if id == 0 {
			return Manifest{}, nil, fmt.Errorf("%w: member identifier must be non-zero", ErrInvalidInput)
		}
		if _, exists := seen[id]; exists {
			return Manifest{}, nil, fmt.Errorf("%w: duplicate member identifier %d", ErrInvalidInput, id)
		}
		seen[id] = struct{}{}
	}

	var coefficients []*big.Int
	var evaluations []*big.Int
	for attempt := 0; attempt < 32; attempt++ {
		coefficients = make([]*big.Int, threshold)
		coefficients[0] = secret.Big()
		for i := 1; i < threshold; i++ {
			value, err := randomScalar(source)
			if err != nil {
				return Manifest{}, nil, err
			}
			coefficients[i] = value
		}
		if threshold > 1 {
			value, err := randomNonZeroScalar(source)
			if err != nil {
				return Manifest{}, nil, err
			}
			coefficients[threshold-1] = value
		}

		evaluations = make([]*big.Int, len(ordered))
		valid := true
		for i, id := range ordered {
			evaluations[i] = evaluatePolynomial(coefficients, new(big.Int).SetUint64(uint64(id)))
			if evaluations[i].Sign() == 0 {
				valid = false
				break
			}
		}
		if valid {
			break
		}
		if attempt == 31 {
			return Manifest{}, nil, fmt.Errorf("%w: could not produce non-zero evaluations", ErrInvalidInput)
		}
	}

	members := make([]PublicShare, len(ordered))
	secretShares := make([]SecretShare, len(ordered))
	for i, id := range ordered {
		x, err := ScalarFromBig(evaluations[i])
		if err != nil {
			return Manifest{}, nil, err
		}
		y, err := pointToCanonical(scalarBaseMultCurve(evaluations[i]))
		if err != nil {
			return Manifest{}, nil, fmt.Errorf("%w: public share: %v", ErrInvalidInput, err)
		}
		members[i] = PublicShare{ID: id, Y: y}
		secretShares[i] = SecretShare{ID: id, X: x}
	}

	aggregate, err := pointToCanonical(scalarBaseMultCurve(secret.Big()))
	if err != nil {
		return Manifest{}, nil, err
	}
	manifest := Manifest{
		Threshold: threshold,
		Members:   members,
		Y:         aggregate,
	}
	if err := ValidateManifest(manifest); err != nil {
		return Manifest{}, nil, err
	}
	return manifest, secretShares, nil
}

func evaluatePolynomial(coefficients []*big.Int, x *big.Int) *big.Int {
	result := new(big.Int)
	for i := len(coefficients) - 1; i >= 0; i-- {
		result.Mul(result, x)
		result.Add(result, coefficients[i])
		result.Mod(result, curveOrder)
	}
	return result
}
