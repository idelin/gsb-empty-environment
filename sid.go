package netsec

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// SID is a Windows security identifier.
type SID struct {
	Revision       byte
	Authority      [6]byte
	SubAuthorities []uint32
}

// Valid reports whether the SID has a non-zero revision.
func (s SID) Valid() bool {
	return s.Revision != 0
}

// IsZero reports whether s is the zero value.
func (s SID) IsZero() bool {
	if s.Revision != 0 || s.SubAuthorities != nil {
		return false
	}
	for _, b := range s.Authority {
		if b != 0 {
			return false
		}
	}
	return true
}

// Equal reports whether s and other identify the same SID.
func (s SID) Equal(other SID) bool {
	if s.Revision != other.Revision || s.Authority != other.Authority {
		return false
	}
	if len(s.SubAuthorities) != len(other.SubAuthorities) {
		return false
	}
	for i := range s.SubAuthorities {
		if s.SubAuthorities[i] != other.SubAuthorities[i] {
			return false
		}
	}
	return true
}

// String returns the SDDL form, for example S-1-5-21-1-2-3-513.
func (s SID) String() string {
	if !s.Valid() {
		return ""
	}
	var b strings.Builder
	b.WriteString("S-")
	b.WriteString(fmt.Sprint(s.Revision))
	b.WriteByte('-')
	b.WriteString(fmt.Sprint(sidAuthority(s.Authority)))
	for _, sub := range s.SubAuthorities {
		b.WriteByte('-')
		b.WriteString(fmt.Sprint(sub))
	}
	return b.String()
}

// Bytes returns the RPC_SID wire form.
func (s SID) Bytes() []byte {
	out := make([]byte, 8+4*len(s.SubAuthorities))
	out[0] = s.Revision
	out[1] = byte(len(s.SubAuthorities))
	copy(out[2:8], s.Authority[:])
	for i, sub := range s.SubAuthorities {
		binary.LittleEndian.PutUint32(out[8+4*i:], sub)
	}
	return out
}

// parseSID parses an RPC_SID and returns the SID and its encoded length.
func parseSID(data []byte) (SID, int, error) {
	if len(data) < 8 {
		return SID{}, 0, fmt.Errorf("%w: short SID", ErrMalformedLogonInfo)
	}
	revision := data[0]
	count := int(data[1])
	if count > 15 {
		return SID{}, 0, fmt.Errorf("%w: SID has %d sub-authorities", ErrMalformedLogonInfo, count)
	}
	size := 8 + 4*count
	if size > len(data) {
		return SID{}, 0, fmt.Errorf("%w: truncated SID", ErrMalformedLogonInfo)
	}
	s := SID{
		Revision:       revision,
		SubAuthorities: make([]uint32, count),
	}
	copy(s.Authority[:], data[2:8])
	for i := 0; i < count; i++ {
		s.SubAuthorities[i] = binary.LittleEndian.Uint32(data[8+4*i:])
	}
	return s, size, nil
}

// parseSIDAt parses an RPC_SID at a 32-bit offset relative to data's start.
func parseSIDAt(data []byte, offset uint32) (SID, error) {
	if uint64(offset) > uint64(len(data)) {
		return SID{}, fmt.Errorf("%w: SID offset %d is out of bounds", ErrMalformedLogonInfo, offset)
	}
	s, _, err := parseSID(data[offset:])
	if err != nil {
		return SID{}, err
	}
	return s, nil
}

func sidAuthority(a [6]byte) uint64 {
	var v uint64
	for _, b := range a {
		v = v<<8 | uint64(b)
	}
	return v
}
