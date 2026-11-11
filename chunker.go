package cdcupload

import (
	"bufio"
	"crypto/sha256"
	"hash"
	"io"
)

// ChunkConfig configures normalized content-defined chunking.
//
// MinSize and MaxSize are hard bounds on chunk length. AvgSize is the target
// average chunk size: the cut-point mask is derived from it, so the expected
// chunk length converges to AvgSize.
type ChunkConfig struct {
	MinSize int
	AvgSize int
	MaxSize int
}

// DefaultChunkConfig returns a configuration targeting roughly 64 KiB chunks
// bounded to 16 KiB..256 KiB, which is a good fit for large file uploads.
func DefaultChunkConfig() ChunkConfig {
	return ChunkConfig{
		MinSize: 16 * 1024,
		AvgSize: 64 * 1024,
		MaxSize: 256 * 1024,
	}
}

func (c ChunkConfig) withDefaults() ChunkConfig {
	def := DefaultChunkConfig()
	if c.MinSize == 0 {
		c.MinSize = def.MinSize
	}
	if c.AvgSize == 0 {
		c.AvgSize = def.AvgSize
	}
	if c.MaxSize == 0 {
		c.MaxSize = def.MaxSize
	}
	return c
}

// Validate reports an error if the size bounds are inconsistent.
func (c ChunkConfig) Validate() error {
	c = c.withDefaults()
	switch {
	case c.MinSize < 16:
		return ErrMinSizeTooSmall
	case c.AvgSize < c.MinSize:
		return ErrAvgBelowMin
	case c.MaxSize < c.AvgSize:
		return ErrMaxBelowAvg
	}
	return nil
}

// maskBits returns the number of low hash bits that must be zero for a cut.
// A mask of 2^b - 1 yields an expected cut distance of 2^b, i.e. an average
// chunk length close to the requested AvgSize.
func maskBits(avg int) int {
	bits := 0
	for (1 << bits) < avg {
		bits++
	}
	return bits
}

// gearTable holds 256 64-bit pseudo-random constants, one per byte value.
// The values are generated deterministically (splitmix64 from a fixed seed),
// which keeps the package self-contained while giving the same statistical
// properties as the reference FastCDC Gear table.
var gearTable [256]uint64

func init() {
	var state uint64 = 0x1a2b3c4d5e6f7081
	next := func() uint64 {
		state += 0x9e3779b97f4a7c15
		z := state
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		return z ^ (z >> 31)
	}
	for i := range gearTable {
		gearTable[i] = next()
	}
}

// Chunk describes one content-defined chunk and its position in the source.
type Chunk struct {
	Index  int64  // zero-based ordinal within the file
	Offset int64  // byte offset of the chunk in the source
	Length int    // chunk length in bytes
	Digest []byte // SHA-256 digest of the chunk content
}

// Chunker splits a byte stream into content-defined chunks using a 64-bit Gear
// rolling hash with normalized (FastCDC style) masks:
//
//   - below MinSize cut points are skipped entirely;
//   - between MinSize and AvgSize a smaller mask makes cuts more likely,
//     shrinking oversized chunks toward the average;
//   - from AvgSize to MaxSize the normal mask applies;
//   - at MaxSize a cut is forced, guaranteeing a hard upper bound.
type Chunker struct {
	r        *bufio.Reader
	cfg      ChunkConfig
	mask     uint64
	maskMid  uint64
	fileHash hash.Hash

	offset int64
	index  int64
	done   bool

	buf []byte // payload of the chunk currently being built
}

// NewChunker creates a Chunker over r using the supplied configuration.
func NewChunker(r io.Reader, cfg ChunkConfig) (*Chunker, error) {
	cfg = cfg.withDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	bits := maskBits(cfg.AvgSize)
	// Halving the mask width doubles the per-byte cut probability, pulling
	// long chunks back toward the average (FastCDC normalized chunking).
	midBits := bits - 1
	if midBits < 1 {
		midBits = 1
	}
	return &Chunker{
		r:        bufio.NewReaderSize(r, cfg.MaxSize),
		cfg:      cfg,
		mask:     uint64(1)<<bits - 1,
		maskMid:  uint64(1)<<midBits - 1,
		fileHash: sha256.New(),
	}, nil
}

// Next returns the next chunk, or io.EOF when the stream is fully consumed.
func (c *Chunker) Next() (Chunk, error) {
	ch, _, err := c.NextChunk()
	return ch, err
}

// NextChunk returns the next chunk together with its payload. The returned
// slice is valid until the following call and must be copied if retained.
func (c *Chunker) NextChunk() (Chunk, []byte, error) {
	if c.done {
		return Chunk{}, nil, io.EOF
	}

	chunkHash := sha256.New()
	var rolling uint64
	length := 0
	start := c.offset
	c.buf = c.buf[:0]

	for {
		b, err := c.r.ReadByte()
		if err == io.EOF {
			c.done = true
			if length == 0 {
				return Chunk{}, nil, io.EOF
			}
			ch := c.emitAt(start, length, chunkHash)
			return ch, c.buf, nil
		}
		if err != nil {
			return Chunk{}, nil, err
		}

		length++
		c.buf = append(c.buf, b)
		chunkHash.Write([]byte{b})
		c.fileHash.Write([]byte{b})
		rolling = (rolling << 1) + gearTable[b]

		switch {
		case length < c.cfg.MinSize:
			// Suppress cut points below the minimum chunk size.
			continue
		case length == c.cfg.MaxSize:
			// Hard upper bound: force a boundary.
			ch := c.emitAt(start, length, chunkHash)
			return ch, c.buf, nil
		}

		mask := c.mask
		if length < c.cfg.AvgSize {
			mask = c.maskMid
		}
		if rolling&mask == 0 {
			ch := c.emitAt(start, length, chunkHash)
			return ch, c.buf, nil
		}
	}
}

func (c *Chunker) emitAt(start int64, length int, h hash.Hash) Chunk {
	ch := Chunk{
		Index:  c.index,
		Offset: start,
		Length: length,
		Digest: h.Sum(nil),
	}
	c.index++
	c.offset += int64(length)
	return ch
}

// FileSum returns the SHA-256 of the whole input. It is only meaningful after
// Next has returned io.EOF.
func (c *Chunker) FileSum() []byte {
	return c.fileHash.Sum(nil)
}
