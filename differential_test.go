package conntrack

import (
	"fmt"
	"math/rand"
	"net/netip"
	"testing"
	"time"
)

// genSocket describes one endpoint of a generated connection.
type genSocket struct {
	ip    netip.Addr
	port  uint16
	isn   uint32
	next  uint32 // next sequence this endpoint will send
	ack   uint32 // last ack number this endpoint advertises
	win   uint16 // raw window field it advertises
	ws    int    // window scale it sends in its SYN
	hasWS bool
}

type generator struct {
	rng *rand.Rand
	a   genSocket
	b   genSocket
}

func newGenerator(rng *rand.Rand, wrap bool) *generator {
	aISN := rng.Uint32()
	bISN := rng.Uint32()
	if wrap {
		// Put ISNs close to the 32-bit boundary so ordinary traffic wraps.
		aISN = uint32(0xFFFFFFFF - rng.Intn(2000))
		bISN = uint32(0xFFFFFFFF - rng.Intn(2000))
	}
	wsA, wsB := rng.Intn(15), rng.Intn(15)
	if rng.Intn(4) == 0 {
		wsA = 0
	}
	if rng.Intn(4) == 0 {
		wsB = 0
	}
	winA := uint16(2048 + rng.Intn(60000))
	winB := uint16(2048 + rng.Intn(60000))
	if rng.Intn(10) == 0 {
		winB = 0 // exercise the zero-window path
	}
	return &generator{
		rng: rng,
		a: genSocket{
			ip: netip.MustParseAddr("10.0.0.1"), port: 1111,
			isn: aISN, next: aISN + 1, ack: 0,
			win: winA, ws: wsA, hasWS: true,
		},
		b: genSocket{
			ip: netip.MustParseAddr("10.0.0.2"), port: 2222,
			isn: bISN, next: bISN + 1, ack: 0,
			win: winB, ws: wsB, hasWS: true,
		},
	}
}

// pkt builds a packet from src to dst with the given flags and payload size.
func (g *generator) pkt(src, dst *genSocket, flags uint8, payload int) Packet {
	p := Packet{
		SrcIP: src.ip, DstIP: dst.ip,
		SrcPort: src.port, DstPort: dst.port,
		Seq: src.next, Ack: src.ack,
		Flags:   flags,
		Window:  src.win,
		Payload: payload,
	}
	return p
}

func (g *generator) syn(src, dst *genSocket, seq uint32) Packet {
	return Packet{
		SrcIP: src.ip, DstIP: dst.ip, SrcPort: src.port, DstPort: dst.port,
		Seq: seq, Flags: FlagSYN, Window: src.win,
		WScale: src.ws, HasWScale: src.hasWS,
	}
}

func (g *generator) synAck(src, dst *genSocket, seq, ack uint32) Packet {
	return Packet{
		SrcIP: src.ip, DstIP: dst.ip, SrcPort: src.port, DstPort: dst.port,
		Seq: seq, Ack: ack, Flags: FlagSYN | FlagACK, Window: src.win,
		WScale: src.ws, HasWScale: src.hasWS,
	}
}

// sendData emits an in-order data segment and advances the sender's state.
func (g *generator) sendData(src, dst *genSocket, n int) Packet {
	p := g.pkt(src, dst, FlagACK|FlagPSH, n)
	src.next += uint32(n)
	src.ack = dst.next
	return p
}

func (g *generator) sendAck(src, dst *genSocket) Packet {
	src.ack = dst.next
	return g.pkt(src, dst, FlagACK, 0)
}

func (g *generator) sendFin(src, dst *genSocket) Packet {
	src.ack = dst.next
	p := g.pkt(src, dst, FlagFIN|FlagACK, 0)
	src.next++
	return p
}

// attack builds an adversarial segment whose sequence or ack is randomized,
// spanning in-window and wildly out-of-window values.
func (g *generator) attack(rng *rand.Rand, src, dst *genSocket, flags uint8) Packet {
	p := g.pkt(src, dst, flags, rng.Intn(3))
	switch rng.Intn(5) {
	case 0:
		p.Seq = dst.next // exact expected sequence
	case 1:
		p.Seq = dst.next + uint32(rng.Intn(int(dst.win)+1))
	case 2:
		p.Seq = dst.next - uint32(1+rng.Intn(1_000_000))
	case 3:
		p.Seq = rng.Uint32()
	}
	if flags&FlagACK != 0 {
		switch rng.Intn(4) {
		case 0:
			p.Ack = src.next
		case 1:
			p.Ack = src.next - uint32(1+rng.Intn(1_000_000))
		case 2:
			p.Ack = rng.Uint32()
		}
	}
	p.Window = uint16(rng.Intn(65536))
	return p
}

// trace runs one scenario: a plausible handshake/close interwoven with random
// attacker segments, and returns every packet in order.
func (g *generator) trace(rng *rand.Rand) []Packet {
	var pkts []Packet
	a, b := &g.a, &g.b

	mode := rng.Intn(3)
	switch mode {
	case 0: // standard three-way handshake
		pkts = append(pkts, g.syn(a, b, a.isn))
		if rng.Intn(3) == 0 {
			pkts = append(pkts, g.syn(a, b, a.isn)) // SYN retransmit
		}
		pkts = append(pkts, g.synAck(b, a, b.isn, a.isn+1))
		b.ack = a.isn + 1
		pkts = append(pkts, g.sendAck(a, b))
	case 1: // simultaneous open
		pkts = append(pkts, g.syn(a, b, a.isn))
		pkts = append(pkts, g.syn(b, a, b.isn))
		pkts = append(pkts, g.sendAck(a, b))
		pkts = append(pkts, g.sendAck(b, a))
	case 2: // handshake with loss/reorder emulated by duplicates
		pkts = append(pkts, g.syn(a, b, a.isn))
		pkts = append(pkts, g.synAck(b, a, b.isn, a.isn+1))
		b.ack = a.isn + 1
		pkts = append(pkts, g.synAck(b, a, b.isn, a.isn+1)) // retransmit
		pkts = append(pkts, g.sendAck(a, b))
	}

	// Attack traffic while the handshake is still incomplete.
	if rng.Intn(2) == 0 {
		pkts = append(pkts, g.maybeAttack(rng)...)
	}

	// Established phase: data in both directions plus attacks.
	rounds := 5 + rng.Intn(8)
	for i := 0; i < rounds; i++ {
		if rng.Intn(2) == 0 {
			pkts = append(pkts, g.sendData(a, b, 1+rng.Intn(100)))
			pkts = append(pkts, g.sendAck(b, a))
		} else {
			pkts = append(pkts, g.sendData(b, a, 1+rng.Intn(100)))
			pkts = append(pkts, g.sendAck(a, b))
		}
		pkts = append(pkts, g.maybeAttack(rng)...)
	}

	// Close phase, sometimes skipped to keep long-lived established state.
	if rng.Intn(5) != 0 {
		closeMode := rng.Intn(3)
		switch closeMode {
		case 0: // orderly close by a
			pkts = append(pkts, g.sendFin(a, b))
			pkts = append(pkts, g.sendAck(b, a))
			pkts = append(pkts, g.sendFin(b, a))
			pkts = append(pkts, g.sendAck(a, b))
		case 1: // simultaneous close
			pkts = append(pkts, g.sendFin(a, b))
			pkts = append(pkts, g.sendFin(b, a))
			pkts = append(pkts, g.sendAck(a, b))
			pkts = append(pkts, g.sendAck(b, a))
		case 2: // half close then attacker noise
			pkts = append(pkts, g.sendFin(a, b))
			pkts = append(pkts, g.sendAck(b, a))
			pkts = append(pkts, g.maybeAttack(rng)...)
			pkts = append(pkts, g.sendData(b, a, 10))
			pkts = append(pkts, g.sendAck(a, b))
			pkts = append(pkts, g.sendFin(b, a))
			pkts = append(pkts, g.sendAck(a, b))
		}
		pkts = append(pkts, g.maybeAttack(rng)...)
	}

	// A sprinkling of illegal flag combinations and unknown-flow packets.
	if rng.Intn(2) == 0 {
		pkts = append(pkts, Packet{
			SrcIP: a.ip, DstIP: b.ip, SrcPort: a.port, DstPort: b.port,
			Flags: FlagSYN | FlagRST,
		})
	}
	return pkts
}

func (g *generator) maybeAttack(rng *rand.Rand) []Packet {
	n := rng.Intn(4)
	out := make([]Packet, 0, n)
	for i := 0; i < n; i++ {
		src, dst := g.a, g.b
		if rng.Intn(2) == 0 {
			src, dst = g.b, g.a
		}
		var flags uint8
		switch rng.Intn(4) {
		case 0:
			flags = FlagRST
		case 1:
			flags = FlagRST | FlagACK
		case 2:
			flags = FlagSYN
		default:
			flags = FlagACK
		}
		out = append(out, g.attack(rng, &src, &dst, flags))
	}
	return out
}

func TestDifferentialAgainstReference(t *testing.T) {
	const trials = 500
	for trial := 0; trial < trials; trial++ {
		seed := int64(0x5151_0000 + trial)
		rng := rand.New(rand.NewSource(seed))
		gen := newGenerator(rng, trial%2 == 0)
		pkts := gen.trace(rng)

		got := NewTable()
		want := newRefTable()
		for i, p := range pkts {
			v1 := got.ProcessAt(p, time.Unix(int64(i), 0))
			v2 := want.process(p)
			if v1 != v2 {
				t.Fatalf("seed=%d packet=%d/%d %s flags=%s seq=%d ack=%d payload=%d: got %s want %s",
					seed, i, len(pkts), flowName(p), flagName(p.Flags),
					p.Seq, p.Ack, p.Payload, verdictName(v1), verdictName(v2))
			}
		}
	}
}

func flowName(p Packet) string {
	return fmt.Sprintf("%s:%d->%s:%d", p.SrcIP, p.SrcPort, p.DstIP, p.DstPort)
}

func flagName(f uint8) string {
	names := []struct {
		bit  uint8
		name string
	}{
		{FlagFIN, "FIN"}, {FlagSYN, "SYN"}, {FlagRST, "RST"},
		{FlagPSH, "PSH"}, {FlagACK, "ACK"}, {FlagURG, "URG"},
	}
	s := ""
	for _, n := range names {
		if f&n.bit != 0 {
			if s != "" {
				s += "|"
			}
			s += n.name
		}
	}
	if s == "" {
		s = "NONE"
	}
	return s
}

func verdictName(v Verdict) string {
	switch v {
	case VerdictAccept:
		return "ACCEPT"
	case VerdictDrop:
		return "DROP"
	case VerdictChallengeACK:
		return "CHALLENGE_ACK"
	default:
		return "UNKNOWN"
	}
}
