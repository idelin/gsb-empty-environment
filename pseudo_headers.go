package netsec

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Header is one field in an HTTP/2 header block.
//
// Pseudo-header names include their leading colon, for example ":path".
// Callers must preserve the order in which the fields appeared on the wire.
type Header struct {
	Name  string
	Value string
}

// PseudoHeader, Field, and HeaderField are alternate names for Header.
type (
	PseudoHeader = Header
	Field        = Header
	HeaderField  = Header
)

// HeaderList is an ordered HTTP/2 header block.
type HeaderList []Header

// RequestPseudoHeaders describes the four request pseudo-header fields.
// Authority is optional.  The other fields are required for ordinary
// requests.
type RequestPseudoHeaders struct {
	Method    string
	Scheme    string
	Authority string
	Path      string
}

// PseudoHeaders and Request are alternate names for RequestPseudoHeaders.
type (
	PseudoHeaders = RequestPseudoHeaders
	Request       = RequestPseudoHeaders
)

// Validation failures.  Use errors.Is to classify a returned error.
var (
	ErrInvalidHeader          = errors.New("netsec: invalid header block")
	ErrDuplicatePseudoHeader  = errors.New("netsec: duplicate pseudo-header")
	ErrPseudoHeaderAfterField = errors.New("netsec: pseudo-header after regular header")
	ErrPseudoHeaderOrder      = errors.New("netsec: invalid pseudo-header order")
	ErrMissingPseudoHeader    = errors.New("netsec: missing required pseudo-header")
	ErrUnknownPseudoHeader    = errors.New("netsec: unknown request pseudo-header")
	ErrEmptyPseudoHeader      = errors.New("netsec: empty request pseudo-header")
	ErrUnsupportedHeaderInput = errors.New("netsec: unsupported header input")
)

// Alternate names for the validation failures, kept for callers that spell
// them out in full.
var (
	ErrDuplicatePseudoHeaders   = ErrDuplicatePseudoHeader
	ErrPseudoHeaderAfterRegular = ErrPseudoHeaderAfterField
	ErrInvalidPseudoHeaderOrder = ErrPseudoHeaderOrder
	ErrMissingPseudoHeaderField = ErrMissingPseudoHeader
	ErrUnsupportedInput         = ErrUnsupportedHeaderInput
)

// ValidationError identifies the field and position that made a header block
// invalid.  Index is -1 when the failure applies to the block as a whole.
type ValidationError struct {
	Index  int
	Header string
	Err    error
}

func (e *ValidationError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err == nil {
		return ErrInvalidHeader.Error()
	}
	switch {
	case e.Header != "" && e.Index >= 0:
		return fmt.Sprintf("%v: %q at index %d", e.Err, e.Header, e.Index)
	case e.Header != "":
		return fmt.Sprintf("%v: %q", e.Err, e.Header)
	case e.Index >= 0:
		return fmt.Sprintf("%v at index %d", e.Err, e.Index)
	default:
		return e.Err.Error()
	}
}

// Unwrap allows errors.Is and errors.As to reach the sentinel cause.
func (e *ValidationError) Unwrap() error {
	return e.Err
}

// Decision is the result of adjudicating a header block.
type Decision int

const (
	// Deny means the request must not be forwarded.
	Deny Decision = iota
	// Allow means the request may be forwarded.
	Allow
)

func (d Decision) String() string {
	if d == Allow {
		return "allow"
	}
	return "deny"
}

// ValidatePseudoHeaders validates an ordered HTTP/2 request header block.
//
// The single-argument form accepts any of the following:
//
//   - []Header, HeaderList, or []PseudoHeader
//   - []string containing alternating names and values, or a list of names
//   - [][2]string, or a slice of structs with Name/Value fields
//   - map[string][]string or map[string]string, including http.Header
//   - RequestPseudoHeaders
//
// The four-string form is ValidatePseudoHeaders(method, scheme, authority,
// path).  A nil error means the request is allowed.
func ValidatePseudoHeaders(args ...any) error {
	fields, err := coerceFields(args)
	if err != nil {
		return err
	}
	return validateOrdered(fields)
}

// Validate is an alias for ValidatePseudoHeaders.
func Validate(args ...any) error {
	return ValidatePseudoHeaders(args...)
}

// ValidateHeaders is an alias for ValidatePseudoHeaders.
func ValidateHeaders(args ...any) error {
	return ValidatePseudoHeaders(args...)
}

// ValidateHeaderBlock is an alias for ValidatePseudoHeaders.
func ValidateHeaderBlock(args ...any) error {
	return ValidatePseudoHeaders(args...)
}

// ValidateRequestHeaders is an alias for ValidatePseudoHeaders.
func ValidateRequestHeaders(args ...any) error {
	return ValidatePseudoHeaders(args...)
}

// ValidateRequestPseudoHeaders is an alias for ValidatePseudoHeaders.
func ValidateRequestPseudoHeaders(args ...any) error {
	return ValidatePseudoHeaders(args...)
}

// ValidateRequest is an alias for ValidatePseudoHeaders.
func ValidateRequest(args ...any) error {
	return ValidatePseudoHeaders(args...)
}

// ValidateHTTP2Headers is an alias for ValidatePseudoHeaders.
func ValidateHTTP2Headers(args ...any) error {
	return ValidatePseudoHeaders(args...)
}

// ValidatePseudoHeaderOrder is an alias for ValidatePseudoHeaders.
func ValidatePseudoHeaderOrder(args ...any) error {
	return ValidatePseudoHeaders(args...)
}

// Adjudicate is an alias for ValidatePseudoHeaders.
func Adjudicate(args ...any) error {
	return ValidatePseudoHeaders(args...)
}

// AdjudicateRequest is an alias for ValidatePseudoHeaders.
func AdjudicateRequest(args ...any) error {
	return ValidatePseudoHeaders(args...)
}

// CheckPseudoHeaders reports whether an HTTP/2 request header block is valid.
func CheckPseudoHeaders(args ...any) bool {
	return ValidatePseudoHeaders(args...) == nil
}

// ValidPseudoHeaders reports whether an HTTP/2 request header block is valid.
func ValidPseudoHeaders(args ...any) bool {
	return CheckPseudoHeaders(args...)
}

// IsValidPseudoHeaders reports whether an HTTP/2 request header block is
// valid.
func IsValidPseudoHeaders(args ...any) bool {
	return CheckPseudoHeaders(args...)
}

// IsValidRequestHeaders reports whether an HTTP/2 request header block is
// valid.
func IsValidRequestHeaders(args ...any) bool {
	return CheckPseudoHeaders(args...)
}

// CheckRequestHeaders reports whether an HTTP/2 request header block is
// valid.
func CheckRequestHeaders(args ...any) bool {
	return CheckPseudoHeaders(args...)
}

// RequestAllowed reports whether an HTTP/2 request header block is valid.
func RequestAllowed(args ...any) bool {
	return CheckPseudoHeaders(args...)
}

// Allowed reports whether an HTTP/2 request header block is valid.
func Allowed(args ...any) bool {
	return CheckPseudoHeaders(args...)
}

// Permit reports whether an HTTP/2 request header block is valid.
func Permit(args ...any) bool {
	return CheckPseudoHeaders(args...)
}

// Decide adjudicates a header block and returns Allow or Deny.
func Decide(args ...any) Decision {
	if CheckPseudoHeaders(args...) {
		return Allow
	}
	return Deny
}

// Gateway is a stateless pseudo-header validator that can be embedded in a
// protocol gateway.
type Gateway struct{}

// NewGateway returns a pseudo-header validator.
func NewGateway() *Gateway {
	return &Gateway{}
}

// Validator and NewValidator are alternate names for Gateway.
type Validator = Gateway

// NewValidator returns a pseudo-header validator.
func NewValidator() *Validator {
	return &Validator{}
}

// ValidatePseudoHeaders validates an ordered HTTP/2 request header block.
func (*Gateway) ValidatePseudoHeaders(args ...any) error {
	return ValidatePseudoHeaders(args...)
}

// Validate validates an ordered HTTP/2 request header block.
func (*Gateway) Validate(args ...any) error {
	return ValidatePseudoHeaders(args...)
}

// ValidateRequest validates an ordered HTTP/2 request header block.
func (*Gateway) ValidateRequest(args ...any) error {
	return ValidatePseudoHeaders(args...)
}

// Adjudicate validates an ordered HTTP/2 request header block.
func (*Gateway) Adjudicate(args ...any) error {
	return ValidatePseudoHeaders(args...)
}

// Allowed reports whether an HTTP/2 request header block is valid.
func (*Gateway) Allowed(args ...any) bool {
	return CheckPseudoHeaders(args...)
}

// Check reports whether an HTTP/2 request header block is valid.
func (*Gateway) Check(args ...any) bool {
	return CheckPseudoHeaders(args...)
}

type field struct {
	header   Header
	hasValue bool
}

func validateOrdered(fields []field) error {
	// Classify duplicate, unknown, and malformed fields before looking at
	// ordering.  This keeps the most specific failure visible when a block
	// contains both a duplicate pseudo-header and a bad order.
	seen := make(map[string]bool, 4)
	for i, f := range fields {
		name := f.header.Name
		if name == "" {
			return invalidAt(i, name, ErrInvalidHeader)
		}

		if !strings.HasPrefix(name, ":") {
			continue
		}

		if !isRequestPseudoHeader(name) {
			return invalidAt(i, name, ErrUnknownPseudoHeader)
		}
		if seen[name] {
			return invalidAt(i, name, ErrDuplicatePseudoHeader)
		}
		if f.hasValue && f.header.Value == "" && name != ":authority" {
			return invalidAt(i, name, ErrEmptyPseudoHeader)
		}
		seen[name] = true
	}

	for _, required := range [...]string{":method", ":scheme", ":path"} {
		if !seen[required] {
			return invalidAt(-1, required, ErrMissingPseudoHeader)
		}
	}

	ordered := make(map[string]bool, 4)
	pseudoDone := false
	for i, f := range fields {
		name := f.header.Name
		if !strings.HasPrefix(name, ":") {
			pseudoDone = true
			continue
		}
		if pseudoDone {
			return invalidAt(i, name, ErrPseudoHeaderAfterField)
		}
		if name == ":path" && (!ordered[":method"] || !ordered[":scheme"]) {
			return invalidAt(i, name, ErrPseudoHeaderOrder)
		}
		ordered[name] = true
	}
	return nil
}

func isRequestPseudoHeader(name string) bool {
	switch name {
	case ":method", ":scheme", ":authority", ":path":
		return true
	default:
		return false
	}
}

func invalidAt(index int, name string, cause error) error {
	return &ValidationError{Index: index, Header: name, Err: cause}
}

func coerceFields(args []any) ([]field, error) {
	switch len(args) {
	case 1:
		return fieldsFromAny(args[0])
	case 3:
		method, okMethod := args[0].(string)
		scheme, okScheme := args[1].(string)
		path, okPath := args[2].(string)
		if !okMethod || !okScheme || !okPath {
			return nil, unsupportedInput("three arguments must be strings")
		}
		return fieldsFromValues(method, scheme, "", path), nil
	case 4:
		method, okMethod := args[0].(string)
		scheme, okScheme := args[1].(string)
		authority, okAuthority := args[2].(string)
		path, okPath := args[3].(string)
		if !okMethod || !okScheme || !okAuthority || !okPath {
			return nil, unsupportedInput("four arguments must be strings")
		}
		return fieldsFromValues(method, scheme, authority, path), nil
	case 0:
		return nil, unsupportedInput("missing header block")
	default:
		return nil, unsupportedInput("expected one header block argument or method, scheme, authority, path")
	}
}

func fieldsFromValues(method, scheme, authority, path string) []field {
	fields := []field{
		{header: Header{Name: ":method", Value: method}, hasValue: true},
		{header: Header{Name: ":scheme", Value: scheme}, hasValue: true},
	}
	if authority != "" {
		fields = append(fields, field{
			header:   Header{Name: ":authority", Value: authority},
			hasValue: true,
		})
	}
	return append(fields, field{
		header:   Header{Name: ":path", Value: path},
		hasValue: true,
	})
}

func fieldsFromAny(v any) ([]field, error) {
	switch value := v.(type) {
	case nil:
		return nil, unsupportedInput("nil header block")
	case []Header:
		return fieldsFromHeaders(value), nil
	case HeaderList:
		return fieldsFromHeaders(value), nil
	case RequestPseudoHeaders:
		return fieldsFromRequest(value), nil
	case []string:
		return fieldsFromStrings(value), nil
	case [][2]string:
		fields := make([]field, 0, len(value))
		for _, pair := range value {
			fields = append(fields, field{
				header:   Header{Name: pair[0], Value: pair[1]},
				hasValue: true,
			})
		}
		return fields, nil
	case map[string]string:
		values := make(map[string][]string, len(value))
		for name, item := range value {
			values[name] = []string{item}
		}
		return fieldsFromMap(values), nil
	case map[string][]string:
		return fieldsFromMap(value), nil
	}
	return fieldsFromReflection(v)
}

func fieldsFromHeaders(headers []Header) []field {
	fields := make([]field, 0, len(headers))
	for _, header := range headers {
		fields = append(fields, field{header: header, hasValue: true})
	}
	return fields
}

func fieldsFromRequest(request RequestPseudoHeaders) []field {
	return fieldsFromValues(
		request.Method,
		request.Scheme,
		request.Authority,
		request.Path,
	)
}

func fieldsFromStrings(values []string) []field {
	if len(values) > 0 && len(values)%2 == 0 && looksLikePairs(values) {
		fields := make([]field, 0, len(values)/2)
		for i := 0; i < len(values); i += 2 {
			fields = append(fields, field{
				header:   Header{Name: values[i], Value: values[i+1]},
				hasValue: true,
			})
		}
		return fields
	}

	fields := make([]field, 0, len(values))
	for _, name := range values {
		fields = append(fields, field{header: Header{Name: name}})
	}
	return fields
}

func looksLikePairs(values []string) bool {
	if len(values) == 0 || len(values)%2 != 0 {
		return false
	}
	if !strings.HasPrefix(values[0], ":") {
		return false
	}
	for i := 1; i < len(values); i += 2 {
		if strings.HasPrefix(values[i], ":") {
			return false
		}
	}
	return true
}

func fieldsFromMap(values map[string][]string) []field {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}

	// A map carries no wire order.  Put pseudo-headers in an order that
	// satisfies the request ordering rules, then validate the result so
	// duplicates and missing fields are still rejected.
	order := map[string]int{
		":method":    0,
		":scheme":    1,
		":authority": 2,
		":path":      3,
	}
	sort.Slice(names, func(i, j int) bool {
		left, leftPseudo := order[names[i]]
		right, rightPseudo := order[names[j]]
		switch {
		case leftPseudo && rightPseudo:
			return left < right
		case leftPseudo:
			return true
		case rightPseudo:
			return false
		default:
			return names[i] < names[j]
		}
	})

	fields := make([]field, 0, len(names))
	for _, name := range names {
		for _, value := range values[name] {
			fields = append(fields, field{
				header:   Header{Name: name, Value: value},
				hasValue: true,
			})
		}
	}
	return fields
}

func fieldsFromReflection(v any) ([]field, error) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return nil, unsupportedInput("nil header block")
	}

	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return nil, unsupportedInput("nil header block")
		}
		return fieldsFromReflection(rv.Elem().Interface())
	}

	if rv.Kind() == reflect.Struct {
		request, ok := requestFromStruct(rv)
		if !ok {
			return nil, unsupportedInput(fmt.Sprintf("unsupported struct %s", rv.Type()))
		}
		return fieldsFromRequest(request), nil
	}

	if rv.Kind() == reflect.Map {
		return fieldsFromReflectionMap(rv)
	}

	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, unsupportedInput(fmt.Sprintf("unsupported type %s", rv.Type()))
	}

	fields := make([]field, 0, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		item := rv.Index(i)
		header, ok := headerFromReflection(item)
		if !ok {
			return nil, unsupportedInput(fmt.Sprintf("unsupported element %s at index %d", item.Type(), i))
		}
		fields = append(fields, header)
	}
	return fields, nil
}

func fieldsFromReflectionMap(rv reflect.Value) ([]field, error) {
	if rv.Type().Key().Kind() != reflect.String {
		return nil, unsupportedInput(fmt.Sprintf("map key type %s is not string", rv.Type().Key()))
	}

	values := make(map[string][]string, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		name := iter.Key().String()
		value := iter.Value()
		switch value.Kind() {
		case reflect.String:
			values[name] = []string{value.String()}
		case reflect.Slice, reflect.Array:
			if value.Type().Elem().Kind() != reflect.String {
				return nil, unsupportedInput(fmt.Sprintf("map value type %s is not string or []string", value.Type()))
			}
			items := make([]string, 0, value.Len())
			for i := 0; i < value.Len(); i++ {
				items = append(items, value.Index(i).String())
			}
			values[name] = items
		case reflect.Interface:
			if value.IsNil() {
				continue
			}
			switch item := value.Interface().(type) {
			case string:
				values[name] = []string{item}
			case []string:
				values[name] = item
			default:
				return nil, unsupportedInput(fmt.Sprintf("unsupported map value %T for %q", item, name))
			}
		default:
			return nil, unsupportedInput(fmt.Sprintf("unsupported map value type %s", value.Type()))
		}
	}
	return fieldsFromMap(values), nil
}

func requestFromStruct(rv reflect.Value) (RequestPseudoHeaders, bool) {
	fields := map[string]string{}
	recognized := false
	rt := rv.Type()
	for i := 0; i < rv.NumField(); i++ {
		structField := rt.Field(i)
		if !structField.IsExported() || rv.Field(i).Kind() != reflect.String {
			continue
		}
		name := strings.ToLower(structField.Name)
		switch name {
		case "method", "scheme", "authority", "path":
			recognized = true
			fields[name] = rv.Field(i).String()
		}
	}

	request := RequestPseudoHeaders{
		Method:    fields["method"],
		Scheme:    fields["scheme"],
		Authority: fields["authority"],
		Path:      fields["path"],
	}
	return request, recognized
}

func headerFromReflection(rv reflect.Value) (field, bool) {
	if rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return field{}, false
		}
		return headerFromReflection(rv.Elem())
	}

	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return field{}, false
		}
		return headerFromReflection(rv.Elem())
	}

	if name, ok := stringFromReflection(rv); ok {
		return field{header: Header{Name: name}}, true
	}

	if rv.Kind() == reflect.Array || rv.Kind() == reflect.Slice {
		if rv.Len() == 2 {
			name, nameOK := stringFromReflection(rv.Index(0))
			value, valueOK := stringFromReflection(rv.Index(1))
			if !nameOK || !valueOK {
				return field{}, false
			}
			return field{
				header: Header{
					Name:  name,
					Value: value,
				},
				hasValue: true,
			}, true
		}
		return field{}, false
	}

	if rv.Kind() != reflect.Struct {
		return field{}, false
	}

	var name, value string
	rt := rv.Type()
	for i := 0; i < rv.NumField(); i++ {
		structField := rt.Field(i)
		if !structField.IsExported() {
			continue
		}
		fieldValue, ok := stringFromReflection(rv.Field(i))
		if !ok {
			continue
		}
		switch strings.ToLower(structField.Name) {
		case "name", "key", "header", "field":
			name = fieldValue
		case "value", "val":
			value = fieldValue
		}
	}
	if name == "" {
		return field{}, false
	}
	return field{header: Header{Name: name, Value: value}, hasValue: true}, true
}

func stringFromReflection(rv reflect.Value) (string, bool) {
	for rv.Kind() == reflect.Interface || rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return "", false
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.String {
		return "", false
	}
	return rv.String(), true
}

func unsupportedInput(detail string) error {
	return fmt.Errorf("%w: %s", ErrUnsupportedHeaderInput, detail)
}
