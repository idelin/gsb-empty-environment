package h2guard

import (
	"time"
)

// Stream lifecycle states (only the ones the security parser needs).
const (
	streamIdle    = 0
	streamOpen    = 1 // HEADERS seen, neither side ended
	streamHCLocal = 2 // client sent END_STREAM (half-closed local)
	streamClosed  = 3 // RST_STREAM or terminal DATA seen
)

// EventType enumerates the semantic events emitted by Parser.Handle.
type EventType int

const (
	EventHeaders EventType = iota
	EventData
	EventRSTStream
	EventSettings
	EventPing
	EventGoAway
	EventWindowUpdate
	EventPriority
	EventPushPromise
)

// Event is one validated, security-relevant frame outcome.
type Event struct {
	Type     EventType
	StreamID uint32

	// Headers is populated for EventHeaders/EventPushPromise.
	Headers *DecodedHeaders
	// Fields holds the ordered raw header list (pseudo-headers included),
	// useful for audit logging.
	Fields []HeaderField

	// Data is populated for EventData.
	Data []byte

	// RSTCode is populated for EventRSTStream.
	RSTCode uint32

	// EndStream mirrors the frame flag for EventHeaders/EventData.
	EndStream bool
}

type streamState struct {
	state      int
	dataBytes  int64 // DATA payload bytes accepted so far
	hasCL      bool
	contentLen int64
	ended      bool // client already sent END_STREAM
}

type headerBlockState struct {
	active       bool
	streamID     uint32
	frameType    byte // FrameHeaders or FramePushPromise
	continuation int  // CONTINUATION frames received
	bytes        int  // accumulated fragment bytes
	fragments    [][]byte
	promiseID    uint32 // PUSH_PROMISE promised stream id
}

// Config configures a Parser. Zero fields use DefaultConfig values.
type Config struct {
	// MaxContinuationFrames bounds CONTINUATION frames per header block.
	MaxContinuationFrames int
	// MaxHeaderBlockBytes bounds the reassembled header block size.
	MaxHeaderBlockBytes int
	// MaxFrameSize bounds a single frame payload (SETTINGS_MAX_FRAME_SIZE).
	MaxFrameSize uint32
	// MaxHPACKTableSize is the decoder dynamic table cap.
	MaxHPACKTableSize uint32
	// AllowClientPushPromise enables PUSH_PROMISE parsing. RFC 9113 forbids
	// clients from sending it, so the safe default rejects it.
	AllowClientPushPromise bool
	// RapidReset tunes the sliding-window detector.
	RapidReset RapidResetConfig
	// Now injects a clock for deterministic tests; nil means time.Now.
	Now func() time.Time
}

// DefaultConfig returns the recommended defensive limits.
func DefaultConfig() Config {
	return Config{
		MaxContinuationFrames: 16,
		MaxHeaderBlockBytes:   1 << 20, // 1 MiB
		MaxFrameSize:          DefaultMaxFrameSize,
		MaxHPACKTableSize:     4096,
		RapidReset: RapidResetConfig{
			Window:     10 * time.Second,
			MaxResets:  100,
			ResetRatio: 0.5,
			MinOpens:   100,
		},
	}
}

// Parser is the per-connection security state machine. It is not safe for
// concurrent use; a connection is a single goroutine's read loop.
type Parser struct {
	cfg     Config
	hpack   *hpackDecoder
	streams map[uint32]*streamState
	block   headerBlockState
	rr      *RapidResetDetector
	now     func() time.Time

	lastClientStreamID uint32
	lastProcessedID    uint32

	closed bool
	goAway []byte
	goErr  *ConnError
}

// NewParser constructs a parser. A nil cfg selects DefaultConfig.
func NewParser(cfg *Config) *Parser {
	var c Config
	if cfg == nil {
		c = DefaultConfig()
	} else {
		c = *cfg
		d := DefaultConfig()
		if c.MaxContinuationFrames == 0 {
			c.MaxContinuationFrames = d.MaxContinuationFrames
		}
		if c.MaxHeaderBlockBytes == 0 {
			c.MaxHeaderBlockBytes = d.MaxHeaderBlockBytes
		}
		if c.MaxFrameSize == 0 {
			c.MaxFrameSize = d.MaxFrameSize
		}
		if c.MaxHPACKTableSize == 0 {
			c.MaxHPACKTableSize = 4096
		}
		if c.RapidReset.Window == 0 {
			c.RapidReset = d.RapidReset
		}
	}
	now := c.Now
	if now == nil {
		now = time.Now
	}
	return &Parser{
		cfg:     c,
		hpack:   newHPACKDecoder(c.MaxHPACKTableSize),
		streams: make(map[uint32]*streamState),
		rr:      newRapidResetDetector(c.RapidReset, now),
		now:     now,
	}
}

// Closed reports whether the parser has entered the fatal GOAWAY state.
func (p *Parser) Closed() bool { return p.closed }

// GoAwayFrame returns the serialized GOAWAY frame the host must write before
// closing the transport, or nil before a fatal error.
func (p *Parser) GoAwayFrame() []byte { return p.goAway }

// FatalError returns the connection error that closed the parser.
func (p *Parser) FatalError() *ConnError { return p.goErr }

// RapidReset exposes the detector for metrics/inspection.
func (p *Parser) RapidReset() *RapidResetDetector { return p.rr }

func (p *Parser) fatal(err *ConnError) *ConnError {
	p.closed = true
	p.goErr = err
	p.goAway = BuildGoAway(p.lastProcessedID, err.Code, []byte(err.Message))
	return err
}

func (p *Parser) tripRapidReset() *ConnError {
	return p.fatal(connError(ErrCodeEnhanceYourCalm,
		"rapid reset pattern detected: %d early resets in window", p.rr.resets))
}

func maxU32(a, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}
