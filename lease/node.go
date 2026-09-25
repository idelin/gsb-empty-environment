package lease

import (
	"errors"
	"time"

	"leasefencing/sim"
)

var (
	ErrNotLeader    = errors.New("not leader")
	ErrLeaseExpired = errors.New("lease expired")
)

type Role int

const (
	Follower Role = iota
	Candidate
	Leader
)

func (r Role) String() string {
	switch r {
	case Candidate:
		return "candidate"
	case Leader:
		return "leader"
	default:
		return "follower"
	}
}

type Node struct {
	id            string
	sim           *sim.Simulator
	coordinator   string
	resource      string
	clock         sim.Clock
	maxClockError time.Duration
	ttl           time.Duration
	renewEvery    time.Duration
	retryEvery    time.Duration

	role              Role
	term              uint64
	leaseExpiresLocal time.Time
	nextRequestID     uint64
	pending           map[uint64]string
	lastTerm          uint64
	lastFence         uint64
	lastRejected      string
}

func NewNode(id string, s *sim.Simulator, coordinator, resource string, clock sim.Clock, ttl, maxClockError time.Duration) *Node {
	renewEvery := ttl / 6
	if renewEvery <= 0 {
		renewEvery = 50 * time.Millisecond
	}
	retryEvery := ttl / 4
	if retryEvery <= 0 {
		retryEvery = 100 * time.Millisecond
	}
	return &Node{
		id:            id,
		sim:           s,
		coordinator:   coordinator,
		resource:      resource,
		clock:         clock,
		maxClockError: maxClockError,
		ttl:           ttl,
		renewEvery:    renewEvery,
		retryEvery:    retryEvery,
		role:          Follower,
		pending:       make(map[uint64]string),
	}
}

func (n *Node) Campaign() {
	if n.role == Leader {
		return
	}
	n.role = Candidate
	n.sim.Send(n.id, n.coordinator, CampaignRequest{Candidate: n.id})
	n.sim.SendAfter(n.retryEvery, n.id, n.id, timerMsg{kind: campaignTimer})
}

func (n *Node) RequestWrite(value string) error {
	if n.role != Leader {
		return ErrNotLeader
	}
	if !n.clock.Now().Before(n.leaseExpiresLocal) {
		n.stepDown()
		return ErrLeaseExpired
	}
	n.nextRequestID++
	id := n.nextRequestID
	n.pending[id] = value
	n.sim.Send(n.id, n.coordinator, TokenRequest{Node: n.id, Term: n.term, RequestID: id})
	return nil
}

func (n *Node) Receive(msg interface{}) {
	switch m := msg.(type) {
	case CampaignResponse:
		n.handleCampaignResponse(m)
	case RenewResponse:
		n.handleRenewResponse(m)
	case TokenResponse:
		n.handleTokenResponse(m)
	case WriteResponse:
		if !m.Accepted {
			n.lastRejected = m.Reason
		}
	case timerMsg:
		n.handleTimer(m)
	}
}

func (n *Node) handleCampaignResponse(m CampaignResponse) {
	if n.role != Candidate {
		return
	}
	if !m.Granted {
		return
	}
	n.role = Leader
	n.term = m.Term
	n.leaseExpiresLocal = m.ExpiresAt.Add(-2 * n.maxClockError)
	n.sim.SendAfter(n.renewEvery, n.id, n.id, timerMsg{kind: renewTimer, term: n.term})
}

func (n *Node) handleRenewResponse(m RenewResponse) {
	if n.role != Leader || m.Term != n.term {
		return
	}
	if !m.Granted {
		n.stepDown()
		return
	}
	n.leaseExpiresLocal = m.ExpiresAt.Add(-2 * n.maxClockError)
}

func (n *Node) handleTokenResponse(m TokenResponse) {
	value, ok := n.pending[m.RequestID]
	if !ok {
		return
	}
	delete(n.pending, m.RequestID)
	if !m.Granted {
		n.lastRejected = m.Reason
		return
	}
	if n.role != Leader || m.Term != n.term {
		return
	}
	n.lastTerm = m.Term
	n.lastFence = m.Fence
	n.sim.Send(n.id, n.resource, WriteRequest{
		From:  n.id,
		Term:  m.Term,
		Fence: m.Fence,
		Value: value,
	})
}

func (n *Node) handleTimer(m timerMsg) {
	switch m.kind {
	case campaignTimer:
		if n.role != Candidate {
			return
		}
		n.sim.Send(n.id, n.coordinator, CampaignRequest{Candidate: n.id})
		n.sim.SendAfter(n.retryEvery, n.id, n.id, timerMsg{kind: campaignTimer})
	case renewTimer:
		if n.role != Leader || m.term != n.term {
			return
		}
		if !n.clock.Now().Before(n.leaseExpiresLocal) {
			n.stepDown()
			return
		}
		n.sim.Send(n.id, n.coordinator, RenewRequest{Node: n.id, Term: n.term})
		n.sim.SendAfter(n.renewEvery, n.id, n.id, timerMsg{kind: renewTimer, term: n.term})
	}
}

func (n *Node) stepDown() {
	n.role = Follower
	n.pending = make(map[uint64]string)
}

func (n *Node) ID() string {
	return n.id
}

func (n *Node) Role() Role {
	return n.role
}

func (n *Node) Term() uint64 {
	return n.term
}

func (n *Node) LastFence() uint64 {
	return n.lastFence
}

func (n *Node) LastTerm() uint64 {
	return n.lastTerm
}

func (n *Node) LastRejected() string {
	return n.lastRejected
}

func (n *Node) LeaseExpiresLocal() time.Time {
	return n.leaseExpiresLocal
}
