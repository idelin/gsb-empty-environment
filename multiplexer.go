package logrec

import (
	"fmt"
	"regexp"
	"sync"
)

// Multiplexer reassembles records from many independent log sources.
// Each (source, epoch) pair gets its own assembler state, so a source
// restarting with a new epoch never contaminates the previous stream.
//
// A single buffer budget caps the bytes held across all assemblers
// (partial lines plus open records). Feeds that would exceed the
// budget fail with ErrBackpressure and consume nothing.
type Multiplexer struct {
	mu         sync.Mutex
	assemblers map[streamKey]*assembler
	rules      Rules // default rules for new streams
	budget     int64 // total buffered-byte allowance; 0 = unlimited
	used       int64
	telemetry  *Telemetry
}

type streamKey struct {
	source string
	epoch  uint64
}

// Option configures a Multiplexer.
type Option func(*Multiplexer)

// WithBudget sets the total buffered-byte budget across all streams.
func WithBudget(bytes int64) Option {
	return func(m *Multiplexer) { m.budget = bytes }
}

// WithRules sets the default assembly rules for new streams.
func WithRules(r Rules) Option {
	return func(m *Multiplexer) { m.rules = r }
}

// WithTelemetry attaches an existing telemetry sink.
func WithTelemetry(t *Telemetry) Option {
	return func(m *Multiplexer) { m.telemetry = t }
}

// NewMultiplexer creates a multiplexer with DefaultRules and no budget
// limit unless overridden by options.
func NewMultiplexer(opts ...Option) *Multiplexer {
	m := &Multiplexer{
		assemblers: map[streamKey]*assembler{},
		rules:      DefaultRules(),
		telemetry:  NewTelemetry(),
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Telemetry returns the multiplexer's event sink.
func (m *Multiplexer) Telemetry() *Telemetry { return m.telemetry }

// Used returns the currently buffered bytes.
func (m *Multiplexer) Used() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.used
}

func (m *Multiplexer) reserve(n int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.budget > 0 && m.used+n > m.budget {
		return false
	}
	m.used += n
	return true
}

func (m *Multiplexer) release(n int64) {
	m.mu.Lock()
	m.used -= n
	if m.used < 0 {
		m.used = 0
	}
	m.mu.Unlock()
}

func (m *Multiplexer) assemblerFor(source string, epoch uint64) *assembler {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := streamKey{source, epoch}
	a, ok := m.assemblers[k]
	if !ok {
		a = newAssembler(m, source, epoch, m.rules)
		m.assemblers[k] = a
	}
	return a
}

// FeedChunk feeds a raw byte chunk of a source stream. Lines may be
// split across chunks arbitrarily. It returns the emissions produced
// and the number of bytes consumed; on ErrBackpressure the unconsumed
// remainder must be retried after draining.
func (m *Multiplexer) FeedChunk(source string, epoch uint64, data []byte) ([]Emission, int, error) {
	a := m.assemblerFor(source, epoch)
	n, out, err := a.feedChunk(data)
	return out, n, err
}

// FeedLine feeds one logical line with its original sequence number
// (1-based line number in the source stream). Lines may arrive out of
// order; gaps, duplicates and corrections are handled explicitly.
func (m *Multiplexer) FeedLine(source string, epoch, seq uint64, text string) ([]Emission, error) {
	return m.assemblerFor(source, epoch).feedLine(seq, text)
}

// SetRules hot-swaps the assembly rules of one stream. Lines already
// consumed keep their identity and placement; only lines classified
// afterwards use the new rules.
func (m *Multiplexer) SetRules(source string, epoch uint64, r Rules) {
	a := m.assemblerFor(source, epoch)
	a.rules = r
	a.emit(Event{Kind: EvRulesSwapped, Detail: patternOf(r)})
}

func patternOf(r Rules) string {
	if r.StartPattern == nil {
		return "<nil>"
	}
	return r.StartPattern.String()
}

// FlushPending force-closes the open record of a stream, freeing its
// buffer budget. The record is emitted with reason CloseFlush; late
// continuations can still revise it.
func (m *Multiplexer) FlushPending(source string, epoch uint64) []Emission {
	m.mu.Lock()
	a, ok := m.assemblers[streamKey{source, epoch}]
	m.mu.Unlock()
	if !ok {
		return nil
	}
	return a.flushPending()
}

// Close terminates a source stream: any buffered partial line is
// completed and a still-open record is emitted as an incomplete tail
// with reason CloseEOF.
func (m *Multiplexer) Close(source string, epoch uint64) []Emission {
	m.mu.Lock()
	a, ok := m.assemblers[streamKey{source, epoch}]
	m.mu.Unlock()
	if !ok {
		return nil
	}
	return a.close()
}

// Pending returns a snapshot of the currently open record of a
// stream, or nil.
func (m *Multiplexer) Pending(source string, epoch uint64) *Record {
	m.mu.Lock()
	a, ok := m.assemblers[streamKey{source, epoch}]
	m.mu.Unlock()
	if !ok || a.pending == nil {
		return nil
	}
	rec := *a.pending
	return &rec
}

// MustPattern is a helper to build a start-pattern regexp, panicking
// on invalid input. Intended for tests and fixtures.
func MustPattern(expr string) *regexp.Regexp {
	re, err := regexp.Compile(expr)
	if err != nil {
		panic(fmt.Sprintf("logrec: invalid pattern %q: %v", expr, err))
	}
	return re
}
