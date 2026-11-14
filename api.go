package netsec

import (
	"encoding/binary"
	"sync"
)

// HeaderBlockDecoder is a descriptive alias for Decoder.
type HeaderBlockDecoder = Decoder

// GatewayDecoder is a descriptive alias for Decoder.
type GatewayDecoder = Decoder

// DefaultConfig returns the normalized default configuration.
func DefaultConfig() Config { return normalizedConfig(Config{}) }

// New returns a decoder with default limits.
func New() *Decoder { return NewDecoder(Config{}) }

// NewSecurityContext builds a peer/tenant/generation context.
func NewSecurityContext(peerConnectionID, tenantID string, settingsGeneration uint64) SecurityContext {
	return SecurityContext{
		PeerConnectionID:   peerConnectionID,
		TenantID:           tenantID,
		SettingsGeneration: settingsGeneration,
	}
}

// NewHeaderBlockDecoder is an alias for NewDecoder.
func NewHeaderBlockDecoder(cfg Config) *Decoder { return NewDecoder(cfg) }

// DecodeHeaderBlock decodes one standalone, complete block with a fresh
// compression context. Use a Decoder when blocks must share HPACK state.
func DecodeHeaderBlock(block []byte, cfg Config) ([]HeaderField, error) {
	res, err := NewDecoder(cfg).DecodeHeaderBlock(1, block, false)
	if err != nil {
		return nil, err
	}
	return res.Fields, nil
}

// DecodeBlock is an alias for the package-level DecodeHeaderBlock.
func DecodeBlock(block []byte, cfg Config) ([]HeaderField, error) {
	return DecodeHeaderBlock(block, cfg)
}

// DecodeHeaderBlock decodes one complete header block (equivalent to a
// HEADERS frame with END_HEADERS set).
func (d *Decoder) DecodeHeaderBlock(streamID uint32, block []byte, endStream bool) (Result, error) {
	var flags FrameFlags = FlagEndHeaders
	if endStream {
		flags |= FlagEndStream
	}
	return d.ProcessFrame(Frame{Type: FrameHeaders, Flags: flags, StreamID: streamID, Payload: block})
}

// DecodeHeaderBlockContext decodes one complete block under an explicit
// peer/tenant context.
func (d *Decoder) DecodeHeaderBlockContext(ctx SecurityContext, streamID uint32, block []byte, endStream bool) (Result, error) {
	var flags FrameFlags = FlagEndHeaders
	if endStream {
		flags |= FlagEndStream
	}
	return d.ProcessFrameForContext(Frame{Type: FrameHeaders, Flags: flags, StreamID: streamID, Payload: block}, ctx)
}

// ProcessHeaders is a convenience wrapper for a HEADERS frame fragment.
func (d *Decoder) ProcessHeaders(streamID uint32, fragment []byte, endHeaders, endStream bool) (Result, error) {
	var flags FrameFlags
	if endHeaders {
		flags |= FlagEndHeaders
	}
	if endStream {
		flags |= FlagEndStream
	}
	return d.ProcessFrame(Frame{Type: FrameHeaders, Flags: flags, StreamID: streamID, Payload: fragment})
}

// ProcessContinuation is a convenience wrapper for a CONTINUATION frame.
func (d *Decoder) ProcessContinuation(streamID uint32, fragment []byte, endHeaders bool) (Result, error) {
	var flags FrameFlags
	if endHeaders {
		flags |= FlagEndHeaders
	}
	return d.ProcessFrame(Frame{Type: FrameContinuation, Flags: flags, StreamID: streamID, Payload: fragment})
}

// ProcessRSTStream is a convenience wrapper for RST_STREAM.
func (d *Decoder) ProcessRSTStream(streamID, code uint32) (Result, error) {
	var payload [4]byte
	binary.BigEndian.PutUint32(payload[:], code)
	return d.ProcessFrame(Frame{Type: FrameRSTStream, StreamID: streamID, Payload: payload[:]})
}

// ProcessSettings is a convenience wrapper for a SETTINGS frame.
func (d *Decoder) ProcessSettings(payload []byte, ack bool) error {
	var flags FrameFlags
	if ack {
		flags |= FlagAck
	}
	_, err := d.ProcessFrame(Frame{Type: FrameSettings, Flags: flags, Payload: payload})
	return err
}

// ContextPool reuses one Decoder per peer connection while enforcing that
// only one tenant owns its HPACK context at a time. Acquire serializes context
// selection; the returned Decoder must still be driven by one frame-processing
// goroutine at a time.
type ContextPool struct {
	mu          sync.Mutex
	cfg         Config
	decoders    map[string]*Decoder
	initialized bool
}

// Pool is an alias for ContextPool.
type Pool = ContextPool

// NewContextPool creates an empty peer-context pool.
func NewContextPool(cfg Config) *ContextPool {
	return &ContextPool{cfg: normalizedConfig(cfg), decoders: make(map[string]*Decoder), initialized: true}
}

// NewPool is an alias for NewContextPool.
func NewPool(cfg Config) *ContextPool { return NewContextPool(cfg) }

// Acquire returns the decoder for ctx. When settings is non-nil it is applied
// before binding, which is how a tenant switch obtains the SETTINGS
// synchronization required to reset the compression context.
func (p *ContextPool) Acquire(ctx SecurityContext, settings *SettingsSync) (*Decoder, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.init()
	d := p.decoders[ctx.PeerConnectionID]
	if d == nil || d.Closed() {
		d = NewDecoder(p.cfg)
		p.decoders[ctx.PeerConnectionID] = d
	}
	if settings != nil {
		if err := d.ApplySettings(*settings); err != nil {
			return nil, err
		}
	}
	if err := d.BindContext(ctx); err != nil {
		return nil, err
	}
	return d, nil
}

// AcquireTenant is a convenience wrapper around Acquire.
func (p *ContextPool) AcquireTenant(peerConnectionID, tenantID string, settingsGeneration uint64, settings *SettingsSync) (*Decoder, error) {
	return p.Acquire(SecurityContext{
		PeerConnectionID:   peerConnectionID,
		TenantID:           tenantID,
		SettingsGeneration: settingsGeneration,
	}, settings)
}

// Release keeps the peer decoder in the pool. It exists so gateway code can
// express checkout/check-in symmetrically.
func (p *ContextPool) Release(peerConnectionID string, d *Decoder) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.init()
	if d == nil {
		return
	}
	if p.decoders[peerConnectionID] == d {
		return
	}
	p.decoders[peerConnectionID] = d
}

// Remove drops a peer connection from the pool.
func (p *ContextPool) Remove(peerConnectionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.init()
	delete(p.decoders, peerConnectionID)
}

// Decoder returns the pooled decoder for a peer, if present.
func (p *ContextPool) Decoder(peerConnectionID string) (*Decoder, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.init()
	d, ok := p.decoders[peerConnectionID]
	return d, ok
}

func (p *ContextPool) init() {
	if p.initialized {
		return
	}
	p.cfg = normalizedConfig(p.cfg)
	p.decoders = make(map[string]*Decoder)
	p.initialized = true
}
