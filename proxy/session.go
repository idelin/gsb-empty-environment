// Package proxy provides an embeddable HTTP/2 sender session for a reverse
// proxy that multiplexes requests from several tenants over shared upstream
// connections. It serializes request header blocks and control frames
// through a single ordered queue so that a header block (HEADERS plus any
// CONTINUATION frames) is never interleaved with other frames, applies peer
// SETTINGS header-table reductions and acknowledges them, and clears the
// HPACK dynamic table when switching tenants. Any write failure permanently
// breaks the session: the connection must be discarded.
package proxy

import (
	"errors"
	"io"
	"sync"

	"example.org/offlinecli/hpack"
)

// ErrSessionBroken is returned by every method once the session has failed.
// The underlying connection has been closed and must be discarded.
var ErrSessionBroken = errors.New("proxy: session broken, connection must be discarded")

const defaultMaxFrameSize = 16384

// HeaderField aliases hpack.HeaderField for convenience.
type HeaderField = hpack.HeaderField

// Request is a queued outbound request header block.
type Request struct {
	StreamID  uint32
	Tenant    string
	Fields    []HeaderField
	EndStream bool
}

// Settings carries the peer SETTINGS parameters the session reacts to.
type Settings struct {
	HeaderTableSize uint32
}

type op struct {
	req      *Request
	settings *Settings
	result   chan error
}

// Session serializes header blocks and control frames onto one connection.
type Session struct {
	w      io.Writer
	closer io.Closer
	enc    *hpack.Encoder

	ops  chan op
	done chan struct{}
	quit chan struct{}

	mu  sync.Mutex
	err error

	maxFrameSize uint32
	tenant       string
	hasTenant    bool
}

// NewSession creates a Session writing to w with an HPACK dynamic table of
// tableSize bytes. If w also implements io.Closer it is closed when the
// session breaks or is closed.
func NewSession(w io.Writer, tableSize uint32) *Session {
	s := &Session{
		w:            w,
		enc:          hpack.NewEncoder(tableSize),
		ops:          make(chan op, 64),
		done:         make(chan struct{}),
		quit:         make(chan struct{}),
		maxFrameSize: defaultMaxFrameSize,
	}
	if c, ok := w.(io.Closer); ok {
		s.closer = c
	}
	go s.run()
	return s
}

// SetMaxFrameSize sets the maximum outbound frame payload; larger header
// blocks are fragmented into CONTINUATION frames.
func (s *Session) SetMaxFrameSize(n uint32) {
	s.mu.Lock()
	s.maxFrameSize = n
	s.mu.Unlock()
}

// Submit queues a request header block and blocks until it has been written
// or the session fails.
func (s *Session) Submit(req Request) error {
	return s.enqueue(op{req: &req, result: make(chan error, 1)})
}

// ApplyPeerSettings records a peer SETTINGS frame: it shrinks the encoder
// dynamic table if the peer reduced HEADER_TABLE_SIZE and queues the
// SETTINGS acknowledgment. It blocks until the ACK has been written or the
// session fails.
func (s *Session) ApplyPeerSettings(st Settings) error {
	return s.enqueue(op{settings: &st, result: make(chan error, 1)})
}

// Close shuts the session down and closes the underlying connection.
func (s *Session) Close() error {
	s.fail(errors.New("proxy: session closed"))
	<-s.done
	return nil
}

func (s *Session) enqueue(o op) error {
	s.mu.Lock()
	err := s.err
	s.mu.Unlock()
	if err != nil {
		return ErrSessionBroken
	}
	select {
	case s.ops <- o:
	case <-s.done:
		return ErrSessionBroken
	}
	select {
	case r := <-o.result:
		return r
	case <-s.done:
		return ErrSessionBroken
	}
}

func (s *Session) run() {
	defer close(s.done)
	for {
		select {
		case o := <-s.ops:
			err := s.process(o)
			if err != nil {
				// Discard the connection before reporting the error,
				// so a caller that observes the failure knows the
				// connection is already dead.
				s.fail(err)
				o.result <- err
				return
			}
			o.result <- err
		case <-s.quit:
			return
		}
	}
}

func (s *Session) fail(err error) {
	s.mu.Lock()
	already := s.err != nil
	if !already {
		s.err = err
		close(s.quit)
	}
	s.mu.Unlock()
	if !already && s.closer != nil {
		s.closer.Close()
	}
}

func (s *Session) process(o op) error {
	s.mu.Lock()
	if s.err != nil {
		s.mu.Unlock()
		return ErrSessionBroken
	}
	s.mu.Unlock()
	switch {
	case o.req != nil:
		return s.writeRequest(*o.req)
	case o.settings != nil:
		return s.writeSettingsAck(*o.settings)
	}
	return nil
}

// writeRequest encodes and writes one header block atomically: the HEADERS
// frame and all CONTINUATION frames are assembled into a single buffer and
// written with one Write call, so no control frame can interleave with a
// partially written header block.
func (s *Session) writeRequest(req Request) error {
	if !s.hasTenant || s.tenant != req.Tenant {
		if s.hasTenant {
			// Tenant switch: emit a clear-table update (size 0 then
			// restore) at the head of the next block before any header
			// field may reference this tenant's entries.
			s.enc.ClearTable()
		}
		s.tenant = req.Tenant
		s.hasTenant = true
	}
	block := s.enc.Encode(nil, req.Fields)

	s.mu.Lock()
	maxFrame := s.maxFrameSize
	s.mu.Unlock()

	var buf []byte
	flags := uint8(0)
	if req.EndStream {
		flags |= FlagEndStream
	}
	first := true
	for len(block) > 0 || first {
		n := uint32(len(block))
		if n > maxFrame {
			n = maxFrame
		}
		chunk := block[:n]
		block = block[n:]
		f := flags
		if len(block) == 0 {
			f |= FlagEndHeaders
		}
		if first {
			buf = appendFrame(buf, FrameHeaders, f, req.StreamID, chunk)
			first = false
		} else {
			buf = appendFrame(buf, FrameContinuation, f&FlagEndHeaders, req.StreamID, chunk)
		}
	}
	_, err := s.w.Write(buf)
	return err
}

// writeSettingsAck applies a peer SETTINGS frame and writes the ACK.
func (s *Session) writeSettingsAck(st Settings) error {
	if st.HeaderTableSize < s.enc.MaxDynamicTableSize() {
		// The peer reduced the compression capacity: shrink the table
		// and emit a table size update at the head of the next block.
		s.enc.SetMaxDynamicTableSize(st.HeaderTableSize)
	}
	_, err := s.w.Write(appendFrame(nil, FrameSettings, FlagAck, 0, nil))
	return err
}
