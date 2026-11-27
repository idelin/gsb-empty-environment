package dnssec

// Status classifies the security outcome of a validation operation, mirroring
// the Secure / Insecure / Bogus states of RFC 4035 section 4.3.
type Status int

const (
	StatusUnknown Status = iota
	StatusSecure
	StatusInsecure
	StatusBogus
)

func (s Status) String() string {
	switch s {
	case StatusSecure:
		return "Secure"
	case StatusInsecure:
		return "Insecure"
	case StatusBogus:
		return "Bogus"
	default:
		return "Unknown"
	}
}

// BogusError marks a hard validation failure: the data is signed or expected
// to be signed but cannot be validated, so a resolver must return SERVFAIL.
type BogusError struct {
	Reason string
}

func (e *BogusError) Error() string { return "dnssec bogus: " + e.Reason }

// ErrBogus constructs a BogusError.
func ErrBogus(reason string) error { return &BogusError{Reason: reason} }

// IsBogus reports whether err is (or wraps) a BogusError.
func IsBogus(err error) bool {
	for err != nil {
		if _, ok := err.(*BogusError); ok {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
