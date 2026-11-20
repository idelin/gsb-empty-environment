package clockwire

import (
	"encoding/binary"
	"errors"
)

// Bound is the closed integer interval for offset[Left] - offset[Right].
type Bound struct {
	Left, Right uint16
	Min, Max    int64
}

func DecodeV1(b []byte) (Bound, error) {
	if len(b) != 21 || b[0] != 1 {
		return Bound{}, errors.New("bad clock bound")
	}
	z := Bound{binary.BigEndian.Uint16(b[1:]), binary.BigEndian.Uint16(b[3:]), int64(binary.BigEndian.Uint64(b[5:])), int64(binary.BigEndian.Uint64(b[13:]))}
	if z.Min > z.Max {
		return Bound{}, errors.New("reversed bounds")
	}
	return z, nil
}
func (z Bound) EncodeV1() ([]byte, error) {
	if z.Min > z.Max {
		return nil, errors.New("reversed bounds")
	}
	b := make([]byte, 21)
	b[0] = 1
	binary.BigEndian.PutUint16(b[1:], z.Left)
	binary.BigEndian.PutUint16(b[3:], z.Right)
	binary.BigEndian.PutUint64(b[5:], uint64(z.Min))
	binary.BigEndian.PutUint64(b[13:], uint64(z.Max))
	return b, nil
}
