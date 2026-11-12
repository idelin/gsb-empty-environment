package netsec

import "fmt"

// exactOptionLen lists options whose concatenated value must have one fixed
// length. The check runs after concatenation, so an option split across the
// main, file and sname areas is validated as one whole value.
var exactOptionLen = map[uint8]int{
	OptSubnet:   4,
	OptServerID: 4,
	OptLease:    4,
	OptMsgType:  1,
}

// addressListUnit lists options whose concatenated value must be a non-empty
// sequence of 4-byte IPv4 addresses.
var addressListUnit = map[uint8]bool{
	OptRouter: true,
	OptDNS:    true,
}

// Validate runs every fixed-length and semantic check against the normalized
// options. Both the inspection layer and the business layer call this on the
// same Options value, so they can never disagree about concatenation.
func (options Options) Validate() error {
	for code, length := range exactOptionLen {
		value, ok := options[code]
		if !ok {
			continue
		}
		if len(value) != length {
			return fmt.Errorf("%w: option %d has length %d, want %d",
				ErrOptionLength, code, len(value), length)
		}
	}

	for code := range addressListUnit {
		value, ok := options[code]
		if !ok {
			continue
		}
		if len(value) == 0 || len(value)%4 != 0 {
			return fmt.Errorf("%w: option %d has length %d, want a positive multiple of 4",
				ErrOptionLength, code, len(value))
		}
	}

	if value, ok := options[OptMsgType]; ok && (value[0] < 1 || value[0] > 9) {
		return fmt.Errorf("%w: type code %d is outside 1..9", ErrBadMessageType, value[0])
	}

	return nil
}
