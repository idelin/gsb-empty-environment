package netsec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const maxTokenSize = 8 << 20

// Signature is one signature from a JWS. Only Protected contains parameters
// that are covered by the signature. Unprotected is preserved for inspection
// and conflict detection but never drives key or algorithm selection.
type Signature struct {
	Protected        Header
	ProtectedEncoded string
	// Header is an alias for Unprotected, matching the common JWS field name.
	Header           Header
	Unprotected      Header
	Signature        []byte
	SignatureEncoded string
}

// Token is a parsed JWS. Parsing does not authenticate any signature; call
// Verify for that.
type Token struct {
	Payload        []byte
	PayloadEncoded string
	Signatures     []Signature
	Detached       bool
}

// JSONWebSignature is an alias for Token.
type JSONWebSignature = Token

// Verify verifies the parsed token and returns its authenticated payload.
func (t *Token) Verify(keyOrResolver any) ([]byte, error) {
	return t.VerifyContext(context.Background(), keyOrResolver)
}

// VerifyContext verifies the parsed token with a context for key resolution.
func (t *Token) VerifyContext(ctx context.Context, keyOrResolver any) ([]byte, error) {
	return (&Verifier{Keys: keyOrResolver}).VerifyTokenContext(ctx, t)
}

// VerifyResult verifies the parsed token and returns details about the
// accepted signature.
func (t *Token) VerifyResult(keyOrResolver any) (*Result, error) {
	return (&Verifier{Keys: keyOrResolver}).VerifyTokenResult(t)
}

// DetachedVerify verifies a parsed token whose payload is supplied separately.
func (t *Token) DetachedVerify(payload []byte, keyOrResolver any) ([]byte, error) {
	if t == nil {
		return nil, fmt.Errorf("%w: nil token", ErrMalformedToken)
	}
	if !t.Detached && t.PayloadEncoded != "" {
		return nil, fmt.Errorf("%w: token already contains a payload", ErrMalformedToken)
	}
	copy := *t
	copy.Detached = false
	copy.Payload = append([]byte(nil), payload...)
	copy.PayloadEncoded = encodeBase64URL(payload)
	return (&Verifier{Keys: keyOrResolver}).VerifyToken(&copy)
}

// Result describes the signature that successfully verified.
type Result struct {
	Payload           []byte
	PayloadEncoded    string
	Signature         []byte
	SignatureIndex    int
	Protected         Header
	ProtectedHeader   Header
	ProtectedEncoded  string
	Unprotected       Header
	UnprotectedHeader Header
	Algorithm         string
	KeyID             string
}

// Parse parses either the compact or JSON JWS serialization. It does not
// verify the signature and does not trust the unprotected header for any
// security decision.
func Parse(input any) (*Token, error) {
	data, err := tokenBytes(input)
	if err != nil {
		return nil, err
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty input", ErrMalformedToken)
	}
	if data[0] == '{' {
		return parseJSONToken(data)
	}
	return parseCompactToken(data)
}

// ParseSigned is an alias for Parse. Additional arguments are accepted for
// interoperability and ignored.
func ParseSigned(input any, _ ...any) (*Token, error) {
	return Parse(input)
}

// ParseCompact parses a compact JWS.
func ParseCompact(input any) (*Token, error) {
	data, err := tokenBytes(input)
	if err != nil {
		return nil, err
	}
	return parseCompactToken(bytes.TrimSpace(data))
}

// ParseJSON parses a JWS JSON serialization.
func ParseJSON(input any) (*Token, error) {
	data, err := tokenBytes(input)
	if err != nil {
		return nil, err
	}
	return parseJSONToken(bytes.TrimSpace(data))
}

func tokenBytes(input any) ([]byte, error) {
	switch value := input.(type) {
	case string:
		if len(value) > maxTokenSize {
			return nil, fmt.Errorf("%w: token exceeds size limit", ErrMalformedToken)
		}
		return []byte(value), nil
	case []byte:
		if len(value) > maxTokenSize {
			return nil, fmt.Errorf("%w: token exceeds size limit", ErrMalformedToken)
		}
		return value, nil
	case json.RawMessage:
		if len(value) > maxTokenSize {
			return nil, fmt.Errorf("%w: token exceeds size limit", ErrMalformedToken)
		}
		return []byte(value), nil
	case io.Reader:
		data, err := io.ReadAll(io.LimitReader(value, maxTokenSize+1))
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrMalformedToken, err)
		}
		if len(data) > maxTokenSize {
			return nil, fmt.Errorf("%w: token exceeds size limit", ErrMalformedToken)
		}
		return data, nil
	default:
		return nil, fmt.Errorf("%w: unsupported input type %T", ErrMalformedToken, input)
	}
}

func parseCompactToken(data []byte) (*Token, error) {
	text := string(bytes.TrimSpace(data))
	if text == "" {
		return nil, fmt.Errorf("%w: empty input", ErrMalformedToken)
	}
	if len(text) > maxTokenSize {
		return nil, fmt.Errorf("%w: token exceeds size limit", ErrMalformedToken)
	}

	parts := strings.Split(text, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: compact JWS must have three segments", ErrMalformedToken)
	}

	protected, err := parseHeaderSegment(parts[0])
	if err != nil {
		return nil, err
	}
	payload, err := decodeBase64URL(parts[1])
	if err != nil {
		return nil, err
	}
	signature, err := decodeBase64URL(parts[2])
	if err != nil {
		return nil, err
	}

	return &Token{
		Payload:        payload,
		PayloadEncoded: parts[1],
		Signatures: []Signature{{
			Protected:        protected,
			ProtectedEncoded: parts[0],
			Header:           emptyHeader(),
			Unprotected:      emptyHeader(),
			Signature:        signature,
			SignatureEncoded: parts[2],
		}},
	}, nil
}

func parseJSONToken(data []byte) (*Token, error) {
	obj, err := decodeJSONObject(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedToken, err)
	}

	token := &Token{}
	payloadRaw, hasPayload := obj["payload"]
	if hasPayload {
		if isJSONNull(payloadRaw) {
			return nil, fmt.Errorf("%w: payload must be a string", ErrMalformedToken)
		}
		var payloadEncoded string
		if err := json.Unmarshal(payloadRaw, &payloadEncoded); err != nil {
			return nil, fmt.Errorf("%w: payload must be a string", ErrMalformedToken)
		}
		payload, err := decodeBase64URL(payloadEncoded)
		if err != nil {
			return nil, err
		}
		token.Payload = payload
		token.PayloadEncoded = payloadEncoded
	} else {
		token.Detached = true
	}

	_, hasSignatures := obj["signatures"]
	_, hasSignature := obj["signature"]
	if hasSignatures && hasSignature {
		return nil, fmt.Errorf("%w: both signatures and signature are present", ErrMalformedToken)
	}

	switch {
	case hasSignatures:
		var items []json.RawMessage
		if err := json.Unmarshal(obj["signatures"], &items); err != nil {
			return nil, fmt.Errorf("%w: signatures must be an array", ErrMalformedToken)
		}
		if len(items) == 0 {
			return nil, fmt.Errorf("%w: signatures is empty", ErrMalformedToken)
		}
		for index, item := range items {
			itemObject, err := decodeJSONObject(item)
			if err != nil {
				return nil, fmt.Errorf("%w: signature %d: %v", ErrMalformedToken, index, err)
			}
			signature, err := parseSignatureObject(itemObject)
			if err != nil {
				return nil, fmt.Errorf("%w: signature %d: %v", ErrMalformedToken, index, err)
			}
			token.Signatures = append(token.Signatures, signature)
		}
	case hasSignature:
		signature, err := parseSignatureObject(obj)
		if err != nil {
			return nil, err
		}
		token.Signatures = []Signature{signature}
	default:
		return nil, fmt.Errorf("%w: no signatures", ErrMalformedToken)
	}

	return token, nil
}

func parseSignatureObject(obj map[string]json.RawMessage) (Signature, error) {
	signature := Signature{
		Protected:   emptyHeader(),
		Header:      emptyHeader(),
		Unprotected: emptyHeader(),
	}

	if raw, ok := obj["protected"]; ok {
		if isJSONNull(raw) {
			return Signature{}, fmt.Errorf("%w: protected must be a string", ErrMalformedToken)
		}
		var encoded string
		if err := json.Unmarshal(raw, &encoded); err != nil {
			return Signature{}, fmt.Errorf("%w: protected must be a string", ErrMalformedToken)
		}
		protected, err := parseHeaderSegment(encoded)
		if err != nil {
			return Signature{}, err
		}
		signature.Protected = protected
		signature.ProtectedEncoded = encoded
	}

	if raw, ok := obj["header"]; ok && !isJSONNull(raw) {
		unprotectedObject, err := decodeJSONObject(raw)
		if err != nil {
			return Signature{}, fmt.Errorf("%w: unprotected header: %v", ErrMalformedToken, err)
		}
		unprotected, err := parseHeaderObject(unprotectedObject)
		if err != nil {
			return Signature{}, err
		}
		signature.Unprotected = unprotected
		signature.Header = unprotected
	}

	raw, ok := obj["signature"]
	if !ok {
		return Signature{}, fmt.Errorf("%w: signature is missing", ErrMalformedToken)
	}
	if isJSONNull(raw) {
		return Signature{}, fmt.Errorf("%w: signature must be a string", ErrMalformedToken)
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return Signature{}, fmt.Errorf("%w: signature must be a string", ErrMalformedToken)
	}
	decoded, err := decodeBase64URL(encoded)
	if err != nil {
		return Signature{}, err
	}
	signature.Signature = decoded
	signature.SignatureEncoded = encoded

	return signature, nil
}
