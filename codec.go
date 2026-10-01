package chronos

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Binary serialization. Self-defined supported range:
//   - instants: years 0001..9999 (MinInstantSec..MaxInstantSec)
//   - durations: +/-MaxDurationNanos (~146 years)
//   - offsets: +/-18h
//   - zone labels: 1..64 chars, versions: 1..32 chars, [A-Za-z0-9_\-/+.]
//
// Decoding never executes host code: it only parses bytes into plain data.
// Every length field is validated against the remaining buffer BEFORE any
// allocation or slice is taken, and every value is validated by its
// constructor before the object is published to the caller.

const (
	formatMagic0 = 'C'
	formatMagic1 = 'H'
	formatMagic2 = 'R'
	formatMagic3 = 'O'
	formatVer    = 1

	tagInstant  = 1
	tagWall     = 2
	tagDuration = 3
	tagOffset   = 4

	headerLen = 8 // magic(4) + ver(1) + type(1) + prec(1) + tzLen(1)
)

var (
	ErrTruncated  = errors.New("chronos: truncated or corrupt buffer")
	ErrBadMagic   = errors.New("chronos: bad magic or format version")
	ErrBadTypeTag = errors.New("chronos: illegal type tag")
)

func u32be(b []byte) uint32 { return binary.BigEndian.Uint32(b) }
func u64be(b []byte) uint64 { return binary.BigEndian.Uint64(b) }

func putU32(b []byte, v uint32) { binary.BigEndian.PutUint32(b, v) }
func putU64(b []byte, v uint64) { binary.BigEndian.PutUint64(b, v) }

// Encode serializes a supported value (Instant, WallClock, Duration,
// OffsetChoice) into the binary envelope.
func Encode(v interface{}) ([]byte, error) {
	switch t := v.(type) {
	case Instant:
		out := make([]byte, headerLen+12)
		writeHeader(out, tagInstant, t.prec, "")
		putU64(out[headerLen:], uint64(t.sec))
		putU32(out[headerLen+8:], t.nanos)
		return out, nil
	case Duration:
		out := make([]byte, headerLen+8)
		writeHeader(out, tagDuration, t.prec, "")
		putU64(out[headerLen:], uint64(t.nanos))
		return out, nil
	case OffsetChoice:
		if _, err := NewOffsetChoice(t.OffsetSec, t.Fold); err != nil {
			return nil, err
		}
		out := make([]byte, headerLen+5)
		writeHeader(out, tagOffset, 0, "")
		putU32(out[headerLen:], uint32(t.OffsetSec))
		out[headerLen+4] = t.Fold
		return out, nil
	case WallClock:
		if !validLabel(t.zone, MaxZoneLabelLen) {
			return nil, fmt.Errorf("chronos: illegal zone label %q", t.zone)
		}
		if !validLabel(t.tzVersion, MaxVersionLabelLen) {
			return nil, fmt.Errorf("chronos: illegal tz version label %q", t.tzVersion)
		}
		out := make([]byte, headerLen+len(t.tzVersion)+19+len(t.zone))
		writeHeader(out, tagWall, t.prec, t.tzVersion)
		p := headerLen + len(t.tzVersion)
		putU32(out[p:], uint32(t.year))
		out[p+4] = t.month
		out[p+5] = t.day
		out[p+6] = t.hour
		out[p+7] = t.min
		out[p+8] = t.sec
		putU32(out[p+9:], t.nanos)
		putU32(out[p+13:], uint32(t.choice.OffsetSec))
		out[p+17] = t.choice.Fold
		out[p+18] = uint8(len(t.zone))
		copy(out[p+19:], t.zone)
		return out, nil
	}
	return nil, fmt.Errorf("chronos: unsupported type %T", v)
}

func writeHeader(out []byte, typ uint8, prec Precision, tzVersion string) {
	out[0] = formatMagic0
	out[1] = formatMagic1
	out[2] = formatMagic2
	out[3] = formatMagic3
	out[4] = formatVer
	out[5] = typ
	out[6] = uint8(prec)
	out[7] = uint8(len(tzVersion))
	copy(out[headerLen:], tzVersion)
}

// Decode parses one envelope. It validates the header, then every length
// field against the remaining bytes, and only then allocates and builds the
// value through its validating constructor.
func Decode(b []byte) (interface{}, error) {
	if len(b) < headerLen {
		return nil, ErrTruncated
	}
	if b[0] != formatMagic0 || b[1] != formatMagic1 || b[2] != formatMagic2 || b[3] != formatMagic3 || b[4] != formatVer {
		return nil, ErrBadMagic
	}
	typ := b[5]
	if typ < tagInstant || typ > tagOffset {
		return nil, fmt.Errorf("%w: %d", ErrBadTypeTag, typ)
	}
	tzLen := int(b[7])
	if typ == tagWall {
		if tzLen < 1 || tzLen > MaxVersionLabelLen {
			return nil, fmt.Errorf("chronos: illegal tz version length %d", tzLen)
		}
	} else if tzLen != 0 {
		return nil, fmt.Errorf("chronos: unexpected tz version length %d for type %d", tzLen, typ)
	}
	// Length gate: the full fixed part must be present before we touch it.
	if len(b) < headerLen+tzLen {
		return nil, ErrTruncated
	}
	tzVersion := string(b[headerLen : headerLen+tzLen])
	if tzLen > 0 && !validLabel(tzVersion, MaxVersionLabelLen) {
		return nil, fmt.Errorf("chronos: illegal tz version label %q", tzVersion)
	}
	body := b[headerLen+tzLen:]

	switch typ {
	case tagInstant:
		prec, err := parsePrecision(b[6])
		if err != nil {
			return nil, err
		}
		if len(body) != 12 {
			return nil, ErrTruncated
		}
		return NewInstant(int64(u64be(body)), u32be(body[8:]), prec)
	case tagDuration:
		prec, err := parsePrecision(b[6])
		if err != nil {
			return nil, err
		}
		if len(body) != 8 {
			return nil, ErrTruncated
		}
		return NewDuration(int64(u64be(body)), prec)
	case tagOffset:
		if b[6] != 0 {
			return nil, fmt.Errorf("chronos: offset choice must not carry a precision label")
		}
		if len(body) != 5 {
			return nil, ErrTruncated
		}
		return NewOffsetChoice(int32(u32be(body)), body[4])
	case tagWall:
		prec, err := parsePrecision(b[6])
		if err != nil {
			return nil, err
		}
		if len(body) < 19 {
			return nil, ErrTruncated
		}
		zoneLen := int(body[18])
		// Length gate for the variable part, checked before allocation.
		if zoneLen < 1 || zoneLen > MaxZoneLabelLen {
			return nil, fmt.Errorf("chronos: illegal zone label length %d", zoneLen)
		}
		if len(body) != 19+zoneLen {
			return nil, ErrTruncated
		}
		zone := string(body[19:])
		if !validLabel(zone, MaxZoneLabelLen) {
			return nil, fmt.Errorf("chronos: illegal zone label %q", zone)
		}
		choice, err := NewOffsetChoice(int32(u32be(body[13:])), body[17])
		if err != nil {
			return nil, err
		}
		year := int32(u32be(body))
		month, day := body[4], body[5]
		hour, min, sec := body[6], body[7], body[8]
		nanos := u32be(body[9:])
		if err := validateCivil(year, month, day, hour, min, sec, nanos, prec); err != nil {
			return nil, err
		}
		return WallClock{
			year: year, month: month, day: day,
			hour: hour, min: min, sec: sec, nanos: nanos,
			prec: prec, zone: zone, tzVersion: tzVersion, choice: choice,
		}, nil
	}
	return nil, ErrBadTypeTag
}

func validateCivil(year int32, month, day, hour, min, sec uint8, nanos uint32, prec Precision) error {
	if year < 1 || year > 9999 {
		return fmt.Errorf("chronos: year %d outside supported range [1, 9999]", year)
	}
	if month < 1 || month > 12 {
		return fmt.Errorf("chronos: illegal month %d", month)
	}
	if day < 1 || day > daysInMonth(year, month) {
		return fmt.Errorf("chronos: illegal day %d for %04d-%02d", day, year, month)
	}
	if hour > 23 || min > 59 || sec > 59 {
		return fmt.Errorf("chronos: illegal time %02d:%02d:%02d", hour, min, sec)
	}
	if nanos >= uint32(nanosPerSec) {
		return fmt.Errorf("chronos: nanoseconds %d out of range", nanos)
	}
	if int64(nanos)%prec.NanosPerUnit() != 0 {
		return fmt.Errorf("chronos: %d ns not aligned to precision %s", nanos, prec)
	}
	return nil
}
