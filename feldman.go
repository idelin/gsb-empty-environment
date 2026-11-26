package threshold

import (
	"fmt"
	"io"
	"math/big"
)

const (
	popDomain       = "GSB/threshold-dkg/pop-schnorr/v1"
	feldmanDomain   = "GSB/threshold-dkg/feldman/v1"
	rhoDomain       = "GSB/frost/binding-factor-rho/v1"
	challengeDomain = "GSB/frost/schnorr-challenge/v1"
)

// SchnorrProof is the domain-separated proof of knowledge of the constant
// coefficient of a Feldman polynomial.
type SchnorrProof struct {
	Nonce    []byte `json:"nonce"`
	Response []byte `json:"response"`
}

// Round1Message is a dealer's degree t-1 Feldman commitment vector and PoP.
type Round1Message struct {
	Dealer      ID           `json:"dealer"`
	Commitments [][]byte     `json:"commitments"`
	PoP         SchnorrProof `json:"pop"`
}

func popChallenge(g Group, context []byte, participant ID, c0, nonceCommitment []byte) *big.Int {
	return g.HashToScalar(
		popDomain,
		[]byte("pop"),
		context,
		[]byte(participant),
		c0,
		nonceCommitment,
	)
}

func signPoP(g Group, rng io.Reader, context []byte, participant ID, secret *big.Int, c0 []byte) (SchnorrProof, error) {
	nonce, err := g.RandomScalar(rng)
	if err != nil {
		return SchnorrProof{}, err
	}
	rPoint := g.ScalarBaseMult(nonce)
	rBytes := g.Encode(rPoint)
	challenge := popChallenge(g, context, participant, c0, rBytes)
	response := new(big.Int).Add(nonce, new(big.Int).Mul(challenge, secret))
	response.Mod(response, g.Order())
	return SchnorrProof{Nonce: rBytes, Response: encodeScalar(g, response)}, nil
}

// VerifyProofOfPossession verifies the tagged Schnorr PoK for C0.
func VerifyProofOfPossession(g Group, context []byte, participant ID, c0 []byte, proof SchnorrProof) error {
	if g == nil {
		return ErrGroupRequired
	}
	rPoint, err := decodePointBytes(g, proof.Nonce)
	if err != nil {
		return fmt.Errorf("%w: proof nonce: %v", ErrProofOfPossession, err)
	}
	c0Point, err := decodePointBytes(g, c0)
	if err != nil {
		return fmt.Errorf("%w: C0: %v", ErrProofOfPossession, err)
	}
	response, err := parseScalar(g, proof.Response)
	if err != nil {
		return fmt.Errorf("%w: response: %v", ErrProofOfPossession, err)
	}
	challenge := popChallenge(g, context, participant, c0, proof.Nonce)

	left := g.ScalarBaseMult(response)
	right := g.Add(rPoint, g.ScalarMult(c0Point, challenge))
	if !g.Equal(left, right) {
		return fmt.Errorf("%w: participant %q", ErrProofOfPossession, participant)
	}
	return nil
}

func generatePolynomial(g Group, rng io.Reader, degree int) ([]*big.Int, [][]byte, error) {
	coeffs := make([]*big.Int, degree+1)
	commitments := make([][]byte, degree+1)
	for i := range coeffs {
		a, err := g.RandomScalar(rng)
		if err != nil {
			return nil, nil, err
		}
		coeffs[i] = a
		commitments[i] = g.Encode(g.ScalarBaseMult(a))
	}
	return coeffs, commitments, nil
}

func evaluatePolynomial(g Group, coeffs []*big.Int, x int) *big.Int {
	order := g.Order()
	xx := big.NewInt(int64(x))
	result := big.NewInt(0)
	for k := len(coeffs) - 1; k >= 0; k-- {
		result.Mul(result, xx)
		result.Add(result, coeffs[k])
		result.Mod(result, order)
	}
	return result
}

// VerifyShare checks g^share == product_k C_k^(index^k), written additively
// for the abstract Group as share*G == sum_k index^k * C_k.
func VerifyShare(g Group, commitments [][]byte, recipientIndex int, share []byte) error {
	if g == nil {
		return ErrGroupRequired
	}
	if recipientIndex == 0 || big.NewInt(int64(recipientIndex)).Cmp(g.Order()) >= 0 {
		return fmt.Errorf("%w: index=%d", ErrInvalidParticipantIndex, recipientIndex)
	}
	s, err := parseScalar(g, share)
	if err != nil {
		return err
	}
	if len(commitments) == 0 {
		return fmt.Errorf("%w: empty commitment vector", ErrInvalidCommitment)
	}

	right := g.ScalarBaseMult(big.NewInt(0))
	x := big.NewInt(int64(recipientIndex))
	xPower := big.NewInt(1)
	order := g.Order()
	for k, encoded := range commitments {
		c, err := decodePointBytes(g, encoded)
		if err != nil {
			return fmt.Errorf("%w: C_%d: %v", ErrInvalidCommitment, k, err)
		}
		term := g.ScalarMult(c, xPower)
		right = g.Add(right, term)
		xPower.Mul(xPower, x).Mod(xPower, order)
	}

	left := g.ScalarBaseMult(s)
	if !g.Equal(left, right) {
		return fmt.Errorf("%w: recipient index %d", ErrInvalidShare, recipientIndex)
	}
	return nil
}

func evaluateCommitmentVector(g Group, commitments [][]byte, recipientIndex int) ([]byte, error) {
	x := big.NewInt(int64(recipientIndex))
	xPower := big.NewInt(1)
	order := g.Order()
	result := g.ScalarBaseMult(big.NewInt(0))
	for _, encoded := range commitments {
		c, err := decodePointBytes(g, encoded)
		if err != nil {
			return nil, err
		}
		result = g.Add(result, g.ScalarMult(c, xPower))
		xPower.Mul(xPower, x).Mod(xPower, order)
	}
	return g.Encode(result), nil
}

func addEncodedPoints(g Group, encoded [][]byte) ([]byte, error) {
	sum := g.ScalarBaseMult(big.NewInt(0))
	for _, b := range encoded {
		p, err := decodePointBytes(g, b)
		if err != nil {
			return nil, err
		}
		sum = g.Add(sum, p)
	}
	return g.Encode(sum), nil
}
