// Package pagereloc implements offline compaction of page files.
//
// A page file is a fixed-page-size binary container. Each allocated page
// carries an external stable object ID, an opaque payload, and references
// to other pages by page number. Compaction rewrites the file so that all
// reachable pages are densely numbered, updating every internal reference
// while preserving external object IDs and page sharing.
package pagereloc

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sort"
)

const (
	Magic           = "PGRL0001"
	Version         = 1
	headerSize      = 32
	pageHeaderSize  = 20 // flags(4) + objectID(8) + payloadLen(4) + refCount(4)
	DefaultPageSize = 256
)

const flagAllocated uint32 = 1 << 0

// Page is one allocated page in a page file.
type Page struct {
	ObjectID uint64   // external stable object ID; never rewritten
	Payload  []byte   // opaque content
	Refs     []uint32 // page numbers this page points to
}

// File is an in-memory image of a page file. Pages is sparse: it only
// contains allocated pages, keyed by their page number.
type File struct {
	PageSize uint32
	Root     uint32
	Pages    map[uint32]*Page
}

// pageCount returns the number of page slots the file occupies on disk,
// i.e. one past the highest allocated page number.
func (f *File) pageCount() uint32 {
	var max uint32
	for n := range f.Pages {
		if n >= max {
			max = n + 1
		}
	}
	if f.Root >= max {
		max = f.Root + 1
	}
	return max
}

// pageFits reports whether a page with the given reference count and
// payload length fits in one slot.
func pageFits(pageSize uint32, refCount, payloadLen int) bool {
	need := pageHeaderSize + 4*refCount + payloadLen
	return uint64(need) <= uint64(pageSize)
}

// ReadFile parses a page file from disk. It is strict about structural
// corruption (bad magic, truncated slots, oversized pages) but does not
// validate reference integrity; that is the validator's job.
func ReadFile(path string) (*File, error) {
	raw, err := readAll(path)
	if err != nil {
		return nil, err
	}
	if len(raw) < headerSize {
		return nil, fmt.Errorf("pagereloc: %s: file too small for header", path)
	}
	if string(raw[:8]) != Magic {
		return nil, fmt.Errorf("pagereloc: %s: bad magic %q", path, raw[:8])
	}
	version := binary.LittleEndian.Uint32(raw[8:12])
	if version != Version {
		return nil, fmt.Errorf("pagereloc: %s: unsupported version %d", path, version)
	}
	pageSize := binary.LittleEndian.Uint32(raw[12:16])
	pageCount := binary.LittleEndian.Uint32(raw[16:20])
	root := binary.LittleEndian.Uint32(raw[20:24])
	if pageSize < pageHeaderSize {
		return nil, fmt.Errorf("pagereloc: %s: page size %d too small", path, pageSize)
	}
	if uint64(headerSize)+uint64(pageSize)*uint64(pageCount) > uint64(len(raw)) {
		return nil, fmt.Errorf("pagereloc: %s: truncated: header promises %d pages of %d bytes",
			path, pageCount, pageSize)
	}
	if pageCount > 0 && root >= pageCount {
		return nil, fmt.Errorf("pagereloc: %s: root page %d out of range (%d pages)",
			path, root, pageCount)
	}

	f := &File{PageSize: pageSize, Root: root, Pages: make(map[uint32]*Page)}
	for n := uint32(0); n < pageCount; n++ {
		off := headerSize + int(n)*int(pageSize)
		slot := raw[off : off+int(pageSize)]
		flags := binary.LittleEndian.Uint32(slot[0:4])
		if flags&flagAllocated == 0 {
			continue
		}
		p := &Page{
			ObjectID: binary.LittleEndian.Uint64(slot[4:12]),
		}
		payloadLen := binary.LittleEndian.Uint32(slot[12:16])
		refCount := binary.LittleEndian.Uint32(slot[16:20])
		if !pageFits(pageSize, int(refCount), int(payloadLen)) {
			return nil, fmt.Errorf("pagereloc: %s: page %d corrupt: %d refs + %d payload bytes exceed page size %d",
				path, n, refCount, payloadLen, pageSize)
		}
		cur := pageHeaderSize
		for i := uint32(0); i < refCount; i++ {
			p.Refs = append(p.Refs, binary.LittleEndian.Uint32(slot[cur:cur+4]))
			cur += 4
		}
		p.Payload = append([]byte(nil), slot[cur:cur+int(payloadLen)]...)
		f.Pages[n] = p
	}
	if _, ok := f.Pages[root]; !ok && pageCount > 0 {
		return nil, fmt.Errorf("pagereloc: %s: root page %d is not allocated", path, root)
	}
	return f, nil
}

// write serializes the file image to w.
func (f *File) write(w io.Writer, crashAfterPages int) error {
	pageCount := f.pageCount()
	hdr := make([]byte, headerSize)
	copy(hdr[:8], Magic)
	binary.LittleEndian.PutUint32(hdr[8:12], Version)
	binary.LittleEndian.PutUint32(hdr[12:16], f.PageSize)
	binary.LittleEndian.PutUint32(hdr[16:20], pageCount)
	binary.LittleEndian.PutUint32(hdr[20:24], f.Root)
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	for n := uint32(0); n < pageCount; n++ {
		slot := make([]byte, f.PageSize)
		if p, ok := f.Pages[n]; ok {
			if !pageFits(f.PageSize, len(p.Refs), len(p.Payload)) {
				return fmt.Errorf("pagereloc: page %d does not fit in %d-byte slot", n, f.PageSize)
			}
			binary.LittleEndian.PutUint32(slot[0:4], flagAllocated)
			binary.LittleEndian.PutUint64(slot[4:12], p.ObjectID)
			binary.LittleEndian.PutUint32(slot[12:16], uint32(len(p.Payload)))
			binary.LittleEndian.PutUint32(slot[16:20], uint32(len(p.Refs)))
			cur := pageHeaderSize
			for _, r := range p.Refs {
				binary.LittleEndian.PutUint32(slot[cur:cur+4], r)
				cur += 4
			}
			copy(slot[cur:], p.Payload)
		}
		if _, err := w.Write(slot); err != nil {
			return err
		}
		// crashAfterPages is a test hook simulating an abrupt exit
		// (power loss) partway through the page stream.
		if crashAfterPages > 0 && int(n+1) == crashAfterPages {
			return errSimulatedCrash
		}
	}
	return nil
}

// writeFileAtomicContent writes f to path with an fsync before close.
// Callers are responsible for writing to a temporary path and renaming.
func writeFileSync(path string, f *File, crashAfterPages int) error {
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	werr := f.write(out, crashAfterPages)
	serr := out.Sync()
	cerr := out.Close()
	if werr != nil {
		return werr
	}
	if serr != nil {
		return serr
	}
	return cerr
}

func readAll(path string) ([]byte, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	return readAllFrom(fh)
}

func readAllFrom(r io.Reader) ([]byte, error) {
	var buf []byte
	tmp := make([]byte, 32*1024)
	for {
		n, err := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err == io.EOF {
			return buf, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// sortedPageNumbers returns the allocated page numbers in ascending order.
func (f *File) sortedPageNumbers() []uint32 {
	nums := make([]uint32, 0, len(f.Pages))
	for n := range f.Pages {
		nums = append(nums, n)
	}
	sort.Slice(nums, func(i, j int) bool { return nums[i] < nums[j] })
	return nums
}
