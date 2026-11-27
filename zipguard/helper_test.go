package zipguard

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"hash/crc32"
	"io/fs"
)

// unixAttrs packs a Unix mode into the high 16 bits of external attributes.
func unixAttrs(mode fs.FileMode) uint32 {
	var m uint16 = uint16(mode.Perm())
	switch {
	case mode&fs.ModeSymlink != 0:
		m |= 0xA000
	case mode&fs.ModeDir != 0:
		m |= 0x4000
	case mode&fs.ModeNamedPipe != 0:
		m |= 0x1000
	case mode&fs.ModeSocket != 0:
		m |= 0xC000
	case mode&fs.ModeDevice != 0:
		if mode&fs.ModeCharDevice != 0 {
			m |= 0x2000
		} else {
			m |= 0x6000
		}
	default:
		m |= 0x8000
	}
	if mode&fs.ModeSetuid != 0 {
		m |= 0o4000
	}
	if mode&fs.ModeSetgid != 0 {
		m |= 0o2000
	}
	return uint32(m) << 16
}

// specEntry is a hand-crafted central-directory entry used by buildArchive.
type specEntry struct {
	name           string
	content        []byte
	method         uint16
	externalAttrs  uint32
	hostSystem     uint8
	flags          uint16
	dataDescriptor bool

	// Optional raw overrides. Non-zero fields replace the generated values in
	// the CDH and, where applicable, the LFH.
	overrideCRC         *uint32
	overrideCompSize    *uint32
	overrideUncompSize  *uint32
	overrideMethod      *uint16
	overrideFlags       *uint16
	overrideName        string
	overrideLocalOffset *uint32

	// lfhOnly overrides change only the local header, creating LFH/CDH
	// divergence.
	lfhOnlyCRC        *uint32
	lfhOnlyCompSize   *uint32
	lfhOnlyUncompSize *uint32
	lfhOnlyMethod     *uint16
	lfhOnlyFlags      *uint16
	lfhOnlyName       string

	// rawPayload replaces the computed payload verbatim.
	rawPayload []byte
}

func deflateBytes(data []byte) []byte {
	var buf bytes.Buffer
	w, _ := flate.NewWriter(&buf, flate.BestCompression)
	w.Write(data)
	w.Close()
	return buf.Bytes()
}

func put16(b []byte, v uint16) { binary.LittleEndian.PutUint16(b, v) }
func put32(b []byte, v uint32) { binary.LittleEndian.PutUint32(b, v) }

type builtEntry struct {
	spec      *specEntry
	crc       uint32
	comp      []byte
	localOff  uint32
	regionEnd int64
}

// buildArchive serializes entries without using archive/zip.
func buildArchive(specs []specEntry, eocdComment []byte) []byte {
	var localArea bytes.Buffer
	var cdArea bytes.Buffer
	built := make([]builtEntry, 0, len(specs))

	for i := range specs {
		s := &specs[i]
		method := s.method
		if method == 0 {
			method = methodStored
		}
		var payload []byte
		switch method {
		case methodStored:
			payload = append([]byte(nil), s.content...)
		case methodDeflate:
			payload = deflateBytes(s.content)
		default:
			payload = append([]byte(nil), s.content...)
		}
		if s.rawPayload != nil {
			payload = append([]byte(nil), s.rawPayload...)
		}
		crc := crc32.ChecksumIEEE(s.content)

		flags := s.flags
		if s.dataDescriptor {
			flags |= flagDataDescriptor
		}
		name := s.name
		if s.overrideName != "" {
			name = s.overrideName
		}

		localOff := uint32(localArea.Len())

		// LFH
		lfhName := name
		if s.lfhOnlyName != "" {
			lfhName = s.lfhOnlyName
		}
		lfh := make([]byte, lfhFixedSize)
		put32(lfh[0:], sigLFH)
		put16(lfh[4:], 20)
		lfhFlags := flags
		lfhCRC := crc
		lfhComp := uint32(len(payload))
		lfhUncomp := uint32(len(s.content))
		if s.lfhOnlyFlags != nil {
			lfhFlags = *s.lfhOnlyFlags
		}
		if s.lfhOnlyCRC != nil {
			lfhCRC = *s.lfhOnlyCRC
		}
		if s.lfhOnlyCompSize != nil {
			lfhComp = *s.lfhOnlyCompSize
		}
		if s.lfhOnlyUncompSize != nil {
			lfhUncomp = *s.lfhOnlyUncompSize
		}
		lfhMethod := method
		if s.lfhOnlyMethod != nil {
			lfhMethod = *s.lfhOnlyMethod
		}
		// With a data descriptor the LFH carries zeroed crc/size fields.
		if s.dataDescriptor {
			lfhCRC, lfhComp, lfhUncomp = 0, 0, 0
		}
		put16(lfh[6:], lfhFlags)
		put16(lfh[8:], lfhMethod)
		put16(lfh[10:], 0)    // mod time
		put16(lfh[12:], 0x21) // mod date
		put32(lfh[14:], lfhCRC)
		put32(lfh[18:], lfhComp)
		put32(lfh[22:], lfhUncomp)
		put16(lfh[26:], uint16(len(lfhName)))
		put16(lfh[28:], 0)
		localArea.Write(lfh)
		localArea.WriteString(lfhName)
		dataOff := int64(localArea.Len())
		localArea.Write(payload)
		if s.dataDescriptor {
			dd := make([]byte, ddFixedSize+4)
			put32(dd[0:], sigDD)
			put32(dd[4:], crc)
			put32(dd[8:], uint32(len(payload)))
			put32(dd[12:], uint32(len(s.content)))
			localArea.Write(dd)
		}

		// CDH
		cdhName := name
		cdh := make([]byte, cdhFixedSize)
		put32(cdh[0:], sigCDH)
		put16(cdh[4:], uint16(s.hostSystem)<<8|20)
		put16(cdh[6:], 20)
		cdhFlags := flags
		cdhMethod := method
		cdhCRC := crc
		cdhComp := uint32(len(payload))
		cdhUncomp := uint32(len(s.content))
		cdhOff := localOff
		if s.overrideFlags != nil {
			cdhFlags = *s.overrideFlags
		}
		if s.overrideMethod != nil {
			cdhMethod = *s.overrideMethod
		}
		if s.overrideCRC != nil {
			cdhCRC = *s.overrideCRC
		}
		if s.overrideCompSize != nil {
			cdhComp = *s.overrideCompSize
		}
		if s.overrideUncompSize != nil {
			cdhUncomp = *s.overrideUncompSize
		}
		if s.overrideLocalOffset != nil {
			cdhOff = *s.overrideLocalOffset
		}
		put16(cdh[8:], cdhFlags)
		put16(cdh[10:], cdhMethod)
		put32(cdh[16:], cdhCRC)
		put32(cdh[20:], cdhComp)
		put32(cdh[24:], cdhUncomp)
		put16(cdh[28:], uint16(len(cdhName)))
		put32(cdh[38:], s.externalAttrs)
		put32(cdh[42:], cdhOff)
		cdArea.Write(cdh)
		cdArea.WriteString(cdhName)

		built = append(built, builtEntry{
			spec:      s,
			crc:       crc,
			comp:      payload,
			localOff:  localOff,
			regionEnd: int64(localArea.Len()),
		})
		_ = dataOff
	}

	cdOffset := uint32(localArea.Len())
	var out bytes.Buffer
	out.Write(localArea.Bytes())
	out.Write(cdArea.Bytes())

	eocd := make([]byte, eocdFixedSize)
	put32(eocd[0:], sigEOCD)
	put16(eocd[8:], uint16(len(specs)))
	put16(eocd[10:], uint16(len(specs)))
	put32(eocd[12:], uint32(cdArea.Len()))
	put32(eocd[16:], cdOffset)
	put16(eocd[20:], uint16(len(eocdComment)))
	out.Write(eocd)
	out.Write(eocdComment)
	return out.Bytes()
}

// patch32/patch16 replace a little-endian value at an absolute offset.
func patch32(b []byte, off int, v uint32) { binary.LittleEndian.PutUint32(b[off:], v) }
func patch16(b []byte, off int, v uint16) { binary.LittleEndian.PutUint16(b[off:], v) }

// indexSignature returns the first offset of sig starting at from.
func indexSignature(b []byte, sig uint32, from int) int {
	for i := from; i+4 <= len(b); i++ {
		if binary.LittleEndian.Uint32(b[i:]) == sig {
			return i
		}
	}
	return -1
}

func testPolicy() Policy {
	return Policy{
		MaxEntries:           1000,
		MaxArchiveSize:       64 << 20,
		MaxEntryUncompressed: 10 << 20,
		MaxTotalUncompressed: 100 << 20,
		MaxCompressionRatio:  100,
		RatioFloorBytes:      64,
		RejectSymlink:        true,
		RejectSetID:          true,
		RejectSpecial:        true,
	}
}
