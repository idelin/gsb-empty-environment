package netsec

import (
	"errors"
	"net/netip"
	"strings"
	"time"
)

// LoginMapper resolves the SSH login name supplied by the client to the
// principal name that must appear in the certificate. A nil mapper is
// replaced by the identity mapping. Returning an empty string rejects
// the login outright.
type LoginMapper func(login string) (string, error)

// Authorizer enforces SSH user-certificate policy after the SSH
// handshake has authenticated the client's possession of the key
// embedded in the certificate.
type Authorizer struct {
	ca     PublicKey
	mapper LoginMapper
	now    func() time.Time
}

// Option configures an Authorizer.
type Option func(*Authorizer)

// WithLoginMapper overrides the default identity login mapping.
func WithLoginMapper(mapper LoginMapper) Option {
	return func(a *Authorizer) {
		if mapper != nil {
			a.mapper = mapper
		}
	}
}

// WithClock overrides the time source, mainly for deterministic tests.
func WithClock(now func() time.Time) Option {
	return func(a *Authorizer) {
		if now != nil {
			a.now = now
		}
	}
}

// NewAuthorizer trusts exactly one CA and rejects every certificate not
// signed by it.
func NewAuthorizer(ca PublicKey, opts ...Option) (*Authorizer, error) {
	if ca == nil {
		return nil, errors.New("netsec: trusted CA is required")
	}
	a := &Authorizer{
		ca:     ca,
		mapper: identityMapper,
		now:    time.Now,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a, nil
}

func identityMapper(login string) (string, error) { return login, nil }

// Request is the post-authentication callback input. RawCertificate is the
// blob presented by the client; RemoteAddr is the real transport peer
// (never a client-supplied value); RequestedCommand is what the client
// asked to run, which is advisory once force-command is present.
type Request struct {
	RawCertificate   []byte
	Login            string
	RemoteAddr       netip.Addr
	RequestedCommand string
}

// Decision is the authorization result.
type Decision struct {
	// Principal is the mapped login name matched against the certificate.
	Principal string
	// KeyID is the certificate's key id, useful for audit logs.
	KeyID string
	// Command is the command the session must execute: the certificate's
	// force-command when present, otherwise the client's request.
	Command string
	// ForceCommand reports whether the command came from the certificate.
	ForceCommand bool
	Certificate  *Certificate
}

// Policy error sentinels. Callers should match with errors.Is.
var (
	// ErrUntrustedCA means the certificate was not signed by the trusted CA.
	ErrUntrustedCA = errors.New("netsec: certificate not signed by trusted CA")
	// ErrNotUserCertificate means the cert_type is not SSH_CERT_TYPE_USER.
	ErrNotUserCertificate = errors.New("netsec: certificate is not a user certificate")
	// ErrPrincipalMismatch means the mapped login is absent from principals.
	ErrPrincipalMismatch = errors.New("netsec: mapped login not in certificate principals")
	// ErrCertificateExpired means now is outside the half-open validity window.
	ErrCertificateExpired = errors.New("netsec: certificate outside validity interval")
	// ErrUnknownCriticalOption means a critical option the gateway does not
	// understand is present; such certificates must be refused wholesale.
	ErrUnknownCriticalOption = errors.New("netsec: unknown critical option")
	// ErrSourceAddressDenied means the peer address is not permitted by the
	// certificate's source-address option.
	ErrSourceAddressDenied = errors.New("netsec: source address not permitted")
	// ErrMalformedSourceAddress means the source-address option cannot be parsed.
	ErrMalformedSourceAddress = errors.New("netsec: malformed source-address option")
)

// Authorize runs the full policy chain. The first failed check returns a
// wrapped sentinel error; a non-nil Decision is returned only on success.
func (a *Authorizer) Authorize(req Request) (*Decision, error) {
	cert, err := ParseCertificate(req.RawCertificate)
	if err != nil {
		return nil, err
	}

	// 1. Only the provisioned CA is trusted; this also rejects tampering.
	if err := cert.verifyWith(a.ca); err != nil {
		if errors.Is(err, ErrBadSignature) {
			return nil, ErrUntrustedCA
		}
		return nil, err
	}

	// 2. User certificates only.
	if cert.Type != CertTypeUser {
		return nil, ErrNotUserCertificate
	}

	// 3. Unknown critical options are fatal, before any other policy use.
	for name := range cert.CriticalOptions {
		if _, ok := knownCriticalOptions[name]; !ok {
			return nil, errors.Join(ErrUnknownCriticalOption, errors.New("option="+name))
		}
	}

	// 4. The mapped login must be one of the certificate principals.
	login := strings.TrimSpace(req.Login)
	if login == "" {
		return nil, ErrPrincipalMismatch
	}
	principal, err := a.mapper(login)
	if err != nil {
		return nil, errors.Join(ErrPrincipalMismatch, err)
	}
	if principal == "" || !contains(cert.ValidPrincipals, principal) {
		return nil, ErrPrincipalMismatch
	}

	// 5. Half-open validity window: ValidAfter <= now < ValidBefore.
	now := a.now().UTC()
	if !cert.ValidAfter.IsZero() && now.Before(cert.ValidAfter) {
		return nil, ErrCertificateExpired
	}
	if !cert.ValidBefore.IsZero() && !now.Before(cert.ValidBefore) {
		return nil, ErrCertificateExpired
	}

	// 6. source-address is matched against the real transport peer.
	if spec, ok := cert.CriticalOptions[OptionSourceAddress]; ok {
		if err := checkSourceAddress(spec, req.RemoteAddr); err != nil {
			return nil, err
		}
	}

	// 7. force-command pins the executed command; the client request is
	// ignored whenever it is present.
	decision := &Decision{
		Principal:   principal,
		KeyID:       cert.KeyID,
		Command:     req.RequestedCommand,
		Certificate: cert,
	}
	if forced, ok := cert.CriticalOptions[OptionForceCommand]; ok {
		decision.Command = forced
		decision.ForceCommand = true
	}

	return decision, nil
}

var knownCriticalOptions = map[string]struct{}{
	OptionForceCommand:  {},
	OptionSourceAddress: {},
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// checkSourceAddress evaluates the source-address comma-separated list of
// CIDRs (bare IPs are treated as single-host prefixes). The peer must match
// at least one entry and address families must agree.
func checkSourceAddress(spec string, peer netip.Addr) error {
	if !peer.IsValid() {
		return ErrSourceAddressDenied
	}
	peer = peer.Unmap()

	matched := false
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return ErrMalformedSourceAddress
		}

		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			// A bare address means a /32 (v4) or /128 (v6) prefix.
			addr, addrErr := netip.ParseAddr(entry)
			if addrErr != nil {
				return errors.Join(ErrMalformedSourceAddress, addrErr)
			}
			hostPrefix, prefixErr := addr.Unmap().Prefix(addr.BitLen())
			if prefixErr != nil {
				return ErrMalformedSourceAddress
			}
			prefix = hostPrefix
		}

		prefixAddr := prefix.Addr().Unmap()
		if prefixAddr.BitLen() != peer.BitLen() {
			continue
		}
		if prefix.Contains(peer) {
			matched = true
		}
	}
	if !matched {
		return ErrSourceAddressDenied
	}
	return nil
}
