// Package netsim verifies fault tolerance against network packet loss and
// corruption. It provides a deterministic lossy link plus a stop-and-wait
// ARQ session that retransmits until every frame is acknowledged intact.
package netsim

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math/rand"
	"sync"
	"time"
)

// Frame layout on the wire: [seq:4][crc32:4][payload]
const headerSize = 8

var (
	// ErrClosed is returned when sending on a closed session.
	ErrClosed = errors.New("netsim: session closed")
	// ErrCorruptFrame is reported to the corruption hook.
	ErrCorruptFrame = errors.New("netsim: frame failed CRC")
)

// Link simulates an unreliable medium. It is deterministic when seeded,
// which keeps fault-injection tests reproducible. Safe for concurrent use.
type Link struct {
	mu          sync.Mutex
	rng         *rand.Rand
	DropRate    float64 // probability a frame is lost entirely
	CorruptRate float64 // probability a frame arrives bit-flipped
	// Counters for assertions.
	Sent      int
	Dropped   int
	Corrupted int
}

// NewLink returns a lossy link with the given seed and fault rates.
func NewLink(seed int64, dropRate, corruptRate float64) *Link {
	return &Link{rng: rand.New(rand.NewSource(seed)), DropRate: dropRate, CorruptRate: corruptRate}
}

// Transmit passes frame through the loss model. ok=false means the frame
// was dropped. Otherwise the returned frame may have been corrupted.
func (l *Link) Transmit(frame []byte) (out []byte, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Sent++
	if l.rng.Float64() < l.DropRate {
		l.Dropped++
		return nil, false
	}
	out = make([]byte, len(frame))
	copy(out, frame)
	if l.rng.Float64() < l.CorruptRate && len(out) > 0 {
		l.Corrupted++
		out[l.rng.Intn(len(out))] ^= 0xFF
	}
	return out, true
}

// Session is a stop-and-wait ARQ endpoint: frames carry a sequence number
// and CRC32; the receiver ACKs each intact frame; the sender retransmits
// on timeout until the ACK arrives. Exactly-once, in-order delivery.
type Session struct {
	sendLink *Link
	ackLink  *Link

	mu     sync.Mutex
	closed bool

	// receiver state
	expectSeq uint32
	deliver   chan []byte

	// sender state
	ackCh chan uint32

	done chan struct{}
	wg   sync.WaitGroup

	// RetransmitTimeout bounds how long the sender waits for an ACK.
	RetransmitTimeout time.Duration
}

// NewSession wires a session over two links (data and ACK paths). recvLoop
// and the retransmit timer are the only goroutines, both joined by Close.
func NewSession(dataLink, ackLink *Link) *Session {
	s := &Session{
		sendLink:          dataLink,
		ackLink:           ackLink,
		deliver:           make(chan []byte, 64),
		ackCh:             make(chan uint32, 64),
		done:              make(chan struct{}),
		RetransmitTimeout: 5 * time.Millisecond,
	}
	return s
}

// EncodeFrame builds a wire frame for seq+payload.
func EncodeFrame(seq uint32, payload []byte) []byte {
	f := make([]byte, headerSize+len(payload))
	binary.LittleEndian.PutUint32(f[0:4], seq)
	binary.LittleEndian.PutUint32(f[4:8], crc32.ChecksumIEEE(payload))
	copy(f[headerSize:], payload)
	return f
}

// DecodeFrame verifies the CRC and returns seq+payload.
func DecodeFrame(f []byte) (seq uint32, payload []byte, err error) {
	if len(f) < headerSize {
		return 0, nil, ErrCorruptFrame
	}
	seq = binary.LittleEndian.Uint32(f[0:4])
	crc := binary.LittleEndian.Uint32(f[4:8])
	payload = f[headerSize:]
	if crc32.ChecksumIEEE(payload) != crc {
		return 0, nil, ErrCorruptFrame
	}
	return seq, payload, nil
}

// Send delivers payload reliably, retransmitting through loss until the
// peer ACKs. It blocks until acknowledged or the session closes.
func (s *Session) Send(seq uint32, payload []byte) error {
	frame := EncodeFrame(seq, payload)
	for {
		select {
		case <-s.done:
			return ErrClosed
		default:
		}
		if out, ok := s.sendLink.Transmit(frame); ok {
			s.onWireFrame(out) // loopback to the receiver side of this session pair
		}
		select {
		case ack := <-s.ackCh:
			if ack == seq {
				return nil
			}
		case <-time.After(s.RetransmitTimeout):
			// timeout: retransmit
		case <-s.done:
			return ErrClosed
		}
	}
}

// onWireFrame is the receiver half: validate CRC, dedupe by sequence,
// deliver in order, and emit an ACK through the ack link.
func (s *Session) onWireFrame(frame []byte) {
	seq, payload, err := DecodeFrame(frame)
	if err != nil {
		return // corrupted frame: drop silently, sender will retransmit
	}
	s.mu.Lock()
	expect := s.expectSeq
	s.mu.Unlock()
	if seq != expect {
		// Duplicate or out of order: re-ACK so the sender can advance,
		// but do not deliver twice.
		s.sendAck(seq)
		return
	}
	s.mu.Lock()
	s.expectSeq++
	s.mu.Unlock()
	select {
	case s.deliver <- payload:
	case <-s.done:
	}
	s.sendAck(seq)
}

func (s *Session) sendAck(seq uint32) {
	ack := EncodeFrame(seq, []byte("ACK"))
	if out, ok := s.ackLink.Transmit(ack); ok {
		if aseq, _, err := DecodeFrame(out); err == nil {
			select {
			case s.ackCh <- aseq:
			case <-s.done:
			}
		}
	}
}

// Recv returns the next in-order payload, or ErrClosed.
func (s *Session) Recv() ([]byte, error) {
	select {
	case p := <-s.deliver:
		return p, nil
	case <-s.done:
		return nil, ErrClosed
	}
}

// Close shuts the session down and waits for all goroutines to exit.
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	s.closed = true
	s.mu.Unlock()
	close(s.done)
	s.wg.Wait()
	return nil
}
