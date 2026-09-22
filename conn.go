package framemux

// frameCharge is the per-frame byte overhead charged against buffer quotas.
const frameCharge = 32

// Config tunes a Conn. Zero values are replaced by defaults.
type Config struct {
	MaxPayload      int // maximum payload bytes per frame
	MaxStreamBuffer int // per-stream buffered-byte quota (independent)
	MaxTotalBuffer  int // global buffered-byte quota across all streams
	MaxResyncScan   int // delimiter scan budget before ErrNoDelimiter
	GraceFrames     int // old-epoch tail frames accepted after an epoch switch
}

func (c Config) withDefaults() Config {
	if c.MaxPayload <= 0 {
		c.MaxPayload = 1024
	}
	if c.MaxStreamBuffer <= 0 {
		c.MaxStreamBuffer = 4096
	}
	if c.MaxTotalBuffer <= 0 {
		c.MaxTotalBuffer = 16384
	}
	if c.MaxResyncScan <= 0 {
		c.MaxResyncScan = 65536
	}
	if c.GraceFrames <= 0 {
		c.GraceFrames = 8
	}
	return c
}

// Stats exposes parser and connection counters (used to verify bounded
// memory and bounded scanning).
type Stats struct {
	FramesDelivered int64
	Duplicates      int64 // already-acknowledged frames dropped, not redelivered
	BadFrames       int64
	Resyncs         int64
	ScanBytes       int64 // bytes examined while hunting / resyncing
	MaxRawBuffer    int   // high-water mark of the per-frame raw buffer
	EpochSwitches   int64
}

type stream struct {
	id          uint8
	sendSeq     uint8
	expectSeq   uint8
	hasExpect   bool
	paused      bool
	hold        []Frame // frames received while paused (charged to quotas)
	holdBytes   int
	queuedBytes int // bytes of this stream currently sitting in ready
}

// Conn multiplexes logical streams over one byte channel. Write feeds
// received bytes (arbitrarily chunked); Recv drains completed frames;
// Send/SendAck/ProposeEpoch produce outbound bytes drained with TakeOutput.
type Conn struct {
	cfg     Config
	p       *parser
	epoch   uint8
	streams map[uint8]*stream
	ready   []Frame // dispatched frames awaiting Recv, in arrival order
	total   int     // bytes currently charged against the global quota
	out     []byte
	stats   Stats
	lastErr error
}

// NewConn creates a connection at escape epoch 0.
func NewConn(cfg Config) *Conn {
	c := &Conn{cfg: cfg.withDefaults(), streams: map[uint8]*stream{}}
	c.p = &parser{
		cfg:        configForEpoch(0),
		maxPayload: c.cfg.MaxPayload,
		maxRaw:     2*(headerLen+c.cfg.MaxPayload+crcLen) + 16,
		maxScan:    c.cfg.MaxResyncScan,
		stats:      &c.stats,
	}
	c.p.budget = func() int { return c.cfg.MaxTotalBuffer - c.total }
	c.p.deliver = c.dispatch
	c.p.onBad = func(err error) {
		if c.lastErr == nil {
			c.lastErr = err
		}
	}
	return c
}

func chargeOf(f Frame) int { return len(f.Payload) + frameCharge }

func (c *Conn) streamFor(id uint8) *stream {
	s, ok := c.streams[id]
	if !ok {
		s = &stream{id: id}
		c.streams[id] = s
	}
	return s
}

// Write feeds inbound bytes. It returns ErrBackpressure when quotas are
// exhausted (drain with Recv, then retry) and a wrapped ErrBadData when
// corrupt bytes were met (the parser has already resynchronized; other
// streams are unaffected).
func (c *Conn) Write(buf []byte) (int, error) {
	c.p.retryStall()
	if c.p.state == stStalled || c.p.state == stWaitBudget {
		return 0, ErrBackpressure
	}
	c.lastErr = nil
	c.p.feed(buf)
	err := c.lastErr
	if c.p.state == stStalled || c.p.state == stWaitBudget {
		if err == nil {
			err = ErrBackpressure
		}
	}
	return len(buf), err
}

// Recv returns the next completed frame, if any.
func (c *Conn) Recv() (Frame, bool) {
	c.p.retryStall()
	if len(c.ready) == 0 {
		return Frame{}, false
	}
	f := c.ready[0]
	c.ready = c.ready[1:]
	ch := chargeOf(f)
	c.total -= ch
	if f.Type == TypeData {
		if s, ok := c.streams[f.Stream]; ok {
			s.queuedBytes -= ch
		}
	}
	c.p.retryStall()
	return f, true
}

// Pause stops delivery of frames for one stream; they are held (within the
// stream's quota) until Resume. Parser escape state and negotiation state
// are preserved across Pause/Resume.
func (c *Conn) Pause(streamID uint8) { c.streamFor(streamID).paused = true }

// Resume releases frames held while paused.
func (c *Conn) Resume(streamID uint8) {
	s := c.streamFor(streamID)
	s.paused = false
	if len(s.hold) > 0 {
		c.ready = append(c.ready, s.hold...)
		s.queuedBytes += s.holdBytes
		s.hold = nil
		s.holdBytes = 0
	}
	c.p.retryStall()
}

// Send queues a data frame on a stream.
func (c *Conn) Send(streamID uint8, payload []byte) error {
	if len(payload) > c.cfg.MaxPayload {
		return ErrTooLarge
	}
	s := c.streamFor(streamID)
	f := Frame{Type: TypeData, Stream: streamID, Seq: s.sendSeq, Epoch: c.epoch, Payload: payload}
	s.sendSeq++
	c.out = AppendFrame(c.out, f)
	return nil
}

// SendAck queues a cumulative acknowledgement for a stream.
func (c *Conn) SendAck(streamID uint8, seq uint8) {
	c.out = AppendFrame(c.out, Frame{Type: TypeAck, Stream: streamID, Seq: seq, Epoch: c.epoch})
}

// ProposeEpoch asks the peer to switch escape rules to epoch. The switch
// takes effect only at a frame boundary, after the peer confirms; old-epoch
// tail frames are still accepted during a bounded grace window.
func (c *Conn) ProposeEpoch(epoch uint8) {
	c.out = AppendFrame(c.out, Frame{Type: TypePropose, Epoch: c.epoch, Payload: []byte{epoch}})
}

// TakeOutput drains queued outbound bytes.
func (c *Conn) TakeOutput() []byte {
	out := c.out
	c.out = nil
	return out
}

// Epoch returns the current escape epoch.
func (c *Conn) Epoch() uint8 { return c.epoch }

// Stats returns a snapshot of the connection counters.
func (c *Conn) Stats() Stats { return c.stats }

// BufferedBytes returns the bytes currently charged to the global quota.
func (c *Conn) BufferedBytes() int { return c.total }

// dispatch delivers a parsed frame. Returning false applies backpressure:
// the parser stalls (state preserved) until Recv/Resume frees quota.
func (c *Conn) dispatch(f Frame) bool {
	switch f.Type {
	case TypePropose:
		c.handlePropose(f)
		return true
	case TypeConfirm:
		c.handleConfirm(f)
		return true
	case TypeAck:
		ch := chargeOf(f)
		if c.total+ch > c.cfg.MaxTotalBuffer {
			return false
		}
		c.ready = append(c.ready, f)
		c.total += ch
		return true
	case TypeData:
		s := c.streamFor(f.Stream)
		if s.hasExpect {
			if d := f.Seq - s.expectSeq; d > 128 {
				// Already delivered (e.g. retransmitted after a
				// recovery): drop, never deliver twice.
				c.stats.Duplicates++
				return true
			}
		}
		ch := chargeOf(f)
		if s.queuedBytes+s.holdBytes+ch > c.cfg.MaxStreamBuffer ||
			c.total+ch > c.cfg.MaxTotalBuffer {
			return false
		}
		s.hasExpect = true
		s.expectSeq = f.Seq + 1
		if s.paused {
			s.hold = append(s.hold, f)
			s.holdBytes += ch
		} else {
			c.ready = append(c.ready, f)
			s.queuedBytes += ch
		}
		c.total += ch
		c.stats.FramesDelivered++
		return true
	}
	return true
}

func (c *Conn) handlePropose(f Frame) {
	if len(f.Payload) != 1 {
		return
	}
	e := f.Payload[0]
	// Confirm with the *current* epoch encoding, then switch at this frame
	// boundary. Old-epoch tail frames already in flight are accepted via
	// the grace window.
	c.out = AppendFrame(c.out, Frame{Type: TypeConfirm, Epoch: c.epoch, Payload: []byte{e}})
	c.switchEpoch(e)
}

func (c *Conn) handleConfirm(f Frame) {
	if len(f.Payload) != 1 {
		return
	}
	if e := f.Payload[0]; e != c.epoch {
		c.switchEpoch(e)
	}
}

func (c *Conn) switchEpoch(e uint8) {
	c.p.prevEpoch = c.p.epoch
	c.p.epoch = e
	c.p.cfg = configForEpoch(e)
	c.p.grace = c.cfg.GraceFrames
	c.p.seenNew = false
	c.epoch = e
	c.stats.EpochSwitches++
}
