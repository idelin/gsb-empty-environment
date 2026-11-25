package evidence

import (
	"fmt"
	netip "net/netip"
	"strings"
	"time"
	"unicode/utf8"
)

// Challenge identifies the validation challenge type bound to an order.
type Challenge string

const (
	ChallengeHTTP01    Challenge = "http-01"
	ChallengeDNS01     Challenge = "dns-01"
	ChallengeTLSALPN01 Challenge = "tls-alpn-01"
)

func (c Challenge) Valid() bool {
	s := string(c)
	if s == "" || len(s) > 64 || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

// Order is the certificate order context.  IP, port, challenge, account,
// nonce and public-key digest are all covered by Order.Digest and therefore by
// every witness that commits to that digest.
type Order struct {
	IP                  netip.Addr `json:"ip"`
	Port                uint16     `json:"port"`
	Challenge           Challenge  `json:"challenge"`
	Account             string     `json:"account"`
	Nonce               string     `json:"nonce"`
	PublicKeyDigest     Digest     `json:"public_key_digest"`
	MinDomains          int        `json:"min_domains"`
	AuthorizationWindow Interval   `json:"authorization_window"`
}

// Validate checks every order binding and re-runs the public-IP policy.
func (o Order) Validate() error {
	if _, err := NormalizePublicAddr(o.IP); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidOrder, err)
	}
	if o.Port == 0 {
		return fmt.Errorf("%w: port is zero", ErrInvalidOrder)
	}
	if !o.Challenge.Valid() {
		return fmt.Errorf("%w: invalid challenge type %q", ErrInvalidOrder, o.Challenge)
	}
	if strings.TrimSpace(o.Account) == "" || !utf8.ValidString(o.Account) {
		return fmt.Errorf("%w: invalid account", ErrInvalidOrder)
	}
	if strings.TrimSpace(o.Nonce) == "" || !utf8.ValidString(o.Nonce) {
		return fmt.Errorf("%w: invalid nonce", ErrInvalidOrder)
	}
	if o.PublicKeyDigest.IsZero() {
		return fmt.Errorf("%w: zero public-key digest", ErrInvalidOrder)
	}
	if o.MinDomains < 1 {
		return fmt.Errorf("%w: min domains must be at least 1", ErrInvalidOrder)
	}
	if !o.AuthorizationWindow.Valid() {
		return fmt.Errorf("%w: invalid authorization window", ErrInvalidOrder)
	}
	return nil
}

// Normalize validates the order and returns a canonical copy.
func (o Order) Normalize() (Order, error) {
	addr, err := NormalizePublicAddr(o.IP)
	if err != nil {
		return Order{}, fmt.Errorf("%w: %w", ErrInvalidOrder, err)
	}
	o.IP = addr
	if err := o.Validate(); err != nil {
		return Order{}, err
	}
	return o, nil
}

// Digest returns the canonical binding digest of the order.
func (o Order) Digest() Digest {
	addr, err := NormalizePublicAddr(o.IP)
	if err != nil {
		addr = o.IP
	}
	c := newCanonical("ip-cert-order/v1")
	c.str(addr.String())
	c.u64(uint64(o.Port))
	c.str(string(o.Challenge))
	c.str(o.Account)
	c.str(o.Nonce)
	c.digest(o.PublicKeyDigest)
	c.i64(int64(o.MinDomains))
	c.timeValue(o.AuthorizationWindow.Start)
	c.timeValue(o.AuthorizationWindow.End)
	return c.sum()
}

// AuthorizedAt reports whether t is inside the authorization window.
func (o Order) AuthorizedAt(t time.Time) bool {
	return o.AuthorizationWindow.Contains(t)
}

// AddrPort returns the order's target address and port.
func (o Order) AddrPort() netip.AddrPort {
	return netip.AddrPortFrom(o.IP, o.Port)
}

// NewOrder is a convenience constructor.  The authorization window is
// [authStart, authEnd).
func NewOrder(ip string, port uint16, challenge Challenge, account, nonce string,
	publicKeyDigest Digest, minDomains int, authStart, authEnd time.Time) (Order, error) {
	addr, err := ParsePublicIP(ip)
	if err != nil {
		return Order{}, err
	}
	o := Order{
		IP:                  addr,
		Port:                port,
		Challenge:           challenge,
		Account:             account,
		Nonce:               nonce,
		PublicKeyDigest:     publicKeyDigest,
		MinDomains:          minDomains,
		AuthorizationWindow: Interval{Start: authStart, End: authEnd},
	}
	return o.Normalize()
}
