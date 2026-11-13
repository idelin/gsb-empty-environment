package netsec

import "reflect"

// ValidateSequence applies all events in order and reports the first invalid
// transition. It accepts string-based handshake states, including Event and
// State aliases.
func ValidateSequence[T ~string](events []T) error {
	m := NewStateMachine()
	for _, event := range events {
		if err := m.Step(event); err != nil {
			return err
		}
	}
	return m.Validate()
}

// ValidateClientAuthSequence, ValidateHandshakeSequence, and
// ValidateClientCertificateSequence are aliases for ValidateSequence.
func ValidateClientAuthSequence[T ~string](events []T) error {
	return ValidateSequence(events)
}

func ValidateHandshakeSequence[T ~string](events []T) error {
	return ValidateSequence(events)
}

func ValidateClientCertificateSequence[T ~string](events []T) error {
	return ValidateSequence(events)
}

// Validate applies events supplied either individually or as a single slice.
// It is the convenience form for callers whose state values are not statically
// typed as strings.
func Validate(events ...any) error {
	m := NewStateMachine()
	for _, event := range events {
		rv := reflect.ValueOf(event)
		if rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) {
			for i := 0; i < rv.Len(); i++ {
				if err := m.Step(rv.Index(i).Interface()); err != nil {
					return err
				}
			}
			continue
		}
		if err := m.Step(event); err != nil {
			return err
		}
	}
	return m.Validate()
}

// CheckSequence and CheckHandshakeSequence are aliases for ValidateSequence.
func CheckSequence[T ~string](events []T) error {
	return ValidateSequence(events)
}

func CheckHandshakeSequence[T ~string](events []T) error {
	return ValidateSequence(events)
}
