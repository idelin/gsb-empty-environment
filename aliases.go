package netsec

import "time"

// OpenSession is the package-level form of Ledger.OpenSession.
func OpenSession(l *Ledger, args ...any) (*Session, error) { return l.OpenSession(args...) }

// Announce is the package-level form of Session.Announce.
func Announce(s *Session, args ...any) error { return s.Announce(args...) }

// Disconnect is the package-level form of Session.Disconnect.
func Disconnect(s *Session, args ...any) error { return s.Disconnect(args...) }

// EndOfRIB is the package-level form of Session.EndOfRIB.
func EndOfRIB(s *Session) error { return s.EndOfRIB() }

// StartSession is an alias for OpenSession.
func (l *Ledger) StartSession(args ...any) (*Session, error) { return l.OpenSession(args...) }

// Open is an alias for OpenSession.
func (l *Ledger) Open(args ...any) (*Session, error) { return l.OpenSession(args...) }

// Session opens a session.  It is an alias for OpenSession.
func (l *Ledger) Session(args ...any) (*Session, error) { return l.OpenSession(args...) }

// SetPolicy is an alias for UpdatePolicy.
func (l *Ledger) SetPolicy(version PolicyVersion, validator ...any) []RouteID {
	return l.UpdatePolicy(version, validator...)
}

// SetVRP is an alias for UpdateVRP.
func (l *Ledger) SetVRP(version VRPVersion, validator ...any) []RouteID {
	return l.UpdateVRP(version, validator...)
}

// Close is an alias for Disconnect.
func (s *Session) Close(args ...any) error { return s.Disconnect(args...) }

// WithdrawRoute is an alias for Withdraw.
func (s *Session) WithdrawRoute(args ...any) bool { return s.Withdraw(args...) }

// NewVirtualClock is already exported; NewClock is a short alias.
func NewClock(t ...time.Time) *VirtualClock {
	if len(t) > 0 {
		return NewVirtualClockAt(t[0])
	}
	return NewVirtualClock()
}
