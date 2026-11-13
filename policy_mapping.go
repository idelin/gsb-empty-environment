// Package netsec provides X.509 certificate policy helpers.
//
// RFC 5280, Section 4.2.1.5 requires that policy mappings use neither
// anyPolicy as an issuerDomainPolicy nor as a subjectDomainPolicy. The
// validators in this package enforce that rule and can be called while
// building or verifying a certification path.
package netsec

import (
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// OID is an object identifier. It is an alias of encoding/asn1's type so
// callers can use the standard ASN.1 OID literals directly.
type OID = asn1.ObjectIdentifier

// AnyPolicyOID is the special anyPolicy policy identifier from RFC 5280,
// Section 4.2.1.4.
var AnyPolicyOID = OID{2, 5, 29, 32, 0}

// AnyPolicy is an alias for AnyPolicyOID.
var AnyPolicy = AnyPolicyOID

// AnyPolicyOIDString is the dotted representation of AnyPolicyOID.
const AnyPolicyOIDString = "2.5.29.32.0"

// PolicyMappingsOID is the extension OID for policyMappings (2.5.29.33).
var PolicyMappingsOID = OID{2, 5, 29, 33}

var (
	// ErrAnyPolicyMapping is returned when either side of a policy mapping
	// uses anyPolicy. It is the sentinel error for all such failures.
	ErrAnyPolicyMapping = errors.New("netsec: policy mappings must not use anyPolicy")

	// ErrPolicyMappingAnyPolicy is an alias for ErrAnyPolicyMapping.
	ErrPolicyMappingAnyPolicy = ErrAnyPolicyMapping

	// ErrAnyPolicy is an alias for ErrAnyPolicyMapping.
	ErrAnyPolicy = ErrAnyPolicyMapping

	// ErrInvalidPolicyMapping is returned for malformed mappings, such as
	// missing or syntactically invalid OIDs.
	ErrInvalidPolicyMapping = errors.New("netsec: invalid policy mapping")

	// ErrUnsupportedPolicyMapping is returned when the input representation
	// of policy mappings cannot be interpreted.
	ErrUnsupportedPolicyMapping = errors.New("netsec: unsupported policy mapping representation")

	// ErrNilCertificate is returned when a certificate validation function
	// receives a nil certificate.
	ErrNilCertificate = errors.New("netsec: nil certificate")
)

// PolicyMapping represents one issuerDomainPolicy -> subjectDomainPolicy
// entry from the policyMappings certificate extension.
type PolicyMapping struct {
	IssuerDomainPolicy  OID
	SubjectDomainPolicy OID
}

// PolicyMappings is a collection of policy mapping entries.
type PolicyMappings = []PolicyMapping

// NewPolicyMapping builds a mapping from any OID representation accepted by
// ParseOID's callers, including asn1.ObjectIdentifier, crypto/x509.OID, and
// dotted strings.
func NewPolicyMapping(issuerDomainPolicy, subjectDomainPolicy any) (PolicyMapping, error) {
	issuer, err := canonicalOID(issuerDomainPolicy)
	if err != nil {
		return PolicyMapping{}, err
	}
	subject, err := canonicalOID(subjectDomainPolicy)
	if err != nil {
		return PolicyMapping{}, err
	}
	mapping := PolicyMapping{
		IssuerDomainPolicy:  mustOID(issuer),
		SubjectDomainPolicy: mustOID(subject),
	}
	if err := mapping.Validate(); err != nil {
		return PolicyMapping{}, err
	}
	return mapping, nil
}

// MustNewPolicyMapping is like NewPolicyMapping but panics on invalid input.
func MustNewPolicyMapping(issuerDomainPolicy, subjectDomainPolicy any) PolicyMapping {
	mapping, err := NewPolicyMapping(issuerDomainPolicy, subjectDomainPolicy)
	if err != nil {
		panic(err)
	}
	return mapping
}

// PolicyMappingEnd identifies which side of a mapping contains a forbidden
// anyPolicy OID.
type PolicyMappingEnd string

const (
	// IssuerDomainPolicy identifies the issuerDomainPolicy side.
	IssuerDomainPolicy PolicyMappingEnd = "issuerDomainPolicy"
	// SubjectDomainPolicy identifies the subjectDomainPolicy side.
	SubjectDomainPolicy PolicyMappingEnd = "subjectDomainPolicy"
	// BothDomainPolicies identifies mappings where both sides use anyPolicy.
	BothDomainPolicies PolicyMappingEnd = "issuerDomainPolicy and subjectDomainPolicy"
)

// AnyPolicyMappingError describes a policy mapping that illegally uses
// anyPolicy. It supports errors.Is(err, ErrAnyPolicyMapping).
type AnyPolicyMappingError struct {
	IssuerDomainPolicy  OID
	SubjectDomainPolicy OID
	End                 PolicyMappingEnd
}

func (e *AnyPolicyMappingError) Error() string {
	return fmt.Sprintf(
		"netsec: policy mapping %s -> %s uses anyPolicy in %s; anyPolicy cannot be mapped to or from",
		oidString(e.IssuerDomainPolicy),
		oidString(e.SubjectDomainPolicy),
		e.End,
	)
}

// Unwrap makes errors.Is(err, ErrAnyPolicyMapping) report true.
func (e *AnyPolicyMappingError) Unwrap() error { return ErrAnyPolicyMapping }

// InvalidPolicyMappingError describes a malformed mapping.
type InvalidPolicyMappingError struct {
	IssuerDomainPolicy  any
	SubjectDomainPolicy any
	Reason              string
}

func (e *InvalidPolicyMappingError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("netsec: invalid policy mapping: %s", e.Reason)
	}
	return "netsec: invalid policy mapping"
}

func (e *InvalidPolicyMappingError) Unwrap() error { return ErrInvalidPolicyMapping }

// String returns the dotted representation of the mapping.
func (m PolicyMapping) String() string {
	return oidString(m.IssuerDomainPolicy) + " -> " + oidString(m.SubjectDomainPolicy)
}

// Equal reports whether both mappings contain the same two OIDs.
func (m PolicyMapping) Equal(other PolicyMapping) bool {
	return m.IssuerDomainPolicy.Equal(other.IssuerDomainPolicy) &&
		m.SubjectDomainPolicy.Equal(other.SubjectDomainPolicy)
}

// Validate validates this mapping.
func (m PolicyMapping) Validate() error {
	return ValidatePolicyMapping(m.IssuerDomainPolicy, m.SubjectDomainPolicy)
}

// IsAnyPolicy reports whether oid denotes the special anyPolicy OID. It
// accepts asn1.ObjectIdentifier, crypto/x509.OID, strings, integer slices,
// and values implementing fmt.Stringer.
func IsAnyPolicy(oid any) bool {
	s, err := canonicalOID(oid)
	if err != nil {
		return false
	}
	return s == AnyPolicyOIDString
}

// ParseOID parses a dotted object identifier and validates its arcs.
func ParseOID(s string) (OID, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("%w: empty OID", ErrInvalidPolicyMapping)
	}

	parts := strings.Split(s, ".")
	oid := make(OID, len(parts))
	for i, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("%w: empty arc in %q", ErrInvalidPolicyMapping, s)
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("%w: invalid arc %q in %q", ErrInvalidPolicyMapping, part, s)
		}
		oid[i] = n
	}
	if err := validateOID(oid); err != nil {
		return nil, err
	}
	return oid, nil
}

// ValidatePolicyMapping validates one policy mapping. Either argument may
// use any representation accepted by IsAnyPolicy.
func ValidatePolicyMapping(issuerDomainPolicy, subjectDomainPolicy any) error {
	issuer, err := canonicalOID(issuerDomainPolicy)
	if err != nil {
		return &InvalidPolicyMappingError{
			IssuerDomainPolicy:  issuerDomainPolicy,
			SubjectDomainPolicy: subjectDomainPolicy,
			Reason:              "invalid issuerDomainPolicy",
		}
	}
	subject, err := canonicalOID(subjectDomainPolicy)
	if err != nil {
		return &InvalidPolicyMappingError{
			IssuerDomainPolicy:  issuerDomainPolicy,
			SubjectDomainPolicy: subjectDomainPolicy,
			Reason:              "invalid subjectDomainPolicy",
		}
	}

	issuerAny := issuer == AnyPolicyOIDString
	subjectAny := subject == AnyPolicyOIDString
	switch {
	case issuerAny && subjectAny:
		return &AnyPolicyMappingError{
			IssuerDomainPolicy:  AnyPolicyOID,
			SubjectDomainPolicy: AnyPolicyOID,
			End:                 BothDomainPolicies,
		}
	case issuerAny:
		return &AnyPolicyMappingError{
			IssuerDomainPolicy:  AnyPolicyOID,
			SubjectDomainPolicy: mustOID(subject),
			End:                 IssuerDomainPolicy,
		}
	case subjectAny:
		return &AnyPolicyMappingError{
			IssuerDomainPolicy:  mustOID(issuer),
			SubjectDomainPolicy: AnyPolicyOID,
			End:                 SubjectDomainPolicy,
		}
	}
	return nil
}

// ValidatePolicyMappings validates a collection of policy mappings. It
// accepts []PolicyMapping, []x509.PolicyMapping, []*x509.Certificate,
// *x509.Certificate, maps of issuer OIDs to subject OIDs, and equivalent
// structs. All mappings are checked; valid mappings are not discarded or
// rewritten.
//
// A mapping that uses anyPolicy at either end causes validation to fail with
// an error wrapping ErrAnyPolicyMapping.
func ValidatePolicyMappings(mappings any, subjectDomainPolicy ...any) error {
	if len(subjectDomainPolicy) > 0 {
		if len(subjectDomainPolicy) != 1 {
			return fmt.Errorf("%w: expected at most one subjectDomainPolicy", ErrUnsupportedPolicyMapping)
		}
		return ValidatePolicyMapping(mappings, subjectDomainPolicy[0])
	}

	switch value := mappings.(type) {
	case *x509.Certificate:
		return ValidateCertificatePolicyMappings(value)
	case x509.Certificate:
		return ValidateCertificatePolicyMappings(&value)
	case []*x509.Certificate:
		return ValidatePathPolicyMappings(value)
	}
	_, err := NormalizePolicyMappings(mappings)
	return err
}

// CheckPolicyMappings is an alias for ValidatePolicyMappings.
func CheckPolicyMappings(mappings any, subjectDomainPolicy ...any) error {
	return ValidatePolicyMappings(mappings, subjectDomainPolicy...)
}

// VerifyPolicyMappings is an alias for ValidatePolicyMappings.
func VerifyPolicyMappings(mappings any, subjectDomainPolicy ...any) error {
	return ValidatePolicyMappings(mappings, subjectDomainPolicy...)
}

// Validate is an alias for ValidatePolicyMappings.
func Validate(mappings any, subjectDomainPolicy ...any) error {
	return ValidatePolicyMappings(mappings, subjectDomainPolicy...)
}

// ContainsAnyPolicy reports whether any mapping in the input uses anyPolicy
// at either end. Malformed input also produces an error.
func ContainsAnyPolicy(mappings any) (bool, error) {
	_, err := NormalizePolicyMappings(mappings)
	if err != nil {
		if errors.Is(err, ErrAnyPolicyMapping) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

// NormalizePolicyMappings validates all mappings and returns canonical OID
// values in input order. Mappings between two ordinary policy OIDs are
// preserved exactly; no such mapping is dropped.
func NormalizePolicyMappings(input any) ([]PolicyMapping, error) {
	switch value := input.(type) {
	case *x509.Certificate:
		return NormalizePolicyMappings(value.PolicyMappings)
	case x509.Certificate:
		return NormalizePolicyMappings(value.PolicyMappings)
	}

	raw, err := extractMappings(input)
	if err != nil {
		return nil, err
	}

	out := make([]PolicyMapping, 0, len(raw))
	for _, m := range raw {
		if err := ValidatePolicyMapping(m.issuer, m.subject); err != nil {
			return nil, err
		}
		issuer, err := ParseOID(m.issuer)
		if err != nil {
			return nil, err
		}
		subject, err := ParseOID(m.subject)
		if err != nil {
			return nil, err
		}
		out = append(out, PolicyMapping{
			IssuerDomainPolicy:  issuer,
			SubjectDomainPolicy: subject,
		})
	}
	return out, nil
}

// FilterPolicyMappings validates mappings and returns all legal mappings.
// It returns no mappings if any entry contains anyPolicy, because a
// certificate with an illegal mapping must be rejected as a whole.
func FilterPolicyMappings(input any) ([]PolicyMapping, error) {
	return NormalizePolicyMappings(input)
}

// ProcessPolicyMappings is an alias for NormalizePolicyMappings.
func ProcessPolicyMappings(input any) ([]PolicyMapping, error) {
	return NormalizePolicyMappings(input)
}

// PolicyMappingSet is an immutable, validated collection of policy mappings.
type PolicyMappingSet struct {
	mappings []PolicyMapping
}

// NewPolicyMappingSet validates and stores mappings. It rejects any mapping
// that uses anyPolicy.
func NewPolicyMappingSet(input any) (*PolicyMappingSet, error) {
	mappings, err := NormalizePolicyMappings(input)
	if err != nil {
		return nil, err
	}
	return &PolicyMappingSet{mappings: cloneMappings(mappings)}, nil
}

// Mappings returns a copy of the validated mappings in their original order.
func (s *PolicyMappingSet) Mappings() []PolicyMapping {
	if s == nil {
		return nil
	}
	return cloneMappings(s.mappings)
}

// Issuers returns the distinct issuerDomainPolicy OIDs in first-seen order.
func (s *PolicyMappingSet) Issuers() []OID {
	if s == nil {
		return nil
	}
	seen := make(map[string]bool, len(s.mappings))
	var out []OID
	for _, m := range s.mappings {
		k := oidString(m.IssuerDomainPolicy)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, cloneOID(m.IssuerDomainPolicy))
	}
	return out
}

// SubjectsFor returns all subjectDomainPolicy OIDs mapped from issuer.
func (s *PolicyMappingSet) SubjectsFor(issuer any) ([]OID, error) {
	key, err := canonicalOID(issuer)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, nil
	}
	var out []OID
	for _, m := range s.mappings {
		if oidString(m.IssuerDomainPolicy) == key {
			out = append(out, cloneOID(m.SubjectDomainPolicy))
		}
	}
	return out, nil
}

// Map returns a copy of the mappings keyed by issuerDomainPolicy.
func (s *PolicyMappingSet) Map() map[string][]OID {
	if s == nil {
		return nil
	}
	out := make(map[string][]OID, len(s.mappings))
	for _, m := range s.mappings {
		k := oidString(m.IssuerDomainPolicy)
		out[k] = append(out[k], cloneOID(m.SubjectDomainPolicy))
	}
	return out
}

type rawMapping struct {
	issuer  string
	subject string
}

func extractMappings(input any) ([]rawMapping, error) {
	if input == nil {
		return nil, nil
	}

	v := reflect.ValueOf(input)
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return nil, nil
	}
	v = indirect(v)

	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		if v.Len() == 0 {
			return nil, nil
		}
		out := make([]rawMapping, 0, v.Len())
		for i := 0; i < v.Len(); i++ {
			m, err := extractSingle(v.Index(i))
			if err != nil {
				return nil, err
			}
			out = append(out, m)
		}
		return out, nil
	case reflect.Map:
		return extractMap(v)
	default:
		m, err := extractSingle(v)
		if err != nil {
			return nil, err
		}
		return []rawMapping{m}, nil
	}
}

func extractMap(v reflect.Value) ([]rawMapping, error) {
	if v.IsNil() || v.Len() == 0 {
		return nil, nil
	}

	keys := v.MapKeys()
	issuerKey, subjectKey := findPairKeys(keys)
	if issuerKey.IsValid() && subjectKey.IsValid() {
		issuer, err := oidText(v.MapIndex(issuerKey))
		if err != nil {
			return nil, err
		}
		subject, err := oidText(v.MapIndex(subjectKey))
		if err != nil {
			return nil, err
		}
		return []rawMapping{{issuer: issuer, subject: subject}}, nil
	}

	type keyedValue struct {
		name  string
		value reflect.Value
	}
	var entries []keyedValue
	for _, k := range keys {
		name, err := keyString(k)
		if err != nil {
			return nil, err
		}
		entries = append(entries, keyedValue{name: name, value: v.MapIndex(k)})
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	var out []rawMapping
	for _, entry := range entries {
		if isSubjectKey(entry.name) {
			continue
		}
		issuer, err := canonicalOIDText(entry.name)
		if err != nil {
			return nil, &InvalidPolicyMappingError{
				IssuerDomainPolicy: entry.name,
				Reason:             "map key is not a policy OID",
			}
		}
		subjects, err := extractSubjectValues(entry.value)
		if err != nil {
			return nil, err
		}
		for _, subject := range subjects {
			out = append(out, rawMapping{issuer: issuer, subject: subject})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: map has no issuerDomainPolicy entries", ErrUnsupportedPolicyMapping)
	}
	return out, nil
}

func extractSubjectValues(v reflect.Value) ([]string, error) {
	v = indirect(v)
	if !v.IsValid() || (v.Kind() == reflect.Pointer && v.IsNil()) {
		return nil, &InvalidPolicyMappingError{Reason: "nil subjectDomainPolicy value"}
	}
	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		if v.Len() == 0 {
			return nil, &InvalidPolicyMappingError{Reason: "empty subjectDomainPolicy list"}
		}
		out := make([]string, 0, v.Len())
		for i := 0; i < v.Len(); i++ {
			subject, err := oidText(v.Index(i))
			if err != nil {
				return nil, err
			}
			out = append(out, subject)
		}
		return out, nil
	default:
		subject, err := oidText(v)
		if err != nil {
			return nil, err
		}
		return []string{subject}, nil
	}
}

func extractSingle(v reflect.Value) (rawMapping, error) {
	v = indirect(v)
	if !v.IsValid() {
		return rawMapping{}, &InvalidPolicyMappingError{Reason: "nil policy mapping"}
	}

	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return rawMapping{}, &InvalidPolicyMappingError{Reason: "nil policy mapping"}
		}
		return extractSingle(v.Elem())
	}

	switch v.Kind() {
	case reflect.Array, reflect.Slice:
		if v.Len() != 2 {
			return rawMapping{}, &InvalidPolicyMappingError{Reason: "policy mapping pair must contain exactly two OIDs"}
		}
		issuer, err := oidText(v.Index(0))
		if err != nil {
			return rawMapping{}, err
		}
		subject, err := oidText(v.Index(1))
		if err != nil {
			return rawMapping{}, err
		}
		return rawMapping{issuer: issuer, subject: subject}, nil
	case reflect.Map:
		pairs, err := extractMap(v)
		if err != nil {
			return rawMapping{}, err
		}
		if len(pairs) != 1 {
			return rawMapping{}, &InvalidPolicyMappingError{Reason: "expected exactly one policy mapping"}
		}
		return pairs[0], nil
	case reflect.Struct:
		issuerField, subjectField := findStructPair(v)
		if !issuerField.IsValid() || !subjectField.IsValid() {
			return rawMapping{}, fmt.Errorf("%w: struct lacks issuerDomainPolicy and subjectDomainPolicy fields", ErrUnsupportedPolicyMapping)
		}
		issuer, err := oidText(issuerField)
		if err != nil {
			return rawMapping{}, err
		}
		subject, err := oidText(subjectField)
		if err != nil {
			return rawMapping{}, err
		}
		return rawMapping{issuer: issuer, subject: subject}, nil
	default:
		return rawMapping{}, fmt.Errorf("%w: %T", ErrUnsupportedPolicyMapping, v.Interface())
	}
}

func findPairKeys(keys []reflect.Value) (reflect.Value, reflect.Value) {
	var issuer, subject reflect.Value
	for _, k := range keys {
		name, err := keyString(k)
		if err != nil {
			continue
		}
		if !issuer.IsValid() && isIssuerKey(name) {
			issuer = k
		}
		if !subject.IsValid() && isSubjectKey(name) {
			subject = k
		}
	}
	return issuer, subject
}

func findStructPair(v reflect.Value) (reflect.Value, reflect.Value) {
	var issuer, subject reflect.Value
	exported := 0
	var candidates []reflect.Value
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if !f.CanInterface() {
			continue
		}
		exported++
		candidates = append(candidates, f)
		name := normalizeKey(v.Type().Field(i).Name)
		if !issuer.IsValid() && isIssuerKey(name) {
			issuer = f
		}
		if !subject.IsValid() && isSubjectKey(name) {
			subject = f
		}
	}
	if !issuer.IsValid() || !subject.IsValid() {
		if exported == 2 {
			issuer, subject = candidates[0], candidates[1]
		}
	}
	return issuer, subject
}

func keyString(k reflect.Value) (string, error) {
	if k.Kind() == reflect.Interface {
		if k.IsNil() {
			return "", &InvalidPolicyMappingError{Reason: "nil policy mapping map key"}
		}
		k = k.Elem()
	}
	if k.Kind() == reflect.String {
		return k.String(), nil
	}
	text, err := oidText(k)
	if err == nil {
		return text, nil
	}
	return "", &InvalidPolicyMappingError{Reason: "policy mapping map key is not an OID string"}
}

func isIssuerKey(name string) bool {
	n := normalizeKey(name)
	return n == "issuerdomainpolicy" || n == "issuerpolicy" || n == "issuer" || n == "from"
}

func isSubjectKey(name string) bool {
	n := normalizeKey(name)
	return n == "subjectdomainpolicy" || n == "subjectpolicy" || n == "subject" || n == "to"
}

func normalizeKey(name string) string {
	name = strings.ToLower(name)
	replacer := strings.NewReplacer("_", "", "-", "", " ", "")
	return replacer.Replace(name)
}

func indirect(v reflect.Value) reflect.Value {
	for v.IsValid() {
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if v.IsNil() {
				return reflect.Value{}
			}
			v = v.Elem()
		default:
			return v
		}
	}
	return v
}

func oidText(v any) (string, error) {
	text, err := canonicalOID(v)
	if err != nil {
		return "", err
	}
	return text, nil
}

func canonicalOID(v any) (string, error) {
	if v == nil {
		return "", &InvalidPolicyMappingError{Reason: "nil OID"}
	}
	if rv, ok := v.(reflect.Value); ok {
		if !rv.IsValid() {
			return "", &InvalidPolicyMappingError{Reason: "nil OID"}
		}
		if !rv.CanInterface() {
			return "", &InvalidPolicyMappingError{Reason: "unexported OID value"}
		}
		return canonicalOID(rv.Interface())
	}
	if oid, ok := v.(OID); ok {
		if err := validateOID(oid); err != nil {
			return "", err
		}
		return strings.TrimSpace(oid.String()), nil
	}
	if oid, ok := v.(*OID); ok {
		if oid == nil {
			return "", &InvalidPolicyMappingError{Reason: "nil OID"}
		}
		return canonicalOID(*oid)
	}
	if s, ok := v.(string); ok {
		return canonicalOIDText(s)
	}
	if values, ok := v.([]int); ok {
		return canonicalOID(OID(values))
	}
	if values, ok := v.([]uint64); ok {
		oid := make(OID, len(values))
		for i, n := range values {
			oid[i] = int(n)
		}
		return canonicalOID(oid)
	}
	if stringer, ok := v.(fmt.Stringer); ok {
		return canonicalOIDText(stringer.String())
	}

	rv := reflect.ValueOf(v)
	rv = indirect(rv)
	if !rv.IsValid() {
		return "", &InvalidPolicyMappingError{Reason: "nil OID"}
	}
	if rv.Kind() == reflect.String {
		return canonicalOIDText(rv.String())
	}
	if rv.Kind() == reflect.Slice {
		oid := make(OID, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			elem := indirect(rv.Index(i))
			if !elem.IsValid() {
				return "", &InvalidPolicyMappingError{Reason: "invalid OID component"}
			}
			switch elem.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				oid = append(oid, int(elem.Int()))
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				oid = append(oid, int(elem.Uint()))
			default:
				return "", &InvalidPolicyMappingError{Reason: "OID components must be integers"}
			}
		}
		return canonicalOID(oid)
	}
	return "", fmt.Errorf("%w: OID type %T", ErrUnsupportedPolicyMapping, v)
}

func canonicalOIDText(s string) (string, error) {
	oid, err := ParseOID(s)
	if err != nil {
		return "", err
	}
	return oid.String(), nil
}

func validateOID(oid OID) error {
	if len(oid) < 2 {
		return fmt.Errorf("%w: OID must contain at least two arcs", ErrInvalidPolicyMapping)
	}
	if oid[0] < 0 || oid[0] > 2 {
		return fmt.Errorf("%w: invalid first OID arc %d", ErrInvalidPolicyMapping, oid[0])
	}
	if oid[0] < 2 && (oid[1] < 0 || oid[1] > 39) {
		return fmt.Errorf("%w: invalid second OID arc %d", ErrInvalidPolicyMapping, oid[1])
	}
	for _, arc := range oid {
		if arc < 0 {
			return fmt.Errorf("%w: negative OID arc", ErrInvalidPolicyMapping)
		}
	}
	return nil
}

func mustOID(s string) OID {
	oid, err := ParseOID(s)
	if err != nil {
		panic(err)
	}
	return oid
}

func oidString(oid OID) string {
	if len(oid) == 0 {
		return ""
	}
	return oid.String()
}

func cloneOID(oid OID) OID {
	if oid == nil {
		return nil
	}
	return append(OID(nil), oid...)
}

func cloneMappings(in []PolicyMapping) []PolicyMapping {
	if in == nil {
		return nil
	}
	out := make([]PolicyMapping, len(in))
	for i, m := range in {
		out[i] = PolicyMapping{
			IssuerDomainPolicy:  cloneOID(m.IssuerDomainPolicy),
			SubjectDomainPolicy: cloneOID(m.SubjectDomainPolicy),
		}
	}
	return out
}
