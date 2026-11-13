package netsec

import (
	"encoding/binary"
	"errors"
	"io"
)

const (
	zip64ExtraID = 0x0001
	uint16max    = 0xffff
	uint32max    = 0xffffffff
)

var errZIP64Field = errors.New("zip: malformed ZIP64 extra field")

type zip64Extra struct {
	raw []byte
}

// findZip64Extra returns the payload of the ZIP64 extended information extra
// field. Multiple ZIP64 blocks are rejected: they are ambiguous and cannot be
// used to make a single, consistent size decision.
func findZip64Extra(extra []byte) (*zip64Extra, error) {
	var found *zip64Extra
	for len(extra) >= 4 {
		tag := binary.LittleEndian.Uint16(extra[0:2])
		size := int(binary.LittleEndian.Uint16(extra[2:4]))
		extra = extra[4:]
		if size > len(extra) {
			return nil, ErrFormat
		}
		payload := extra[:size]
		extra = extra[size:]
		if tag == zip64ExtraID {
			if found != nil {
				return nil, errZIP64Field
			}
			found = &zip64Extra{raw: payload}
		}
	}
	return found, nil
}

func (z *zip64Extra) hasData() bool {
	return z != nil && len(z.raw) > 0
}

// readStrict reads the ZIP64 values that the specification says are present,
// in the canonical order: uncompressed size, compressed size, local header
// offset, and disk number.
func (z *zip64Extra) readStrict(needU, needC, needO, needD bool) (u, c, o uint64, d uint32, hasU, hasC, hasO, hasD bool, err error) {
	if z == nil {
		if needU || needC || needO || needD {
			err = errZIP64Field
		}
		return
	}
	pos := 0
	take64 := func() (uint64, bool) {
		if pos+8 > len(z.raw) {
			return 0, false
		}
		v := binary.LittleEndian.Uint64(z.raw[pos : pos+8])
		pos += 8
		return v, true
	}
	take32 := func() (uint32, bool) {
		if pos+4 > len(z.raw) {
			return 0, false
		}
		v := binary.LittleEndian.Uint32(z.raw[pos : pos+4])
		pos += 4
		return v, true
	}
	if needU {
		if u, hasU = take64(); !hasU {
			err = errZIP64Field
			return
		}
	}
	if needC {
		if c, hasC = take64(); !hasC {
			err = errZIP64Field
			return
		}
	}
	if needO {
		if o, hasO = take64(); !hasO {
			err = errZIP64Field
			return
		}
	}
	if needD {
		if d, hasD = take32(); !hasD {
			err = errZIP64Field
			return
		}
	}
	return
}

// readLocal accepts the canonical local-header layout. Unlike the central
// directory, the local header has no offset or disk fields. Some writers emit
// both sizes even when only one of the 32-bit fields is maxed out, so the
// parser also recognizes that layout after honoring any required fields.
func (z *zip64Extra) readLocal(needU, needC bool) (u, c uint64, hasU, hasC bool, err error) {
	if z == nil {
		if needU || needC {
			err = errZIP64Field
		}
		return
	}
	switch {
	case needU && needC:
		if len(z.raw) < 16 {
			err = errZIP64Field
			return
		}
		u = binary.LittleEndian.Uint64(z.raw[0:8])
		c = binary.LittleEndian.Uint64(z.raw[8:16])
		hasU, hasC = true, true
	case needU:
		if len(z.raw) < 8 {
			err = errZIP64Field
			return
		}
		u = binary.LittleEndian.Uint64(z.raw[0:8])
		hasU = true
		if len(z.raw) >= 16 {
			c = binary.LittleEndian.Uint64(z.raw[8:16])
			hasC = true
		}
	case needC:
		if len(z.raw) < 8 {
			err = errZIP64Field
			return
		}
		c = binary.LittleEndian.Uint64(z.raw[0:8])
		hasC = true
		if len(z.raw) >= 16 {
			u = binary.LittleEndian.Uint64(z.raw[8:16])
			hasU = true
		}
	default:
		// The local 32-bit fields are not sentinels. If a ZIP64 block is
		// nevertheless present, its sizes are authoritative.
		if len(z.raw) >= 8 {
			u = binary.LittleEndian.Uint64(z.raw[0:8])
			hasU = true
		}
		if len(z.raw) >= 16 {
			c = binary.LittleEndian.Uint64(z.raw[8:16])
			hasC = true
		}
	}
	return
}

// readCanonical is used for non-standard but common ZIP64 blocks whose 32-bit
// counterparts are not sentinels. Such a block still takes precedence over
// local-header values.
func (z *zip64Extra) readCanonical() (u, c uint64, o uint64, d uint32, hasU, hasC, hasO, hasD bool) {
	if z == nil {
		return
	}
	if len(z.raw) >= 8 {
		u = binary.LittleEndian.Uint64(z.raw[0:8])
		hasU = true
	}
	if len(z.raw) >= 16 {
		c = binary.LittleEndian.Uint64(z.raw[8:16])
		hasC = true
	}
	if len(z.raw) >= 24 {
		o = binary.LittleEndian.Uint64(z.raw[16:24])
		hasO = true
	}
	if len(z.raw) >= 28 {
		d = binary.LittleEndian.Uint32(z.raw[24:28])
		hasD = true
	}
	return
}

type localHeader struct {
	flags      uint16
	method     uint16
	crc32      uint32
	compressed uint32
	uncomp     uint32
	name       []byte
	extra      []byte
	bodyOffset int64
}

func readLocalHeader(r io.ReaderAt, offset int64) (*localHeader, error) {
	var fixed [30]byte
	if _, err := r.ReadAt(fixed[:], offset); err != nil {
		return nil, err
	}
	if binary.LittleEndian.Uint32(fixed[0:4]) != localHeaderSignature {
		return nil, ErrFormat
	}
	h := &localHeader{
		flags:      binary.LittleEndian.Uint16(fixed[6:8]),
		method:     binary.LittleEndian.Uint16(fixed[8:10]),
		crc32:      binary.LittleEndian.Uint32(fixed[14:18]),
		compressed: binary.LittleEndian.Uint32(fixed[18:22]),
		uncomp:     binary.LittleEndian.Uint32(fixed[22:26]),
	}
	nameLen := int(binary.LittleEndian.Uint16(fixed[26:28]))
	extraLen := int(binary.LittleEndian.Uint16(fixed[28:30]))
	payload := make([]byte, nameLen+extraLen)
	if _, err := r.ReadAt(payload, offset+30); err != nil {
		return nil, err
	}
	h.name = payload[:nameLen]
	h.extra = payload[nameLen:]
	h.bodyOffset = int64(30 + nameLen + extraLen)
	return h, nil
}

// resolveSizes applies the ZIP64 authority rule. A local 32-bit size is a
// hint only when the corresponding ZIP64 extra field is present. The resulting
// uncompressed and compressed sizes must agree with the central directory.
func resolveSizes(f *File) error {
	central := &f.central
	local := f.local

	cExtra, err := findZip64Extra(central.extra)
	if err != nil {
		return err
	}
	needU := central.uncompressed32 == uint32max
	needC := central.compressed32 == uint32max
	needO := central.offset32 == uint32max
	needD := central.disk32 == uint16max
	cu, cc, co, _, hasCU, hasCC, hasCO, _, err := cExtra.readStrict(needU, needC, needO, needD)
	if err != nil {
		return err
	}
	if needU {
		central.uncompressed64 = cu
	}
	if needC {
		central.compressed64 = cc
	}
	if needO {
		central.offset64 = int64(co)
	}
	centralUExtra := needU
	centralCExtra := needC
	if !needU && !needC && !needO && !needD && cExtra.hasData() {
		cu, cc, co, _, hasCU, hasCC, hasCO, _ = cExtra.readCanonical()
		if hasCU {
			central.uncompressed64 = cu
			centralUExtra = true
		}
		if hasCC {
			central.compressed64 = cc
			centralCExtra = true
		}
		if hasCO {
			central.offset64 = int64(co)
		}
	}

	localExtra, err := findZip64Extra(local.extra)
	if err != nil {
		return err
	}
	lu, lc, hasLU, hasLC, err := localExtra.readLocal(local.uncomp == uint32max, local.compressed == uint32max)
	if err != nil {
		return err
	}

	usesZIP64 := needU || needC || needO || cExtra.hasData() ||
		local.uncomp == uint32max || local.compressed == uint32max || localExtra.hasData()

	lu64 := uint64(local.uncomp)
	lc64 := uint64(local.compressed)
	if localExtra != nil {
		if hasLU {
			lu64 = lu
		}
		if hasLC {
			lc64 = lc
		}
	}

	if needU && !hasCU || needC && !hasCC || needO && !hasCO {
		return ErrFormat
	}
	if usesZIP64 {
		if hasLU || !centralUExtra {
			if central.uncompressed64 != lu64 {
				return ErrFormat
			}
		}
		if hasLC || !centralCExtra {
			if central.compressed64 != lc64 {
				return ErrFormat
			}
		}
	} else if local.flags&0x8 == 0 {
		if uint64(central.uncompressed32) != lu64 || uint64(central.compressed32) != lc64 {
			return ErrFormat
		}
	}
	if local.flags&0x8 == 0 {
		if local.crc32 != 0 && central.crc32 != 0 && local.crc32 != central.crc32 {
			return ErrFormat
		}
	}
	return nil
}
