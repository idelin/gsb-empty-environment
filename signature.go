package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"fmt"
	"hash"
	"math/big"
)

// verifySignature checks signature over signingInput with the supplied public
// key and the JWT header algorithm. Only asymmetric algorithms are accepted.
func verifySignature(alg string, key crypto.PublicKey, signingInput, signature []byte) error {
	switch alg {
	case "RS256", "RS384", "RS512", "PS256", "PS384", "PS512":
		rsaKey, ok := key.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: %s requires an RSA key", ErrUnsupportedAlg, alg)
		}
		hashed, h, err := hashFor(alg, signingInput)
		if err != nil {
			return err
		}
		if alg[:2] == "PS" {
			err = rsa.VerifyPSS(rsaKey, h, hashed, signature, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		} else {
			err = rsa.VerifyPKCS1v15(rsaKey, h, hashed, signature)
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrSignature, err)
		}
		return nil
	case "ES256", "ES384", "ES512":
		ecKey, ok := key.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: %s requires an EC key", ErrUnsupportedAlg, alg)
		}
		hashed, _, err := hashFor(alg, signingInput)
		if err != nil {
			return err
		}
		half := len(signature) / 2
		if len(signature)%2 != 0 || half == 0 {
			return fmt.Errorf("%w: malformed ECDSA signature", ErrSignature)
		}
		r := new(big.Int).SetBytes(signature[:half])
		s := new(big.Int).SetBytes(signature[half:])
		if !ecdsa.Verify(ecKey, hashed, r, s) {
			return ErrSignature
		}
		return nil
	case "EdDSA":
		edKey, ok := key.(ed25519.PublicKey)
		if !ok {
			return fmt.Errorf("%w: EdDSA requires an Ed25519 key", ErrUnsupportedAlg)
		}
		if !ed25519.Verify(edKey, signingInput, signature) {
			return ErrSignature
		}
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedAlg, alg)
	}
}

func hashFor(alg string, input []byte) (sum []byte, h crypto.Hash, err error) {
	var hasher hash.Hash
	switch alg {
	case "RS256", "PS256", "ES256":
		h, hasher = crypto.SHA256, sha256.New()
	case "RS384", "PS384", "ES384":
		h, hasher = crypto.SHA384, sha512.New384()
	case "RS512", "PS512", "ES512":
		h, hasher = crypto.SHA512, sha512.New()
	default:
		return nil, 0, fmt.Errorf("%w: %q", ErrUnsupportedAlg, alg)
	}
	if _, err := hasher.Write(input); err != nil {
		return nil, 0, err
	}
	return hasher.Sum(nil), h, nil
}

// sign is a test/helper utility that produces a compact JWS; it is unexported
// and used only through the package's own tests.
func sign(alg string, key crypto.Signer, headerJSON, payload []byte) (string, error) {
	signingInput := encodeSegment(headerJSON) + "." + encodeSegment(payload)
	var signature []byte
	var err error
	switch alg {
	case "RS256", "RS384", "RS512":
		sum, h, hashErr := hashFor(alg, []byte(signingInput))
		if hashErr != nil {
			return "", hashErr
		}
		signature, err = rsa.SignPKCS1v15(rand.Reader, key.(*rsa.PrivateKey), h, sum)
	case "PS256", "PS384", "PS512":
		sum, h, hashErr := hashFor(alg, []byte(signingInput))
		if hashErr != nil {
			return "", hashErr
		}
		signature, err = rsa.SignPSS(rand.Reader, key.(*rsa.PrivateKey), h, sum, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
	case "ES256", "ES384", "ES512":
		sum, _, hashErr := hashFor(alg, []byte(signingInput))
		if hashErr != nil {
			return "", hashErr
		}
		var r, s *big.Int
		r, s, err = ecdsa.Sign(rand.Reader, key.(*ecdsa.PrivateKey), sum)
		if err == nil {
			curveBits := key.Public().(*ecdsa.PublicKey).Curve.Params().BitSize
			size := (curveBits + 7) / 8
			signature = make([]byte, size*2)
			r.FillBytes(signature[:size])
			s.FillBytes(signature[size:])
		}
	case "EdDSA":
		signature = ed25519.Sign(key.(ed25519.PrivateKey), []byte(signingInput))
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedAlg, alg)
	}
	if err != nil {
		return "", errors.New("netsec: signing failed: " + err.Error())
	}
	return signingInput + "." + encodeSegment(signature), nil
}
