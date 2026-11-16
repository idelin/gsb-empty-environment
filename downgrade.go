// Package netsec provides TLS downgrade protection for Go's crypto/tls.
//
// The package implements the TLS 1.3 downgrade sentinel rules from RFC 8446,
// Section 4.1.3, and the TLS_FALLBACK_SCSV rule from RFC 7507. Server and
// Client wrap a net.Conn with a crypto/tls connection that enforces those
// rules at the TLS record layer.
package netsec

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
)

const (
	// TLS_FALLBACK_SCSV is the signalling cipher suite value defined by
	// RFC 7507. It is not a real cipher suite.
	TLS_FALLBACK_SCSV uint16 = 0x5600

	// FallbackSCSV is an alias for TLS_FALLBACK_SCSV.
	FallbackSCSV uint16 = TLS_FALLBACK_SCSV

	// TLSFallbackSCSV is an alias for TLS_FALLBACK_SCSV.
	TLSFallbackSCSV uint16 = TLS_FALLBACK_SCSV

	// TLSFALLBACKSCSV is an alias for TLS_FALLBACK_SCSV.
	TLSFALLBACKSCSV uint16 = TLS_FALLBACK_SCSV
)

const (
	// DowngradeSentinelTLS12 is written to the last eight bytes of a
	// ServerHello.random when a TLS 1.3-capable server negotiates TLS 1.2.
	DowngradeSentinelTLS12 = "DOWNGRD\x01"

	// DowngradeSentinelTLS11 is written to the last eight bytes of a
	// ServerHello.random when a TLS 1.3-capable server negotiates TLS 1.1 or
	// lower.
	DowngradeSentinelTLS11 = "DOWNGRD\x00"
)

const (
	// DowngradeCanaryTLS12 is an alias for DowngradeSentinelTLS12.
	DowngradeCanaryTLS12 = DowngradeSentinelTLS12
	// DowngradeCanaryTLS11 is an alias for DowngradeSentinelTLS11.
	DowngradeCanaryTLS11 = DowngradeSentinelTLS11
)

var (
	// SentinelTLS12 is the byte form of DowngradeSentinelTLS12.
	SentinelTLS12 = []byte(DowngradeSentinelTLS12)
	// SentinelTLS11 is the byte form of DowngradeSentinelTLS11.
	SentinelTLS11 = []byte(DowngradeSentinelTLS11)
)

const (
	// AlertIllegalParameter is the TLS alert description illegal_parameter.
	AlertIllegalParameter uint8 = 47
	// AlertInappropriateFallback is the TLS alert description
	// inappropriate_fallback.
	AlertInappropriateFallback uint8 = 86
)

var (
	// ErrDowngradeDetected is returned when a TLS 1.3-capable client receives
	// a ServerHello containing a downgrade sentinel.
	ErrDowngradeDetected = errors.New("tls: downgrade attempt detected, possibly due to a MitM attack or a broken middlebox")

	// ErrInappropriateFallback is returned when a client sends
	// TLS_FALLBACK_SCSV for a version lower than the server supports.
	ErrInappropriateFallback = errors.New("tls: client using inappropriate protocol fallback")

	// ErrIllegalParameter is the alert reason used when a downgrade sentinel
	// is detected. It is an alias for ErrDowngradeDetected.
	ErrIllegalParameter = ErrDowngradeDetected
)

// AlertError reports a fatal TLS alert sent by the local guard.
type AlertError struct {
	Description uint8
	Reason      error
}

func (e *AlertError) Error() string {
	if e.Reason != nil {
		return e.Reason.Error()
	}
	return fmt.Sprintf("tls: sent alert %d", e.Description)
}

func (e *AlertError) Unwrap() error {
	return e.Reason
}

// MaxSupportedVersion returns the highest TLS version supported by cfg.
//
// A zero MaxVersion means the crypto/tls default, which is TLS 1.3. Values
// above TLS 1.3 are capped at TLS 1.3, matching crypto/tls.
func MaxSupportedVersion(cfg *tls.Config) uint16 {
	if cfg == nil {
		return tls.VersionTLS13
	}
	v := cfg.MaxVersion
	if v == 0 || v > tls.VersionTLS13 {
		v = tls.VersionTLS13
	}
	if cfg.MinVersion != 0 && v < cfg.MinVersion {
		return 0
	}
	return v
}

// ServerSentinelFor returns the downgrade sentinel required for the given
// server maximum and negotiated versions, or nil if no sentinel is required.
//
// A server that only supports TLS 1.2 or lower never writes a sentinel.
func ServerSentinelFor(serverMax, negotiated uint16) []byte {
	if serverMax < tls.VersionTLS13 || negotiated >= tls.VersionTLS13 {
		return nil
	}
	if negotiated == tls.VersionTLS12 {
		return []byte(DowngradeSentinelTLS12)
	}
	return []byte(DowngradeSentinelTLS11)
}

// ApplyServerSentinel writes the required downgrade sentinel to the last eight
// bytes of random. It reports whether a sentinel was written.
//
// random is normally the 32-byte ServerHello.random. The last eight bytes are
// used so this also works with callers that pass a larger buffer.
func ApplyServerSentinel(random []byte, serverMax, negotiated uint16) bool {
	if len(random) < 8 {
		return false
	}
	sentinel := ServerSentinelFor(serverMax, negotiated)
	if sentinel == nil {
		return false
	}
	copy(random[len(random)-8:], sentinel)
	return true
}

// ValidateServerRandom checks ServerHello.random from the client's point of
// view. It returns ErrDowngradeDetected if a TLS 1.3-capable client negotiated
// TLS 1.2 or lower and the random ends with either downgrade sentinel.
func ValidateServerRandom(clientMax, negotiated uint16, random []byte) error {
	if clientMax < tls.VersionTLS13 || negotiated > tls.VersionTLS12 {
		return nil
	}
	if len(random) < 8 {
		return nil
	}
	suffix := random[len(random)-8:]
	if bytes.Equal(suffix, []byte(DowngradeSentinelTLS12)) || bytes.Equal(suffix, []byte(DowngradeSentinelTLS11)) {
		return ErrDowngradeDetected
	}
	return nil
}

// HasFallbackSCSV reports whether cipherSuites contains TLS_FALLBACK_SCSV.
func HasFallbackSCSV(cipherSuites []uint16) bool {
	for _, id := range cipherSuites {
		if id == TLS_FALLBACK_SCSV {
			return true
		}
	}
	return false
}

// ContainsFallbackSCSV is an alias for HasFallbackSCSV.
func ContainsFallbackSCSV(cipherSuites []uint16) bool {
	return HasFallbackSCSV(cipherSuites)
}

// AddFallbackSCSV returns cipherSuites with TLS_FALLBACK_SCSV appended if it
// is not already present. It does not modify the input slice.
func AddFallbackSCSV(cipherSuites []uint16) []uint16 {
	if HasFallbackSCSV(cipherSuites) {
		return cipherSuites
	}
	out := make([]uint16, len(cipherSuites)+1)
	copy(out, cipherSuites)
	out[len(cipherSuites)] = TLS_FALLBACK_SCSV
	return out
}

// WithoutFallbackSCSV returns cipherSuites without TLS_FALLBACK_SCSV. If the
// result would be empty, it returns nil so crypto/tls uses its defaults.
func WithoutFallbackSCSV(cipherSuites []uint16) []uint16 {
	return withoutFallbackSCSV(cipherSuites)
}

// ValidateFallbackSCSV enforces RFC 7507. If the ClientHello contains
// TLS_FALLBACK_SCSV and its legacy_version is lower than the server's maximum
// supported version, the server must abort with inappropriate_fallback.
func ValidateFallbackSCSV(legacyVersion, serverMax uint16, cipherSuites []uint16) error {
	if HasFallbackSCSV(cipherSuites) && legacyVersion < serverMax {
		return ErrInappropriateFallback
	}
	return nil
}

// CheckServerRandom is an alias for ValidateServerRandom.
func CheckServerRandom(clientMax, negotiated uint16, random []byte) error {
	return ValidateServerRandom(clientMax, negotiated, random)
}

// CheckFallbackSCSV is an alias for ValidateFallbackSCSV.
func CheckFallbackSCSV(legacyVersion, serverMax uint16, cipherSuites []uint16) error {
	return ValidateFallbackSCSV(legacyVersion, serverMax, cipherSuites)
}

// WriteServerSentinel is an alias for ApplyServerSentinel.
func WriteServerSentinel(random []byte, serverMax, negotiated uint16) bool {
	return ApplyServerSentinel(random, serverMax, negotiated)
}

// EffectiveMaxVersion is an alias for MaxSupportedVersion.
func EffectiveMaxVersion(cfg *tls.Config) uint16 {
	return MaxSupportedVersion(cfg)
}
