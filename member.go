// Package gracefulleave implements a graceful-leave protocol with a
// quiescent-period broadcast: a node announces its intent to leave, peers
// mark it Leaving and stop routing new traffic to it, in-flight RPCs are
// allowed to drain, and only after the quiescent timer expires is the
// member's routing entry physically destroyed.
package gracefulleave

import (
	"sync/atomic"
	"time"
)

// State is the lifecycle state of a cluster member.
type State int32

const (
	// StateAlive means the member is ready to receive new traffic.
	StateAlive State = iota
	// StateLeaving means the member announced a leave: it is excluded from
	// the ready routing pool but still serves in-flight requests until the
	// quiescent period expires.
	StateLeaving
)

func (s State) String() string {
	switch s {
	case StateAlive:
		return "Alive"
	case StateLeaving:
		return "Leaving"
	default:
		return "Unknown"
	}
}

// Member is a single routing-table entry as seen by a peer.
type Member struct {
	ID   string
	Addr string

	state          atomic.Int32
	inflight       atomic.Int64
	leavingSinceNs atomic.Int64
}

// NewMember creates a member entry in StateAlive.
func NewMember(id, addr string) *Member {
	m := &Member{ID: id, Addr: addr}
	m.state.Store(int32(StateAlive))
	return m
}

// State returns the current lifecycle state.
func (m *Member) State() State { return State(m.state.Load()) }

// InFlight returns the number of RPCs currently being served for this member.
func (m *Member) InFlight() int64 { return m.inflight.Load() }

// LeavingSince reports when the member entered StateLeaving. The zero value
// is returned while the member is still alive.
func (m *Member) LeavingSince() time.Time {
	ns := m.leavingSinceNs.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

func (m *Member) markLeaving() bool {
	if !m.state.CompareAndSwap(int32(StateAlive), int32(StateLeaving)) {
		return false
	}
	m.leavingSinceNs.Store(time.Now().UnixNano())
	return true
}

func (m *Member) beginCall() { m.inflight.Add(1) }
func (m *Member) endCall()   { m.inflight.Add(-1) }
