package netsec

import (
	"fmt"
	"net/http"
	"net/textproto"
	"sort"
	"strings"
)

// Field is one HTTP field occurrence. Sections use slices rather than maps so
// that repeated occurrences and their order are retained.
type Field struct {
	Name  string
	Value string
}

// Section is an ordered HTTP field section. Two occurrences with the same name
// are distinct fields and are signed independently.
type Section []Field

// TrailerHeader is the field that predeclares the trailer section.
const TrailerHeader = "Trailer"

// forbiddenTrailerFields contains fields that must not be moved to, or
// supplied in, a trailer section. Digest fields are intentionally absent:
// trailers are allowed to carry representation digests.
var forbiddenTrailerFields = map[string]struct{}{
	// Authentication, credentials, and signature material.
	"authorization":             {},
	"proxy-authorization":       {},
	"www-authenticate":          {},
	"proxy-authenticate":        {},
	"authentication-info":       {},
	"proxy-authentication-info": {},
	"cookie":                    {},
	"set-cookie":                {},
	"signature":                 {},
	"signature-input":           {},
	"x-message-signature":       {},

	// Routing and forwarded-request metadata.
	"host":               {},
	"forwarded":          {},
	"via":                {},
	"x-real-ip":          {},
	"x-forwarded-for":    {},
	"x-forwarded-host":   {},
	"x-forwarded-proto":  {},
	"x-forwarded-port":   {},
	"x-forwarded-prefix": {},
	"x-forwarded-server": {},
	"x-original-url":     {},
	"x-rewrite-url":      {},

	// Framing, transfer length, and connection control.
	"content-length":    {},
	"transfer-encoding": {},
	"trailer":           {},
	"connection":        {},
	"proxy-connection":  {},
	"keep-alive":        {},
	"te":                {},
	"upgrade":           {},
	"expect":            {},
	"content-range":     {},
}

// IsForbiddenTrailerField reports whether name belongs to one of the
// authentication, routing, or message-framing fields that may not appear in a
// trailer section.
func IsForbiddenTrailerField(name string) bool {
	canonical := textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(name))
	if canonical == "" {
		return false
	}
	lower := strings.ToLower(canonical)
	if _, ok := forbiddenTrailerFields[lower]; ok {
		return true
	}
	return strings.HasPrefix(lower, "forwarded-") ||
		strings.HasPrefix(lower, "x-forwarded-") ||
		strings.HasPrefix(lower, "x-original-") ||
		strings.HasPrefix(lower, "x-rewrite-")
}

// DeclareTrailers adds names to the authenticated Trailer declaration. Names
// are canonicalized, deduplicated, and checked against the forbidden set.
func DeclareTrailers(header http.Header, names ...string) error {
	if header == nil {
		return fmt.Errorf("%w: nil header map", ErrInvalidHeader)
	}

	declared, err := DeclaredTrailers(header)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(declared)+len(names))
	for _, name := range declared {
		seen[strings.ToLower(name)] = struct{}{}
	}

	ordered := append([]string(nil), declared...)
	for _, name := range names {
		canonical, err := normalizeTrailerName(name)
		if err != nil {
			return err
		}
		key := strings.ToLower(canonical)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ordered = append(ordered, canonical)
	}
	if len(ordered) == 0 {
		return nil
	}
	for key := range header {
		if strings.EqualFold(key, TrailerHeader) {
			delete(header, key)
		}
	}
	header.Set(TrailerHeader, strings.Join(ordered, ", "))
	return nil
}

// DeclaredTrailers parses and validates all Trailer header values. It returns
// the declared names in declaration order without duplicates.
func DeclaredTrailers(header http.Header) ([]string, error) {
	return parseTrailerDeclarations(canonicalHeaderValues(header)[strings.ToLower(TrailerHeader)])
}

// ValidateTrailers checks the complete trailer policy before any merge:
//
//   - every trailer was predeclared in the authenticated header section;
//   - no trailer is an authentication, routing, or length field;
//   - no trailer has the same name as an authenticated header field;
//   - every declared trailer is present (a missing one is treated as a
//     truncated message); and
//   - names and values are structurally valid HTTP fields.
//
// Duplicate trailer occurrences are allowed; their order is bound by the
// signature. Validation never mutates either map.
func ValidateTrailers(header, trailers http.Header) error {
	return validateTrailers(header, trailers, false)
}

// ValidateParsedTrailers is like ValidateTrailers but reconstructs a missing
// declaration from the trailer map. It exists for net/http maps whose Trailer
// field was removed during parsing; the reconstructed names are still bound by
// the signature. Prefer CaptureDeclaredTrailers and
// MergeTrailersWithDeclaration when the original declaration is available.
func ValidateParsedTrailers(header, trailers http.Header) error {
	return validateTrailers(header, trailers, true)
}

func validateTrailers(header, trailers http.Header, inferDeclaration bool) error {
	if err := validateHTTPHeader(header); err != nil {
		return err
	}
	if err := validateHTTPHeader(trailers); err != nil {
		return err
	}
	declared, err := DeclaredTrailers(header)
	if err != nil {
		return err
	}
	trailerNames := canonicalHeaderValues(trailers)
	if len(declared) == 0 && inferDeclaration {
		// net/http removes the Trailer header from Request.Header and
		// Response.Header after parsing it into the map-typed trailer
		// section. Reconstruct the declaration so a normal Go server can
		// validate the message. The signature still binds these names, so
		// adding or removing a trailer later is detected as a signature
		// mismatch.
		for _, name := range sortedKeys(trailerNames) {
			declared = append(declared, textproto.CanonicalMIMEHeaderKey(name))
		}
	}

	declaredSet := make(map[string]struct{}, len(declared))
	for _, name := range declared {
		declaredSet[strings.ToLower(name)] = struct{}{}
	}

	headerNames := canonicalHeaderValues(header)

	for _, name := range sortedKeys(trailerNames) {
		values := trailerNames[name]
		if IsForbiddenTrailerField(name) {
			return fmt.Errorf("%w: %s", ErrForbiddenTrailer, name)
		}
		if _, ok := headerNames[name]; ok {
			return fmt.Errorf("%w: %s", ErrTrailerHeaderCollision, name)
		}
		if _, ok := declaredSet[name]; !ok {
			return fmt.Errorf("%w: %s", ErrUndeclaredTrailer, name)
		}
		for _, value := range values {
			if err := validateFieldValue(value); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	}

	for _, name := range declared {
		key := strings.ToLower(name)
		if len(trailerNames[key]) == 0 {
			return fmt.Errorf("%w: %s", ErrMissingDeclaredTrailer, name)
		}
	}
	return nil
}

// ValidateSections is the ordered-section form of ValidateTrailers. It is
// useful when a caller parses the wire representation directly and wants the
// signature to bind the exact arrival order, including duplicate fields.
func ValidateSections(header, trailers Section) error {
	headerNames := make(map[string]struct{}, len(header))
	var declarationLines []string

	for _, field := range header {
		canonical, err := canonicalFieldName(field.Name)
		if err != nil {
			return err
		}
		if err := validateFieldValue(field.Value); err != nil {
			return fmt.Errorf("%s: %w", canonical, err)
		}
		key := strings.ToLower(canonical)
		headerNames[key] = struct{}{}
		if key == strings.ToLower(TrailerHeader) {
			declarationLines = append(declarationLines, field.Value)
		}
	}

	declared, err := parseTrailerDeclarations(declarationLines)
	if err != nil {
		return err
	}
	declaredSet := make(map[string]struct{}, len(declared))
	for _, name := range declared {
		declaredSet[strings.ToLower(name)] = struct{}{}
	}

	trailerNames := make(map[string]struct{}, len(trailers))
	for _, field := range trailers {
		canonical, err := canonicalFieldName(field.Name)
		if err != nil {
			return err
		}
		if err := validateFieldValue(field.Value); err != nil {
			return fmt.Errorf("%s: %w", canonical, err)
		}
		key := strings.ToLower(canonical)
		if IsForbiddenTrailerField(canonical) {
			return fmt.Errorf("%w: %s", ErrForbiddenTrailer, canonical)
		}
		if _, ok := headerNames[key]; ok {
			return fmt.Errorf("%w: %s", ErrTrailerHeaderCollision, canonical)
		}
		if _, ok := declaredSet[key]; !ok {
			return fmt.Errorf("%w: %s", ErrUndeclaredTrailer, canonical)
		}
		trailerNames[key] = struct{}{}
	}

	for _, name := range declared {
		if _, ok := trailerNames[strings.ToLower(name)]; !ok {
			return fmt.Errorf("%w: %s", ErrMissingDeclaredTrailer, name)
		}
	}
	return nil
}

// SectionFromHTTPHeader converts a map-shaped header into an ordered section.
// Values for each field keep their slice order, and field names are visited in
// sorted order so the conversion is deterministic. A map cannot retain the
// original inter-name wire order; callers that need that property should build
// a Section from their parser directly.
func SectionFromHTTPHeader(header http.Header) Section {
	if len(header) == 0 {
		return nil
	}

	keys := make([]string, 0, len(header))
	for key := range header {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	total := 0
	for _, values := range header {
		total += len(values)
	}
	section := make(Section, 0, total)
	for _, key := range keys {
		canonical := textproto.CanonicalMIMEHeaderKey(key)
		if canonical == "" {
			canonical = key
		}
		for _, value := range header[key] {
			section = append(section, Field{Name: canonical, Value: value})
		}
	}
	return section
}

// HTTPHeader converts an ordered section into a map-shaped header. Values for
// duplicate names remain in section order.
func (section Section) HTTPHeader() http.Header {
	header := make(http.Header, len(section))
	for _, field := range section {
		header.Add(field.Name, field.Value)
	}
	return header
}

// MergeSections verifies the signature over the exact ordered header and
// trailer sections, applies the trailer policy, and returns a merged section
// with trailer fields appended. The Trailer declaration is removed because the
// result no longer has a separate trailer section. Nothing is returned unless
// both authentication and policy checks succeed.
func MergeSections(header, trailers Section, signature []byte, digest DigestFunc) (Section, error) {
	if err := ValidateSections(header, trailers); err != nil {
		return nil, err
	}
	if err := Verify(header, trailers, signature, digest); err != nil {
		return nil, err
	}

	merged := make(Section, 0, len(header)+len(trailers))
	for _, field := range header {
		if !strings.EqualFold(field.Name, TrailerHeader) {
			merged = append(merged, field)
		}
	}
	merged = append(merged, trailers...)
	return merged, nil
}

// MergeTrailers verifies the header and trailer sections with digest and, only
// on success, returns a new header map containing the authenticated trailers.
// The declaration field is removed from the merged map. Neither input map is
// modified; on any error the result is nil.
//
// MergeTrailers requires the header to contain an explicit Trailer
// declaration whenever a trailer section is present. When net/http has already
// removed that field, use
// MergeTrailersWithDeclaration with a declaration captured before the body was
// read.
//
// Because http.Header is a map, MergeTrailers signs field names in sorted order
// while preserving the order of repeated values. Use Sign/Verify or
// MergeSections with parsed Sections if exact inter-name wire order must be
// bound.
func MergeTrailers(header, trailers http.Header, signature []byte, digest DigestFunc) (http.Header, error) {
	if err := ValidateTrailers(header, trailers); err != nil {
		return nil, err
	}
	if err := VerifyHTTP(header, trailers, signature, digest); err != nil {
		return nil, err
	}

	merged := canonicalHeader(header)
	trailerValues := canonicalHeaderValues(trailers)
	for _, name := range sortedKeys(trailerValues) {
		for _, value := range trailerValues[name] {
			merged.Add(name, value)
		}
	}
	for key := range merged {
		if strings.EqualFold(key, TrailerHeader) {
			delete(merged, key)
		}
	}
	return merged, nil
}

// CaptureDeclaredTrailers records the trailer names that net/http parsed from
// the Trailer header. Call it before reading the body: at that point the
// Trailer map contains only predeclared names, with nil values. After the body
// is read, net/http may merge undeclared trailer fields into the same map.
//
// The returned names are canonicalized and sorted to match the HTTP signature
// helpers.
func CaptureDeclaredTrailers(trailers http.Header) []string {
	names := make([]string, 0, len(trailers))
	for name := range trailers {
		names = append(names, name)
	}
	return canonicalTrailerNames(names)
}

// MergeTrailersWithDeclaration is the stream-friendly form of MergeTrailers
// for net/http servers. declared should come from CaptureDeclaredTrailers
// before the body is read; it is authoritative even after net/http has removed
// the Trailer header and merged undeclared trailer fields into trailers. If
// declared is empty, an explicit Trailer header is used. Trailer fields not in
// the resulting declaration are rejected.
//
// The function verifies the signature and returns a new merged header only on
// success. Neither input map is modified.
func MergeTrailersWithDeclaration(header, trailers http.Header, declared []string, signature []byte, digest DigestFunc) (http.Header, error) {
	prepared := canonicalHeader(header)
	existing, err := DeclaredTrailers(prepared)
	if err != nil {
		return nil, err
	}
	for key := range prepared {
		if strings.EqualFold(key, TrailerHeader) {
			delete(prepared, key)
		}
	}

	for _, name := range declared {
		if _, err := canonicalFieldName(strings.TrimSpace(name)); err != nil {
			return nil, err
		}
	}
	names := canonicalTrailerNames(declared)
	if len(names) == 0 {
		names = canonicalTrailerNames(existing)
	}
	if len(names) == 0 {
		if trailerNames := sortedKeys(canonicalHeaderValues(trailers)); len(trailerNames) > 0 {
			return nil, fmt.Errorf("%w: %s", ErrUndeclaredTrailer, trailerNames[0])
		}
	} else {
		prepared.Set(TrailerHeader, strings.Join(names, ", "))
	}
	return MergeTrailers(prepared, trailers, signature, digest)
}

func normalizeTrailerName(name string) (string, error) {
	canonical, err := canonicalFieldName(strings.TrimSpace(name))
	if err != nil {
		return "", err
	}
	if IsForbiddenTrailerField(canonical) {
		return "", fmt.Errorf("%w: %s", ErrForbiddenTrailer, canonical)
	}
	return canonical, nil
}

func parseTrailerDeclarations(lines []string) ([]string, error) {
	declared := make([]string, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))

	for _, line := range lines {
		for _, raw := range strings.Split(line, ",") {
			name := strings.TrimSpace(raw)
			if name == "" {
				return nil, fmt.Errorf("%w: empty field name", ErrInvalidTrailerDeclaration)
			}
			canonical, err := normalizeTrailerName(name)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", ErrInvalidTrailerDeclaration, err)
			}
			key := strings.ToLower(canonical)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			declared = append(declared, canonical)
		}
	}
	return declared, nil
}

func canonicalFieldName(name string) (string, error) {
	if name == "" || !isHTTPToken(name) {
		return "", fmt.Errorf("%w: %q", ErrInvalidFieldName, name)
	}
	return textproto.CanonicalMIMEHeaderKey(name), nil
}

func validateFieldValue(value string) error {
	for i := 0; i < len(value); i++ {
		switch c := value[i]; {
		case c == '\t':
		case c < 0x20 || c == 0x7f:
			return fmt.Errorf("%w: contains a control character", ErrInvalidFieldValue)
		}
	}
	return nil
}

func validateHTTPHeader(header http.Header) error {
	for key, values := range header {
		canonical, err := canonicalFieldName(key)
		if err != nil {
			return err
		}
		for _, value := range values {
			if err := validateFieldValue(value); err != nil {
				return fmt.Errorf("%s: %w", canonical, err)
			}
		}
	}
	return nil
}

func isHTTPToken(value string) bool {
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c)):
		default:
			return false
		}
	}
	return value != ""
}

func canonicalHeaderValues(header http.Header) map[string][]string {
	if len(header) == 0 {
		return nil
	}

	keys := make([]string, 0, len(header))
	for key := range header {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	values := make(map[string][]string, len(header))
	for _, key := range keys {
		canonical := textproto.CanonicalMIMEHeaderKey(key)
		if canonical == "" {
			canonical = key
		}
		lower := strings.ToLower(canonical)
		values[lower] = append(values[lower], header[key]...)
	}
	return values
}

func canonicalHeader(header http.Header) http.Header {
	values := canonicalHeaderValues(header)
	out := make(http.Header, len(values))
	for _, name := range sortedKeys(values) {
		canonical := textproto.CanonicalMIMEHeaderKey(name)
		out[canonical] = append([]string(nil), values[name]...)
	}
	return out
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
