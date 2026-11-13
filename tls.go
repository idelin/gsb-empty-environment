package netsec

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"sync"
)

// recordingConn captures the bytes read by crypto/tls so that the
// certificate_authorities extension can be inspected from GetCertificate.
// crypto/tls does not expose that extension on ClientHelloInfo.
type recordingConn struct {
	net.Conn

	mu        sync.Mutex
	recorded  []byte
	maxRecord int
}

// ClientHelloSource is implemented by connections that retain the raw
// ClientHello bytes. NewServerConn's connection wrapper implements it.
type ClientHelloSource interface {
	ClientHelloBytes() []byte
}

func (c *recordingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n <= 0 {
		return n, err
	}

	c.mu.Lock()
	if c.maxRecord == 0 {
		c.maxRecord = 1 << 20
	}
	if remaining := c.maxRecord - len(c.recorded); remaining > 0 {
		if n < remaining {
			remaining = n
		}
		c.recorded = append(c.recorded, p[:remaining]...)
	}
	c.mu.Unlock()
	return n, err
}

// ClientHelloBytes returns a copy of the handshake bytes captured so far.
func (c *recordingConn) ClientHelloBytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.recorded...)
}

// NewServerConn wraps conn with crypto/tls and enforces the client's
// certificate_authorities extension when selecting a server certificate.
//
// Use this (or NewListener) instead of tls.Server directly. Wrapping the
// connection is required because crypto/tls does not expose the extension
// contents to GetCertificate.
func NewServerConn(conn net.Conn, config *tls.Config) *tls.Conn {
	recorder, ok := conn.(*recordingConn)
	if !ok {
		recorder = &recordingConn{Conn: conn, maxRecord: 1 << 20}
	}
	return tls.Server(recorder, WrapServerConfig(config))
}

// Server is an alias for NewServerConn.
func Server(conn net.Conn, config *tls.Config) *tls.Conn {
	return NewServerConn(conn, config)
}

// NewListener wraps inner so every accepted connection enforces
// certificate_authorities during the TLS handshake.
func NewListener(inner net.Listener, config *tls.Config) net.Listener {
	return &listener{Listener: inner, config: WrapServerConfig(config)}
}

// Listen creates a TCP listener whose accepted connections enforce
// certificate_authorities.
func Listen(network, address string, config *tls.Config) (net.Listener, error) {
	inner, err := net.Listen(network, address)
	if err != nil {
		return nil, err
	}
	return NewListener(inner, config), nil
}

type listener struct {
	net.Listener
	config *tls.Config
}

func (l *listener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return NewServerConn(conn, l.config), nil
}

var wrappedServerConfigs sync.Map

// WrapServerConfig returns a clone of config whose certificate selection
// enforces the certificate_authorities extension. The original config is not
// modified.
//
// When the extension is absent, the normal crypto/tls selection behavior is
// retained. When it is present, only a certificate whose leaf RawIssuer exactly
// matches one of the advertised names is eligible. If no eligible certificate
// exists, the handshake fails with ErrNoUsableCertificate.
//
// If the extension is present but could not be captured, the handshake fails
// closed with ErrAuthoritiesUnavailable. Use NewServerConn or NewListener so the
// extension bytes are available.
func WrapServerConfig(config *tls.Config) *tls.Config {
	if config == nil {
		return nil
	}
	if _, alreadyWrapped := wrappedServerConfigs.Load(config); alreadyWrapped {
		return config
	}

	clone := config.Clone()
	wrappedServerConfigs.Store(clone, struct{}{})

	originalGetCertificate := clone.GetCertificate
	clone.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		extension, err := authoritiesFromClientHello(hello)
		if err != nil {
			return nil, err
		}
		if !extension.Present {
			return defaultServerCertificate(hello, clone.Certificates, originalGetCertificate)
		}
		authorities := presentAuthorities(extension.Authorities)

		if originalGetCertificate != nil {
			certificate, err := originalGetCertificate(hello)
			if err != nil {
				return nil, err
			}
			if certificate == nil {
				return nil, ErrNoUsableCertificate
			}
			if err := ValidateTLSLeaf(certificate, authorities); err != nil {
				return nil, err
			}
			return certificate, nil
		}
		return SelectCertificate(hello, clone.Certificates, authorities)
	}

	if originalGetConfigForClient := clone.GetConfigForClient; originalGetConfigForClient != nil {
		clone.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			config, err := originalGetConfigForClient(hello)
			if err != nil || config == nil {
				return config, err
			}
			if config == clone {
				return clone, nil
			}
			return WrapServerConfig(config), nil
		}
	}

	return clone
}

// NewServerConfig is an alias for WrapServerConfig.
func NewServerConfig(config *tls.Config) *tls.Config {
	return WrapServerConfig(config)
}

// WrapConfig is an alias for WrapServerConfig.
func WrapConfig(config *tls.Config) *tls.Config {
	return WrapServerConfig(config)
}

func defaultServerCertificate(hello *tls.ClientHelloInfo, certificates []tls.Certificate, getCertificate func(*tls.ClientHelloInfo) (*tls.Certificate, error)) (*tls.Certificate, error) {
	if getCertificate != nil {
		certificate, err := getCertificate(hello)
		if err != nil {
			return nil, err
		}
		if certificate == nil {
			return nil, ErrNoUsableCertificate
		}
		return certificate, nil
	}
	return SelectCertificate(hello, certificates, nil)
}

func authoritiesFromClientHello(hello *tls.ClientHelloInfo) (Extension, error) {
	if hello == nil {
		return Extension{}, nil
	}

	advertised := false
	for _, extensionType := range hello.Extensions {
		if extensionType == ExtensionCertificateAuthorities {
			advertised = true
			break
		}
	}

	var captured []byte
	if source, ok := hello.Conn.(ClientHelloSource); ok {
		captured = source.ClientHelloBytes()
	}
	if len(captured) == 0 {
		if advertised {
			return Extension{Present: true}, ErrAuthoritiesUnavailable
		}
		return Extension{}, nil
	}

	parsed, err := ParseClientHello(captured)
	if err != nil {
		if advertised {
			return Extension{}, fmt.Errorf("%w: %v", ErrAuthoritiesUnavailable, err)
		}
		return Extension{}, nil
	}
	if advertised && !parsed.Authorities.Present {
		return Extension{}, fmt.Errorf("%w: extension advertised but not decoded", ErrAuthoritiesUnavailable)
	}
	return parsed.Authorities, nil
}

// AuthoritiesFromClientHello returns the certificate_authorities extension
// advertised by hello. The connection must implement ClientHelloSource so the
// raw extension contents are available; otherwise presence is still detected
// from hello.Extensions and the function fails closed with
// ErrAuthoritiesUnavailable when the extension was advertised.
func AuthoritiesFromClientHello(hello *tls.ClientHelloInfo) (Extension, error) {
	return authoritiesFromClientHello(hello)
}

// ValidateTLSLeaf enforces authorities against a tls.Certificate. A nil
// authorities means the extension was absent and therefore does not restrict
// the certificate.
func ValidateTLSLeaf(certificate *tls.Certificate, authorities Authorities) error {
	if authorities == nil {
		return nil
	}
	leaf, err := certificateLeaf(certificate)
	if err != nil {
		return err
	}
	return authorities.ValidateLeaf(leaf)
}

// ValidateTLSExtension enforces the presence-aware extension against a
// tls.Certificate.
func ValidateTLSExtension(certificate *tls.Certificate, extension Extension) error {
	if !extension.Present {
		return nil
	}
	return ValidateTLSLeaf(certificate, presentAuthorities(extension.Authorities))
}

var wrappedClientConfigs sync.Map

// WrapClientConfig returns a clone of config whose client-certificate
// selection checks the leaf certificate's issuer against the server's
// certificate_authorities list (CertificateRequestInfo.AcceptableCAs).
//
// crypto/tls's default selection can accept a chain when any certificate in it
// matches an acceptable CA. This wrapper requires the leaf itself to match.
func WrapClientConfig(config *tls.Config) *tls.Config {
	if config == nil {
		return nil
	}
	if _, alreadyWrapped := wrappedClientConfigs.Load(config); alreadyWrapped {
		return config
	}

	clone := config.Clone()
	wrappedClientConfigs.Store(clone, struct{}{})

	originalGetClientCertificate := clone.GetClientCertificate
	clone.GetClientCertificate = func(request *tls.CertificateRequestInfo) (*tls.Certificate, error) {
		if request == nil {
			return nil, ErrNoUsableCertificate
		}
		authorities := Authorities(request.AcceptableCAs)
		if len(authorities) == 0 {
			if originalGetClientCertificate != nil {
				certificate, err := originalGetClientCertificate(request)
				if err != nil {
					return nil, err
				}
				if certificate == nil {
					return nil, ErrNoUsableCertificate
				}
				return certificate, nil
			}
			return SelectClientCertificate(request, clone.Certificates, nil)
		}

		if originalGetClientCertificate != nil {
			certificate, err := originalGetClientCertificate(request)
			if err != nil {
				return nil, err
			}
			if certificate == nil {
				return nil, ErrNoUsableCertificate
			}
			if err := ValidateTLSLeaf(certificate, authorities); err != nil {
				return nil, err
			}
			return certificate, nil
		}
		return SelectClientCertificate(request, clone.Certificates, authorities)
	}
	return clone
}

// NewClientConfig is an alias for WrapClientConfig.
func NewClientConfig(config *tls.Config) *tls.Config {
	return WrapClientConfig(config)
}

// SelectClientCertificate chooses the first client certificate whose leaf
// issuer is acceptable. A nil authorities follows crypto/tls's default
// behavior. When authorities is non-nil, there is no fallback to a
// non-matching certificate.
func SelectClientCertificate(request *tls.CertificateRequestInfo, certificates []tls.Certificate, authorities Authorities) (*tls.Certificate, error) {
	if authorities == nil {
		for i := range certificates {
			if certificateRequestSupports(request, &certificates[i]) {
				return &certificates[i], nil
			}
		}
		if len(certificates) > 0 {
			return &certificates[0], nil
		}
		return nil, ErrNoUsableCertificate
	}

	var firstIssuer []byte
	for i := range certificates {
		certificate := &certificates[i]
		leaf, err := certificateLeaf(certificate)
		if err != nil {
			return nil, err
		}
		issuer, err := issuerDER(leaf)
		if err != nil {
			return nil, err
		}
		if firstIssuer == nil {
			firstIssuer = append([]byte(nil), issuer...)
		}
		if !authorities.Contains(issuer) {
			continue
		}
		if certificateRequestSupports(request, certificate) {
			return certificate, nil
		}
	}
	if firstIssuer != nil {
		return nil, errors.Join(ErrNoUsableCertificate, &IssuerNotAuthorizedError{
			Issuer:      firstIssuer,
			Authorities: authorities.Clone(),
		})
	}
	return nil, ErrNoUsableCertificate
}

func certificateRequestSupports(request *tls.CertificateRequestInfo, certificate *tls.Certificate) bool {
	if request == nil || len(request.SignatureSchemes) == 0 {
		return true
	}
	return request.SupportsCertificate(certificate) == nil
}

// LeafCertificate returns the parsed leaf of certificate without modifying
// certificate.
func LeafCertificate(certificate *tls.Certificate) (*x509.Certificate, error) {
	return certificateLeaf(certificate)
}
