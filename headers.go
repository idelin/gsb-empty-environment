package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// TraceParent is a parsed W3C traceparent value.
type TraceParent struct {
	Version string
	TraceID string
	SpanID  string
	Flags   byte
	Sampled bool
}

// String formats the traceparent in canonical version 00 form.
func (tp TraceParent) String() string {
	flags := byte(0)
	if tp.Sampled {
		flags = 1
	}
	return fmt.Sprintf("%s-%s-%s-%02x", tp.Version, tp.TraceID, tp.SpanID, flags)
}

// ParseTraceParent strictly parses a W3C version 00 traceparent. Uppercase
// hex, zero identifiers, duplicate separators, and future versions are
// rejected so the wire representation has one unambiguous meaning.
func ParseTraceParent(value string) (TraceParent, error) {
	if len(value) > MaxTraceParentBytes {
		return TraceParent{}, headerError(HeaderTraceParent, ErrHeaderTooLong, "exceeds 128 bytes")
	}
	parts := strings.Split(value, "-")
	if len(parts) != 4 {
		return TraceParent{}, headerError(HeaderTraceParent, ErrMalformedHeader, "expected four fields")
	}
	if parts[0] != "00" {
		return TraceParent{}, headerError(HeaderTraceParent, ErrUnsupportedVersion, parts[0])
	}
	if len(value) != 55 {
		return TraceParent{}, headerError(HeaderTraceParent, ErrMalformedHeader, "version 00 must be 55 bytes")
	}
	if !validTraceID(parts[1]) {
		return TraceParent{}, headerError(HeaderTraceParent, ErrMalformedHeader, "invalid trace ID")
	}
	if !validSpanID(parts[2]) {
		return TraceParent{}, headerError(HeaderTraceParent, ErrMalformedHeader, "invalid span ID")
	}
	if len(parts[3]) != 2 || !isLowerHex(parts[3]) {
		return TraceParent{}, headerError(HeaderTraceParent, ErrMalformedHeader, "invalid flags")
	}
	flags, err := strconv.ParseUint(parts[3], 16, 8)
	if err != nil {
		return TraceParent{}, headerError(HeaderTraceParent, ErrMalformedHeader, "invalid flags")
	}
	return TraceParent{
		Version: "00",
		TraceID: parts[1],
		SpanID:  parts[2],
		Flags:   byte(flags),
		Sampled: flags&1 == 1,
	}, nil
}

// FormatTraceParent builds a canonical version 00 traceparent after validating
// both identifiers.
func FormatTraceParent(traceID, spanID string, sampled bool) (string, error) {
	if !validTraceID(traceID) {
		return "", fmt.Errorf("%w: invalid trace ID", ErrInvalidContext)
	}
	if !validSpanID(spanID) {
		return "", fmt.Errorf("%w: invalid span ID", ErrInvalidContext)
	}
	flags := "00"
	if sampled {
		flags = "01"
	}
	return "00-" + traceID + "-" + spanID + "-" + flags, nil
}

// NormalizeTraceState validates a W3C tracestate value, enforces its limits,
// and returns a canonical comma-separated representation.
func NormalizeTraceState(value string) (string, error) {
	if len(value) > MaxTraceStateBytes {
		return "", headerError(HeaderTraceState, ErrHeaderTooLong, "exceeds 512 bytes")
	}
	if value == "" {
		return "", headerError(HeaderTraceState, ErrMalformedHeader, "empty value")
	}
	parts := strings.Split(value, ",")
	if len(parts) > MaxTraceStateMembers {
		return "", headerError(HeaderTraceState, ErrMalformedHeader, "too many members")
	}
	seen := make(map[string]struct{}, len(parts))
	normalized := make([]string, 0, len(parts))
	for _, raw := range parts {
		member := strings.Trim(raw, " \t")
		if member == "" {
			return "", headerError(HeaderTraceState, ErrMalformedHeader, "empty member")
		}
		eq := strings.IndexByte(member, '=')
		if eq <= 0 || eq == len(member)-1 {
			return "", headerError(HeaderTraceState, ErrMalformedHeader, "member must be key=value")
		}
		if strings.IndexByte(member[eq+1:], '=') >= 0 {
			return "", headerError(HeaderTraceState, ErrMalformedHeader, "member has multiple equals")
		}
		key := strings.Trim(member[:eq], " \t")
		val := strings.Trim(member[eq+1:], " \t")
		if key == "" || val == "" {
			return "", headerError(HeaderTraceState, ErrMalformedHeader, "empty key or value")
		}
		if !validTraceStateKey(key) {
			return "", headerError(HeaderTraceState, ErrMalformedHeader, "invalid key")
		}
		if len(val) > 256 || !validTraceStateValue(val) {
			return "", headerError(HeaderTraceState, ErrMalformedHeader, "invalid value")
		}
		if _, exists := seen[key]; exists {
			return "", headerError(HeaderTraceState, ErrMalformedHeader, "duplicate key")
		}
		seen[key] = struct{}{}
		normalized = append(normalized, key+"="+val)
	}
	out := strings.Join(normalized, ",")
	if len(out) > MaxTraceStateBytes {
		return "", headerError(HeaderTraceState, ErrHeaderTooLong, "normalized value exceeds 512 bytes")
	}
	return out, nil
}

// ValidateTraceState validates a tracestate header value.
func ValidateTraceState(value string) error {
	_, err := NormalizeTraceState(value)
	return err
}

func validTraceStateKey(key string) bool {
	if len(key) == 0 || len(key) > 256 {
		return false
	}
	parts := strings.Split(key, "@")
	if len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for i := 0; i < len(part); i++ {
			c := part[i]
			if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
				continue
			}
			if i == 0 {
				return false
			}
			switch c {
			case '_', '-', '*', '/':
				continue
			default:
				return false
			}
		}
	}
	return true
}

func validTraceStateValue(value string) bool {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c < 0x20 || c > 0x7e || c == ',' || c == '=' {
			return false
		}
	}
	return true
}

type tracingHeaders struct {
	values  map[string]string
	present bool
}

func collectTracingHeaders(h http.Header, maxBytes int) (tracingHeaders, error) {
	result := tracingHeaders{values: make(map[string]string, 6)}
	if h == nil {
		return result, nil
	}
	known := map[string]string{
		HeaderTraceParent: strings.ToLower(HeaderTraceParent),
		HeaderTraceState:  strings.ToLower(HeaderTraceState),
		HeaderTenant:      strings.ToLower(HeaderTenant),
		HeaderOrigin:      strings.ToLower(HeaderOrigin),
		HeaderTimestamp:   strings.ToLower(HeaderTimestamp),
		HeaderSignature:   strings.ToLower(HeaderSignature),
	}
	total := 0
	for name, values := range h {
		canonicalName, ok := lookupFold(known, name)
		if !ok {
			continue
		}
		result.present = true
		if len(values) != 1 {
			if len(values) == 0 {
				return tracingHeaders{}, headerError(canonicalName, ErrMalformedHeader, "header has no value")
			}
			return tracingHeaders{}, headerError(canonicalName, ErrDuplicateHeader, "header occurs more than once")
		}
		normalized := strings.ToLower(canonicalName)
		if _, exists := result.values[normalized]; exists {
			return tracingHeaders{}, headerError(canonicalName, ErrDuplicateHeader, "header occurs more than once")
		}
		total += len(name) + len(values[0])
		result.values[normalized] = values[0]
	}
	if total > maxBytes {
		return tracingHeaders{}, headerError("trace context", ErrHeaderTooLong, "aggregate tracing headers exceed limit")
	}
	return result, nil
}

func lookupFold(values map[string]string, key string) (string, bool) {
	for candidate, canonical := range values {
		if strings.EqualFold(candidate, key) {
			return canonical, true
		}
	}
	return "", false
}

func (t *Tracer) extractHeaders(h http.Header, binding Binding, hasBinding bool) (*RemoteContext, error) {
	headers, err := collectTracingHeaders(h, t.maxHeaderBytes)
	if err != nil {
		return nil, err
	}
	if !headers.present {
		return nil, nil
	}

	tpValue, ok := headers.values[strings.ToLower(HeaderTraceParent)]
	if !ok {
		return nil, headerError(HeaderTraceParent, ErrMissingTraceParent, "missing traceparent")
	}
	tp, err := ParseTraceParent(tpValue)
	if err != nil {
		return nil, err
	}

	traceState := ""
	traceStateRaw := ""
	if raw, ok := headers.values[strings.ToLower(HeaderTraceState)]; ok {
		if len(raw) > t.maxTraceState {
			return nil, headerError(HeaderTraceState, ErrHeaderTooLong, "exceeds configured limit")
		}
		traceStateRaw = raw
		traceState, err = NormalizeTraceState(raw)
		if err != nil {
			return nil, err
		}
	}

	tenant, ok := headers.values[strings.ToLower(HeaderTenant)]
	if !ok {
		return nil, headerError(HeaderTenant, ErrMalformedHeader, "missing tenant")
	}
	origin, ok := headers.values[strings.ToLower(HeaderOrigin)]
	if !ok {
		return nil, headerError(HeaderOrigin, ErrMalformedHeader, "missing origin")
	}
	if len(tenant) > MaxBindingBytes {
		return nil, headerError(HeaderTenant, ErrHeaderTooLong, "exceeds 128 bytes")
	}
	if len(origin) > MaxBindingBytes {
		return nil, headerError(HeaderOrigin, ErrHeaderTooLong, "exceeds 128 bytes")
	}
	if err := validateBindingValue("tenant", tenant); err != nil {
		return nil, headerError(HeaderTenant, ErrMalformedHeader, err.Error())
	}
	if err := validateBindingValue("origin", origin); err != nil {
		return nil, headerError(HeaderOrigin, ErrMalformedHeader, err.Error())
	}

	timestampValue, ok := headers.values[strings.ToLower(HeaderTimestamp)]
	if !ok {
		return nil, headerError(HeaderTimestamp, ErrMalformedHeader, "missing timestamp")
	}
	timestamp, err := parseTimestamp(timestampValue)
	if err != nil {
		return nil, err
	}
	if now := t.now().UTC(); now.Sub(timestamp) > t.maxAge {
		return nil, headerError(HeaderTimestamp, ErrExpiredContext, "signature age exceeds policy")
	} else if timestamp.Sub(now) > t.futureSkew {
		return nil, headerError(HeaderTimestamp, ErrFutureContext, "timestamp exceeds allowed clock skew")
	}

	signatureValue, ok := headers.values[strings.ToLower(HeaderSignature)]
	if !ok {
		return nil, headerError(HeaderSignature, ErrMalformedHeader, "missing signature")
	}
	keyID, providedSignature, err := parseSignature(signatureValue)
	if err != nil {
		return nil, err
	}
	if keyID != t.keyID {
		return nil, headerError(HeaderSignature, ErrInvalidSignature, "unknown key ID")
	}
	expectedSignature := t.signatureBytes(tpValue, traceStateRaw, tenant, origin, timestampValue)
	if !hmac.Equal(providedSignature, expectedSignature) {
		return nil, headerError(HeaderSignature, ErrInvalidSignature, "HMAC mismatch")
	}

	if !hasBinding {
		return nil, headerError("binding", ErrMissingBinding, "no authenticated tenant and origin")
	}
	if tenant != binding.TenantID || origin != binding.Origin {
		return nil, headerError("binding", ErrBindingMismatch, "signed identity does not match request identity")
	}

	return &RemoteContext{
		TraceID:    tp.TraceID,
		SpanID:     tp.SpanID,
		TenantID:   tenant,
		Origin:     origin,
		KeyID:      keyID,
		TraceState: traceState,
		Sampled:    tp.Sampled,
		Timestamp:  timestamp,
	}, nil
}

func parseTimestamp(value string) (time.Time, error) {
	if len(value) == 0 {
		return time.Time{}, headerError(HeaderTimestamp, ErrMalformedHeader, "invalid length")
	}
	if len(value) > MaxTimestampBytes {
		return time.Time{}, headerError(HeaderTimestamp, ErrHeaderTooLong, "exceeds 20 bytes")
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return time.Time{}, headerError(HeaderTimestamp, ErrMalformedHeader, "must be decimal Unix seconds")
		}
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds < 0 {
		return time.Time{}, headerError(HeaderTimestamp, ErrMalformedHeader, "invalid Unix time")
	}
	return time.Unix(seconds, 0).UTC(), nil
}

func parseSignature(value string) (string, []byte, error) {
	if len(value) == 0 {
		return "", nil, headerError(HeaderSignature, ErrMalformedHeader, "empty signature")
	}
	if len(value) > MaxSignatureBytes {
		return "", nil, headerError(HeaderSignature, ErrHeaderTooLong, "exceeds 128 bytes")
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] != signatureVersion {
		return "", nil, headerError(HeaderSignature, ErrMalformedHeader, "expected v1.<key-id>.<mac>")
	}
	if !keyIDPattern.MatchString(parts[1]) {
		return "", nil, headerError(HeaderSignature, ErrMalformedHeader, "invalid key ID")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", nil, headerError(HeaderSignature, ErrMalformedHeader, "invalid base64url MAC")
	}
	if len(raw) != sha256.Size {
		return "", nil, headerError(HeaderSignature, ErrMalformedHeader, "invalid MAC length")
	}
	return parts[1], raw, nil
}

func (t *Tracer) sign(traceParent, traceState, tenant, origin, timestamp string) string {
	mac := t.signatureBytes(traceParent, traceState, tenant, origin, timestamp)
	return signatureVersion + "." + t.keyID + "." + base64.RawURLEncoding.EncodeToString(mac)
}

func (t *Tracer) signatureBytes(traceParent, traceState, tenant, origin, timestamp string) []byte {
	mac := hmac.New(sha256.New, t.key)
	for _, part := range []string{
		"netsec-trace-context-v1",
		t.keyID,
		traceParent,
		traceState,
		tenant,
		origin,
		timestamp,
	} {
		mac.Write([]byte(strconv.Itoa(len(part))))
		mac.Write([]byte{':'})
		mac.Write([]byte(part))
		mac.Write([]byte{'\n'})
	}
	return mac.Sum(nil)
}

func clearTracingHeaders(h http.Header) {
	if h == nil {
		return
	}
	names := []string{
		HeaderTraceParent,
		HeaderTraceState,
		HeaderTenant,
		HeaderOrigin,
		HeaderTimestamp,
		HeaderSignature,
	}
	for name := range h {
		for _, tracingName := range names {
			if strings.EqualFold(name, tracingName) {
				delete(h, name)
				break
			}
		}
	}
}

// ErrMissingTraceParent lets callers distinguish an absent traceparent from
// other malformed tracing headers. It also matches ErrMalformedHeader.
var ErrMissingTraceParent = fmt.Errorf("%w: missing traceparent", ErrMalformedHeader)
