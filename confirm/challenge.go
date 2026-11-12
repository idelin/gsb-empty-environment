// Package confirm implements the server-side verification of transaction
// signatures produced with the RFC 6287 OCRA-1:HOTP-SHA256-8:C-QH64-T1M
// suite.
//
// A challenge is the SHA-256 digest of a canonical, unambiguous encoding of
// the transfer's amount, currency, payee account and a server nonce. Because
// every transaction field is covered by the digest, an answer signed for one
// transfer cannot be replayed against another amount or payee.
package confirm

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
)

var (
	amountRe   = regexp.MustCompile(`^(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)
	currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)
	nonceRe    = regexp.MustCompile(`^[0-9a-fA-F]{16,128}$`)
)

// Transaction is the business data that a token must sign.
type Transaction struct {
	// Amount is the canonical decimal amount in Currency units, e.g.
	// "1250.00". No sign, exponent or thousands separators are accepted.
	Amount string
	// Currency is the ISO 4217 alphabetic code, e.g. "CNY".
	Currency string
	// Payee is the beneficiary account identifier.
	Payee string
	// Nonce is a fresh, server-generated hex random value (at least 8 bytes).
	Nonce string
}

// Validate checks the transaction fields before they enter the challenge.
func (t Transaction) Validate() error {
	if !amountRe.MatchString(t.Amount) {
		return fmt.Errorf("confirm: invalid canonical amount %q", t.Amount)
	}
	if !currencyRe.MatchString(t.Currency) {
		return fmt.Errorf("confirm: currency must be a 3-letter ISO 4217 code, got %q", t.Currency)
	}
	if t.Payee == "" {
		return errors.New("confirm: payee account is required")
	}
	for _, r := range t.Payee {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("confirm: payee account must not contain control characters")
		}
	}
	if len(t.Payee) > 128 {
		return errors.New("confirm: payee account exceeds 128 characters")
	}
	if !nonceRe.MatchString(t.Nonce) {
		return errors.New("confirm: nonce must be 16-128 hexadecimal characters")
	}
	if len(t.Nonce)%2 != 0 {
		return errors.New("confirm: nonce must contain a whole number of bytes")
	}
	return nil
}

// challengeTag domain-separates this digest from any other SHA-256 usage.
const challengeTag = "OCRA-TX-CONFIRM-v1"

// encodeField appends a 4-byte big-endian length prefix and the field bytes,
// which removes ambiguity if a field value itself contains separators.
func encodeField(dst []byte, value string) []byte {
	dst = append(dst, byte(len(value)>>24), byte(len(value)>>16), byte(len(value)>>8), byte(len(value)))
	return append(dst, value...)
}

// Challenge returns the 64-character lowercase hex SHA-256 digest that is
// presented to the token as the QH64 question.
func Challenge(tx Transaction) (string, error) {
	if err := tx.Validate(); err != nil {
		return "", err
	}
	var msg []byte
	msg = encodeField(msg, challengeTag)
	msg = encodeField(msg, tx.Amount)
	msg = encodeField(msg, tx.Currency)
	msg = encodeField(msg, tx.Payee)

	// Normalize the nonce to lowercase so two encodings of the same random
	// bytes never produce two challenges.
	nonce := make([]byte, len(tx.Nonce))
	hex.Encode(nonce, mustDecodeHex(tx.Nonce))
	msg = encodeField(msg, string(nonce))

	sum := sha256.Sum256(msg)
	return hex.EncodeToString(sum[:]), nil
}

// NewNonce returns 16 cryptographically random bytes encoded as 32 hex
// characters, suitable for Transaction.Nonce.
func NewNonce() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("confirm: generate nonce: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func mustDecodeHex(s string) []byte {
	raw, err := hex.DecodeString(s)
	if err != nil {
		// Unreachable: Validate already accepted the nonce as hex.
		panic(err)
	}
	return raw
}
