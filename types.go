package netsec

import (
	"bytes"
	"cmp"
	"fmt"
	"time"
)

// BridgeID is a spanning-tree bridge identifier. Lower values are better.
//
// A BridgeID is ordered by priority first and by the MAC address second, which
// matches the wire encoding used by IEEE 802.1D.
type BridgeID struct {
	Priority uint16
	MAC      [6]byte
}

// Compare returns -1 if id is better than other, 0 if they are equal, and 1 if
// id is worse than other.
func (id BridgeID) Compare(other BridgeID) int {
	if c := cmp.Compare(id.Priority, other.Priority); c != 0 {
		return c
	}
	return bytes.Compare(id.MAC[:], other.MAC[:])
}

// Less reports whether id is a superior bridge identifier to other.
func (id BridgeID) Less(other BridgeID) bool {
	return id.Compare(other) < 0
}

func (id BridgeID) String() string {
	return fmt.Sprintf("%d:%02x:%02x:%02x:%02x:%02x:%02x",
		id.Priority,
		id.MAC[0], id.MAC[1], id.MAC[2],
		id.MAC[3], id.MAC[4], id.MAC[5],
	)
}

// PortID is a spanning-tree port identifier. Lower values are better.
//
// Priority is compared before Number so that the ordering matches the 16-bit
// port identifier carried in a BPDU.
type PortID struct {
	Priority uint16
	Number   uint16
}

// Compare returns -1 if id is better than other, 0 if they are equal, and 1 if
// id is worse than other.
func (id PortID) Compare(other PortID) int {
	if c := cmp.Compare(id.Priority, other.Priority); c != 0 {
		return c
	}
	return cmp.Compare(id.Number, other.Number)
}

// Less reports whether id is a superior port identifier to other.
func (id PortID) Less(other PortID) bool {
	return id.Compare(other) < 0
}

func (id PortID) String() string {
	return fmt.Sprintf("%d:%d", id.Priority, id.Number)
}

// PriorityVector is the four-tuple used by spanning tree to select a root
// port and a designated port. Fields are ordered from most to least
// significant.
type PriorityVector struct {
	RootID         BridgeID
	RootPathCost   uint32
	SenderBridgeID BridgeID
	SenderPortID   PortID
}

// Compare returns -1 if v is superior to other, 0 if the vectors are equal,
// and 1 if v is inferior to other. The comparison is lexicographic over root
// bridge ID, root path cost, sender bridge ID, and sender port ID.
func (v PriorityVector) Compare(other PriorityVector) int {
	if c := v.RootID.Compare(other.RootID); c != 0 {
		return c
	}
	if c := cmp.Compare(v.RootPathCost, other.RootPathCost); c != 0 {
		return c
	}
	if c := v.SenderBridgeID.Compare(other.SenderBridgeID); c != 0 {
		return c
	}
	return v.SenderPortID.Compare(other.SenderPortID)
}

// BetterThan reports whether v is strictly superior to other.
func (v PriorityVector) BetterThan(other PriorityVector) bool {
	return v.Compare(other) < 0
}

// Equal reports whether v and other contain the same priority vector.
func (v PriorityVector) Equal(other PriorityVector) bool {
	return v.Compare(other) == 0
}

// BPDUType identifies the kind of spanning-tree BPDU.
type BPDUType uint8

const (
	// ConfigBPDU is a configuration BPDU and carries a priority vector.
	ConfigBPDU BPDUType = 0x00
	// TCNBPDU is a topology change notification.
	TCNBPDU BPDUType = 0x80
)

func (t BPDUType) String() string {
	switch t {
	case ConfigBPDU:
		return "configuration"
	case TCNBPDU:
		return "TCN"
	default:
		return fmt.Sprintf("BPDU(%#x)", uint8(t))
	}
}

// BPDU is the subset of a spanning-tree BPDU needed by the receive-path
// guard. MaxAge may be zero, in which case GuardConfig.MaxAge is used.
type BPDU struct {
	Type BPDUType

	RootID         BridgeID
	RootPathCost   uint32
	SenderBridgeID BridgeID
	SenderPortID   PortID

	MessageAge time.Duration
	MaxAge     time.Duration
}

// PriorityVector returns the configuration BPDU's advertised vector.
func (b BPDU) PriorityVector() PriorityVector {
	return PriorityVector{
		RootID:         b.RootID,
		RootPathCost:   b.RootPathCost,
		SenderBridgeID: b.SenderBridgeID,
		SenderPortID:   b.SenderPortID,
	}
}

// IsTopologyChange reports whether the BPDU is a TCN.
func (b BPDU) IsTopologyChange() bool {
	return b.Type == TCNBPDU
}
