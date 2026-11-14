package netsec

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// StreamState is the subset of HTTP/2 stream state needed by the header
// decoder. It is tracked from the receiving endpoint's perspective.
type StreamState uint8

const (
	StreamIdle StreamState = iota
	StreamOpen
	StreamHalfClosedRemote
	StreamHalfClosedLocal
	StreamClosed
)

func (s StreamState) String() string {
	switch s {
	case StreamIdle:
		return "idle"
	case StreamOpen:
		return "open"
	case StreamHalfClosedRemote:
		return "half-closed(remote)"
	case StreamHalfClosedLocal:
		return "half-closed(local)"
	case StreamClosed:
		return "closed"
	default:
		return "unknown"
	}
}

// SecurityContext identifies the connection and security tenant to which the
// HPACK context belongs. A compression context is never shared across a
// different peer connection or tenant.
type SecurityContext struct {
	PeerConnectionID   string `json:"peerConnectionId"`
	TenantID           string `json:"tenantId"`
	SettingsGeneration uint64 `json:"settingsGeneration"`
}

func (c SecurityContext) equal(o SecurityContext) bool {
	return c.PeerConnectionID == o.PeerConnectionID &&
		c.TenantID == o.TenantID &&
		c.SettingsGeneration == o.SettingsGeneration
}

// SettingsSync describes SETTINGS synchronization for one peer/tenant
// generation. Synchronized must be true before the generation may be used to
// switch tenants.
type SettingsSync struct {
	PeerConnectionID   string `json:"peerConnectionId"`
	TenantID           string `json:"tenantId"`
	Generation         uint64 `json:"generation"`
	HeaderTableSize    uint32 `json:"headerTableSize,omitempty"`
	HasHeaderTableSize bool   `json:"hasHeaderTableSize,omitempty"`
	Synchronized       bool   `json:"synchronized"`
}

type settingsKey struct {
	peer   string
	tenant string
	gen    uint64
}

func settingsKeyFor(c SecurityContext) settingsKey {
	return settingsKey{peer: c.PeerConnectionID, tenant: c.TenantID, gen: c.SettingsGeneration}
}

type streamInfo struct {
	state   StreamState
	headers int
}

type activeBlock struct {
	blockID    uint64
	streamID   uint32
	fragments  [][]byte
	compressed int
	endStream  bool
	context    SecurityContext
	trailers   bool
	canceled   bool
	discard    bool
	pendingErr error
}

// Result is the structured result of processing one frame.
type Result struct {
	StreamID          uint32        `json:"streamId"`
	Fields            []HeaderField `json:"fields,omitempty"`
	Commit            *TableCommit  `json:"commit,omitempty"`
	EndStream         bool          `json:"endStream,omitempty"`
	EndHeaders        bool          `json:"endHeaders,omitempty"`
	Pending           bool          `json:"pending,omitempty"`
	Canceled          bool          `json:"canceled,omitempty"`
	Discarded         bool          `json:"discarded,omitempty"`
	CompressedBytes   int           `json:"compressedBytes,omitempty"`
	DecompressedBytes int           `json:"decompressedBytes,omitempty"`
}

// Decoder is a connection-scoped, transactional HTTP/2 header block decoder.
// It buffers HEADERS and CONTINUATION frames, decodes HPACK against a cloned
// dynamic table, validates the complete field list, and only then publishes
// the table changes.
type Decoder struct {
	cfg Config

	table       *DynamicTable
	ctx         SecurityContext
	bound       bool
	active      *activeBlock
	streams     map[uint32]*streamInfo
	maxStreamID uint32

	nextBlockID    uint64
	commitSequence uint64
	commits        []TableCommit

	closed  bool
	connErr error

	settingsRecords map[settingsKey]SettingsSync
	autoGeneration  uint64
}

// NewDecoder creates a connection-scoped decoder.
func NewDecoder(cfg Config) *Decoder {
	cfg = normalizedConfig(cfg)
	return &Decoder{
		cfg:             cfg,
		table:           NewDynamicTable(cfg.MaxDynamicTableSize),
		streams:         make(map[uint32]*streamInfo),
		settingsRecords: make(map[settingsKey]SettingsSync),
	}
}

// NewHeaderDecoder is an alias for NewDecoder.
func NewHeaderDecoder(cfg Config) *Decoder { return NewDecoder(cfg) }

// NewGatewayDecoder is an alias for NewDecoder.
func NewGatewayDecoder(cfg Config) *Decoder { return NewDecoder(cfg) }

func (d *Decoder) init() {
	if d.table != nil {
		return
	}
	d.cfg = normalizedConfig(d.cfg)
	d.table = NewDynamicTable(d.cfg.MaxDynamicTableSize)
	d.streams = make(map[uint32]*streamInfo)
	d.settingsRecords = make(map[settingsKey]SettingsSync)
}

// Config returns the normalized configuration.
func (d *Decoder) Config() Config {
	d.init()
	c := d.cfg
	c.SensitiveHeaders = append([]string(nil), d.cfg.SensitiveHeaders...)
	c.SingletonHeaders = append([]string(nil), d.cfg.SingletonHeaders...)
	return c
}

// Context returns the currently bound security context.
func (d *Decoder) Context() SecurityContext { return d.ctx }

// Bound reports whether a security context has been bound.
func (d *Decoder) Bound() bool { return d.bound }

// SettingsGeneration returns the generation currently applied to the context.
func (d *Decoder) SettingsGeneration() uint64 { return d.ctx.SettingsGeneration }

// SynchronizedSettingsGeneration returns the newest SETTINGS generation for
// which synchronization completed.
func (d *Decoder) SynchronizedSettingsGeneration() uint64 {
	d.init()
	if rec, ok := d.settingsRecords[settingsKeyFor(d.ctx)]; ok && rec.Synchronized {
		return rec.Generation
	}
	return 0
}

// DynamicTable returns a snapshot of the live HPACK dynamic table.
func (d *Decoder) DynamicTable() *DynamicTable {
	d.init()
	return d.table.Clone()
}

// Table is an alias for DynamicTable.
func (d *Decoder) Table() *DynamicTable { return d.DynamicTable() }

// Commits returns replayable records for every successfully committed header
// block currently retained.
func (d *Decoder) Commits() []TableCommit {
	out := make([]TableCommit, len(d.commits))
	for i, c := range d.commits {
		out[i] = cloneCommit(c)
	}
	return out
}

// CommitRecords is an alias for Commits.
func (d *Decoder) CommitRecords() []TableCommit { return d.Commits() }

// LastCommit returns the newest committed header block record.
func (d *Decoder) LastCommit() (TableCommit, bool) {
	if len(d.commits) == 0 {
		return TableCommit{}, false
	}
	return cloneCommit(d.commits[len(d.commits)-1]), true
}

func cloneCommit(c TableCommit) TableCommit {
	c.Operations = append([]TableOp(nil), c.Operations...)
	c.Evicted = append([]HeaderField(nil), c.Evicted...)
	c.Fields = append([]HeaderField(nil), c.Fields...)
	c.TableBefore = append([]HeaderField(nil), c.TableBefore...)
	return c
}

func (d *Decoder) recordCommit(c TableCommit) {
	d.commits = append(d.commits, c)
	if d.cfg.CommitHistory > 0 && len(d.commits) > d.cfg.CommitHistory {
		drop := len(d.commits) - d.cfg.CommitHistory
		copy(d.commits, d.commits[drop:])
		d.commits = d.commits[:d.cfg.CommitHistory]
	}
}

// BindContext binds the decoder to a peer connection and tenant. The first
// binding establishes a fresh compression context. A peer change also starts
// fresh. A tenant change on the same peer is only accepted after a
// synchronized SETTINGS record for the target peer/tenant/generation exists;
// the dynamic table is then reset.
func (d *Decoder) BindContext(ctx SecurityContext) error {
	d.init()
	if d.closed {
		return d.ConnectionError()
	}
	if d.active != nil {
		return connErr(CodeProtocol, "cannot change security context while a header block is open", ErrFrameOrder)
	}
	if !d.bound {
		d.ctx = ctx
		d.bound = true
		d.table = NewDynamicTable(d.tableSizeForContext(ctx))
		return nil
	}
	if ctx.PeerConnectionID != d.ctx.PeerConnectionID {
		d.ctx = ctx
		d.table = NewDynamicTable(d.tableSizeForContext(ctx))
		d.streams = make(map[uint32]*streamInfo)
		d.maxStreamID = 0
		return nil
	}
	if ctx.TenantID == d.ctx.TenantID {
		if ctx.SettingsGeneration == 0 {
			ctx.SettingsGeneration = d.ctx.SettingsGeneration
		}
		if ctx.SettingsGeneration < d.ctx.SettingsGeneration {
			return connErr(CodeProtocol, "settings generation moved backwards", ErrSettingsNotSynchronized)
		}
		if ctx.SettingsGeneration > d.ctx.SettingsGeneration {
			if rec, ok := d.settingsRecords[settingsKeyFor(ctx)]; ok && rec.Synchronized {
				d.applyTableSize(rec)
			}
		}
		d.ctx = ctx
		return nil
	}
	// Same underlying peer connection, different tenant.
	rec, ok := d.settingsRecords[settingsKeyFor(ctx)]
	if !ok || !rec.Synchronized {
		return connErr(CodeProtocol, "tenant switch requires synchronized SETTINGS for the target tenant", ErrSettingsNotSynchronized)
	}
	d.ctx = ctx
	d.table = NewDynamicTable(d.tableSizeForContext(ctx))
	d.streams = make(map[uint32]*streamInfo)
	return nil
}

// BindTenant is a convenience wrapper around BindContext.
func (d *Decoder) BindTenant(peerConnectionID, tenantID string, settingsGeneration uint64) error {
	return d.BindContext(SecurityContext{
		PeerConnectionID:   peerConnectionID,
		TenantID:           tenantID,
		SettingsGeneration: settingsGeneration,
	})
}

// SwitchTenant is a convenience wrapper around BindContext.
func (d *Decoder) SwitchTenant(ctx SecurityContext) error { return d.BindContext(ctx) }

// ApplySettings records a SETTINGS observation. A record marked
// Synchronized can authorize a tenant switch. For the current tenant, the
// HEADER_TABLE_SIZE setting is applied immediately (the dynamic table may be
// evicted, but is not reset).
func (d *Decoder) ApplySettings(s SettingsSync) error {
	d.init()
	if d.closed {
		return d.ConnectionError()
	}
	s.TenantID = strings.TrimSpace(s.TenantID)
	key := settingsKey{peer: s.PeerConnectionID, tenant: s.TenantID, gen: s.Generation}
	if s.Synchronized {
		if old, ok := d.settingsRecords[key]; ok && old.Synchronized && old.Generation > s.Generation {
			return connErr(CodeProtocol, "settings generation moved backwards", ErrSettingsNotSynchronized)
		}
	}
	current := d.bound && s.PeerConnectionID == d.ctx.PeerConnectionID && s.TenantID == d.ctx.TenantID
	if current && s.Generation < d.ctx.SettingsGeneration {
		return connErr(CodeProtocol, "stale SETTINGS generation", ErrSettingsNotSynchronized)
	}
	if current && d.active != nil {
		return connErr(CodeProtocol, "SETTINGS cannot be applied while a header block is open", ErrFrameOrder)
	}
	d.settingsRecords[key] = s
	if !current {
		return nil
	}
	if s.HasHeaderTableSize {
		d.applyTableSize(s)
	}
	if s.Generation >= d.ctx.SettingsGeneration {
		d.ctx.SettingsGeneration = s.Generation
	}
	return nil
}

// SynchronizeSettings is an alias for ApplySettings.
func (d *Decoder) SynchronizeSettings(s SettingsSync) error { return d.ApplySettings(s) }

func (d *Decoder) applyTableSize(s SettingsSync) {
	if !s.HasHeaderTableSize {
		return
	}
	size := s.HeaderTableSize
	if d.cfg.MaxDynamicTableSize != 0 && size > d.cfg.MaxDynamicTableSize {
		size = d.cfg.MaxDynamicTableSize
	}
	d.table.SetAllowedMaxSize(size)
}

func (d *Decoder) tableSizeForContext(ctx SecurityContext) uint32 {
	rec, ok := d.settingsRecords[settingsKeyFor(ctx)]
	if ok && rec.HasHeaderTableSize {
		size := rec.HeaderTableSize
		if size > d.cfg.MaxDynamicTableSize {
			size = d.cfg.MaxDynamicTableSize
		}
		return size
	}
	return d.cfg.MaxDynamicTableSize
}

// SettingsRecord returns the recorded SETTINGS state for ctx.
func (d *Decoder) SettingsRecord(ctx SecurityContext) (SettingsSync, bool) {
	s, ok := d.settingsRecords[settingsKeyFor(ctx)]
	return s, ok
}

func (d *Decoder) ensureBound() {
	if !d.bound {
		d.bound = true
	}
}

func (d *Decoder) stream(id uint32) *streamInfo {
	s, ok := d.streams[id]
	if !ok {
		s = &streamInfo{state: StreamIdle}
		d.streams[id] = s
	}
	return s
}

func (d *Decoder) connectionError(err error, reason string) error {
	var ce *ConnectionError
	if existing, ok := AsConnectionError(err); ok {
		ce = existing
	} else {
		ce = connErr(CodeProtocol, reason, err)
	}
	d.closed = true
	d.connErr = ce
	d.active = nil
	return ce
}

func (d *Decoder) failConnection(code ErrorCode, reason string, err error) error {
	return d.connectionError(connErr(code, reason, err), reason)
}

// ProcessFrameForContext processes a frame under an explicit security context.
// HEADERS frames bind the context; CONTINUATION frames must match the context
// of the open block.
func (d *Decoder) ProcessFrameForContext(f Frame, ctx SecurityContext) (Result, error) {
	f.StreamID &= 0x7fffffff
	if d.closed {
		return Result{StreamID: f.StreamID}, d.ConnectionError()
	}
	if d.active != nil {
		if ctx.SettingsGeneration == 0 &&
			ctx.PeerConnectionID == d.active.context.PeerConnectionID &&
			ctx.TenantID == d.active.context.TenantID {
			ctx.SettingsGeneration = d.active.context.SettingsGeneration
		}
		if !d.active.context.equal(ctx) {
			return Result{StreamID: f.StreamID}, d.connectionError(ErrTenantIsolation, "frame context does not match the open header block")
		}
		return d.ProcessFrame(f)
	}
	if !d.bound || !d.ctx.equal(ctx) {
		if err := d.BindContext(ctx); err != nil {
			return Result{StreamID: f.StreamID}, err
		}
	}
	return d.ProcessFrame(f)
}

// ProcessFrame processes one HTTP/2 frame and returns decoded fields only
// when a header block completes successfully.
func (d *Decoder) ProcessFrame(f Frame) (Result, error) {
	d.init()
	f.StreamID &= 0x7fffffff
	if d.closed {
		return Result{StreamID: f.StreamID}, d.ConnectionError()
	}
	d.ensureBound()
	if d.active != nil {
		switch f.Type {
		case FrameContinuation:
			return d.processContinuation(f)
		case FrameRSTStream:
			if f.StreamID != d.active.streamID {
				return Result{StreamID: f.StreamID}, d.connectionError(ErrFrameOrder, "RST_STREAM for another stream while a header block is open")
			}
			return d.processRSTStream(f)
		default:
			return Result{StreamID: f.StreamID}, d.connectionError(ErrFrameOrder, fmt.Sprintf("%s frame interleaved inside a header block", f.Type))
		}
	}

	switch f.Type {
	case FrameHeaders:
		return d.processHeaders(f)
	case FrameContinuation:
		return Result{StreamID: f.StreamID}, d.connectionError(ErrUnexpectedContinuation, "CONTINUATION without an active HEADERS block")
	case FrameRSTStream:
		return d.processRSTStream(f)
	case FrameData:
		return d.processData(f)
	case FrameSettings:
		if err := d.processSettings(f); err != nil {
			return Result{}, err
		}
		return Result{}, nil
	case FrameGoAway:
		d.closed = true
		d.connErr = connErr(CodeNoError, "GOAWAY received", ErrConnectionClosed)
		d.active = nil
		return Result{}, nil
	case FramePushPromise:
		return Result{StreamID: f.StreamID}, d.failConnection(CodeProtocol, "PUSH_PROMISE carries an untracked header block", ErrFrameOrder)
	default:
		return Result{StreamID: f.StreamID}, nil
	}
}

func (d *Decoder) processData(f Frame) (Result, error) {
	if f.StreamID == 0 {
		return Result{}, d.failConnection(CodeProtocol, "DATA on stream 0", ErrFrameOrder)
	}
	st := d.stream(f.StreamID)
	switch st.state {
	case StreamIdle:
		return Result{StreamID: f.StreamID}, d.failConnection(CodeProtocol, "DATA on an idle stream", ErrFrameOrder)
	case StreamClosed, StreamHalfClosedRemote:
		return Result{StreamID: f.StreamID}, streamErr(f.StreamID, CodeStreamClosed, "DATA on a closed or half-closed(remote) stream", ErrStreamClosed)
	}
	if f.Flags.Has(FlagEndStream) {
		if st.state == StreamHalfClosedLocal {
			st.state = StreamClosed
		} else {
			st.state = StreamHalfClosedRemote
		}
	}
	return Result{StreamID: f.StreamID, EndStream: f.Flags.Has(FlagEndStream)}, nil
}

// Feed is an alias for ProcessFrame.
func (d *Decoder) Feed(f Frame) (Result, error) { return d.ProcessFrame(f) }

func (d *Decoder) processHeaders(f Frame) (Result, error) {
	if f.StreamID == 0 {
		return Result{}, d.failConnection(CodeProtocol, "HEADERS on stream 0", ErrFrameOrder)
	}
	st := d.stream(f.StreamID)
	switch st.state {
	case StreamClosed:
		return Result{StreamID: f.StreamID}, streamErr(f.StreamID, CodeStreamClosed, "HEADERS on closed stream", ErrStreamClosed)
	case StreamHalfClosedRemote:
		return Result{StreamID: f.StreamID}, streamErr(f.StreamID, CodeStreamClosed, "HEADERS after END_STREAM", ErrStreamClosed)
	case StreamIdle:
		if f.StreamID <= d.maxStreamID {
			st.state = StreamClosed
			return Result{StreamID: f.StreamID}, streamErr(f.StreamID, CodeStreamClosed, "stream identifier was already used", ErrStreamClosed)
		}
		d.maxStreamID = f.StreamID
	}

	fragment, err := f.HeaderBlockFragment()
	if err != nil {
		d.closed = true
		if ce, ok := AsConnectionError(err); ok {
			d.connErr = ce
		}
		return Result{}, err
	}
	d.nextBlockID++
	trailers := st.headers > 0
	active := &activeBlock{
		blockID:   d.nextBlockID,
		streamID:  f.StreamID,
		endStream: f.Flags.Has(FlagEndStream),
		context:   d.ctx,
		trailers:  trailers,
	}
	st.headers++
	if st.state == StreamIdle {
		st.state = StreamOpen
	}
	if active.endStream {
		st.state = StreamHalfClosedRemote
	}

	if err := d.addFragment(active, fragment); err != nil {
		if !f.Flags.Has(FlagEndHeaders) {
			active.discard = true
			active.pendingErr = err
			d.active = active
			d.streams[f.StreamID].state = StreamClosed
			return Result{StreamID: f.StreamID, Pending: true, Discarded: true, CompressedBytes: active.compressed}, err
		}
		d.streams[f.StreamID].state = StreamClosed
		return Result{StreamID: f.StreamID, Discarded: true, CompressedBytes: active.compressed}, err
	}
	d.active = active
	if f.Flags.Has(FlagEndHeaders) {
		return d.finishBlock()
	}
	return Result{StreamID: f.StreamID, Pending: true, CompressedBytes: active.compressed}, nil
}

func (d *Decoder) addFragment(active *activeBlock, fragment []byte) error {
	active.compressed += len(fragment)
	if d.cfg.MaxCompressedBlockBytes > 0 && active.compressed > d.cfg.MaxCompressedBlockBytes {
		return streamErr(active.streamID, CodeEnhanceYourCalm, "compressed header block budget exceeded", ErrLimitExceeded)
	}
	active.fragments = append(active.fragments, append([]byte(nil), fragment...))
	return nil
}

func (d *Decoder) processContinuation(f Frame) (Result, error) {
	active := d.active
	if f.StreamID != active.streamID {
		return Result{StreamID: f.StreamID}, d.connectionError(ErrFrameOrder, "CONTINUATION stream does not match the open header block")
	}
	if !active.discard {
		if err := d.addFragment(active, f.Payload); err != nil {
			active.discard = true
			active.pendingErr = err
			active.fragments = nil
			d.streams[f.StreamID].state = StreamClosed
			result := Result{StreamID: f.StreamID, Discarded: true, Pending: !f.Flags.Has(FlagEndHeaders), CompressedBytes: active.compressed}
			if f.Flags.Has(FlagEndHeaders) {
				d.active = nil
				d.streams[f.StreamID].state = StreamClosed
				return result, err
			}
			return result, err
		}
	}
	if f.Flags.Has(FlagEndHeaders) {
		return d.finishBlock()
	}
	return Result{
		StreamID:        f.StreamID,
		Pending:         true,
		Canceled:        active.canceled,
		Discarded:       active.discard,
		CompressedBytes: active.compressed,
	}, active.pendingErr
}

func (d *Decoder) finishBlock() (Result, error) {
	active := d.active
	d.active = nil
	result := Result{
		StreamID:        active.streamID,
		EndStream:       active.endStream,
		EndHeaders:      true,
		Canceled:        active.canceled,
		Discarded:       active.discard,
		CompressedBytes: active.compressed,
	}
	if active.discard {
		d.streams[active.streamID].state = StreamClosed
		return result, active.pendingErr
	}
	fields, commit, err := d.decodeBlock(active.streamID, active.fragments, active.context, active.blockID, active.trailers)
	if err != nil {
		result.Discarded = true
		d.streams[active.streamID].state = StreamClosed
		if ce, ok := AsConnectionError(err); ok {
			d.closed = true
			d.connErr = ce
		}
		return result, err
	}
	result.Fields = fields
	result.Commit = commit
	if commit != nil {
		result.DecompressedBytes = commit.DecompressedBytes
	}
	return result, nil
}

func (d *Decoder) processRSTStream(f Frame) (Result, error) {
	if f.StreamID == 0 {
		return Result{}, d.failConnection(CodeProtocol, "RST_STREAM on stream 0", ErrFrameOrder)
	}
	result := Result{StreamID: f.StreamID, Canceled: true}
	if d.active != nil && d.active.streamID == f.StreamID {
		active := d.active
		active.canceled = true
		active.discard = true
		active.fragments = nil
		active.pendingErr = nil
		result.Pending = true
		result.Discarded = true
		result.CompressedBytes = active.compressed
		d.streams[f.StreamID].state = StreamClosed
		return result, nil
	}
	st := d.stream(f.StreamID)
	st.state = StreamClosed
	return result, nil
}

// CancelStream cancels any in-progress header block on streamID as if an
// RST_STREAM had arrived. All uncommitted table changes are discarded.
func (d *Decoder) CancelStream(streamID uint32) (Result, error) {
	d.init()
	streamID &= 0x7fffffff
	if d.active != nil && d.active.streamID != streamID {
		return Result{StreamID: streamID}, d.connectionError(ErrFrameOrder, "RST_STREAM for another stream while a header block is open")
	}
	return d.processRSTStream(Frame{Type: FrameRSTStream, StreamID: streamID})
}

func (d *Decoder) processSettings(f Frame) error {
	if f.StreamID != 0 {
		return d.failConnection(CodeProtocol, "SETTINGS on non-zero stream", ErrFrameOrder)
	}
	if f.Flags.Has(FlagAck) {
		if len(f.Payload) != 0 {
			return d.failConnection(CodeFrameSize, "SETTINGS ACK with payload", ErrFrameOrder)
		}
		rec, ok := d.settingsRecords[settingsKeyFor(d.ctx)]
		if !ok {
			rec = SettingsSync{
				PeerConnectionID: d.ctx.PeerConnectionID,
				TenantID:         d.ctx.TenantID,
				Generation:       d.ctx.SettingsGeneration,
			}
		}
		rec.Synchronized = true
		d.settingsRecords[settingsKeyFor(d.ctx)] = rec
		return nil
	}
	if len(f.Payload)%6 != 0 {
		return d.failConnection(CodeFrameSize, "SETTINGS payload is not a multiple of 6", ErrFrameOrder)
	}
	var size uint32
	var hasSize bool
	for off := 0; off < len(f.Payload); off += 6 {
		id := binary.BigEndian.Uint16(f.Payload[off : off+2])
		value := binary.BigEndian.Uint32(f.Payload[off+2 : off+6])
		if id == 0x1 {
			size = value
			hasSize = true
		}
	}
	generation := d.ctx.SettingsGeneration + 1
	if generation <= d.autoGeneration {
		generation = d.autoGeneration + 1
	}
	d.autoGeneration = generation
	d.ctx.SettingsGeneration = generation
	rec := SettingsSync{
		PeerConnectionID:   d.ctx.PeerConnectionID,
		TenantID:           d.ctx.TenantID,
		Generation:         d.autoGeneration,
		HeaderTableSize:    size,
		HasHeaderTableSize: hasSize,
		Synchronized:       false,
	}
	d.settingsRecords[settingsKeyFor(d.ctx)] = rec
	if hasSize {
		d.applyTableSize(rec)
	}
	return nil
}

// ApplyWireSettings applies SETTINGS_HEADER_TABLE_SIZE and synchronization
// state to the current context without consuming a frame. It is intended for
// gateways that negotiate settings out of band.
func (d *Decoder) ApplyWireSettings(generation uint64, headerTableSize uint32, hasSize, synchronized bool) error {
	return d.ApplySettings(SettingsSync{
		PeerConnectionID:   d.ctx.PeerConnectionID,
		TenantID:           d.ctx.TenantID,
		Generation:         generation,
		HeaderTableSize:    headerTableSize,
		HasHeaderTableSize: hasSize,
		Synchronized:       synchronized,
	})
}

// Reset discards any in-progress block and resets the HPACK dynamic table for
// the current security context. Committed audit records are retained.
func (d *Decoder) Reset() {
	d.init()
	d.active = nil
	d.closed = false
	d.connErr = nil
	d.streams = make(map[uint32]*streamInfo)
	d.table = NewDynamicTable(d.tableSizeForContext(d.ctx))
}

// ResetForContext resets the compression context and binds ctx. Tenant
// changes still require synchronized SETTINGS.
func (d *Decoder) ResetForContext(ctx SecurityContext) error {
	if err := d.BindContext(ctx); err != nil {
		return err
	}
	d.Reset()
	return nil
}

// ConnectionError returns the terminal connection error, if any.
func (d *Decoder) ConnectionError() error {
	if d.connErr == nil {
		return connErr(CodeProtocol, "connection is closed", ErrConnectionClosed)
	}
	return d.connErr
}

// Closed reports whether the decoder has observed a terminal connection error
// or GOAWAY.
func (d *Decoder) Closed() bool { return d.closed }

// Close marks the connection decoder closed. It does not publish any
// in-progress block.
func (d *Decoder) Close() {
	if d.closed {
		return
	}
	d.closed = true
	d.connErr = connErr(CodeNoError, "decoder closed", ErrConnectionClosed)
	d.active = nil
}

// StreamState returns the tracked state for a stream.
func (d *Decoder) StreamState(streamID uint32) StreamState {
	if s, ok := d.streams[streamID]; ok {
		return s.state
	}
	return StreamIdle
}
