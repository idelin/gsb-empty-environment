package netsec

import (
	"crypto/tls"
	"errors"
	"fmt"
)

const (
	// TLS12DowngradeSentinel is the final eight bytes of ServerHello.random
	// when a TLS 1.3-capable server negotiates TLS 1.2.
	TLS12DowngradeSentinel = "DOWNGRD\x01"

	// TLS11DowngradeSentinel is the final eight bytes of ServerHello.random
	// when a TLS 1.3-capable server negotiates TLS 1.1 or lower.
	TLS11DowngradeSentinel = "DOWNGRD\x00"

	// TLSFallbackSCSV is the signalling cipher-suite value from RFC 7507.
	TLSFallbackSCSV uint16 = 0x5600

	// AlertIllegalParameter is the TLS alert sent when a client detects a
	// ServerHello downgrade sentinel.
	AlertIllegalParameter uint8 = 47

	// AlertInappropriateFallback is the TLS alert sent when a server sees
	// TLS_FALLBACK_SCSV from a client that is falling back below the server's
	// maximum supported version.
	AlertInappropriateFallback uint8 = 86
)

// Compatibility aliases for callers that use the RFC/stdlib spelling.
const (
	TLS12Sentinel      = TLS12DowngradeSentinel
	TLS11Sentinel      = TLS11DowngradeSentinel
	TLS_FALLBACK_SCSV  = TLSFallbackSCSV
	AlertIllegalParam  = AlertIllegalParameter
	AlertInappropriate = AlertInappropriateFallback
)

var (
	// ErrDowngradeDetected reports a ServerHello canary while the client
	// supports TLS 1.3 and negotiated TLS 1.2 or lower.
	ErrDowngradeDetected = errors.New("tls: downgrade attempt detected, possibly due to a MitM attack or a broken middlebox (illegal_parameter)")

	// ErrInappropriateFallback reports a ClientHello that carries
	// TLS_FALLBACK_SCSV below the server's maximum supported version.
	ErrInappropriateFallback = errors.New("tls: client using inappropriate protocol fallback (inappropriate_fallback)")

	// ErrShortServerRandom reports a ServerHello.random shorter than the
	// eight-byte sentinel field.
	ErrShortServerRandom = errors.New("tls: ServerHello.random is too short for a downgrade sentinel")
)

// AlertError is returned when this package terminates a handshake with an
// explicit TLS alert before handing the message to crypto/tls.
type AlertError struct {
	Alert uint8
	Err   error
}

func (e *AlertError) Error() string {
	if e == nil || e.Err == nil {
		return fmt.Sprintf("tls: alert %d", e.Alert)
	}
	return e.Err.Error()
}

func (e *AlertError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// DowngradeSentinel returns the ServerHello.random suffix required for the
// supplied server maximum and negotiated versions. It returns nil when the
// server maximum is TLS 1.2 or lower, or when no downgrade occurred.
func DowngradeSentinel(serverMaxVersion, negotiatedVersion uint16) []byte {
	if serverMaxVersion < tls.VersionTLS13 || negotiatedVersion > tls.VersionTLS12 {
		return nil
	}
	if negotiatedVersion == tls.VersionTLS12 {
		return []byte(TLS12DowngradeSentinel)
	}
	return []byte(TLS11DowngradeSentinel)
}

// SentinelForVersion is an alias for DowngradeSentinel.
func SentinelForVersion(serverMaxVersion, negotiatedVersion uint16) []byte {
	return DowngradeSentinel(serverMaxVersion, negotiatedVersion)
}

// ApplyServerRandomSentinel applies the RFC 8446 downgrade sentinel to the
// final eight bytes of random. random must contain at least eight bytes. The
// function leaves random unchanged when no sentinel is required.
func ApplyServerRandomSentinel(random []byte, serverMaxVersion, negotiatedVersion uint16) error {
	sentinel := DowngradeSentinel(serverMaxVersion, negotiatedVersion)
	if sentinel == nil {
		return nil
	}
	if len(random) < len(sentinel) {
		return ErrShortServerRandom
	}
	copy(random[len(random)-len(sentinel):], sentinel)
	return nil
}

// CheckServerRandom implements the client-side sentinel check. A client that
// itself supports TLS 1.3 and negotiated TLS 1.2 or lower rejects either
// sentinel with illegal_parameter.
func CheckServerRandom(clientMaxVersion, negotiatedVersion uint16, random []byte) error {
	if clientMaxVersion < tls.VersionTLS13 || negotiatedVersion > tls.VersionTLS12 {
		return nil
	}
	if len(random) < 8 {
		return ErrShortServerRandom
	}
	suffix := random[len(random)-8:]
	if string(suffix) == TLS12DowngradeSentinel || string(suffix) == TLS11DowngradeSentinel {
		return &AlertError{Alert: AlertIllegalParameter, Err: ErrDowngradeDetected}
	}
	return nil
}

// HasFallbackSCSV reports whether suites contains TLS_FALLBACK_SCSV.
func HasFallbackSCSV(suites []uint16) bool {
	for _, suite := range suites {
		if suite == TLSFallbackSCSV {
			return true
		}
	}
	return false
}

// CheckFallbackSCSV validates RFC 7507 fallback signalling. A ClientHello
// that contains TLS_FALLBACK_SCSV while its legacy_version is below the
// server's maximum supported version must be rejected with
// inappropriate_fallback.
func CheckFallbackSCSV(serverMaxVersion, clientLegacyVersion uint16, suites []uint16) error {
	if clientLegacyVersion < serverMaxVersion && HasFallbackSCSV(suites) {
		return &AlertError{Alert: AlertInappropriateFallback, Err: ErrInappropriateFallback}
	}
	return nil
}

// ClientCipherSuites returns a copy of suites with TLS_FALLBACK_SCSV added for
// a client-initiated downgrade retry, or removed for a first handshake. A nil
// base means the caller is using crypto/tls defaults; in that case nil is
// returned and the package adds TLS_FALLBACK_SCSV at the wire layer instead.
func ClientCipherSuites(base []uint16, fallbackRetry bool) []uint16 {
	if base == nil {
		return nil
	}
	out := append([]uint16(nil), base...)
	if fallbackRetry {
		if !HasFallbackSCSV(out) {
			out = append(out, TLSFallbackSCSV)
		}
		return out
	}
	return removeFallbackSCSV(out)
}

func removeFallbackSCSV(suites []uint16) []uint16 {
	out := suites[:0]
	for _, suite := range suites {
		if suite != TLSFallbackSCSV {
			out = append(out, suite)
		}
	}
	return out
}

func maxVersion(cfg *tls.Config) uint16 {
	if cfg != nil && cfg.MaxVersion != 0 {
		return cfg.MaxVersion
	}
	return tls.VersionTLS13
}

func minVersion(cfg *tls.Config) uint16 {
	if cfg != nil && cfg.MinVersion != 0 {
		return cfg.MinVersion
	}
	return tls.VersionTLS12
}
