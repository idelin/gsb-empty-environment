package netsec

import "reflect"

// Sequence is the set of integer types accepted by sequence-number APIs.
// Internally sequence numbers are stored as uint64.
type Sequence interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

// NormalizeSequence converts a sequence number to the internal uint64
// representation and rejects negatives and non-integer values.
func NormalizeSequence(sequence any) (uint64, error) {
	if sequence == nil {
		return 0, ErrInvalidSequence
	}

	value := reflect.ValueOf(sequence)
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		signed := value.Int()
		if signed < 0 {
			return 0, ErrInvalidSequence
		}
		return uint64(signed), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return value.Uint(), nil
	default:
		return 0, ErrInvalidSequence
	}
}
