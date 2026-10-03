package roaring

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math/rand"
	"sync"
	"time"
)

var (
	errConnClosed = errors.New("roaring: connection closed")
	errTimeout    = errors.New("roaring: receive timeout")
)

// PacketConn is a bidirectional packet channel.
type PacketConn interface {
	Send(p []byte) error
	Recv() ([]byte, error)
	RecvAfter(d time.Duration) ([]byte, error)
	Close() error
}

type connState struct {
	done chan struct{}
	once sync.Once
}

type connEndpoint struct {
	in    chan []byte
	out   chan<- []byte
	state *connState
}

// ConnPair returns two connected in-memory endpoints.
func ConnPair(buffer int) (a, b PacketConn) {
	a2b := make(chan []byte, buffer)
	b2a := make(chan []byte, buffer)
	st := &connState{done: make(chan struct{})}
	return &connEndpoint{in: b2a, out: a2b, state: st},
		&connEndpoint{in: a2b, out: b2a, state: st}
}

func (e *connEndpoint) Send(p []byte) error {
	select {
	case e.out <- p:
		return nil
	case <-e.state.done:
		return errConnClosed
	}
}

func (e *connEndpoint) Recv() ([]byte, error) {
	select {
	case p := <-e.in:
		return p, nil
	case <-e.state.done:
		return nil, errConnClosed
	}
}

func (e *connEndpoint) RecvAfter(d time.Duration) ([]byte, error) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case p := <-e.in:
		return p, nil
	case <-e.state.done:
		return nil, errConnClosed
	case <-t.C:
		return nil, errTimeout
	}
}

func (e *connEndpoint) Close() error {
	e.state.once.Do(func() { close(e.state.done) })
	return nil
}

// LossyConn wraps a PacketConn and randomly drops outgoing packets with the
// given probability, simulating an unreliable network.
type LossyConn struct {
	Inner    PacketConn
	DropRate float64
	Seed     int64

	mu        sync.Mutex
	rnd       *rand.Rand
	Dropped   int
	Delivered int
}

func (c *LossyConn) Send(p []byte) error {
	c.mu.Lock()
	if c.rnd == nil {
		c.rnd = rand.New(rand.NewSource(c.Seed))
	}
	drop := c.rnd.Float64() < c.DropRate
	if drop {
		c.Dropped++
	} else {
		c.Delivered++
	}
	c.mu.Unlock()
	if drop {
		return nil // silently lost on the wire
	}
	return c.Inner.Send(p)
}

func (c *LossyConn) Recv() ([]byte, error) { return c.Inner.Recv() }

func (c *LossyConn) RecvAfter(d time.Duration) ([]byte, error) {
	return c.Inner.RecvAfter(d)
}

func (c *LossyConn) Close() error { return c.Inner.Close() }

// Replication protocol: stop-and-wait ARQ with CRC-protected packets,
// sequence numbers, acknowledgements and retransmission. Duplicates caused
// by lost ACKs are detected and dropped by the receiver.
const (
	pktData byte = 1
	pktAck  byte = 2
)

func encodePacket(typ byte, seq uint32, payload []byte) []byte {
	w := &byteWriter{}
	w.u8(typ)
	w.u32(seq)
	w.u32(crc32.ChecksumIEEE(payload))
	w.bytes(payload)
	return w.buf
}

func decodePacket(p []byte) (typ byte, seq uint32, payload []byte, ok bool) {
	if len(p) < 9 {
		return 0, 0, nil, false
	}
	payload = p[9:]
	if crc32.ChecksumIEEE(payload) != binary.LittleEndian.Uint32(p[5:9]) {
		return 0, 0, nil, false
	}
	return p[0], binary.LittleEndian.Uint32(p[1:5]), payload, true
}

// Sender reliably transmits records over a lossy connection.
type Sender struct {
	Conn    PacketConn
	Timeout time.Duration
	seq     uint32
}

// Send blocks until the record is acknowledged, retransmitting as needed.
func (s *Sender) Send(record []byte) error {
	s.seq++
	pkt := encodePacket(pktData, s.seq, record)
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Millisecond
	}
	for {
		if err := s.Conn.Send(pkt); err != nil {
			return err
		}
		ack, err := s.Conn.RecvAfter(timeout)
		if errors.Is(err, errTimeout) {
			continue // packet or ACK lost: retransmit
		}
		if err != nil {
			return err
		}
		typ, seq, _, ok := decodePacket(ack)
		if ok && typ == pktAck && seq == s.seq {
			return nil
		}
	}
}

// Receiver applies records in order, acknowledging each. Out-of-order
// packets are dropped (the sender will retransmit); duplicates are
// re-acknowledged but not re-applied.
type Receiver struct {
	Conn    PacketConn
	Apply   func(record []byte)
	lastSeq uint32
}

// Run processes packets until the connection is closed; it returns
// errConnClosed on a clean shutdown.
func (r *Receiver) Run() error {
	for {
		pkt, err := r.Conn.Recv()
		if err != nil {
			return err
		}
		typ, seq, payload, ok := decodePacket(pkt)
		if !ok || typ != pktData {
			continue
		}
		if seq == r.lastSeq+1 {
			if r.Apply != nil {
				r.Apply(payload)
			}
			r.lastSeq = seq
		}
		if seq <= r.lastSeq {
			if err := r.Conn.Send(encodePacket(pktAck, seq, nil)); err != nil {
				return err
			}
		}
	}
}
