package netsec

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io"
	"math/big"
)

// NewBlindingFactor returns a random invertible factor in (1, N).
func NewBlindingFactor(modulus *big.Int) (*big.Int, error) {
	return NewBlindingFactorFrom(rand.Reader, modulus)
}

// NewBlindingFactorFrom returns a random invertible factor in (1, N) using the
// supplied entropy source.
func NewBlindingFactorFrom(reader io.Reader, modulus *big.Int) (*big.Int, error) {
	if reader == nil {
		return nil, fmt.Errorf("%w: nil entropy source", ErrInvalidBlind)
	}
	if modulus == nil || modulus.Sign() <= 0 || modulus.Cmp(big.NewInt(2)) <= 0 {
		return nil, fmt.Errorf("%w: modulus must be greater than two", ErrInvalidConfig)
	}

	two := big.NewInt(2)
	one := big.NewInt(1)
	for {
		candidate, err := rand.Int(reader, modulus)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidBlind, err)
		}
		if candidate.Cmp(two) < 0 {
			continue
		}
		if new(big.Int).GCD(nil, nil, candidate, modulus).Cmp(one) == 0 {
			return candidate, nil
		}
	}
}

// BlindMessage computes b = message * r^e mod N.
func BlindMessage(message *big.Int, factor *big.Int, modulus *big.Int, exponent int) (*big.Int, error) {
	if message == nil || modulus == nil || factor == nil {
		return nil, fmt.Errorf("%w: nil input", ErrInvalidBlind)
	}
	if modulus.Sign() <= 0 || modulus.Cmp(big.NewInt(1)) <= 0 {
		return nil, fmt.Errorf("%w: modulus must exceed one", ErrInvalidConfig)
	}
	if exponent < 1 {
		return nil, fmt.Errorf("%w: public exponent must be positive", ErrInvalidConfig)
	}
	if message.Sign() < 0 || message.Cmp(modulus) >= 0 {
		return nil, fmt.Errorf("%w: message must be in [0, N)", ErrInvalidBlind)
	}
	if factor.Sign() <= 0 || factor.Cmp(modulus) >= 0 {
		return nil, fmt.Errorf("%w: blinding factor must be in (0, N)", ErrInvalidBlind)
	}
	if new(big.Int).GCD(nil, nil, factor, modulus).Cmp(big.NewInt(1)) != 0 {
		return nil, ErrBlindNotCoprime
	}

	rToE := new(big.Int).Exp(factor, big.NewInt(int64(exponent)), modulus)
	blind := new(big.Int).Mul(message, rToE)
	blind.Mod(blind, modulus)
	if err := ValidateBlind(blind, modulus); err != nil {
		return nil, err
	}
	return blind, nil
}

// ValidateBlind enforces the signing host's 1 < b < N and gcd(b, N) = 1 rule.
func ValidateBlind(blind *big.Int, modulus *big.Int) error {
	if blind == nil {
		return fmt.Errorf("%w: nil blind value", ErrInvalidBlind)
	}
	if modulus == nil || modulus.Cmp(big.NewInt(1)) <= 0 {
		return fmt.Errorf("%w: modulus must exceed one", ErrInvalidConfig)
	}
	if blind.Cmp(big.NewInt(1)) <= 0 || blind.Cmp(modulus) >= 0 {
		return fmt.Errorf("%w: require 1 < b < N", ErrInvalidBlind)
	}
	if new(big.Int).GCD(nil, nil, blind, modulus).Cmp(big.NewInt(1)) != 0 {
		return ErrBlindNotCoprime
	}
	return nil
}

func BlindWithPublicKey(message *big.Int, factor *big.Int, publicKey *rsa.PublicKey) (*big.Int, error) {
	if publicKey == nil {
		return nil, fmt.Errorf("%w: nil RSA public key", ErrInvalidConfig)
	}
	return BlindMessage(message, factor, publicKey.N, publicKey.E)
}

func BlindClaimDigest(claim ClaimDigest, factor *big.Int, modulus *big.Int, exponent int) (*big.Int, error) {
	return BlindMessage(claim.BigInt(), factor, modulus, exponent)
}

// Unblind computes s * r^-1 mod N.
func Unblind(signature *big.Int, factor *big.Int, modulus *big.Int) (*big.Int, error) {
	if signature == nil || factor == nil || modulus == nil {
		return nil, fmt.Errorf("%w: nil input", ErrInvalidBlind)
	}
	if signature.Sign() <= 0 || signature.Cmp(modulus) >= 0 {
		return nil, fmt.Errorf("%w: signature outside (0, N)", ErrInvalidSignature)
	}
	if factor.Sign() <= 0 || factor.Cmp(modulus) >= 0 {
		return nil, fmt.Errorf("%w: blinding factor outside (0, N)", ErrInvalidBlind)
	}
	inverse := new(big.Int).ModInverse(factor, modulus)
	if inverse == nil {
		return nil, ErrBlindNotCoprime
	}
	message := new(big.Int).Mul(signature, inverse)
	message.Mod(message, modulus)
	return message, nil
}

func VerifyBlindSignature(modulus *big.Int, exponent int, message *big.Int, signature *big.Int) bool {
	if modulus == nil || message == nil || signature == nil {
		return false
	}
	if modulus.Cmp(big.NewInt(1)) <= 0 || exponent < 1 {
		return false
	}
	if message.Sign() < 0 || message.Cmp(modulus) >= 0 {
		return false
	}
	if signature.Sign() <= 0 || signature.Cmp(modulus) >= 0 {
		return false
	}
	actual := new(big.Int).Exp(signature, big.NewInt(int64(exponent)), modulus)
	return actual.Cmp(message) == 0
}

func VerifyWithPublicKey(publicKey *rsa.PublicKey, message *big.Int, signature *big.Int) bool {
	if publicKey == nil {
		return false
	}
	return VerifyBlindSignature(publicKey.N, publicKey.E, message, signature)
}

// BlindSignatureClient is a small helper bundling a client's blinding factor.
type BlindSignatureClient struct {
	Modulus  *big.Int
	Exponent int
	Factor   *big.Int
	Message  *big.Int
	Blind    *big.Int
}

func NewBlindSignatureClient(message *big.Int, modulus *big.Int, exponent int) (*BlindSignatureClient, error) {
	factor, err := NewBlindingFactor(modulus)
	if err != nil {
		return nil, err
	}
	blind, err := BlindMessage(message, factor, modulus, exponent)
	if err != nil {
		return nil, err
	}
	return &BlindSignatureClient{
		Modulus:  new(big.Int).Set(modulus),
		Exponent: exponent,
		Factor:   factor,
		Message:  new(big.Int).Set(message),
		Blind:    blind,
	}, nil
}

func (c *BlindSignatureClient) Unblind(signature *big.Int) (*big.Int, error) {
	message, err := Unblind(signature, c.Factor, c.Modulus)
	if err != nil {
		return nil, err
	}
	if !VerifyBlindSignature(c.Modulus, c.Exponent, c.Message, message) {
		return nil, fmt.Errorf("%w: unblinded signature does not verify", ErrInvalidSignature)
	}
	return message, nil
}
