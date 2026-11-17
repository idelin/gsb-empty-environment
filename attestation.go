package netsec

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// ParseStatement parses either a raw in-toto statement or a DSSE envelope
// whose payload is an in-toto statement.
//
// Signature verification is intentionally not performed here: the caller must
// authenticate the DSSE envelope (or statement) with a trusted key. This
// package binds the authenticated statement to the exact manifest being
// deployed.
func ParseStatement(data []byte) (*Statement, error) {
	var envelope struct {
		PayloadType string  `json:"payloadType"`
		Payload     *string `json:"payload"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAttestation, err)
	}

	statementBytes := data
	if envelope.Payload != nil {
		payloadType := strings.ToLower(strings.TrimSpace(strings.SplitN(envelope.PayloadType, ";", 2)[0]))
		if payloadType != "application/vnd.in-toto+json" {
			return nil, fmt.Errorf("%w: unsupported DSSE payload type %q", ErrInvalidAttestation, envelope.PayloadType)
		}
		decoded, err := decodeBase64(*envelope.Payload)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid DSSE payload: %v", ErrInvalidAttestation, err)
		}
		statementBytes = decoded
	}

	var statement Statement
	if err := json.Unmarshal(statementBytes, &statement); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAttestation, err)
	}
	if !strings.HasPrefix(strings.TrimSpace(statement.Type), "https://in-toto.io/Statement/") {
		return nil, fmt.Errorf("%w: unexpected statement type %q", ErrInvalidAttestation, statement.Type)
	}
	return &statement, nil
}

// ParseAttestation is an alias for ParseStatement.
func ParseAttestation(data []byte) (*Statement, error) {
	return ParseStatement(data)
}

func decodeBase64(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var lastErr error
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(value)
		if err == nil {
			return decoded, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func subjectSHA256(subject Subject) (string, bool) {
	for algorithm, value := range subject.Digest {
		if !strings.EqualFold(strings.TrimSpace(algorithm), "sha256") {
			continue
		}
		digest, err := parseSHA256Digest(value)
		if err == nil {
			return digest, true
		}
	}
	return "", false
}

func findManifestSubject(statement *Statement, manifestDigest string) (Subject, error) {
	seen := make([]string, 0, len(statement.Subject))
	for _, subject := range statement.Subject {
		digest, ok := subjectSHA256(subject)
		if !ok {
			continue
		}
		if sameDigest(digest, manifestDigest) {
			return subject, nil
		}
		seen = append(seen, digest)
	}
	return Subject{}, fmt.Errorf(
		"%w: subjects %v do not contain deployed manifest %s",
		ErrSubjectMissing,
		seen,
		manifestDigest,
	)
}
