// Package nackrtx implements a retransmission buffer for the send path of a
// media server: it caches outgoing RTP packets, parses RTCP generic NACK
// feedback (PID + BLP), and retransmits lost packets as RTX (RFC 4588).
package nackrtx

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

const (
	rtpHeaderSize  = 12
	rtcpHeaderSize = 4

	rtcpPTNACK  = 205 // RTPFB
	rtcpFMTNACK = 1   // generic NACK
)

// ParseNACK parses one RTCP generic NACK packet (PT=205, FMT=1) and returns
// the sequence numbers reported lost. Each FCI entry carries a PID and a
// bitmask of following losses (BLP): bit i set means PID+1+i is also lost.
func ParseNACK(pkt []byte) ([]uint16, error) {
	if len(pkt) < rtcpHeaderSize+8 { // header + sender SSRC + media SSRC
		return nil, errors.New("nackrtx: RTCP packet too short")
	}
	if pkt[0]>>6 != 2 {
		return nil, errors.New("nackrtx: bad RTCP version")
	}
	if pkt[0]&0x1f != rtcpFMTNACK || pkt[1] != rtcpPTNACK {
		return nil, fmt.Errorf("nackrtx: not a generic NACK (fmt=%d pt=%d)", pkt[0]&0x1f, pkt[1])
	}
	length := int(binary.BigEndian.Uint16(pkt[2:4])) + 1 // in 32-bit words
	if length*4 > len(pkt) {
		return nil, errors.New("nackrtx: RTCP length exceeds packet size")
	}
	fci := pkt[12 : length*4]
	if len(fci)%4 != 0 {
		return nil, errors.New("nackrtx: malformed FCI")
	}
	var lost []uint16
	for i := 0; i+4 <= len(fci); i += 4 {
		pid := binary.BigEndian.Uint16(fci[i:])
		blp := binary.BigEndian.Uint16(fci[i+2:])
		lost = append(lost, pid)
		for bit := 0; bit < 16 && blp != 0; bit++ {
			if blp&(1<<(15-bit)) != 0 {
				lost = append(lost, pid+1+uint16(bit))
			}
		}
	}
	return lost, nil
}

// entry is one cached RTP packet.
type entry struct {
	seq       uint16
	timestamp uint32
	payload   []byte
	size      int // bytes accounted against the buffer limit
	storedAt  time.Time
	lastRTX   time.Time
	rtxSent   bool
}

// Buffer caches recently sent packets so they can be retransmitted on NACK.
// Entries are evicted when they are older than MaxAge or when the buffer
// exceeds MaxBytes (oldest first). A packet is retransmitted at most once
// per RTT.
type Buffer struct {
	MaxAge   time.Duration
	MaxBytes int
	RTT      time.Duration

	now     func() time.Time
	entries map[uint16]*entry
	order   []uint16 // insertion order, for FIFO eviction
	bytes   int
}

// NewBuffer creates a Buffer. maxAge and rtt must be > 0; maxBytes <= 0
// disables the byte limit.
func NewBuffer(maxAge time.Duration, maxBytes int, rtt time.Duration) *Buffer {
	return &Buffer{
		MaxAge:   maxAge,
		MaxBytes: maxBytes,
		RTT:      rtt,
		now:      time.Now,
		entries:  make(map[uint16]*entry),
	}
}

// store caches a sent packet. If the sequence number is already present
// (possible only after a 16-bit wrap with a large byte limit), the stale
// entry is evicted first so old and new generations never mix.
func (b *Buffer) store(seq uint16, timestamp uint32, payload []byte) {
	now := b.now()
	if _, dup := b.entries[seq]; dup {
		b.evictSeq(seq)
	}
	cp := make([]byte, len(payload))
	copy(cp, payload)
	e := &entry{
		seq:       seq,
		timestamp: timestamp,
		payload:   cp,
		size:      rtpHeaderSize + len(cp),
		storedAt:  now,
	}
	b.entries[seq] = e
	b.order = append(b.order, seq)
	b.bytes += e.size
	b.evict(now)
}

// evict drops expired entries and enforces the byte cap, oldest first.
func (b *Buffer) evict(now time.Time) {
	for len(b.order) > 0 {
		oldest := b.entries[b.order[0]]
		expired := now.Sub(oldest.storedAt) > b.MaxAge
		over := b.MaxBytes > 0 && b.bytes > b.MaxBytes
		if !expired && !over {
			return
		}
		b.evictSeq(b.order[0])
	}
}

func (b *Buffer) evictSeq(seq uint16) {
	e, ok := b.entries[seq]
	if !ok {
		return
	}
	delete(b.entries, seq)
	b.bytes -= e.size
	for i, s := range b.order {
		if s == seq {
			b.order = append(b.order[:i], b.order[i+1:]...)
			break
		}
	}
}

// get returns the cached packet for an original sequence number, or nil.
func (b *Buffer) get(seq uint16) *entry {
	b.evict(b.now())
	return b.entries[seq]
}

// Sender emits RTP packets on a main stream and retransmits them as RTX on a
// separate stream (own SSRC and sequence number space, per RFC 4588).
type Sender struct {
	SSRC           uint32
	RTXSSRC        uint32
	PayloadType    uint8
	RTXPayloadType uint8
	TimestampStep  uint32

	buf       *Buffer
	seq       uint16
	rtxSeq    uint16
	timestamp uint32
}

// NewSender creates a Sender whose sent packets are cached in buf.
func NewSender(buf *Buffer, ssrc, rtxSSRC uint32, pt, rtxPT uint8, tsStep uint32) *Sender {
	return &Sender{
		SSRC:           ssrc,
		RTXSSRC:        rtxSSRC,
		PayloadType:    pt,
		RTXPayloadType: rtxPT,
		TimestampStep:  tsStep,
		buf:            buf,
	}
}

// Send marshals an RTP packet carrying payload, caches it for possible
// retransmission, and returns the wire bytes.
func (s *Sender) Send(payload []byte) []byte {
	pkt := marshalRTP(s.PayloadType, s.seq, s.timestamp, s.SSRC, payload)
	s.buf.store(s.seq, s.timestamp, payload)
	s.seq++
	s.timestamp += s.TimestampStep
	return pkt
}

// HandleRTCP processes an incoming RTCP packet. For a generic NACK it
// returns the RTX packets to send, in NACK order; other feedback is ignored.
func (s *Sender) HandleRTCP(rtcp []byte) ([][]byte, error) {
	lost, err := ParseNACK(rtcp)
	if err != nil {
		return nil, err
	}
	now := s.buf.now()
	var out [][]byte
	for _, seq := range lost {
		e := s.buf.get(seq)
		if e == nil {
			continue // already evicted or never sent
		}
		if e.rtxSent && now.Sub(e.lastRTX) < s.buf.RTT {
			continue // already retransmitted within one RTT
		}
		e.rtxSent = true
		e.lastRTX = now
		out = append(out, s.marshalRTX(e))
	}
	return out, nil
}

// marshalRTX builds an RTX packet: RTX SSRC and sequence number, original
// timestamp, and the original sequence number prepended to the payload.
func (s *Sender) marshalRTX(e *entry) []byte {
	payload := make([]byte, 2+len(e.payload))
	binary.BigEndian.PutUint16(payload, e.seq)
	copy(payload[2:], e.payload)
	pkt := marshalRTP(s.RTXPayloadType, s.rtxSeq, e.timestamp, s.RTXSSRC, payload)
	s.rtxSeq++
	return pkt
}

func marshalRTP(pt uint8, seq uint16, ts uint32, ssrc uint32, payload []byte) []byte {
	pkt := make([]byte, rtpHeaderSize+len(payload))
	pkt[0] = 0x80 // V=2
	pkt[1] = pt & 0x7f
	binary.BigEndian.PutUint16(pkt[2:], seq)
	binary.BigEndian.PutUint32(pkt[4:], ts)
	binary.BigEndian.PutUint32(pkt[8:], ssrc)
	copy(pkt[rtpHeaderSize:], payload)
	return pkt
}
