// Package arc implements receiver-side validation of an Authentication
// Received Chain (RFC 8617) at a mail hand-off.
//
// Cryptographic verification is never performed by this package: callers
// inject the two verifiers (DKIM-like signature verifiers) they trust through
// the Verifiers struct. This keeps the package independent of any particular
// DKIM/crypto implementation and makes the chain policy independently
// testable.
//
// The policy enforced here is:
//
//   - Instance numbers form the continuous sequence 1, 2, ..., N.
//   - Every instance i has exactly one ARC-Seal (AS), one
//     ARC-Message-Signature (AMS) and one ARC-Authentication-Results (AAR).
//   - The AS of instance 1 carries cv=none; every later AS carries cv=pass or
//     cv=fail.
//   - Any cv=fail, any failed AS signature or a failed latest (instance N)
//     AMS signature makes the whole chain fail.
//   - AS(i) is verified against every ARC header at instance i and below,
//     excluding AS(i) itself (an ARC seal never signs its own header, per
//     RFC 8617 section 4.1.3).
package arc

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Kind identifies one of the three ARC header types.
type Kind int

const (
	// AS is the ARC-Seal header.
	AS Kind = iota
	// AMS is the ARC-Message-Signature header.
	AMS
	// AAR is the ARC-Authentication-Results header.
	AAR
)

// String returns the canonical header name for k.
func (k Kind) String() string {
	switch k {
	case AS:
		return "ARC-Seal"
	case AMS:
		return "ARC-Message-Signature"
	case AAR:
		return "ARC-Authentication-Results"
	default:
		return "ARC-UNKNOWN"
	}
}

var kindByName = map[string]Kind{
	"arc-seal":                   AS,
	"arc-message-signature":      AMS,
	"arc-authentication-results": AAR,
}

// Status is the overall chain validation verdict.
type Status int

const (
	// NoChain means no ARC headers were present.
	NoChain Status = iota
	// Pass means the chain structure is intact and every signature verified.
	Pass
	// Fail means the chain is present but policy or signature validation failed.
	Fail
)

// String renders the status for logging.
func (s Status) String() string {
	switch s {
	case NoChain:
		return "none"
	case Pass:
		return "pass"
	case Fail:
		return "fail"
	default:
		return "unknown"
	}
}

// RawHeader is an ARC header as received, name and value separated as a mail
// implementation would split a header block. Value may contain folded lines.
type RawHeader struct {
	Name  string
	Value string
}

// Header is a parsed ARC header.
type Header struct {
	// Kind is the ARC header type.
	Kind Kind
	// Instance is the value of the i= tag; always >= 1 for a parsed header.
	Instance int
	// Name is the original header name as received.
	Name string
	// Value is the unfolded header value.
	Value string
	// Tags holds semicolon-delimited tag values keyed by lower-cased tag name.
	// For AAR only the i= tag is guaranteed to be present.
	Tags map[string]string
}

// Tag returns the lower-cased value of tag t (e.g. "cv", "i"), or "" if the
// header does not carry that tag.
func (h Header) Tag(t string) string {
	return h.Tags[strings.ToLower(t)]
}

// VerifyFunc verifies one ARC signature against the header set the signer
// claims to cover.
//
// h is the signature header carrying the b= value: an ARC-Seal or the latest
// ARC-Message-Signature. covered is the ordered list of headers that the
// signature is supposed to cover, in canonical signing order. A nil error
// means the signature validates.
type VerifyFunc func(h Header, covered []Header) error

// Verifiers groups the caller-injected signature verifiers. Both fields must
// be non-nil for validation.
type Verifiers struct {
	// VerifySeal verifies an ARC-Seal signature.
	VerifySeal VerifyFunc
	// VerifyMessageSignature verifies an ARC-Message-Signature signature.
	VerifyMessageSignature VerifyFunc
}

// Sentinel errors reported through ValidationError.
var (
	// ErrMalformedHeader means an ARC header cannot be parsed.
	ErrMalformedHeader = errors.New("malformed ARC header")
	// ErrDuplicateHeader means two ARC headers share a kind and instance number.
	ErrDuplicateHeader = errors.New("duplicate ARC header")
	// ErrNoChain means no ARC headers were supplied.
	ErrNoChain = errors.New("no ARC chain present")
	// ErrMissingInstance means the 1..N sequence has a gap or an empty slot.
	ErrMissingInstance = errors.New("missing ARC instance")
	// ErrInvalidCV means an ARC-Seal carries an illegal or misplaced cv value.
	ErrInvalidCV = errors.New("invalid ARC-Seal cv value")
	// ErrSealVerification means an injected ARC-Seal verifier rejected a seal.
	ErrSealVerification = errors.New("ARC-Seal verification failed")
	// ErrMessageSignatureVerification means the latest AMS verifier rejected it.
	ErrMessageSignatureVerification = errors.New("ARC-Message-Signature verification failed")
)

// ParseError annotates ErrMalformedHeader with the offending header name.
type ParseError struct {
	Name string
	Err  error
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("arc: %s: %v", e.Name, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

// ValidationError describes one policy or signature failure. It wraps one of
// the package sentinel errors so callers can use errors.Is.
type ValidationError struct {
	// Kind is the offending header kind, when the failure is header-specific.
	Kind Kind
	// Instance is the offending instance number, when applicable.
	Instance int
	// Err is the wrapped sentinel error.
	Err error
	// Detail optionally carries the verifier's own error.
	Detail error
}

func (e *ValidationError) Error() string {
	switch {
	case e.Instance > 0:
		if e.Detail != nil {
			return fmt.Sprintf("arc: %s i=%d: %v: %v", e.Kind, e.Instance, e.Err, e.Detail)
		}
		return fmt.Sprintf("arc: %s i=%d: %v", e.Kind, e.Instance, e.Err)
	case e.Detail != nil:
		return fmt.Sprintf("arc: %v: %v", e.Err, e.Detail)
	default:
		return fmt.Sprintf("arc: %v", e.Err)
	}
}

func (e *ValidationError) Unwrap() error { return e.Err }

// Result is the chain validation verdict.
type Result struct {
	// Status is the overall verdict.
	Status Status
	// Instances is N, the highest instance number seen. Zero when the input
	// contains no ARC headers.
	Instances int
	// Failures lists every structural, policy or signature failure. Empty when
	// Status is Pass or NoChain.
	Failures []*ValidationError
}

// ParseHeaders parses the ARC headers in hs, preserving their input order.
// Non-ARC headers are ignored. Header folding (RFC 5322) is unfolded before
// parsing.
func ParseHeaders(hs []RawHeader) ([]Header, error) {
	parsed := make([]Header, 0, len(hs))
	for _, raw := range hs {
		kind, ok := kindByName[strings.ToLower(strings.TrimSpace(raw.Name))]
		if !ok {
			continue
		}
		h, err := parseHeader(kind, raw)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, h)
	}
	return parsed, nil
}

// ValidateChain is a convenience wrapper around ParseHeaders and
// ValidateParsed.
func ValidateChain(raw []RawHeader, v Verifiers) (*Result, error) {
	headers, err := ParseHeaders(raw)
	if err != nil {
		return nil, err
	}
	return ValidateParsed(headers, v)
}

// ValidateParsed runs the full chain policy over already parsed ARC headers.
// The error return is reserved for caller errors (missing verifiers) and is
// nil for every chain-level outcome; inspect Result.Status and
// Result.Failures for the verdict.
func ValidateParsed(headers []Header, v Verifiers) (*Result, error) {
	if v.VerifySeal == nil || v.VerifyMessageSignature == nil {
		return nil, errors.New("arc: both verifiers must be injected")
	}

	res := &Result{Status: Pass}
	if len(headers) == 0 {
		res.Status = NoChain
		return res, nil
	}

	type slot struct {
		as, ams, aar *Header
	}
	slots := map[int]*slot{}
	maxInstance := 0

	addErr := func(kind Kind, instance int, err error, detail ...error) {
		ve := &ValidationError{Kind: kind, Instance: instance, Err: err}
		if len(detail) > 0 {
			ve.Detail = detail[0]
		}
		res.Failures = append(res.Failures, ve)
	}

	for idx := range headers {
		h := headers[idx]
		if h.Instance > maxInstance {
			maxInstance = h.Instance
		}
		s := slots[h.Instance]
		if s == nil {
			s = &slot{}
			slots[h.Instance] = s
		}
		target := map[Kind]**Header{AS: &s.as, AMS: &s.ams, AAR: &s.aar}[h.Kind]
		if *target != nil {
			addErr(h.Kind, h.Instance, ErrDuplicateHeader)
			continue
		}
		*target = &h
	}
	res.Instances = maxInstance

	// Structural phase: continuous 1..N sequence with one of each kind, plus
	// the cv policy. Signature verification only runs on a structurally sound
	// chain.
	structuralOK := true
	for i := 1; i <= maxInstance; i++ {
		s := slots[i]
		if s == nil {
			structuralOK = false
			addErr(AS, i, ErrMissingInstance)
			continue
		}
		if s.as == nil || s.ams == nil || s.aar == nil {
			structuralOK = false
			if s.as == nil {
				addErr(AS, i, ErrMissingInstance)
			}
			if s.ams == nil {
				addErr(AMS, i, ErrMissingInstance)
			}
			if s.aar == nil {
				addErr(AAR, i, ErrMissingInstance)
			}
			continue
		}
		cv := strings.ToLower(s.as.Tag("cv"))
		switch i {
		case 1:
			if cv != "none" {
				structuralOK = false
				addErr(AS, 1, ErrInvalidCV)
			}
		default:
			if cv != "pass" && cv != "fail" {
				structuralOK = false
				addErr(AS, i, ErrInvalidCV)
				continue
			}
			if cv == "fail" {
				addErr(AS, i, ErrInvalidCV)
			}
		}
	}

	if !structuralOK {
		res.Status = Fail
		return res, nil
	}

	// Signature phase. Each AS(i) covers all ARC headers at instance i and
	// below except AS(i) itself; the latest AMS covers every AAR and itself.
	for i := 1; i <= maxInstance; i++ {
		seal := *slots[i].as
		if err := v.VerifySeal(seal, SealCoveredHeaders(headers, i)); err != nil {
			addErr(AS, i, ErrSealVerification, err)
		}
	}

	latestAMS := *slots[maxInstance].ams
	if err := v.VerifyMessageSignature(latestAMS, AMSCoveredHeaders(headers, maxInstance)); err != nil {
		addErr(AMS, maxInstance, ErrMessageSignatureVerification, err)
	}

	if len(res.Failures) > 0 {
		res.Status = Fail
	}
	return res, nil
}

// SealCoveredHeaders returns the headers an ARC-Seal at instance i signs:
// every AAR, AMS and AS at instances 1..i except AS(i) itself, ordered by
// instance and then AAR, AMS, AS within each instance (the on-the-wire order
// in which the signing hop observed them).
func SealCoveredHeaders(headers []Header, i int) []Header {
	covered := make([]Header, 0, 3*i-1)
	for j := 1; j <= i; j++ {
		for _, kind := range []Kind{AAR, AMS, AS} {
			if h, ok := findHeader(headers, kind, j); ok && !(j == i && kind == AS) {
				covered = append(covered, h)
			}
		}
	}
	return covered
}

// AMSCoveredHeaders returns the headers an ARC-Message-Signature at instance i
// signs: every AAR at instances 1..i followed by AMS(i) itself. An AMS only
// covers the authentication-results headers present when the hop signed and
// never covers ARC-Seal headers (RFC 8617 section 4.1.2).
func AMSCoveredHeaders(headers []Header, i int) []Header {
	covered := make([]Header, 0, i+1)
	for j := 1; j <= i; j++ {
		if h, ok := findHeader(headers, AAR, j); ok {
			covered = append(covered, h)
		}
	}
	if h, ok := findHeader(headers, AMS, i); ok {
		covered = append(covered, h)
	}
	return covered
}

func findHeader(headers []Header, kind Kind, instance int) (Header, bool) {
	for _, h := range headers {
		if h.Kind == kind && h.Instance == instance {
			return h, true
		}
	}
	return Header{}, false
}

func parseHeader(kind Kind, raw RawHeader) (Header, error) {
	name := strings.TrimSpace(raw.Name)
	value := unfold(raw.Value)

	h := Header{
		Kind:     kind,
		Name:     name,
		Value:    value,
		Instance: -1,
		Tags:     map[string]string{},
	}

	instanceFound := false
	for _, part := range strings.Split(value, ";") {
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			// AAR values begin with the free-form authres token
			// (e.g. "i=1; spf=pass smtp.from=..."); only that header may
			// contain segments without a tag assignment.
			if kind == AAR {
				continue
			}
			return Header{}, &ParseError{Name: name, Err: ErrMalformedHeader}
		}
		key := strings.ToLower(strings.TrimSpace(part[:eq]))
		val := strings.TrimSpace(part[eq+1:])
		if key == "" {
			return Header{}, &ParseError{Name: name, Err: ErrMalformedHeader}
		}
		if _, dup := h.Tags[key]; dup {
			return Header{}, &ParseError{Name: name, Err: ErrMalformedHeader}
		}
		h.Tags[key] = val
		if key == "i" {
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return Header{}, &ParseError{Name: name, Err: ErrMalformedHeader}
			}
			h.Instance = n
			instanceFound = true
		}
	}

	if !instanceFound {
		return Header{}, &ParseError{Name: name, Err: ErrMalformedHeader}
	}
	return h, nil
}

// unfold removes RFC 5322 header folding: a CRLF (or bare LF) plus the
// whitespace that introduces the continuation is replaced by a single space,
// together with any whitespace immediately preceding the line break.
func unfold(v string) string {
	var b strings.Builder
	b.Grow(len(v))
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c != '\n' && c != '\r' {
			b.WriteByte(c)
			continue
		}
		// Drop trailing whitespace in front of the line break.
		out := b.String()
		b.Reset()
		b.WriteString(strings.TrimRight(out, " \t"))
		j := i
		if c == '\r' && i+1 < len(v) && v[i+1] == '\n' {
			j++
		}
		k := j + 1
		for k < len(v) && (v[k] == ' ' || v[k] == '\t') {
			k++
		}
		if k > j+1 {
			// A real fold: collapse the break and continuation whitespace.
			b.WriteByte(' ')
			i = k - 1
			continue
		}
		// Stray newline not part of a fold: keep it so downstream validation
		// can reject the malformed header.
		b.WriteString(v[i : j+1])
		i = j
	}
	return b.String()
}
