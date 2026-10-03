package aries

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"
)

// PageStore is a log-structured page file. Pages are stored as compressed
// blobs (see SparsePage.Serialize); an in-memory index maps pageID to the
// latest blob location and is rebuilt by scanning the file on open.
type PageStore struct {
	mu        sync.RWMutex
	f         *os.File
	index     map[uint64]pageLoc
	appendOff uint64
}

type pageLoc struct {
	off    uint64
	length uint32
}

const pageHeaderSize = 12 // 8-byte pageID + 4-byte blob length

// OpenPageStore opens (or creates) the page file and rebuilds the index.
func OpenPageStore(path string) (*PageStore, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	s := &PageStore{f: f, index: map[uint64]pageLoc{}}
	if err := s.rebuildIndex(); err != nil {
		f.Close()
		return nil, err
	}
	return s, nil
}

func (s *PageStore) rebuildIndex() error {
	if _, err := s.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	data, err := io.ReadAll(s.f)
	if err != nil {
		return err
	}
	off := uint64(0)
	for off+pageHeaderSize <= uint64(len(data)) {
		pid := binary.LittleEndian.Uint64(data[off:])
		length := binary.LittleEndian.Uint32(data[off+8:])
		blobStart := off + pageHeaderSize
		blobEnd := blobStart + uint64(length)
		if blobEnd > uint64(len(data)) {
			return fmt.Errorf("pagestore: truncated blob for page %d", pid)
		}
		s.index[pid] = pageLoc{off: blobStart, length: length}
		off = blobEnd
	}
	s.appendOff = off
	return nil
}

// ReadPage returns the page with the given id, or a fresh empty page if the
// id has never been written.
func (s *PageStore) ReadPage(id uint64) (*SparsePage, error) {
	s.mu.RLock()
	loc, ok := s.index[id]
	s.mu.RUnlock()
	if !ok {
		return NewSparsePage(), nil
	}
	buf := make([]byte, loc.length)
	if _, err := s.f.ReadAt(buf, int64(loc.off)); err != nil {
		return nil, err
	}
	return DeserializePage(buf)
}

// WritePage serializes p, appends the blob to the file, updates the index
// and fsyncs. Safe for concurrent use.
func (s *PageStore) WritePage(id uint64, p *SparsePage) error {
	blob := p.Serialize()
	rec := make([]byte, pageHeaderSize, pageHeaderSize+len(blob))
	binary.LittleEndian.PutUint64(rec[0:], id)
	binary.LittleEndian.PutUint32(rec[8:], uint32(len(blob)))
	rec = append(rec, blob...)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.f.WriteAt(rec, int64(s.appendOff)); err != nil {
		return err
	}
	if err := s.f.Sync(); err != nil {
		return err
	}
	s.index[id] = pageLoc{off: s.appendOff + pageHeaderSize, length: uint32(len(blob))}
	s.appendOff += uint64(len(rec))
	return nil
}

// Close closes the underlying file.
func (s *PageStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f.Close()
}
