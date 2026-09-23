package negotiate

import (
	"errors"
	"fmt"
)

// ErrStaleBinding is returned when a retry or downgrade request presents a
// summary that does not match the session's current state.
var ErrStaleBinding = errors.New("negotiate: request not bound to current session summary")

// Session is an established negotiation context. Its certificate's summary
// binds every later decode, retry and downgrade to the exact parameters
// that produced it; results from older parameters cannot be replayed.
type Session struct {
	A, B   *Profile
	Cert   Certificate
	Budget int
}

// Establish negotiates a and b and, on success, opens a session.
func Establish(a, b *Profile, budget int) (*Session, Result, error) {
	res := Negotiate(a, b, budget)
	if res.Status != StatusOK {
		return nil, res, fmt.Errorf("negotiate: cannot establish session: %s", res.Status)
	}
	s := &Session{
		A:      a.Clone(),
		B:      b.Clone(),
		Budget: budget,
	}
	s.Cert = NewCertificate(s.A, s.B, res.Selection)
	return s, res, nil
}

// Summary returns the summary that binds the session's current state.
func (s *Session) Summary() [32]byte { return s.Cert.Summary }

// checkBinding enforces that a request is bound to the current summary.
func (s *Session) checkBinding(bound [32]byte) error {
	if bound != s.Cert.Summary {
		return ErrStaleBinding
	}
	return nil
}

// adopt re-negotiates the given profiles and, on success, replaces the
// session state. Every feature required by either original side must remain
// selected, so a downgrade or renegotiation can never silently drop a
// required capability.
func (s *Session) adopt(a2, b2 *Profile) (Certificate, Result, error) {
	res := Negotiate(a2, b2, s.Budget)
	if res.Status != StatusOK {
		return Certificate{}, res, fmt.Errorf("negotiate: renegotiation failed: %s", res.Status)
	}
	for _, p := range [2]*Profile{s.A, s.B} {
		for _, f := range p.Features {
			if f.Required {
				if _, ok := res.Selection[f.Name]; !ok {
					return Certificate{}, res, fmt.Errorf(
						"negotiate: renegotiation would silently drop required feature %q", f.Name)
				}
			}
		}
	}
	s.A, s.B = a2, b2
	s.Cert = NewCertificate(a2, b2, res.Selection)
	return s.Cert, res, nil
}

// Retry re-runs the negotiation with the session's current parameters. The
// request must be bound to the current summary; the result is the same
// certificate, confirming the session state is reproducible.
func (s *Session) Retry(bound [32]byte) (Certificate, error) {
	if err := s.checkBinding(bound); err != nil {
		return Certificate{}, err
	}
	res := Negotiate(s.A, s.B, s.Budget)
	if res.Status != StatusOK {
		return Certificate{}, fmt.Errorf("negotiate: retry failed: %s", res.Status)
	}
	cert := NewCertificate(s.A, s.B, res.Selection)
	if cert.Summary != s.Cert.Summary {
		return Certificate{}, errors.New("negotiate: retry produced a divergent result")
	}
	return cert, nil
}

// Downgrade re-negotiates with the named feature capped at maxVersion on
// both sides. The request must be bound to the current session summary.
// Required features are never dropped: if the cap makes a required
// capability unsatisfiable the downgrade fails and the session is
// unchanged. On success the session moves to a new summary and certificates
// issued for the old one can no longer be used for decoding.
func (s *Session) Downgrade(feature string, maxVersion int, bound [32]byte) (Certificate, Result, error) {
	if err := s.checkBinding(bound); err != nil {
		return Certificate{}, Result{}, err
	}
	a2, b2 := s.A.Clone(), s.B.Clone()
	for _, p := range [2]*Profile{a2, b2} {
		if f := p.feature(feature); f != nil && f.Versions.Max > maxVersion {
			f.Versions.Max = maxVersion
		}
	}
	return s.adopt(a2, b2)
}

// Renegotiate replaces both profiles and re-negotiates. The request must be
// bound to the current summary. The new certificate carries a new summary
// whenever the parameters differ, so old results cannot decode the new
// parameters.
func (s *Session) Renegotiate(a2, b2 *Profile, bound [32]byte) (Certificate, Result, error) {
	if err := s.checkBinding(bound); err != nil {
		return Certificate{}, Result{}, err
	}
	if err := a2.Validate(); err != nil {
		return Certificate{}, Result{}, err
	}
	if err := b2.Validate(); err != nil {
		return Certificate{}, Result{}, err
	}
	return s.adopt(a2.Clone(), b2.Clone())
}
