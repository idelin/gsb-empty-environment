package netsec

import (
	"fmt"
	"sort"
)

const (
	ClassIN uint16 = 1

	TypeA     uint16 = 1
	TypeNS    uint16 = 2
	TypeCNAME uint16 = 5
	TypeSOA   uint16 = 6
	TypeAAAA  uint16 = 28
	TypeDS    uint16 = 43
	TypeRRSIG uint16 = 46
	TypeNSEC  uint16 = 47
	TypeNSEC3 uint16 = 50
	TypeOPT   uint16 = 41
	TypeTSIG  uint16 = 250
	TypeIXFR  uint16 = 251
	TypeAXFR  uint16 = 252
	TypeANY   uint16 = 255
)

// TypeBitmap is a canonical, sorted set of RR type numbers. It is used both
// for NSEC/NSEC3 type bitmaps and for internal absence tests.
type TypeBitmap struct {
	Types []uint16
}

// NewTypeBitmap canonicalizes the supplied type numbers. Type zero is
// rejected; duplicates are removed.
func NewTypeBitmap(types ...uint16) (TypeBitmap, error) {
	seen := make(map[uint16]struct{}, len(types))
	out := make([]uint16, 0, len(types))
	for _, t := range types {
		if t == 0 {
			return TypeBitmap{}, fmt.Errorf("%w: type zero in bitmap", ErrMalformed)
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return TypeBitmap{Types: out}, nil
}

// MustTypeBitmap panics on an invalid type list; intended for literals.
func MustTypeBitmap(types ...uint16) TypeBitmap {
	b, err := NewTypeBitmap(types...)
	if err != nil {
		panic(err)
	}
	return b
}

// Has reports whether t is present.
func (b TypeBitmap) Has(t uint16) bool {
	for _, v := range b.Types {
		if v == t {
			return true
		}
	}
	return false
}

// Empty reports whether no types are present.
func (b TypeBitmap) Empty() bool { return len(b.Types) == 0 }

// Canonical returns a sorted copy with duplicates removed.
func (b TypeBitmap) Canonical() TypeBitmap {
	if len(b.Types) == 0 {
		return TypeBitmap{}
	}
	out := append([]uint16(nil), b.Types...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	uniq := out[:0]
	for _, t := range out {
		if t == 0 {
			continue
		}
		if len(uniq) == 0 || uniq[len(uniq)-1] != t {
			uniq = append(uniq, t)
		}
	}
	return TypeBitmap{Types: uniq}
}

// Wire serializes the bitmap in RFC 4034 section 4.1.2 window-block form.
func (b TypeBitmap) Wire() []byte {
	canon := b.Canonical()
	var out []byte
	for start := 0; start < len(canon.Types); {
		window := canon.Types[start] >> 8
		end := start
		maxOctet := 0
		for end < len(canon.Types) && canon.Types[end]>>8 == window {
			octet := int((canon.Types[end] & 0xff) / 8)
			if octet > maxOctet {
				maxOctet = octet
			}
			end++
		}
		blockLen := maxOctet + 1
		block := make([]byte, blockLen)
		for _, t := range canon.Types[start:end] {
			bit := int(t & 0xff)
			block[bit/8] |= 0x80 >> uint(bit%8)
		}
		out = append(out, byte(window), byte(blockLen))
		out = append(out, block...)
		start = end
	}
	return out
}

// ParseTypeBitmap parses a wire-format type bitmap. It rejects non-canonical
// window ordering, zero bit 0, trailing zero octets, and duplicate windows.
func ParseTypeBitmap(w []byte) (TypeBitmap, error) {
	var types []uint16
	lastWindow := -1
	for pos := 0; pos < len(w); {
		if pos+2 > len(w) {
			return TypeBitmap{}, fmt.Errorf("%w: truncated bitmap window", ErrMalformed)
		}
		window := int(w[pos])
		length := int(w[pos+1])
		pos += 2
		if length < 1 || length > 32 {
			return TypeBitmap{}, fmt.Errorf("%w: invalid bitmap window length", ErrMalformed)
		}
		if pos+length > len(w) {
			return TypeBitmap{}, fmt.Errorf("%w: truncated bitmap block", ErrMalformed)
		}
		if window <= lastWindow {
			return TypeBitmap{}, fmt.Errorf("%w: non-canonical bitmap window", ErrMalformed)
		}
		lastWindow = window
		block := w[pos : pos+length]
		if block[length-1] == 0 {
			return TypeBitmap{}, fmt.Errorf("%w: trailing zero bitmap octet", ErrMalformed)
		}
		for octet, bits := range block {
			for bit := 0; bit < 8; bit++ {
				if bits&(0x80>>uint(bit)) != 0 {
					t := uint16(window<<8 | octet<<3 | bit)
					if t == 0 {
						return TypeBitmap{}, fmt.Errorf("%w: type zero in bitmap", ErrMalformed)
					}
					types = append(types, t)
				}
			}
		}
		pos += length
	}
	return TypeBitmap{Types: types}, nil
}

func validQuestionType(t uint16) bool {
	switch t {
	case 0, TypeOPT, TypeTSIG, TypeIXFR, TypeAXFR, TypeANY:
		return false
	default:
		return true
	}
}
