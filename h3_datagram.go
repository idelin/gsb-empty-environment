package netsec

import (
	"fmt"
	"sync"
)

const (
	// H3DatagramSettingID is the HTTP/3 SETTINGS identifier for
	// SETTINGS_H3_DATAGRAM (RFC 9297, Section 2.1.1).
	H3DatagramSettingID uint64 = 0x33

	// H3DatagramErrorCode is the HTTP/3 error code H3_DATAGRAM_ERROR
	// (RFC 9297, Section 5.2).
	H3DatagramErrorCode uint64 = 0x33

	// MaxQuarterStreamID is the largest legal Quarter Stream ID. A larger
	// value is an H3_DATAGRAM_ERROR.
	MaxQuarterStreamID uint64 = (1 << 60) - 1

	// MaxBufferedDatagrams is the number of pending datagrams retained for
	// one not-yet-created stream. When the limit is reached the oldest
	// datagram is discarded.
	MaxBufferedDatagrams = 16
)

// H3ConnectionError describes an HTTP/3 connection error that the embedding
// server must use to terminate the connection.
type H3ConnectionError struct {
	Code uint64
	Name string
}

func (e *H3ConnectionError) Error() string {
	return e.Name
}

// ErrH3DatagramError is returned when the connection must be closed with
// H3_DATAGRAM_ERROR. It is also used for a malformed or truncated Quarter
// Stream ID, as required by RFC 9297.
var ErrH3DatagramError = &H3ConnectionError{
	Code: H3DatagramErrorCode,
	Name: "H3_DATAGRAM_ERROR",
}

// DatagramHandler receives a validated datagram. streamID is the full QUIC
// stream ID (Quarter Stream ID multiplied by four), and payload excludes the
// Quarter Stream ID field. payload must not be retained: it can alias the
// caller's input buffer.
type DatagramHandler func(streamID uint64, payload []byte)

// ConnectionErrorHandler is invoked once for a fatal datagram-processing
// error. The server should abort the connection with the supplied code.
type ConnectionErrorHandler func(code uint64, reason string)

// StreamState is the demultiplexer's view of a request stream.
type StreamState uint8

const (
	// StreamUnknown means that the stream has not been created yet. Datagrams
	// for this stream are buffered.
	StreamUnknown StreamState = iota

	// StreamOpen means that the stream exists but has not established an
	// HTTP Datagram channel. Datagrams for it are dropped.
	StreamOpen

	// StreamDatagramChannel means that a CONNECT request has established an
	// HTTP Datagram channel. Datagrams for it are delivered.
	StreamDatagramChannel

	// StreamClosed means that the stream was closed. Datagrams for it are
	// dropped, including any that were buffered.
	StreamClosed
)

type streamRecord struct {
	state StreamState
}

// Dispatcher validates and demultiplexes HTTP/3 Datagrams belonging to one
// HTTP/3 connection. It is safe for concurrent use.
//
// The zero value is not ready for use; call NewDispatcher.
type Dispatcher struct {
	mu sync.Mutex

	localH3Datagram bool
	peerH3Datagram  bool

	handler        DatagramHandler
	connectionErr  ConnectionErrorHandler
	streams        map[uint64]streamRecord
	buffers        map[uint64][][]byte
	connectionDead bool
}

type dispatcherConfig struct {
	handler       DatagramHandler
	connectionErr ConnectionErrorHandler
	localDatagram bool
	peerDatagram  bool
}

// Option configures a Dispatcher.
type Option func(*dispatcherConfig)

// WithHandler installs the callback for validated datagrams.
func WithHandler(handler DatagramHandler) Option {
	return func(c *dispatcherConfig) {
		c.handler = handler
	}
}

// WithConnectionErrorHandler installs the callback used to report fatal
// H3_DATAGRAM_ERROR conditions to the HTTP/3 server.
func WithConnectionErrorHandler(handler ConnectionErrorHandler) Option {
	return func(c *dispatcherConfig) {
		c.connectionErr = handler
	}
}

// WithSettings records whether this endpoint and its peer each advertised
// SETTINGS_H3_DATAGRAM = 1. Datagrams are accepted only when both are true.
func WithSettings(local, peer bool) Option {
	return func(c *dispatcherConfig) {
		c.localDatagram = local
		c.peerDatagram = peer
	}
}

// WithLocalDatagramSupport records this endpoint's SETTINGS_H3_DATAGRAM value.
func WithLocalDatagramSupport(enabled bool) Option {
	return func(c *dispatcherConfig) {
		c.localDatagram = enabled
	}
}

// WithPeerDatagramSupport records the peer's SETTINGS_H3_DATAGRAM value.
func WithPeerDatagramSupport(enabled bool) Option {
	return func(c *dispatcherConfig) {
		c.peerDatagram = enabled
	}
}

// NewDispatcher creates a connection-scoped datagram dispatcher. By default
// HTTP/3 Datagrams are not negotiated.
func NewDispatcher(options ...Option) *Dispatcher {
	config := dispatcherConfig{}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}

	return &Dispatcher{
		localH3Datagram: config.localDatagram,
		peerH3Datagram:  config.peerDatagram,
		handler:         config.handler,
		connectionErr:   config.connectionErr,
		streams:         make(map[uint64]streamRecord),
		buffers:         make(map[uint64][][]byte),
	}
}

// NewServerDispatcher is a convenience constructor for the common server
// case. The server is the local endpoint and the client is the peer.
func NewServerDispatcher(handler DatagramHandler, localDatagram, peerDatagram bool) *Dispatcher {
	return NewDispatcher(
		WithHandler(handler),
		WithSettings(localDatagram, peerDatagram),
	)
}

// SetSettings updates the local and peer SETTINGS_H3_DATAGRAM values.
func (d *Dispatcher) SetSettings(local, peer bool) {
	d.mu.Lock()
	d.localH3Datagram = local
	d.peerH3Datagram = peer
	d.mu.Unlock()
}

// SetLocalDatagramSupport updates this endpoint's SETTINGS_H3_DATAGRAM value.
func (d *Dispatcher) SetLocalDatagramSupport(enabled bool) {
	d.mu.Lock()
	d.localH3Datagram = enabled
	d.mu.Unlock()
}

// SetPeerDatagramSupport updates the peer's SETTINGS_H3_DATAGRAM value.
func (d *Dispatcher) SetPeerDatagramSupport(enabled bool) {
	d.mu.Lock()
	d.peerH3Datagram = enabled
	d.mu.Unlock()
}

// Negotiated reports whether both endpoints advertised HTTP Datagram support.
func (d *Dispatcher) Negotiated() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.localH3Datagram && d.peerH3Datagram
}

// SetHandler replaces the validated-datagram callback.
func (d *Dispatcher) SetHandler(handler DatagramHandler) {
	d.mu.Lock()
	d.handler = handler
	d.mu.Unlock()
}

// SetConnectionErrorHandler replaces the fatal-error callback.
func (d *Dispatcher) SetConnectionErrorHandler(handler ConnectionErrorHandler) {
	d.mu.Lock()
	d.connectionErr = handler
	d.mu.Unlock()
}

// HandleDatagram validates and dispatches one HTTP/3 Datagram. The input is
// the payload of a QUIC DATAGRAM frame, beginning with the Quarter Stream ID
// varint.
//
// The returned error is ErrH3DatagramError when the connection must be
// terminated. Nil means the datagram was delivered, buffered, or silently
// discarded as required by RFC 9297 and the connection's stream state.
func (d *Dispatcher) HandleDatagram(datagram []byte) error {
	quarterStreamID, payload, err := DecodeVarint(datagram)
	if err != nil {
		return d.connectionFailure("malformed Quarter Stream ID")
	}
	return d.HandleQuarterStreamID(quarterStreamID, payload)
}

// HandleQuarterStreamID dispatches a datagram whose Quarter Stream ID has
// already been decoded. payload is the HTTP Datagram payload that follows the
// Quarter Stream ID field.
func (d *Dispatcher) HandleQuarterStreamID(quarterStreamID uint64, payload []byte) error {
	if err := d.requireNegotiated(); err != nil {
		return err
	}
	if quarterStreamID > MaxQuarterStreamID {
		return d.connectionFailure("Quarter Stream ID exceeds 2^60-1")
	}

	streamID := quarterStreamID << 2
	if !isPeerInitiatedBidirectional(streamID) {
		return nil
	}
	return d.dispatchStream(streamID, payload)
}

// HandleStreamDatagram dispatches a datagram already associated with a full
// QUIC stream ID. This form is useful when an HTTP/3 server has already
// performed the Quarter Stream ID conversion. Foreign stream IDs (including
// server-initiated and unidirectional streams) are silently discarded.
func (d *Dispatcher) HandleStreamDatagram(streamID uint64, payload []byte) error {
	if err := d.requireNegotiated(); err != nil {
		return err
	}
	if !isPeerInitiatedBidirectional(streamID) {
		return nil
	}
	if streamID > MaxQuarterStreamID<<2 {
		return d.connectionFailure("stream ID exceeds the legal quarter stream ID range")
	}
	return d.dispatchStream(streamID, payload)
}

func (d *Dispatcher) requireNegotiated() error {
	d.mu.Lock()
	if d.connectionDead {
		d.mu.Unlock()
		return ErrH3DatagramError
	}
	negotiated := d.localH3Datagram && d.peerH3Datagram
	d.mu.Unlock()
	if !negotiated {
		return d.connectionFailure("SETTINGS_H3_DATAGRAM was not negotiated by both peers")
	}
	return nil
}

func (d *Dispatcher) dispatchStream(streamID uint64, payload []byte) error {
	d.mu.Lock()
	if d.connectionDead {
		d.mu.Unlock()
		return ErrH3DatagramError
	}

	record, exists := d.streams[streamID]
	if exists {
		switch record.state {
		case StreamClosed, StreamOpen:
			d.mu.Unlock()
			return nil
		case StreamDatagramChannel:
			handler := d.handler
			d.mu.Unlock()
			if handler != nil {
				handler(streamID, payload)
			}
			return nil
		default:
			d.mu.Unlock()
			return nil
		}
	}

	// The stream does not exist yet. Retain only peer-initiated
	// bidirectional streams, and keep the newest sixteen datagrams.
	buffer := d.buffers[streamID]
	if len(buffer) >= MaxBufferedDatagrams {
		copy(buffer, buffer[1:])
		buffer[len(buffer)-1] = append([]byte(nil), payload...)
	} else {
		buffer = append(buffer, append([]byte(nil), payload...))
	}
	d.buffers[streamID] = buffer
	d.mu.Unlock()
	return nil
}

// DispatchDatagram is an alias for HandleQuarterStreamID.
func (d *Dispatcher) DispatchDatagram(quarterStreamID uint64, payload []byte) error {
	return d.HandleQuarterStreamID(quarterStreamID, payload)
}

// OpenStream records that the peer created streamID without establishing an
// HTTP Datagram channel. Any buffered datagrams for the stream are discarded.
// Invalid, foreign, or closed stream IDs are ignored. It reports whether the
// stream was accepted.
func (d *Dispatcher) OpenStream(streamID uint64) bool {
	if !isPeerInitiatedBidirectional(streamID) {
		return false
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.connectionDead {
		return false
	}
	if record, ok := d.streams[streamID]; ok {
		return record.state != StreamClosed
	}
	d.streams[streamID] = streamRecord{state: StreamOpen}
	delete(d.buffers, streamID)
	return true
}

// RegisterRequest records the method of a peer-created request stream. Only
// CONNECT-class requests establish an HTTP Datagram channel. For any other
// method, buffered datagrams are discarded and later datagrams are dropped.
// It reports whether streamID is a valid peer-initiated bidirectional stream.
func (d *Dispatcher) RegisterRequest(streamID uint64, method string) bool {
	if !isPeerInitiatedBidirectional(streamID) {
		return false
	}

	d.mu.Lock()
	if d.connectionDead {
		d.mu.Unlock()
		return false
	}
	record, exists := d.streams[streamID]
	if exists && record.state == StreamClosed {
		d.mu.Unlock()
		return false
	}
	if method != "CONNECT" {
		d.streams[streamID] = streamRecord{state: StreamOpen}
		delete(d.buffers, streamID)
		d.mu.Unlock()
		return true
	}

	d.streams[streamID] = streamRecord{state: StreamDatagramChannel}
	buffer := d.buffers[streamID]
	delete(d.buffers, streamID)
	handler := d.handler
	d.mu.Unlock()

	if handler != nil {
		for _, payload := range buffer {
			handler(streamID, payload)
		}
	}
	return true
}

// OpenConnectStream records that a CONNECT-class request established an HTTP
// Datagram channel on streamID and synchronously delivers any buffered
// datagrams in arrival order. It reports whether the stream was accepted.
func (d *Dispatcher) OpenConnectStream(streamID uint64) bool {
	return d.RegisterRequest(streamID, "CONNECT")
}

// EstablishDatagramChannel is an alias for OpenConnectStream.
func (d *Dispatcher) EstablishDatagramChannel(streamID uint64) bool {
	return d.OpenConnectStream(streamID)
}

// CloseStream records that streamID was closed. Buffered datagrams for the
// stream are discarded, and future datagrams for the stream are silently
// dropped. Stream IDs are never reused within a QUIC connection, so closed
// IDs are retained as tombstones.
func (d *Dispatcher) CloseStream(streamID uint64) {
	d.mu.Lock()
	d.streams[streamID] = streamRecord{state: StreamClosed}
	delete(d.buffers, streamID)
	d.mu.Unlock()
}

// StreamState returns the current state of streamID.
func (d *Dispatcher) StreamState(streamID uint64) StreamState {
	d.mu.Lock()
	defer d.mu.Unlock()
	record, ok := d.streams[streamID]
	if !ok {
		return StreamUnknown
	}
	return record.state
}

// BufferedDatagrams returns the number of datagrams currently buffered for a
// stream that has not been created yet.
func (d *Dispatcher) BufferedDatagrams(streamID uint64) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.buffers[streamID])
}

func (d *Dispatcher) connectionFailure(reason string) error {
	d.mu.Lock()
	if d.connectionDead {
		d.mu.Unlock()
		return ErrH3DatagramError
	}
	d.connectionDead = true
	handler := d.connectionErr
	d.mu.Unlock()

	if handler != nil {
		handler(H3DatagramErrorCode, reason)
	}
	return ErrH3DatagramError
}

// isPeerInitiatedBidirectional recognizes client-initiated bidirectional
// streams. HTTP/3 servers receive requests on these streams, whose IDs are
// divisible by four (RFC 9000, Section 2.1).
func isPeerInitiatedBidirectional(streamID uint64) bool {
	return streamID&0x3 == 0
}

// ValidateStreamID applies the same peer-stream rule used by the dispatcher.
// It is exported for servers that need to reject foreign stream IDs before
// reporting stream events.
func ValidateStreamID(streamID uint64) bool {
	return isPeerInitiatedBidirectional(streamID)
}

// FormatStreamID is a small helper for diagnostics.
func FormatStreamID(streamID uint64) string {
	return fmt.Sprintf("stream %d", streamID)
}
