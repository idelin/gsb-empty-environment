// Package pagefile implements an offline page-file compaction and
// relocation tool. Compaction drops free slots so page numbers change;
// every in-file reference is rewritten through a relocation table while
// external stable object IDs are preserved unchanged.
package pagefile

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

const (
	// Magic identifies a page file.
	Magic = "PGRLC001"
	// Version is the on-disk format version.
	Version = 1
	// HeaderSize is the fixed file header size in bytes.
	HeaderSize = 32
	// DefaultPageSize is the slot size used by builders when unspecified.
	DefaultPageSize = 4096

	pageMagic   = "PAGE"
	pageHdrSize = 20 // magic(4) + externalID(8) + refCount(4) + payloadLen(4)
)

// Page is a single occupied slot in a page file. ExternalID is the stable
// object ID visible to external systems; it never changes across
// compaction. Refs are page numbers of other slots in the same file.
type Page struct {
	ExternalID uint64
	Refs       []uint32
	Payload    []byte
}

// File is an in-memory image of a page file. Pages may contain nil slots
// (free pages); a compacted file never does.
type File struct {
	PageSize uint32
	Root     uint32
	Pages    []*Page
}

// PageCount returns the number of slots, including free ones.
func (f *File) PageCount() uint32 { return uint32(len(f.Pages)) }

// LivePages returns the number of occupied slots.
func (f *File) LivePages() int {
	n := 0
	for _, p := range f.Pages {
		if p != nil {
			n++
		}
	}
	return n
}

func encodePage(p *Page, pageSize uint32) ([]byte, error) {
	need := pageHdrSize + 4*len(p.Refs) + len(p.Payload)
	if uint32(need) > pageSize {
		return nil, fmt.Errorf("page needs %d bytes, page size is %d", need, pageSize)
	}
	buf := make([]byte, need)
	copy(buf[0:4], pageMagic)
	binary.LittleEndian.PutUint64(buf[4:12], p.ExternalID)
	binary.LittleEndian.PutUint32(buf[12:16], uint32(len(p.Refs)))
	binary.LittleEndian.PutUint32(buf[16:20], uint32(len(p.Payload)))
	off := pageHdrSize
	for _, r := range p.Refs {
		binary.LittleEndian.PutUint32(buf[off:off+4], r)
		off += 4
	}
	copy(buf[off:], p.Payload)
	return buf, nil
}

func decodePage(slot []byte) (*Page, error) {
	if len(slot) < pageHdrSize {
		return nil, fmt.Errorf("slot smaller than page header")
	}
	if string(slot[0:4]) != pageMagic {
		return nil, fmt.Errorf("bad page magic %q", string(slot[0:4]))
	}
	p := &Page{ExternalID: binary.LittleEndian.Uint64(slot[4:12])}
	refCount := binary.LittleEndian.Uint32(slot[12:16])
	payloadLen := binary.LittleEndian.Uint32(slot[16:20])
	need := pageHdrSize + 4*int(refCount) + int(payloadLen)
	if need > len(slot) {
		return nil, fmt.Errorf("page claims %d bytes, slot is %d", need, len(slot))
	}
	off := pageHdrSize
	for i := 0; i < int(refCount); i++ {
		p.Refs = append(p.Refs, binary.LittleEndian.Uint32(slot[off:off+4]))
		off += 4
	}
	p.Payload = append([]byte(nil), slot[off:off+int(payloadLen)]...)
	return p, nil
}

// WriteTo serializes f to w. If progress is non-nil it is called after the
// header and after every page slot with the total bytes written so far; a
// non-nil return aborts the write (used to simulate mid-write crashes).
func WriteTo(w io.Writer, f *File, progress func(off int64) error) (int64, error) {
	var off int64
	write := func(b []byte) error {
		n, err := w.Write(b)
		off += int64(n)
		if err != nil {
			return err
		}
		if n != len(b) {
			return io.ErrShortWrite
		}
		return nil
	}
	if f.PageSize < pageHdrSize {
		return 0, fmt.Errorf("page size %d too small", f.PageSize)
	}
	if int(f.Root) >= len(f.Pages) {
		return 0, fmt.Errorf("root page %d out of range (%d slots)", f.Root, len(f.Pages))
	}
	hdr := make([]byte, HeaderSize)
	copy(hdr[0:8], Magic)
	binary.LittleEndian.PutUint32(hdr[8:12], Version)
	binary.LittleEndian.PutUint32(hdr[12:16], f.PageSize)
	binary.LittleEndian.PutUint32(hdr[16:20], uint32(len(f.Pages)))
	binary.LittleEndian.PutUint32(hdr[20:24], f.Root)
	if err := write(hdr); err != nil {
		return off, err
	}
	if progress != nil {
		if err := progress(off); err != nil {
			return off, err
		}
	}
	for i, p := range f.Pages {
		slot := make([]byte, f.PageSize)
		if p != nil {
			data, err := encodePage(p, f.PageSize)
			if err != nil {
				return off, fmt.Errorf("page %d: %v", i, err)
			}
			copy(slot, data)
		}
		if err := write(slot); err != nil {
			return off, err
		}
		if progress != nil {
			if err := progress(off); err != nil {
				return off, err
			}
		}
	}
	return off, nil
}

// Read parses a page file from r.
func Read(r io.Reader) (*File, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(raw) < HeaderSize {
		return nil, fmt.Errorf("file too small: %d bytes", len(raw))
	}
	if string(raw[0:8]) != Magic {
		return nil, fmt.Errorf("bad file magic %q", string(raw[0:8]))
	}
	if v := binary.LittleEndian.Uint32(raw[8:12]); v != Version {
		return nil, fmt.Errorf("unsupported version %d", v)
	}
	pageSize := binary.LittleEndian.Uint32(raw[12:16])
	pageCount := binary.LittleEndian.Uint32(raw[16:20])
	root := binary.LittleEndian.Uint32(raw[20:24])
	if pageSize < pageHdrSize {
		return nil, fmt.Errorf("page size %d too small", pageSize)
	}
	want := HeaderSize + int64(pageSize)*int64(pageCount)
	if int64(len(raw)) != want {
		return nil, fmt.Errorf("file size %d, expected %d (%d pages of %d bytes)", len(raw), want, pageCount, pageSize)
	}
	if root >= pageCount {
		return nil, fmt.Errorf("root page %d out of range (%d slots)", root, pageCount)
	}
	f := &File{PageSize: pageSize, Root: root, Pages: make([]*Page, pageCount)}
	for i := uint32(0); i < pageCount; i++ {
		slot := raw[HeaderSize+int64(i)*int64(pageSize) : HeaderSize+int64(i+1)*int64(pageSize)]
		if bytes.Count(slot, []byte{0}) == len(slot) {
			continue // free slot
		}
		p, err := decodePage(slot)
		if err != nil {
			return nil, fmt.Errorf("page %d: %v", i, err)
		}
		f.Pages[i] = p
	}
	return f, nil
}
