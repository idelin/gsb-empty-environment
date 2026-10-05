package rowconv

import "fmt"

// Mode selects how inexact narrowing conversions are handled.
type Mode string

const (
	// ModeReject fails the row when a value cannot be represented exactly.
	ModeReject Mode = "reject"
	// ModeHalfEven rounds to the nearest representable value, ties to even.
	ModeHalfEven Mode = "half_even"
)

func normalizeMode(m Mode) (Mode, error) {
	switch m {
	case "", ModeReject:
		return ModeReject, nil
	case ModeHalfEven:
		return ModeHalfEven, nil
	}
	return "", fmt.Errorf("unknown mode %q (want %q or %q)", m, ModeReject, ModeHalfEven)
}

// Config controls narrowing behavior for the whole batch.
type Config struct {
	// DecimalMode applies when the target scale is smaller than the source
	// scale and the coefficient is not divisible by 10^diff.
	DecimalMode Mode `json:"decimal_mode"`
	// TimestampMode applies when the input carries sub-microsecond digits.
	TimestampMode Mode `json:"timestamp_mode"`
}

func (c Config) normalized() (Config, error) {
	d, err := normalizeMode(c.DecimalMode)
	if err != nil {
		return Config{}, fmt.Errorf("decimal_mode: %v", err)
	}
	t, err := normalizeMode(c.TimestampMode)
	if err != nil {
		return Config{}, fmt.Errorf("timestamp_mode: %v", err)
	}
	return Config{DecimalMode: d, TimestampMode: t}, nil
}

// Field describes one column of a schema.
type Field struct {
	Name     string `json:"name"`
	Kind     Kind   `json:"kind"`
	Required bool   `json:"required,omitempty"`
	// Min and Max bound int values, decimal coefficients, and timestamp
	// UTC microseconds (target side only).
	Min *int64 `json:"min,omitempty"`
	Max *int64 `json:"max,omitempty"`
	// Scale is the fixed-point scale for decimal fields (nil means 0).
	Scale *int32 `json:"scale,omitempty"`
	// MaxBytes bounds string and bytes fields (target side only).
	MaxBytes *int64 `json:"max_bytes,omitempty"`
	// Default is used when the source key is absent (not when it is null).
	// It is expressed in target terms and validated like any other value.
	Default *Value `json:"default,omitempty"`
}

func (f Field) scale() int32 {
	if f.Scale == nil {
		return 0
	}
	return *f.Scale
}

func (f Field) validate() error {
	if f.Name == "" {
		return fmt.Errorf("field with empty name")
	}
	if !validKind(f.Kind) {
		return fmt.Errorf("field %q: unknown kind %q", f.Name, f.Kind)
	}
	if f.Min != nil && f.Max != nil && *f.Min > *f.Max {
		return fmt.Errorf("field %q: min > max", f.Name)
	}
	if f.Scale != nil && *f.Scale < 0 {
		return fmt.Errorf("field %q: negative scale", f.Name)
	}
	if f.MaxBytes != nil && *f.MaxBytes < 0 {
		return fmt.Errorf("field %q: negative max_bytes", f.Name)
	}
	return nil
}

// Schema is an ordered list of fields.
type Schema struct {
	Fields []Field `json:"fields"`
}

func (s Schema) field(name string) (Field, bool) {
	for _, f := range s.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

func (s Schema) validate() error {
	seen := map[string]bool{}
	for _, f := range s.Fields {
		if err := f.validate(); err != nil {
			return err
		}
		if seen[f.Name] {
			return fmt.Errorf("duplicate field %q", f.Name)
		}
		seen[f.Name] = true
	}
	return nil
}

// Error is a structured, per-field conversion failure.
type Error struct {
	Field string `json:"field"`
	Code  string `json:"code"`
	Msg   string `json:"message"`
}

func (e *Error) Error() string {
	if e.Field == "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Msg)
	}
	return fmt.Sprintf("field %q: %s: %s", e.Field, e.Code, e.Msg)
}

// Error codes.
const (
	CodeMissingRequired    = "missing_required"
	CodeNullNotAllowed     = "null_not_allowed"
	CodeUnknownSourceField = "unknown_source_field"
	CodeKindMismatch       = "kind_mismatch"
	CodeTypeMismatch       = "type_mismatch"
	CodeInvalidUTF8        = "invalid_utf8"
	CodeTooLong            = "too_long"
	CodeOutOfRange         = "out_of_range"
	CodeOverflow           = "overflow"
	CodeInexact            = "inexact"
	CodeSubmicrosecond     = "submicrosecond"
	CodeBadTimestamp       = "bad_timestamp"
	CodeBadDefault         = "bad_default"
)
