package roaring

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"time"
)

// chunkSize is the payload size of one transfer packet.
const chunkSize = 1024

// Packet is one unit of the bitmap transfer protocol. Data packets carry a
// sequence number and a per-chunk CRC; the header (IsHeader) announces the
// chunk count and the CRC of the whole image; ACK packets carry a bitmask
// of received chunks.
type Packet struct {
	Ack      bool
	IsHeader bool
	Seq      uint32
	Total    uint32
	Payload  []byte
	Mask     []byte
	Crc      uint32
}

// Transport is a bidirectional packet channel. Recv must return ErrTimeout
// when no packet arrives within its receive window.
type Transport interface {
	Send(Packet) error
	TrySend(Packet)
	Recv() (Packet, error)
}

// SendBitmap transmits b over t with selective retransmission: chunks that
// are not acknowledged (lost or corrupted in flight) are resent until the
// receiver acknowledges every chunk.
func SendBitmap(b *Bitmap, t Transport) error {
	data := b.Marshal()
	n := (len(data) + chunkSize - 1) / chunkSize
	totalCRC := crc32.ChecksumIEEE(data)
	hdr := make([]byte, 4)
	binary.LittleEndian.PutUint32(hdr, totalCRC)
	acked := make([]bool, n)
	remaining := n
	maskLen := (n + 7) / 8
	for rounds := 0; remaining > 0; rounds++ {
		if rounds > 100000 {
			return errors.New("roaring: too many retransmissions")
		}
		if err := t.Send(Packet{IsHeader: true, Total: uint32(n), Payload: hdr, Crc: crc32.ChecksumIEEE(hdr)}); err != nil {
			return err
		}
		for i := 0; i < n; i++ {
			if acked[i] {
				continue
			}
			lo := i * chunkSize
			hi := lo + chunkSize
			if hi > len(data) {
				hi = len(data)
			}
			payload := data[lo:hi]
			if err := t.Send(Packet{Seq: uint32(i), Payload: payload, Crc: crc32.ChecksumIEEE(payload)}); err != nil {
				return err
			}
		}
		for {
			p, err := t.Recv()
			if err == ErrTimeout {
				break
			}
			if err != nil {
				return err
			}
			if !p.Ack || len(p.Mask) != maskLen {
				continue
			}
			for i := 0; i < n; i++ {
				if !acked[i] && p.Mask[i/8]&(1<<uint(i%8)) != 0 {
					acked[i] = true
					remaining--
				}
			}
		}
	}
	return nil
}

// ReceiveBitmap receives a bitmap sent with SendBitmap. Corrupted chunks
// (bad CRC) are dropped and retransmitted by the sender; the assembled
// image is verified against the whole-image CRC before being decoded.
func ReceiveBitmap(t Transport) (*Bitmap, error) {
	var (
		chunks   [][]byte
		got      []bool
		total    uint32
		totalCRC uint32
		haveHdr  bool
		left     int
	)
	stall := time.Now().Add(10 * time.Second)
	sendAck := func() {
		if !haveHdr {
			return
		}
		mask := make([]byte, (total+7)/8)
		for i, g := range got {
			if g {
				mask[i/8] |= 1 << uint(i%8)
			}
		}
		t.TrySend(Packet{Ack: true, Mask: mask})
	}
	for {
		p, err := t.Recv()
		if err == ErrTimeout {
			if time.Now().After(stall) {
				return nil, errors.New("roaring: receive stalled")
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		stall = time.Now().Add(10 * time.Second)
		if p.Ack {
			continue
		}
		if crc32.ChecksumIEEE(p.Payload) != p.Crc {
			continue // corrupted in flight: drop, sender will retransmit
		}
		if p.IsHeader {
			if len(p.Payload) != 4 {
				continue
			}
			if haveHdr {
				// Duplicate header from a retransmission round: keep
				// the chunks already received, just re-ACK.
				if p.Total != total || binary.LittleEndian.Uint32(p.Payload) != totalCRC {
					continue
				}
				sendAck()
				continue
			}
			total = p.Total
			totalCRC = binary.LittleEndian.Uint32(p.Payload)
			chunks = make([][]byte, total)
			got = make([]bool, total)
			left = int(total)
			haveHdr = true
			sendAck()
			continue
		}
		if !haveHdr || p.Seq >= total {
			continue
		}
		if !got[p.Seq] {
			got[p.Seq] = true
			chunks[p.Seq] = p.Payload
			left--
		}
		sendAck()
		if left == 0 {
			data := bytes.Join(chunks, nil)
			if crc32.ChecksumIEEE(data) != totalCRC {
				return nil, ErrCorrupt
			}
			// Grace window: keep answering duplicate retransmissions so
			// the sender can observe the final ACK even if it was lost.
			deadline := time.Now().Add(30 * time.Millisecond)
			for time.Now().Before(deadline) {
				if p, err := t.Recv(); err == nil && !p.Ack {
					sendAck()
				}
			}
			return Unmarshal(data)
		}
	}
}

// LossyLink is an in-memory Transport pair that drops and corrupts packets
// according to a FaultInjector, emulating an unreliable network.
type LossyLink struct {
	ab chan Packet
	ba chan Packet
	fi *FaultInjector
}

// NewLossyLink creates a lossy in-memory link. A nil injector means a
// perfect link.
func NewLossyLink(fi *FaultInjector) *LossyLink {
	if fi == nil {
		fi = NewFaultInjector()
	}
	return &LossyLink{
		ab: make(chan Packet, 256),
		ba: make(chan Packet, 256),
		fi: fi,
	}
}

// Ends returns the two transport endpoints of the link.
func (l *LossyLink) Ends() (Transport, Transport) {
	return &endpoint{out: l.ab, in: l.ba, fi: l.fi},
		&endpoint{out: l.ba, in: l.ab, fi: l.fi}
}

type endpoint struct {
	out chan Packet
	in  <-chan Packet
	fi  *FaultInjector
}

func (e *endpoint) Send(p Packet) error {
	if e.fi.shouldDrop() {
		return nil // packet lost on the wire
	}
	if e.fi.shouldCorrupt() && len(p.Payload) > 0 {
		p.Payload = append([]byte(nil), p.Payload...)
		p.Payload[0] ^= 0xFF
	}
	select {
	case e.out <- p:
		return nil
	case <-time.After(5 * time.Second):
		return ErrTimeout
	}
}

// TrySend delivers p best-effort: it never blocks and may drop the packet
// when the peer is busy. Used for ACKs, whose loss is tolerated.
func (e *endpoint) TrySend(p Packet) {
	select {
	case e.out <- p:
	default:
	}
}

func (e *endpoint) Recv() (Packet, error) {
	select {
	case p := <-e.in:
		return p, nil
	case <-time.After(2 * time.Millisecond):
		return Packet{}, ErrTimeout
	}
}
