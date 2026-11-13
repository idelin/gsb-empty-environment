package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"errors"
	"fmt"
	"hash"
	"math/big"
	"reflect"
)

func isSupportedAlgorithm(alg string) bool {
	switch alg {
	case "HS256", "HS384", "HS512",
		"RS256", "RS384", "RS512",
		"PS256", "PS384", "PS512",
		"ES256", "ES384", "ES512",
		"EdDSA":
		return true
	default:
		return false
	}
}

func verifyWithKeys(alg string, key any, input, signature []byte) error {
	if key == nil {
		return ErrKeyNotFound
	}

	switch keys := key.(type) {
	case []any:
		return verifyKeyList(alg, keys, input, signature)
	}

	value := reflect.ValueOf(key)
	if value.IsValid() && value.Kind() == reflect.Slice && value.Type().Elem().Kind() != reflect.Uint8 {
		if value.Len() == 0 {
			return ErrKeyNotFound
		}
		var failures []error
		for index := 0; index < value.Len(); index++ {
			err := verifyWithAlgorithm(alg, value.Index(index).Interface(), input, signature)
			if err == nil {
				return nil
			}
			failures = append(failures, err)
		}
		return errors.Join(failures...)
	}

	return verifyWithAlgorithm(alg, key, input, signature)
}

func verifyKeyList(alg string, keys []any, input, signature []byte) error {
	if len(keys) == 0 {
		return ErrKeyNotFound
	}
	var failures []error
	for _, key := range keys {
		err := verifyWithAlgorithm(alg, key, input, signature)
		if err == nil {
			return nil
		}
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func verifyWithAlgorithm(alg string, key any, input, signature []byte) error {
	switch alg {
	case "HS256", "HS384", "HS512":
		return verifyHMAC(alg, key, input, signature)
	case "RS256", "RS384", "RS512":
		return verifyRSA(alg, key, input, signature, false)
	case "PS256", "PS384", "PS512":
		return verifyRSA(alg, key, input, signature, true)
	case "ES256", "ES384", "ES512":
		return verifyECDSA(alg, key, input, signature)
	case "EdDSA":
		return verifyEdDSA(key, input, signature)
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, alg)
	}
}

func verifyHMAC(alg string, key any, input, signature []byte) error {
	keyBytes, err := hmacKeyBytes(key)
	if err != nil {
		return err
	}
	_, constructor, err := hashForAlgorithm(alg)
	if err != nil {
		return err
	}

	mac := hmac.New(constructor, keyBytes)
	mac.Write(input)
	expected := mac.Sum(nil)
	if !hmac.Equal(expected, signature) {
		return ErrInvalidSignature
	}
	return nil
}

func verifyRSA(alg string, key any, input, signature []byte, pss bool) error {
	publicKey, err := rsaPublicKey(key)
	if err != nil {
		return err
	}
	hashType, constructor, err := hashForAlgorithm(alg)
	if err != nil {
		return err
	}
	digest := digestWith(constructor, input)

	if pss {
		options := &rsa.PSSOptions{
			SaltLength: rsa.PSSSaltLengthEqualsHash,
			Hash:       hashType,
		}
		if err := rsa.VerifyPSS(publicKey, hashType, digest, signature, options); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
		}
		return nil
	}

	if err := rsa.VerifyPKCS1v15(publicKey, hashType, digest, signature); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	return nil
}

func verifyECDSA(alg string, key any, input, signature []byte) error {
	publicKey, err := ecdsaPublicKey(key)
	if err != nil {
		return err
	}

	var curve elliptic.Curve
	var coordinateSize int
	switch alg {
	case "ES256":
		curve = elliptic.P256()
		coordinateSize = 32
	case "ES384":
		curve = elliptic.P384()
		coordinateSize = 48
	case "ES512":
		curve = elliptic.P521()
		coordinateSize = 66
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, alg)
	}

	if publicKey.Curve == nil || publicKey.Curve.Params().Name != curve.Params().Name {
		return fmt.Errorf("%w: %s requires curve %s", ErrInvalidKey, alg, curve.Params().Name)
	}
	if publicKey.X == nil || publicKey.Y == nil || !publicKey.Curve.IsOnCurve(publicKey.X, publicKey.Y) {
		return fmt.Errorf("%w: ECDSA public key is not on curve %s", ErrInvalidKey, curve.Params().Name)
	}
	if len(signature) != 2*coordinateSize {
		return fmt.Errorf("%w: invalid ECDSA signature length", ErrInvalidSignature)
	}

	_, constructor, err := hashForAlgorithm(alg)
	if err != nil {
		return err
	}
	digest := digestWith(constructor, input)

	r := new(big.Int).SetBytes(signature[:coordinateSize])
	s := new(big.Int).SetBytes(signature[coordinateSize:])
	if !ecdsa.Verify(publicKey, digest, r, s) {
		return ErrInvalidSignature
	}
	return nil
}

func verifyEdDSA(key any, input, signature []byte) error {
	publicKey, err := ed25519PublicKey(key)
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, input, signature) {
		return ErrInvalidSignature
	}
	return nil
}

func hashForAlgorithm(alg string) (crypto.Hash, func() hash.Hash, error) {
	switch alg {
	case "HS256", "RS256", "PS256", "ES256":
		return crypto.SHA256, sha256.New, nil
	case "HS384", "RS384", "PS384", "ES384":
		return crypto.SHA384, sha512.New384, nil
	case "HS512", "RS512", "PS512", "ES512":
		return crypto.SHA512, sha512.New, nil
	default:
		return 0, nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, alg)
	}
}

func digestWith(constructor func() hash.Hash, input []byte) []byte {
	digest := constructor()
	digest.Write(input)
	return digest.Sum(nil)
}

func hmacKeyBytes(key any) ([]byte, error) {
	switch value := key.(type) {
	case []byte:
		return value, nil
	case string:
		return []byte(value), nil
	case *[]byte:
		if value == nil {
			return nil, fmt.Errorf("%w: nil HMAC key", ErrInvalidKey)
		}
		return *value, nil
	default:
		return nil, fmt.Errorf("%w: HMAC requires a []byte or string key, got %T", ErrInvalidKey, key)
	}
}

func rsaPublicKey(key any) (*rsa.PublicKey, error) {
	var result *rsa.PublicKey
	switch value := key.(type) {
	case *rsa.PublicKey:
		if value == nil {
			return nil, fmt.Errorf("%w: nil RSA key", ErrInvalidKey)
		}
		result = value
	case rsa.PublicKey:
		result = &value
	case crypto.Signer:
		publicKey, ok := value.Public().(*rsa.PublicKey)
		if ok {
			result = publicKey
		}
	case *x509.Certificate:
		if value == nil {
			return nil, fmt.Errorf("%w: nil certificate", ErrInvalidKey)
		}
		publicKey, ok := value.PublicKey.(*rsa.PublicKey)
		if ok {
			result = publicKey
		}
	case x509.Certificate:
		publicKey, ok := value.PublicKey.(*rsa.PublicKey)
		if ok {
			result = publicKey
		}
	}
	if result != nil {
		if result.N == nil || result.E < 2 {
			return nil, fmt.Errorf("%w: invalid RSA public key", ErrInvalidKey)
		}
		return result, nil
	}
	return nil, fmt.Errorf("%w: RSA algorithm requires an RSA public key, got %T", ErrInvalidKey, key)
}

func ecdsaPublicKey(key any) (*ecdsa.PublicKey, error) {
	switch value := key.(type) {
	case *ecdsa.PublicKey:
		if value == nil {
			return nil, fmt.Errorf("%w: nil ECDSA key", ErrInvalidKey)
		}
		return value, nil
	case ecdsa.PublicKey:
		return &value, nil
	case crypto.Signer:
		publicKey, ok := value.Public().(*ecdsa.PublicKey)
		if ok {
			return publicKey, nil
		}
	case *x509.Certificate:
		if value == nil {
			return nil, fmt.Errorf("%w: nil certificate", ErrInvalidKey)
		}
		publicKey, ok := value.PublicKey.(*ecdsa.PublicKey)
		if ok {
			return publicKey, nil
		}
	case x509.Certificate:
		publicKey, ok := value.PublicKey.(*ecdsa.PublicKey)
		if ok {
			return publicKey, nil
		}
	}
	return nil, fmt.Errorf("%w: ECDSA algorithm requires an ECDSA public key, got %T", ErrInvalidKey, key)
}

func ed25519PublicKey(key any) (ed25519.PublicKey, error) {
	var result ed25519.PublicKey
	switch value := key.(type) {
	case ed25519.PublicKey:
		result = value
	case *ed25519.PublicKey:
		if value == nil {
			return nil, fmt.Errorf("%w: nil Ed25519 key", ErrInvalidKey)
		}
		result = *value
	case crypto.Signer:
		publicKey, ok := value.Public().(ed25519.PublicKey)
		if ok {
			result = publicKey
		}
	case *x509.Certificate:
		if value == nil {
			return nil, fmt.Errorf("%w: nil certificate", ErrInvalidKey)
		}
		publicKey, ok := value.PublicKey.(ed25519.PublicKey)
		if ok {
			result = publicKey
		}
	case x509.Certificate:
		publicKey, ok := value.PublicKey.(ed25519.PublicKey)
		if ok {
			result = publicKey
		}
	}
	if len(result) == ed25519.PublicKeySize {
		return result, nil
	}
	return nil, fmt.Errorf("%w: EdDSA requires an Ed25519 public key, got %T", ErrInvalidKey, key)
}
