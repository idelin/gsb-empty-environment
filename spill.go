package bigtxn

import (
	"encoding/gob"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
)

// SpillBuffer persists chunks to temporary disk files when the memory
// budget is exhausted, and reads them back for delivery. Files are
// removed as soon as their chunk has been delivered.
type SpillBuffer struct {
	mu      sync.Mutex
	dir     string
	n       int
	written int64
}

// NewSpillBuffer creates a spill buffer rooted at dir. An empty dir
// means a fresh temporary directory.
func NewSpillBuffer(dir string) (*SpillBuffer, error) {
	if dir == "" {
		d, err := ioutil.TempDir("", "bigtxn-spill-")
		if err != nil {
			return nil, err
		}
		dir = d
	} else if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	return &SpillBuffer{dir: dir}, nil
}

// Dir returns the spill directory.
func (s *SpillBuffer) Dir() string { return s.dir }

// Written reports how many chunks have been spilled so far.
func (s *SpillBuffer) Written() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.written
}

// Write serializes the chunk to a new spill file and returns its path.
func (s *SpillBuffer) Write(c *Chunk) (string, error) {
	s.mu.Lock()
	s.n++
	name := filepath.Join(s.dir, fmt.Sprintf("chunk-txn%d-seq%06d-%06d.gob", c.TxnID, c.Seq, s.n))
	s.written++
	s.mu.Unlock()

	f, err := os.Create(name)
	if err != nil {
		return "", err
	}
	if err := gob.NewEncoder(f).Encode(c); err != nil {
		f.Close()
		os.Remove(name)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

// Read loads a spilled chunk back from disk.
func (s *SpillBuffer) Read(path string) (*Chunk, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var c Chunk
	if err := gob.NewDecoder(f).Decode(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Remove deletes a spill file after successful delivery.
func (s *SpillBuffer) Remove(path string) error {
	return os.Remove(path)
}
