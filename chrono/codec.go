package chrono

import (
	"encoding/binary"
	"fmt"
)

// Binary serialization format (big-endian):
//
//	Instant:   tag=1 | prec u8 | nanos i64
//	Duration:  tag=2 | nanos i64
//	WallClock: tag=3 | prec u8 | year i32 | month u8 | day u8 | hour u8 |
//	           minute u8 | second u8 | nanos u32 | offsetSec i32 |
//	           choice u8 | zoneLen u16 zone | versionLen u16 version
//
// Decoding is pure data validation: it never executes host code, never
// reflects, and validates every length against the remaining input before
// allocating. Decoded fields are passed through the validating constructors,
// so illegal tags, overflows and invalid fields are rejected before any
// object is published.
const (
	tagInstant   byte = 1
	tagDuration  byte = 2
	tagWallClock byte = 3
)

// maxStringLen bounds length-prefixed strings; the length is validated
// against both this bound and the remaining input before any allocation.
const maxStringLen = 256

// Value is the sum type of everything the codec can carry.
type Value interface{ value() }

func (Instant) value()   {}
func (Duration) value()  {}
func (WallClock) value() {}

// Encode serializes a Value. The value is already valid by construction.
func Encode(v Value) ([]byte, error) {
	switch t := v.(type) {
	case Instant:
		b := make([]byte, 0, 10)
		b = append(b, tagInstant, byte(t.prec))
		return binary.BigEndian.AppendUint64(b, uint64(t.nanos)), nil
	case Duration:
		b := make([]byte, 0, 9)
		b = append(b, tagDuration)
		return binary.BigEndian.AppendUint64(b, uint64(t.nanos)), nil
	case WallClock:
		if len(t.zone) > maxStringLen || len(t.tzVersion) > maxStringLen {
			return nil, ErrStringTooLong
		}
		b := make([]byte, 0, 24+len(t.zone)+len(t.tzVersion))
		b = append(b, tagWallClock, byte(t.prec))
		b = binary.BigEndian.AppendUint32(b, uint32(int32(t.year)))
		b = append(b, byte(t.month), byte(t.day), byte(t.hour), byte(t.minute), byte(t.second))
		b = binary.BigEndian.AppendUint32(b, uint32(t.nanos))
		b = binary.BigEndian.AppendUint32(b, uint32(t.offsetSec))
		b = append(b, byte(t.choice))
		b = binary.BigEndian.AppendUint16(b, uint16(len(t.zone)))
		b = append(b, t.zone...)
		b = binary.BigEndian.AppendUint16(b, uint16(len(t.tzVersion)))
		b = append(b, t.tzVersion...)
		return b, nil
	}
	return nil, fmt.Errorf("%w: %T", ErrUnknownTag, v)
}

// Decode parses one value and rejects trailing bytes.
func Decode(b []byte) (Value, error) {
	d := &decoder{b: b}
	tag, err := d.u8()
	if err != nil {
		return nil, err
	}
	var v Value
	switch tag {
	case tagInstant:
		prec, err := d.u8()
		if err != nil {
			return nil, err
		}
		nanos, err := d.i64()
		if err != nil {
			return nil, err
		}
		v, err = NewInstant(nanos, Precision(prec))
		if err != nil {
			return nil, err
		}
	case tagDuration:
		nanos, err := d.i64()
		if err != nil {
			return nil, err
		}
		v, err = NewDuration(nanos)
		if err != nil {
			return nil, err
		}
	case tagWallClock:
		v, err = d.wallClock()
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("%w: 0x%02x", ErrUnknownTag, tag)
	}
	if d.pos != len(d.b) {
		return nil, ErrTrailing
	}
	return v, nil
}

func (d *decoder) wallClock() (WallClock, error) {
	prec, err := d.u8()
	if err != nil {
		return WallClock{}, err
	}
	year, err := d.i32()
	if err != nil {
		return WallClock{}, err
	}
	month, err := d.u8()
	if err != nil {
		return WallClock{}, err
	}
	day, err := d.u8()
	if err != nil {
		return WallClock{}, err
	}
	hour, err := d.u8()
	if err != nil {
		return WallClock{}, err
	}
	minute, err := d.u8()
	if err != nil {
		return WallClock{}, err
	}
	second, err := d.u8()
	if err != nil {
		return WallClock{}, err
	}
	nanos, err := d.u32()
	if err != nil {
		return WallClock{}, err
	}
	offset, err := d.i32()
	if err != nil {
		return WallClock{}, err
	}
	choice, err := d.u8()
	if err != nil {
		return WallClock{}, err
	}
	zone, err := d.str()
	if err != nil {
		return WallClock{}, err
	}
	version, err := d.str()
	if err != nil {
		return WallClock{}, err
	}
	// All fields are local until this constructor validates them; only a
	// fully valid WallClock is published to the caller.
	return NewWallClock(int(year), int(month), int(day), int(hour), int(minute), int(second),
		int64(nanos), offset, OffsetChoice(choice), Precision(prec), zone, version)
}

type decoder struct {
	b   []byte
	pos int
}

// take validates n against the remaining input before slicing; no
// allocation happens unless the bytes are actually present.
func (d *decoder) take(n int) ([]byte, error) {
	if n < 0 || len(d.b)-d.pos < n {
		return nil, ErrTruncated
	}
	s := d.b[d.pos : d.pos+n]
	d.pos += n
	return s, nil
}

func (d *decoder) u8() (byte, error) {
	s, err := d.take(1)
	if err != nil {
		return 0, err
	}
	return s[0], nil
}

func (d *decoder) u16() (int, error) {
	s, err := d.take(2)
	if err != nil {
		return 0, err
	}
	return int(binary.BigEndian.Uint16(s)), nil
}

func (d *decoder) u32() (uint32, error) {
	s, err := d.take(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(s), nil
}

func (d *decoder) i32() (int32, error) {
	u, err := d.u32()
	return int32(u), err
}

func (d *decoder) i64() (int64, error) {
	s, err := d.take(8)
	if err != nil {
		return 0, err
	}
	return int64(binary.BigEndian.Uint64(s)), nil
}

func (d *decoder) str() (string, error) {
	n, err := d.u16()
	if err != nil {
		return "", err
	}
	if n > maxStringLen {
		return "", ErrStringTooLong
	}
	s, err := d.take(n) // length validated before the string allocation
	if err != nil {
		return "", err
	}
	return string(s), nil
}
