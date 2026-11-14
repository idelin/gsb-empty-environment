package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
)

var rawURL = base64.RawURLEncoding

type jwsHeader struct {
	Algorithm string   `json:"alg"`
	KeyID     string   `json:"kid"`
	Type      string   `json:"typ,omitempty"`
	Critical  []string `json:"crit,omitempty"`
}

func (ks *KeySet) signJWS(payload []byte) (string, error) {
	entry, err := ks.activeEntry()
	if err != nil {
		return "", err
	}
	header := jwsHeader{
		Algorithm: entry.algorithm,
		KeyID:     entry.id,
		Type:      "JWT",
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	signingInput := rawURL.EncodeToString(headerJSON) + "." + rawURL.EncodeToString(payload)
	signature, err := signBytes(entry, []byte(signingInput))
	if err != nil {
		return "", err
	}
	return signingInput + "." + rawURL.EncodeToString(signature), nil
}

func signBytes(entry *keyEntry, signingInput []byte) ([]byte, error) {
	algorithm := entry.algorithm
	switch {
	case algorithm == "EdDSA":
		return entry.signer.Sign(rand.Reader, signingInput, crypto.Hash(0))
	case len(algorithm) == 5 && algorithm[:2] == "RS":
		hash, err := hashForAlgorithm(algorithm)
		if err != nil {
			return nil, err
		}
		digest := digestBytes(hash, signingInput)
		return entry.signer.Sign(rand.Reader, digest, hash)
	case len(algorithm) == 5 && algorithm[:2] == "PS":
		hash, err := hashForAlgorithm(algorithm)
		if err != nil {
			return nil, err
		}
		digest := digestBytes(hash, signingInput)
		return entry.signer.Sign(rand.Reader, digest, &rsa.PSSOptions{
			SaltLength: rsa.PSSSaltLengthEqualsHash,
			Hash:       hash,
		})
	case len(algorithm) == 5 && algorithm[:2] == "ES":
		hash, err := hashForAlgorithm(algorithm)
		if err != nil {
			return nil, err
		}
		digest := digestBytes(hash, signingInput)
		der, err := entry.signer.Sign(rand.Reader, digest, hash)
		if err != nil {
			return nil, err
		}
		var parsed struct {
			R, S *big.Int
		}
		if _, err := asn1.Unmarshal(der, &parsed); err != nil {
			return nil, fmt.Errorf("netsec: malformed ECDSA signature: %w", err)
		}
		size := curveByteSize(entry.public)
		if size == 0 {
			return nil, fmt.Errorf("%w: invalid ECDSA curve", ErrUnsupportedAlgorithm)
		}
		signature := make([]byte, size*2)
		parsed.R.FillBytes(signature[:size])
		parsed.S.FillBytes(signature[size:])
		return signature, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedAlgorithm, algorithm)
	}
}

func verifyBytes(entry *keyEntry, algorithm string, signingInput, signature []byte) error {
	switch {
	case algorithm == "EdDSA":
		public, ok := ed25519Public(entry.public)
		if !ok {
			return fmt.Errorf("%w: key is not Ed25519", ErrUnsupportedAlgorithm)
		}
		if !ed25519.Verify(public, signingInput, signature) {
			return ErrInvalidToken
		}
		return nil
	case len(algorithm) == 5 && algorithm[:2] == "RS":
		public, ok := entry.public.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: key is not RSA", ErrUnsupportedAlgorithm)
		}
		hash, err := hashForAlgorithm(algorithm)
		if err != nil {
			return err
		}
		digest := digestBytes(hash, signingInput)
		if err := rsa.VerifyPKCS1v15(public, hash, digest, signature); err != nil {
			return ErrInvalidToken
		}
		return nil
	case len(algorithm) == 5 && algorithm[:2] == "PS":
		public, ok := entry.public.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: key is not RSA", ErrUnsupportedAlgorithm)
		}
		hash, err := hashForAlgorithm(algorithm)
		if err != nil {
			return err
		}
		digest := digestBytes(hash, signingInput)
		if err := rsa.VerifyPSS(public, hash, digest, signature, &rsa.PSSOptions{
			SaltLength: rsa.PSSSaltLengthEqualsHash,
			Hash:       hash,
		}); err != nil {
			return ErrInvalidToken
		}
		return nil
	case len(algorithm) == 5 && algorithm[:2] == "ES":
		public, ok := entry.public.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: key is not ECDSA", ErrUnsupportedAlgorithm)
		}
		size := curveByteSize(public)
		if size == 0 || len(signature) != size*2 {
			return ErrInvalidToken
		}
		hash, err := hashForAlgorithm(algorithm)
		if err != nil {
			return err
		}
		digest := digestBytes(hash, signingInput)
		r := new(big.Int).SetBytes(signature[:size])
		s := new(big.Int).SetBytes(signature[size:])
		if !ecdsa.Verify(public, digest, r, s) {
			return ErrInvalidToken
		}
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedAlgorithm, algorithm)
	}
}

func hashForAlgorithm(algorithm string) (crypto.Hash, error) {
	switch algorithm {
	case "RS256", "PS256", "ES256":
		return crypto.SHA256, nil
	case "RS384", "PS384", "ES384":
		return crypto.SHA384, nil
	case "RS512", "PS512", "ES512":
		return crypto.SHA512, nil
	default:
		return 0, fmt.Errorf("%w: %s", ErrUnsupportedAlgorithm, algorithm)
	}
}

func digestBytes(hash crypto.Hash, data []byte) []byte {
	switch hash {
	case crypto.SHA256:
		sum := sha256.Sum256(data)
		return sum[:]
	case crypto.SHA384:
		sum := sha512.Sum384(data)
		return sum[:]
	case crypto.SHA512:
		sum := sha512.Sum512(data)
		return sum[:]
	default:
		return nil
	}
}

func curveByteSize(public crypto.PublicKey) int {
	key, ok := public.(*ecdsa.PublicKey)
	if !ok || key.Curve == nil {
		return 0
	}
	return (key.Curve.Params().BitSize + 7) / 8
}

func ed25519Public(public crypto.PublicKey) (ed25519.PublicKey, bool) {
	switch key := public.(type) {
	case ed25519.PublicKey:
		return key, true
	case *ed25519.PublicKey:
		if key == nil {
			return nil, false
		}
		return *key, true
	default:
		return nil, false
	}
}

func decodeSegment(segment string) ([]byte, error) {
	data, err := rawURL.DecodeString(segment)
	if err != nil {
		if padded, padErr := base64.URLEncoding.DecodeString(segment); padErr == nil {
			return padded, nil
		}
		return nil, err
	}
	return data, nil
}

func parseJWS(raw string) (header jwsHeader, payload []byte, signingInput, signature []byte, err error) {
	parts, err := splitJWS(raw)
	if err != nil {
		return header, nil, nil, nil, err
	}
	headerBytes, err := decodeSegment(parts[0])
	if err != nil {
		return header, nil, nil, nil, fmt.Errorf("%w: malformed header", ErrInvalidToken)
	}
	if err := rejectDuplicateJSONKeys(headerBytes); err != nil {
		return header, nil, nil, nil, err
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return header, nil, nil, nil, fmt.Errorf("%w: malformed header", ErrInvalidToken)
	}
	payload, err = decodeSegment(parts[1])
	if err != nil {
		return header, nil, nil, nil, fmt.Errorf("%w: malformed payload", ErrInvalidToken)
	}
	signature, err = decodeSegment(parts[2])
	if err != nil {
		return header, nil, nil, nil, fmt.Errorf("%w: malformed signature", ErrInvalidToken)
	}
	if len(signature) == 0 {
		return header, nil, nil, nil, fmt.Errorf("%w: empty signature", ErrInvalidToken)
	}
	signingInput = []byte(parts[0] + "." + parts[1])
	return header, payload, signingInput, signature, nil
}

func splitJWS(raw string) ([3]string, error) {
	var parts [3]string
	if len(raw) == 0 {
		return parts, fmt.Errorf("%w: invalid token size", ErrInvalidToken)
	}
	first := -1
	second := -1
	for i := 0; i < len(raw); i++ {
		if raw[i] != '.' {
			continue
		}
		if first == -1 {
			first = i
		} else if second == -1 {
			second = i
		} else {
			return parts, fmt.Errorf("%w: too many segments", ErrInvalidToken)
		}
	}
	if first <= 0 || second <= first+1 || second >= len(raw)-1 {
		return parts, fmt.Errorf("%w: malformed compact JWS", ErrInvalidToken)
	}
	parts[0] = raw[:first]
	parts[1] = raw[first+1 : second]
	parts[2] = raw[second+1:]
	return parts, nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := walkJSONValue(decoder); err != nil {
		return fmt.Errorf("%w: malformed JSON", ErrInvalidToken)
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return fmt.Errorf("%w: malformed JSON", ErrInvalidToken)
		}
		_ = token
		return fmt.Errorf("%w: trailing JSON data", ErrInvalidToken)
	}
	return nil
}

func walkJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return errors.New("duplicate object key")
			}
			seen[key] = struct{}{}
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim('}') {
			return errors.New("unterminated object")
		}
	case '[':
		for decoder.More() {
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim(']') {
			return errors.New("unterminated array")
		}
	default:
		return errors.New("unexpected delimiter")
	}
	return nil
}

func constantTimeEqual(a, b []byte) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare(a, b) == 1
}
