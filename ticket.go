package krb5policy

import (
	"fmt"
	"strings"
	"time"
)

// IntegrityMAC is an HMAC-SHA256 digest and the key identity used to produce
// it.  KeyPrincipal is compared case-insensitively in the same way as other
// directory principals; the key bytes themselves are never serialized into a
// ticket.
type IntegrityMAC struct {
	Algorithm    string
	KeyPrincipal string
	KeyVersion   int
	Value        []byte
}

// TicketSeal is the MAC protecting a ticket's canonical metadata.
type TicketSeal = IntegrityMAC

// PACSignature is the MAC protecting a PAC body.
type PACSignature = IntegrityMAC

const (
	// MACAlgorithmHMACSHA256 is the only algorithm accepted by this package.
	MACAlgorithmHMACSHA256 = "HMAC-SHA256"
)

// TicketFlags uses the RFC 4120 bit numbering.  The two high bits are
// Windows/KILE extensions retained here so a host can fail closed when they
// appear on a ticket.
type TicketFlags uint32

const (
	FlagForwardable TicketFlags = 1 << iota
	FlagForwarded
	FlagProxiable
	FlagProxy
	FlagMayPostdate
	FlagPostdated
	FlagInvalid
	FlagRenewable
	FlagInitial
	FlagPreAuthent
	FlagHWAuthent
	FlagTransitedPolicyChecked
	FlagOKAsDelegate

	// Windows/KILE protection extensions.  They are intentionally outside
	// the low RFC 4120 flag range to avoid colliding with future registered
	// bits.
	FlagNotDelegated TicketFlags = 1 << 20
	FlagSensitive    TicketFlags = 1 << 21
)

// Has reports whether all bits in mask are set.
func (f TicketFlags) Has(mask TicketFlags) bool {
	return f&mask == mask
}

// Protected reports whether the ticket carries a non-delegation marker.
func (f TicketFlags) Protected() bool {
	return f&(FlagNotDelegated|FlagSensitive) != 0
}

func (f TicketFlags) String() string {
	var names []string
	type namedFlag struct {
		bit  TicketFlags
		name string
	}
	ordered := []namedFlag{
		{FlagForwardable, "forwardable"},
		{FlagForwarded, "forwarded"},
		{FlagProxiable, "proxiable"},
		{FlagProxy, "proxy"},
		{FlagMayPostdate, "may-postdate"},
		{FlagPostdated, "postdated"},
		{FlagInvalid, "invalid"},
		{FlagRenewable, "renewable"},
		{FlagInitial, "initial"},
		{FlagPreAuthent, "pre-authent"},
		{FlagHWAuthent, "hw-authent"},
		{FlagTransitedPolicyChecked, "transited-policy-checked"},
		{FlagOKAsDelegate, "ok-as-delegate"},
		{FlagNotDelegated, "not-delegated"},
		{FlagSensitive, "sensitive"},
	}
	for _, nf := range ordered {
		if f.Has(nf.bit) {
			names = append(names, nf.name)
		}
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ",")
}

// AccountFlags are the directory/PAC protection bits used by delegation
// policy.
type AccountFlags uint32

const (
	AccountNotDelegated AccountFlags = 1 << iota
	AccountSensitive
)

// Protected reports whether the account must be excluded from a delegation
// chain.
func (f AccountFlags) Protected() bool {
	return f&(AccountNotDelegated|AccountSensitive) != 0
}

// Has reports whether all mask bits are set.
func (f AccountFlags) Has(mask AccountFlags) bool {
	return f&mask == mask
}

func (f AccountFlags) String() string {
	switch {
	case f.Protected():
		return "not-delegated+sensitive"
	case f.Has(AccountNotDelegated):
		return "not-delegated"
	case f.Has(AccountSensitive):
		return "sensitive"
	default:
		return "none"
	}
}

// PAC is the subset of Privilege Attribute Certificate data relevant to
// delegation policy.  Signatures bind the body with real HMAC-SHA256 keys.
type PAC struct {
	User       Principal
	UserFlags  AccountFlags
	Groups     []string
	LogonTime  time.Time
	Signatures []PACSignature
}

// Ticket is a normalized Kerberos ticket view.  It is deliberately limited to
// the fields needed for trust, integrity, lifetime, and delegation policy.
type Ticket struct {
	Client      Principal
	Server      Principal
	IssuerRealm string

	Flags     TicketFlags
	AuthTime  time.Time
	StartTime time.Time
	EndTime   time.Time
	RenewTill time.Time

	Transited TransitedEncoding
	PAC       *PAC
	Seal      TicketSeal
}

// ClientRealm returns the realm in the client name, falling back to the ticket
// field only if Principal.Realm is empty.
func (t Ticket) ClientRealm() string {
	if t.Client.Realm != "" {
		return t.Client.Realm
	}
	return ""
}

// ServiceRealm returns the realm in the service principal.
func (t Ticket) ServiceRealm() string {
	return t.Server.Realm
}

// EffectiveStart returns StartTime, or AuthTime when StartTime is unset.
func (t Ticket) EffectiveStart() time.Time {
	if !t.StartTime.IsZero() {
		return t.StartTime
	}
	return t.AuthTime
}

// EffectiveIssuerRealm returns IssuerRealm or, when omitted, the realm in the
// server principal.  For a krbtgt/DEST@ISSUER ticket this is the issuing realm.
func (t Ticket) EffectiveIssuerRealm() string {
	if t.IssuerRealm != "" {
		return t.IssuerRealm
	}
	return t.Server.Realm
}

// ValidateTicketWindow applies a strict half-open-inclusive clock-skew
// policy: a ticket is valid during [start-skew, end+skew], and equality at the
// exact skew boundary is accepted.  One nanosecond outside is rejected.
func ValidateTicketWindow(t *Ticket, now time.Time, skew time.Duration) error {
	if t == nil {
		return fmt.Errorf("%w: nil ticket", ErrInvalidTicketWindow)
	}
	if skew < 0 {
		return fmt.Errorf("%w: negative clock skew", ErrInvalidConfig)
	}
	if t.EndTime.IsZero() {
		return fmt.Errorf("%w: missing endtime", ErrInvalidTicketWindow)
	}
	start := t.EffectiveStart()
	if start.IsZero() {
		return fmt.Errorf("%w: missing start/auth time", ErrInvalidTicketWindow)
	}
	if !t.EndTime.After(start) {
		return fmt.Errorf("%w: endtime %s is not after start %s", ErrInvalidTicketWindow, t.EndTime, start)
	}
	if !t.AuthTime.IsZero() && t.AuthTime.After(start) {
		return fmt.Errorf("%w: auth time is after start time", ErrInvalidTicketWindow)
	}
	if !t.RenewTill.IsZero() && t.RenewTill.Before(t.EndTime) {
		return fmt.Errorf("%w: renew-till precedes endtime", ErrInvalidTicketWindow)
	}
	if t.Flags.Has(FlagRenewable) && t.RenewTill.IsZero() {
		return fmt.Errorf("%w: renewable ticket has no renew-till", ErrInvalidTicketWindow)
	}

	now = now.UTC()
	start = start.UTC()
	end := t.EndTime.UTC()
	if now.Before(start.Add(-skew)) {
		return fmt.Errorf("%w: now %s before start-skew %s", ErrTicketNotYetValid, now, start.Add(-skew))
	}
	if now.After(end.Add(skew)) {
		return fmt.Errorf("%w: now %s after end+skew %s", ErrTicketExpired, now, end.Add(skew))
	}
	return nil
}

// ValidateRenewalWindow verifies that the current time is still inside the
// ticket's renewable lifetime, with the same inclusive skew boundary.
func ValidateRenewalWindow(t *Ticket, now time.Time, skew time.Duration) error {
	if t == nil {
		return fmt.Errorf("%w: nil ticket", ErrRenewalWindow)
	}
	if !t.Flags.Has(FlagRenewable) {
		return fmt.Errorf("%w: ticket is not renewable", ErrRenewalWindow)
	}
	if t.RenewTill.IsZero() {
		return fmt.Errorf("%w: missing renew-till", ErrRenewalWindow)
	}
	if skew < 0 {
		return fmt.Errorf("%w: negative clock skew", ErrInvalidConfig)
	}
	if now.UTC().After(t.RenewTill.UTC().Add(skew)) {
		return fmt.Errorf("%w: now %s after renew-till+skew %s", ErrRenewalWindow, now.UTC(), t.RenewTill.UTC().Add(skew))
	}
	return nil
}
