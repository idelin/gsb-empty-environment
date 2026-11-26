package sshcert

import (
	"net"
	"strings"
)

// VerifyOptions controls host-side certificate policy. The zero value is
// deliberately unusable for authentication: callers must state the expected
// service, the trusted CA and the principal being requested.
type VerifyOptions struct {
	// ExpectedType must be CertTypeUser or CertTypeHost and is matched
	// against the certificate type.
	ExpectedType CertType

	// CAPublicKeyBlob is the trusted CA's "ssh-ed25519" public-key blob.
	// The embedded signature key must equal it exactly.
	CAPublicKeyBlob []byte

	// Principal is the username (user cert) or hostname (host cert) the
	// peer is requesting. It must appear in valid_principals.
	Principal string

	// AllowWildcardPrincipal permits an empty valid_principals list to act
	// as a wildcard. It defaults to false and must be enabled explicitly by
	// the host; an empty list is otherwise rejected.
	AllowWildcardPrincipal bool

	// RemoteIP is the source IP of the connection. It is required when the
	// certificate carries a source-address critical option.
	RemoteIP net.IP

	// Now is the evaluation time in seconds since the UNIX epoch. Zero uses
	// the host wall clock; tests inject a fixed value.
	Now int64

	// KRL, when non-nil, is consulted for every presented certificate and a
	// hit rejects authentication.
	KRL *KRL

	// AllowedCriticalOptions lists additional critical-option names the
	// host understands. The standard options force-command, source-address
	// and verify-required are always accepted. Anything else (including
	// OpenSSH extensions the host does not implement) rejects the cert.
	AllowedCriticalOptions []string
}

// VerifyResult carries data the host must enforce after a successful check.
type VerifyResult struct {
	// ForceCommand is non-empty when the certificate's force-command
	// critical option pins the session command. The host must execute it
	// and ignore any client-supplied command.
	ForceCommand string
	// ForceCommandPresent distinguishes a pinned empty command from absence.
	ForceCommandPresent bool
}

// Verify runs the full host-side verification of a parsed certificate:
//
//  1. certificate type matches the requested service,
//  2. every critical option is known (unknown ones reject the cert),
//  3. the Ed25519 signature verifies under the trusted CA,
//  4. valid_after <= now < valid_before (zero bounds are unbounded),
//  5. the requested principal is in valid_principals (empty list needs an
//     explicit wildcard opt-in),
//  6. source-address CIDRs cover the connection IP,
//  7. force-command is parsed and returned as non-overridable,
//  8. the KRL, when supplied, does not revoke the credential.
func Verify(c *Certificate, opts VerifyOptions) (*VerifyResult, error) {
	if c == nil {
		return nil, ErrNotEd25519Cert
	}
	if opts.ExpectedType != CertTypeUser && opts.ExpectedType != CertTypeHost {
		return nil, ErrBadCertType
	}
	if c.Type != opts.ExpectedType {
		return nil, ErrCertTypeMismatch
	}

	if err := checkCriticalOptions(c, opts.AllowedCriticalOptions); err != nil {
		return nil, err
	}

	if err := c.VerifySignature(opts.CAPublicKeyBlob); err != nil {
		return nil, err
	}

	now := opts.Now
	if now == 0 {
		now = unixNow()
	}
	if err := checkLifetime(c, uint64(now)); err != nil {
		return nil, err
	}

	if err := checkPrincipal(c, opts.Principal, opts.AllowWildcardPrincipal); err != nil {
		return nil, err
	}

	if err := checkSourceAddress(c, opts.RemoteIP); err != nil {
		return nil, err
	}

	result, err := checkForceCommand(c)
	if err != nil {
		return nil, err
	}

	if opts.KRL != nil {
		if err := opts.KRL.CheckCertificate(c); err != nil {
			return nil, err
		}
	}

	return result, nil
}

// checkCriticalOptions enforces the "critical" part of critical options:
// any name the host has not opted into rejects the certificate outright.
func checkCriticalOptions(c *Certificate, extraAllowed []string) error {
	allowed := make(map[string]bool, len(extraAllowed)+3)
	allowed[CriticalForceCommand] = true
	allowed[CriticalSourceAddress] = true
	allowed[CriticalVerifyRequired] = true
	for _, name := range extraAllowed {
		allowed[name] = true
	}
	for _, opt := range c.CriticalOptions {
		if !allowed[opt.Name] {
			return ErrUnknownCriticalOption
		}
	}
	return nil
}

// checkLifetime applies valid_after <= now < valid_before. A zero
// valid_after means "from the epoch" and a zero valid_before means "no
// expiry", matching OpenSSH semantics.
func checkLifetime(c *Certificate, now uint64) error {
	if c.ValidAfter != 0 && now < c.ValidAfter {
		return ErrCertNotYetValid
	}
	if c.ValidBefore != 0 && now >= c.ValidBefore {
		return ErrCertExpired
	}
	return nil
}

// checkPrincipal enforces membership in valid_principals. An empty list is
// only accepted when the host explicitly enables wildcard behavior.
func checkPrincipal(c *Certificate, principal string, allowWildcard bool) error {
	if len(c.ValidPrincipals) == 0 {
		if !allowWildcard {
			return ErrPrincipalNotAllowed
		}
		return nil
	}
	if principal == "" {
		return ErrPrincipalRequired
	}
	for _, p := range c.ValidPrincipals {
		if p == principal {
			return nil
		}
	}
	return ErrPrincipalNotAllowed
}

// checkSourceAddress requires the connection IP to fall inside at least one
// CIDR (or bare IP literal) listed by the source-address critical option.
// Absence of the option means no network restriction.
func checkSourceAddress(c *Certificate, remote net.IP) error {
	raw := c.SourceAddresses()
	if raw == nil {
		return nil
	}
	if remote == nil {
		return ErrSourceAddressRequired
	}

	r := newWireReader(raw)
	spec, err := r.string()
	if err != nil || !r.done() {
		return ErrBadSourceAddress
	}
	cidrs := strings.Split(string(spec), ",")
	if len(cidrs) == 0 {
		return ErrBadSourceAddress
	}

	remote = remote.To16()
	if remote == nil {
		return ErrSourceAddressDenied
	}
	matched := false
	for _, entry := range cidrs {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return ErrBadSourceAddress
		}
		ipnet, err := parseSourceEntry(entry)
		if err != nil {
			return ErrBadSourceAddress
		}
		if ipnet.Contains(remote) {
			matched = true
		}
	}
	if !matched {
		return ErrSourceAddressDenied
	}
	return nil
}

// parseSourceEntry accepts both CIDR notation ("10.0.0.0/8", "fd00::/32")
// and bare IP literals ("10.1.2.3"), matching sshd's source-address syntax.
func parseSourceEntry(entry string) (*net.IPNet, error) {
	if _, ipnet, err := net.ParseCIDR(entry); err == nil {
		return ipnet, nil
	}
	ip := net.ParseIP(entry)
	if ip == nil {
		return nil, ErrBadSourceAddress
	}
	bits := 32
	if v4 := ip.To4(); v4 == nil {
		bits = 128
	} else {
		ip = v4
	}
	mask := net.CIDRMask(bits, bits)
	return &net.IPNet{IP: ip, Mask: mask}, nil
}

// checkForceCommand extracts the pinned command. The option value is itself
// an SSH string; malformed encoding rejects the certificate rather than
// silently dropping the forced command.
func checkForceCommand(c *Certificate) (*VerifyResult, error) {
	res := &VerifyResult{}
	cmd, present := c.ForceCommand()
	if !present {
		if c.forceCommandRaw != nil {
			return nil, ErrBadForceCommand
		}
		return res, nil
	}
	res.ForceCommand = cmd
	res.ForceCommandPresent = true
	return res, nil
}
