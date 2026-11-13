package netsec

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
)

const (
	// UnixExtraID is the PKWARE/Info-ZIP UNIX extra field that can carry the
	// original linked-to name after its 12 bytes of metadata.
	UnixExtraID uint16 = 0x000d

	// InfoZipUnixExtraID is the older Info-ZIP Unix extra field. Some writers
	// append the link target after its 12 bytes of metadata as well.
	InfoZipUnixExtraID uint16 = 0x5855

	// AsiUnixExtraID is the ASi Unix extra field, whose variable tail is a
	// symbolic-link target.
	AsiUnixExtraID uint16 = 0x756e

	// UnicodePathExtraID is Info-ZIP's alternate UTF-8 entry path.
	UnicodePathExtraID uint16 = 0x7075
)

const (
	unixExtraFixed    = 12
	asiUnixExtraFixed = 14
	unicodePathHeader = 5
)

// ExtraField is one sub-field in a ZIP extra block.
type ExtraField struct {
	ID   uint16
	Data []byte
}

// ParseExtra splits and validates a ZIP extra block.
func ParseExtra(extra []byte) ([]ExtraField, error) {
	fields := make([]ExtraField, 0, 4)
	for len(extra) > 0 {
		if len(extra) < 4 {
			if allZero(extra) {
				break
			}
			return nil, ErrMalformedExtra
		}
		id := binary.LittleEndian.Uint16(extra[:2])
		size := int(binary.LittleEndian.Uint16(extra[2:4]))
		if size > len(extra)-4 {
			return nil, ErrMalformedExtra
		}
		fields = append(fields, ExtraField{
			ID:   id,
			Data: append([]byte(nil), extra[4:4+size]...),
		})
		extra = extra[4+size:]
	}
	return fields, nil
}

// UnixLinkTarget returns the first non-empty link target in the Unix-style
// extra fields.
func UnixLinkTarget(extra []byte) (string, bool, error) {
	targets, err := UnixLinkTargets(extra)
	if err != nil {
		return "", false, err
	}
	if len(targets) == 0 {
		return "", false, nil
	}
	return targets[0], true, nil
}

// UnixLinkTargets returns every non-empty link target in the Unix-style extra
// fields. Checking all of them prevents one sub-field from hiding another.
func UnixLinkTargets(extra []byte) ([]string, error) {
	fields, err := ParseExtra(extra)
	if err != nil {
		return nil, err
	}
	targets := make([]string, 0, 1)
	for _, field := range fields {
		fixed, ok := unixFixedLen(field.ID)
		if !ok || len(field.Data) <= fixed {
			continue
		}
		tail := field.Data[fixed:]
		for len(tail) > 0 {
			segment := tail
			if i := bytes.IndexByte(tail, 0); i >= 0 {
				segment = tail[:i]
				tail = tail[i+1:]
			} else {
				tail = nil
			}
			if len(segment) > 0 {
				targets = append(targets, string(segment))
			}
		}
	}
	return targets, nil
}

// UnicodePath returns the alternate path from a 0x7075 extra field. The CRC is
// not used as a reason to skip validation: a path that a permissive reader
// would accept must still be adjudicated here.
func UnicodePath(extra []byte) (string, bool, error) {
	paths, err := UnicodePaths(extra)
	if err != nil {
		return "", false, err
	}
	if len(paths) == 0 {
		return "", false, nil
	}
	return paths[0], true, nil
}

// UnicodePaths returns every non-empty alternate path in 0x7075 fields.
func UnicodePaths(extra []byte) ([]string, error) {
	fields, err := ParseExtra(extra)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, 1)
	for _, field := range fields {
		if field.ID != UnicodePathExtraID || len(field.Data) < unicodePathHeader {
			continue
		}
		name := field.Data[unicodePathHeader:]
		if len(name) == 0 {
			continue
		}
		paths = append(paths, string(name))
	}
	return paths, nil
}

// UnicodePathExtra builds an Info-ZIP Unicode Path extra field for original.
func UnicodePathExtra(original []byte, unicodeName string) []byte {
	data := make([]byte, unicodePathHeader+len(unicodeName))
	data[0] = 1
	binary.LittleEndian.PutUint32(data[1:5], crc32.ChecksumIEEE(original))
	copy(data[5:], unicodeName)

	out := make([]byte, 4+len(data))
	binary.LittleEndian.PutUint16(out[:2], UnicodePathExtraID)
	binary.LittleEndian.PutUint16(out[2:4], uint16(len(data)))
	copy(out[4:], data)
	return out
}

// InfoZipUnixExtra builds a 0x000d UNIX extra field with target in its
// variable-length tail. The target is stored verbatim.
func InfoZipUnixExtra(target string) []byte {
	return UnixExtraWithID(UnixExtraID, target)
}

// UnixExtraWithID builds a Unix extra field using id and stores target after
// the fixed metadata for that id.
func UnixExtraWithID(id uint16, target string) []byte {
	fixed, ok := unixFixedLen(id)
	if !ok {
		fixed = unixExtraFixed
	}
	payload := make([]byte, fixed+len(target))
	copy(payload[fixed:], target)

	out := make([]byte, 4+len(payload))
	binary.LittleEndian.PutUint16(out[:2], id)
	binary.LittleEndian.PutUint16(out[2:4], uint16(len(payload)))
	copy(out[4:], payload)
	return out
}

func unixFixedLen(id uint16) (int, bool) {
	switch id {
	case UnixExtraID, InfoZipUnixExtraID:
		return unixExtraFixed, true
	case AsiUnixExtraID:
		return asiUnixExtraFixed, true
	default:
		return 0, false
	}
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}
