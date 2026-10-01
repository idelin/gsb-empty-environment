package cdc

import "crypto/sha256"

// Default chunking parameters. A boundary is declared when the rolling
// hash hits a 1-in-AvgChunkSize pattern, so chunks average ~8 KiB.
const (
	MinChunkSize = 2 * 1024
	AvgChunkSize = 8 * 1024
	MaxChunkSize = 64 * 1024

	chunkMask = AvgChunkSize - 1

	// tableSeed seeds the Buzhash substitution table. Keeping it fixed
	// makes chunk boundaries deterministic across runs and machines.
	tableSeed = 0xC0FFEE
)

// Chunk describes one content-defined chunk of a data stream.
type Chunk struct {
	Offset int               // byte offset within the stream
	Length int               // chunk length in bytes
	Hash   [sha256.Size]byte // SHA-256 of the chunk content
}

// Chunker splits a byte stream into content-defined chunks using a
// Buzhash rolling hash. Boundaries depend only on the content of a
// small sliding window, so a local edit only re-splits locally.
type Chunker struct {
	min  int
	max  int
	mask uint64
	bh   *Buzhash
}

// NewChunker returns a Chunker with the default parameters.
func NewChunker() *Chunker {
	return &Chunker{
		min:  MinChunkSize,
		max:  MaxChunkSize,
		mask: chunkMask,
		bh:   NewBuzhash(tableSeed),
	}
}

func makeChunk(data []byte, off int) Chunk {
	return Chunk{Offset: off, Length: len(data), Hash: sha256.Sum256(data)}
}

// Split chunks data in one shot and returns all chunks.
func (c *Chunker) Split(data []byte) []Chunk {
	var chunks []Chunk
	s := NewSplitter(c, func(ch Chunk, _ []byte) { chunks = append(chunks, ch) })
	s.Write(data)
	s.Close()
	return chunks
}

// Splitter incrementally finds chunk boundaries in a stream of bytes.
// Feed data with Write and finish with Close; every completed chunk is
// delivered to the OnChunk callback exactly once, in order.
type Splitter struct {
	c       *Chunker
	buf     []byte // bytes of the current, not-yet-closed chunk
	offset  int    // stream offset of buf[0]
	OnChunk func(ch Chunk, data []byte)
}

// NewSplitter creates a streaming splitter using chunker parameters.
func NewSplitter(c *Chunker, onChunk func(ch Chunk, data []byte)) *Splitter {
	return &Splitter{c: c, OnChunk: onChunk}
}

// Write feeds p into the stream and emits any chunks that complete.
func (s *Splitter) Write(p []byte) {
	c := s.c
	c.bh.Reset()
	// Re-hash the tail of the open chunk so the rolling window state
	// matches a continuous scan across Write boundaries.
	tail := s.buf
	if len(tail) > WindowSize {
		tail = tail[len(tail)-WindowSize:]
	}
	for _, b := range tail {
		c.bh.Roll(b)
	}

	for _, b := range p {
		h := c.bh.Roll(b)
		s.buf = append(s.buf, b)
		n := len(s.buf)
		if n >= c.max || (n >= c.min && h&c.mask == 0) {
			s.emit()
			c.bh.Reset()
		}
	}
}

// Close flushes the final partial chunk, if any.
func (s *Splitter) Close() {
	if len(s.buf) > 0 {
		s.emit()
	}
}

func (s *Splitter) emit() {
	data := s.buf
	if s.OnChunk != nil {
		s.OnChunk(makeChunk(data, s.offset), data)
	}
	s.offset += len(data)
	s.buf = nil
}
