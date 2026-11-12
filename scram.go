package netsec

import (
	"crypto"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const (
	// MechanismSCRAMSHA256 is the SCRAM mechanism without channel binding.
	MechanismSCRAMSHA256 = "SCRAM-SHA-256"
	// MechanismSCRAMSHA256PLUS is the SCRAM mechanism with channel binding.
	MechanismSCRAMSHA256PLUS = "SCRAM-SHA-256-PLUS"

	// GS2FlagNoChannelBinding means that the client does not support channel
	// binding.
	GS2FlagNoChannelBinding = "n"
	// GS2FlagChannelBindingSupported means that the client supports channel
	// binding, but believes that the server does not.
	GS2FlagChannelBindingSupported = "y"
	// GS2FlagChannelBindingRequired means that the client requires channel
	// binding of the type following "p=".
	GS2FlagChannelBindingRequired = "p"

	// ChannelBindingTLSServerEndPoint is the RFC 5929 tls-server-end-point
	// channel binding type.
	ChannelBindingTLSServerEndPoint = "tls-server-end-point"
	// ChannelBindingTLSExporter is the RFC 9266 tls-exporter channel binding
	// type.
	ChannelBindingTLSExporter = "tls-exporter"
)

var (
	// ErrMalformedGS2Header reports an invalid GS2 header.
	ErrMalformedGS2Header = errors.New("scram: malformed GS2 header")
	// ErrMalformedFinalMessage reports a final message without exactly one
	// well-formed c= attribute.
	ErrMalformedFinalMessage = errors.New("scram: malformed client final message")
	// ErrUnsupportedChannelBindingType reports a p= type that the server does
	// not support.
	ErrUnsupportedChannelBindingType = errors.New("scram: unsupported channel binding type")
	// ErrChannelBindingDowngrade reports the SCRAM channel binding downgrade
	// signaled by y after the server advertised a -PLUS mechanism.
	ErrChannelBindingDowngrade = errors.New("scram: channel binding downgrade")
	// ErrMechanismMismatch reports an incompatible mechanism and GS2 flag.
	ErrMechanismMismatch = errors.New("scram: mechanism and GS2 channel binding flag mismatch")
	// ErrChannelBindingMismatch reports a c= value that does not match the
	// GS2 header and the expected channel binding data.
	ErrChannelBindingMismatch = errors.New("scram: channel binding data mismatch")
)

// GS2Header is a parsed GS2 header from a SCRAM client-first message.
//
// Raw is the exact header prefix through its second comma. It is the value
// that must precede channel binding data in the client's final c= attribute.
type GS2Header struct {
	Raw                string
	Flag               string
	ChannelBindingType string
	AuthzID            string
}

// RequiresChannelBinding reports whether the GS2 header uses p=.
func (h GS2Header) RequiresChannelBinding() bool {
	return h.Flag == GS2FlagChannelBindingRequired
}

// ParseGS2Header parses the GS2 header at the beginning of a SCRAM
// client-first message. Any bytes after the header's second comma (the
// client-first-message-bare) are ignored.
func ParseGS2Header(message string) (GS2Header, error) {
	firstComma := strings.IndexByte(message, ',')
	if firstComma < 0 {
		return GS2Header{}, fmt.Errorf("%w: missing channel binding flag terminator", ErrMalformedGS2Header)
	}

	flag := message[:firstComma]
	rest := message[firstComma+1:]
	secondComma := strings.IndexByte(rest, ',')
	if secondComma < 0 {
		return GS2Header{}, fmt.Errorf("%w: missing authzid terminator", ErrMalformedGS2Header)
	}

	header := GS2Header{
		Raw: message[:firstComma+1+secondComma+1],
	}

	switch {
	case flag == GS2FlagNoChannelBinding:
		header.Flag = GS2FlagNoChannelBinding
	case flag == GS2FlagChannelBindingSupported:
		header.Flag = GS2FlagChannelBindingSupported
	case strings.HasPrefix(flag, GS2FlagChannelBindingRequired+"=") &&
		len(flag) > len(GS2FlagChannelBindingRequired)+1:
		header.Flag = GS2FlagChannelBindingRequired
		header.ChannelBindingType = flag[len(GS2FlagChannelBindingRequired)+1:]
	default:
		return GS2Header{}, fmt.Errorf("%w: invalid channel binding flag %q", ErrMalformedGS2Header, flag)
	}

	if authzID := rest[:secondComma]; authzID != "" {
		if !strings.HasPrefix(authzID, "a=") {
			return GS2Header{}, fmt.Errorf("%w: invalid authzid", ErrMalformedGS2Header)
		}
		header.AuthzID = strings.TrimPrefix(authzID, "a=")
	}

	return header, nil
}

// ChannelBinding is the channel binding state selected while processing the
// client-first message. BindingData must be supplied by the caller for the
// selected type: the certificate hash for tls-server-end-point, or the TLS
// exporter output for tls-exporter. It is empty for n and y.
type ChannelBinding struct {
	GS2Header          string
	ChannelBindingType string
	BindingData        []byte

	supportedTypes []string
}

// WithBindingData returns a copy of the channel binding with data attached.
func (b ChannelBinding) WithBindingData(data []byte) ChannelBinding {
	b.BindingData = append([]byte(nil), data...)
	return b
}

// Input returns the exact bytes that the client must place in c= (before
// base64 encoding): the GS2 header followed by the binding data.
func (b ChannelBinding) Input() []byte {
	input := make([]byte, 0, len(b.GS2Header)+len(b.BindingData))
	input = append(input, b.GS2Header...)
	input = append(input, b.BindingData...)
	return input
}

// EncodeC returns the base64 representation expected in the client's c=
// attribute.
func (b ChannelBinding) EncodeC() string {
	return base64.StdEncoding.EncodeToString(b.Input())
}

// VerifyFinalMessage checks the client-final-message's c= attribute against
// this channel binding.
func (b ChannelBinding) VerifyFinalMessage(finalMessage string) error {
	supported := b.supportedTypes
	if supported == nil {
		supported = DefaultChannelBindingTypes()
	}
	return verifyFinalMessage(finalMessage, b.GS2Header, b.BindingData, supported)
}

// DefaultChannelBindingTypes returns the channel binding types supported by
// this package.
func DefaultChannelBindingTypes() []string {
	return []string{
		ChannelBindingTLSServerEndPoint,
		ChannelBindingTLSExporter,
	}
}

// FirstMessagePolicy contains the server-side choices needed to evaluate a
// SCRAM client-first message.
type FirstMessagePolicy struct {
	// AdvertisedMechanisms is the mechanism list sent to the client.
	AdvertisedMechanisms []string
	// SupportedChannelBindingTypes is the p= type allowlist. If nil, the
	// package defaults to tls-server-end-point and tls-exporter. An explicitly
	// empty slice permits no p= binding type.
	SupportedChannelBindingTypes []string
}

// Evaluate checks a mechanism and client-first message against the policy.
func (p FirstMessagePolicy) Evaluate(mechanism, clientFirstMessage string) (ChannelBinding, error) {
	gs2, err := ParseGS2Header(clientFirstMessage)
	if err != nil {
		return ChannelBinding{}, err
	}

	supported := p.SupportedChannelBindingTypes
	if supported == nil {
		supported = DefaultChannelBindingTypes()
	}

	plus := strings.HasSuffix(mechanism, "-PLUS")
	advertisedPLUS := advertisesPLUS(p.AdvertisedMechanisms)
	binding := ChannelBinding{
		GS2Header:          gs2.Raw,
		ChannelBindingType: gs2.ChannelBindingType,
		supportedTypes:     append([]string(nil), supported...),
	}

	switch gs2.Flag {
	case GS2FlagNoChannelBinding:
		if plus {
			return ChannelBinding{}, fmt.Errorf("%w: -PLUS mechanism requires p=, got n", ErrMechanismMismatch)
		}
		binding.ChannelBindingType = ""
		return binding, nil

	case GS2FlagChannelBindingSupported:
		if plus {
			return ChannelBinding{}, fmt.Errorf("%w: -PLUS mechanism requires p=, got y", ErrMechanismMismatch)
		}
		if advertisedPLUS {
			return ChannelBinding{}, fmt.Errorf("%w: server advertised -PLUS but client sent y", ErrChannelBindingDowngrade)
		}
		binding.ChannelBindingType = ""
		return binding, nil

	case GS2FlagChannelBindingRequired:
		if !plus {
			return ChannelBinding{}, fmt.Errorf("%w: non-PLUS mechanism cannot use p=", ErrMechanismMismatch)
		}
		if !containsString(supported, gs2.ChannelBindingType) {
			return ChannelBinding{}, fmt.Errorf("%w: %q", ErrUnsupportedChannelBindingType, gs2.ChannelBindingType)
		}
		return binding, nil
	}

	return ChannelBinding{}, ErrMalformedGS2Header
}

// EvaluateFirstMessage evaluates a client-first message. The optional
// supportedChannelBindingTypes argument overrides the default allowlist
// (tls-server-end-point and tls-exporter).
func EvaluateFirstMessage(mechanism, clientFirstMessage string, advertisedMechanisms []string, supportedChannelBindingTypes ...[]string) (ChannelBinding, error) {
	policy := FirstMessagePolicy{AdvertisedMechanisms: advertisedMechanisms}
	if len(supportedChannelBindingTypes) > 0 {
		policy.SupportedChannelBindingTypes = supportedChannelBindingTypes[0]
	}
	return policy.Evaluate(mechanism, clientFirstMessage)
}

// EvaluateFirstMessageWithTypes is EvaluateFirstMessage with a custom p= type
// allowlist.
func EvaluateFirstMessageWithTypes(mechanism, clientFirstMessage string, advertisedMechanisms, supportedChannelBindingTypes []string) (ChannelBinding, error) {
	return FirstMessagePolicy{
		AdvertisedMechanisms:         advertisedMechanisms,
		SupportedChannelBindingTypes: supportedChannelBindingTypes,
	}.Evaluate(mechanism, clientFirstMessage)
}

// ValidateFirstMessage is an alias for EvaluateFirstMessage.
func ValidateFirstMessage(mechanism, clientFirstMessage string, advertisedMechanisms []string, supportedChannelBindingTypes ...[]string) (ChannelBinding, error) {
	return EvaluateFirstMessage(mechanism, clientFirstMessage, advertisedMechanisms, supportedChannelBindingTypes...)
}

// VerifyFinalMessage checks that the client-final-message's c= attribute
// decodes to gs2Header followed by bindingData. gs2Header must be the exact
// header returned by ParseGS2Header or FirstMessagePolicy.Evaluate.
func VerifyFinalMessage(finalMessage, gs2Header string, bindingData []byte) error {
	return verifyFinalMessage(finalMessage, gs2Header, bindingData, DefaultChannelBindingTypes())
}

func verifyFinalMessage(finalMessage, gs2Header string, bindingData []byte, supportedTypes []string) error {
	header, err := ParseGS2Header(gs2Header)
	if err != nil {
		return err
	}
	if header.Raw != gs2Header {
		return fmt.Errorf("%w: GS2 header includes data beyond the header", ErrMalformedGS2Header)
	}
	if !header.RequiresChannelBinding() && len(bindingData) != 0 {
		return fmt.Errorf("%w: binding data supplied for %q", ErrMalformedGS2Header, header.Flag)
	}
	if header.RequiresChannelBinding() && !containsString(supportedTypes, header.ChannelBindingType) {
		return fmt.Errorf("%w: %q", ErrUnsupportedChannelBindingType, header.ChannelBindingType)
	}

	decoded, err := parseChannelBindingValue(finalMessage)
	if err != nil {
		return err
	}

	expected := make([]byte, 0, len(gs2Header)+len(bindingData))
	expected = append(expected, gs2Header...)
	expected = append(expected, bindingData...)

	if len(decoded) != len(expected) || subtle.ConstantTimeCompare(decoded, expected) != 1 {
		return ErrChannelBindingMismatch
	}
	return nil
}

// ValidateFinalMessage is an alias for VerifyFinalMessage.
func ValidateFinalMessage(finalMessage, gs2Header string, bindingData []byte) error {
	return VerifyFinalMessage(finalMessage, gs2Header, bindingData)
}

func parseChannelBindingValue(finalMessage string) ([]byte, error) {
	attributes := make(map[string]string)
	for _, attribute := range strings.Split(finalMessage, ",") {
		if len(attribute) < 2 || attribute[1] != '=' {
			return nil, fmt.Errorf("%w: invalid attribute %q", ErrMalformedFinalMessage, attribute)
		}
		name := attribute[:1]
		if _, duplicate := attributes[name]; duplicate {
			return nil, fmt.Errorf("%w: duplicate attribute %q", ErrMalformedFinalMessage, name)
		}
		attributes[name] = attribute[2:]
	}

	value, ok := attributes["c"]
	if !ok {
		return nil, fmt.Errorf("%w: missing c attribute", ErrMalformedFinalMessage)
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(value)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid c base64", ErrMalformedFinalMessage)
		}
	}
	return decoded, nil
}

// CertificateHashAlgorithm returns the hash algorithm used for RFC 5929
// tls-server-end-point binding. MD5 and SHA-1 signatures are upgraded to
// SHA-256. Algorithms without a supported certificate hash use SHA-256.
func CertificateHashAlgorithm(cert *x509.Certificate) crypto.Hash {
	if cert == nil {
		return crypto.SHA256
	}
	return SignatureAlgorithmHash(cert.SignatureAlgorithm)
}

// SignatureAlgorithmHash returns the tls-server-end-point hash for a
// certificate signature algorithm, upgrading MD5 and SHA-1 to SHA-256.
func SignatureAlgorithmHash(algorithm x509.SignatureAlgorithm) crypto.Hash {
	switch algorithm {
	case x509.SHA384WithRSA,
		x509.ECDSAWithSHA384,
		x509.SHA384WithRSAPSS:
		return crypto.SHA384
	case x509.SHA512WithRSA,
		x509.ECDSAWithSHA512,
		x509.SHA512WithRSAPSS:
		return crypto.SHA512
	default:
		// This includes MD5WithRSA, SHA1WithRSA, DSAWithSHA1,
		// ECDSAWithSHA1, SHA-256 signatures, Ed25519, and unknown
		// signature algorithms.
		return crypto.SHA256
	}
}

// CertificateHash returns the DER-encoded certificate hash used as
// tls-server-end-point channel binding data.
func CertificateHash(cert *x509.Certificate) ([]byte, error) {
	if cert == nil {
		return nil, errors.New("scram: nil certificate")
	}
	if len(cert.Raw) == 0 {
		return nil, errors.New("scram: certificate has no DER encoding")
	}

	switch CertificateHashAlgorithm(cert) {
	case crypto.SHA384:
		sum := sha512.Sum384(cert.Raw)
		return sum[:], nil
	case crypto.SHA512:
		sum := sha512.Sum512(cert.Raw)
		return sum[:], nil
	default:
		sum := sha256.Sum256(cert.Raw)
		return sum[:], nil
	}
}

// TLSServerEndPointBindingData returns the channel binding data for a
// certificate. It hashes the certificate's DER bytes using the hash from the
// certificate's signature algorithm, upgrading MD5 and SHA-1 to SHA-256.
func TLSServerEndPointBindingData(cert *x509.Certificate) ([]byte, error) {
	return CertificateHash(cert)
}

// CertificateChannelBindingData is an alias for
// TLSServerEndPointBindingData.
func CertificateChannelBindingData(cert *x509.Certificate) ([]byte, error) {
	return TLSServerEndPointBindingData(cert)
}

// TLSServerEndPointBindingDataFromDER parses DER certificate bytes and
// returns their tls-server-end-point channel binding data.
func TLSServerEndPointBindingDataFromDER(der []byte) ([]byte, error) {
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("scram: parse certificate: %w", err)
	}
	return TLSServerEndPointBindingData(cert)
}

func advertisesPLUS(mechanisms []string) bool {
	for _, mechanism := range mechanisms {
		if strings.HasSuffix(mechanism, "-PLUS") {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
