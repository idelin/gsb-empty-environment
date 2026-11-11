// Package unwind provides on-demand sparse-memory stack unwinding for a
// crash-reporting SDK. Memory is fetched lazily through an io.ReaderAt,
// concurrent requests for the same page are coalesced, and cancelling one
// consumer never affects others sharing the same underlying read.
package unwind

import (
	"context"
	"io"
	"sync"
)

// DefaultPageSize is used when NewMemory is given a non-positive page size.
const DefaultPageSize = 4096

// Gap describes an exact range of memory that could not be read.
type Gap struct {
	Addr uint64
	Len  uint64
}

// GapError is returned by Memory.Read when a backing page is unavailable.
// It carries the precise page-aligned range that failed.
type GapError struct {
	Gap Gap
	Err error
}

func (e *GapError) Error() string {
	return "unwind: unreadable memory at " + hex(e.Gap.Addr) + " (" + itoa(e.Gap.Len) + " bytes): " + e.Err.Error()
}

func (e *GapError) Unwrap() error { return e.Err }

// Memory is a sparse, page-cached view over an io.ReaderAt.
//
// Pages are fetched on demand. Concurrent readers asking for the same page
// share a single underlying ReadAt call; each waiter blocks on its own
// context, so cancelling one consumer never cancels the shared fetch or any
// other consumer. Successfully fetched pages are cached; failures are not
// cached and may be retried by later reads.
type Memory struct {
	src      io.ReaderAt
	pageSize int64

	mu       sync.Mutex
	cache    map[int64][]byte
	inflight map[int64]*pageFetch
}

type pageFetch struct {
	done chan struct{}
	data []byte
	err  error
}

// NewMemory wraps src with on-demand paging. A non-positive pageSize selects
// DefaultPageSize.
func NewMemory(src io.ReaderAt, pageSize int64) *Memory {
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	return &Memory{
		src:      src,
		pageSize: pageSize,
		cache:    make(map[int64][]byte),
		inflight: make(map[int64]*pageFetch),
	}
}

// PageSize returns the paging granularity in bytes.
func (m *Memory) PageSize() int64 { return m.pageSize }

// page returns the contents of the page with the given index, coalescing
// concurrent fetches. The fetch runs in the background and is never
// abandoned once started: a caller whose context is cancelled simply stops
// waiting, while the in-flight read still completes and populates the
// shared cache for the remaining consumers.
func (m *Memory) page(ctx context.Context, idx int64) ([]byte, error) {
	m.mu.Lock()
	if data, ok := m.cache[idx]; ok {
		m.mu.Unlock()
		return data, nil
	}
	f, ok := m.inflight[idx]
	if !ok {
		f = &pageFetch{done: make(chan struct{})}
		m.inflight[idx] = f
		go m.fetch(idx, f)
	}
	m.mu.Unlock()

	select {
	case <-f.done:
		return f.data, f.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// fetch performs the underlying read for one page and publishes the result
// to every waiter on f.done.
func (m *Memory) fetch(idx int64, f *pageFetch) {
	data := make([]byte, m.pageSize)
	n, err := m.src.ReadAt(data, idx*m.pageSize)
	if err == nil && n < len(data) {
		err = io.ErrUnexpectedEOF
	}

	m.mu.Lock()
	if err == nil {
		m.cache[idx] = data
		f.data = data
	} else {
		f.err = err
	}
	delete(m.inflight, idx)
	m.mu.Unlock()
	close(f.done)
}

// Read fills a fresh buffer with n bytes starting at addr, fetching pages on
// demand. If any required page is unavailable it returns a *GapError naming
// the exact page-aligned range that is missing.
func (m *Memory) Read(ctx context.Context, addr uint64, n int) ([]byte, error) {
	out := make([]byte, n)
	copied := 0
	for copied < n {
		cur := addr + uint64(copied)
		idx := int64(cur / uint64(m.pageSize))
		off := int(cur % uint64(m.pageSize))
		data, err := m.page(ctx, idx)
		if err != nil {
			if _, isGap := err.(*GapError); !isGap {
				err = &GapError{
					Gap: Gap{Addr: uint64(idx) * uint64(m.pageSize), Len: uint64(m.pageSize)},
					Err: err,
				}
			}
			return nil, err
		}
		copied += copy(out[copied:], data[off:])
	}
	return out, nil
}

// Uint64 reads one little-endian 8-byte word at addr.
func (m *Memory) Uint64(ctx context.Context, addr uint64) (uint64, error) {
	b, err := m.Read(ctx, addr, 8)
	if err != nil {
		return 0, err
	}
	return le64(b), nil
}

func le64(b []byte) uint64 {
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
}

func putle64(b []byte, v uint64) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
	b[4] = byte(v >> 32)
	b[5] = byte(v >> 40)
	b[6] = byte(v >> 48)
	b[7] = byte(v >> 56)
}

func hex(v uint64) string {
	const digits = "0123456789abcdef"
	if v == 0 {
		return "0x0"
	}
	var buf [16]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = digits[v&0xf]
		v >>= 4
	}
	return "0x" + string(buf[i:])
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
