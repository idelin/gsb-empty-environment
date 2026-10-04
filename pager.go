package bptree

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
)

// pager owns the page file. Page 0 is the meta page; pages 1..numPages-1
// hold tree nodes or are free. All reads use ReadAt and all writes use
// WriteAt, so concurrent readers never block on the writer. Pager state
// (numPages, freeSet, root, seq) is only mutated under DB.mu.
type pager struct {
	f        *os.File
	numPages uint64
	freeSet  map[uint64]bool
	freeList []uint64 // stack of free page ids

	capacity int
	root     uint64
	seq      uint64

	// failWrite is a test hook: when true, writePage fails.
	failWrite bool
}

var errInjectedWrite = errors.New("bptree: injected write failure")

func openPager(path string, capacity int) (*pager, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	p := &pager{f: f, freeSet: map[uint64]bool{}}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if st.Size() == 0 {
		// Fresh file: create an empty tree.
		if err := checkCapacity(capacity); err != nil {
			f.Close()
			return nil, err
		}
		p.capacity = capacity
		p.numPages = 2
		p.root = 1
		p.seq = 0
		root := &node{id: 1, version: 0, leaf: true}
		if _, err := f.WriteAt(root.encode(), pageOffset(1)); err != nil {
			f.Close()
			return nil, err
		}
		if err := p.writeMeta(); err != nil {
			f.Close()
			return nil, err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return nil, err
		}
		return p, nil
	}
	// Existing file: load and verify meta page.
	if st.Size() < PageSize || st.Size()%PageSize != 0 {
		f.Close()
		return nil, fmt.Errorf("%w: bad file size %d", ErrCorrupt, st.Size())
	}
	if err := p.readMeta(); err != nil {
		f.Close()
		return nil, err
	}
	if capacity != 0 && capacity != p.capacity {
		f.Close()
		return nil, fmt.Errorf("%w: file uses capacity %d", ErrInvalidCapacity, p.capacity)
	}
	// Rebuild the free list by scanning page headers.
	hdr := make([]byte, headerSize)
	for id := uint64(1); id < p.numPages; id++ {
		if _, err := f.ReadAt(hdr, pageOffset(id)); err != nil {
			f.Close()
			return nil, fmt.Errorf("read page %d header: %w", id, err)
		}
		if pageTypeOf(hdr) == pageTypeFree {
			p.freeSet[id] = true
			p.freeList = append(p.freeList, id)
		}
	}
	return p, nil
}

func pageOffset(id uint64) int64 { return int64(id) * PageSize }

func (p *pager) writeMeta() error {
	buf := make([]byte, PageSize)
	binary.LittleEndian.PutUint64(buf[0:8], metaMagic)
	binary.LittleEndian.PutUint64(buf[8:16], p.seq)
	binary.LittleEndian.PutUint64(buf[16:24], p.root)
	binary.LittleEndian.PutUint64(buf[24:32], uint64(p.capacity))
	binary.LittleEndian.PutUint64(buf[32:40], p.numPages)
	crc := crc32.NewIEEE()
	crc.Write(buf[0:40])
	binary.LittleEndian.PutUint32(buf[40:44], crc.Sum32())
	if p.failWrite {
		return errInjectedWrite
	}
	if _, err := p.f.WriteAt(buf, 0); err != nil {
		return err
	}
	return nil
}

func (p *pager) readMeta() error {
	buf := make([]byte, PageSize)
	if _, err := p.f.ReadAt(buf, 0); err != nil {
		return fmt.Errorf("%w: read meta: %v", ErrCorrupt, err)
	}
	if binary.LittleEndian.Uint64(buf[0:8]) != metaMagic {
		return fmt.Errorf("%w: bad meta magic", ErrCorrupt)
	}
	crc := crc32.NewIEEE()
	crc.Write(buf[0:40])
	if binary.LittleEndian.Uint32(buf[40:44]) != crc.Sum32() {
		return fmt.Errorf("%w: meta checksum mismatch", ErrCorrupt)
	}
	p.seq = binary.LittleEndian.Uint64(buf[8:16])
	p.root = binary.LittleEndian.Uint64(buf[16:24])
	p.capacity = int(binary.LittleEndian.Uint64(buf[24:32]))
	p.numPages = binary.LittleEndian.Uint64(buf[32:40])
	if err := checkCapacity(p.capacity); err != nil {
		return fmt.Errorf("%w: meta capacity: %v", ErrCorrupt, err)
	}
	if p.root == 0 || p.root >= p.numPages {
		return fmt.Errorf("%w: meta root %d out of range", ErrCorrupt, p.root)
	}
	return nil
}

// alloc returns a page id, reusing free pages before extending the file.
func (p *pager) alloc() (id uint64, fromFree bool) {
	if n := len(p.freeList); n > 0 {
		id = p.freeList[n-1]
		p.freeList = p.freeList[:n-1]
		delete(p.freeSet, id)
		return id, true
	}
	id = p.numPages
	p.numPages++
	return id, false
}

// rollbackAlloc undoes an alloc that will never be published.
func (p *pager) rollbackAlloc(id uint64, fromFree bool) {
	if fromFree {
		p.freeSet[id] = true
		p.freeList = append(p.freeList, id)
		return
	}
	p.numPages--
}

// free marks a page free on disk and in memory. Callers must guarantee the
// page is unreachable from every active root.
func (p *pager) free(id uint64) error {
	if p.freeSet[id] || id == 0 || id >= p.numPages {
		return fmt.Errorf("bptree: invalid free of page %d", id)
	}
	if _, err := p.f.WriteAt(encodeFreePage(id), pageOffset(id)); err != nil {
		return err
	}
	p.freeSet[id] = true
	p.freeList = append(p.freeList, id)
	return nil
}

func (p *pager) readRaw(id uint64) ([]byte, error) {
	buf := make([]byte, PageSize)
	if _, err := p.f.ReadAt(buf, pageOffset(id)); err != nil && err != io.EOF {
		return nil, err
	}
	return buf, nil
}

func (p *pager) readNode(id uint64) (*node, error) {
	if id == 0 || id >= p.numPages {
		return nil, fmt.Errorf("%w: page %d out of range [1,%d)", ErrCorrupt, id, p.numPages)
	}
	buf, err := p.readRaw(id)
	if err != nil {
		return nil, err
	}
	n, err := decodeNode(buf)
	if err != nil {
		return nil, fmt.Errorf("page %d: %w", id, err)
	}
	if n.id != id {
		return nil, fmt.Errorf("%w: page %d has mismatched id %d", ErrCorrupt, id, n.id)
	}
	return n, nil
}

func (p *pager) writeNode(n *node) error {
	if p.failWrite {
		return errInjectedWrite
	}
	_, err := p.f.WriteAt(n.encode(), pageOffset(n.id))
	return err
}

func (p *pager) sync() error {
	if p.failWrite {
		return errInjectedWrite
	}
	return p.f.Sync()
}

func (p *pager) close() error {
	p.f.Sync()
	return p.f.Close()
}
