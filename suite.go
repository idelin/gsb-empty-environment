package netsec

import "errors"

// Algorithm identifiers that must never be admitted: algorithm 0 means
// "none" for both authentication and confidentiality in IPMI 2.0.
const (
	AuthAlgNone uint8 = 0
	ConfAlgNone uint8 = 0
)

var (
	// ErrSuiteNotRegistered is returned for cipher suite IDs the caller
	// never registered.
	ErrSuiteNotRegistered = errors.New("netsec: cipher suite not registered")
	// ErrSuiteInsecure is returned for suites whose authentication or
	// confidentiality algorithm is 0 (none).
	ErrSuiteInsecure = errors.New("netsec: cipher suite uses none authentication or confidentiality algorithm")
)

// Suite is an IPMI 2.0 cipher suite: an ID plus the RAKP authentication,
// integrity and confidentiality algorithm triple.
type Suite struct {
	ID           uint8
	AuthAlg      uint8
	IntegrityAlg uint8
	ConfAlg      uint8
}

// Registry holds the cipher suites the caller explicitly allows for
// RMCP+ session establishment.
type Registry struct {
	suites map[uint8]Suite
}

func NewRegistry() *Registry {
	return &Registry{suites: make(map[uint8]Suite)}
}

// Register admits a suite. Suites with authentication algorithm 0 (none)
// or confidentiality algorithm 0 are refused outright.
func (r *Registry) Register(s Suite) error {
	if s.AuthAlg == AuthAlgNone || s.ConfAlg == ConfAlgNone {
		return ErrSuiteInsecure
	}
	r.suites[s.ID] = s
	return nil
}

// Admit returns the registered suite for id, or an error if the suite is
// unknown or insecure. Callers invoke this before accepting RAKP1.
func (r *Registry) Admit(id uint8) (Suite, error) {
	s, ok := r.suites[id]
	if !ok {
		return Suite{}, ErrSuiteNotRegistered
	}
	if s.AuthAlg == AuthAlgNone || s.ConfAlg == ConfAlgNone {
		return Suite{}, ErrSuiteInsecure
	}
	return s, nil
}
