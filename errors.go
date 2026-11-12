package netsec

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

// redactedMarker replaces every sensitive value retained inside the
// service. It is deliberately short and constant so redacted log lines do
// not reintroduce a length side channel that mirrors the secret length.
const redactedMarker = "[REDACTED]"

// SensitiveField is a secret-bearing piece of context that an internal
// component may attach for local diagnostics. The value is held only for
// redacted logging and never reaches any error text, callback, or wire
// representation.
type SensitiveField struct {
	// Name identifies the field in redacted logs; it must not contain the
	// secret itself.
	Name string
	// value is unexported so embedding hosts cannot read it through the API.
	value string
}

// NewSensitiveField stores a secret-bearing value for internal diagnostics.
func NewSensitiveField(name, value string) SensitiveField {
	return SensitiveField{Name: name, value: value}
}

// internalError is the detailed, in-process failure representation. It
// carries the failing stage and secret context, but none of its rendering
// methods expose those details: Error and Format return a fixed string and
// Unwrap is deliberately absent so wrapping chains cannot be walked across
// the boundary via errors.Is/As.
type internalError struct {
	stage      internalCategory
	message    string
	sensitive  []SensitiveField
	underlying error
}

// NewInternalError builds a detailed internal failure. message should
// describe the condition without embedding secrets; secrets must be passed
// as SensitiveField values instead.
func NewInternalError(stage string, message string, underlying error, sensitive ...SensitiveField) error {
	cat := internalCategory(stage)
	if !cat.isInternal() {
		cat = stageUnknown
	}
	return &internalError{
		stage:      cat,
		message:    message,
		sensitive:  append([]SensitiveField(nil), sensitive...),
		underlying: underlying,
	}
}

// Error implements error. The text is intentionally constant: stage,
// message, wrapped error and secrets are never rendered here.
func (e *internalError) Error() string {
	return "netsec: internal failure"
}

// Format implements fmt.Formatter defensively. Any formatting verb (including
// %+v, %#v and width/width-precision variants) yields the same fixed string,
// so a host-side formatting callback cannot extract internal detail.
func (e *internalError) Format(state fmt.State, verb rune) {
	_, _ = state.Write([]byte(e.Error()))
}

// redactedLog renders the diagnostic view consumed only by the in-process
// Logger. Secret values are replaced by a constant marker and underlying
// errors render through their own Error/Format path, which is itself
// sanitized for *internalError values.
func (e *internalError) redactedLog() map[string]any {
	entry := map[string]any{
		"internal_category": string(e.stage),
		"message":           e.message,
		"sensitive_fields":  e.redactedFields(),
	}
	if e.underlying != nil {
		entry["cause"] = e.underlying.Error()
	}
	return entry
}

func (e *internalError) redactedFields() map[string]any {
	fields := make(map[string]any, len(e.sensitive))
	for _, field := range e.sensitive {
		fields[field.Name] = redactedMarker
	}
	return fields
}

// correlationIDLen is the fixed size, in hex characters, of the
// correlation identifier carried by every public error.
const correlationIDLen = 32

// wirePrefix and wireLen define the constant-length on-wire frame of a
// public error: 'N' + 4-character category + 32 hex characters, so every
// failure response has byte-identical length regardless of its class.
const (
	wirePrefix = "N"
	wireLen    = len(wirePrefix) + 4 + correlationIDLen
)

// Sentinel errors exported for errors.Is comparisons on the host side.
// All four carry the same message length.
var (
	ErrHandshakeFailed = errors.New("netsec: handshake failed")
	ErrAuthFailed      = errors.New("netsec: authentication failed")
	ErrUnavailable     = errors.New("netsec: service unavailable")
	ErrInternal        = errors.New("netsec: internal failure")
)

func publicSentinel(c PublicCategory) error {
	switch c {
	case PublicHandshakeFailed:
		return ErrHandshakeFailed
	case PublicAuthFailed:
		return ErrAuthFailed
	case PublicUnavailable:
		return ErrUnavailable
	default:
		return ErrInternal
	}
}

// PublicError is the only failure shape allowed to cross the host boundary.
// Both fields have fixed widths and no internal error is wrapped, so its
// text, JSON and wire encodings are all constant-length for a given
// correlation identifier.
type PublicError struct {
	Category      PublicCategory
	CorrelationID string
}

func newCorrelationID() string {
	buf := make([]byte, correlationIDLen/2)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failure means the platform cannot provide a secure
		// source of randomness; fail loudly rather than guess.
		panic("netsec: cannot generate correlation id: " + err.Error())
	}
	return hex.EncodeToString(buf)
}

// Error implements error with a fixed-width category code and no detail.
func (e PublicError) Error() string {
	return fmt.Sprintf("netsec: %s: ref=%s", e.Category, e.CorrelationID)
}

// String implements fmt.Stringer with the same fixed shape as Error.
func (e PublicError) String() string {
	return e.Error()
}

// Format implements fmt.Formatter so that %+v, %#v and friends cannot reach
// any hidden detail; every verb renders the identical fixed string.
func (e PublicError) Format(state fmt.State, verb rune) {
	_, _ = state.Write([]byte(e.Error()))
}

// Is enables errors.Is(publicErr, ErrAuthFailed) style checks on the host
// side without exposing any wrapped internal error.
func (e PublicError) Is(target error) bool {
	return target == publicSentinel(e.Category)
}

// MarshalText emits the fixed-length wire frame "N<code><correlation>".
func (e PublicError) MarshalText() ([]byte, error) {
	return []byte(e.wireFrame()), nil
}

// MarshalJSON emits a fixed-shape object. Because both fields are
// fixed-width, every category serializes to the same JSON length.
func (e PublicError) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`{"code":%q,"ref":%q}`, e.Category, e.CorrelationID)), nil
}

func (e PublicError) wireFrame() string {
	return wirePrefix + string(e.Category) + e.CorrelationID
}

// Publicize maps an arbitrary internal error onto the fixed public shape.
// Errors produced inside this package are mapped through the stage
// allow-list; anything else collapses to PublicInternal so an unknown
// failure type can never carry its message across the boundary.
func Publicize(err error) PublicError {
	pub := PublicError{
		Category:      PublicInternal,
		CorrelationID: newCorrelationID(),
	}
	var detailed *internalError
	if errors.As(err, &detailed) {
		pub.Category = detailed.stage.public()
	}
	if !pub.Category.IsValid() {
		pub.Category = PublicInternal
	}
	return pub
}
