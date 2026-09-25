package pagestore

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// PageSize is the number of user data bytes carried by one page.
const PageSize = 4096

// On-disk page layout: version(8) | checksum(4) | data(PageSize).
// The checksum covers version+data, so a torn or corrupted write of
// either the header or the payload is detected on read.
const pageDiskSize = 8 + 4 + PageSize

var errPageChecksum = errors.New("pagestore: page checksum mismatch")

// page is an in-memory page image. version is the LSN of the WAL record
// that produced this image; it makes replay idempotent.
type page struct {
	version uint64
	data    []byte // always exactly PageSize bytes
}

func (p *page) marshal() []byte {
	buf := make([]byte, pageDiskSize)
	binary.LittleEndian.PutUint64(buf[0:8], p.version)
	copy(buf[12:], p.data)
	c := crc32.NewIEEE()
	c.Write(buf[0:8])
	c.Write(buf[12:])
	binary.LittleEndian.PutUint32(buf[8:12], c.Sum32())
	return buf
}

func unmarshalPage(buf []byte) (*page, error) {
	if len(buf) != pageDiskSize {
		return nil, errors.New("pagestore: short page image")
	}
	// An all-zero page is a valid never-written page (version 0). This is
	// what holes in the sparse data file look like.
	zero := true
	for _, b := range buf {
		if b != 0 {
			zero = false
			break
		}
	}
	if zero {
		return &page{version: 0, data: make([]byte, PageSize)}, nil
	}
	c := crc32.NewIEEE()
	c.Write(buf[0:8])
	c.Write(buf[12:])
	if binary.LittleEndian.Uint32(buf[8:12]) != c.Sum32() {
		return nil, errPageChecksum
	}
	data := make([]byte, PageSize)
	copy(data, buf[12:])
	return &page{version: binary.LittleEndian.Uint64(buf[0:8]), data: data}, nil
}
