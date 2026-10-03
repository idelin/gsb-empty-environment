package wal

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"io/ioutil"
	"os"
	"sync"
)

// LogStore abstracts stable log storage. Appends first land in a
// volatile buffer (the log buffer in memory); Flush makes them durable.
// LoseVolatile models a crash: everything not yet flushed is gone.
type LogStore interface {
	AppendVolatile(frame []byte)
	Flush() error
	ReadStable() ([][]byte, error)
	LoseVolatile()
	StableLen() int
}

// EncodeFrame wraps a payload with length and CRC32 checksum.
func EncodeFrame(payload []byte) []byte {
	frame := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(payload)))
	binary.BigEndian.PutUint32(frame[4:8], crc32.ChecksumIEEE(payload))
	copy(frame[8:], payload)
	return frame
}

// SplitFrames parses a byte stream of frames, verifying checksums, and
// returns the raw frames (headers included).
func SplitFrames(data []byte) ([][]byte, error) {
	var out [][]byte
	for len(data) > 0 {
		if len(data) < 8 {
			return nil, fmt.Errorf("wal: truncated frame header (%d bytes left)", len(data))
		}
		n := binary.BigEndian.Uint32(data[0:4])
		crc := binary.BigEndian.Uint32(data[4:8])
		if n > 1<<24 {
			return nil, fmt.Errorf("wal: implausible frame length %d", n)
		}
		if len(data) < 8+int(n) {
			return nil, fmt.Errorf("wal: truncated frame body (want %d, have %d)", n, len(data)-8)
		}
		if crc32.ChecksumIEEE(data[8:8+n]) != crc {
			return nil, fmt.Errorf("wal: checksum mismatch on frame %d (log corrupted)", len(out))
		}
		cp := make([]byte, 8+n)
		copy(cp, data[:8+n])
		out = append(out, cp)
		data = data[8+n:]
	}
	return out, nil
}

// DecodeFrames parses a byte stream of frames, verifying checksums, and
// returns the payloads.
func DecodeFrames(data []byte) ([][]byte, error) {
	frames, err := SplitFrames(data)
	if err != nil {
		return nil, err
	}
	out := make([][]byte, 0, len(frames))
	for _, fr := range frames {
		n := binary.BigEndian.Uint32(fr[0:4])
		payload := make([]byte, n)
		copy(payload, fr[8:8+n])
		out = append(out, payload)
	}
	return out, nil
}

// MemStore is an in-memory LogStore used by tests and the simulator.
type MemStore struct {
	mu       sync.Mutex
	volatile [][]byte
	stable   [][]byte
}

func NewMemStore() *MemStore { return &MemStore{} }

func (m *MemStore) AppendVolatile(frame []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.volatile = append(m.volatile, frame)
}

func (m *MemStore) Flush() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stable = append(m.stable, m.volatile...)
	m.volatile = nil
	return nil
}

func (m *MemStore) ReadStable() ([][]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]byte, len(m.stable))
	copy(out, m.stable)
	return out, nil
}

func (m *MemStore) LoseVolatile() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.volatile = nil
}

func (m *MemStore) StableLen() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.stable)
}

// FlipByte deliberately corrupts one byte of a stable frame. It exists
// so fault-injection tests can verify checksum-based corruption
// detection.
func (m *MemStore) FlipByte(frameIdx, byteIdx int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stable[frameIdx][byteIdx] ^= 0xFF
}

// FileStore is a file-backed LogStore. Flush appends buffered frames to
// the file and fsyncs.
type FileStore struct {
	mu       sync.Mutex
	f        *os.File
	volatile [][]byte
}

func NewFileStore(path string) (*FileStore, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	return &FileStore{f: f}, nil
}

func (s *FileStore) AppendVolatile(frame []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.volatile = append(s.volatile, frame)
}

func (s *FileStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, fr := range s.volatile {
		if _, err := s.f.Write(fr); err != nil {
			return err
		}
	}
	s.volatile = nil
	return s.f.Sync()
}

func (s *FileStore) ReadStable() ([][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := ioutil.ReadAll(s.f)
	if err != nil {
		return nil, err
	}
	return SplitFrames(data)
}

func (s *FileStore) LoseVolatile() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.volatile = nil
}

func (s *FileStore) StableLen() int {
	frames, err := s.ReadStable()
	if err != nil {
		return -1
	}
	return len(frames)
}

func (s *FileStore) Close() error { return s.f.Close() }
