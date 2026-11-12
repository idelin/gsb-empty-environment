package netsec

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
)

// ErrNoApplicationProtocol is returned when a client asks for an application
// protocol that this listener does not allow, or when ALPN is absent and no
// legacy protocol is configured. On the wire this must be represented by the
// fatal no_application_protocol alert from RFC 7301, Section 3.1.
var ErrNoApplicationProtocol = errors.New("alpn: no application protocol (RFC 7301)")

// ErrInvalidPolicy is returned when a Policy cannot describe a usable
// listener.
var ErrInvalidPolicy = errors.New("alpn: invalid policy")

// Policy is the ALPN policy for exactly one listener.
//
// Protocols is a closed allow-list, not an open-ended preference list: a
// ClientHello that carries ALPN is accepted only when at least one offered
// protocol appears in Protocols. Protocols are ordered by server preference.
//
// A ClientHello without the ALPN extension is accepted only when
// LegacyProtocol is non-empty. In that case no protocol is negotiated on the
// wire (ConnectionState.NegotiatedProtocol remains empty), and the application
// may assume LegacyProtocol. This is intentional: it permits legacy SMTP or
// implicit-FTP clients, but never lets a cross-protocol browser request
// silently fall back to the listener's legacy service.
type Policy struct {
	Protocols      []string
	LegacyProtocol string
}

// NewPolicy validates and copies protocols and legacyProtocol into a Policy.
func NewPolicy(protocols []string, legacyProtocol string) (Policy, error) {
	p := Policy{
		Protocols:      slices.Clone(protocols),
		LegacyProtocol: legacyProtocol,
	}
	if err := p.Validate(); err != nil {
		return Policy{}, err
	}
	return p, nil
}

// Validate checks the limits that the TLS wire format imposes on ALPN
// protocol IDs.
func (p Policy) Validate() error {
	if len(p.Protocols) == 0 && p.LegacyProtocol == "" {
		return fmt.Errorf("%w: no ALPN or legacy protocol configured", ErrInvalidPolicy)
	}

	seen := make(map[string]struct{}, len(p.Protocols))
	for _, protocol := range p.Protocols {
		if err := validateProtocolID(protocol); err != nil {
			return fmt.Errorf("%w: ALPN protocol %q: %v", ErrInvalidPolicy, protocol, err)
		}
		if _, duplicate := seen[protocol]; duplicate {
			return fmt.Errorf("%w: duplicate ALPN protocol %q", ErrInvalidPolicy, protocol)
		}
		seen[protocol] = struct{}{}
	}
	if p.LegacyProtocol != "" {
		if err := validateProtocolID(p.LegacyProtocol); err != nil {
			return fmt.Errorf("%w: legacy protocol %q: %v", ErrInvalidPolicy, p.LegacyProtocol, err)
		}
	}
	return nil
}

func validateProtocolID(protocol string) error {
	if len(protocol) == 0 {
		return errors.New("protocol ID is empty")
	}
	if len(protocol) > 255 {
		return errors.New("protocol ID exceeds 255 bytes")
	}
	return nil
}

// Select returns the application protocol for a ClientHello.
//
// clientProtocols is the exact ALPN list from the ClientHello. A nil or empty
// list means the ALPN extension was absent. Selection uses server preference
// order and never falls back to an unrelated protocol.
func (p Policy) Select(clientProtocols []string) (string, error) {
	if len(clientProtocols) == 0 {
		if p.LegacyProtocol == "" {
			return "", ErrNoApplicationProtocol
		}
		return p.LegacyProtocol, nil
	}

	for _, serverProtocol := range p.Protocols {
		for _, clientProtocol := range clientProtocols {
			if serverProtocol == clientProtocol {
				return serverProtocol, nil
			}
		}
	}
	return "", fmt.Errorf("%w: client offered [%s], listener accepts [%s]",
		ErrNoApplicationProtocol,
		strings.Join(clientProtocols, ", "),
		strings.Join(p.Protocols, ", "),
	)
}

// ConfigureTLS returns a clone of base with a strict per-ClientHello ALPN
// check installed.
//
// The returned Config is intended to be used by one listener. If base already
// has GetConfigForClient, it is still called, and the policy is applied to the
// Config it returns. The policy replaces NextProtos on the per-handshake
// Config; callers must not use NextProtos as the source of truth for an
// accepted protocol set.
//
// A client that offers ALPN with no intersection is rejected by Go's TLS
// negotiator with the RFC 7301 no_application_protocol alert. The installed
// callback forces that path even for Go's special h2/http/1.1 fallback case.
func ConfigureTLS(base *tls.Config, policy Policy) (*tls.Config, error) {
	if base == nil {
		return nil, errors.New("alpn: nil base TLS config")
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	policy = policy.clone()

	config := base.Clone()
	previous := config.GetConfigForClient
	config.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		effective := config
		if previous != nil {
			next, err := previous(hello)
			if err != nil {
				return nil, err
			}
			if next != nil {
				effective = next
			}
		}
		return policy.configForClientHello(effective, hello)
	}
	return config, nil
}

func (p Policy) configForClientHello(base *tls.Config, hello *tls.ClientHelloInfo) (*tls.Config, error) {
	config := base.Clone()

	if len(hello.SupportedProtos) == 0 {
		if p.LegacyProtocol == "" {
			markNoApplicationProtocol(hello.Conn)
			return nil, ErrNoApplicationProtocol
		}
		// No ALPN extension means no ALPN protocol is selected. Leaving
		// NextProtos set is harmless because crypto/tls only negotiates when
		// the client also offered ALPN.
		config.NextProtos = slices.Clone(p.Protocols)
		return config, nil
	}

	selected, err := p.Select(hello.SupportedProtos)
	if err != nil {
		// An empty ALPN protocol ID cannot appear in a valid ClientHello.
		// Presenting only it to crypto/tls guarantees a no-intersection
		// result, including for the h2/http/1.1 fallback special case, and
		// makes crypto/tls send alert 120 before any application data.
		config.NextProtos = []string{""}
		return config, nil
	}

	config.NextProtos = []string{selected}
	return config, nil
}

func (p Policy) clone() Policy {
	return Policy{
		Protocols:      slices.Clone(p.Protocols),
		LegacyProtocol: p.LegacyProtocol,
	}
}

func (p Policy) accepts(protocol string) bool {
	return slices.Contains(p.Protocols, protocol)
}

// noApplicationProtocolMarker is implemented by the listener's net.Conn
// wrapper. crypto/tls turns callback errors into alert 80, so the wrapper
// rewrites that single pre-handshake alert to alert 120.
type noApplicationProtocolMarker interface {
	markNoApplicationProtocol()
}

func markNoApplicationProtocol(conn net.Conn) {
	if marker, ok := conn.(noApplicationProtocolMarker); ok {
		marker.markNoApplicationProtocol()
	}
}
