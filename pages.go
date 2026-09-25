package rootstore

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// PageSize is the fixed page size of the page file.
	PageSize = 256
	// MaxChildren is the maximum number of page references per page.
	MaxChildren = 16
	// MaxPageData is the maximum payload bytes per page.
	MaxPageData = 96
	pageMagic   = 0x50414745524f4f54 // "PAGEROOT"
	pageCRCOff  = 244
)

var ErrPageInvalid = errors.New("invalid page")

// Page is a fixed-size node in a page graph; Children are references to
// other pages and form the reachable closure of a root.
type Page struct {
	ID       uint64
	Children []uint64
	Data     []byte
}

// PageFile is a grow-only array of fixed-size pages in one file.
type PageFile struct {
	f     File
	count uint64
}

func openPageFile(f File) (*PageFile, error) {
	size, err := f.Size()
	if err != nil {
		return nil, err
	}
	return &PageFile{f: f, count: uint64(size / PageSize)}, nil
}

// Count returns the number of allocated pages.
func (pf *PageFile) Count() uint64 { return pf.count }

// Page layout (little-endian), zero-padded to PageSize:
//
//	[0:8]     magic
//	[8:16]    page id (must match its offset)
//	[16:18]   child count
//	[18:20]   data length
//	[20:148]  child page ids (16 x 8)
//	[148:244] payload
//	[244:248] CRC32-Castagnoli over [0:244]
func (pf *PageFile) WritePage(pg *Page) error {
	if len(pg.Children) > MaxChildren {
		return fmt.Errorf("page %d: too many children", pg.ID)
	}
	if len(pg.Data) > MaxPageData {
		return fmt.Errorf("page %d: payload too large", pg.ID)
	}
	buf := make([]byte, PageSize)
	binary.LittleEndian.PutUint64(buf[0:], pageMagic)
	binary.LittleEndian.PutUint64(buf[8:], pg.ID)
	binary.LittleEndian.PutUint16(buf[16:], uint16(len(pg.Children)))
	binary.LittleEndian.PutUint16(buf[18:], uint16(len(pg.Data)))
	for i, c := range pg.Children {
		binary.LittleEndian.PutUint64(buf[20+i*8:], c)
	}
	copy(buf[20+MaxChildren*8:], pg.Data)
	binary.LittleEndian.PutUint32(buf[pageCRCOff:], checksum(buf[:pageCRCOff]))
	_, err := pf.f.WriteAt(buf, int64(pg.ID)*PageSize)
	return err
}

// ReadPage reads and fully validates one page; a corrupt page is never
// returned as valid.
func (pf *PageFile) ReadPage(id uint64) (*Page, error) {
	buf := make([]byte, PageSize)
	if _, err := pf.f.ReadAt(buf, int64(id)*PageSize); err != nil {
		return nil, fmt.Errorf("page %d unreadable: %w", id, err)
	}
	if binary.LittleEndian.Uint64(buf[0:]) != pageMagic {
		return nil, fmt.Errorf("%w: page %d bad magic", ErrPageInvalid, id)
	}
	want := binary.LittleEndian.Uint32(buf[pageCRCOff:])
	if got := checksum(buf[:pageCRCOff]); want != got {
		return nil, fmt.Errorf("%w: page %d checksum mismatch", ErrPageInvalid, id)
	}
	pg := &Page{ID: binary.LittleEndian.Uint64(buf[8:])}
	if pg.ID != id {
		return nil, fmt.Errorf("%w: page %d carries embedded id %d", ErrPageInvalid, id, pg.ID)
	}
	nch, nd := binary.LittleEndian.Uint16(buf[16:]), binary.LittleEndian.Uint16(buf[18:])
	if nch > MaxChildren || nd > MaxPageData {
		return nil, fmt.Errorf("%w: page %d bad counts", ErrPageInvalid, id)
	}
	for i := 0; i < int(nch); i++ {
		pg.Children = append(pg.Children, binary.LittleEndian.Uint64(buf[20+i*8:]))
	}
	pg.Data = append(pg.Data, buf[20+MaxChildren*8:20+MaxChildren*8+int(nd)]...)
	return pg, nil
}

func (pf *PageFile) Sync() error { return pf.f.Sync() }

// VerifyClosure independently checks that the root page and every page
// reachable from it are present, checksum-valid and self-consistent. It
// reads pages straight from storage and is used both before publishing a
// slot and as a stand-alone verifier for a recovered root.
func VerifyClosure(pf *PageFile, root uint64) error {
	seen := map[uint64]bool{}
	stack := []uint64{root}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		pg, err := pf.ReadPage(id)
		if err != nil {
			return fmt.Errorf("closure of root %d: %w", root, err)
		}
		stack = append(stack, pg.Children...)
	}
	return nil
}
