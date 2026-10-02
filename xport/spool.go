package xport

import (
	"encoding/gob"
	"fmt"
	"os"
)

// Spool persists chunks to a temporary file when the in-memory budget is
// exhausted, so the receiver never has to buffer a whole transaction.
type Spool struct {
	path  string
	f     *os.File
	enc   *gob.Encoder
	count int
	bytes int64
}

// NewSpool creates a temporary spill file inside dir (the system temp dir
// is used when dir is empty).
func NewSpool(dir string) (*Spool, error) {
	f, err := os.CreateTemp(dir, "bigtx-spool-*.bin")
	if err != nil {
		return nil, fmt.Errorf("spool: create temp file: %w", err)
	}
	return &Spool{path: f.Name(), f: f, enc: gob.NewEncoder(f)}, nil
}

// Path returns the backing file path.
func (s *Spool) Path() string { return s.path }

// Write appends a chunk to the spill file.
func (s *Spool) Write(c *Chunk) error {
	if err := s.enc.Encode(c); err != nil {
		return fmt.Errorf("spool: encode chunk %d: %w", c.Seq, err)
	}
	s.count++
	s.bytes += c.Bytes()
	return nil
}

// Close flushes and closes the write side of the spool.
func (s *Spool) Close() error {
	if s.f == nil {
		return nil
	}
	err := s.f.Close()
	s.f = nil
	return err
}

// Stats reports how many chunks and payload bytes were spilled.
func (s *Spool) Stats() (chunks int, bytes int64) { return s.count, s.bytes }

// Open reopens the spill file for sequential reading.
func (s *Spool) Open() (*SpoolReader, error) {
	f, err := os.Open(s.path)
	if err != nil {
		return nil, fmt.Errorf("spool: open for read: %w", err)
	}
	return &SpoolReader{f: f, dec: gob.NewDecoder(f)}, nil
}

// Remove deletes the spill file.
func (s *Spool) Remove() {
	_ = s.Close()
	_ = os.Remove(s.path)
}

// SpoolReader streams chunks back from a spill file, one at a time.
type SpoolReader struct {
	f   *os.File
	dec *gob.Decoder
}

// Next returns the next chunk, or io.EOF when the spool is drained.
func (r *SpoolReader) Next() (*Chunk, error) {
	var c Chunk
	if err := r.dec.Decode(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Close closes the reader.
func (r *SpoolReader) Close() error { return r.f.Close() }
