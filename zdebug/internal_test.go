package zdebug

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"sync"
	"sync/atomic"
)

// MethodFrame is a test-only compression method backed by frameCodec.
const MethodFrame Method = 99

// frameCodec is a deterministic framed codec used to exercise every failure
// mode without depending on a third-party zstd package. Wire layout per
// frame:
//
//	uint32 payload length (big endian)
//	uint32 crc32(ieee) of the payload
//	...    payload
//
// A zero-length frame is the end marker. The decoder verifies each checksum,
// rejects truncated input, and rejects any bytes after the end marker.
type frameDecoder struct {
	src      io.Reader
	payload  []byte
	finished bool
}

func (d *frameDecoder) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if d.finished {
		return 0, io.EOF
	}

	for len(d.payload) == 0 {
		var header [8]byte
		if _, err := io.ReadFull(d.src, header[:]); err != nil {
			return 0, err
		}
		length := binary.BigEndian.Uint32(header[:4])
		checksum := binary.BigEndian.Uint32(header[4:])
		if length == 0 {
			d.finished = true
			// Any byte past the end marker is trailing junk.
			var one [1]byte
			if n, err := d.src.Read(one[:]); n != 0 {
				return 0, errors.New("frameCodec: trailing data after end marker")
			} else if err != nil && err != io.EOF {
				return 0, err
			}
			return 0, io.EOF
		}

		payload := make([]byte, length)
		if _, err := io.ReadFull(d.src, payload); err != nil {
			return 0, err
		}
		if crc32.ChecksumIEEE(payload) != checksum {
			return 0, errors.New("frameCodec: checksum mismatch")
		}
		d.payload = payload
	}

	n := copy(p, d.payload)
	d.payload = d.payload[n:]
	return n, nil
}

func (d *frameDecoder) Close() error { return nil }

func frameFactory(r io.Reader) (Decoder, error) {
	return &frameDecoder{src: r}, nil
}

// EncodeFrames builds frameCodec wire data from payload chunks.
func EncodeFrames(chunks ...[]byte) []byte {
	var out []byte
	putFrame := func(payload []byte) {
		var header [8]byte
		binary.BigEndian.PutUint32(header[:4], uint32(len(payload)))
		binary.BigEndian.PutUint32(header[4:], crc32.ChecksumIEEE(payload))
		out = append(out, header[:]...)
		out = append(out, payload...)
	}
	for _, chunk := range chunks {
		putFrame(chunk)
	}
	putFrame(nil)
	return out
}

// CountingFactory wraps a factory and records decoder activity so tests can
// assert on stream sharing and physical reads.
type CountingFactory struct {
	base DecoderFactory

	NewCount    atomic.Int64
	ReadCalls   atomic.Int64
	ReadBytes   atomic.Int64
	SourceReads atomic.Int64
}

func NewCountingFactory(base DecoderFactory) *CountingFactory {
	return &CountingFactory{base: base}
}

func (f *CountingFactory) Factory(r io.Reader) (Decoder, error) {
	f.NewCount.Add(1)
	source, ok := r.(*countedSource)
	if !ok {
		return nil, errors.New("CountingFactory expects *countedSource")
	}
	inner, err := f.base(r)
	if err != nil {
		return nil, err
	}
	return &countingDecoder{factory: f, inner: inner, source: source}, nil
}

func (f *CountingFactory) Reset() {
	f.NewCount.Store(0)
	f.ReadCalls.Store(0)
	f.ReadBytes.Store(0)
	f.SourceReads.Store(0)
}

type countingDecoder struct {
	factory *CountingFactory
	inner   Decoder
	source  *countedSource
	once    sync.Once
}

func (d *countingDecoder) Read(p []byte) (int, error) {
	d.factory.ReadCalls.Add(1)
	n, err := d.inner.Read(p)
	d.factory.ReadBytes.Add(int64(n))
	d.factory.SourceReads.Store(d.source.consumed)
	return n, err
}

func (d *countingDecoder) Close() error {
	var err error
	d.once.Do(func() { err = d.inner.Close() })
	return err
}

// flakyReader returns half of every requested ReadAt without an error, then
// satisfies the rest on the retry. It models a backing source that performs
// legitimate short reads.
type flakyReader struct {
	data []byte
}

func (r *flakyReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(r.data)) {
		return 0, io.EOF
	}
	available := r.data[off:]
	n := len(p)
	if n > len(available) {
		n = len(available)
	}
	if n > 1 {
		n /= 2
	}
	copy(p, available[:n])
	return n, nil
}

// truncatingReader behaves like byte data but ends at limit, modeling a
// truncated physical range that still claims its declared length.
type truncatingReader struct {
	data  []byte
	limit int64
}

func (r *truncatingReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	if off >= r.limit {
		return 0, io.EOF
	}
	available := r.data[off:]
	if max := r.limit - off; int64(len(available)) > max {
		available = available[:max]
	}
	n := copy(p, available)
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func corruptAt(data []byte, index int) []byte {
	out := append([]byte(nil), data...)
	out[index] ^= 0xFF
	return out
}

func testConfig(factory *CountingFactory, pageSize int, budget int64) Config {
	cfg := Config{
		PageSize:    pageSize,
		BudgetBytes: budget,
		Factories: map[Method]DecoderFactory{
			MethodFrame: factory.Factory,
		},
	}
	return cfg
}

// cachedPageCount reports how many verified pages of state are currently
// held in the shared budget cache.
func cachedPageCount(host *Host, state *sectionState) int {
	host.budget.mu.Lock()
	defer host.budget.mu.Unlock()

	count := 0
	for key := range host.budget.index {
		if key.state == state {
			count++
		}
	}
	return count
}
