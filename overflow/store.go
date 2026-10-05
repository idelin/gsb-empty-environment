// Package overflow implements a copy-on-write overflow page store with
// lazy reclamation. Records larger than one page are stored as chains of
// overflow pages. Updates write a fresh chain and then atomically switch
// the root reference in the superblock, so a failed update never loses
// the old record. Reclamation only frees pages that are unreachable from
// every record root.
package overflow

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
)

const (
	statusFree = 0x00
	statusUsed = 0xA5

	// nilPage is the null page id. Page 0 is the superblock and never
	// holds record data, so 0 doubles as "no page".
	nilPage = 0

	// sbPage is the page id of the superblock.
	sbPage = 0

	// headerSize is the per-page chain header: status(1) next(8) dataLen(4).
	headerSize = 13

	sbMagic       = "OVFLIB01"
	defaultPageSz = 4096
	minPageSize   = 64
)

// ErrNotFound is returned by Get and Delete for unknown record names.
var ErrNotFound = errors.New("overflow: record not found")

// Corruption kinds reported by CorruptError.
const (
	CorruptCycle       = "cycle"
	CorruptSharedPage  = "shared-page"
	CorruptFreePageRef = "free-page-referenced"
	CorruptBadStatus   = "bad-status"
	CorruptOutOfRange  = "page-out-of-range"
	CorruptBadLength   = "bad-data-length"
	CorruptChecksum    = "superblock-checksum"
)

// CorruptError reports a structurally invalid chain or page table.
type CorruptError struct {
	Kind   string
	Record string
	Page   uint64
}

func (e *CorruptError) Error() string {
	if e.Record != "" {
		return fmt.Sprintf("overflow: corrupt structure (%s) in record %q at page %d", e.Kind, e.Record, e.Page)
	}
	return fmt.Sprintf("overflow: corrupt structure (%s) at page %d", e.Kind, e.Page)
}

// pager abstracts page IO so tests can inject write failures.
type pager interface {
	readPage(id uint64) ([]byte, error)
	writePage(id uint64, data []byte) error
	sync() error
	close() error
}

// filePager is a pager backed by a single data file; page i lives at
// offset i*pageSize.
type filePager struct {
	f        *os.File
	pageSize int
}

func (p *filePager) readPage(id uint64) ([]byte, error) {
	buf := make([]byte, p.pageSize)
	if _, err := p.f.ReadAt(buf, int64(id)*int64(p.pageSize)); err != nil {
		return nil, err
	}
	return buf, nil
}

func (p *filePager) writePage(id uint64, data []byte) error {
	if len(data) != p.pageSize {
		return fmt.Errorf("overflow: short page write: %d != %d", len(data), p.pageSize)
	}
	_, err := p.f.WriteAt(data, int64(id)*int64(p.pageSize))
	return err
}

func (p *filePager) sync() error  { return p.f.Sync() }
func (p *filePager) close() error { return p.f.Close() }

// chain page layout helpers

func encodeChainPage(buf []byte, next uint64, chunk []byte) {
	buf[0] = statusUsed
	binary.LittleEndian.PutUint64(buf[1:9], next)
	binary.LittleEndian.PutUint32(buf[9:13], uint32(len(chunk)))
	copy(buf[headerSize:], chunk)
}

func decodeChainPage(page []byte) (next uint64, chunk []byte) {
	next = binary.LittleEndian.Uint64(page[1:9])
	n := binary.LittleEndian.Uint32(page[9:13])
	return next, page[headerSize : headerSize+int(n)]
}
