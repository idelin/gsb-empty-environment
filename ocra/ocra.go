package ocra

import (
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
)

// SuiteC_QH64_T1M is the deployed transaction confirmation suite:
//
//	OCRA-1:HOTP-SHA256-8:C-QH64-T1M
//
// HMAC-SHA256 truncated to 8 digits, binding an increasing counter (C), a
// 64-hex-character (32-byte) challenge question (QH64) and a 1-minute time
// step (T1M).
const SuiteC_QH64_T1M = "OCRA-1:HOTP-SHA256-8:C-QH64-T1M"

// DefaultSuite is the parsed form of SuiteC_QH64_T1M.
var DefaultSuite = MustParse(SuiteC_QH64_T1M)

// DecodeHexQuestion decodes a hex-encoded question (QHn). It enforces
// lowercase/uppercase hex and the exact byte length declared by the suite.
func (s Suite) DecodeHexQuestion(question string) ([]byte, error) {
	if !s.question || !s.questionHex {
		return nil, errors.New("ocra: suite does not use a hexadecimal question")
	}
	raw, err := hex.DecodeString(question)
	if err != nil {
		return nil, fmt.Errorf("ocra: question is not valid hex: %w", err)
	}
	if len(raw) != s.questionSize {
		return nil, fmt.Errorf("ocra: question must decode to %d bytes, got %d", s.questionSize, len(raw))
	}
	return raw, nil
}

// Validate recomputes the OCRA response and compares it against code using a
// constant-time comparison. A malformed code returns an error without ever
// comparing against the expected value.
func (s Suite) Validate(key []byte, in Input, code string) (bool, error) {
	if len(code) != s.digits {
		return false, fmt.Errorf("ocra: response must be %d decimal digits, got %d", s.digits, len(code))
	}
	for i := 0; i < len(code); i++ {
		if code[i] < '0' || code[i] > '9' {
			return false, fmt.Errorf("ocra: response %q contains a non-decimal character", code)
		}
	}
	expected, err := s.Generate(key, in)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1, nil
}
