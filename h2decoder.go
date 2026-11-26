package h2h1

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Config controls resource bounds for an H2Decoder.
type Config struct {
	HeaderTableSize    uint32
	MaxHeaderBlockSize int
	MaxHeaderListSize  int
	MaxBodySize        int64
}

// Option configures an H2Decoder.
type Option func(*Config)

func WithHeaderTableSize(max uint32) Option {
	return func(c *Config) { c.HeaderTableSize = max }
}

func WithMaxHeaderBlockSize(max int) Option {
	return func(c *Config) { c.MaxHeaderBlockSize = max }
}

func WithMaxHeaderListSize(max int) Option {
	return func(c *Config) { c.MaxHeaderListSize = max }
}

func WithMaxBodySize(max int64) Option {
	return func(c *Config) { c.MaxBodySize = max }
}

func defaultConfig() Config {
	return Config{
		HeaderTableSize:    4096,
		MaxHeaderBlockSize: 4 << 20,
		MaxHeaderListSize:  4 << 20,
		MaxBodySize:        64 << 20,
	}
}

type headerRole uint8

const (
	initialHeaders headerRole = iota
	trailerHeaders
)

type pendingHeaderBlock struct {
	streamID  uint32
	role      headerRole
	endStream bool
	block     []byte
}

type requestStream struct {
	id          uint32
	request     *Request
	hadData     bool
	trailers    bool
	initialDone bool
}

// H2Decoder is a strict, stateful HTTP/2 request stream decoder. It validates
// raw frames, HEADERS/CONTINUATION ordering, HPACK, request semantics, and the
// final DATA/Content-Length invariant. One decoder covers one connection.
type H2Decoder struct {
	cfg           Config
	hp            *HPACKDecoder
	streams       map[uint32]*requestStream
	pending       *pendingHeaderBlock
	completed     []*Request
	maxNewStream  uint32
	haveNewStream bool
}

func NewH2Decoder(options ...Option) *H2Decoder {
	cfg := defaultConfig()
	for _, option := range options {
		if option != nil {
			option(&cfg)
		}
	}
	if cfg.MaxHeaderBlockSize <= 0 {
		cfg.MaxHeaderBlockSize = defaultConfig().MaxHeaderBlockSize
	}
	if cfg.MaxHeaderListSize <= 0 {
		cfg.MaxHeaderListSize = defaultConfig().MaxHeaderListSize
	}
	if cfg.MaxBodySize < 0 {
		cfg.MaxBodySize = defaultConfig().MaxBodySize
	}
	return &H2Decoder{
		cfg:     cfg,
		hp:      NewHPACKDecoder(cfg.HeaderTableSize),
		streams: make(map[uint32]*requestStream),
	}
}

// HPACK exposes the connection-level decoder for inspection or explicit
// SETTINGS updates by a trusted host.
func (d *H2Decoder) HPACK() *HPACKDecoder {
	if d == nil {
		return nil
	}
	return d.hp
}

// SetHeaderTableSize sets the largest dynamic-table size that may be selected
// by a subsequent header block (the SETTINGS_HEADER_TABLE_SIZE limit).
func (d *H2Decoder) SetHeaderTableSize(max uint32) error {
	if d == nil {
		return errors.New("h2h1: nil H2 decoder")
	}
	d.cfg.HeaderTableSize = max
	return d.hp.SetAllowedTableSize(max)
}

// FeedBytes parses a complete raw frame sequence and feeds every frame.
func (d *H2Decoder) FeedBytes(raw []byte) error {
	if d == nil {
		return errors.New("h2h1: nil H2 decoder")
	}
	frames, err := ParseAllFrames(raw)
	if err != nil {
		return err
	}
	for i := range frames {
		if err := d.Feed(frames[i]); err != nil {
			return fmt.Errorf("h2h1: frame %d: %w", i, err)
		}
	}
	return nil
}

// Feed processes one raw frame.
func (d *H2Decoder) Feed(frame Frame) error {
	if d == nil {
		return errors.New("h2h1: nil H2 decoder")
	}
	if int(frame.Header.Length) != len(frame.Payload) {
		return errors.New("h2h1: frame header length does not match payload")
	}

	if d.pending != nil {
		if frame.Header.Type != FrameContinuation {
			return fmt.Errorf("h2h1: continuation: expected CONTINUATION on stream %d, got %s on stream %d",
				d.pending.streamID, frame.Header.Type, frame.Header.StreamID)
		}
		if frame.Header.StreamID != d.pending.streamID {
			return fmt.Errorf("h2h1: continuation: CONTINUATION stream %d does not match HEADERS stream %d",
				frame.Header.StreamID, d.pending.streamID)
		}
		return d.feedContinuation(frame)
	}

	switch frame.Header.Type {
	case FrameHeaders:
		return d.feedHeaders(frame)
	case FrameData:
		return d.feedData(frame)
	case FrameContinuation:
		return errors.New("h2h1: continuation: CONTINUATION without an open HEADERS block")
	case FrameRSTStream:
		return d.feedReset(frame)
	case FrameSettings:
		return feedSettings(frame)
	case FramePing:
		if frame.Header.StreamID != 0 {
			return errors.New("h2h1: PING must use stream 0")
		}
		if frame.Header.Flags&FlagPingAck != 0 {
			if len(frame.Payload) != 0 {
				return errors.New("h2h1: PING ACK must have an empty payload")
			}
			return nil
		}
		if len(frame.Payload) != 8 {
			return errors.New("h2h1: PING payload must be eight bytes")
		}
		return nil
	case FrameGoAway:
		if frame.Header.StreamID != 0 {
			return errors.New("h2h1: GOAWAY must use stream 0")
		}
		if len(frame.Payload) < 8 {
			return errors.New("h2h1: GOAWAY payload is shorter than eight bytes")
		}
		return nil
	case FrameWindowUpdate:
		if len(frame.Payload) != 4 {
			return errors.New("h2h1: WINDOW_UPDATE payload must be four bytes")
		}
		if binary.BigEndian.Uint32(frame.Payload)&0x7fffffff == 0 {
			return errors.New("h2h1: WINDOW_UPDATE increment is zero")
		}
		return nil
	case FramePriority:
		if frame.Header.StreamID == 0 {
			return errors.New("h2h1: PRIORITY must use a non-zero stream")
		}
		if len(frame.Payload) != 5 {
			return errors.New("h2h1: PRIORITY payload must be five bytes")
		}
		return nil
	case FramePushPromise:
		return errors.New("h2h1: PUSH_PROMISE is not valid in a client request stream")
	case FrameAltsvc, FrameOrigin:
		return nil
	default:
		// Unknown frame types are ignored by HTTP/2. They still may not
		// interleave a CONTINUATION sequence, which was checked above.
		return nil
	}
}

func feedSettings(frame Frame) error {
	if frame.Header.StreamID != 0 {
		return errors.New("h2h1: SETTINGS must use stream 0")
	}
	if frame.Header.Flags&FlagSettingsAck != 0 {
		if len(frame.Payload) != 0 {
			return errors.New("h2h1: SETTINGS ACK must have an empty payload")
		}
		return nil
	}
	_, err := ParseSettings(frame.Payload)
	return err
}

func (d *H2Decoder) feedHeaders(frame Frame) error {
	if frame.Header.StreamID == 0 {
		return errors.New("h2h1: HEADERS must use a non-zero stream")
	}
	streamID := frame.Header.StreamID
	st := d.streams[streamID]
	role := initialHeaders
	if st == nil {
		if d.haveNewStream && streamID <= d.maxNewStream {
			return fmt.Errorf("h2h1: new stream id %d is not greater than previous %d", streamID, d.maxNewStream)
		}
		d.haveNewStream = true
		d.maxNewStream = streamID
		st = &requestStream{id: streamID}
		d.streams[streamID] = st
	} else {
		if !st.initialDone {
			return errors.New("h2h1: second initial HEADERS block")
		}
		if st.trailers {
			return errors.New("h2h1: HEADERS after trailers")
		}
		role = trailerHeaders
	}

	if role == trailerHeaders && frame.Header.Flags&FlagHeadersEndStream == 0 {
		return errors.New("h2h1: trailer HEADERS must set END_STREAM")
	}
	fragment, err := frame.HeaderBlockFragment()
	if err != nil {
		return err
	}
	if len(fragment) > d.cfg.MaxHeaderBlockSize {
		return errors.New("h2h1: HPACK block exceeds size limit")
	}
	endStream := frame.Header.Flags&FlagHeadersEndStream != 0
	endHeaders := frame.Header.Flags&FlagHeadersEndHeaders != 0

	if !endHeaders {
		d.pending = &pendingHeaderBlock{
			streamID:  streamID,
			role:      role,
			endStream: endStream,
			block:     append([]byte(nil), fragment...),
		}
		return nil
	}
	return d.applyHeaderBlock(st, role, endStream, fragment)
}

func (d *H2Decoder) feedContinuation(frame Frame) error {
	if len(frame.Payload) > d.cfg.MaxHeaderBlockSize-len(d.pending.block) {
		return errors.New("h2h1: HPACK block exceeds size limit")
	}
	d.pending.block = append(d.pending.block, frame.Payload...)
	if frame.Header.Flags&FlagContinuationEndHeaders == 0 {
		return nil
	}
	p := d.pending
	d.pending = nil
	return d.applyHeaderBlock(d.streams[p.streamID], p.role, p.endStream, p.block)
}

func (d *H2Decoder) applyHeaderBlock(st *requestStream, role headerRole, endStream bool, block []byte) error {
	if st == nil {
		return errors.New("h2h1: header block targets an unknown stream")
	}
	fields, err := d.hp.Decode(block)
	if err != nil {
		return err
	}
	headerBytes := 0
	for _, field := range fields {
		headerBytes += len(field.Name) + len(field.Value) + 32
		if headerBytes > d.cfg.MaxHeaderListSize {
			return errors.New("h2h1: decoded header list exceeds size limit")
		}
	}

	switch role {
	case initialHeaders:
		meta, err := ValidateRequestHeaders(fields)
		if err != nil {
			return err
		}
		req := &Request{StreamID: st.id, Headers: meta.Headers}
		req.applyMeta(meta)
		st.request = req
		st.initialDone = true
		if endStream {
			return d.finishStream(st)
		}
		return nil
	case trailerHeaders:
		if err := ValidateTrailers(fields); err != nil {
			return err
		}
		st.request.Trailers = fields
		st.trailers = true
		if !endStream {
			return errors.New("h2h1: trailers require END_STREAM")
		}
		return d.finishStream(st)
	default:
		return errors.New("h2h1: invalid header role")
	}
}

func (d *H2Decoder) feedData(frame Frame) error {
	if frame.Header.StreamID == 0 {
		return errors.New("h2h1: DATA must use a non-zero stream")
	}
	st := d.streams[frame.Header.StreamID]
	if st == nil || !st.initialDone {
		return fmt.Errorf("h2h1: DATA on stream %d before initial HEADERS", frame.Header.StreamID)
	}
	if st.trailers {
		return errors.New("h2h1: DATA after trailers")
	}
	body, err := frame.DataBody()
	if err != nil {
		return err
	}
	st.hadData = true
	if int64(len(st.request.Body))+int64(len(body)) > d.cfg.MaxBodySize {
		return errors.New("h2h1: request body exceeds size limit")
	}
	st.request.Body = append(st.request.Body, body...)
	if st.request.HasContentLength && int64(len(st.request.Body)) > st.request.ContentLength {
		return fmt.Errorf("h2h1: content-length: DATA on stream %d exceeds declared Content-Length %d",
			st.id, st.request.ContentLength)
	}
	if frame.Header.Flags&FlagDataEndStream != 0 {
		return d.finishStream(st)
	}
	return nil
}

func (d *H2Decoder) feedReset(frame Frame) error {
	if frame.Header.StreamID == 0 {
		return errors.New("h2h1: RST_STREAM must use a non-zero stream")
	}
	if len(frame.Payload) != 3 {
		return errors.New("h2h1: RST_STREAM payload must be three bytes")
	}
	if st := d.streams[frame.Header.StreamID]; st != nil {
		delete(d.streams, frame.Header.StreamID)
	}
	return nil
}

func (d *H2Decoder) finishStream(st *requestStream) error {
	if st == nil || st.request == nil {
		return errors.New("h2h1: cannot finish a stream without request headers")
	}
	if err := st.request.Validate(); err != nil {
		return err
	}
	d.completed = append(d.completed, st.request)
	delete(d.streams, st.id)
	return nil
}

// Finish verifies that no CONTINUATION is open and no request stream was
// truncated, then returns completed requests.
func (d *H2Decoder) Finish() ([]*Request, error) {
	if d == nil {
		return nil, errors.New("h2h1: nil H2 decoder")
	}
	if d.pending != nil {
		return nil, fmt.Errorf("h2h1: continuation: unterminated HEADERS sequence on stream %d", d.pending.streamID)
	}
	if len(d.streams) != 0 {
		for _, st := range d.streams {
			return nil, fmt.Errorf("h2h1: request stream %d ended before END_STREAM", st.id)
		}
	}
	out := make([]*Request, len(d.completed))
	copy(out, d.completed)
	return out, nil
}

func (d *H2Decoder) Requests() []*Request {
	if d == nil {
		return nil
	}
	out := make([]*Request, len(d.completed))
	copy(out, d.completed)
	return out
}

// ParseH2Requests decodes a complete raw HTTP/2 frame stream into strictly
// validated requests.
func ParseH2Requests(raw []byte, options ...Option) ([]*Request, error) {
	d := NewH2Decoder(options...)
	if err := d.FeedBytes(raw); err != nil {
		return nil, err
	}
	return d.Finish()
}

// DecodeH2Requests is an alias for ParseH2Requests.
func DecodeH2Requests(raw []byte, options ...Option) ([]*Request, error) {
	return ParseH2Requests(raw, options...)
}

// ParseH2Request reads frames from a reader and returns all validated requests.
func ParseH2Request(r io.Reader, options ...Option) ([]*Request, error) {
	frames, err := ParseFrameStream(r)
	if err != nil {
		return nil, err
	}
	d := NewH2Decoder(options...)
	for i := range frames {
		if err := d.Feed(frames[i]); err != nil {
			return nil, fmt.Errorf("h2h1: frame %d: %w", i, err)
		}
	}
	return d.Finish()
}
