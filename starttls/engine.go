// Package starttls implements a protocol-phase engine for line oriented
// relays (SMTP/STARTTLS style) that must keep the plaintext session strictly
// separated from the TLS session.
//
// The security problem this package solves is pre-read leakage: a buffered
// reader can ingest bytes the client sent immediately after the STARTTLS
// command while still in plaintext. If those bytes survive the upgrade they
// may be interpreted as TLS-protected commands even though any on-path
// attacker could have seen or modified them. Engine therefore owns the
// buffered reader, detects data pipelined across the upgrade boundary,
// refuses such connections and scrubs every plaintext parsing buffer before
// the TLS handshake and again before the upgraded session becomes usable.
package starttls

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Phase is the protocol phase of an Engine.
type Phase uint8

const (
	// Plain is the unencrypted command/response phase before STARTTLS.
	Plain Phase = iota
	// Upgrading is the transient phase covering the TLS handshake. No
	// application reads or writes are allowed while Upgrading.
	Upgrading
	// TLS is the encrypted phase. The Engine reads and writes the upgraded
	// connection through a fresh parser that never saw plaintext bytes.
	TLS
	// Closed is terminal: the underlying connection has been closed.
	Closed
)

// String returns a stable, lowercase name for the phase, useful in logs.
func (p Phase) String() string {
	switch p {
	case Plain:
		return "plain"
	case Upgrading:
		return "upgrading"
	case TLS:
		return "tls"
	case Closed:
		return "closed"
	default:
		return fmt.Sprintf("phase(%d)", uint8(p))
	}
}

// DuplexConn is the injectable full duplex byte stream an Engine drives. It
// is satisfied by net.Conn, net.Pipe connections and *tls.Conn.
type DuplexConn interface {
	Read(p []byte) (n int, err error)
	Write(p []byte) (n int, err error)
	Close() error
}

// CloseWriter is implemented by connections that can half-close the write
// side while keeping the read side open (net.TCPConn, net.UnixConn and
// net.Pipe connections all implement it).
type CloseWriter interface {
	CloseWrite() error
}

// CloseReader is implemented by connections that can half-close the read
// side.
type CloseReader interface {
	CloseRead() error
}

// UpgradeFunc performs the protocol upgrade.
//
// The function receives the original, unwrapped connection so the TLS stack
// owns every byte from this point onward. It must return the upgraded
// connection on success. Returning an error aborts the upgrade; Engine
// closes the connection regardless of whether a non-nil connection is also
// returned.
type UpgradeFunc func(ctx context.Context, raw DuplexConn) (DuplexConn, error)

// Config configures an Engine. The zero value is not valid; use NewEngine.
type Config struct {
	// Conn is the underlying duplex byte stream. Required.
	Conn DuplexConn
	// MaxLine is the maximum accepted command/response line length in bytes,
	// excluding the terminating CRLF. Zero selects DefaultMaxLine.
	MaxLine int
}

// DefaultMaxLine is the maximum line length used when Config.MaxLine is zero.
const DefaultMaxLine = 4096

// Sentinel errors. Wrapped errors from the connection and the upgrade
// callback are passed through unchanged.
var (
	// ErrNoConn is returned when Config.Conn is nil.
	ErrNoConn = errors.New("starttls: nil connection")
	// ErrClosed is returned after the Engine has been closed.
	ErrClosed = errors.New("starttls: connection closed")
	// ErrWrongPhase is returned when an operation is not legal in the
	// current phase (for example reading during the handshake).
	ErrWrongPhase = errors.New("starttls: operation not allowed in current phase")
	// ErrLineTooLong is returned when a line exceeds the configured limit.
	ErrLineTooLong = errors.New("starttls: line too long")
	// ErrBadLine is returned for malformed framing: a bare LF, an embedded
	// CR, a non-printable byte or a truncated line at EOF.
	ErrBadLine = errors.New("starttls: malformed line")
	// ErrPipelined is returned when bytes belonging to the plaintext session
	// are already buffered when STARTTLS is acknowledged. The connection is
	// rejected and closed; see Engine.BeginUpgrade.
	ErrPipelined = errors.New("starttls: data pipelined across STARTTLS boundary")
	// ErrUnsupported is returned when half closing is requested on a
	// connection that does not implement CloseWriter/CloseReader.
	ErrUnsupported = errors.New("starttls: half close not supported by connection")
)

// Engine drives one side of a relay connection through its plaintext,
// upgrading and TLS phases.
//
// All reads go through an Engine-owned lineReader, never through the raw
// connection directly, so bytes consumed before the upgrade cannot leak into
// the TLS session. A single Engine is safe for concurrent use: reads and
// writes may proceed concurrently, while phase transitions are globally
// serialized.
type Engine struct {
	// mu guards phase, conn and reader and is the outer lock; rmu/wmu may be
	// taken while holding mu, never the reverse.
	mu     sync.Mutex
	phase  Phase
	conn   DuplexConn
	reader *lineReader
	maxLen int

	rmu sync.Mutex // serializes ReadLine
	wmu sync.Mutex // serializes WriteLine
}

// NewEngine constructs an Engine in the Plain phase.
func NewEngine(cfg Config) (*Engine, error) {
	if cfg.Conn == nil {
		return nil, ErrNoConn
	}
	maxLen := cfg.MaxLine
	if maxLen <= 0 {
		maxLen = DefaultMaxLine
	}
	return &Engine{
		phase:  Plain,
		conn:   cfg.Conn,
		reader: newLineReader(cfg.Conn, maxLen),
		maxLen: maxLen,
	}, nil
}

// Phase reports the current protocol phase.
func (e *Engine) Phase() Phase {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.phase
}

// Conn returns the connection currently backing the Engine: the raw
// connection in Plain/Upgrading and the upgraded connection in TLS.
func (e *Engine) Conn() DuplexConn {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.conn
}

// MaxLine reports the configured maximum line length.
func (e *Engine) MaxLine() int {
	return e.maxLen
}

// ReadLine returns one line without its terminating CRLF. It is valid in the
// Plain and TLS phases; during the handshake it returns ErrWrongPhase.
func (e *Engine) ReadLine() (string, error) {
	e.rmu.Lock()
	defer e.rmu.Unlock()

	e.mu.Lock()
	switch e.phase {
	case Plain, TLS:
	case Closed:
		e.mu.Unlock()
		return "", ErrClosed
	default:
		e.mu.Unlock()
		return "", ErrWrongPhase
	}
	reader := e.reader
	e.mu.Unlock()

	line, err := reader.readLine()
	if err != nil {
		if err == io.EOF {
			// A clean half-close is not an engine failure; leave the
			// phase untouched so writes/half-close semantics stay usable.
			return "", err
		}
		// A read-side failure (including EOF) is terminal.
		e.markClosed()
	}
	return line, err
}

// WriteLine writes data followed by CRLF. data must be non-empty printable
// ASCII (0x20..0x7e); CR and LF inside data are rejected so callers cannot
// inject extra protocol lines. Writes are valid in Plain and TLS phases.
func (e *Engine) WriteLine(data string) error {
	if len(data) == 0 {
		return fmt.Errorf("%w: empty line", ErrBadLine)
	}
	if len(data) > e.maxLen {
		return fmt.Errorf("%w: %d bytes", ErrLineTooLong, len(data))
	}
	for i := 0; i < len(data); i++ {
		if data[i] < 0x20 || data[i] > 0x7e {
			return fmt.Errorf("%w: byte 0x%02x", ErrBadLine, data[i])
		}
	}

	e.wmu.Lock()
	defer e.wmu.Unlock()

	e.mu.Lock()
	switch e.phase {
	case Plain, TLS:
	case Closed:
		e.mu.Unlock()
		return ErrClosed
	default:
		e.mu.Unlock()
		return ErrWrongPhase
	}
	conn := e.conn
	e.mu.Unlock()

	payload := make([]byte, 0, len(data)+2)
	payload = append(payload, data...)
	payload = append(payload, '\r', '\n')
	if _, err := conn.Write(payload); err != nil {
		e.markClosed()
		return err
	}
	return nil
}

// BeginUpgrade transitions across the STARTTLS boundary using upgrade to
// perform the handshake on the raw connection.
//
// The boundary rules are:
//
//   - If the plaintext parser holds any unconsumed bytes (a client pipelined
//     commands after STARTTLS, or a server pipelined responses), the upgrade
//     is refused with ErrPipelined and the connection is closed. Plaintext
//     bytes are never handed to the TLS stack.
//   - On entry the plaintext parsing buffer is zeroed and dropped, and the
//     reader is detached from the raw connection.
//   - The phase moves to Upgrading, so concurrent application reads and
//     writes fail fast with ErrWrongPhase.
//   - upgrade runs with the original, unwrapped connection. On success the
//     plaintext reader state is cleared once more and a fresh reader bound
//     only to the upgraded connection is installed before the phase becomes
//     TLS.
//   - If upgrade returns an error (handshake failure, alert, timeout) the
//     connection is closed and the phase becomes Closed.
func (e *Engine) BeginUpgrade(ctx context.Context, upgrade UpgradeFunc) (err error) {
	if upgrade == nil {
		return errors.New("starttls: nil upgrade function")
	}

	e.mu.Lock()
	switch e.phase {
	case Closed:
		e.mu.Unlock()
		return ErrClosed
	case Upgrading, TLS:
		e.mu.Unlock()
		return ErrWrongPhase
	}

	// Reject data already buffered in the plaintext session.
	if e.reader.buffered() > 0 {
		raw := e.conn
		e.phase = Closed
		e.scrubReaderLocked()
		e.mu.Unlock()
		_ = raw.Close()
		return ErrPipelined
	}

	raw := e.conn
	// Clear plaintext parsing state before any TLS byte is exchanged and
	// detach the reader so it cannot touch the raw connection again.
	e.scrubReaderLocked()
	e.phase = Upgrading
	e.mu.Unlock()

	defer func() {
		if err != nil {
			e.markClosed()
		}
	}()

	upgraded, err := upgrade(ctx, raw)
	if err != nil {
		if upgraded != nil {
			// A failed handshake must not leak the half-built connection.
			_ = upgraded.Close()
		}
		return err
	}
	if upgraded == nil {
		return errors.New("starttls: upgrade returned nil connection")
	}

	e.mu.Lock()
	if e.phase == Closed {
		// Close raced with the handshake.
		e.mu.Unlock()
		_ = upgraded.Close()
		return ErrClosed
	}
	// Install a pristine parser bound only to the upgraded connection and
	// clear the old state a second time, after the handshake completed.
	e.scrubReaderLocked()
	e.conn = upgraded
	e.reader = newLineReader(upgraded, e.maxLen)
	e.phase = TLS
	e.mu.Unlock()
	return nil
}

// Close terminates the Engine and closes the active connection. It is safe
// to call repeatedly; subsequent calls return ErrClosed.
func (e *Engine) Close() error {
	e.mu.Lock()
	if e.phase == Closed {
		e.mu.Unlock()
		return ErrClosed
	}
	conn := e.conn
	e.phase = Closed
	e.scrubReaderLocked()
	e.mu.Unlock()
	return conn.Close()
}

// CloseWrite half-closes the write side of the active connection. The read
// side stays usable so the peer's remaining data can drain to EOF.
func (e *Engine) CloseWrite() error {
	e.mu.Lock()
	if e.phase == Closed {
		e.mu.Unlock()
		return ErrClosed
	}
	conn := e.conn
	e.mu.Unlock()

	hw, ok := conn.(CloseWriter)
	if !ok {
		return ErrUnsupported
	}
	if err := hw.CloseWrite(); err != nil {
		e.markClosed()
		return err
	}
	return nil
}

// CloseRead half-closes the read side of the active connection.
func (e *Engine) CloseRead() error {
	e.mu.Lock()
	if e.phase == Closed {
		e.mu.Unlock()
		return ErrClosed
	}
	conn := e.conn
	e.mu.Unlock()

	hr, ok := conn.(CloseReader)
	if !ok {
		return ErrUnsupported
	}
	if err := hr.CloseRead(); err != nil {
		e.markClosed()
		return err
	}
	return nil
}

// markClosed moves a failed Engine to Closed and closes its connection.
func (e *Engine) markClosed() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.phase == Closed {
		return
	}
	conn := e.conn
	e.phase = Closed
	e.scrubReaderLocked()
	_ = conn.Close()
}

// scrubReaderLocked zeroes and drops the plaintext parse buffer. The caller
// must hold e.mu.
func (e *Engine) scrubReaderLocked() {
	if e.reader != nil {
		e.reader.reset()
	}
}
