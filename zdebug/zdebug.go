// Package zdebug provides memory-bounded, paged access to compressed debug
// sections used by a symbol service host.
//
// A debug section is described by a directory entry that names the compression
// method, the physical byte range of the compressed stream and a cap on the
// number of decompressed bytes. All three values are treated as untrusted
// input: every entry is validated before it is exposed and a stream is only
// published as cached pages once the entire stream has been decompressed,
// bounds-checked and verified. A failure at any point discards every staged
// page, so partially decoded bytes are never returned as verified content.
//
// Cached pages are demand-paged and share a single global byte budget across
// all sections. Pages that do not fit are evicted least-recently-used and the
// section is transparently re-materialized on the next request.
package zdebug

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"sort"
	"sync"
)

// Method identifies how a section's physical bytes are compressed.
type Method uint32

const (
	// MethodNone stores the section verbatim.
	MethodNone Method = 0
	// MethodZlib uses RFC 1950 zlib framing.
	MethodZlib Method = 1
	// MethodZstd uses RFC 8478 zstd framing; a decoder factory must be
	// supplied because the standard library has no zstd reader.
	MethodZstd Method = 2
)

func (m Method) String() string {
	switch m {
	case MethodNone:
		return "none"
	case MethodZlib:
		return "zlib"
	case MethodZstd:
		return "zstd"
	default:
		return fmt.Sprintf("method(%d)", uint32(m))
	}
}

// Section describes one compressed debug section. Every field is untrusted
// and is validated by NewHostConfig before the section becomes usable.
type Section struct {
	// Name identifies the section, for example ".zdebug_info".
	Name string
	// Method selects the Decoder used for the physical bytes.
	Method Method
	// PhysicalOffset is the offset, in bytes, into the backing data where
	// the compressed stream starts.
	PhysicalOffset int64
	// PhysicalLength is the exact number of compressed bytes that make up
	// the stream. Physical ranges may overlap.
	PhysicalLength int64
	// MaxDecompressed bounds the number of bytes the stream is allowed to
	// produce. A stream that emits more is rejected.
	MaxDecompressed int64
}

// Config configures a Host. A zero Config uses a default page size, an
// unlimited global budget and the built-in none/zlib decoder factories.
type Config struct {
	// PageSize is the granularity, in bytes, at which decompressed content
	// is cached. Defaults to DefaultPageSize.
	PageSize int
	// BudgetBytes is the shared global cache budget. Zero means unlimited.
	BudgetBytes int64
	// Factories supplies decoders keyed by Method. It is merged onto the
	// built-in factories; entries here take precedence.
	Factories map[Method]DecoderFactory
}

// Decoder is a streaming decompressor. Read must return io.EOF only after the
// complete framed stream has been consumed and its internal checksum
// verified; a corrupted or truncated stream must return a non-nil error
// instead. Implementations should stop consuming input at the end of their
// frame so that trailing bytes remain visible to the host.
type Decoder interface {
	io.Reader
	io.Closer
}

// DecoderFactory builds a Decoder over one compressed stream. The returned
// reader only covers the section's physical range.
type DecoderFactory func(r io.Reader) (Decoder, error)

// DefaultPageSize is used when Config.PageSize is zero.
const DefaultPageSize = 64 * 1024

// Sentinel errors. Callers can use errors.Is to classify failures.
var (
	// ErrClosed is returned after Host.Close.
	ErrClosed = errors.New("zdebug: host is closed")
	// ErrUnknownSection is returned when a section name is not in the
	// directory.
	ErrUnknownSection = errors.New("zdebug: unknown section")
	// ErrBudgetExhausted indicates a page cannot fit in the global budget
	// even after every other page has been evicted.
	ErrBudgetExhausted = errors.New("zdebug: shared budget exhausted")
	// ErrCapExceeded indicates a stream produced more bytes than its
	// declared MaxDecompressed.
	ErrCapExceeded = errors.New("zdebug: decompressed size exceeds declared cap")
	// ErrInvalidSection indicates a malformed directory entry.
	ErrInvalidSection = errors.New("zdebug: invalid section directory entry")
)

// Host serves validated, paged and budgeted section content. A Host is safe
// for concurrent use by many consumers.
type Host struct {
	data     io.ReaderAt
	dataSize int64
	pageSize int

	factories map[Method]DecoderFactory

	mu      sync.Mutex
	budget  *budget
	closed  bool
	entries map[string]*sectionState
}

type sectionState struct {
	section Section

	// runs counts full-stream decoding attempts, including retries and
	// re-materializations after eviction.
	runs int64

	// loadGates maps a page index to the gate of its in-flight decode.
	// The gate closes when the run finishes; every waiter then rechecks
	// the cache under Host.mu and retries if the page is still missing.
	// The map is accessed only under Host.mu.
	loadGates map[int]*pageLoad
	// decodeMu serializes decode-and-commit within one section so
	// concurrent page requests cannot evict each other's fresh pages.
	decodeMu sync.Mutex

	size int64
	// knownSize becomes true once a tail page decode observed EOF.
	knownSize bool
	// eofPages records page indices whose decode observed stream EOF.
	eofPages map[int]bool
}

type pageLoad struct {
	done chan struct{}
	err  error
}

// NewHost validates the section directory against dataSize and returns a Host
// using the default configuration.
func NewHost(data io.ReaderAt, dataSize int64, sections []Section) (*Host, error) {
	return NewHostConfig(data, dataSize, sections, Config{})
}

// NewHostConfig validates the section directory and returns a Host with the
// supplied configuration.
func NewHostConfig(data io.ReaderAt, dataSize int64, sections []Section, cfg Config) (*Host, error) {
	if data == nil {
		return nil, errors.New("zdebug: backing data reader is nil")
	}
	if dataSize < 0 {
		return nil, errors.New("zdebug: negative backing data size")
	}

	pageSize := cfg.PageSize
	if pageSize == 0 {
		pageSize = DefaultPageSize
	}
	if pageSize < 0 {
		return nil, fmt.Errorf("zdebug: negative page size %d", pageSize)
	}
	if cfg.BudgetBytes < 0 {
		return nil, fmt.Errorf("zdebug: negative budget %d", cfg.BudgetBytes)
	}

	factories := defaultFactories()
	for method, factory := range cfg.Factories {
		if factory == nil {
			return nil, fmt.Errorf("%w: nil decoder factory for %s", ErrInvalidSection, method)
		}
		factories[method] = factory
	}

	host := &Host{
		data:      data,
		dataSize:  dataSize,
		pageSize:  pageSize,
		factories: factories,
		budget:    newBudget(cfg.BudgetBytes),
		entries:   make(map[string]*sectionState, len(sections)),
	}

	names := make(map[string]struct{}, len(sections))
	for index, section := range sections {
		if err := validateSection(section, dataSize, factories, names); err != nil {
			return nil, fmt.Errorf("section[%d] %q: %w", index, section.Name, err)
		}
		names[section.Name] = struct{}{}
		host.entries[section.Name] = &sectionState{section: section}
	}
	return host, nil
}

func validateSection(section Section, dataSize int64, factories map[Method]DecoderFactory, names map[string]struct{}) error {
	if section.Name == "" {
		return fmt.Errorf("%w: empty name", ErrInvalidSection)
	}
	if _, exists := names[section.Name]; exists {
		return fmt.Errorf("%w: duplicate name %q", ErrInvalidSection, section.Name)
	}
	if section.PhysicalOffset < 0 || section.PhysicalLength < 0 || section.MaxDecompressed < 0 {
		return fmt.Errorf("%w: negative field", ErrInvalidSection)
	}
	if section.PhysicalOffset > dataSize || section.PhysicalLength > dataSize-section.PhysicalOffset {
		return fmt.Errorf("%w: physical range [%d,+%d) outside data of %d bytes",
			ErrInvalidSection, section.PhysicalOffset, section.PhysicalLength, dataSize)
	}
	if _, ok := factories[section.Method]; !ok {
		return fmt.Errorf("%w: no decoder factory for %s", ErrInvalidSection, section.Method)
	}
	return nil
}

// Sections returns the validated directory in sorted name order.
func (h *Host) Sections() []Section {
	h.mu.Lock()
	defer h.mu.Unlock()

	out := make([]Section, 0, len(h.entries))
	for _, state := range h.entries {
		out = append(out, state.section)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// BudgetUsed reports the number of bytes currently held by cached pages.
func (h *Host) BudgetUsed() int64 {
	h.budget.mu.Lock()
	defer h.budget.mu.Unlock()
	return h.budget.used
}

// Close releases all cached pages. Subsequent reads fail with ErrClosed.
func (h *Host) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return ErrClosed
	}
	h.closed = true
	h.entries = nil
	h.mu.Unlock()

	// purgeAll drops every cached page across all sections in one step;
	// per-section removal would only repeat the same work.
	h.budget.purgeAll()
	return nil
}

// Size returns the verified decompressed size of a section, materializing it
// if necessary.
func (h *Host) Size(name string) (int64, error) {
	state, err := h.lookup(name)
	if err != nil {
		return 0, err
	}
	if err := h.discoverSize(state); err != nil {
		return 0, err
	}
	h.mu.Lock()
	size := state.size
	h.mu.Unlock()
	return size, nil
}

// Open returns an io.SectionReader over the verified decompressed content of
// a section. The reader materializes and caches pages on demand and shares the
// host's global budget.
func (h *Host) Open(name string) (*io.SectionReader, error) {
	state, err := h.lookup(name)
	if err != nil {
		return nil, err
	}
	return io.NewSectionReader(sectionAt{host: h, state: state}, 0, state.section.MaxDecompressed), nil
}

// ReadAt reads verified decompressed bytes from a section at off. It satisfies
// io.ReaderAt: it always fills p unless fewer bytes remain or an error occurs.
func (h *Host) ReadAt(name string, p []byte, off int64) (int, error) {
	state, err := h.lookup(name)
	if err != nil {
		return 0, err
	}
	reader := io.NewSectionReader(sectionAt{host: h, state: state}, 0, state.section.MaxDecompressed)
	return reader.ReadAt(p, off)
}

type sectionAt struct {
	host  *Host
	state *sectionState
}

func (s sectionAt) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("zdebug: negative offset %d", off)
	}
	if len(p) == 0 {
		return 0, nil
	}

	host := s.host
	state := s.state
	pageSize := int64(host.pageSize)
	total := 0

	for len(p) > 0 {
		pageIndex := int(off / pageSize)
		within := off % pageSize

		page, atEOF, err := host.requirePage(state, pageIndex)
		if err != nil {
			return total, err
		}

		available := page[within:]
		if atEOF {
			if limit := host.verifiedSize(state) - off; int64(len(available)) > limit {
				available = available[:limit]
			}
		}
		n := copy(p, available)
		p = p[n:]
		off += int64(n)
		total += n
		if atEOF && off >= host.verifiedSize(state) {
			if len(p) > 0 {
				return total, io.EOF
			}
		}
	}
	return total, nil
}

// requirePage returns one verified page, decoding and committing it on
// demand. Concurrent requests for the same page share a single decode.
func (h *Host) requirePage(state *sectionState, pageIndex int) ([]byte, bool, error) {
	if page, ok := h.budget.getPage(state, pageIndex); ok {
		return page, h.isEOFPage(state, pageIndex), nil
	}

	// Under cross-section contention a freshly committed page can be
	// evicted before this goroutine reads it. A single page always fits
	// the configured budget, so retry with scheduler yield; this bounds
	// work without rejecting a request that can ultimately be served.
	const maxAttempts = 64
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := h.loadPage(state, pageIndex); err != nil {
			return nil, false, err
		}
		if page, ok := h.budget.getPage(state, pageIndex); ok {
			return page, h.isEOFPage(state, pageIndex), nil
		}
		runtime.Gosched()
	}
	return nil, false, fmt.Errorf("%w: section %q page %d evicted repeatedly under contention",
		ErrBudgetExhausted, state.section.Name, pageIndex)
}

func (h *Host) isEOFPage(state *sectionState, pageIndex int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return state.eofPages[pageIndex]
}

// verifiedSize reports the true decompressed size once an EOF page has been
// decoded, otherwise the declared cap as an upper bound.
func (h *Host) verifiedSize(state *sectionState) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if state.knownSize {
		return state.size
	}
	return state.section.MaxDecompressed
}

// lookup returns the state for a directory entry without decoding anything.
func (h *Host) lookup(name string) (*sectionState, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, ErrClosed
	}
	state, ok := h.entries[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownSection, name)
	}
	return state, nil
}

// loadPage decodes and caches one page on demand, joining an in-flight decode
// for the same page instead of starting a competing one. It also drives
// re-materialization after eviction: a missing page simply starts a new
// verified decode. Coordination uses Host.mu; decoding runs without it.
func (h *Host) loadPage(state *sectionState, target int) error {
	for {
		if _, ok := h.budget.getPage(state, target); ok {
			return nil
		}

		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			return ErrClosed
		}
		if load, ok := state.loadGates[target]; ok {
			done := load.done
			h.mu.Unlock()
			<-done
			h.mu.Lock()
			err := load.err
			h.mu.Unlock()
			if err != nil {
				continue
			}
			// Re-check under the loop; the leader guarantees the page was
			// resident at completion, though cross-section eviction may
			// have since removed it, in which case we decode again.
			continue
		}

		load := &pageLoad{done: make(chan struct{})}
		if state.loadGates == nil {
			state.loadGates = make(map[int]*pageLoad)
		}
		state.loadGates[target] = load
		h.mu.Unlock()

		// The section lock serializes decode-and-commit within a section.
		// It is held until the gate is published so waiters waking after
		// close observe a coherent cache state.
		state.decodeMu.Lock()

		// Re-check after acquiring the lock: an earlier leader for the
		// same target may have finished while we were queued.
		var err error
		_, resident := h.budget.getPage(state, target)
		if !resident {
			err = h.decodeAndCommit(state, target)
		}

		h.mu.Lock()
		load.err = err
		delete(state.loadGates, target)
		close(load.done)
		h.mu.Unlock()
		state.decodeMu.Unlock()

		if err != nil {
			return err
		}
		// Loop to confirm residency; cross-section eviction is the only
		// reason to repeat, and progress is guaranteed because one page
		// always fits within the configured budget.
	}
}

// decodeAndCommit verifies one page and publishes it only on full success.
func (h *Host) decodeAndCommit(state *sectionState, target int) error {
	// Stage the page while decoding. It is committed to the shared cache
	// only after the full decode for this page succeeds, so a truncated or
	// corrupt stream can never leave bytes behind.
	page, _, err := h.materializePage(state, target)
	if err != nil {
		// A later page failing means earlier pages from the same stream
		// are no longer trustworthy: drop every cached page of this
		// section so partial-stream bytes are never served as verified.
		h.budget.removeSection(state)
		h.invalidateSize(state)
		return err
	}
	return h.budget.commitPage(state, target, page)
}

func (h *Host) invalidateSize(state *sectionState) {
	h.mu.Lock()
	state.knownSize = false
	state.size = 0
	state.eofPages = nil
	h.mu.Unlock()
}

// discoverSize decodes pages from zero until a page observes the natural
// stream EOF, then records the verified decompressed size. Pages remain
// subject to the shared budget and may be evicted as the walk progresses.
func (h *Host) discoverSize(state *sectionState) error {
	h.mu.Lock()
	known := state.knownSize
	h.mu.Unlock()
	if known {
		return nil
	}

	for pageIndex := 0; ; pageIndex++ {
		page, atEOF, err := h.requirePage(state, pageIndex)
		if err != nil {
			return err
		}
		if atEOF {
			size := int64(pageIndex)*int64(h.pageSize) + int64(len(page))
			h.mu.Lock()
			state.size = size
			state.knownSize = true
			h.mu.Unlock()
			return nil
		}
	}
}
