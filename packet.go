// Package streamsim reconstructs ordered byte streams from packets that
// traverse a discrete-event network model. All time is measured in integer
// ticks; no real network traffic is ever generated.
package streamsim

import "fmt"

// Direction identifies one of the two independent sequence spaces of a
// connection.
type Direction int

const (
	ClientToServer Direction = iota
	ServerToClient
)

func (d Direction) String() string {
	if d == ClientToServer {
		return "client->server"
	}
	return "server->client"
}

// Tuple is the connection 4-tuple in one packet's direction.
type Tuple struct {
	SrcIP   string
	SrcPort uint16
	DstIP   string
	DstPort uint16
}

func (t Tuple) String() string {
	return fmt.Sprintf("%s:%d>%s:%d", t.SrcIP, t.SrcPort, t.DstIP, t.DstPort)
}

// Reverse swaps source and destination.
func (t Tuple) Reverse() Tuple {
	return Tuple{SrcIP: t.DstIP, SrcPort: t.DstPort, DstIP: t.SrcIP, DstPort: t.SrcPort}
}

// flowKey is direction-independent so both ways of a connection map to the
// same flow.
func (t Tuple) flowKey() string {
	a := fmt.Sprintf("%s:%d", t.SrcIP, t.SrcPort)
	b := fmt.Sprintf("%s:%d", t.DstIP, t.DstPort)
	if a < b {
		return a + "|" + b
	}
	return b + "|" + a
}

// Packet is a single simulated segment. Epoch identifies the connection
// incarnation: packets from an older epoch must never be mixed into a newer
// connection that reuses the same 4-tuple.
type Packet struct {
	Tuple Tuple
	Epoch uint32
	Seq   uint32
	Data  []byte
	Syn   bool
	Fin   bool
}

func (p Packet) String() string {
	flags := ""
	if p.Syn {
		flags += "S"
	}
	if p.Fin {
		flags += "F"
	}
	return fmt.Sprintf("pkt{%s epoch=%d seq=%d len=%d %s}", p.Tuple, p.Epoch, p.Seq, len(p.Data), flags)
}
