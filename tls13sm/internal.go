package tls13sm

import (
	"errors"
	"fmt"
)

func errorIs(err, target error) bool { return errors.Is(err, target) }

func errHRR(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrHRRRejected, fmt.Sprintf(format, args...))
}

func errIllegal(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrIllegalMessage, fmt.Sprintf(format, args...))
}

func errProto(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrProtocol, fmt.Sprintf(format, args...))
}

func errState(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrState, fmt.Sprintf(format, args...))
}

func equalConstantTime(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
