package netsec

import (
	"bytes"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Sentinel errors returned by ValidateCompactJWE. Errors may be wrapped with
// contextual detail; use errors.Is for classification.
var (
	ErrMalformedToken    = errors.New("netsec: malformed JWE compact token")
	ErrMalformedHeader   = errors.New("netsec: malformed JWE protected header")
	ErrDuplicateHeader   = errors.New("netsec: duplicate JWE header field")
	ErrUnsupportedAlg    = errors.New("netsec: alg not in allow table")
	ErrUnsupportedEnc    = errors.New("netsec: enc not in allow table")
	ErrAlgorithmMismatch = errors.New("netsec: alg/enc key-length mismatch")
	ErrCompression       = errors.New("netsec: zip header is forbidden")
	ErrEPK               = errors.New("netsec: invalid ECDH-ES ephemeral public key")
)

var (
	allowedAlg = map[string]bool{
		// Direct key agreement / direct CEK.
		"dir":     true,
		"ECDH-ES": true,
		// ECDH-ES key agreement with key wrapping.
		"ECDH-ES+A128KW": true,
		"ECDH-ES+A192KW": true,
		"ECDH-ES+A256KW": true,
		// AES key wrapping.
		"A128KW": true,
		"A192KW": true,
		"A256KW": true,
		// RSA key management (RFC 7518; RSA1_5 intentionally excluded).
		"RSA-OAEP":     true,
		"RSA-OAEP-256": true,
	}

	allowedEnc = map[string]bool{
		"A128GCM":       true,
		"A192GCM":       true,
		"A256GCM":       true,
		"A128CBC-HS256": true,
		"A192CBC-HS384": true,
		"A256CBC-HS512": true,
	}

	// Content-encryption key sizes in bytes per RFC 7518 section 5.
	cekSize = map[string]int{
		"A128GCM":       16,
		"A192GCM":       24,
		"A256GCM":       32,
		"A128CBC-HS256": 32,
		"A192CBC-HS384": 48,
		"A256CBC-HS512": 64,
	}

	// Wrapped-key sizes in bytes for the key-wrapping algs.
	wrapKeySize = map[string]int{
		"A128KW":         16,
		"A192KW":         24,
		"A256KW":         32,
		"ECDH-ES+A128KW": 16,
		"ECDH-ES+A192KW": 24,
		"ECDH-ES+A256KW": 32,
	}
)

// ValidateCompactJWE validates a JWE compact-serialization token and returns
// nil only when the protected header passes every gateway policy check and the
// remaining compact parts are structurally decodable. It performs no key
// agreement or decryption and must be called by the decryption entry point
// before any private-key operation.
func ValidateCompactJWE(token string) error {
	parts := splitCompact(token)
	if len(parts) != 5 {
		return fmt.Errorf("%w: expected 5 dot-separated parts, got %d",
			ErrMalformedToken, len(parts))
	}

	rawHeader, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return fmt.Errorf("%w: protected header is not base64url: %v",
			ErrMalformedToken, err)
	}

	header, err := parseProtectedHeader(rawHeader)
	if err != nil {
		return err
	}
	if err := validateHeaderFields(header); err != nil {
		return err
	}

	alg := header["alg"].(string)
	// Only dir and direct ECDH-ES use an empty JWE Encrypted Key.
	if parts[1] == "" && alg != "dir" && alg != "ECDH-ES" {
		return fmt.Errorf("%w: empty encrypted key part for %s",
			ErrMalformedToken, alg)
	}
	if err := decodePart(parts[1], "encrypted key", true); err != nil {
		return err
	}
	for i, name := range map[int]string{
		2: "initialization vector",
		3: "ciphertext",
		4: "authentication tag",
	} {
		if err := decodePart(parts[i], name, false); err != nil {
			return err
		}
	}
	return nil
}

func splitCompact(token string) []string {
	parts := bytes.Split([]byte(token), []byte{'.'})
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = string(p)
	}
	return out
}

func decodePart(value, name string, allowEmpty bool) error {
	if value == "" {
		if !allowEmpty {
			return fmt.Errorf("%w: empty %s part", ErrMalformedToken, name)
		}
		return nil
	}
	if _, err := base64.RawURLEncoding.DecodeString(value); err != nil {
		return fmt.Errorf("%w: %s part is not base64url: %v",
			ErrMalformedToken, name, err)
	}
	return nil
}

// parseProtectedHeader rejects JSON that is not a single object, contains
// duplicate object keys at any depth, or has trailing data.
func parseProtectedHeader(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("%w: header is not valid JSON: %v",
			ErrMalformedHeader, err)
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return nil, fmt.Errorf("%w: header must be a JSON object",
			ErrMalformedHeader)
	}

	seen := map[string]bool{}
	if err := rejectDuplicateObject(dec, seen, ""); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: trailing data after header object",
			ErrMalformedHeader)
	}

	var header map[string]any
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedHeader, err)
	}
	return header, nil
}

// rejectDuplicateObject walks an object whose opening '{' has already been
// consumed and rejects repeated member names at every nesting level.
func rejectDuplicateObject(dec *json.Decoder, seen map[string]bool, path string) error {
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrMalformedHeader, err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("%w: non-string object key", ErrMalformedHeader)
		}
		qualified := path + "." + key
		if seen[qualified] {
			return fmt.Errorf("%w: %s", ErrDuplicateHeader, qualified)
		}
		seen[qualified] = true

		if err := checkValue(dec, seen, qualified); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil { // closing '}'
		return fmt.Errorf("%w: %v", ErrMalformedHeader, err)
	}
	return nil
}

// checkValue consumes one complete JSON value, recursing into objects and
// arrays so duplicate keys are detected at every depth.
func checkValue(dec *json.Decoder, seen map[string]bool, path string) error {
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrMalformedHeader, err)
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil // scalar value
	}
	switch delim {
	case '{':
		return rejectDuplicateObject(dec, seen, path)
	case '[':
		for i := 0; dec.More(); i++ {
			if err := checkValue(dec, seen, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil { // closing ']'
			return fmt.Errorf("%w: %v", ErrMalformedHeader, err)
		}
	}
	return nil
}

func validateHeaderFields(header map[string]any) error {
	if _, present := header["zip"]; present {
		return fmt.Errorf("%w: compression is never accepted", ErrCompression)
	}

	alg, err := requiredString(header, "alg")
	if err != nil {
		return err
	}
	enc, err := requiredString(header, "enc")
	if err != nil {
		return err
	}

	if !allowedAlg[alg] {
		return fmt.Errorf("%w: %q", ErrUnsupportedAlg, alg)
	}
	if !allowedEnc[enc] {
		return fmt.Errorf("%w: %q", ErrUnsupportedEnc, enc)
	}

	if isECDHES(alg) {
		if err := validateEPK(header["epk"]); err != nil {
			return err
		}
	} else if _, present := header["epk"]; present {
		return fmt.Errorf("%w: epk only valid with ECDH-ES algorithms", ErrEPK)
	}

	if want, wraps := wrapKeySize[alg]; wraps {
		if got := cekSize[enc]; got != want {
			return fmt.Errorf("%w: %s wraps a %d-byte key but %s needs %d bytes",
				ErrAlgorithmMismatch, alg, want, enc, got)
		}
	}
	return nil
}

func requiredString(header map[string]any, field string) (string, error) {
	value, present := header[field]
	if !present {
		return "", fmt.Errorf("%w: missing %q field", ErrMalformedHeader, field)
	}
	s, ok := value.(string)
	if !ok || s == "" {
		return "", fmt.Errorf("%w: %q must be a non-empty string",
			ErrMalformedHeader, field)
	}
	return s, nil
}

func isECDHES(alg string) bool {
	return alg == "ECDH-ES" ||
		(len(alg) > len("ECDH-ES+") && alg[:len("ECDH-ES+")] == "ECDH-ES+")
}

// validateEPK requires an EC JWK on P-256 whose coordinates decode to a point
// actually on the curve. The standard library performs full point validation,
// rejecting off-curve points, infinity and malformed coordinates.
func validateEPK(value any) error {
	if value == nil {
		return fmt.Errorf("%w: missing epk for ECDH-ES", ErrEPK)
	}

	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%w: epk is not a JSON object", ErrEPK)
	}
	var jwk struct {
		Kty string `json:"kty"`
		Crv string `json:"crv"`
		X   string `json:"x"`
		Y   string `json:"y"`
		D   string `json:"d"`
	}
	if err := json.Unmarshal(raw, &jwk); err != nil {
		return fmt.Errorf("%w: epk is not a valid JWK: %v", ErrEPK, err)
	}

	if jwk.Kty != "EC" {
		return fmt.Errorf("%w: epk kty is %q, want EC", ErrEPK, jwk.Kty)
	}
	if jwk.Crv != "P-256" {
		return fmt.Errorf("%w: epk crv is %q, want P-256", ErrEPK, jwk.Crv)
	}
	if jwk.D != "" {
		return fmt.Errorf("%w: epk must not contain a private component", ErrEPK)
	}

	x, err := base64.RawURLEncoding.DecodeString(jwk.X)
	if err != nil || len(x) != 32 {
		return fmt.Errorf("%w: epk x must be 32 base64url bytes", ErrEPK)
	}
	y, err := base64.RawURLEncoding.DecodeString(jwk.Y)
	if err != nil || len(y) != 32 {
		return fmt.Errorf("%w: epk y must be 32 base64url bytes", ErrEPK)
	}

	// 0x04 = uncompressed point prefix per SEC 1; NewPublicKey rejects any
	// point not on P-256, including (0,0) and arbitrary off-curve pairs.
	point := make([]byte, 0, 65)
	point = append(point, 0x04)
	point = append(point, x...)
	point = append(point, y...)
	if _, err := ecdh.P256().NewPublicKey(point); err != nil {
		return fmt.Errorf("%w: not a valid P-256 point: %v", ErrEPK, err)
	}
	return nil
}
