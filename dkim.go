package netsec

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// TxtRecord is one TXT resource record returned for a selector. A single TXT
// record can be delivered as several character-strings; the DKIM key record is
// the concatenation of those strings with no separator, exactly as required by
// RFC 6376 section 3.6.2.2. DNS lookup is performed by the caller.
type TxtRecord struct {
	Strings []string
}

// RSAParser is injected by the caller and turns the base64-decoded RSA public
// key body into its bit length. Signature/crypto operations stay outside this
// package; the parser only reports the modulus size.
type RSAParser func(der []byte) (bits int, err error)

// SignatureInput carries the signature fields that key-record validation
// depends on. Field names follow the DKIM tag names (d=, i=).
type SignatureInput struct {
	// Domain is the signing domain (d=). It is compared case-insensitively.
	Domain string
	// Identity is the signing identity (i=). The domain part after "@" is
	// compared against Domain.
	Identity string
	// HashAlgorithm is the a= hash component (for example "sha256") and must
	// appear in the key record's h= list when that list is present.
	HashAlgorithm string
}

// KeyRecordVerdict reports the outcome of EvaluateKeyRecord. A non-nil error
// means the record is invalid or the key is revoked; the flags otherwise carry
// non-fatal markings.
type KeyRecordVerdict struct {
	// WeakRSA is true for an RSA key whose modulus is 1024..2047 bits.
	WeakRSA bool
	// Testing is true when t=y is present in the key record.
	Testing bool
	// Algorithm is the normalized key type: "rsa" or "ed25519".
	Algorithm string
	// PublicKey is the base64-decoded p= value.
	PublicKey []byte
}

// Sentinel errors returned by EvaluateKeyRecord. They may be wrapped; use
// errors.Is to match them.
var (
	ErrNoKeyRecord          = errors.New("dkim: no usable DKIM key TXT record")
	ErrMultipleKeyRecords   = errors.New("dkim: more than one TXT record starts with v=DKIM1")
	ErrDuplicateTag         = errors.New("dkim: duplicate tag in key record")
	ErrMalformedTag         = errors.New("dkim: malformed tag-list entry")
	ErrInvalidVersion       = errors.New("dkim: unsupported v= value")
	ErrInvalidKeyType       = errors.New("dkim: unsupported k= algorithm")
	ErrMissingPublicKey     = errors.New("dkim: mandatory p= tag is missing")
	ErrKeyRevoked           = errors.New("dkim: key has been revoked (empty p=)")
	ErrInvalidBase64        = errors.New("dkim: p= is not valid base64")
	ErrHashNotAllowed       = errors.New("dkim: signature hash algorithm is not in h= list")
	ErrInvalidServiceType   = errors.New("dkim: s= contains neither \"*\" nor \"email\"")
	ErrIdentityDomain       = errors.New("dkim: i= domain is not aligned with d=")
	ErrInvalidED25519Length = errors.New("dkim: ed25519 public key must decode to exactly 32 bytes")
	ErrWeakRSA              = errors.New("dkim: RSA key shorter than 1024 bits")
)

// EvaluateKeyRecord applies the DKIM key-record rules to the TXT records the
// caller fetched for a selector.
//
// Each TXT record's character-strings are concatenated without a separator.
// When more than one record begins with "v=DKIM1" the record set is invalid.
// A single "v=DKIM1" record is used directly; otherwise the first record
// without that prefix is treated as a legacy record whose v= tag defaults to
// DKIM1 (RFC 6376 3.6.2.2). If no candidate exists the record set is invalid.
func EvaluateKeyRecord(records []TxtRecord, sig SignatureInput, parseRSA RSAParser) (*KeyRecordVerdict, error) {
	record, err := selectKeyRecord(records)
	if err != nil {
		return nil, err
	}

	tags, err := parseTagList(record)
	if err != nil {
		return nil, err
	}

	verdict := &KeyRecordVerdict{}

	if value, ok := tags["v"]; ok && value != "DKIM1" {
		return nil, fmt.Errorf("%w: %q", ErrInvalidVersion, value)
	}

	algorithm := "rsa"
	if value, ok := tags["k"]; ok {
		if value != "rsa" && value != "ed25519" {
			return nil, fmt.Errorf("%w: %q", ErrInvalidKeyType, value)
		}
		algorithm = value
	}
	verdict.Algorithm = algorithm

	rawKey, ok := tags["p"]
	if !ok {
		return nil, ErrMissingPublicKey
	}
	if stripWhitespace(rawKey) == "" {
		return verdict, ErrKeyRevoked
	}

	if values, ok := tags["h"]; ok {
		if !hashAllowed(values, sig.HashAlgorithm) {
			return nil, fmt.Errorf("%w: h=%q hash=%q", ErrHashNotAllowed, values, sig.HashAlgorithm)
		}
	}

	if value, ok := tags["s"]; ok && !serviceAllowed(value) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidServiceType, value)
	}

	strictIdentity := false
	if value, ok := tags["t"]; ok {
		for _, flag := range strings.Split(value, ":") {
			switch flag {
			case "s":
				strictIdentity = true
			case "y":
				verdict.Testing = true
			}
		}
	}

	if err := checkIdentityDomain(sig.Domain, sig.Identity, strictIdentity); err != nil {
		return nil, err
	}

	decoded, err := base64.StdEncoding.DecodeString(stripWhitespace(rawKey))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidBase64, err)
	}
	verdict.PublicKey = decoded

	switch algorithm {
	case "ed25519":
		if len(decoded) != 32 {
			return nil, fmt.Errorf("%w: got %d bytes", ErrInvalidED25519Length, len(decoded))
		}
	case "rsa":
		if parseRSA == nil {
			return nil, errors.New("dkim: no RSA key parser injected")
		}
		bits, err := parseRSA(decoded)
		if err != nil {
			return nil, fmt.Errorf("dkim: RSA public key parse failed: %w", err)
		}
		switch {
		case bits < 1024:
			return nil, fmt.Errorf("%w: %d bits", ErrWeakRSA, bits)
		case bits < 2048:
			verdict.WeakRSA = true
		}
	}

	return verdict, nil
}

// FromBindingVerdict reports the outcome of CheckFromBinding.
type FromBindingVerdict struct {
	// FromCount is the number of From header lines in the message.
	FromCount int
	// SignedFromCount is the number of From entries in the signature h= list.
	SignedFromCount int
	// Oversigned is true when From appears one extra time in h=, as required
	// when the caller requests over-signing.
	Oversigned bool
}

// Sentinel errors returned by CheckFromBinding.
var (
	ErrMultipleFromHeaders = errors.New("dkim: multiple From header fields")
	ErrFromNotSigned       = errors.New("dkim: From header field is not covered by h=")
	ErrOversignRequired    = errors.New("dkim: h= must list From one more time than the message contains")
)

// CheckFromBinding enforces that the single From header field is covered by
// the signature. headerLines are the raw message header lines (folding lines
// are not From fields); signedHeaders is the h= tag value already split into
// header field names. requireOversign demands that h= contains From exactly
// one time more than the message does.
func CheckFromBinding(headerLines, signedHeaders []string, requireOversign bool) (*FromBindingVerdict, error) {
	fromCount := 0
	for _, line := range headerLines {
		if headerFieldName(line) == "from" {
			fromCount++
		}
	}
	if fromCount > 1 {
		return nil, fmt.Errorf("%w: %d From fields", ErrMultipleFromHeaders, fromCount)
	}

	signedFromCount := 0
	for _, name := range signedHeaders {
		if strings.ToLower(strings.TrimSpace(name)) == "from" {
			signedFromCount++
		}
	}
	if signedFromCount == 0 {
		return nil, ErrFromNotSigned
	}

	verdict := &FromBindingVerdict{
		FromCount:       fromCount,
		SignedFromCount: signedFromCount,
	}
	if requireOversign {
		if signedFromCount != fromCount+1 {
			return nil, fmt.Errorf("%w: h= has %d, message has %d", ErrOversignRequired, signedFromCount, fromCount)
		}
		verdict.Oversigned = true
	}
	return verdict, nil
}

var (
	ErrInvalidSelector = errors.New("dkim: invalid selector name")
	ErrInvalidDomain   = errors.New("dkim: invalid d= domain")
)

// NormalizeSelector lower-cases the selector and signing domain and validates
// every dot-separated selector label as a DNS label per RFC 6376 2.9: each
// label is 1..63 octets, starts and ends with a letter or digit, and its inner
// characters are letters, digits or hyphens.
func NormalizeSelector(selector, domain string) (string, string, error) {
	selector = strings.ToLower(strings.TrimSpace(selector))
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return "", "", ErrInvalidDomain
	}
	for _, label := range strings.Split(selector, ".") {
		if !validDNSLabel(label) {
			return "", "", fmt.Errorf("%w: %q", ErrInvalidSelector, label)
		}
	}
	return selector, domain, nil
}

func selectKeyRecord(records []TxtRecord) (string, error) {
	joined := make([]string, 0, len(records))
	for _, record := range records {
		joined = append(joined, strings.Join(record.Strings, ""))
	}

	var versioned, legacy []string
	for _, record := range joined {
		if strings.HasPrefix(record, "v=DKIM1") {
			versioned = append(versioned, record)
		} else {
			legacy = append(legacy, record)
		}
	}
	if len(versioned) > 1 {
		return "", ErrMultipleKeyRecords
	}
	if len(versioned) == 1 {
		return versioned[0], nil
	}
	if len(legacy) == 0 {
		return "", ErrNoKeyRecord
	}
	return legacy[0], nil
}

func parseTagList(record string) (map[string]string, error) {
	tags := make(map[string]string)
	for _, entry := range strings.Split(record, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		equals := strings.IndexByte(entry, '=')
		if equals <= 0 {
			return nil, fmt.Errorf("%w: %q", ErrMalformedTag, entry)
		}
		name := strings.TrimSpace(entry[:equals])
		value := strings.TrimSpace(entry[equals+1:])
		if name == "" {
			return nil, fmt.Errorf("%w: %q", ErrMalformedTag, entry)
		}
		if _, exists := tags[name]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateTag, name)
		}
		tags[name] = value
	}
	return tags, nil
}

func stripWhitespace(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, r := range value {
		if !unicode.IsSpace(r) {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func hashAllowed(list, hash string) bool {
	if hash == "" {
		return false
	}
	for _, allowed := range strings.Split(list, ":") {
		if strings.TrimSpace(allowed) == hash {
			return true
		}
	}
	return false
}

func serviceAllowed(value string) bool {
	for _, service := range strings.Split(value, ":") {
		switch strings.TrimSpace(service) {
		case "*", "email":
			return true
		}
	}
	return false
}

func checkIdentityDomain(domain, identity string, strict bool) error {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return fmt.Errorf("%w: empty d=", ErrIdentityDomain)
	}

	identityDomain := strings.ToLower(strings.TrimSpace(identity))
	if at := strings.LastIndexByte(identityDomain, '@'); at >= 0 {
		identityDomain = identityDomain[at+1:]
	}
	if identityDomain == "" {
		return fmt.Errorf("%w: empty i= domain", ErrIdentityDomain)
	}

	if strict {
		if identityDomain != domain {
			return fmt.Errorf("%w: t=s requires %q == %q", ErrIdentityDomain, identityDomain, domain)
		}
		return nil
	}
	if identityDomain == domain || strings.HasSuffix(identityDomain, "."+domain) {
		return nil
	}
	return fmt.Errorf("%w: %q is not %q or a subdomain", ErrIdentityDomain, identityDomain, domain)
}

func headerFieldName(line string) string {
	line = strings.TrimSpace(line)
	colon := strings.IndexByte(line, ':')
	if colon <= 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(line[:colon]))
}

func validDNSLabel(label string) bool {
	if len(label) == 0 || len(label) > 63 {
		return false
	}
	for i, r := range label {
		isLetterDigit := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		isInnerHyphen := r == '-' && i > 0 && i < len(label)-1
		if !isLetterDigit && !isInnerHyphen {
			return false
		}
	}
	return true
}
