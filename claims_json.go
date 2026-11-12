package netsec

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type commonClaimsJSON struct {
	JTI           string          `json:"jti"`
	IssuedAt      int64           `json:"iat"`
	Expires       int64           `json:"exp"`
	Issuer        string          `json:"iss"`
	IssuerAlt     string          `json:"issuer"`
	Subject       string          `json:"sub"`
	SubjectAlt    string          `json:"subject"`
	Audience      string          `json:"aud"`
	Name          string          `json:"name"`
	Type          string          `json:"type"`
	NATS          json.RawMessage `json:"nats"`
	SigningKeys   json.RawMessage `json:"signing_keys"`
	Revocations   json.RawMessage `json:"revocations"`
	IssuerAccount string          `json:"issuer_account"`
}

func decodeCommonClaims(data []byte) (Claims, commonClaimsJSON, error) {
	var raw commonClaimsJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return Claims{}, raw, fmt.Errorf("%w: invalid claims JSON: %v", ErrMalformedToken, err)
	}

	issuer := raw.Issuer
	if raw.IssuerAlt != "" {
		if issuer != "" && issuer != raw.IssuerAlt {
			return Claims{}, raw, fmt.Errorf("%w: conflicting iss and issuer", ErrMalformedToken)
		}
		issuer = raw.IssuerAlt
	}
	subject := raw.Subject
	if raw.SubjectAlt != "" {
		if subject != "" && subject != raw.SubjectAlt {
			return Claims{}, raw, fmt.Errorf("%w: conflicting sub and subject", ErrMalformedToken)
		}
		subject = raw.SubjectAlt
	}

	return Claims{
		JTI:      raw.JTI,
		IssuedAt: raw.IssuedAt,
		Expires:  raw.Expires,
		Issuer:   issuer,
		Subject:  subject,
		Audience: raw.Audience,
		Name:     raw.Name,
		Type:     raw.Type,
	}, raw, nil
}

type accountNATSJSON struct {
	SigningKeys json.RawMessage `json:"signing_keys"`
	Revocations json.RawMessage `json:"revocations"`
}

// UnmarshalJSON accepts both the NATS nested nats section and the older
// top-level signing_keys/revocations fields.
func (claims *AccountClaims) UnmarshalJSON(data []byte) error {
	common, raw, err := decodeCommonClaims(data)
	if err != nil {
		return err
	}

	var nats accountNATSJSON
	if len(raw.NATS) > 0 && string(raw.NATS) != "null" {
		if err := json.Unmarshal(raw.NATS, &nats); err != nil {
			return fmt.Errorf("%w: invalid nats claims: %v", ErrMalformedToken, err)
		}
	}

	keys, err := decodeStringList(raw.SigningKeys, nats.SigningKeys)
	if err != nil {
		return err
	}
	revocations, err := decodeRevocations(raw.Revocations, nats.Revocations)
	if err != nil {
		return err
	}

	claims.Claims = common
	claims.NATS.SigningKeys = keys
	claims.NATS.Revocations = revocations
	return nil
}

type userNATSJSON struct {
	IssuerAccount string `json:"issuer_account"`
}

// UnmarshalJSON accepts issuer_account both in the NATS nested nats section
// and at the claims top level.
func (claims *UserClaims) UnmarshalJSON(data []byte) error {
	common, raw, err := decodeCommonClaims(data)
	if err != nil {
		return err
	}

	var nats userNATSJSON
	if len(raw.NATS) > 0 && string(raw.NATS) != "null" {
		if err := json.Unmarshal(raw.NATS, &nats); err != nil {
			return fmt.Errorf("%w: invalid nats claims: %v", ErrMalformedToken, err)
		}
	}

	issuerAccount := raw.IssuerAccount
	if nats.IssuerAccount != "" {
		if issuerAccount != "" && issuerAccount != nats.IssuerAccount {
			return fmt.Errorf("%w: conflicting issuer_account fields", ErrMalformedToken)
		}
		issuerAccount = nats.IssuerAccount
	}

	claims.Claims = common
	claims.NATS.IssuerAccount = issuerAccount
	return nil
}

type operatorNATSJSON struct {
	SigningKeys json.RawMessage `json:"signing_keys"`
}

// UnmarshalJSON accepts operator signing_keys in the nested nats section and
// at the claims top level.
func (claims *OperatorClaims) UnmarshalJSON(data []byte) error {
	common, raw, err := decodeCommonClaims(data)
	if err != nil {
		return err
	}

	var nats operatorNATSJSON
	if len(raw.NATS) > 0 && string(raw.NATS) != "null" {
		if err := json.Unmarshal(raw.NATS, &nats); err != nil {
			return fmt.Errorf("%w: invalid nats claims: %v", ErrMalformedToken, err)
		}
	}

	keys, err := decodeStringList(raw.SigningKeys, nats.SigningKeys)
	if err != nil {
		return err
	}
	claims.Claims = common
	claims.NATS.SigningKeys = keys
	return nil
}

type keyReference struct {
	Key       string `json:"key"`
	PublicKey string `json:"public_key"`
}

func decodeStringList(fields ...json.RawMessage) ([]string, error) {
	var result []string
	for _, field := range fields {
		if len(field) == 0 || bytes.Equal(field, []byte("null")) {
			continue
		}

		var values []json.RawMessage
		if err := json.Unmarshal(field, &values); err != nil {
			var single string
			if singleErr := json.Unmarshal(field, &single); singleErr == nil {
				result = appendUnique(result, single)
				continue
			}
			return nil, fmt.Errorf("%w: signing_keys must be an array", ErrMalformedToken)
		}

		for _, value := range values {
			var id string
			if err := json.Unmarshal(value, &id); err == nil {
				result = appendUnique(result, id)
				continue
			}

			var reference keyReference
			if err := json.Unmarshal(value, &reference); err == nil {
				id := reference.Key
				if id == "" {
					id = reference.PublicKey
				}
				if id != "" {
					result = appendUnique(result, id)
					continue
				}
			}
			return nil, fmt.Errorf("%w: invalid signing key entry", ErrMalformedToken)
		}
	}
	return result, nil
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func decodeRevocations(fields ...json.RawMessage) (map[string]int64, error) {
	result := make(map[string]int64)
	for _, field := range fields {
		if len(field) == 0 || bytes.Equal(field, []byte("null")) {
			continue
		}
		values := make(map[string]int64)
		if err := json.Unmarshal(field, &values); err != nil {
			return nil, fmt.Errorf("%w: revocations must map user keys to unix times", ErrMalformedToken)
		}
		for key, revokedAt := range values {
			if existing, ok := result[key]; !ok || revokedAt > existing {
				result[key] = revokedAt
			}
		}
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}
