package netsec

import (
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// Certificate usage values defined by RFC 6698.  This package implements the
// two domain-issued usages: DANE-TA and DANE-EE.
const (
	UsagePKIXTA = 0
	UsagePKIXEE = 1
	UsageDANETA = 2
	UsageDANEEE = 3

	CertUsagePKIXTA = UsagePKIXTA
	CertUsagePKIXEE = UsagePKIXEE
	CertUsageDANETA = UsageDANETA
	CertUsageDANEEE = UsageDANEEE
	TLSAUsagePKIXTA = UsagePKIXTA
	TLSAUsagePKIXEE = UsagePKIXEE
	TLSAUsageDANETA = UsageDANETA
	TLSAUsageDANEEE = UsageDANEEE

	PKIXTA = UsagePKIXTA
	PKIXEE = UsagePKIXEE
	DANETA = UsageDANETA
	DANEEE = UsageDANEEE
)

// TLSA selector values defined by RFC 6698.
const (
	SelectorFullCert             = 0
	SelectorFull                 = SelectorFullCert
	SelectorCert                 = SelectorFullCert
	SelectorSPKI                 = 1
	SelectorSubjectPublicKeyInfo = SelectorSPKI
	TLSASelectorFull             = SelectorFullCert
	TLSASelectorSPKI             = SelectorSPKI

	FullCert = SelectorFullCert
	SPKI     = SelectorSPKI
)

// TLSA matching type values defined by RFC 6698.
const (
	MatchingTypeFull   = 0
	MatchingTypeSHA256 = 1
	MatchingTypeSHA512 = 2

	MatchingFull    = MatchingTypeFull
	MatchingExact   = MatchingTypeFull
	MatchingSHA256  = MatchingTypeSHA256
	MatchingSHA512  = MatchingTypeSHA512
	TLSAMatchFull   = MatchingTypeFull
	TLSAMatchSHA256 = MatchingTypeSHA256
	TLSAMatchSHA512 = MatchingTypeSHA512

	MatchFull       = MatchingTypeFull
	MatchSHA256     = MatchingTypeSHA256
	MatchSHA512     = MatchingTypeSHA512
	MatchTypeFull   = MatchingTypeFull
	MatchTypeSHA256 = MatchingTypeSHA256
	MatchTypeSHA512 = MatchingTypeSHA512
)

// Usage and selector are aliases for callers that prefer named field types.
// The values themselves remain one-octet integers on the wire.
type Usage uint8
type CertUsage = Usage
type TLSAUsage = Usage
type Selector uint8
type TLSASelector = Selector
type MatchingType uint8
type MatchType = MatchingType
type TLSAMatchType = MatchingType

// Errors returned by this package.  Match failures wrap ErrNoMatch, so callers
// can distinguish "record did not match" from a malformed or unsupported
// record.
var (
	ErrNoPeerCertificate    = errors.New("dane: no peer certificate")
	ErrNoChain              = errors.New("dane: empty certificate chain")
	ErrUnsupportedUsage     = errors.New("dane: unsupported certificate usage")
	ErrUnsupportedSelector  = errors.New("dane: unsupported selector")
	ErrUnsupportedMatchType = errors.New("dane: unsupported matching type")
	ErrMalformedRecord      = errors.New("dane: malformed TLSA record")
	ErrNoMatch              = errors.New("dane: TLSA association does not match")

	// Common short aliases.
	ErrNoCertificate = ErrNoPeerCertificate
	ErrMismatch      = ErrNoMatch
)

// TLSA is a DNSSEC-validated TLSA record.  Usage, Selector and MatchingType
// are the three one-octet fields of the record; Data is the certificate
// association data, already decoded from its DNS wire/hex representation.
//
// Certificate and CertAssoc are accepted as aliases for Data so records from
// common DNS libraries can be used without conversion.  If several data fields
// are populated they must contain identical bytes.
type TLSA struct {
	Usage        uint8
	Selector     uint8
	MatchingType uint8
	Data         []byte

	Certificate     any
	Cert            []byte
	CertUsage       uint8
	MatchType       uint8
	CertAssoc       any
	AssociationData []byte
}

// Record is an alias for TLSA.
type Record = TLSA

// Match reports whether the selected part of cert matches this record.
func (r TLSA) Match(cert *x509.Certificate) bool {
	return Match(cert, r)
}

// Verify validates this record against loose target arguments.
func (r TLSA) Verify(args ...any) error {
	all := make([]any, 0, len(args)+1)
	all = append(all, r)
	all = append(all, args...)
	return Verify(all...)
}

// Validate is an alias for TLSA.Verify.
func (r TLSA) Validate(args ...any) error { return r.Verify(args...) }

// VerifyConnection validates this record against a TLS connection state.
func (r TLSA) VerifyConnection(state *tls.ConnectionState) error {
	return VerifyConnection(state, r)
}

// VerifyCertificate validates this record against an end-entity certificate.
func (r TLSA) VerifyCertificate(cert *x509.Certificate) error {
	return VerifyCertificate(cert, r)
}

// VerifyChain validates this record against a server-presented chain.
func (r TLSA) VerifyChain(chain []*x509.Certificate) error {
	return VerifyChain(chain, r)
}

// NewTLSA builds a record from raw association data.  The data must not be a
// textual hex representation; use ParseTLSA for DNS presentation format.
func NewTLSA(usage, selector, matchingType uint8, data []byte) TLSA {
	return TLSA{
		Usage:        usage,
		Selector:     selector,
		MatchingType: matchingType,
		Data:         append([]byte(nil), data...),
	}
}

// ParseTLSA decodes a TLSA record whose association data is encoded as
// hexadecimal, as it is in DNS presentation format.  Whitespace and colons
// between hex digits are ignored.
func ParseTLSA(usage, selector, matchingType uint8, hexData string) (TLSA, error) {
	data, err := decodeHex(hexData)
	if err != nil {
		return TLSA{}, err
	}
	return NewTLSA(usage, selector, matchingType, data), nil
}

// VerifyConnection validates one or more records against a completed TLS
// handshake.  DANE-EE uses state.PeerCertificates[0] and deliberately does not
// perform hostname or validity-period checks.  DANE-TA searches the verified
// chains, when present, otherwise the certificate chain presented by the peer.
func VerifyConnection(state *tls.ConnectionState, records ...TLSA) error {
	if state == nil {
		return fmt.Errorf("%w: nil TLS connection state", ErrMalformedRecord)
	}
	target := targetFromState(state)
	if len(records) == 0 {
		return fmt.Errorf("%w: no TLSA records", ErrMalformedRecord)
	}
	return verifyRecords(records, target)
}

// VerifyCertificate validates a record against an end-entity certificate.  It
// is useful for tests and for callers that already extracted the leaf from a
// handshake.  DANE-EE matches the supplied certificate.  For DANE-TA the
// supplied certificate is treated as a one-element chain.
func VerifyCertificate(cert *x509.Certificate, records ...TLSA) error {
	if cert == nil {
		return ErrNoPeerCertificate
	}
	return verifyRecords(records, targetSet{
		leaf: cert,
		chains: []certChain{{
			certs: []*x509.Certificate{cert},
		}},
	})
}

// VerifyChain validates a record against a server-presented certificate chain.
// The end-entity certificate must be first.  DANE-EE matches only chain[0];
// DANE-TA requires a matching certificate anywhere in the chain.
func VerifyChain(chain []*x509.Certificate, records ...TLSA) error {
	if len(chain) == 0 {
		return ErrNoChain
	}
	return verifyRecords(records, targetSet{
		leaf: chain[0],
		chains: []certChain{{
			certs: chain,
		}},
	})
}

// VerifyRecord is a convenience wrapper for a single record and chain.
func VerifyRecord(record TLSA, chain []*x509.Certificate) error {
	return VerifyChain(chain, record)
}

// Match reports whether the selected part of cert equals the association data
// under the record's selector and matching type.  It does not consider usage.
// An unsupported or malformed record returns false.
func Match(cert *x509.Certificate, record TLSA) bool {
	rec, err := normalizeRecord(record)
	if err != nil {
		return false
	}
	return MatchesCertificate(cert, rec.Selector, rec.MatchingType, rec.Data)
}

// MatchesCertificate reports whether cert matches data under selector and
// matchingType.  It returns false for unsupported values and malformed input.
func MatchesCertificate(cert *x509.Certificate, selector, matchingType uint8, data []byte) bool {
	matched, err := match(cert, selector, matchingType, data)
	return err == nil && matched
}

// MatchRecord reports whether cert matches the selected part of record.
func MatchRecord(cert *x509.Certificate, record TLSA) bool {
	return Match(cert, record)
}

// MatchesRecord is an alias for MatchRecord.
func MatchesRecord(cert *x509.Certificate, record TLSA) bool {
	return Match(cert, record)
}

// VerifyDANEEE validates a DANE-EE association.  It accepts a TLS connection
// state, an end-entity certificate, a certificate chain, or a TLSA record in
// any of the usual argument orders.  DANE-EE performs no hostname or
// certificate-validity checks; it only checks the TLSA association against the
// peer's end-entity certificate.
func VerifyDANEEE(args ...any) error {
	return verifyFlexible(UsageDANEEE, args...)
}

// VerifyDANETA validates a DANE-TA association.  A matching certificate must
// occur in the verified chain (or, when VerifiedChains is unavailable, the
// chain presented during the handshake).  The end-entity certificate is not
// required to match for DANE-TA.
func VerifyDANETA(args ...any) error {
	return verifyFlexible(UsageDANETA, args...)
}

// ValidateDANEEE is an alias for VerifyDANEEE.
func ValidateDANEEE(args ...any) error { return VerifyDANEEE(args...) }

// ValidateDANETA is an alias for VerifyDANETA.
func ValidateDANETA(args ...any) error { return VerifyDANETA(args...) }

// CheckDANEEE is an alias for VerifyDANEEE.
func CheckDANEEE(args ...any) error { return VerifyDANEEE(args...) }

// CheckDANETA is an alias for VerifyDANETA.
func CheckDANETA(args ...any) error { return VerifyDANETA(args...) }

// Verify validates records using their Usage field, or a record assembled from
// loose arguments.  It is an order-independent convenience API over
// VerifyConnection, VerifyCertificate and VerifyChain.
func Verify(args ...any) error {
	return verifyFlexible(-1, args...)
}

// VerifyTLSA is an alias for Verify.
func VerifyTLSA(args ...any) error { return Verify(args...) }

// VerifyDANE is an alias for Verify.
func VerifyDANE(args ...any) error { return Verify(args...) }

// Validate is an alias for Verify.
func Validate(args ...any) error { return Verify(args...) }

// Check is an alias for Verify.
func Check(args ...any) error { return Verify(args...) }

func verifyFlexible(fixedUsage int, args ...any) error {
	parsed, err := parseArguments(args)
	if err != nil {
		return err
	}
	if len(parsed.records) == 0 {
		record, err := recordFromParts(fixedUsage, parsed.numbers, parsed.data)
		if err != nil {
			return err
		}
		parsed.records = []TLSA{record}
	} else {
		if len(parsed.data) > 0 || len(parsed.numbers) > 0 {
			return fmt.Errorf("%w: record supplied together with loose fields", ErrMalformedRecord)
		}
	}
	if len(parsed.records) == 0 {
		return fmt.Errorf("%w: no TLSA records", ErrMalformedRecord)
	}
	target, err := parsed.target.build()
	if err != nil {
		return err
	}
	if fixedUsage >= 0 {
		records := make([]TLSA, 0, len(parsed.records))
		for _, rec := range parsed.records {
			rec, err = normalizeRecord(rec)
			if err != nil {
				return err
			}
			if rec.Usage == 0 {
				rec.Usage = uint8(fixedUsage)
			}
			if int(rec.Usage) != fixedUsage {
				continue
			}
			records = append(records, rec)
		}
		if len(records) == 0 {
			return fmt.Errorf("%w: %d", ErrUnsupportedUsage, fixedUsage)
		}
		parsed.records = records
	}
	return verifyRecords(parsed.records, target)
}

func verifyRecords(records []TLSA, target targetSet) error {
	if len(records) == 0 {
		return fmt.Errorf("%w: no TLSA records", ErrMalformedRecord)
	}
	var firstErr error
	for _, record := range records {
		rec, err := normalizeRecord(record)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := verifyOne(rec, target); err != nil {
			if !errors.Is(err, ErrNoMatch) && firstErr == nil {
				firstErr = err
			}
			continue
		}
		return nil
	}
	if firstErr != nil {
		return firstErr
	}
	return ErrNoMatch
}

func verifyOne(record TLSA, target targetSet) error {
	switch record.Usage {
	case UsageDANEEE:
		if target.leaf == nil {
			return ErrNoPeerCertificate
		}
		matched, err := match(target.leaf, record.Selector, record.MatchingType, recordData(record))
		if err != nil {
			return err
		}
		if !matched {
			return fmt.Errorf("%w: DANE-EE leaf", ErrNoMatch)
		}
		return nil
	case UsageDANETA:
		chains := target.allChains()
		if len(chains) == 0 {
			return ErrNoChain
		}
		for _, chain := range chains {
			for i, cert := range chain.certs {
				matched, err := match(cert, record.Selector, record.MatchingType, recordData(record))
				if err != nil {
					return err
				}
				if matched {
					if chain.verified {
						return nil
					}
					if err := verifyPresentedPath(chain.certs, i); err != nil {
						continue
					}
					return nil
				}
			}
		}
		return fmt.Errorf("%w: DANE-TA chain", ErrNoMatch)
	default:
		return fmt.Errorf("%w: %d", ErrUnsupportedUsage, record.Usage)
	}
}

func match(cert *x509.Certificate, selector, matchingType uint8, data []byte) (bool, error) {
	if cert == nil {
		return false, ErrNoPeerCertificate
	}
	selected, err := selectedContent(cert, selector)
	if err != nil {
		return false, err
	}
	presented, err := associationValue(selected, matchingType)
	if err != nil {
		return false, err
	}
	return equalBytes(presented, data), nil
}

func selectedContent(cert *x509.Certificate, selector uint8) ([]byte, error) {
	switch selector {
	case SelectorFullCert:
		if len(cert.Raw) == 0 {
			return nil, fmt.Errorf("%w: certificate has no DER bytes", ErrMalformedRecord)
		}
		return cert.Raw, nil
	case SelectorSPKI:
		if len(cert.RawSubjectPublicKeyInfo) != 0 {
			return cert.RawSubjectPublicKeyInfo, nil
		}
		if cert.PublicKey == nil {
			return nil, fmt.Errorf("%w: certificate has no public key", ErrMalformedRecord)
		}
		der, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("dane: marshal public key: %w", err)
		}
		return der, nil
	default:
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedSelector, selector)
	}
}

func associationValue(selected []byte, matchingType uint8) ([]byte, error) {
	switch matchingType {
	case MatchingTypeFull:
		return selected, nil
	case MatchingTypeSHA256:
		sum := sha256.Sum256(selected)
		return sum[:], nil
	case MatchingTypeSHA512:
		sum := sha512.Sum512(selected)
		return sum[:], nil
	default:
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedMatchType, matchingType)
	}
}

func equalBytes(a, b []byte) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare(a, b) == 1
}

func verifyPresentedPath(chain []*x509.Certificate, target int) error {
	if target < 0 || target >= len(chain) {
		return fmt.Errorf("%w: matching certificate is outside the chain", ErrNoMatch)
	}
	for i := 0; i < target; i++ {
		if chain[i] == nil || chain[i+1] == nil {
			return fmt.Errorf("%w: nil certificate in chain", ErrNoMatch)
		}
		if err := chain[i].CheckSignatureFrom(chain[i+1]); err != nil {
			return fmt.Errorf("%w: certificate %d does not chain to certificate %d: %v", ErrNoMatch, i, i+1, err)
		}
	}
	return nil
}

func normalizeRecord(record TLSA) (TLSA, error) {
	var out TLSA
	out.Usage = record.Usage
	out.Selector = record.Selector
	out.MatchingType = record.MatchingType
	out.Data = record.Data
	out.Certificate = record.Certificate
	out.Cert = record.Cert
	out.CertUsage = record.CertUsage
	out.MatchType = record.MatchType
	out.CertAssoc = record.CertAssoc
	out.AssociationData = record.AssociationData

	if out.CertUsage != 0 {
		if out.Usage != 0 && out.Usage != out.CertUsage {
			return TLSA{}, fmt.Errorf("%w: conflicting usage fields", ErrMalformedRecord)
		}
		out.Usage = out.CertUsage
	}
	if out.MatchType != 0 {
		if out.MatchingType != 0 && out.MatchingType != out.MatchType {
			return TLSA{}, fmt.Errorf("%w: conflicting matching type fields", ErrMalformedRecord)
		}
		out.MatchingType = out.MatchType
	}

	data, err := mergeDataFields(out.Data, out.Certificate, out.Cert, out.CertAssoc, out.AssociationData)
	if err != nil {
		return TLSA{}, err
	}
	out.Data = data
	out.Certificate = nil
	out.Cert = nil
	out.CertAssoc = nil
	out.AssociationData = nil
	// Return aliases empty so callers cannot observe a second, stale copy.
	out.CertUsage = 0
	out.MatchType = 0
	return out, nil
}

func recordData(record TLSA) []byte {
	data, _ := mergeDataFields(record.Data, record.Certificate, record.Cert, record.CertAssoc, record.AssociationData)
	return data
}

func mergeDataFields(values ...any) ([]byte, error) {
	var data []byte
	var set bool
	for _, value := range values {
		if value == nil {
			continue
		}
		rv := reflect.ValueOf(value)
		for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
			if rv.IsNil() {
				rv = reflect.Value{}
				break
			}
			rv = rv.Elem()
		}
		if !rv.IsValid() {
			continue
		}
		bytes, ok := bytesValue(rv)
		if !ok {
			return nil, fmt.Errorf("%w: association data has type %T", ErrMalformedRecord, value)
		}
		if len(bytes) == 0 {
			continue
		}
		if set && !equalBytes(data, bytes) {
			return nil, fmt.Errorf("%w: conflicting association data", ErrMalformedRecord)
		}
		if !set {
			data = append([]byte(nil), bytes...)
			set = true
		}
	}
	return data, nil
}

func bytesValue(rv reflect.Value) ([]byte, bool) {
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil, true
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.String:
		data, _ := decodeAssociationText(rv.String())
		return data, true
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return nil, true
		}
		out := make([]byte, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			item := rv.Index(i)
			if item.Kind() != reflect.Uint8 {
				return nil, false
			}
			out[i] = byte(item.Uint())
		}
		return out, true
	default:
		return nil, false
	}
}

type certChain struct {
	certs    []*x509.Certificate
	verified bool
}

type targetSet struct {
	leaf      *x509.Certificate
	chains    []certChain
	sawTarget bool
}

func (t targetSet) allChains() []certChain {
	if len(t.chains) != 0 {
		return t.chains
	}
	if t.leaf != nil {
		return []certChain{{certs: []*x509.Certificate{t.leaf}}}
	}
	return nil
}

func targetFromState(state *tls.ConnectionState) targetSet {
	var t targetSet
	t.sawTarget = true
	if len(state.PeerCertificates) != 0 {
		t.leaf = state.PeerCertificates[0]
	}
	if len(state.VerifiedChains) != 0 {
		for _, chain := range state.VerifiedChains {
			t.chains = append(t.chains, certChain{certs: chain, verified: true})
			if t.leaf == nil && len(chain) != 0 {
				t.leaf = chain[0]
			}
		}
	} else if len(state.PeerCertificates) != 0 {
		t.chains = append(t.chains, certChain{certs: state.PeerCertificates})
		t.leaf = state.PeerCertificates[0]
	}
	if t.leaf == nil && len(state.PeerCertificates) != 0 {
		t.leaf = state.PeerCertificates[0]
	}
	return t
}

type parsedArguments struct {
	records []TLSA
	numbers []int
	data    [][]byte
	target  targetSet
}

func parseArguments(args []any) (parsedArguments, error) {
	var parsed parsedArguments
	for _, arg := range args {
		if err := parsed.add(arg); err != nil {
			return parsedArguments{}, err
		}
	}
	return parsed, nil
}

func (p *parsedArguments) add(arg any) error {
	if arg == nil {
		return fmt.Errorf("%w: nil argument", ErrMalformedRecord)
	}
	switch value := arg.(type) {
	case TLSA:
		p.records = append(p.records, value)
		return nil
	case *TLSA:
		if value == nil {
			return fmt.Errorf("%w: nil TLSA", ErrMalformedRecord)
		}
		p.records = append(p.records, *value)
		return nil
	case []TLSA:
		p.records = append(p.records, value...)
		return nil
	case []any:
		for _, item := range value {
			if err := p.add(item); err != nil {
				return err
			}
		}
		return nil
	case *tls.ConnectionState:
		if value == nil {
			return fmt.Errorf("%w: nil TLS connection state", ErrMalformedRecord)
		}
		p.addTarget(targetFromState(value))
		return nil
	case tls.ConnectionState:
		state := value
		p.addTarget(targetFromState(&state))
		return nil
	case *x509.Certificate:
		if value == nil {
			return ErrNoPeerCertificate
		}
		p.addCertificate(value)
		return nil
	case x509.Certificate:
		cert := value
		p.addCertificate(&cert)
		return nil
	case *tls.Certificate:
		if value == nil {
			return ErrNoPeerCertificate
		}
		return p.addTLSCertificate(*value)
	case tls.Certificate:
		return p.addTLSCertificate(value)
	case []*x509.Certificate:
		if len(value) == 0 {
			return ErrNoChain
		}
		p.addChain(value)
		return nil
	case []x509.Certificate:
		if len(value) == 0 {
			return ErrNoChain
		}
		chain := make([]*x509.Certificate, len(value))
		for i := range value {
			chain[i] = &value[i]
		}
		p.addChain(chain)
		return nil
	case [][]*x509.Certificate:
		for _, chain := range value {
			p.addChain(chain)
		}
		return nil
	case [][]byte:
		chain, err := parseCertificateChain(value)
		if err != nil {
			return err
		}
		p.addChain(chain)
		return nil
	case []byte:
		p.data = append(p.data, append([]byte(nil), value...))
		return nil
	case string:
		data, err := decodeAssociationText(value)
		if err != nil {
			return err
		}
		p.data = append(p.data, data)
		return nil
	}

	if n, ok := integerValue(arg); ok {
		p.numbers = append(p.numbers, n)
		return nil
	}
	if rv := reflect.ValueOf(arg); rv.Kind() == reflect.Array || rv.Kind() == reflect.Slice {
		if bytes, ok := bytesValue(rv); ok {
			p.data = append(p.data, bytes)
			return nil
		}
	}
	if record, ok, err := recordFromStruct(arg); ok {
		if err != nil {
			return err
		}
		p.records = append(p.records, record)
		return nil
	}
	return fmt.Errorf("%w: unsupported argument type %T", ErrMalformedRecord, arg)
}

func (p *parsedArguments) addTarget(t targetSet) {
	p.target.sawTarget = true
	if p.target.leaf == nil {
		p.target.leaf = t.leaf
	}
	p.target.chains = append(p.target.chains, t.chains...)
}

func (p *parsedArguments) addCertificate(cert *x509.Certificate) {
	p.addTarget(targetSet{
		leaf: cert,
		chains: []certChain{{
			certs: []*x509.Certificate{cert},
		}},
		sawTarget: true,
	})
}

func (p *parsedArguments) addChain(chain []*x509.Certificate) {
	if len(chain) == 0 {
		return
	}
	p.addTarget(targetSet{
		leaf: chain[0],
		chains: []certChain{{
			certs: chain,
		}},
		sawTarget: true,
	})
}

func (p *parsedArguments) addTLSCertificate(cert tls.Certificate) error {
	if cert.Leaf != nil {
		p.addCertificate(cert.Leaf)
	}
	if len(cert.Certificate) != 0 {
		chain, err := parseCertificateChain(cert.Certificate)
		if err != nil {
			return err
		}
		p.addChain(chain)
	}
	if cert.Leaf == nil && len(cert.Certificate) == 0 {
		return ErrNoPeerCertificate
	}
	return nil
}

func (t targetSet) build() (targetSet, error) {
	if !t.sawTarget {
		return targetSet{}, fmt.Errorf("%w: no certificate or connection state", ErrNoPeerCertificate)
	}
	if t.leaf == nil && len(t.chains) == 0 {
		return targetSet{}, ErrNoPeerCertificate
	}
	return t, nil
}

func recordFromParts(fixedUsage int, numbers []int, data [][]byte) (TLSA, error) {
	if len(data) == 0 {
		return TLSA{}, fmt.Errorf("%w: missing association data", ErrMalformedRecord)
	}
	if len(data) > 1 {
		return TLSA{}, fmt.Errorf("%w: multiple association data arguments", ErrMalformedRecord)
	}
	var usage, selector, matching uint8
	switch {
	case fixedUsage >= 0 && len(numbers) == 2:
		if err := checkOctet(numbers[0]); err != nil {
			return TLSA{}, err
		}
		if err := checkOctet(numbers[1]); err != nil {
			return TLSA{}, err
		}
		usage = uint8(fixedUsage)
		selector = uint8(numbers[0])
		matching = uint8(numbers[1])
	case fixedUsage >= 0 && len(numbers) == 3:
		if numbers[0] != fixedUsage {
			return TLSA{}, fmt.Errorf("%w: %d", ErrUnsupportedUsage, numbers[0])
		}
		if err := checkOctet(numbers[1]); err != nil {
			return TLSA{}, err
		}
		if err := checkOctet(numbers[2]); err != nil {
			return TLSA{}, err
		}
		usage = uint8(fixedUsage)
		selector = uint8(numbers[1])
		matching = uint8(numbers[2])
	case fixedUsage < 0 && len(numbers) == 3:
		if err := checkOctet(numbers[0]); err != nil {
			return TLSA{}, err
		}
		if err := checkOctet(numbers[1]); err != nil {
			return TLSA{}, err
		}
		if err := checkOctet(numbers[2]); err != nil {
			return TLSA{}, err
		}
		usage = uint8(numbers[0])
		selector = uint8(numbers[1])
		matching = uint8(numbers[2])
	default:
		return TLSA{}, fmt.Errorf("%w: need usage, selector, matching type and data", ErrMalformedRecord)
	}
	if usage > 3 {
		return TLSA{}, fmt.Errorf("%w: %d", ErrUnsupportedUsage, usage)
	}
	if selector > 1 {
		return TLSA{}, fmt.Errorf("%w: %d", ErrUnsupportedSelector, selector)
	}
	if matching > 2 {
		return TLSA{}, fmt.Errorf("%w: %d", ErrUnsupportedMatchType, matching)
	}
	return NewTLSA(usage, selector, matching, data[0]), nil
}

func checkOctet(value int) error {
	if value < 0 || value > 255 {
		return fmt.Errorf("%w: value %d is not a one-octet integer", ErrMalformedRecord, value)
	}
	return nil
}

func integerValue(arg any) (int, bool) {
	rv := reflect.ValueOf(arg)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return int(rv.Uint()), true
	default:
		return 0, false
	}
}

func parseCertificateChain(rawCerts [][]byte) ([]*x509.Certificate, error) {
	if len(rawCerts) == 0 {
		return nil, ErrNoChain
	}
	chain := make([]*x509.Certificate, 0, len(rawCerts))
	for index, raw := range rawCerts {
		cert, err := x509.ParseCertificate(raw)
		if err != nil {
			return nil, fmt.Errorf("dane: parse certificate %d: %w", index, err)
		}
		chain = append(chain, cert)
	}
	return chain, nil
}

func decodeAssociationText(value string) ([]byte, error) {
	cleaned := strings.TrimSpace(value)
	if strings.HasPrefix(cleaned, "0x") || strings.HasPrefix(cleaned, "0X") {
		cleaned = cleaned[2:]
	}
	cleanHex := strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "", ":", "").Replace(cleaned)
	if len(cleanHex) >= 2 && len(cleanHex)%2 == 0 && isHex(cleanHex) {
		decoded, err := hex.DecodeString(cleanHex)
		if err == nil {
			return decoded, nil
		}
	}
	return []byte(value), nil
}

func decodeHex(value string) ([]byte, error) {
	cleaned := strings.TrimSpace(value)
	if strings.HasPrefix(cleaned, "0x") || strings.HasPrefix(cleaned, "0X") {
		cleaned = cleaned[2:]
	}
	cleanHex := strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "", ":", "").Replace(cleaned)
	if len(cleanHex)%2 != 0 || !isHex(cleanHex) {
		return nil, fmt.Errorf("%w: invalid hexadecimal association data", ErrMalformedRecord)
	}
	decoded, err := hex.DecodeString(cleanHex)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedRecord, err)
	}
	return decoded, nil
}

func isHex(value string) bool {
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

func recordFromStruct(value any) (TLSA, bool, error) {
	rv := reflect.ValueOf(value)
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return TLSA{}, false, nil
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return TLSA{}, false, nil
	}
	rt := rv.Type()
	var record TLSA
	var found bool
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		fieldValue := rv.Field(i)
		if !fieldValue.CanInterface() {
			continue
		}
		switch strings.ToLower(field.Name) {
		case "usage", "certusage":
			n, ok := integerValue(fieldValue.Interface())
			if !ok {
				return TLSA{}, true, fmt.Errorf("%w: %s is not an integer", ErrMalformedRecord, field.Name)
			}
			if err := checkOctet(n); err != nil {
				return TLSA{}, true, err
			}
			record.Usage = uint8(n)
			found = true
		case "selector":
			n, ok := integerValue(fieldValue.Interface())
			if !ok {
				return TLSA{}, true, fmt.Errorf("%w: %s is not an integer", ErrMalformedRecord, field.Name)
			}
			if err := checkOctet(n); err != nil {
				return TLSA{}, true, err
			}
			record.Selector = uint8(n)
			found = true
		case "matchingtype", "matchtype":
			n, ok := integerValue(fieldValue.Interface())
			if !ok {
				return TLSA{}, true, fmt.Errorf("%w: %s is not an integer", ErrMalformedRecord, field.Name)
			}
			if err := checkOctet(n); err != nil {
				return TLSA{}, true, err
			}
			record.MatchingType = uint8(n)
			found = true
		case "data", "cert", "certificate", "certassoc", "associationdata", "assocdata":
			bytes, ok := bytesValue(fieldValue)
			if !ok {
				return TLSA{}, true, fmt.Errorf("%w: %s is not byte data", ErrMalformedRecord, field.Name)
			}
			if record.Data == nil {
				record.Data = append([]byte(nil), bytes...)
			} else if !equalBytes(record.Data, bytes) {
				return TLSA{}, true, fmt.Errorf("%w: conflicting association data", ErrMalformedRecord)
			}
			found = true
		}
	}
	return record, found, nil
}
