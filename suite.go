package netsec

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"fmt"
	"hash"
	"sort"
)

// Algorithm is an IPMI algorithm identifier. Authentication, integrity and
// confidentiality algorithms share the numeric namespace in the IPMI cipher
// suite table; the field in which an Algorithm is used determines its meaning.
type Algorithm uint8

const (
	AlgorithmNone Algorithm = 0
)

// RAKP authentication algorithms.
const (
	AuthNone           Algorithm = 0
	AuthRAKPHMACSHA1   Algorithm = 1
	AuthRAKPHMACMD5    Algorithm = 2
	AuthRAKPHMACSHA256 Algorithm = 3
	AuthRAKPHMACSHA384 Algorithm = 4
	AuthRAKPHMACSHA512 Algorithm = 5
)

// Integrity algorithms used by standard suites.
const (
	IntegrityNone          Algorithm = 0
	IntegrityHMACSHA196    Algorithm = 1
	IntegrityHMACMD5128    Algorithm = 2
	IntegrityMD5128        Algorithm = 3
	IntegrityHMACSHA256128 Algorithm = 4
	IntegrityHMACSHA384192 Algorithm = 5
	IntegrityHMACSHA512256 Algorithm = 6
)

// Confidentiality algorithms used by standard suites.
const (
	ConfidentialityNone      Algorithm = 0
	ConfidentialityAESCBC128 Algorithm = 1
	ConfidentialityXRC4128   Algorithm = 2
	ConfidentialityXRC440    Algorithm = 3
	ConfidentialityAESCBC256 Algorithm = 4
)

// Suite is the authentication/integrity/confidentiality triple associated
// with an IPMI RMCP+ cipher suite number.
type Suite struct {
	Number          uint8
	Auth            Algorithm
	Integrity       Algorithm
	Confidentiality Algorithm
}

// Validate applies the admission policy to the suite itself. A suite using
// authentication algorithm 0 or confidentiality algorithm 0 is always
// rejected. Integrity algorithm 0 is not rejected by this policy because the
// requirement is specifically about the RAKP authentication and
// confidentiality algorithms.
func (s Suite) Validate() error {
	var forbidden []error
	if s.Auth == AuthNone {
		forbidden = append(forbidden, ErrAuthAlgorithmNone)
	}
	if s.Confidentiality == ConfidentialityNone {
		forbidden = append(forbidden, ErrConfidentialityNone)
	}
	if len(forbidden) > 0 {
		return fmt.Errorf("%w: suite %d: %w", ErrSuiteRejected, s.Number, errors.Join(forbidden...))
	}
	if _, err := authHash(s.Auth); err != nil {
		return fmt.Errorf("%w: suite %d: %w", ErrSuiteRejected, s.Number, err)
	}
	return nil
}

// AuthAlgorithm returns the suite's RAKP authentication algorithm.
func (s Suite) AuthAlgorithm() Algorithm { return s.Auth }

// IntegrityAlgorithm returns the suite's integrity algorithm.
func (s Suite) IntegrityAlgorithm() Algorithm { return s.Integrity }

// ConfidentialityAlgorithm returns the suite's confidentiality algorithm.
func (s Suite) ConfidentialityAlgorithm() Algorithm { return s.Confidentiality }

func authHash(algorithm Algorithm) (func() hash.Hash, error) {
	switch algorithm {
	case AuthRAKPHMACSHA1:
		return sha1.New, nil
	case AuthRAKPHMACMD5:
		return md5.New, nil
	case AuthRAKPHMACSHA256:
		return sha256.New, nil
	case AuthRAKPHMACSHA384:
		return sha512.New384, nil
	case AuthRAKPHMACSHA512:
		return sha512.New, nil
	case AuthNone:
		return nil, ErrAuthAlgorithmNone
	default:
		return nil, fmt.Errorf("%w: RAKP authentication algorithm %d", ErrUnsupportedAlgorithm, algorithm)
	}
}

// standardSuites is the subset of the IPMI 2.0 cipher suite table needed to
// validate caller-provided suite numbers. Registration is still separate: a
// standard suite is not admitted until the caller registers its number.
var standardSuites = map[uint8]Suite{
	0:  {Number: 0, Auth: AuthNone, Integrity: IntegrityNone, Confidentiality: ConfidentialityNone},
	1:  {Number: 1, Auth: AuthRAKPHMACSHA1, Integrity: IntegrityHMACSHA196, Confidentiality: ConfidentialityNone},
	2:  {Number: 2, Auth: AuthRAKPHMACSHA1, Integrity: IntegrityHMACSHA196, Confidentiality: ConfidentialityAESCBC128},
	3:  {Number: 3, Auth: AuthRAKPHMACSHA1, Integrity: IntegrityHMACSHA196, Confidentiality: ConfidentialityAESCBC128},
	4:  {Number: 4, Auth: AuthRAKPHMACSHA1, Integrity: IntegrityHMACSHA196, Confidentiality: ConfidentialityXRC4128},
	5:  {Number: 5, Auth: AuthRAKPHMACSHA1, Integrity: IntegrityHMACSHA196, Confidentiality: ConfidentialityXRC440},
	6:  {Number: 6, Auth: AuthRAKPHMACMD5, Integrity: IntegrityHMACMD5128, Confidentiality: ConfidentialityNone},
	7:  {Number: 7, Auth: AuthRAKPHMACMD5, Integrity: IntegrityHMACMD5128, Confidentiality: ConfidentialityAESCBC128},
	8:  {Number: 8, Auth: AuthRAKPHMACMD5, Integrity: IntegrityHMACMD5128, Confidentiality: ConfidentialityXRC4128},
	9:  {Number: 9, Auth: AuthRAKPHMACMD5, Integrity: IntegrityHMACMD5128, Confidentiality: ConfidentialityXRC440},
	10: {Number: 10, Auth: AuthRAKPHMACMD5, Integrity: IntegrityMD5128, Confidentiality: ConfidentialityNone},
	11: {Number: 11, Auth: AuthRAKPHMACMD5, Integrity: IntegrityMD5128, Confidentiality: ConfidentialityAESCBC128},
	12: {Number: 12, Auth: AuthRAKPHMACMD5, Integrity: IntegrityMD5128, Confidentiality: ConfidentialityXRC4128},
	13: {Number: 13, Auth: AuthRAKPHMACMD5, Integrity: IntegrityMD5128, Confidentiality: ConfidentialityXRC440},
	14: {Number: 14, Auth: AuthRAKPHMACSHA256, Integrity: IntegrityHMACSHA256128, Confidentiality: ConfidentialityNone},
	15: {Number: 15, Auth: AuthRAKPHMACSHA256, Integrity: IntegrityHMACSHA256128, Confidentiality: ConfidentialityAESCBC128},
	16: {Number: 16, Auth: AuthRAKPHMACSHA256, Integrity: IntegrityHMACSHA256128, Confidentiality: ConfidentialityXRC4128},
	17: {Number: 17, Auth: AuthRAKPHMACSHA256, Integrity: IntegrityHMACSHA256128, Confidentiality: ConfidentialityAESCBC128},
}

// StandardSuite returns the standard definition for an IPMI cipher suite
// number.
func StandardSuite(number uint8) (Suite, bool) {
	suite, ok := standardSuites[number]
	return suite, ok
}

// StandardSuites returns the known standard suite definitions sorted by
// suite number.
func StandardSuites() []Suite {
	out := make([]Suite, 0, len(standardSuites))
	for _, suite := range standardSuites {
		out = append(out, suite)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out
}
