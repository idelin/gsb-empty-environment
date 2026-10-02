package cdc

import "crypto/sha256"

// Default chunking parameters: 4 KiB minimum, 8 KiB average
// (boundary probability 1/8192 per byte), 64 KiB maximum.
const (
	DefaultMinSize = 4 * 1024
	DefaultAvgSize = 8 * 1024
	DefaultMaxSize = 64 * 1024

	// tableSeed derives the buzhash substitution table. It is part of
	// the chunking format: two chunkers only agree on boundaries when
	// they share the seed and the size parameters.
	tableSeed = "cdc/buzhash/v1"
)

// Chunk is one content-defined block of the input stream.
type Chunk struct {
	Offset int      // byte offset of the chunk within the stream
	Length int      // chunk size in bytes
	Hash   [32]byte // SHA-256 of the chunk content
}

// Chunker splits a byte stream at content-defined boundaries using a
// buzhash rolling hash. A boundary is declared at byte i when the
// rolling hash of the window ending at i has its low bits all zero,
// subject to min/max size constraints. Boundaries depend only on the
// local byte window, so edits re-chunk only their neighbourhood.
type Chunker struct {
	minSize int
	maxSize int
	mask    uint32
	b       *buzhash
}

// NewChunker returns a Chunker with the given size limits. avgSize
// must be a power of two; it sets the boundary probability to
// 1/avgSize per byte once minSize bytes have accumulated.
func NewChunker(minSize, avgSize, maxSize int) *Chunker {
	if minSize <= 0 || avgSize <= 0 || maxSize <= 0 {
		panic("cdc: sizes must be positive")
	}
	if avgSize&(avgSize-1) != 0 {
		panic("cdc: avgSize must be a power of two")
	}
	if minSize > maxSize {
		panic("cdc: minSize exceeds maxSize")
	}
	return &Chunker{
		minSize: minSize,
		maxSize: maxSize,
		mask:    uint32(avgSize - 1),
		b:       newBuzhash(tableSeed),
	}
}

// DefaultChunker returns a Chunker with the default parameters.
func DefaultChunker() *Chunker {
	return NewChunker(DefaultMinSize, DefaultAvgSize, DefaultMaxSize)
}

// Split chunks data and returns the chunks with their content hashes.
// The chunker is safe to reuse across calls; state is reset per call.
func (c *Chunker) Split(data []byte) []Chunk {
	var chunks []Chunk
	c.b.reset()
	start := 0
	for i := 0; i < len(data); i++ {
		h := c.b.roll(data[i])
		n := i - start + 1
		if n >= c.minSize && (h&c.mask == 0 || n >= c.maxSize) {
			chunks = append(chunks, c.emit(data, start, i+1))
			start = i + 1
			c.b.reset()
		}
	}
	if start < len(data) {
		chunks = append(chunks, c.emit(data, start, len(data)))
	}
	return chunks
}

func (c *Chunker) emit(data []byte, start, end int) Chunk {
	return Chunk{
		Offset: start,
		Length: end - start,
		Hash:   sha256.Sum256(data[start:end]),
	}
}

// Hashes returns just the content hashes of the chunks, in order.
func Hashes(chunks []Chunk) [][32]byte {
	out := make([][32]byte, len(chunks))
	for i, ch := range chunks {
		out[i] = ch.Hash
	}
	return out
}
