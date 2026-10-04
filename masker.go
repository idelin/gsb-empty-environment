package desensitize

import "errors"

// ErrBackpressure is returned by Write when the undecided pending buffer
// exceeds the limit set with WithMaxPending. The caller should stop writing
// and call Flush (a hard boundary) to force the pending bytes out.
var ErrBackpressure = errors.New("desensitize: pending buffer limit exceeded")

// LiteralEntryID is the EntryID used in Mapping for unmasked literal spans.
const LiteralEntryID int64 = -1

// Mapping links a span of committed output back to its source span.
// Offsets are absolute byte positions in the output and input streams, so
// mappings from successive Drain calls compose. Masked spans carry the
// winning entry ID; literal spans carry LiteralEntryID.
type Mapping struct {
	OutStart int
	OutEnd   int
	SrcStart int
	SrcEnd   int
	EntryID  int64
}

// Masker is a streaming masker bound to a dictionary version. It is not
// safe for concurrent use.
type Masker struct {
	ver        *Version
	maxPending int // 0 means unlimited

	buf     []byte // source bytes not yet committable
	srcBase int    // absolute source offset of buf[0]

	out     []byte // committed output not yet drained
	outBase int    // absolute output offset of out[0]
	maps    []Mapping
}

// Option configures a Masker.
type Option func(*Masker)

// WithMaxPending caps the number of uncommitted source bytes the Masker
// will buffer. Once the cap is exceeded, Write reports ErrBackpressure.
// A zero or negative value means no limit.
func WithMaxPending(n int) Option {
	return func(m *Masker) { m.maxPending = n }
}

// NewMasker returns a Masker that applies ver to subsequently written bytes.
func NewMasker(ver *Version, opts ...Option) *Masker {
	m := &Masker{ver: ver}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Write consumes p and commits every prefix that can no longer be extended
// by future input. It returns ErrBackpressure (with n == len(p), the bytes
// were accepted) when the pending buffer exceeds the configured limit, and
// refuses new input (n == 0) while the limit is still exceeded.
func (m *Masker) Write(p []byte) (int, error) {
	if m.maxPending > 0 && len(m.buf) >= m.maxPending {
		return 0, ErrBackpressure
	}
	m.buf = append(m.buf, p...)
	m.advance(false)
	if m.maxPending > 0 && len(m.buf) > m.maxPending {
		return len(p), ErrBackpressure
	}
	return len(p), nil
}

// SwitchVersion finalizes all pending bytes under the current version and
// applies next to every byte written afterwards. The switch is a hard byte
// boundary: no match may span it.
func (m *Masker) SwitchVersion(next *Version) {
	m.advance(true)
	m.ver = next
}

// Flush forces all pending bytes to be committed under the current version,
// treating the buffered input as complete. It is a hard boundary: a match
// cannot span a flush.
func (m *Masker) Flush() {
	m.advance(true)
}

// Pending reports how many source bytes are still waiting for a decision.
func (m *Masker) Pending() int {
	return len(m.buf)
}

// Drain returns the committed output and its source mappings, resetting the
// output buffer. Mapping offsets stay absolute across drains.
func (m *Masker) Drain() ([]byte, []Mapping) {
	out, maps := m.out, m.maps
	m.outBase += len(m.out)
	m.out, m.maps = nil, nil
	return out, maps
}

// advance commits decided prefixes of the pending buffer.
func (m *Masker) advance(flush bool) {
	for len(m.buf) > 0 {
		d := m.ver.decide(m.buf, flush)
		if d.pending {
			return
		}
		if d.matchLen > 0 {
			m.emitMask(d.entryID, d.repl, d.matchLen)
		} else {
			m.emitLiteral()
		}
	}
}

func (m *Masker) emitMask(entryID int64, repl string, srcLen int) {
	outStart := m.outBase + len(m.out)
	m.out = append(m.out, repl...)
	m.maps = append(m.maps, Mapping{
		OutStart: outStart,
		OutEnd:   outStart + len(repl),
		SrcStart: m.srcBase,
		SrcEnd:   m.srcBase + srcLen,
		EntryID:  entryID,
	})
	m.consume(srcLen)
}

func (m *Masker) emitLiteral() {
	outStart := m.outBase + len(m.out)
	m.out = append(m.out, m.buf[0])
	if n := len(m.maps); n > 0 {
		last := &m.maps[n-1]
		if last.EntryID == LiteralEntryID && last.OutEnd == outStart && last.SrcEnd == m.srcBase {
			last.OutEnd++
			last.SrcEnd++
			m.consume(1)
			return
		}
	}
	m.maps = append(m.maps, Mapping{
		OutStart: outStart,
		OutEnd:   outStart + 1,
		SrcStart: m.srcBase,
		SrcEnd:   m.srcBase + 1,
		EntryID:  LiteralEntryID,
	})
	m.consume(1)
}

func (m *Masker) consume(n int) {
	m.buf = m.buf[n:]
	m.srcBase += n
	if len(m.buf) == 0 {
		m.buf = nil
	}
}

// Mask is a one-shot helper masking src under ver.
func Mask(ver *Version, src []byte) ([]byte, []Mapping, error) {
	m := NewMasker(ver)
	if _, err := m.Write(src); err != nil {
		return nil, nil, err
	}
	m.Flush()
	out, maps := m.Drain()
	return out, maps, nil
}
