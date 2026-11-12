package netsec

import "fmt"

// BPDUType identifies the kind of BPDU carried on the wire.
type BPDUType uint8

const (
	BPDUTypeConfig BPDUType = 0x00
	BPDUTypeTCN    BPDUType = 0x80
)

// BridgeID is an 8-byte STP bridge identifier: 16-bit priority
// (including VLAN extension) followed by a 48-bit MAC address.
// Lower values are more preferred.
type BridgeID struct {
	Priority uint16
	MAC      [6]byte
}

// Less reports whether b is more preferred than o.
func (b BridgeID) Less(o BridgeID) bool {
	if b.Priority != o.Priority {
		return b.Priority < o.Priority
	}
	for i := range b.MAC {
		if b.MAC[i] != o.MAC[i] {
			return b.MAC[i] < o.MAC[i]
		}
	}
	return false
}

func (b BridgeID) String() string {
	return fmt.Sprintf("%04x:%02x:%02x:%02x:%02x:%02x:%02x",
		b.Priority, b.MAC[0], b.MAC[1], b.MAC[2], b.MAC[3], b.MAC[4], b.MAC[5])
}

// PortID is a 2-byte port identifier: 8-bit priority then port number.
// Lower values are more preferred.
type PortID struct {
	Priority uint8
	Number   uint8
}

// Less reports whether p is more preferred than o.
func (p PortID) Less(o PortID) bool {
	if p.Priority != o.Priority {
		return p.Priority < o.Priority
	}
	return p.Number < o.Number
}

// BPDU is a decoded spanning-tree BPDU presented to the receive path.
type BPDU struct {
	Type         BPDUType
	RootID       BridgeID // claimed root bridge
	RootPathCost uint32   // cost to reach the claimed root
	SenderID     BridgeID // bridge that transmitted this BPDU
	PortID       PortID   // sender's originating port
	MessageAge   uint16   // seconds since the root originated the info
	MaxAge       uint16   // seconds after which the info is stale
}

// PriorityVector is the STP comparison tuple extracted from a BPDU:
// (RootID, RootPathCost, SenderID, PortID). Compared lexicographically,
// lower is better ("superior").
type PriorityVector struct {
	RootID       BridgeID
	RootPathCost uint32
	SenderID     BridgeID
	PortID       PortID
}

// Vector extracts the priority vector from a BPDU.
func (b BPDU) Vector() PriorityVector {
	return PriorityVector{
		RootID:       b.RootID,
		RootPathCost: b.RootPathCost,
		SenderID:     b.SenderID,
		PortID:       b.PortID,
	}
}

// Compare orders two priority vectors: a vector is superior when its
// root bridge ID is lower; ties break on root path cost, then sender
// bridge ID, then sender port ID. Returns -1, 0 or +1.
func (v PriorityVector) Compare(o PriorityVector) int {
	switch {
	case v.RootID.Less(o.RootID):
		return -1
	case o.RootID.Less(v.RootID):
		return 1
	}
	if v.RootPathCost != o.RootPathCost {
		if v.RootPathCost < o.RootPathCost {
			return -1
		}
		return 1
	}
	switch {
	case v.SenderID.Less(o.SenderID):
		return -1
	case o.SenderID.Less(v.SenderID):
		return 1
	}
	switch {
	case v.PortID.Less(o.PortID):
		return -1
	case o.PortID.Less(v.PortID):
		return 1
	}
	return 0
}

// Superior reports whether v is strictly more preferred than o.
func (v PriorityVector) Superior(o PriorityVector) bool {
	return v.Compare(o) < 0
}
