// Package ocra implements the OCRA (OATH Challenge-Response Algorithm)
// challenge/response computation defined in RFC 6287.
//
// The package is deliberately limited to the building blocks used by this
// service: the HOTP crypto functions (HMAC-SHA1/SHA256/SHA512), the counter
// (C), challenge question (Q) and time (T) data inputs. PIN (P) and session
// (S) inputs are rejected because they are not part of the deployed suite.
package ocra

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"fmt"
	"hash"
	"regexp"
	"strconv"
	"strings"
)

// Suite is a parsed OCRASuite string, e.g.
// "OCRA-1:HOTP-SHA256-8:C-QH64-T1M".
type Suite struct {
	// Raw is the original, canonical OCRASuite string.
	Raw string

	hash   func() hash.Hash
	digits int

	counter bool

	question     bool
	questionHex  bool
	questionSize int

	time     bool
	timeStep int64
}

// MustParse parses suite and panics on error; it is intended for package-level
// constants whose value is fixed and known at compile time.
func MustParse(suite string) Suite {
	s, err := Parse(suite)
	if err != nil {
		panic(err)
	}
	return s
}

var (
	suiteRe     = regexp.MustCompile(`^OCRA-1:HOTP-(SHA1|SHA256|SHA512)-([0-9]{1,2}):(.+)$`)
	questionRe  = regexp.MustCompile(`^Q([HAN])([0-9]{1,3})$`)
	timestampRe = regexp.MustCompile(`^T([0-9]{1,3})([SMH])$`)
)

// Parse validates and parses an OCRASuite string.
func Parse(suite string) (Suite, error) {
	m := suiteRe.FindStringSubmatch(suite)
	if m == nil {
		return Suite{}, fmt.Errorf("ocra: unsupported or malformed OCRASuite %q", suite)
	}

	s := Suite{Raw: suite}
	switch m[1] {
	case "SHA1":
		s.hash = sha1.New
	case "SHA256":
		s.hash = sha256.New
	case "SHA512":
		s.hash = sha512.New
	}

	digits, err := strconv.Atoi(m[2])
	if err != nil {
		return Suite{}, err
	}
	if digits < 4 || digits > 10 {
		return Suite{}, fmt.Errorf("ocra: truncation length %d outside RFC 6287 range 4-10", digits)
	}
	s.digits = digits

	// Data inputs must appear in the canonical C-Q-P-S-T order. Only C, Q and
	// T are supported here, so an explicit rank check keeps parsing simple.
	rank := map[string]int{"C": 0, "Q": 1, "T": 2}
	seen := map[string]bool{}
	lastRank := -1
	for _, field := range strings.Split(m[3], "-") {
		if field == "" {
			return Suite{}, errors.New("ocra: empty data input field")
		}
		switch {
		case field == "C":
			if seen["C"] {
				return Suite{}, errors.New("ocra: duplicate counter field")
			}
			s.counter = true
		case strings.HasPrefix(field, "Q"):
			q := questionRe.FindStringSubmatch(field)
			if q == nil {
				return Suite{}, fmt.Errorf("ocra: invalid question field %q", field)
			}
			if seen["Q"] {
				return Suite{}, errors.New("ocra: duplicate question field")
			}
			size, err := strconv.Atoi(q[2])
			if err != nil || size <= 0 || size > 512 {
				return Suite{}, fmt.Errorf("ocra: invalid question size in %q", field)
			}
			s.question = true
			s.questionHex = q[1] == "H"
			if s.questionHex {
				// QHn counts hex nibbles; the MAC message carries the decoded
				// bytes, so an odd length cannot represent whole bytes.
				if size%2 != 0 {
					return Suite{}, fmt.Errorf("ocra: hex question %q must have an even length", field)
				}
				s.questionSize = size / 2
			} else {
				s.questionSize = size
			}
		case strings.HasPrefix(field, "T"):
			t := timestampRe.FindStringSubmatch(field)
			if t == nil {
				return Suite{}, fmt.Errorf("ocra: invalid timestamp field %q", field)
			}
			if seen["T"] {
				return Suite{}, errors.New("ocra: duplicate timestamp field")
			}
			step, err := strconv.Atoi(t[1])
			if err != nil || step <= 0 {
				return Suite{}, fmt.Errorf("ocra: invalid time step in %q", field)
			}
			unit := int64(1)
			switch t[2] {
			case "S":
				unit = 1
			case "M":
				unit = 60
			case "H":
				unit = 3600
			}
			s.time = true
			s.timeStep = int64(step) * unit
		case field == "P" || strings.HasPrefix(field, "P"):
			return Suite{}, fmt.Errorf("ocra: PIN/password field %q is not supported", field)
		case field == "S" || strings.HasPrefix(field, "S"):
			return Suite{}, fmt.Errorf("ocra: session field %q is not supported", field)
		default:
			return Suite{}, fmt.Errorf("ocra: unknown data input field %q", field)
		}

		r, ok := rank[string(field[0])]
		if !ok {
			return Suite{}, fmt.Errorf("ocra: unknown data input field %q", field)
		}
		if r <= lastRank {
			return Suite{}, fmt.Errorf("ocra: data input fields out of order at %q", field)
		}
		lastRank = r
		seen[string(field[0])] = true
	}

	if !s.counter && !s.question && !s.time {
		return Suite{}, errors.New("ocra: suite must declare at least one data input")
	}
	return s, nil
}

// Digits returns the OTP truncation length declared by the suite.
func (s Suite) Digits() int { return s.digits }

// Counter reports whether the suite includes a counter input.
func (s Suite) Counter() bool { return s.counter }

// Question reports whether the suite includes a challenge question.
func (s Suite) Question() bool { return s.question }

// TimeStep returns the time-step size in seconds, or 0 when no time input is
// declared.
func (s Suite) TimeStep() int64 {
	if !s.time {
		return 0
	}
	return s.timeStep
}

// Input holds the raw values used to evaluate an OCRA computation.
type Input struct {
	// Counter is the big-endian counter value; ignored when the suite has no C.
	Counter uint64
	// Question is the raw challenge bytes: for QHn it is the binary decoded
	// from the hex question; for QA/QN it is the ASCII question.
	Question []byte
	// UnixTime is the Unix timestamp in seconds used to derive the T input.
	UnixTime int64
}

// message builds the RFC 6287 data-input message. Each present field is
// encoded big-endian on a fixed width: C and T on 8 bytes, Q on its declared
// length, in the C-Q-T order fixed by the suite.
func (s Suite) message(in Input) ([]byte, error) {
	if s.question {
		if len(in.Question) != s.questionSize {
			return nil, fmt.Errorf("ocra: question must be %d bytes, got %d", s.questionSize, len(in.Question))
		}
	} else if len(in.Question) != 0 {
		return nil, errors.New("ocra: suite declares no question but one was supplied")
	}
	if !s.time && in.UnixTime != 0 {
		return nil, errors.New("ocra: suite declares no timestamp but one was supplied")
	}

	var msg []byte
	putUint64 := func(v uint64) {
		buf := make([]byte, 8)
		for i := 7; i >= 0; i-- {
			buf[i] = byte(v)
			v >>= 8
		}
		msg = append(msg, buf...)
	}

	if s.counter {
		putUint64(in.Counter)
	}
	if s.question {
		msg = append(msg, in.Question...)
	}
	if s.time {
		if in.UnixTime < 0 {
			return nil, errors.New("ocra: negative Unix time is not supported")
		}
		putUint64(uint64(in.UnixTime / s.timeStep))
	}
	return msg, nil
}

// truncate applies the HOTP dynamic truncation of RFC 4226/RFC 6287 and
// returns the zero-padded decimal OTP with the suite's digit count.
func (s Suite) truncate(mac []byte) string {
	offset := int(mac[len(mac)-1] & 0x0f)
	binary := (uint64(mac[offset]&0x7f) << 24) |
		(uint64(mac[offset+1]) << 16) |
		(uint64(mac[offset+2]) << 8) |
		uint64(mac[offset+3])
	mod := uint64(1)
	for i := 0; i < s.digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", s.digits, binary%mod)
}

// Generate computes the OCRA response for key and in.
func (s Suite) Generate(key []byte, in Input) (string, error) {
	if len(key) == 0 {
		return "", errors.New("ocra: empty shared secret")
	}
	msg, err := s.message(in)
	if err != nil {
		return "", err
	}
	mac := hmac.New(s.hash, key)
	mac.Write(msg)
	return s.truncate(mac.Sum(nil)), nil
}
