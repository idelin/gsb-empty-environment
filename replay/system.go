package replay

// Packet is a buffered network packet.
type Packet struct {
	Seq  uint64
	Size uint64
	Sum  uint64
}

// NetSystem is the simulated component under debug: a tiny reliable
// transport receiver. All of its memory lives in plain Go values so the
// whole component can be serialized byte-deterministically.
type NetSystem struct {
	Received    []Packet // every packet that arrived, in arrival order
	Delivered   []uint64 // payload sums acked to the peer, in ack order
	Retransmits uint64   // timeout-driven retransmissions
	Lost        uint64   // acks dropped by the simulated link
	Bytes       uint64   // total payload bytes received
	Checksum    uint64   // running mix of all payload sums
}

func mix(a, b uint64) uint64 {
	a ^= b + 0x9E3779B97F4A7C15 + (a << 6) + (a >> 2)
	return a
}

func checksum(payload []byte) uint64 {
	var sum uint64
	for i, c := range payload {
		sum = mix(sum, uint64(c)+uint64(i))
	}
	return sum
}

// onPacket buffers an arrived packet and updates accounting.
func (s *NetSystem) onPacket(seq uint64, payload []byte) uint64 {
	sum := checksum(payload)
	s.Received = append(s.Received, Packet{Seq: seq, Size: uint64(len(payload)), Sum: sum})
	s.Bytes += uint64(len(payload))
	s.Checksum = mix(s.Checksum, sum)
	return sum
}

// ack marks a packet as acknowledged by the peer.
func (s *NetSystem) ack(seq uint64) {
	for _, p := range s.Received {
		if p.Seq == seq {
			s.Delivered = append(s.Delivered, p.Sum)
			return
		}
	}
}

// onTimeout handles a retransmission timer clock interrupt: the ack was
// lost, so the sender retransmits and the ack is regenerated.
func (s *NetSystem) onTimeout(seq uint64) {
	s.Retransmits++
	s.ack(seq)
}
