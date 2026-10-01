package main

import "fmt"

// Dir identifies one of the two independent sequence spaces of a connection.
type Dir int

const (
	C2S Dir = iota // client -> server
	S2C            // server -> client
)

func (d Dir) String() string {
	if d == C2S {
		return "c->s"
	}
	return "s->c"
}

// Tuple is the connection 4-tuple. Direction is derived from the endpoints:
// packets matching client->server are C2S, the reverse are S2C.
type Tuple struct {
	ClientIP   string
	ClientPort int
	ServerIP   string
	ServerPort int
}

func (t Tuple) String() string {
	return fmt.Sprintf("%s:%d <-> %s:%d", t.ClientIP, t.ClientPort, t.ServerIP, t.ServerPort)
}

// Packet is a single observed segment in the discrete-event model.
// Epoch is the connection incarnation number assigned by the sender side of
// the simulation; the observer uses it as a guard so segments from an old
// incarnation of the same 4-tuple can never mix into a new one.
type Packet struct {
	ConnID  string
	Tuple   Tuple
	Dir     Dir
	Seq     int
	Payload []byte
	SYN     bool
	FIN     bool
	RST     bool
	Epoch   int
}

func (p Packet) describe() string {
	kind := "DATA"
	switch {
	case p.SYN:
		kind = "SYN"
	case p.RST:
		kind = "RST"
	case p.FIN:
		kind = "FIN"
	}
	return fmt.Sprintf("%s %s %s seq=%d len=%d epoch=%d",
		p.ConnID, p.Dir, kind, p.Seq, len(p.Payload), p.Epoch)
}

// Command is a script-level send operation scheduled at an integer tick.
type Command struct {
	Tick     int
	Kind     string // SYN, DATA, FIN, RST
	Conn     string
	Dir      Dir
	Seq      int
	HasSeq   bool
	Payload  []byte
	Delay    int
	HasDelay bool
}
