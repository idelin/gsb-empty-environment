package netsec

import (
	"errors"
	"fmt"
	"time"
)

const maxDuration = time.Duration(1<<63 - 1)

var (
	// ErrInvalidPolicy reports a ticket age policy that cannot be evaluated.
	ErrInvalidPolicy = errors.New("netsec: invalid ticket age policy")

	// ErrInvalidLifetime reports a non-positive ticket lifetime.
	ErrInvalidLifetime = fmt.Errorf("%w: lifetime must be positive", ErrInvalidPolicy)

	// ErrInvalidTolerance reports a negative ticket age tolerance.
	ErrInvalidTolerance = fmt.Errorf("%w: tolerance must be non-negative", ErrInvalidPolicy)

	// ErrTicketAgeOutsideWindow reports a ticket whose decoded age is more than
	// the configured tolerance beyond its lifetime.
	ErrTicketAgeOutsideWindow = errors.New("netsec: ticket age is outside its lifetime window")

	// ErrTicketAgeMismatch reports a client-reported age that differs from the
	// server-observed ticket age by more than the configured tolerance.
	ErrTicketAgeMismatch = errors.New("netsec: ticket age does not match ticket issue time")
)

// TicketAgeOutsideWindowError describes an age that exceeded its allowed
// lifetime window.
type TicketAgeOutsideWindowError struct {
	Age   time.Duration
	Limit time.Duration
}

func (e *TicketAgeOutsideWindowError) Error() string {
	return fmt.Sprintf(
		"netsec: ticket age %s exceeds lifetime window %s",
		e.Age,
		e.Limit,
	)
}

// Unwrap makes errors.Is(err, ErrTicketAgeOutsideWindow) report true.
func (e *TicketAgeOutsideWindowError) Unwrap() error {
	return ErrTicketAgeOutsideWindow
}

// TicketAgeMismatchError describes a mismatch between the age reported by the
// client and the age observed by the server.
type TicketAgeMismatchError struct {
	ClientAge time.Duration
	ServerAge time.Duration
	Tolerance time.Duration
}

func (e *TicketAgeMismatchError) Error() string {
	return fmt.Sprintf(
		"netsec: client ticket age %s differs from server age %s by more than %s",
		e.ClientAge,
		e.ServerAge,
		e.Tolerance,
	)
}

// Unwrap makes errors.Is(err, ErrTicketAgeMismatch) report true.
func (e *TicketAgeMismatchError) Unwrap() error {
	return ErrTicketAgeMismatch
}

// TicketAgePolicy defines how old a ticket may be according to a TLS stack.
//
// Lifetime is the ticket_lifetime supplied in NewSessionTicket. Tolerance is
// the caller-provided clock and scheduling allowance. Both values are
// required; Tolerance may be zero.
type TicketAgePolicy struct {
	Lifetime  time.Duration
	Tolerance time.Duration
}

// Limit returns the largest ticket age accepted by the policy. The limit is
// lifetime plus tolerance so that a ticket at the end of its lifetime remains
// usable during the caller's allowed deviation.
func (p TicketAgePolicy) Limit() (time.Duration, error) {
	if p.Lifetime <= 0 {
		return 0, ErrInvalidLifetime
	}
	if p.Tolerance < 0 {
		return 0, ErrInvalidTolerance
	}
	if p.Tolerance > maxDuration-p.Lifetime {
		return maxDuration, nil
	}
	return p.Lifetime + p.Tolerance, nil
}

// ValidateAge checks an already-decoded ticket age against the policy. Ages at
// or below the limit are accepted; ages beyond it are rejected.
func (p TicketAgePolicy) ValidateAge(age time.Duration) error {
	limit, err := p.Limit()
	if err != nil {
		return err
	}
	if age < 0 || age > limit {
		return &TicketAgeOutsideWindowError{Age: age, Limit: limit}
	}
	return nil
}

// Allows reports whether age is within the policy's lifetime window.
func (p TicketAgePolicy) Allows(age time.Duration) bool {
	return p.ValidateAge(age) == nil
}

// DecodeTicketAge removes the ticket_age_add obfuscation from a TLS 1.3
// obfuscated_ticket_age value. Both inputs are protocol-level uint32 values;
// the subtraction intentionally wraps modulo 2^32 as required by RFC 8446.
//
// The returned duration is the age claimed by the client, in milliseconds.
func DecodeTicketAge(obfuscatedTicketAge, ticketAgeAdd uint32) time.Duration {
	return time.Duration(obfuscatedTicketAge-ticketAgeAdd) * time.Millisecond
}

// UnobfuscateTicketAge is an alias for DecodeTicketAge.
func UnobfuscateTicketAge(obfuscatedTicketAge, ticketAgeAdd uint32) time.Duration {
	return DecodeTicketAge(obfuscatedTicketAge, ticketAgeAdd)
}

// ValidateTicketAge decodes a client-reported ticket age and checks that it
// does not exceed lifetime plus tolerance. It is the minimal check for a TLS
// stack which only retains the ticket lifetime alongside an opaque ticket.
//
// The comparison is deliberately strict: an age of lifetime+tolerance is
// accepted, while lifetime+tolerance+1ns is rejected.
func ValidateTicketAge(obfuscatedTicketAge, ticketAgeAdd uint32, lifetime, tolerance time.Duration) error {
	age := DecodeTicketAge(obfuscatedTicketAge, ticketAgeAdd)
	return (TicketAgePolicy{
		Lifetime:  lifetime,
		Tolerance: tolerance,
	}).ValidateAge(age)
}

// TicketAgeWithinWindow reports whether ValidateTicketAge accepts the ticket.
func TicketAgeWithinWindow(obfuscatedTicketAge, ticketAgeAdd uint32, lifetime, tolerance time.Duration) bool {
	return ValidateTicketAge(obfuscatedTicketAge, ticketAgeAdd, lifetime, tolerance) == nil
}

// TicketAge contains the values needed to validate a TLS 1.3 PSK identity.
//
// Obfuscated and AgeAdd are the obfuscated_ticket_age and ticket_age_add
// protocol values. Lifetime is the ticket_lifetime from NewSessionTicket.
// IssuedAt is optional; when set, Validate also checks the client-reported age
// against the age observed on the server.
type TicketAge struct {
	Obfuscated uint32
	AgeAdd     uint32
	IssuedAt   time.Time
	Lifetime   time.Duration
}

// Decode returns the client-reported ticket age in milliseconds.
func (t TicketAge) Decode() time.Duration {
	return DecodeTicketAge(t.Obfuscated, t.AgeAdd)
}

// Validate checks the decoded age against the ticket lifetime and tolerance.
// If IssuedAt is non-zero, it additionally requires the client-reported age
// to agree with the server-observed age within tolerance.
func (t TicketAge) Validate(now time.Time, tolerance time.Duration) error {
	policy := TicketAgePolicy{
		Lifetime:  t.Lifetime,
		Tolerance: tolerance,
	}
	clientAge := t.Decode()
	if err := policy.ValidateAge(clientAge); err != nil {
		return err
	}
	if t.IssuedAt.IsZero() {
		return nil
	}

	serverAge := now.Sub(t.IssuedAt)
	if serverAge < 0 {
		if -serverAge > tolerance {
			return &TicketAgeMismatchError{
				ClientAge: clientAge,
				ServerAge: serverAge,
				Tolerance: tolerance,
			}
		}
		serverAge = 0
	}
	if err := policy.ValidateAge(serverAge); err != nil {
		return err
	}
	delta := clientAge - serverAge
	if delta < 0 {
		delta = -delta
	}
	if delta > tolerance {
		return &TicketAgeMismatchError{
			ClientAge: clientAge,
			ServerAge: serverAge,
			Tolerance: tolerance,
		}
	}
	return nil
}
