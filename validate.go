package netsec

import (
	"crypto/subtle"
	"fmt"
	"time"
)

const (
	// DefaultClockSkew is the tolerance applied to future OCSP timestamps.
	DefaultClockSkew = 5 * time.Minute
	// DefaultMaxResponseAge bounds a response that omits nextUpdate.
	DefaultMaxResponseAge = 24 * time.Hour
)

// NoncesEqual reports whether the requested and response nonces match.
// An absent request nonce does not impose a requirement on the response.
func NoncesEqual(requestNonce, responseNonce []byte) bool {
	if len(requestNonce) == 0 {
		return true
	}
	if len(responseNonce) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare(requestNonce, responseNonce) == 1
}

// ValidateNonce compares a nonce requested by the TLS client with the nonce
// extracted from a stapled OCSP response.
func ValidateNonce(requestNonce, responseNonce []byte) error {
	if len(requestNonce) == 0 {
		return nil
	}
	if len(responseNonce) == 0 {
		return ErrNonceMissing
	}
	if !NoncesEqual(requestNonce, responseNonce) {
		return ErrNonceMismatch
	}
	return nil
}

// ExtractOCSPNonce returns the nonce from a basic OCSP response. It returns
// ErrNonceMissing when the response has no nonce extension.
func ExtractOCSPNonce(responseDER []byte) ([]byte, error) {
	parsed, err := parseOCSPResponse(responseDER)
	if err != nil {
		return nil, err
	}
	if !parsed.info.NoncePresent || len(parsed.info.Nonce) == 0 {
		return nil, ErrNonceMissing
	}
	return append([]byte(nil), parsed.info.Nonce...), nil
}

// ExtractNonce is a short alias for ExtractOCSPNonce.
func ExtractNonce(responseDER []byte) ([]byte, error) {
	return ExtractOCSPNonce(responseDER)
}

// ValidateOCSPNonce checks the nonce binding of a stapled response. It does
// not check response freshness.
func ValidateOCSPNonce(responseDER, requestNonce []byte) error {
	parsed, err := parseOCSPResponse(responseDER)
	if err != nil {
		return err
	}
	if len(requestNonce) == 0 {
		return nil
	}
	if !parsed.info.NoncePresent || len(parsed.info.Nonce) == 0 {
		return ErrNonceMissing
	}
	return ValidateNonce(requestNonce, parsed.info.Nonce)
}

// ValidateResponseNonce is an alias for ValidateOCSPNonce.
func ValidateResponseNonce(responseDER, requestNonce []byte) error {
	return ValidateOCSPNonce(responseDER, requestNonce)
}

// ValidateOCSPResponseTime checks that producedAt is not in the future and
// that every SingleResponse is inside its validity window.
func ValidateOCSPResponseTime(responseDER []byte, now ...time.Time) error {
	parsed, err := parseOCSPResponse(responseDER)
	if err != nil {
		return err
	}
	return validateResponseTime(&parsed.info, validationTime(now))
}

// ValidateStapledResponse decides whether a stapled OCSP response may be used.
// When requestNonce is non-empty, the response must echo the exact nonce.
// The response must also have a valid production and validity time. If now is
// omitted, the current time is used.
//
// This function does not verify the OCSP signature or the certificate status;
// the TLS stack must perform those checks separately.
func ValidateStapledResponse(responseDER, requestNonce []byte, now ...time.Time) error {
	parsed, err := parseOCSPResponse(responseDER)
	if err != nil {
		return err
	}
	if err := validateResponseNonce(&parsed.info, requestNonce); err != nil {
		return err
	}
	return validateResponseTime(&parsed.info, validationTime(now))
}

// ValidateStapledOCSP is an alias for ValidateStapledResponse.
func ValidateStapledOCSP(responseDER, requestNonce []byte, now ...time.Time) error {
	return ValidateStapledResponse(responseDER, requestNonce, now...)
}

// ValidateOCSPResponse is an alias for ValidateStapledResponse.
func ValidateOCSPResponse(responseDER, requestNonce []byte, now ...time.Time) error {
	return ValidateStapledResponse(responseDER, requestNonce, now...)
}

func validationTime(now []time.Time) time.Time {
	if len(now) > 0 {
		return now[0]
	}
	return time.Now()
}

func validateResponseNonce(info *ResponseInfo, requestNonce []byte) error {
	if len(requestNonce) == 0 {
		return nil
	}
	if !info.NoncePresent || len(info.Nonce) == 0 {
		return ErrNonceMissing
	}
	return ValidateNonce(requestNonce, info.Nonce)
}

func validateResponseTime(info *ResponseInfo, now time.Time) error {
	if info.ProducedAt.After(now.Add(DefaultClockSkew)) {
		return fmt.Errorf("%w: producedAt is in the future", ErrResponseNotYetValid)
	}
	for _, response := range info.Responses {
		if response.ThisUpdate.After(now.Add(DefaultClockSkew)) {
			return fmt.Errorf("%w: thisUpdate is in the future", ErrResponseNotYetValid)
		}
		if response.NextUpdate.IsZero() {
			if now.After(response.ThisUpdate.Add(DefaultMaxResponseAge)) {
				return fmt.Errorf("%w: nextUpdate is absent and thisUpdate is too old", ErrResponseExpired)
			}
			continue
		}
		if now.After(response.NextUpdate) {
			return fmt.Errorf("%w: nextUpdate has passed", ErrResponseExpired)
		}
	}
	return nil
}
